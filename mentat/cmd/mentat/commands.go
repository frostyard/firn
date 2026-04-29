package main

import (
	"fmt"

	"github.com/frostyard/clix"
	"github.com/frostyard/firn/mentat/internal/scanner"
	"github.com/spf13/cobra"
)

func syncCmd() *cobra.Command {
	var repoPath string
	cmd := &cobra.Command{
		Use:   "sync [path]",
		Short: "Scan repo and generate/update SKILL.md files",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				repoPath = args[0]
			}

			r := clix.NewReporter()
			r.Message("scanning %s", repoPath)

			candidates, err := scanner.Scan(cmd.Context(), repoPath, scanner.DefaultConfig())
			if err != nil {
				return fmt.Errorf("scan: %w", err)
			}

			if ok, err := clix.OutputJSON(candidates); ok {
				return err
			}

			// Text output: one candidate per line.
			for _, c := range candidates {
				r.Message("  %s (%d files, %v)", c.Path, c.FileCount, c.Languages)
			}

			if clix.DryRun {
				r.Message("dry-run: no files will be written")
				return nil
			}

			// TODO: classifier, generator
			return nil
		},
	}
	cmd.Flags().StringVarP(&repoPath, "path", "p", ".", "repository path to scan")
	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [path]",
		Short: "Show current domain documentation status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := clix.NewReporter()
			r.Message("status: not yet implemented")
			return nil
		},
	}
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [path]",
		Short: "Initialize .agents/ directory structure in a repository",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := clix.NewReporter()
			r.Message("init: not yet implemented")
			return nil
		},
	}
}
