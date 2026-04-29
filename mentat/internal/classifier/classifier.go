// Package classifier makes a single LLM call to classify scanner candidates
// into logical domains. The LLM backend is selected from environment variables;
// the LLMCaller interface is exposed so callers can inject a mock in tests.
package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/frostyard/firn/mentat/internal/scanner"
)

// ErrNoBackend is returned when no LLM backend can be detected from the
// environment (no ANTHROPIC_API_KEY, OPENAI_API_KEY, or OLLAMA_HOST /
// OLLAMA_BASE_URL set).
var ErrNoBackend = errors.New("no LLM backend configured: set ANTHROPIC_API_KEY, OPENAI_API_KEY, or OLLAMA_HOST")

// DomainResult describes a single logical domain identified by the classifier.
type DomainResult struct {
	// Name is the short domain name (e.g. "auth", "scanner", "config").
	Name string `json:"name"`

	// Path is the relative path from the repository root.
	Path string `json:"path"`

	// Description is a one-sentence summary of what this domain does.
	Description string `json:"description"`

	// FileCount is copied from the matching scanner.Candidate.
	FileCount int `json:"file_count"`

	// Languages holds the distinct languages detected from file extensions,
	// sourced from the matching scanner.Candidate.
	Languages []string `json:"languages"`
}

// Config controls which LLM backend is used.
type Config struct {
	// Backend selects the LLM provider: "claude" | "openai" | "ollama".
	Backend string

	// Model is an optional model name override.
	// Defaults per backend: claude → claude-3-5-haiku-20241022,
	// openai → gpt-4o-mini, ollama → llama3.
	Model string

	// Logger is used for structured output. Defaults to slog.Default().
	Logger *slog.Logger

	// HTTPClient is used by the openai and ollama backends. If nil, a
	// default *http.Client is created inside each backend. Exposed here so
	// callers can inject a test client without the package importing net/http
	// globally.
	HTTPClient interface{} // *http.Client — kept as interface{} to avoid import cycle in tests

	// OllamaBaseURL overrides the Ollama base URL (default: http://localhost:11434).
	OllamaBaseURL string
}

// DefaultConfig detects the backend from environment variables in order:
//  1. ANTHROPIC_API_KEY → "claude"
//  2. OPENAI_API_KEY → "openai"
//  3. OLLAMA_HOST or OLLAMA_BASE_URL → "ollama"
//
// Returns a Config with Backend="" if no relevant env var is set; Classify()
// will return ErrNoBackend in that case.
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

// LLMCaller abstracts a single prompt → response round-trip to an LLM.
// Inject a mock in tests; production code uses newCaller().
type LLMCaller interface {
	Call(ctx context.Context, prompt string) (string, error)
}

// Classify takes scanner candidates and makes one LLM call to identify which
// represent logical domains and to generate a one-sentence description for each.
// It returns one DomainResult per recognised domain. FileCount and Languages are
// populated from the original candidates, not from the LLM response.
func Classify(ctx context.Context, candidates []scanner.Candidate, cfg Config) ([]DomainResult, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	if len(candidates) == 0 {
		log.Info("classifier: no candidates, skipping LLM call")
		return nil, nil
	}

	caller, err := newCaller(cfg)
	if err != nil {
		return nil, err
	}

	return classify(ctx, candidates, caller, log)
}

// ClassifyWith is like Classify but accepts an explicit LLMCaller. Use this in
// tests to inject a mock without touching environment variables.
func ClassifyWith(ctx context.Context, candidates []scanner.Candidate, caller LLMCaller, log *slog.Logger) ([]DomainResult, error) {
	if log == nil {
		log = slog.Default()
	}
	return classify(ctx, candidates, caller, log)
}

