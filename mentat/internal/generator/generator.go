// Package generator uses a single LLM call per domain to produce SKILL.md
// documentation files under .agents/skills/{domain}/SKILL.md in the target
// repository. The LLM backend is reused from the classifier package.
package generator

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/frostyard/clix"
	"github.com/frostyard/firn/mentat/internal/classifier"
)

// defaultOutputDir is where SKILL.md files are written relative to repoPath.
const defaultOutputDir = ".agents/skills"

// maxSampleFiles is the maximum number of source files to include in the prompt.
const maxSampleFiles = 10

// maxSampleLines is the number of lines to read from each sampled file.
const maxSampleLines = 5

// sourceExtensions mirrors scanner.DefaultConfig().Extensions for file sampling.
var sourceExtensions = map[string]struct{}{
	".go": {}, ".ts": {}, ".js": {}, ".py": {}, ".sh": {},
	".rs": {}, ".rb": {}, ".java": {}, ".kt": {}, ".swift": {},
}

// Config controls generator behaviour. The Backend/Model/HTTPClient/OllamaBaseURL
// fields mirror classifier.Config and are forwarded to classifier.NewCaller.
type Config struct {
	// OutputDir is the root directory (relative to repoPath) where skill files
	// are written. Defaults to ".agents/skills".
	OutputDir string

	// Backend selects the LLM provider: "claude" | "openai" | "ollama".
	// Defaults to classifier.DefaultConfig().Backend when empty.
	Backend string

	// Model is an optional model-name override forwarded to the backend.
	Model string

	// Overwrite controls whether an existing SKILL.md is regenerated.
	// When false (default) a domain whose SKILL.md already exists is skipped.
	Overwrite bool

	// Logger is used for structured output. Defaults to slog.Default().
	Logger *slog.Logger

	// OllamaBaseURL overrides the Ollama base URL (forwarded to classifier).
	OllamaBaseURL string

	// HTTPClient is forwarded to classifier.Config for the openai/ollama
	// backends. Kept as interface{} to avoid importing net/http globally.
	HTTPClient interface{} // *http.Client
}

// Result describes the outcome of generating (or skipping) one domain.
type Result struct {
	// Domain is the short domain name (e.g. "auth").
	Domain string `json:"domain"`

	// Path is the absolute path that was written (or would have been written).
	Path string `json:"path"`

	// Skipped is true when the file already existed and Overwrite was false,
	// or when --dry-run is active.
	Skipped bool `json:"skipped"`
}

// Generate takes a single DomainResult, reads a sample of source files from
// the domain directory, calls the LLM to produce SKILL.md content, and writes
// it to OutputDir/{domain}/SKILL.md inside repoPath.
//
// It returns a Result describing what happened and any error.
func Generate(ctx context.Context, domain classifier.DomainResult, repoPath string, cfg Config) (Result, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	caller, err := classifier.NewCaller(classifier.Config{
		Backend:       cfg.Backend,
		Model:         cfg.Model,
		Logger:        log,
		OllamaBaseURL: cfg.OllamaBaseURL,
		HTTPClient:    cfg.HTTPClient,
	})
	if err != nil {
		return Result{}, fmt.Errorf("generator: creating LLM caller for domain %q: %w", domain.Name, err)
	}

	return generateWith(ctx, domain, repoPath, caller, log, cfg)
}

// GenerateWith is like Generate but accepts an explicit LLMCaller. Use this in
// tests to inject a mock without touching environment variables.
func GenerateWith(ctx context.Context, domain classifier.DomainResult, repoPath string, caller classifier.LLMCaller, log *slog.Logger, cfg Config) (Result, error) {
	if log == nil {
		log = slog.Default()
	}
	return generateWith(ctx, domain, repoPath, caller, log, cfg)
}

