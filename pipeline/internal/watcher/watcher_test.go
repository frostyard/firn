package watcher_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/frostyard/firn/pipeline/internal/watcher"
)

// mockRunner implements GHRunner for unit tests.
type mockRunner struct {
	// responses is a queue of (output, error) pairs returned in order.
	// When the queue is exhausted the last entry is repeated.
	responses []mockResponse
	calls     [][]string // args recorded per call
}

type mockResponse struct {
	out []byte
	err error
}

func (m *mockRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	m.calls = append(m.calls, args)
	if len(m.responses) == 0 {
		return []byte("[]"), nil
	}
	idx := len(m.calls) - 1
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	r := m.responses[idx]
	return r.out, r.err
}

// ghIssueJSON is a helper that marshals a slice of maps to JSON bytes.
func ghIssueJSON(issues []map[string]any) []byte {
	b, _ := json.Marshal(issues)
	return b
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestWatchEmptyRepo verifies Watch returns an error when Repo is empty.
func TestWatchEmptyRepo(t *testing.T) {
	cfg := watcher.Config{Label: "needs-spec"}
	_, err := watcher.Watch(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for empty Repo, got nil")
	}
}

// TestWatchParsesIssues verifies that Watch correctly parses gh JSON output and
// emits the right Issue values on the channel.
func TestWatchParsesIssues(t *testing.T) {
	payload := ghIssueJSON([]map[string]any{
		{
			"number": 42,
			"title":  "Add caching layer",
			"body":   "We need a caching layer for performance.",
			"labels": []map[string]any{{"name": "needs-spec"}},
			"url":    "https://github.com/org/repo/issues/42",
		},
		{
			"number": 7,
			"title":  "Fix login bug",
			"body":   "Login is broken on mobile.",
			"labels": []map[string]any{{"name": "needs-spec"}, {"name": "bug"}},
			"url":    "https://github.com/org/repo/issues/7",
		},
	})

	runner := &mockRunner{responses: []mockResponse{{out: payload}}}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cfg := watcher.Config{
		Repo:     "org/repo",
		Label:    "needs-spec",
		Interval: 10 * time.Second, // long enough that only the first poll fires
		Runner:   runner,
	}

	ch, err := watcher.Watch(ctx, cfg)
	if err != nil {
		t.Fatalf("Watch returned unexpected error: %v", err)
	}

	var got []watcher.Issue
	for issue := range ch {
		got = append(got, issue)
		if len(got) == 2 {
			cancel() // stop after collecting both issues
		}
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(got))
	}

	cases := []struct {
		number int
		title  string
		labels []string
		url    string
	}{
		{42, "Add caching layer", []string{"needs-spec"}, "https://github.com/org/repo/issues/42"},
		{7, "Fix login bug", []string{"needs-spec", "bug"}, "https://github.com/org/repo/issues/7"},
	}

	for i, c := range cases {
		g := got[i]
		if g.Number != c.number {
			t.Errorf("issue[%d].Number = %d, want %d", i, g.Number, c.number)
		}
		if g.Title != c.title {
			t.Errorf("issue[%d].Title = %q, want %q", i, g.Title, c.title)
		}
		if len(g.Labels) != len(c.labels) {
			t.Errorf("issue[%d].Labels len = %d, want %d", i, len(g.Labels), len(c.labels))
		} else {
			for j, l := range c.labels {
				if g.Labels[j] != l {
					t.Errorf("issue[%d].Labels[%d] = %q, want %q", i, j, g.Labels[j], l)
				}
			}
		}
		if g.URL != c.url {
			t.Errorf("issue[%d].URL = %q, want %q", i, g.URL, c.url)
		}
	}
}

// TestWatchContextCancellation verifies that the watch loop stops when ctx is
// cancelled and the returned channel is closed.
func TestWatchContextCancellation(t *testing.T) {
	runner := &mockRunner{responses: []mockResponse{{out: []byte("[]")}}}

	ctx, cancel := context.WithCancel(context.Background())

	cfg := watcher.Config{
		Repo:     "org/repo",
		Interval: 100 * time.Millisecond,
		Runner:   runner,
	}

	ch, err := watcher.Watch(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cancel immediately and drain the channel to confirm it closes.
	cancel()

	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed — test passes
			}
		case <-timeout:
			t.Fatal("timed out waiting for channel to close after context cancellation")
		}
	}
}

