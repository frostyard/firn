package classifier_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/frostyard/firn/mentat/internal/classifier"
	"github.com/frostyard/firn/mentat/internal/scanner"
)

// ---------------------------------------------------------------------------
// Mock LLMCaller
// ---------------------------------------------------------------------------

type mockCaller struct {
	response string
	err      error
	calls    int
}

func (m *mockCaller) Call(_ context.Context, _ string) (string, error) {
	m.calls++
	return m.response, m.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

var silentLogger = slog.New(slog.NewTextHandler(nil, &slog.HandlerOptions{Level: slog.LevelError}))

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 10}))
}

// ---------------------------------------------------------------------------
// ClassifyWith tests
// ---------------------------------------------------------------------------

func TestClassifyWith_HappyPath(t *testing.T) {
	candidates := []scanner.Candidate{
		{Path: "internal/auth", FileCount: 5, Languages: []string{"Go"}},
		{Path: "internal/billing", FileCount: 3, Languages: []string{"Go"}},
	}

	mock := &mockCaller{
		response: `[{"name":"auth","path":"internal/auth","description":"Handles user authentication and authorisation."},{"name":"billing","path":"internal/billing","description":"Manages subscription payments and invoicing."}]`,
	}

	results, err := classifier.ClassifyWith(context.Background(), candidates, mock, noopLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}

	// Domain enrichment from candidates.
	for _, r := range results {
		switch r.Path {
		case "internal/auth":
			if r.FileCount != 5 {
				t.Errorf("auth FileCount: want 5, got %d", r.FileCount)
			}
			if r.Name != "auth" {
				t.Errorf("auth Name: want %q, got %q", "auth", r.Name)
			}
		case "internal/billing":
			if r.FileCount != 3 {
				t.Errorf("billing FileCount: want 3, got %d", r.FileCount)
			}
		default:
			t.Errorf("unexpected path %q in results", r.Path)
		}
	}
}

func TestClassifyWith_EmptyCandidates(t *testing.T) {
	mock := &mockCaller{}
	results, err := classifier.ClassifyWith(context.Background(), nil, mock, noopLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("want nil results for empty input, got %v", results)
	}
	if mock.calls != 0 {
		t.Errorf("want 0 LLM calls for empty input, got %d", mock.calls)
	}
}

func TestClassifyWith_LLMError(t *testing.T) {
	candidates := []scanner.Candidate{
		{Path: "internal/auth", FileCount: 5, Languages: []string{"Go"}},
	}
	mock := &mockCaller{err: errors.New("connection refused")}

	_, err := classifier.ClassifyWith(context.Background(), candidates, mock, noopLogger())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, mock.err) {
		// Check that the original error is wrapped.
		t.Errorf("error chain does not contain original error: %v", err)
	}
}