// classify is the shared implementation used by both Classify and ClassifyWith.
func classify(ctx context.Context, candidates []scanner.Candidate, caller LLMCaller, log *slog.Logger) ([]DomainResult, error) {
	if len(candidates) == 0 {
		log.Info("classifier: no candidates, skipping LLM call")
		return nil, nil
	}

	// Build a compact index of candidates keyed by path for quick lookup.
	byPath := make(map[string]scanner.Candidate, len(candidates))
	for _, c := range candidates {
		byPath[c.Path] = c
	}

	prompt, err := buildPrompt(candidates)
	if err != nil {
		return nil, fmt.Errorf("classifier: building prompt: %w", err)
	}

	log.Info("classifier: calling LLM", "candidates", len(candidates))

	raw, err := caller.Call(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("classifier: LLM call: %w", err)
	}

	results, err := parseResponse(raw)
	if err != nil {
		return nil, fmt.Errorf("classifier: parsing response: %w", err)
	}

	// Enrich results with file counts and languages from the original candidates.
	for i, r := range results {
		if c, ok := byPath[r.Path]; ok {
			results[i].FileCount = c.FileCount
			results[i].Languages = c.Languages
		} else {
			log.Warn("classifier: LLM returned unknown path", "path", r.Path)
		}
	}

	log.Info("classifier: done", "domains", len(results))
	return results, nil
}

// buildPrompt serialises candidates as JSON and wraps them in the classification
// prompt.
func buildPrompt(candidates []scanner.Candidate) (string, error) {
	b, err := json.MarshalIndent(candidates, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshalling candidates: %w", err)
	}

	return fmt.Sprintf(`Here is the directory structure of a software project, represented as a JSON array of candidate directories with file counts and detected languages.

%s

Which directories represent logical domains (e.g. auth, billing, scanner, config) vs structural containers or utilities?

For each domain, respond with a JSON array (and nothing else — no markdown fences, no explanation) in this exact format:
[{"name": "short-domain-name", "path": "relative/path/from/root", "description": "One sentence describing what this domain does."}]

Only include directories that represent genuine logical domains. Omit pure utilities, test helpers, or structural scaffolding.`, string(b)), nil
}

// parseResponse strips optional markdown fences and unmarshals the JSON array
// returned by the LLM.
func parseResponse(raw string) ([]DomainResult, error) {
	cleaned := strings.TrimSpace(raw)

	// Strip ```json ... ``` or ``` ... ``` fences that some models add.
	if strings.HasPrefix(cleaned, "```") {
		start := strings.Index(cleaned, "\n")
		end := strings.LastIndex(cleaned, "```")
		if start != -1 && end > start {
			cleaned = strings.TrimSpace(cleaned[start+1 : end])
		}
	}

	// Find the first '[' in case of leading prose.
	if idx := strings.Index(cleaned, "["); idx > 0 {
		cleaned = cleaned[idx:]
	}

	var results []DomainResult
	if err := json.Unmarshal([]byte(cleaned), &results); err != nil {
		return nil, fmt.Errorf("unmarshalling LLM response: %w\nraw response: %s", err, raw)
	}
	return results, nil
}

// newCaller constructs the appropriate LLMCaller from cfg.
func newCaller(cfg Config) (LLMCaller, error) {
	switch cfg.Backend {
	case "claude":
		return &claudeBackend{model: cfg.Model}, nil
	case "openai":
		return newOpenAIBackend(cfg), nil
	case "ollama":
		base := cfg.OllamaBaseURL
		if base == "" {
			base = "http://localhost:11434"
		}
		return newOllamaBackend(cfg, base), nil
	case "":
		return nil, ErrNoBackend
	default:
		return nil, fmt.Errorf("unknown LLM backend %q: use claude, openai, or ollama", cfg.Backend)
	}
}

// ---------------------------------------------------------------------------
// Claude backend — invokes the `claude` CLI
// ---------------------------------------------------------------------------

type claudeBackend struct {
	model string
}

func (b *claudeBackend) Call(ctx context.Context, prompt string) (string, error) {
	args := []string{"--print"}
	if b.model != "" {
		args = append(args, "--model", b.model)
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, "claude", args...)
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude CLI: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