// TestWatchDeduplication verifies that Watch sends each issue at most once even
// across multiple poll ticks.
func TestWatchDeduplication(t *testing.T) {
	// Both poll responses return the same issue.
	payload := ghIssueJSON([]map[string]any{
		{
			"number": 1,
			"title":  "Dedup me",
			"body":   "",
			"labels": []map[string]any{{"name": "needs-spec"}},
			"url":    "https://github.com/org/repo/issues/1",
		},
	})

	// Provide two identical responses so two polls both return the same issue.
	runner := &mockRunner{
		responses: []mockResponse{
			{out: payload},
			{out: payload},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cfg := watcher.Config{
		Repo:     "org/repo",
		Interval: 20 * time.Millisecond, // fast interval so second tick fires quickly
		Runner:   runner,
	}

	ch, err := watcher.Watch(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect what arrives; wait long enough for at least two ticks.
	time.Sleep(80 * time.Millisecond)
	cancel() // stop the watcher

	var got []watcher.Issue
	for issue := range ch {
		got = append(got, issue)
	}

	if len(got) != 1 {
		t.Errorf("expected 1 issue (dedup), got %d", len(got))
	}
}

// TestWatchPollErrorDoesNotStop verifies that a poll error is non-fatal: the
// loop continues and returns issues once the runner recovers.
func TestWatchPollErrorDoesNotStop(t *testing.T) {
	goodPayload := ghIssueJSON([]map[string]any{
		{
			"number": 99,
			"title":  "After error",
			"body":   "",
			"labels": []map[string]any{{"name": "needs-spec"}},
			"url":    "https://github.com/org/repo/issues/99",
		},
	})

	runner := &mockRunner{
		responses: []mockResponse{
			{err: errors.New("network timeout")},   // first poll fails
			{out: goodPayload},                      // second poll succeeds
			{out: []byte("[]")},                     // subsequent polls quiet
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cfg := watcher.Config{
		Repo:     "org/repo",
		Interval: 20 * time.Millisecond,
		Runner:   runner,
	}

	ch, err := watcher.Watch(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got []watcher.Issue
	timeout := time.After(1 * time.Second)
collect:
	for {
		select {
		case issue, ok := <-ch:
			if !ok {
				break collect
			}
			got = append(got, issue)
			if len(got) >= 1 {
				cancel()
			}
		case <-timeout:
			cancel()
			break collect
		}
	}
	// drain
	for range ch {
	}

	if len(got) != 1 {
		t.Errorf("expected 1 issue after recovery, got %d", len(got))
	}
	if got[0].Number != 99 {
		t.Errorf("expected issue #99, got #%d", got[0].Number)
	}
}

// TestWatchDefaultLabel verifies that an empty Label in Config defaults to
// "needs-spec" and is forwarded to the gh runner.
func TestWatchDefaultLabel(t *testing.T) {
	runner := &mockRunner{responses: []mockResponse{{out: []byte("[]")}}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := watcher.Config{
		Repo:     "org/repo",
		Label:    "", // should default to "needs-spec"
		Interval: 10 * time.Second,
		Runner:   runner,
	}

	ch, err := watcher.Watch(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cancel()
	for range ch {
	}

	if len(runner.calls) == 0 {
		t.Fatal("expected at least one runner call")
	}
	args := runner.calls[0]
	foundLabel := false
	for i, a := range args {
		if a == "--label" && i+1 < len(args) && args[i+1] == "needs-spec" {
			foundLabel = true
			break
		}
	}
	if !foundLabel {
		t.Errorf("expected --label needs-spec in runner args, got %v", args)
	}
}

// TestWatchInvalidJSON verifies that malformed JSON from the runner causes a
// Warn log but does not crash the loop.
func TestWatchInvalidJSON(t *testing.T) {
	runner := &mockRunner{
		responses: []mockResponse{
			{out: []byte("not-json")},
			{out: []byte("[]")},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	cfg := watcher.Config{
		Repo:     "org/repo",
		Interval: 20 * time.Millisecond,
		Runner:   runner,
	}

	ch, err := watcher.Watch(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Just ensure the channel closes without panic.
	for range ch {
	}
}