func TestClassifyWith_MarkdownFences(t *testing.T) {
	candidates := []scanner.Candidate{
		{Path: "pkg/scanner", FileCount: 8, Languages: []string{"Go"}},
	}
	mock := &mockCaller{
		response: "```json\n[{\"name\":\"scanner\",\"path\":\"pkg/scanner\",\"description\":\"Walks repository trees to find domain candidates.\"}]\n```",
	}

	results, err := classifier.ClassifyWith(context.Background(), candidates, mock, noopLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Name != "scanner" {
		t.Errorf("want name %q, got %q", "scanner", results[0].Name)
	}
	if results[0].FileCount != 8 {
		t.Errorf("want FileCount 8 (from candidate), got %d", results[0].FileCount)
	}
}

func TestClassifyWith_BadJSON(t *testing.T) {
	candidates := []scanner.Candidate{
		{Path: "internal/auth", FileCount: 5, Languages: []string{"Go"}},
	}
	mock := &mockCaller{response: "Sorry, I cannot classify this."}

	_, err := classifier.ClassifyWith(context.Background(), candidates, mock, noopLogger())
	if err == nil {
		t.Fatal("want error for malformed JSON response, got nil")
	}
}

func TestClassifyWith_UnknownPath(t *testing.T) {
	// LLM returns a path that wasn't in the candidates — should not crash, just
	// log a warning. FileCount and Languages remain zero/nil.
	candidates := []scanner.Candidate{
		{Path: "internal/auth", FileCount: 5, Languages: []string{"Go"}},
	}
	mock := &mockCaller{
		response: `[{"name":"ghost","path":"internal/ghost","description":"A domain that does not exist in the scan results."}]`,
	}

	results, err := classifier.ClassifyWith(context.Background(), candidates, mock, noopLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].FileCount != 0 {
		t.Errorf("want FileCount 0 for unknown path, got %d", results[0].FileCount)
	}
}

// ---------------------------------------------------------------------------
// DefaultConfig / backend detection tests
// ---------------------------------------------------------------------------

func TestDefaultConfig_Anthropic(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_MODEL", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := classifier.DefaultConfig()
	if cfg.Backend != "claude" {
		t.Errorf("want backend %q, got %q", "claude", cfg.Backend)
	}
}

func TestDefaultConfig_OpenAI(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	t.Setenv("CODEX_MODEL", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := classifier.DefaultConfig()
	if cfg.Backend != "openai" {
		t.Errorf("want backend %q, got %q", "openai", cfg.Backend)
	}
}

func TestDefaultConfig_Ollama(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_MODEL", "")
	t.Setenv("OLLAMA_HOST", "localhost:11434")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := classifier.DefaultConfig()
	if cfg.Backend != "ollama" {
		t.Errorf("want backend %q, got %q", "ollama", cfg.Backend)
	}
}

func TestDefaultConfig_OllamaBaseURL(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_MODEL", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_BASE_URL", "http://my-ollama:11434")

	cfg := classifier.DefaultConfig()
	if cfg.Backend != "ollama" {
		t.Errorf("want backend %q, got %q", "ollama", cfg.Backend)
	}
	if cfg.OllamaBaseURL != "http://my-ollama:11434" {
		t.Errorf("want OllamaBaseURL %q, got %q", "http://my-ollama:11434", cfg.OllamaBaseURL)
	}
}

func TestDefaultConfig_NoBackend(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_MODEL", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := classifier.DefaultConfig()
	// If the codex binary is present on PATH, DefaultConfig falls back to it.
	_, codexOnPath := exec.LookPath("codex")
	if codexOnPath == nil {
		if cfg.Backend != "codex" {
			t.Errorf("codex on PATH: want backend %q, got %q", "codex", cfg.Backend)
		}
		return
	}
	if cfg.Backend != "" {
		t.Errorf("want empty backend, got %q", cfg.Backend)
	}
}

// ---------------------------------------------------------------------------
// ErrNoBackend from Classify
// ---------------------------------------------------------------------------

func TestClassify_ErrNoBackend(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_MODEL", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_BASE_URL", "")

	// If codex is on PATH, DefaultConfig() returns codex — ErrNoBackend won't be
	// triggered. Skip rather than fail so CI isn't sensitive to installed tools.
	if _, err := exec.LookPath("codex"); err == nil {
		t.Skip("codex binary found on PATH; ErrNoBackend unreachable in this environment")
	}

	candidates := []scanner.Candidate{
		{Path: "internal/auth", FileCount: 5, Languages: []string{"Go"}},
	}

	_, err := classifier.Classify(context.Background(), candidates, classifier.DefaultConfig())
	if err == nil {
		t.Fatal("want ErrNoBackend, got nil")
	}
	if !errors.Is(err, classifier.ErrNoBackend) {
		t.Errorf("want errors.Is(err, ErrNoBackend) to be true; got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Context cancellation
// ---------------------------------------------------------------------------

func TestClassifyWith_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled immediately

	candidates := []scanner.Candidate{
		{Path: "internal/auth", FileCount: 5, Languages: []string{"Go"}},
	}

	mock := &mockCaller{err: context.Canceled}
	_, err := classifier.ClassifyWith(ctx, candidates, mock, noopLogger())
	if err == nil {
		t.Fatal("want error for cancelled context, got nil")
	}
}

// ---------------------------------------------------------------------------
// Leading prose before JSON array
// ---------------------------------------------------------------------------

func TestClassifyWith_LeadingProse(t *testing.T) {
	candidates := []scanner.Candidate{
		{Path: "internal/auth", FileCount: 5, Languages: []string{"Go"}},
	}
	mock := &mockCaller{
		response: `Sure! Here are the domains:
[{"name":"auth","path":"internal/auth","description":"Handles authentication."}]`,
	}

	results, err := classifier.ClassifyWith(context.Background(), candidates, mock, noopLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Name != "auth" {
		t.Errorf("want name %q, got %q", "auth", results[0].Name)
	}
}

// Ensure silentLogger is used (suppress "declared but not used" warnings).
var _ = silentLogger

// ---------------------------------------------------------------------------
// Codex backend detection and behaviour tests
// ---------------------------------------------------------------------------

// TestDefaultConfig_CodexModel verifies that CODEX_MODEL triggers codex backend
// selection and stores the model name.
func TestDefaultConfig_CodexModel(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_MODEL", "codex-mini")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := classifier.DefaultConfig()
	if cfg.Backend != "codex" {
		t.Errorf("want backend %q, got %q", "codex", cfg.Backend)
	}
	if cfg.Model != "codex-mini" {
		t.Errorf("want Model %q, got %q", "codex-mini", cfg.Model)
	}
}

// TestDefaultConfig_CodexModelPriorityOverOpenAI verifies that CODEX_MODEL takes
// priority over OPENAI_API_KEY.
func TestDefaultConfig_CodexModelPriorityOverOpenAI(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	t.Setenv("CODEX_MODEL", "codex-mini")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := classifier.DefaultConfig()
	if cfg.Backend != "codex" {
		t.Errorf("CODEX_MODEL should beat OPENAI_API_KEY: want %q, got %q", "codex", cfg.Backend)
	}
}

// mockCapturingCaller records the prompt it receives so tests can assert it.
type mockCapturingCaller struct {
	prompt string
}

func (m *mockCapturingCaller) Call(_ context.Context, prompt string) (string, error) {
	m.prompt = prompt
	return `[{"name":"auth","path":"internal/auth","description":"Auth domain."}]`, nil
}

// TestCodexBackend_PromptPassedThrough verifies that the prompt reaches the LLM
// caller unchanged (the codex backend is exercised via ClassifyWith, which
// accepts any LLMCaller, so we use a capturing mock to inspect the prompt rather
// than shelling out to the real codex binary).
func TestCodexBackend_PromptPassedThrough(t *testing.T) {
	candidates := []scanner.Candidate{
		{Path: "internal/auth", FileCount: 3, Languages: []string{"Go"}},
	}

	capture := &mockCapturingCaller{}
	results, err := classifier.ClassifyWith(context.Background(), candidates, capture, noopLogger())
	if err != nil {
		t.Fatalf("ClassifyWith error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	// The prompt must be non-empty and contain the candidate path.
	if capture.prompt == "" {
		t.Error("codex caller received empty prompt")
	}
	if !containsStr(capture.prompt, "internal/auth") {
		t.Errorf("prompt should reference candidate path; got: %.80s", capture.prompt)
	}
}

func containsStr(s, sub string) bool {
	return strings.Contains(s, sub)
}