// generateWith is the shared implementation used by Generate and GenerateWith.
func generateWith(ctx context.Context, domain classifier.DomainResult, repoPath string, caller classifier.LLMCaller, log *slog.Logger, cfg Config) (Result, error) {
	outputDir := cfg.OutputDir
	if outputDir == "" {
		outputDir = defaultOutputDir
	}

	skillPath := filepath.Join(repoPath, outputDir, domain.Name, "SKILL.md")

	// Skip if the file already exists and Overwrite is false.
	if !cfg.Overwrite {
		if _, err := os.Stat(skillPath); err == nil {
			log.Info("generator: skipping existing SKILL.md", "domain", domain.Name, "path", skillPath)
			return Result{Domain: domain.Name, Path: skillPath, Skipped: true}, nil
		}
	}

	// Sample source files from the domain directory.
	domainDir := filepath.Join(repoPath, domain.Path)
	sample, err := sampleFiles(domainDir)
	if err != nil {
		// Non-fatal: proceed with an empty sample rather than aborting.
		log.Warn("generator: sampling files", "domain", domain.Name, "err", err)
		sample = ""
	}

	prompt := buildPrompt(domain, sample)

	log.Info("generator: calling LLM", "domain", domain.Name)

	raw, err := caller.Call(ctx, prompt)
	if err != nil {
		return Result{}, fmt.Errorf("generator: LLM call for domain %q: %w", domain.Name, err)
	}

	content := normaliseContent(raw)

	// If the LLM omitted the YAML frontmatter, inject a minimal one.
	if !strings.HasPrefix(content, "---\n") {
		header := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n", domain.Name, domain.Description)
		content = header + content
	}

	// Ensure the output directory exists.
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("generator: creating output dir for domain %q: %w", domain.Name, err)
	}

	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		return Result{}, fmt.Errorf("generator: writing SKILL.md for domain %q: %w", domain.Name, err)
	}

	log.Info("generator: wrote SKILL.md", "domain", domain.Name, "path", skillPath)
	return Result{Domain: domain.Name, Path: skillPath, Skipped: false}, nil
}

