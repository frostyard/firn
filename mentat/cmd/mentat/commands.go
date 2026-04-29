package main

import (
	"fmt"
	"log/slog"

	"github.com/frostyard/clix"
	"github.com/frostyard/firn/mentat/internal/classifier"
	"github.com/frostyard/firn/mentat/internal/generator"
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

			// Classify candidates into logical domains.
			r.Message("classifying domains")
			classCfg := classifier.DefaultConfig()
			classCfg.Logger = slog.Default()
			domains, err := classifier.Classify(cmd.Context(), candidates, classCfg)
			if err != nil {
				return fmt.Errorf("classify: %w", err)
			}

			// Text output: one domain per line.
			for _, d := range domains {
				r.Message("  [%s] %s — %s (%d files, %v)", d.Name, d.Path, d.Description, d.FileCount, d.Languages)
			}

			// Generate SKILL.md files for each domain.
			// dry-run is handled inside GenerateAll via clix.DryRun.
			r.Message("generating skill docs")
			genCfg := generator.Config{
				Backend:   classCfg.Backend,
				Model:     classCfg.Model,
				Overwrite: false,
				Logger:    slog.Default(),
			}

			genResults, err := generator.GenerateAll(cmd.Context(), domains, repoPath, genCfg)
			if err != nil {
				return fmt.Errorf("generate: %w", err)
			}

			if ok, err := clix.OutputJSON(genResults); ok {
				return err
			}

			for _, res := range genResults {
				if res.Skipped {
					r.Message("  skipped %s", res.Domain)
				} else {
					r.Message("  generated %s → %s", res.Domain, res.Path)
				}
			}

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
