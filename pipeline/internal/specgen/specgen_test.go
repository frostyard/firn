package specgen_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/firn/pipeline/internal/specgen"
	"github.com/frostyard/firn/pipeline/internal/watcher"
)

// mockLLMCaller records calls and returns preconfigured responses.
type mockLLMCaller struct {
	calls     []string
	responses []string
	err       error
}

func (m *mockLLMCaller) Call(_ context.Context, prompt string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	idx := len(m.calls)
	m.calls = append(m.calls, prompt)
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
	// Default: return a minimal valid response based on which call it is.
	if idx == 0 {
		return fakeProductMD(42), nil
	}
	return fakeTechMD(42), nil
}

// mockGHRunner records calls and returns preconfigured output.
type mockGHRunner struct {
	calls   [][]string
	outputs map[string][]byte
	err     error
}

func (m *mockGHRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.calls = append(m.calls, args)
	key := strings.Join(args, " ")
	if out, ok := m.outputs[key]; ok {
		return out, nil
	}
	// Default responses.
	if len(args) >= 1 && args[0] == "api" {
		return []byte(`{"tree": [{"path": "main.go", "type": "blob"}, {"path": "internal/foo/foo.go", "type": "blob"}]}`), nil
	}
	if len(args) >= 1 && args[0] == "pr" {
		return []byte("https://github.com/example/repo/pull/7\n"), nil
	}
	return []byte{}, nil
}

func fakeProductMD(n int) string {
	return fmt.Sprintf(`# Feature GH%d — Product Spec

**Issue:** GH#%d
**Status:** Draft

## Context

This is a test feature.

## Behavioral Invariants

1. The system processes input correctly.
2. The system returns an error when input is invalid.
3. The system does not modify external state during dry-run.

## Out of Scope

- Performance optimization

## Open Questions

- [ ] None`, n, n)
}

func fakeTechMD(n int) string {
	return fmt.Sprintf(`# Feature GH%d — Technical Spec

**Issue:** GH#%d
**Product Spec:** [product.md](product.md)

## Implementation Plan

- `+"`internal/foo/foo.go:1-20`"+` — add new handler function
- `+"`main.go:42`"+` — wire handler into router

## Verification

1. Invariant 1: `+"`go test ./internal/foo/...`"+`
2. Invariant 2: `+"`go test ./internal/foo/... -run TestInvalidInput`"+`
3. Invariant 3: `+"`go test ./... -run TestDryRun`"+`

## Dependencies

- [ ] None

## Risks

- None identified`, n, n)
}

// baseIssue is a reusable test issue.
var baseIssue = watcher.Issue{
	Number: 42,
	Title:  "Add awesome feature",
	Body:   "This feature should do something awesome.",
	Labels: []string{"needs-spec"},
	URL:    "https://github.com/example/repo/issues/42",
}

// TestGenerateSpec_ProductMDCreated verifies that product.md is written at the
// expected path with the LLM response content.
func TestGenerateSpec_ProductMDCreated(t *testing.T) {
	dir := t.TempDir()
	llm := &mockLLMCaller{
		responses: []string{fakeProductMD(42), fakeTechMD(42)},
	}
	gh := &mockGHRunner{}

	result, err := specgen.GenerateSpec(context.Background(), baseIssue, dir, specgen.Config{
		Repo:      "example/repo",
		DryRun:    true,
		LLMCaller: llm,
		GHRunner:  gh,
	})
	if err != nil {
		t.Fatalf("GenerateSpec returned error: %v", err)
	}

	productBytes, err := os.ReadFile(filepath.Join(dir, result.ProductPath))
	if err != nil {
		t.Fatalf("reading product.md: %v", err)
	}

	content := string(productBytes)
	if !strings.Contains(content, "Behavioral Invariants") {
		t.Errorf("product.md missing 'Behavioral Invariants' section; got:\n%s", content)
	}
	if !strings.Contains(content, "GH#42") {
		t.Errorf("product.md missing issue reference GH#42; got:\n%s", content)
	}
}