// GenerateAll runs Generate for each domain, respecting --dry-run via clix.DryRun.
// When dry-run is active it returns one Result per domain with Skipped=true and
// does not write any files. Returns one Result per domain.
func GenerateAll(ctx context.Context, domains []classifier.DomainResult, repoPath string, cfg Config) ([]Result, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	if clix.DryRun {
		log.Info("generator: dry-run active, skipping all writes", "domains", len(domains))
		results := make([]Result, len(domains))
		outputDir := cfg.OutputDir
		if outputDir == "" {
			outputDir = defaultOutputDir
		}
		for i, d := range domains {
			results[i] = Result{
				Domain:  d.Name,
				Path:    filepath.Join(repoPath, outputDir, d.Name, "SKILL.md"),
				Skipped: true,
			}
		}
		return results, nil
	}

	caller, err := classifier.NewCaller(classifier.Config{
		Backend:       cfg.Backend,
		Model:         cfg.Model,
		Logger:        log,
		OllamaBaseURL: cfg.OllamaBaseURL,
		HTTPClient:    cfg.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("generator: creating LLM caller: %w", err)
	}

	return generateAllWith(ctx, domains, repoPath, caller, log, cfg)
}

// GenerateAllWith is like GenerateAll but accepts an explicit LLMCaller.
// It does NOT check clix.DryRun — dry-run handling lives in GenerateAll.
func GenerateAllWith(ctx context.Context, domains []classifier.DomainResult, repoPath string, caller classifier.LLMCaller, log *slog.Logger, cfg Config) ([]Result, error) {
	if log == nil {
		log = slog.Default()
	}
	return generateAllWith(ctx, domains, repoPath, caller, log, cfg)
}

// generateAllWith is the shared implementation.
func generateAllWith(ctx context.Context, domains []classifier.DomainResult, repoPath string, caller classifier.LLMCaller, log *slog.Logger, cfg Config) ([]Result, error) {
	results := make([]Result, 0, len(domains))
	for _, d := range domains {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		r, err := generateWith(ctx, d, repoPath, caller, log, cfg)
		if err != nil {
			return results, fmt.Errorf("generator: domain %q: %w", d.Name, err)
		}
		results = append(results, r)
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Prompt construction
// ---------------------------------------------------------------------------

// promptExample is the one-shot SKILL.md example embedded in every prompt.
// Keeping it as a package-level constant avoids nesting raw-string literals
// (Go does not allow backtick inside a backtick raw string).
const promptExample = `---
name: queue
description: In-memory task queue with priority scheduling and dead-letter support.
---

## When to use

Call ` + "`queue.Enqueue`" + ` to schedule work; call ` + "`queue.Drain`" + ` in tests to flush synchronously.
Never access ` + "`Queue.items`" + ` directly — the field is unexported intentionally.

## Key invariant

` + "`maxRetries`" + ` is checked BEFORE re-enqueue. Exceeding it moves the item to the
dead-letter list, not back to the main queue. Forgetting this causes silent data loss.

## Entry point

Start at ` + "`queue.go`" + ` → ` + "`Queue.process`" + ` loop. The scheduler lives in ` + "`scheduler.go`" + `.
`

// buildPrompt assembles the LLM prompt for a single domain.
//
// Design rationale: concise over prescriptive. One concrete example beats five
// section headers. Only the frontmatter format is mandatory — structure of the
// markdown body is left to the model. Domain context is passed on a single line
// to reduce token count while keeping all signal.
func buildPrompt(domain classifier.DomainResult, fileSample string) string {
	langs := strings.Join(domain.Languages, ", ")
	if langs == "" {
		langs = "unknown"
	}

	var sb strings.Builder

	// Domain context — one dense line keeps token usage low.
	sb.WriteString(fmt.Sprintf("Domain: %s | Path: %s | Lang: %s | Files: %d\n", domain.Name, domain.Path, langs, domain.FileCount))
	sb.WriteString(fmt.Sprintf("Description: %s\n", domain.Description))

	if fileSample != "" {
		sb.WriteString("\nSource sample (filename + first lines):\n")
		sb.WriteString(fileSample)
	}

	// Task instruction.
	sb.WriteString("\nWrite a SKILL.md for an AI coding agent working in this domain.\n\n")
	sb.WriteString("A good SKILL.md is short and specific. It tells the agent only what it cannot\n")
	sb.WriteString("infer from reading the code: non-obvious invariants, tricky entry points,\n")
	sb.WriteString("load-bearing conventions, and concrete usage patterns. Prefer code snippets\n")
	sb.WriteString("over prose where they are clearer. Do not reproduce information that is\n")
	sb.WriteString("obvious from filenames, type names, or package comments.\n\n")

	// Frontmatter requirement.
	sb.WriteString("Required: the file MUST start with YAML frontmatter:\n\n")
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("name: %s\n", domain.Name))
	sb.WriteString("description: <one-sentence description>\n")
	sb.WriteString("---\n\n")

	// One-shot example.
	sb.WriteString("Example of a well-written SKILL.md (for a different domain — do not copy it verbatim):\n\n")
	sb.WriteString(promptExample)
	sb.WriteString("\nOutput ONLY the SKILL.md content — no preamble, no explanation, no markdown fences.\n")

	return sb.String()
}

// ---------------------------------------------------------------------------
// File sampling
// ---------------------------------------------------------------------------

// sampleFiles reads up to maxSampleFiles source files from dir and returns a
// formatted string with each filename followed by its first maxSampleLines lines.
func sampleFiles(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("reading dir %s: %w", dir, err)
	}

	var buf bytes.Buffer
	count := 0

	for _, e := range entries {
		if count >= maxSampleFiles {
			break
		}
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if _, ok := sourceExtensions[ext]; !ok {
			continue
		}

		lines, err := readFirstLines(filepath.Join(dir, e.Name()), maxSampleLines)
		if err != nil {
			// Skip unreadable files rather than aborting.
			continue
		}

		fmt.Fprintf(&buf, "### %s\n%s\n", e.Name(), lines)
		count++
	}

	return buf.String(), nil
}

// readFirstLines returns the first n lines of a file as a single string.
func readFirstLines(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	var sb strings.Builder
	sc := bufio.NewScanner(f)
	lineCount := 0

	for sc.Scan() && lineCount < n {
		sb.WriteString(sc.Text())
		sb.WriteByte('\n')
		lineCount++
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("scanning %s: %w", path, err)
	}
	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// Response normalisation
// ---------------------------------------------------------------------------

// normaliseContent strips markdown code fences and trailing stats lines that
// some LLM CLIs emit, then ensures the result ends with a single newline.
//
// If no YAML frontmatter is detected the caller (generateWith) injects a
// minimal one — that responsibility lives there, not here.
func normaliseContent(raw string) string {
	s := strings.TrimSpace(raw)

	// Strip trailing stats lines emitted by some CLI tools (e.g. copilot).
	// Patterns: "Changes +N -N" or "Requests N Premium (Ns)".
	lines := strings.Split(s, "\n")
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if strings.HasPrefix(last, "Changes") || strings.HasPrefix(last, "Requests") || last == "" {
			lines = lines[:len(lines)-1]
		} else {
			break
		}
	}
	s = strings.TrimSpace(strings.Join(lines, "\n"))

	// Strip outermost code fence ONLY when the entire response is wrapped in one
	// (first non-empty line starts with ```) — not when the content contains
	// internal code blocks inside valid SKILL.md content.
	if strings.HasPrefix(s, "```") {
		openIdx := 0
		closeIdx := strings.LastIndex(s, "```")
		if closeIdx > openIdx {
			start := strings.Index(s, "\n")
			if start != -1 {
				s = strings.TrimSpace(s[start+1 : closeIdx])
			}
		}
	}

	return s + "\n"
}
