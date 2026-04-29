package worker_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/frostyard/firn/pipeline/internal/specgen"
	"github.com/frostyard/firn/pipeline/internal/worker"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// mockGHRunner records calls and returns canned responses.
type mockGHRunner struct {
	// responses maps the first argument to a canned (output, error) pair.
	// The key is the first arg after "pr" (e.g. "list", "create").
	responses map[string]mockResponse
	calls     [][]string
}

type mockResponse struct {
	out []byte
	err error
}

func (m *mockGHRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	m.calls = append(m.calls, args)
	// Key on the second argument (e.g. "list" or "create") when first is "pr".
	key := ""
	if len(args) >= 2 {
		key = args[0] + "_" + args[1]
	}
	if r, ok := m.responses[key]; ok {
		return r.out, r.err
	}
	return []byte("{}"), nil
}

// mockLLMCaller returns a fixed response.
type mockLLMCaller struct {
	response string
	err      error
	calls    int
}

func (m *mockLLMCaller) Call(_ context.Context, _ string) (string, error) {
	m.calls++
	return m.response, m.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeSpecResult writes minimal spec files under a temp directory and returns
// a SpecResult pointing at them.
func makeSpecResult(t *testing.T, issueNum int) (specgen.SpecResult, string) {
	t.Helper()
	dir := t.TempDir()

	specDir := fmt.Sprintf("specs/GH%d", issueNum)
	absSpecDir := filepath.Join(dir, specDir)
	if err := os.MkdirAll(absSpecDir, 0o755); err != nil {
		t.Fatal(err)
	}

	productContent := fmt.Sprintf("# Feature for GH%d\n\n## Behavioral Invariants\n1. System does X.\n", issueNum)
	techContent := fmt.Sprintf("# Tech Spec GH%d\n\n## Implementation Plan\n- `main.go:10` — add handler\n", issueNum)

	productPath := filepath.Join(specDir, "product.md")
	techPath := filepath.Join(specDir, "tech.md")

	if err := os.WriteFile(filepath.Join(dir, productPath), []byte(productContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, techPath), []byte(techContent), 0o644); err != nil {
		t.Fatal(err)
	}

	return specgen.SpecResult{
		IssueNumber: issueNum,
		SpecDir:     specDir,
		ProductPath: productPath,
		TechPath:    techPath,
		PRNumber:    5,
		PRUrl:       fmt.Sprintf("https://github.com/frostyard/firn/pull/5"),
	}, dir
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestProcess_SkippedThrottled verifies that Process returns "skipped_throttled"
// when the number of open agent PRs equals MaxConcurrentPRs.
func TestProcess_SkippedThrottled(t *testing.T) {
	spec, repoPath := makeSpecResult(t, 42)

	// gh pr list returns 3 open PRs.
	ghRunner := &mockGHRunner{
		responses: map[string]mockResponse{
			"pr_list": {out: []byte(`[{"number":1},{"number":2},{"number":3}]`)},
		},
	}

	cfg := worker.Config{
		Repo:             "frostyard/firn",
		MaxConcurrentPRs: 3,
		GHRunner:         ghRunner,
		LLMCaller:        &mockLLMCaller{response: "impl summary"},
	}

	result, err := worker.Process(context.Background(), spec, repoPath, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "skipped_throttled" {
		t.Errorf("expected status skipped_throttled, got %q", result.Status)
	}
	if result.PRNumber != 0 {
		t.Errorf("expected PRNumber 0, got %d", result.PRNumber)
	}
}

// TestProcess_OpensDraftPR verifies that Process opens a draft PR when under
// the throttle limit and returns Status "opened" with the PR details.
func TestProcess_OpensDraftPR(t *testing.T) {
	spec, repoPath := makeSpecResult(t, 7)

	ghRunner := &mockGHRunner{
		responses: map[string]mockResponse{
			"pr_list":   {out: []byte(`[{"number":1}]`)}, // 1 open < max 3
			"pr_create": {out: []byte("https://github.com/frostyard/firn/pull/99\n")},
		},
	}
	llm := &mockLLMCaller{response: "This PR implements the feature from spec GH7."}

	cfg := worker.Config{
		Repo:             "frostyard/firn",
		MaxConcurrentPRs: 3,
		DraftFirst:       true,
		GHRunner:         ghRunner,
		LLMCaller:        llm,
	}

	result, err := worker.Process(context.Background(), spec, repoPath, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "opened" {
		t.Errorf("expected status opened, got %q", result.Status)
	}
	if result.PRNumber != 99 {
		t.Errorf("expected PRNumber 99, got %d", result.PRNumber)
	}
	if result.PRUrl != "https://github.com/frostyard/firn/pull/99" {
		t.Errorf("unexpected PRUrl: %q", result.PRUrl)
	}
	// Confirm the LLM was called once.
	if llm.calls != 1 {
		t.Errorf("expected 1 LLM call, got %d", llm.calls)
	}
	// Confirm --draft was passed to gh.
	foundDraft := false
	for _, call := range ghRunner.calls {
		for _, arg := range call {
			if arg == "--draft" {
				foundDraft = true
			}
		}
	}
	if !foundDraft {
		t.Error("expected --draft flag in gh pr create call")
	}
}

// TestCountOpenAgentPRs_ParsesOutput verifies that CountOpenAgentPRs correctly
// parses a JSON array of PR entries returned by gh.
func TestCountOpenAgentPRs_ParsesOutput(t *testing.T) {
	tests := []struct {
		name    string
		output  []byte
		want    int
		wantErr bool
	}{
		{
			name:   "empty list",
			output: []byte(`[]`),
			want:   0,
		},
		{
			name:   "one PR",
			output: []byte(`[{"number":42}]`),
			want:   1,
		},
		{
			name:   "three PRs",
			output: []byte(`[{"number":1},{"number":2},{"number":3}]`),
			want:   3,
		},
		{
			name:    "invalid JSON",
			output:  []byte(`not json`),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &mockGHRunner{
				responses: map[string]mockResponse{
					"pr_list": {out: tc.output},
				},
			}
			got, err := worker.CountOpenAgentPRs(context.Background(), "frostyard/firn", runner)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("want %d, got %d", tc.want, got)
			}
		})
	}
}

// TestProcess_DryRun verifies that DryRun=true skips PR creation and returns
// Status "dry_run".
func TestProcess_DryRun(t *testing.T) {
	spec, repoPath := makeSpecResult(t, 99)

	ghRunner := &mockGHRunner{} // no responses needed
	llm := &mockLLMCaller{response: "should not be called"}

	cfg := worker.Config{
		Repo:      "frostyard/firn",
		DryRun:    true,
		GHRunner:  ghRunner,
		LLMCaller: llm,
	}

	result, err := worker.Process(context.Background(), spec, repoPath, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "dry_run" {
		t.Errorf("expected status dry_run, got %q", result.Status)
	}
	if len(ghRunner.calls) != 0 {
		t.Errorf("expected no gh calls in dry-run, got %d", len(ghRunner.calls))
	}
	if llm.calls != 0 {
		t.Errorf("expected no LLM calls in dry-run, got %d", llm.calls)
	}
}

// TestProcess_CIFixerMaxAttempts verifies that CIFixerMaxAttempts is stored
// in Config and threaded through (implementation deferred — just verify
// the field is wired by confirming it doesn't cause an error).
func TestProcess_CIFixerMaxAttempts(t *testing.T) {
	spec, repoPath := makeSpecResult(t, 5)

	ghRunner := &mockGHRunner{
		responses: map[string]mockResponse{
			"pr_list":   {out: []byte(`[]`)},
			"pr_create": {out: []byte("https://github.com/frostyard/firn/pull/7\n")},
		},
	}

	cfg := worker.Config{
		Repo:               "frostyard/firn",
		MaxConcurrentPRs:   3,
		CIFixerMaxAttempts: 5, // non-default value
		DraftFirst:         true,
		GHRunner:           ghRunner,
		LLMCaller:          &mockLLMCaller{response: "summary"},
	}

	result, err := worker.Process(context.Background(), spec, repoPath, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Worker ran successfully — CIFixerMaxAttempts is stored in Config without error.
	if result.Status != "opened" {
		t.Errorf("expected status opened, got %q", result.Status)
	}
}
