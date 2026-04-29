// Package worker implements the pipeline issue-worker: given a merged spec PR,
// it checks the PR throttle, calls an LLM to produce an implementation
// summary, and opens a draft "agent-pr" on the target repository.
//
// GHRunner and LLMCaller follow the same interface-injection pattern used by
// the watcher and specgen packages so the unit tests never touch the network
// or the real gh binary.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/frostyard/firn/pipeline/internal/specgen"
)

// GHRunner abstracts the gh CLI invocation. Inject a fake in tests; production
// code uses ExecGHRunner.
type GHRunner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// ExecGHRunner is the production GHRunner that delegates to the real gh binary.
type ExecGHRunner struct{}

// Run executes `gh <args...>` and returns its combined stdout.
func (ExecGHRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh %v: %w", args, err)
	}
	return out, nil
}

// Config holds all configuration required by Process.
type Config struct {
	// Repo is the GitHub repository slug, e.g. "frostyard/snosi". Required.
	Repo string

	// MaxConcurrentPRs is the PR throttle: if this many "agent-pr" labelled
	// PRs are already open, Process returns Status "skipped_throttled".
	// Defaults to 3 when zero.
	MaxConcurrentPRs int

	// CIFixerMaxAttempts is the max number of CI-fixer retry loops. The
	// implementation is deferred — this field is stored and surfaced for
	// wiring verification.
	// Defaults to 3 when zero.
	CIFixerMaxAttempts int

	// DraftFirst controls whether the impl PR is opened as a draft. When
	// false the PR is opened as ready-for-review immediately.
	// Defaults to true when the zero value would be ambiguous — callers
	// should set it explicitly from the pipeline config.
	DraftFirst bool

	// Backend selects the LLM provider: "claude" | "openai" | "ollama".
	// When empty, specgen.DefaultConfig() auto-detection is used.
	Backend string

	// DryRun, when true, skips all PR creation and returns Status "dry_run".
	DryRun bool

	// Log is the structured logger. Defaults to slog.Default() when nil.
	Log *slog.Logger

	// GHRunner overrides gh CLI invocation (for tests).
	GHRunner GHRunner

	// LLMCaller overrides the LLM backend entirely (for tests).
	// When non-nil, Backend is ignored.
	LLMCaller specgen.LLMCaller
}

// WorkResult describes the output of a successful Process call.
type WorkResult struct {
	// IssueNumber is the GitHub issue this work targets.
	IssueNumber int

	// SpecDir is the directory of the merged spec, e.g. "specs/GH42".
	SpecDir string

	// PRNumber is the newly opened impl PR number. Zero when not opened.
	PRNumber int

	// PRUrl is the URL of the opened impl PR. Empty when not opened.
	PRUrl string

	// Status is one of: "opened", "skipped_throttled", "dry_run".
	Status string
}

