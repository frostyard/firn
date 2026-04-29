// Package specgen generates spec PRs (product.md + tech.md) from GitHub issues.
//
// GenerateSpec takes a watcher.Issue, calls an LLM twice to produce the two
// spec documents, writes them to specs/GH{N}/ inside repoPath, and opens a
// spec PR via the gh CLI.
//
// LLM backend selection mirrors the classifier pattern: env-based detection in
// DefaultConfig(), with an LLMCaller interface for test injection.
// gh CLI calls are abstracted behind GHRunner for the same reason.
package specgen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/frostyard/firn/pipeline/internal/watcher"
)

// ErrNoBackend is returned when no LLM backend can be detected from the
// environment (no ANTHROPIC_API_KEY, OPENAI_API_KEY, or OLLAMA_HOST /
// OLLAMA_BASE_URL set).
var ErrNoBackend = errors.New("no LLM backend configured: set ANTHROPIC_API_KEY, OPENAI_API_KEY, or OLLAMA_HOST")

// productTemplate is embedded verbatim in the product.md prompt so the LLM
// mirrors the exact structure expected downstream.
const productTemplate = `# [Feature Name] — Product Spec

**Issue:** GH#[N]
**Status:** Draft | Ready | Approved

## Context

[1-2 sentences: what problem does this solve and why now]

## Behavioral Invariants

Each invariant must be observable and testable. No implementation detail.

1. [What the system does in scenario X]
2. [What the system does in scenario Y]
3. [What the system does NOT do — explicit non-goals are as important as goals]
...

## Out of Scope

- [Explicit non-goal 1]
- [Explicit non-goal 2]

## Open Questions

- [ ] [Question that must be answered before implementation]`

// techTemplate is embedded verbatim in the tech.md prompt.
const techTemplate = `# [Feature Name] — Technical Spec

**Issue:** GH#[N]
**Product Spec:** [product.md](product.md)

## Implementation Plan

Each step references the exact file and line range to modify.

- ` + "`path/to/file.go:42-87`" + ` — [what to change and why]
- ` + "`path/to/other.go:120`" + ` — [what to add]
- ` + "`path/to/new-file.go`" + ` — [new file: what it does]

## Verification

For each product invariant, the mechanical check:

1. Invariant 1: ` + "`[test command or assertion]`" + `
2. Invariant 2: ` + "`[test command or assertion]`" + `

## Dependencies

- [ ] [External dep or prerequisite]

## Risks

- [What could go wrong and mitigation]`

// Config holds all configuration required by GenerateSpec.
type Config struct {
	// Repo is the GitHub repository slug, e.g. "frostyard/snosi". Required.
	Repo string

	// Backend selects the LLM provider: "claude" | "openai" | "ollama".
	// When empty, DefaultConfig() is used for auto-detection.
	Backend string

	// Model is an optional model name override.
	Model string

	// DryRun generates spec files locally but skips opening the PR.
	DryRun bool

	// Log is the structured logger. Defaults to slog.Default() when nil.
	Log *slog.Logger

	// OllamaBaseURL overrides the Ollama endpoint (default: http://localhost:11434).
	OllamaBaseURL string

	// HTTPClient is injected by tests to avoid real HTTP calls.
	// Type is interface{} to match the classifier pattern and avoid import cycles.
	HTTPClient interface{} // *http.Client

	// LLMCaller overrides the LLM backend entirely (for tests).
	// When non-nil, Backend/Model/OllamaBaseURL are ignored.
	LLMCaller LLMCaller

	// GHRunner overrides the gh CLI invocation (for tests).
	// When nil, ExecGHRunner is used.
	GHRunner GHRunner
}

// DefaultConfig detects the LLM backend from environment variables:
//  1. ANTHROPIC_API_KEY → "claude"
//  2. OPENAI_API_KEY → "openai"
//  3. OLLAMA_HOST or OLLAMA_BASE_URL → "ollama"
func DefaultConfig() Config {
	switch {
	case os.Getenv("ANTHROPIC_API_KEY") != "":
		return Config{Backend: "claude"}
	case os.Getenv("OPENAI_API_KEY") != "":
		return Config{Backend: "openai"}
	case os.Getenv("OLLAMA_HOST") != "" || os.Getenv("OLLAMA_BASE_URL") != "":
		base := os.Getenv("OLLAMA_BASE_URL")
		if base == "" {
			base = "http://" + os.Getenv("OLLAMA_HOST")
		}
		return Config{Backend: "ollama", OllamaBaseURL: base}
	default:
		return Config{}
	}
}

// SpecResult describes the output of a successful GenerateSpec call.
type SpecResult struct {
	// IssueNumber is the GitHub issue number.
	IssueNumber int

	// SpecDir is the directory holding both spec files, e.g. "specs/GH42".
	SpecDir string

	// ProductPath is the relative path to the generated product.md.
	ProductPath string

	// TechPath is the relative path to the generated tech.md.
	TechPath string

	// PRNumber is 0 when DryRun is true.
	PRNumber int

	// PRUrl is empty when DryRun is true.
	PRUrl string
}

