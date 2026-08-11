// Command firn is the installer for snosi images: recipe validator,
// headless install pipeline, and the TUI wizard (bare `firn`), all in
// one binary (ADR-0007, docs/plans/roadmap.md).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/frostyard/firn/internal/platform"
	"github.com/frostyard/firn/internal/recipe"
)

// version is stamped by the release build; "dev" otherwise.
var version = "dev"

const usage = `firn — the installer for snosi images

Usage:
  firn                                  launch the interactive installer (TUI)
  firn validate [flags] <recipe.toml>   validate a recipe against this machine
  firn install  [flags] [recipe.toml]   install; with no recipe, launch the TUI
  firn version                          print the firn version

Shared flags:
  --secure-boot auto|on|off   override Secure Boot detection (default auto)
  --tpm auto|on|off           override TPM detection (default auto)

Install flags:
  --dry-run                   validate, assemble, and run preflight only
  --uefi auto|on|off          override UEFI detection (default auto)
  --json-progress             emit NDJSON progress events on stdout
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		// Bare `firn` is the TUI wizard (ADR-0007): probe the platform,
		// no overrides. `firn install` without a recipe reaches the same
		// flow with its flags honored.
		return runTUI(tuiOptions{secureBoot: "auto", tpm: "auto", uefi: "auto"})
	}
	switch args[0] {
	case "validate":
		return runValidate(args[1:])
	case "install":
		return runInstall(args[1:])
	case "version", "--version":
		fmt.Println(version)
		return 0
	case "help", "--help", "-h":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "firn: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	sb := fs.String("secure-boot", "auto", "auto|on|off")
	tpm := fs.String("tpm", "auto", "auto|on|off")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprint(os.Stderr, "firn validate: exactly one recipe path is required\n")
		return 2
	}

	env := recipe.Env{ZoneinfoDir: "/usr/share/zoneinfo"}
	var err error
	if env.SecureBoot, err = tristate(*sb, platform.SecureBoot); err != nil {
		fmt.Fprintf(os.Stderr, "firn validate: --secure-boot: %v\n", err)
		return 2
	}
	if env.TPM, err = tristate(*tpm, platform.TPM); err != nil {
		fmt.Fprintf(os.Stderr, "firn validate: --tpm: %v\n", err)
		return 2
	}

	l, err := recipe.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	issues := recipe.Validate(l, env)
	for _, is := range issues {
		fmt.Fprintf(os.Stderr, "%v\n", is)
	}
	if len(issues) > 0 {
		fmt.Fprintf(os.Stderr, "firn: recipe is invalid (%d issue(s))\n", len(issues))
		return 1
	}
	fmt.Printf("firn: recipe is valid (family %s)\n", l.Recipe.Image.Family)
	return 0
}

func tristate(v string, probe func() bool) (bool, error) {
	switch v {
	case "auto":
		return probe(), nil
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, fmt.Errorf("must be auto, on, or off; got %q", v)
	}
}
