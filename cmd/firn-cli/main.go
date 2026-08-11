// Entry point for the firn binary, frostyard-house-pattern shaped
// (core skill frostyard-go-repo; exemplar updex): clix wraps the cobra
// tree with version plumbing, completions, and man pages. All behavior
// lives in cmd/firn; the pipeline lives in internal/ (ADR-0011 records
// the deliberate deviation from the SDK-first layout).
package main

import (
	"os"

	"github.com/frostyard/clix"
	"github.com/frostyard/firn/cmd/firn"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "local"
)

func main() {
	firn.Version = version
	app := clix.App{
		Version: version,
		Commit:  commit,
		Date:    date,
		BuiltBy: builtBy,
	}
	if err := app.Run(firn.NewRootCmd()); err != nil {
		os.Exit(1)
	}
}
