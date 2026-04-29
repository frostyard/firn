package main

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/frostyard/clix"
	"github.com/frostyard/firn/pipeline/internal/config"
	"github.com/frostyard/firn/pipeline/internal/version"
	"github.com/frostyard/firn/pipeline/internal/watcher"
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
				// In dry-run mode spin up the watcher but only log what would happen.
				ch, err := watcher.Watch(cmd.Context(), watchCfg)
				if err != nil {
					return fmt.Errorf("starting watcher: %w", err)
				}
				for issue := range ch {
					log.Info("dry-run: would process issue",
						"number", issue.Number,
						"title", issue.Title,
					)
				}
				return nil
			}

			log.Info("pipeline daemon starting",
				"repo", repo,
				"interval", interval,
				"pr_throttle", cfg.Pipeline.PRThrottle,
			)

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
