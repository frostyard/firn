package generator_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/clix"
	"github.com/frostyard/firn/mentat/internal/classifier"
	"github.com/frostyard/firn/mentat/internal/generator"
	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 10}))
}

// mockCaller records calls and returns a canned response.
type mockCaller struct {
	response string
	err      error
	calls    int
}

func (m *mockCaller) Call(_ context.Context, _ string) (string, error) {
	m.calls++
	return m.response, m.err
}

// validSkillMD is a minimal valid SKILL.md response the mock LLM returns.
const validSkillMD = `---
name: auth
description: Handles user authentication and session management.
---

## Purpose

The auth domain manages login, logout, and session state.

## Key Abstractions

- Session: active user session struct.
- Authenticator: interface for verifying credentials.

## Common Patterns

Always call Authenticate before accessing protected resources.

## Entry Points

Start with auth.go for the main Authenticator implementation.

## Things to Know Before Modifying

Sessions are stored in-memory; restarting the service invalidates all sessions.
`

// domainAuth is a reusable test DomainResult.
var domainAuth = classifier.DomainResult{
	Name:        "auth",
	Path:        "internal/auth",
	Description: "Handles user authentication.",
	FileCount:   4,
	Languages:   []string{"Go"},
}

// makeRepoDir creates a temporary directory with a source file inside the
// domain path and returns the repo root.
func makeRepoDir(t *testing.T, domainPath string) string {
	t.Helper()
	repo := t.TempDir()
	domainDir := filepath.Join(repo, domainPath)
	if err := os.MkdirAll(domainDir, 0o755); err != nil {
		t.Fatalf("creating domain dir: %v", err)
	}
	// Put a minimal Go source file in the domain dir so sampleFiles has something to read.
	src := filepath.Join(domainDir, "auth.go")
	if err := os.WriteFile(src, []byte("package auth\n\n// Authenticate verifies credentials.\n"), 0o644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}
	return repo
}

// ---------------------------------------------------------------------------
// GenerateWith tests
// ---------------------------------------------------------------------------

func TestGenerateWith_WritesCorrectPath(t *testing.T) {
	repo := makeRepoDir(t, "internal/auth")
	mock := &mockCaller{response: validSkillMD}
	cfg := generator.Config{OutputDir: ".agents/skills", Overwrite: true}

	result, err := generator.GenerateWith(context.Background(), domainAuth, repo, mock, noopLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPath := filepath.Join(repo, ".agents", "skills", "auth", "SKILL.md")
	if result.Path != wantPath {
		t.Errorf("path: want %q, got %q", wantPath, result.Path)
	}
	if result.Skipped {
		t.Error("want Skipped=false, got true")
	}
	if result.Domain != "auth" {
		t.Errorf("domain: want %q, got %q", "auth", result.Domain)
	}

	// Verify file actually exists on disk.
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("SKILL.md not written: %v", err)
	}
	if mock.calls != 1 {
		t.Errorf("want 1 LLM call, got %d", mock.calls)
	}
}

func TestGenerateWith_SkipsExistingFile_OverwriteFalse(t *testing.T) {
	repo := makeRepoDir(t, "internal/auth")

	// Pre-create the SKILL.md file.
	skillDir := filepath.Join(repo, ".agents", "skills", "auth")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("creating skill dir: %v", err)
	}
	existingPath := filepath.Join(skillDir, "SKILL.md")
	original := "# existing content\n"
	if err := os.WriteFile(existingPath, []byte(original), 0o644); err != nil {
		t.Fatalf("writing existing file: %v", err)
	}

	mock := &mockCaller{response: validSkillMD}
	cfg := generator.Config{OutputDir: ".agents/skills", Overwrite: false}

	result, err := generator.GenerateWith(context.Background(), domainAuth, repo, mock, noopLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Skipped {
		t.Error("want Skipped=true when file exists and Overwrite=false")
	}
	if mock.calls != 0 {
		t.Errorf("want 0 LLM calls when skipping, got %d", mock.calls)
	}

	// File content must be unchanged.
	got, _ := os.ReadFile(existingPath)
	if string(got) != original {
		t.Errorf("existing file was modified; want %q, got %q", original, string(got))
	}
}