// TestGenerateSpec_TechMDCreated verifies that tech.md is written at the
// expected path with file:line references.
func TestGenerateSpec_TechMDCreated(t *testing.T) {
	dir := t.TempDir()
	llm := &mockLLMCaller{
		responses: []string{fakeProductMD(42), fakeTechMD(42)},
	}
	gh := &mockGHRunner{}

	result, err := specgen.GenerateSpec(context.Background(), baseIssue, dir, specgen.Config{
		Repo:      "example/repo",
		DryRun:    true,
		LLMCaller: llm,
		GHRunner:  gh,
	})
	if err != nil {
		t.Fatalf("GenerateSpec returned error: %v", err)
	}

	techBytes, err := os.ReadFile(filepath.Join(dir, result.TechPath))
	if err != nil {
		t.Fatalf("reading tech.md: %v", err)
	}

	content := string(techBytes)
	if !strings.Contains(content, "Implementation Plan") {
		t.Errorf("tech.md missing 'Implementation Plan' section; got:\n%s", content)
	}
	if !strings.Contains(content, ".go:") {
		t.Errorf("tech.md missing file:line references; got:\n%s", content)
	}
}

// TestGenerateSpec_SpecDirCorrect verifies the spec directory is created at
// specs/GH{N}/ relative to repoPath.
func TestGenerateSpec_SpecDirCorrect(t *testing.T) {
	dir := t.TempDir()
	llm := &mockLLMCaller{
		responses: []string{fakeProductMD(42), fakeTechMD(42)},
	}
	gh := &mockGHRunner{}

	result, err := specgen.GenerateSpec(context.Background(), baseIssue, dir, specgen.Config{
		Repo:      "example/repo",
		DryRun:    true,
		LLMCaller: llm,
		GHRunner:  gh,
	})
	if err != nil {
		t.Fatalf("GenerateSpec returned error: %v", err)
	}

	wantSpecDir := filepath.Join("specs", "GH42")
	if result.SpecDir != wantSpecDir {
		t.Errorf("SpecDir = %q, want %q", result.SpecDir, wantSpecDir)
	}
	if result.ProductPath != filepath.Join(wantSpecDir, "product.md") {
		t.Errorf("ProductPath = %q, want %q", result.ProductPath, filepath.Join(wantSpecDir, "product.md"))
	}
	if result.TechPath != filepath.Join(wantSpecDir, "tech.md") {
		t.Errorf("TechPath = %q, want %q", result.TechPath, filepath.Join(wantSpecDir, "tech.md"))
	}

	// Confirm the directory exists on disk.
	if _, err := os.Stat(filepath.Join(dir, wantSpecDir)); err != nil {
		t.Errorf("spec directory not found on disk: %v", err)
	}
}

// TestGenerateSpec_PROpened verifies that a PR is created when DryRun is false.
func TestGenerateSpec_PROpened(t *testing.T) {
	dir := t.TempDir()
	llm := &mockLLMCaller{
		responses: []string{fakeProductMD(42), fakeTechMD(42)},
	}
	gh := &mockGHRunner{
		outputs: map[string][]byte{
			"pr create --repo example/repo --title spec: GH42 Add awesome feature --body Spec PR for GH#42.\n\nGenerated from issue: https://github.com/example/repo/issues/42\n\nFiles:\n- `specs/GH42/product.md`\n- `specs/GH42/tech.md` --head spec/GH42 --draft": []byte("https://github.com/example/repo/pull/7\n"),
		},
	}

	result, err := specgen.GenerateSpec(context.Background(), baseIssue, dir, specgen.Config{
		Repo:      "example/repo",
		DryRun:    false,
		LLMCaller: llm,
		GHRunner:  gh,
	})
	if err != nil {
		t.Fatalf("GenerateSpec returned error: %v", err)
	}

	// The mock GHRunner returns a PR URL for any `pr create` call.
	if result.PRUrl == "" {
		t.Error("PRUrl should not be empty when DryRun is false")
	}
	if result.PRNumber == 0 {
		t.Error("PRNumber should not be 0 when DryRun is false")
	}

	// Confirm `gh pr create` was called.
	foundPRCreate := false
	for _, call := range gh.calls {
		if len(call) >= 2 && call[0] == "pr" && call[1] == "create" {
			foundPRCreate = true
			break
		}
	}
	if !foundPRCreate {
		t.Errorf("expected `gh pr create` call, got calls: %v", gh.calls)
	}
}