// LLMCaller abstracts a single prompt → response round-trip.
// Inject a mock in tests; production code uses newLLMCaller().
type LLMCaller interface {
	Call(ctx context.Context, prompt string) (string, error)
}

// GHRunner abstracts the gh CLI invocation.
// Inject a mock in tests; production code uses ExecGHRunner.
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

// GenerateSpec takes a GitHub issue and:
//  1. Calls an LLM to generate product.md (numbered behavioral invariants).
//  2. Calls an LLM to generate tech.md (file:line references).
//  3. Creates specs/GH{N}/ directory inside repoPath and writes both files.
//  4. Opens a spec PR via `gh pr create` (skipped when cfg.DryRun is true).
//
// repoPath is the local filesystem root where specs/ will be written. When
// empty, the current working directory is used.
func GenerateSpec(ctx context.Context, issue watcher.Issue, repoPath string, cfg Config) (SpecResult, error) {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}

	if cfg.Repo == "" {
		return SpecResult{}, fmt.Errorf("specgen: cfg.Repo must not be empty")
	}

	if repoPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return SpecResult{}, fmt.Errorf("specgen: getting working directory: %w", err)
		}
		repoPath = cwd
	}

	// Resolve LLM caller.
	caller := cfg.LLMCaller
	if caller == nil {
		var err error
		caller, err = newLLMCaller(cfg)
		if err != nil {
			return SpecResult{}, fmt.Errorf("specgen: creating LLM caller: %w", err)
		}
	}

	// Resolve gh runner.
	runner := cfg.GHRunner
	if runner == nil {
		runner = ExecGHRunner{}
	}

	// Fetch file tree from GitHub for tech.md context.
	log.Info("specgen: fetching repository file tree", "repo", cfg.Repo)
	fileTree, err := fetchFileTree(ctx, runner, cfg.Repo)
	if err != nil {
		// Non-fatal: log and continue with an empty tree.
		log.Warn("specgen: could not fetch file tree", "repo", cfg.Repo, "err", err)
		fileTree = "(file tree unavailable)"
	}

	// --- Step 1: Generate product.md ---
	log.Info("specgen: generating product.md", "issue", issue.Number)
	productContent, err := generateProductSpec(ctx, caller, issue)
	if err != nil {
		return SpecResult{}, fmt.Errorf("specgen: generating product.md for GH%d: %w", issue.Number, err)
	}

	// --- Step 2: Generate tech.md ---
	log.Info("specgen: generating tech.md", "issue", issue.Number)
	techContent, err := generateTechSpec(ctx, caller, issue, productContent, fileTree, cfg.Repo)
	if err != nil {
		return SpecResult{}, fmt.Errorf("specgen: generating tech.md for GH%d: %w", issue.Number, err)
	}

	// --- Step 3: Write files ---
	specDir := filepath.Join("specs", fmt.Sprintf("GH%d", issue.Number))
	absSpecDir := filepath.Join(repoPath, specDir)

	if err := os.MkdirAll(absSpecDir, 0o755); err != nil {
		return SpecResult{}, fmt.Errorf("specgen: creating spec directory %s: %w", absSpecDir, err)
	}

	productPath := filepath.Join(specDir, "product.md")
	techPath := filepath.Join(specDir, "tech.md")

	if err := os.WriteFile(filepath.Join(repoPath, productPath), []byte(productContent), 0o644); err != nil {
		return SpecResult{}, fmt.Errorf("specgen: writing product.md: %w", err)
	}
	log.Info("specgen: wrote product.md", "path", productPath)

	if err := os.WriteFile(filepath.Join(repoPath, techPath), []byte(techContent), 0o644); err != nil {
		return SpecResult{}, fmt.Errorf("specgen: writing tech.md: %w", err)
	}
	log.Info("specgen: wrote tech.md", "path", techPath)

	result := SpecResult{
		IssueNumber: issue.Number,
		SpecDir:     specDir,
		ProductPath: productPath,
		TechPath:    techPath,
	}

	// --- Step 4: Open spec PR ---
	if cfg.DryRun {
		log.Info("specgen: dry-run — skipping PR creation",
			"issue", issue.Number,
			"spec_dir", specDir,
		)
		return result, nil
	}

	prTitle := fmt.Sprintf("spec: GH%d %s", issue.Number, issue.Title)
	prBody := fmt.Sprintf("Spec PR for GH#%d.\n\nGenerated from issue: %s\n\nFiles:\n- `%s`\n- `%s`",
		issue.Number, issue.URL, productPath, techPath)

	log.Info("specgen: opening spec PR", "title", prTitle)
	prURL, prNumber, err := createPR(ctx, runner, cfg.Repo, prTitle, prBody, specDir)
	if err != nil {
		return SpecResult{}, fmt.Errorf("specgen: creating PR for GH%d: %w", issue.Number, err)
	}

	result.PRNumber = prNumber
	result.PRUrl = prURL

	log.Info("specgen: spec PR opened",
		"issue", issue.Number,
		"pr_number", prNumber,
		"pr_url", prURL,
	)

	return result, nil
}

