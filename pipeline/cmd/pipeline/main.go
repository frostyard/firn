package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/frostyard/clix"
	"github.com/frostyard/firn/pipeline/internal/config"
	"github.com/frostyard/firn/pipeline/internal/specgen"
	"github.com/frostyard/firn/pipeline/internal/version"
	"github.com/frostyard/firn/pipeline/internal/watcher"
	"github.com/frostyard/firn/pipeline/internal/worker"
	"github.com/spf13/cobra"
)

func main() {
	app := clix.App{
		Version: version.Version,
		Commit:  version.Commit,
		Date:    version.Date,
		BuiltBy: version.BuiltBy,
	}

	rootCmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Issue-to-PR execution engine for the frostyard ecosystem",
		Long: `pipeline watches GitHub issues, generates spec PRs, implements them in
isolated worktrees, and verifies results against behavioral invariants.

Use --dry-run to preview actions without making any changes.
Use --json for structured output suitable for scripting.`,
	}

	rootCmd.AddCommand(runCmd(), statusCmd(), triggerCmd())
	app.Run(rootCmd) //nolint:errcheck
}

// runCmd starts the pipeline daemon that polls GitHub for labeled issues.
func runCmd() *cobra.Command {
	var (
		repo       string
		interval   time.Duration
		configPath string
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start the pipeline daemon",
		Long:  "Polls GitHub for issues labeled needs-spec and drives the issue-to-PR workflow.",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := slog.New(slog.NewTextHandler(os.Stderr, nil))
			rep := clix.NewReporter()
			_ = rep // reporter available for future output

			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			if clix.Verbose {
				log.Info("effective config",
					"pr_throttle", cfg.Pipeline.PRThrottle,
					"ci_fixer_max_attempts", cfg.Pipeline.CIFixerMaxAttempts,
					"draft_first", cfg.Pipeline.DraftFirst,
				)
			}

			watchCfg := watcher.Config{
				Repo:     repo,
				Label:    "needs-spec",
				Interval: interval,
				Log:      log,
			}

			if clix.DryRun {
				log.Info("dry-run: pipeline daemon would start",
					"repo", repo,
					"interval", interval,
					"pr_throttle", cfg.Pipeline.PRThrottle,
					"ci_fixer_max_attempts", cfg.Pipeline.CIFixerMaxAttempts,
					"draft_first", cfg.Pipeline.DraftFirst,
				)
				// In dry-run mode spin up the watcher but only generate specs locally.
				sgCfg := specgen.DefaultConfig()
				sgCfg.Repo = repo
				sgCfg.DryRun = true
				sgCfg.Log = log

				ch, err := watcher.Watch(cmd.Context(), watchCfg)
				if err != nil {
					return fmt.Errorf("starting watcher: %w", err)
				}
				for issue := range ch {
					log.Info("dry-run: would process issue",
						"number", issue.Number,
						"title", issue.Title,
					)
					result, err := specgen.GenerateSpec(cmd.Context(), issue, "", sgCfg)
					if err != nil {
						log.Error("dry-run: spec generation failed",
							"issue", issue.Number,
							"err", err,
						)
						continue
					}
					log.Info("dry-run: spec files written (no PR)",
						"issue", issue.Number,
						"product", result.ProductPath,
						"tech", result.TechPath,
					)
				}
				return nil
			}

			log.Info("pipeline daemon starting",
				"repo", repo,
				"interval", interval,
				"pr_throttle", cfg.Pipeline.PRThrottle,
			)

			sgCfg := specgen.DefaultConfig()
			sgCfg.Repo = repo
			sgCfg.Log = log

			ch, err := watcher.Watch(cmd.Context(), watchCfg)
			if err != nil {
				return fmt.Errorf("starting watcher: %w", err)
			}

			for issue := range ch {
				log.Info("discovered issue",
					"number", issue.Number,
					"title", issue.Title,
					"url", issue.URL,
				)
				specResult, err := specgen.GenerateSpec(cmd.Context(), issue, "", sgCfg)
				if err != nil {
					log.Error("spec generation failed",
						"issue", issue.Number,
						"err", err,
					)
					continue
				}
				log.Info("spec generated",
					"issue", issue.Number,
					"spec_dir", specResult.SpecDir,
					"pr_url", specResult.PRUrl,
					"pr_number", specResult.PRNumber,
				)

				// Poll for the spec PR to merge (30s interval, 10min timeout).
				log.Info("worker: waiting for spec PR merge",
					"issue", issue.Number,
					"pr_number", specResult.PRNumber,
				)
				merged, pollErr := pollSpecPRMerge(cmd.Context(), repo, specResult.PRNumber, log)
				if pollErr != nil {
					log.Warn("worker: spec PR merge poll error",
						"issue", issue.Number,
						"pr_number", specResult.PRNumber,
						"err", pollErr,
					)
					continue
				}
				if !merged {
					log.Warn("worker: spec PR did not merge within timeout — skipping implementation",
						"issue", issue.Number,
						"pr_number", specResult.PRNumber,
					)
					continue
				}

				// Spec PR merged — run the worker.
				wCfg := worker.Config{
					Repo:               repo,
					MaxConcurrentPRs:   cfg.Pipeline.PRThrottle,
					CIFixerMaxAttempts: cfg.Pipeline.CIFixerMaxAttempts,
					DraftFirst:         cfg.Pipeline.DraftFirst,
					Log:                log,
				}
				workResult, wErr := worker.Process(cmd.Context(), specResult, "", wCfg)
				if wErr != nil {
					log.Error("worker: process failed",
						"issue", issue.Number,
						"err", wErr,
					)
					continue
				}
				log.Info("worker: impl PR result",
					"issue", issue.Number,
					"status", workResult.Status,
					"pr_number", workResult.PRNumber,
					"pr_url", workResult.PRUrl,
				)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&repo, "repo", "", "GitHub repository to watch (e.g. frostyard/snosi)")
	cmd.Flags().DurationVar(&interval, "interval", 5*time.Minute, "poll interval")
	cmd.Flags().StringVar(&configPath, "config", "", "path to config file (default: .firn/config.toml or ~/.config/firn/config.toml)")
	_ = cmd.MarkFlagRequired("repo")

	return cmd
}

// statusCmd shows the pipeline queue status for a repository.
func statusCmd() *cobra.Command {
	var repo string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show pipeline queue status",
		Long:  "Displays the current state of the issue-to-PR pipeline for a repository.",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := slog.New(slog.NewTextHandler(os.Stderr, nil))
			rep := clix.NewReporter()
			_ = rep // reporter available for future output

			if clix.DryRun {
				log.Info("dry-run: would fetch status", "repo", repo)
				return nil
			}

			log.Info("status not yet implemented", "repo", repo)
			return nil
		},
	}

	cmd.Flags().StringVar(&repo, "repo", "", "GitHub repository to query (e.g. frostyard/snosi)")
	_ = cmd.MarkFlagRequired("repo")

	return cmd
}