// Process takes a SpecResult (a merged spec), checks the PR throttle, calls
// an LLM to generate an implementation summary, and opens a draft impl PR.
//
// Process returns WorkResult with Status:
//   - "dry_run"           — DryRun was true; no PR was created.
//   - "skipped_throttled" — open agent-pr count >= MaxConcurrentPRs.
//   - "opened"            — impl draft PR was successfully created.
func Process(ctx context.Context, spec specgen.SpecResult, repoPath string, cfg Config) (WorkResult, error) {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}

	if cfg.MaxConcurrentPRs <= 0 {
		cfg.MaxConcurrentPRs = 3
	}
	if cfg.CIFixerMaxAttempts <= 0 {
		cfg.CIFixerMaxAttempts = 3
	}

	runner := cfg.GHRunner
	if runner == nil {
		runner = ExecGHRunner{}
	}

	result := WorkResult{
		IssueNumber: spec.IssueNumber,
		SpecDir:     spec.SpecDir,
	}

	// Dry-run: skip all side effects.
	if cfg.DryRun {
		log.Info("worker: dry-run — skipping PR creation",
			"issue", spec.IssueNumber,
			"spec_dir", spec.SpecDir,
			"ci_fixer_max_attempts", cfg.CIFixerMaxAttempts,
		)
		result.Status = "dry_run"
		return result, nil
	}

	// 1. Count currently open agent PRs.
	count, err := CountOpenAgentPRs(ctx, cfg.Repo, runner)
	if err != nil {
		return WorkResult{}, fmt.Errorf("worker: counting open agent PRs: %w", err)
	}

	// 2. Throttle check.
	if count >= cfg.MaxConcurrentPRs {
		log.Warn("worker: PR throttle reached — skipping",
			"open_prs", count,
			"max", cfg.MaxConcurrentPRs,
			"issue", spec.IssueNumber,
		)
		result.Status = "skipped_throttled"
		return result, nil
	}

	// 3. Resolve repoPath.
	if repoPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return WorkResult{}, fmt.Errorf("worker: getting working directory: %w", err)
		}
		repoPath = cwd
	}

	// 4. Read merged spec files.
	productBytes, err := os.ReadFile(filepath.Join(repoPath, spec.ProductPath))
	if err != nil {
		return WorkResult{}, fmt.Errorf("worker: reading product.md at %s: %w", spec.ProductPath, err)
	}

	techBytes, err := os.ReadFile(filepath.Join(repoPath, spec.TechPath))
	if err != nil {
		return WorkResult{}, fmt.Errorf("worker: reading tech.md at %s: %w", spec.TechPath, err)
	}

	// 5. Resolve LLM caller.
	caller := cfg.LLMCaller
	if caller == nil {
		sgCfg := specgen.DefaultConfig()
		if cfg.Backend != "" {
			sgCfg.Backend = cfg.Backend
		}
		built, err := specgen.NewLLMCaller(sgCfg)
		if err != nil {
			return WorkResult{}, fmt.Errorf("worker: creating LLM caller: %w", err)
		}
		caller = built
	}

	// 6. Call LLM to generate an implementation summary for the PR body.
	log.Info("worker: generating implementation summary", "issue", spec.IssueNumber)
	implSummary, err := generateImplSummary(ctx, caller, spec.IssueNumber, string(productBytes), string(techBytes))
	if err != nil {
		return WorkResult{}, fmt.Errorf("worker: generating implementation summary for GH%d: %w", spec.IssueNumber, err)
	}

	// 7. Derive PR title from product.md heading.
	prTitle := fmt.Sprintf("impl: GH%d %s", spec.IssueNumber, extractTitle(string(productBytes)))

	// 8. Open draft impl PR.
	log.Info("worker: opening impl PR", "title", prTitle, "draft", cfg.DraftFirst)
	prURL, prNumber, err := openImplPR(ctx, runner, cfg.Repo, prTitle, implSummary, spec, cfg.DraftFirst)
	if err != nil {
		return WorkResult{}, fmt.Errorf("worker: opening impl PR for GH%d: %w", spec.IssueNumber, err)
	}

	result.PRNumber = prNumber
	result.PRUrl = prURL
	result.Status = "opened"

	log.Info("worker: impl PR opened",
		"issue", spec.IssueNumber,
		"pr_number", prNumber,
		"pr_url", prURL,
	)

	return result, nil
}

// CountOpenAgentPRs returns the number of open PRs carrying the "agent-pr"
// label. It calls:
//
//	gh pr list --repo <repo> --label agent-pr --json number
func CountOpenAgentPRs(ctx context.Context, repo string, runner GHRunner) (int, error) {
	out, err := runner.Run(ctx,
		"pr", "list",
		"--repo", repo,
		"--label", "agent-pr",
		"--json", "number",
	)
	if err != nil {
		return 0, fmt.Errorf("listing agent PRs for %s: %w", repo, err)
	}

	type prEntry struct {
		Number int `json:"number"`
	}
	var prs []prEntry
	if err := json.Unmarshal(out, &prs); err != nil {
		return 0, fmt.Errorf("parsing agent PR list for %s: %w", repo, err)
	}
	return len(prs), nil
}

// generateImplSummary calls the LLM to produce a PR description from the
// merged spec files.
func generateImplSummary(ctx context.Context, caller specgen.LLMCaller, issueNum int, productMD, techMD string) (string, error) {
	prompt := fmt.Sprintf(`You are an agent that has just been handed a merged spec for a GitHub issue. Write a concise pull-request description (3-5 sentences) summarising what will be implemented, which files will change, and how the behavioral invariants will be verified. Do not include implementation detail beyond what is in the tech spec. Output only the PR body text with no preamble.

Issue: GH#%d

--- product.md ---
%s

--- tech.md ---
%s
`, issueNum, productMD, techMD)

	result, err := caller.Call(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM call for impl summary: %w", err)
	}
	return strings.TrimSpace(result), nil
}

// openImplPR opens the implementation draft PR and returns (url, number, error).
func openImplPR(ctx context.Context, runner GHRunner, repo, title, body string, spec specgen.SpecResult, draft bool) (string, int, error) {
	args := []string{
		"pr", "create",
		"--repo", repo,
		"--title", title,
		"--body", body,
		"--label", "agent-pr",
	}
	if draft {
		args = append(args, "--draft")
	}

	out, err := runner.Run(ctx, args...)
	if err != nil {
		return "", 0, fmt.Errorf("gh pr create: %w", err)
	}

	prURL := strings.TrimSpace(string(out))
	prNumber := parsePRNumber(prURL)
	return prURL, prNumber, nil
}

// extractTitle returns the text of the first Markdown heading line in content,
// or an empty string when none is found.
func extractTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return ""
}

// parsePRNumber extracts the integer at the end of a GitHub PR URL.
// Returns 0 when the URL cannot be parsed.
func parsePRNumber(prURL string) int {
	parts := strings.Split(strings.TrimRight(prURL, "/"), "/")
	if len(parts) == 0 {
		return 0
	}
	last := parts[len(parts)-1]
	n := 0
	for _, ch := range last {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}