// TestGenerateSpec_DryRunSkipsPR verifies that no PR is opened in dry-run mode.
func TestGenerateSpec_DryRunSkipsPR(t *testing.T) {
	dir := t.TempDir()
	llm := &mockLLMCaller{
		responses: []string{fakeProductMD(42), fakeTechMD(42)},
	}
	gh := &mockGHRunner{}

	result, err := specgen.GenerateSpec(context.Background(), baseIssue, dir, specgen.Config{
		Repo:      "example/repo",
		DryRun:    true,
		LLMCaller: llm,
		GHRunner:  gh,
	})
	if err != nil {
		t.Fatalf("GenerateSpec returned error: %v", err)
	}

	if result.PRUrl != "" {
		t.Errorf("DryRun=true: expected empty PRUrl, got %q", result.PRUrl)
	}
	if result.PRNumber != 0 {
		t.Errorf("DryRun=true: expected PRNumber=0, got %d", result.PRNumber)
	}

	// Confirm no `gh pr create` was called.
	for _, call := range gh.calls {
		if len(call) >= 2 && call[0] == "pr" && call[1] == "create" {
			t.Errorf("DryRun=true: unexpected `gh pr create` call: %v", call)
		}
	}
}

// TestGenerateSpec_LLMError verifies that an LLM error is propagated.
func TestGenerateSpec_LLMError(t *testing.T) {
	dir := t.TempDir()
	llm := &mockLLMCaller{err: fmt.Errorf("LLM unavailable")}
	gh := &mockGHRunner{}

	_, err := specgen.GenerateSpec(context.Background(), baseIssue, dir, specgen.Config{
		Repo:      "example/repo",
		DryRun:    true,
		LLMCaller: llm,
		GHRunner:  gh,
	})
	if err == nil {
		t.Fatal("expected error when LLM fails, got nil")
	}
	if !strings.Contains(err.Error(), "LLM unavailable") {
		t.Errorf("error should mention 'LLM unavailable', got: %v", err)
	}
}

// TestGenerateSpec_MissingRepo verifies that an empty Repo returns an error.
func TestGenerateSpec_MissingRepo(t *testing.T) {
	dir := t.TempDir()
	llm := &mockLLMCaller{}
	gh := &mockGHRunner{}

	_, err := specgen.GenerateSpec(context.Background(), baseIssue, dir, specgen.Config{
		Repo:      "",
		DryRun:    true,
		LLMCaller: llm,
		GHRunner:  gh,
	})
	if err == nil {
		t.Fatal("expected error for empty Repo, got nil")
	}
}

// TestParsePRNumber tests URL parsing via a round-trip through the full flow —
// the mockGHRunner returns a URL and we verify the number is extracted.
func TestParsePRNumber(t *testing.T) {
	tests := []struct {
		name       string
		prURL      string
		wantNumber int
	}{
		{"standard URL", "https://github.com/example/repo/pull/7", 7},
		{"URL with newline", "https://github.com/example/repo/pull/123\n", 123},
		{"large number", "https://github.com/example/repo/pull/4567", 4567},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			llm := &mockLLMCaller{
				responses: []string{fakeProductMD(42), fakeTechMD(42)},
			}
			gh := &mockGHRunner{
				outputs: map[string][]byte{},
			}
			// Override pr create to return specific URL.
			gh.outputs["api repos/example/repo/git/trees/HEAD?recursive=1"] = []byte(`{"tree":[]}`)
			// Use a custom runner that returns the test URL for pr create.
			customGH := &prURLGHRunner{url: tt.prURL}

			result, err := specgen.GenerateSpec(context.Background(), baseIssue, dir, specgen.Config{
				Repo:      "example/repo",
				DryRun:    false,
				LLMCaller: llm,
				GHRunner:  customGH,
			})
			if err != nil {
				t.Fatalf("GenerateSpec returned error: %v", err)
			}
			if result.PRNumber != tt.wantNumber {
				t.Errorf("PRNumber = %d, want %d (from URL %q)", result.PRNumber, tt.wantNumber, tt.prURL)
			}
		})
	}
}

// prURLGHRunner returns a fixed PR URL for pr create calls, and default tree
// response for api calls.
type prURLGHRunner struct {
	url   string
	calls [][]string
}