// triggerCmd manually triggers the pipeline for a specific issue.
func triggerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trigger [repo] [issue-number]",
		Short: "Manually trigger pipeline for a specific issue",
		Long:  "Bypasses the poller and immediately starts the spec-generation workflow for the given issue.",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			log := slog.New(slog.NewTextHandler(os.Stderr, nil))
			rep := clix.NewReporter()
			_ = rep // reporter available for future output

			if clix.DryRun {
				log.Info("dry-run: would trigger pipeline", "args", args)
				return nil
			}

			log.Info("trigger not yet implemented", "args", args)
			return nil
		},
	}

	return cmd
}

// pollSpecPRMerge polls GitHub every 30 seconds for up to 10 minutes to check
// whether the given spec PR has been merged. It returns (true, nil) when the
// PR state is "MERGED", (false, nil) when the timeout expires without a merge,
// and (false, err) when a polling error occurs that cannot be recovered from.
func pollSpecPRMerge(ctx context.Context, repo string, prNumber int, log *slog.Logger) (bool, error) {
	if prNumber <= 0 {
		// No PR number (dry-run spec, or PR creation was skipped). Nothing to poll.
		return false, nil
	}

	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()

	timeout := time.NewTimer(10 * time.Minute)
	defer timeout.Stop()

	runner := specgen.ExecGHRunner{}

	check := func() (bool, error) {
		out, err := runner.Run(ctx, "pr", "view",
			"--repo", repo,
			fmt.Sprintf("%d", prNumber),
			"--json", "state",
		)
		if err != nil {
			return false, fmt.Errorf("gh pr view %d: %w", prNumber, err)
		}
		type prState struct {
			State string `json:"state"`
		}
		var ps prState
		if jsonErr := json.Unmarshal(out, &ps); jsonErr != nil {
			return false, fmt.Errorf("parsing pr state: %w", jsonErr)
		}
		return ps.State == "MERGED", nil
	}

	// First check fires immediately.
	merged, err := check()
	if err != nil {
		log.Warn("pollSpecPRMerge: initial check failed", "pr", prNumber, "err", err)
	} else if merged {
		return true, nil
	}

	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-timeout.C:
			return false, nil
		case <-tick.C:
			merged, err := check()
			if err != nil {
				log.Warn("pollSpecPRMerge: poll check failed", "pr", prNumber, "err", err)
				continue
			}
			if merged {
				log.Info("pollSpecPRMerge: spec PR merged", "pr", prNumber)
				return true, nil
			}
			log.Debug("pollSpecPRMerge: not merged yet", "pr", prNumber)
		}
	}
}
