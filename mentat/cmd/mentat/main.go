package main

import (
	"github.com/frostyard/clix"
	"github.com/frostyard/firn/mentat/internal/version"
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
		Use:   "mentat",
		Short: "Repo documentation generator — produces per-domain SKILL.md files",
		Long: `mentat scans a repository, identifies logical domains, and generates
one SKILL.md documentation file per domain using an LLM.

Use --dry-run to preview changes without writing files.
Use --json for structured output suitable for scripting.`,
	}

	rootCmd.AddCommand(syncCmd(), statusCmd(), initCmd())
	app.Run(rootCmd) //nolint:errcheck
}