func (r *prURLGHRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	r.calls = append(r.calls, args)
	if len(args) >= 1 && args[0] == "api" {
		return []byte(`{"tree": []}`), nil
	}
	if len(args) >= 1 && args[0] == "pr" {
		return []byte(r.url), nil
	}
	return []byte{}, nil
}

// ---------------------------------------------------------------------------
// Copilot backend detection tests
// ---------------------------------------------------------------------------

func TestDefaultConfig_CopilotToken(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GH_COPILOT_TOKEN", "ghp_copilot_test")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := specgen.DefaultConfig()
	if cfg.Backend != "copilot" {
		t.Errorf("want backend %q when GH_COPILOT_TOKEN is set, got %q", "copilot", cfg.Backend)
	}
}

func TestDefaultConfig_CopilotTokenPriorityOverOpenAI(t *testing.T) {
	// GH_COPILOT_TOKEN takes priority over OPENAI_API_KEY.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GH_COPILOT_TOKEN", "ghp_copilot_test")
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := specgen.DefaultConfig()
	if cfg.Backend != "copilot" {
		t.Errorf("want backend %q (copilot takes priority over openai), got %q", "copilot", cfg.Backend)
	}
}

func TestDefaultConfig_ClaudePriorityOverCopilot(t *testing.T) {
	// ANTHROPIC_API_KEY takes priority over GH_COPILOT_TOKEN.
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("GH_COPILOT_TOKEN", "ghp_copilot_test")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_BASE_URL", "")

	cfg := specgen.DefaultConfig()
	if cfg.Backend != "claude" {
		t.Errorf("want backend %q (claude takes priority over copilot), got %q", "claude", cfg.Backend)
	}
}

func TestDefaultConfig_CopilotWhich(t *testing.T) {
	// When GH_COPILOT_TOKEN is not set but `pi` is in PATH, copilot should be selected.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GH_COPILOT_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_BASE_URL", "")
	if _, err := exec.LookPath("pi"); err != nil {
		t.Skip("pi not in PATH, skipping which-based detection test")
	}

	cfg := specgen.DefaultConfig()
	if cfg.Backend != "copilot" {
		t.Errorf("want backend %q when pi is in PATH, got %q", "copilot", cfg.Backend)
	}
}

func TestDefaultConfig_OpenAI_NoCopilot(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GH_COPILOT_TOKEN", "")
	t.Setenv("OPENAI_API_KEY", "sk-openai-test")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_BASE_URL", "")
	// Prevent piInPath() from returning true in environments where pi is installed.
	t.Setenv("PATH", "/usr/bin:/bin")

	cfg := specgen.DefaultConfig()
	if cfg.Backend != "openai" {
		t.Errorf("want backend %q, got %q", "openai", cfg.Backend)
	}
}

// TestCopilotBackend_ViaLLMCallerInterface verifies that GenerateSpec works
// end-to-end when a copilot-backend-shaped mock is injected. The actual `pi`
// binary is not called; the mock returns pre-canned responses.
func TestCopilotBackend_ViaLLMCallerInterface(t *testing.T) {
	dir := t.TempDir()
	llm := &mockLLMCaller{
		responses: []string{fakeProductMD(99), fakeTechMD(99)},
	}
	gh := &mockGHRunner{}

	issue := watcher.Issue{
		Number: 99,
		Title:  "Copilot backend test",
		Body:   "Verify copilot backend plumbing.",
		Labels: []string{"needs-spec"},
		URL:    "https://github.com/example/repo/issues/99",
	}

	result, err := specgen.GenerateSpec(context.Background(), issue, dir, specgen.Config{
		Repo:      "example/repo",
		DryRun:    true,
		LLMCaller: llm,
		GHRunner:  gh,
	})
	if err != nil {
		t.Fatalf("GenerateSpec returned error: %v", err)
	}
	if result.IssueNumber != 99 {
		t.Errorf("IssueNumber = %d, want 99", result.IssueNumber)
	}
	if llm.calls == nil || len(llm.calls) != 2 {
		t.Errorf("want 2 LLM calls (product + tech), got %d", len(llm.calls))
	}
	// Verify files were written.
	if _, err := os.Stat(filepath.Join(dir, result.ProductPath)); err != nil {
		t.Errorf("product.md not found: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, result.TechPath)); err != nil {
		t.Errorf("tech.md not found: %v", err)
	}
}