func TestGenerateWith_OverwritesExistingFile_OverwriteTrue(t *testing.T) {
	repo := makeRepoDir(t, "internal/auth")

	// Pre-create the SKILL.md file.
	skillDir := filepath.Join(repo, ".agents", "skills", "auth")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("creating skill dir: %v", err)
	}
	existingPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(existingPath, []byte("# old content\n"), 0o644); err != nil {
		t.Fatalf("writing existing file: %v", err)
	}

	mock := &mockCaller{response: validSkillMD}
	cfg := generator.Config{OutputDir: ".agents/skills", Overwrite: true}

	result, err := generator.GenerateWith(context.Background(), domainAuth, repo, mock, noopLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Skipped {
		t.Error("want Skipped=false when Overwrite=true")
	}
	if mock.calls != 1 {
		t.Errorf("want 1 LLM call, got %d", mock.calls)
	}

	got, _ := os.ReadFile(existingPath)
	if strings.Contains(string(got), "# old content") {
		t.Error("old content still present; file was not overwritten")
	}
}

func TestGenerateWith_LLMError(t *testing.T) {
	repo := makeRepoDir(t, "internal/auth")
	mock := &mockCaller{err: errors.New("network timeout")}
	cfg := generator.Config{OutputDir: ".agents/skills", Overwrite: true}

	_, err := generator.GenerateWith(context.Background(), domainAuth, repo, mock, noopLogger(), cfg)
	if err == nil {
		t.Fatal("want error from LLM call, got nil")
	}
	if !errors.Is(err, mock.err) {
		t.Errorf("error chain does not contain original error: %v", err)
	}
}

