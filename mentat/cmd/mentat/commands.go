package main

import (
	"github.com/frostyard/clix"
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
			if clix.DryRun {
				r.Message("dry-run: no files will be written")
			}
			// TODO: implement scanner, classifier, generator
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