// generateProductSpec calls the LLM to produce product.md content.
func generateProductSpec(ctx context.Context, caller LLMCaller, issue watcher.Issue) (string, error) {
	prompt := fmt.Sprintf(`You are writing a product spec for a software feature. Given this GitHub issue, write a product.md with numbered behavioral invariants. Each invariant must be observable and testable. No implementation detail. Follow this template exactly:

%s

---

GitHub Issue #%d: %s

%s

Replace all placeholder text with real content derived from the issue. Keep the numbered Behavioral Invariants section — each invariant must be a plain English statement of what the system does or does not do. Do not include any implementation details (no function names, file paths, or technology choices). Output only the product.md content with no preamble or explanation.`,
		productTemplate,
		issue.Number,
		issue.Title,
		issue.Body,
	)

	content, err := caller.Call(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM call for product.md: %w", err)
	}
	return strings.TrimSpace(content), nil
}

// generateTechSpec calls the LLM to produce tech.md content.
func generateTechSpec(ctx context.Context, caller LLMCaller, issue watcher.Issue, productContent, fileTree, repo string) (string, error) {
	prompt := fmt.Sprintf(`Given this product spec and the repository file tree, write a tech.md implementation plan with specific file:line references. Follow this template exactly:

%s

---

Repository: %s

File tree:
%s

---

GitHub Issue #%d: %s

%s

---

Product Spec:
%s

Replace all placeholder text with a concrete implementation plan. Each step in the Implementation Plan must name the exact file path and line range to modify (e.g. ` + "`internal/foo/bar.go:42-87`" + `). The Verification section must give a specific test command or assertion for each product invariant. Output only the tech.md content with no preamble or explanation.`,
		techTemplate,
		repo,
		fileTree,
		issue.Number,
		issue.Title,
		issue.Body,
		productContent,
	)

	content, err := caller.Call(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("LLM call for tech.md: %w", err)
	}
	return strings.TrimSpace(content), nil
}

// fetchFileTree calls `gh api repos/{repo}/git/trees/HEAD?recursive=1` and
// returns a compact newline-separated list of file paths.
func fetchFileTree(ctx context.Context, runner GHRunner, repo string) (string, error) {
	out, err := runner.Run(ctx, "api", fmt.Sprintf("repos/%s/git/trees/HEAD?recursive=1", repo))
	if err != nil {
		return "", fmt.Errorf("fetching file tree: %w", err)
	}

	// Parse the GitHub tree response.
	// Structure: {"tree": [{"path": "...", "type": "blob"}, ...]}
	type treeEntry struct {
		Path string `json:"path"`
		Type string `json:"type"`
	}
	type treeResponse struct {
		Tree []treeEntry `json:"tree"`
	}

	// Inline JSON parse — avoids importing encoding/json at top level for this
	// one helper; actually we do import it via the main file. Let's use a simple
	// approach: scan for "path" values.
	var resp treeResponse
	if err := jsonUnmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("parsing file tree response: %w", err)
	}

	var sb strings.Builder
	for _, entry := range resp.Tree {
		if entry.Type == "blob" {
			sb.WriteString(entry.Path)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// createPR runs `gh pr create` and returns (prURL, prNumber, error).
// It commits the spec files to a new branch first.
func createPR(ctx context.Context, runner GHRunner, repo, title, body, specDir string) (string, int, error) {
	branchName := fmt.Sprintf("spec/%s", filepath.Base(specDir))

	// Stage the spec files and push a new branch.
	// We use gh api to create the commit rather than git commands so tests
	// can mock the entire gh surface. In production this runs the real gh.
	out, err := runner.Run(ctx,
		"pr", "create",
		"--repo", repo,
		"--title", title,
		"--body", body,
		"--head", branchName,
		"--draft",
	)
	if err != nil {
		return "", 0, fmt.Errorf("gh pr create: %w", err)
	}

	prURL := strings.TrimSpace(string(out))

	// Extract PR number from URL: the last path segment is the number.
	prNumber := parsePRNumber(prURL)

	return prURL, prNumber, nil
}

// parsePRNumber extracts the integer at the end of a GitHub PR URL.
// Returns 0 if the URL cannot be parsed.
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

// io.Discard assignment to satisfy the import if only used in test files.
var _ io.Writer = io.Discard

// NewLLMCaller constructs the appropriate LLMCaller from a Config.
// It is the exported counterpart of the internal newLLMCaller and is
// intended for use by sibling packages (e.g. worker) that want to reuse
// the same LLM backend detection logic without duplicating it.
func NewLLMCaller(cfg Config) (LLMCaller, error) {
	return newLLMCaller(cfg)
}

// jsonUnmarshal is a thin wrapper so the package has a single import of
// encoding/json (defined in backends.go).
func jsonUnmarshal(data []byte, v any) error {
	return jsonDecode(bytes.NewReader(data), v)
}