func TestGenerateWith_DefaultOutputDir(t *testing.T) {
	repo := makeRepoDir(t, "internal/auth")
	mock := &mockCaller{response: validSkillMD}
	// Empty OutputDir → must default to ".agents/skills"
	cfg := generator.Config{Overwrite: true}

	result, err := generator.GenerateWith(context.Background(), domainAuth, repo, mock, noopLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPath := filepath.Join(repo, ".agents", "skills", "auth", "SKILL.md")
	if result.Path != wantPath {
		t.Errorf("path: want %q, got %q", wantPath, result.Path)
	}
}

// ---------------------------------------------------------------------------
// GenerateAllWith tests
// ---------------------------------------------------------------------------

func TestGenerateAllWith_ProcessesAllDomains(t *testing.T) {
	domains := []classifier.DomainResult{
		{Name: "auth", Path: "internal/auth", Description: "Auth domain.", FileCount: 3, Languages: []string{"Go"}},
		{Name: "billing", Path: "internal/billing", Description: "Billing domain.", FileCount: 5, Languages: []string{"Go"}},
		{Name: "scanner", Path: "internal/scanner", Description: "Scanner domain.", FileCount: 7, Languages: []string{"Go"}},
	}

	repo := t.TempDir()
	for _, d := range domains {
		if err := os.MkdirAll(filepath.Join(repo, d.Path), 0o755); err != nil {
			t.Fatalf("creating domain dir: %v", err)
		}
	}

	billingSKILL := strings.ReplaceAll(validSkillMD, "auth", "billing")
	scannerSKILL := strings.ReplaceAll(validSkillMD, "auth", "scanner")

	callCount := 0
	responses := []string{validSkillMD, billingSKILL, scannerSKILL}
	mock := &mockCallerFn{fn: func(_ context.Context, _ string) (string, error) {
		r := responses[callCount]
		callCount++
		return r, nil
	}}

	cfg := generator.Config{OutputDir: ".agents/skills", Overwrite: true}
	results, err := generator.GenerateAllWith(context.Background(), domains, repo, mock, noopLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Skipped {
			t.Errorf("domain %q: want Skipped=false", r.Domain)
		}
		if _, err := os.Stat(r.Path); err != nil {
			t.Errorf("domain %q: SKILL.md not written: %v", r.Domain, err)
		}
	}
	if callCount != 3 {
		t.Errorf("want 3 LLM calls, got %d", callCount)
	}
}

func TestGenerateAllWith_EmptyDomains(t *testing.T) {
	repo := t.TempDir()
	mock := &mockCaller{response: validSkillMD}
	cfg := generator.Config{OutputDir: ".agents/skills", Overwrite: true}

	results, err := generator.GenerateAllWith(context.Background(), nil, repo, mock, noopLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results for empty domains, got %d", len(results))
	}
	if mock.calls != 0 {
		t.Errorf("want 0 LLM calls for empty domains, got %d", mock.calls)
	}
}

// ---------------------------------------------------------------------------
// Dry-run test
// ---------------------------------------------------------------------------

func TestGenerateAll_DryRun_NoWrites(t *testing.T) {
	// Save and restore clix.DryRun so we don't pollute other tests.
	original := clix.DryRun
	clix.DryRun = true
	t.Cleanup(func() { clix.DryRun = original })

	domains := []classifier.DomainResult{
		{Name: "auth", Path: "internal/auth", Description: "Auth domain.", FileCount: 3},
		{Name: "billing", Path: "internal/billing", Description: "Billing domain.", FileCount: 5},
	}

	repo := t.TempDir()
	// NOTE: GenerateAll requires a backend caller; with DryRun=true it should
	// return before constructing one. Pass a cfg that would fail with ErrNoBackend
	// to confirm the caller is never created.
	cfg := generator.Config{
		OutputDir: ".agents/skills",
		Backend:   "", // deliberately no backend
	}

	results, err := generator.GenerateAll(context.Background(), domains, repo, cfg)
	if err != nil {
		t.Fatalf("unexpected error in dry-run: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	for _, r := range results {
		if !r.Skipped {
			t.Errorf("domain %q: want Skipped=true in dry-run mode", r.Domain)
		}
		// No file should have been written.
		if _, err := os.Stat(r.Path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("domain %q: file should not exist in dry-run mode, stat err: %v", r.Domain, err)
		}
	}
}

// ---------------------------------------------------------------------------
// SKILL.md frontmatter validation
// ---------------------------------------------------------------------------

func TestGenerateWith_FrontmatterIsValidYAML(t *testing.T) {
	repo := makeRepoDir(t, "internal/auth")
	mock := &mockCaller{response: validSkillMD}
	cfg := generator.Config{OutputDir: ".agents/skills", Overwrite: true}

	result, err := generator.GenerateWith(context.Background(), domainAuth, repo, mock, noopLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("reading SKILL.md: %v", err)
	}

	content := string(raw)
	if !strings.HasPrefix(content, "---\n") {
		t.Fatalf("SKILL.md does not start with YAML frontmatter; got: %q", content[:min(50, len(content))])
	}

	// Extract YAML block between the first and second "---" markers.
	rest := content[4:] // skip leading "---\n"
	endIdx := strings.Index(rest, "\n---")
	if endIdx == -1 {
		t.Fatalf("SKILL.md frontmatter not closed with ---; content: %q", content[:min(200, len(content))])
	}
	yamlBlock := rest[:endIdx]

	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		t.Fatalf("frontmatter YAML parse error: %v\nyaml block: %q", err, yamlBlock)
	}
	if fm.Name == "" {
		t.Error("frontmatter: name field is empty")
	}
	if fm.Description == "" {
		t.Error("frontmatter: description field is empty")
	}
}

func TestGenerateWith_StripMarkdownFences(t *testing.T) {
	// LLM wrapped the response in ```markdown ... ``` fences.
	fenced := "```markdown\n" + validSkillMD + "\n```"
	repo := makeRepoDir(t, "internal/auth")
	mock := &mockCaller{response: fenced}
	cfg := generator.Config{OutputDir: ".agents/skills", Overwrite: true}

	result, err := generator.GenerateWith(context.Background(), domainAuth, repo, mock, noopLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, _ := os.ReadFile(result.Path)
	content := string(raw)
	if strings.Contains(content, "```") {
		t.Errorf("SKILL.md still contains markdown fences: %q", content[:min(100, len(content))])
	}
	if !strings.HasPrefix(content, "---\n") {
		t.Errorf("SKILL.md does not start with frontmatter after stripping fences: %q", content[:min(50, len(content))])
	}
}

func TestGenerateWith_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled immediately

	repo := makeRepoDir(t, "internal/auth")
	mock := &mockCaller{err: context.Canceled}
	cfg := generator.Config{OutputDir: ".agents/skills", Overwrite: true}

	_, err := generator.GenerateWith(ctx, domainAuth, repo, mock, noopLogger(), cfg)
	if err == nil {
		t.Fatal("want error for cancelled context, got nil")
	}
}

// ---------------------------------------------------------------------------
// Helper types
// ---------------------------------------------------------------------------

// mockCallerFn allows per-call response customisation.
type mockCallerFn struct {
	fn func(context.Context, string) (string, error)
}

func (m *mockCallerFn) Call(ctx context.Context, prompt string) (string, error) {
	return m.fn(ctx, prompt)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
