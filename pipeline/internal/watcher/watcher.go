// Package watcher polls GitHub for issues that carry a specific label and
// emits them on a channel for downstream processing.
//
// The watcher uses the `gh` CLI rather than raw HTTP so it inherits the
// caller's existing GitHub authentication without any extra credential
// management.
package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"time"
)

// GHRunner abstracts the gh CLI invocation for testing.
// The single Run method maps directly to:
//
//	gh <args...>
type GHRunner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// ExecRunner is the production GHRunner that delegates to the real gh binary
// found on PATH.
type ExecRunner struct{}

// Run executes `gh <args...>` and returns its combined stdout.
func (ExecRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh %v: %w", args, err)
	}
	return out, nil
}

// Config holds all configuration required by Watch.
type Config struct {
	// Repo is the GitHub repository slug, e.g. "frostyard/snosi".  Required.
	Repo string

	// Label is the issue label to filter on.  Defaults to "needs-spec" when
	// empty.
	Label string

	// Interval controls how often GitHub is polled.  Defaults to 5 minutes
	// when zero.
	Interval time.Duration

	// Runner is the GHRunner used to invoke the gh CLI.  Defaults to
	// ExecRunner (the real gh binary) when nil.
	Runner GHRunner

	// Log is the structured logger.  Defaults to a discard logger when nil.
	Log *slog.Logger
}

// Issue represents a GitHub issue surfaced by the watcher.
type Issue struct {
	Number int
	Title  string
	Body   string
	Labels []string
	URL    string
}

// ghIssue is the raw JSON structure returned by `gh issue list --json`.
type ghIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	URL string `json:"url"`
}

// Watch polls GitHub for issues labeled cfg.Label at cfg.Interval and sends
// each newly discovered issue on the returned channel.  The same issue is
// never sent more than once per Watch call.  The goroutine — and the channel —
// are closed when ctx is cancelled.
//
// Watch fires the first poll immediately so callers do not have to wait one
// full interval before seeing issues.
//
// It returns an error only for invalid configuration (e.g. empty Repo).
// Runtime poll errors are logged at Warn level and do not stop the loop.
func Watch(ctx context.Context, cfg Config) (<-chan Issue, error) {
	if cfg.Repo == "" {
		return nil, fmt.Errorf("watcher: repo must not be empty")
	}
	if cfg.Label == "" {
		cfg.Label = "needs-spec"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.Runner == nil {
		cfg.Runner = ExecRunner{}
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	ch := make(chan Issue, 32)

	go func() {
		defer close(ch)

		seen := make(map[int]struct{})

		poll := func() {
			issues, err := fetchIssues(ctx, cfg)
			if err != nil {
				cfg.Log.Warn("watcher: poll error", "repo", cfg.Repo, "err", err)
				return
			}
			cfg.Log.Debug("watcher: poll complete", "repo", cfg.Repo, "found", len(issues))
			for _, issue := range issues {
				if _, ok := seen[issue.Number]; ok {
					continue
				}
				seen[issue.Number] = struct{}{}
				select {
				case ch <- issue:
					cfg.Log.Info("watcher: discovered issue",
						"repo", cfg.Repo,
						"number", issue.Number,
						"title", issue.Title,
					)
				case <-ctx.Done():
					return
				}
			}
		}

		// First poll fires immediately.
		poll()

		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				poll()
			}
		}
	}()

	return ch, nil
}

// fetchIssues calls the gh CLI and parses the JSON list of issues.
func fetchIssues(ctx context.Context, cfg Config) ([]Issue, error) {
	out, err := cfg.Runner.Run(ctx,
		"issue", "list",
		"--repo", cfg.Repo,
		"--label", cfg.Label,
		"--json", "number,title,body,labels,url",
	)
	if err != nil {
		return nil, fmt.Errorf("fetching issues for %s: %w", cfg.Repo, err)
	}

	var raw []ghIssue
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing issues JSON for %s: %w", cfg.Repo, err)
	}

	issues := make([]Issue, 0, len(raw))
	for _, r := range raw {
		labels := make([]string, 0, len(r.Labels))
		for _, l := range r.Labels {
			labels = append(labels, l.Name)
		}
		issues = append(issues, Issue{
			Number: r.Number,
			Title:  r.Title,
			Body:   r.Body,
			Labels: labels,
			URL:    r.URL,
		})
	}

	return issues, nil
}
