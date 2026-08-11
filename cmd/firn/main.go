// Command firn is the installer for snosi images. Phase 1 ships the
// recipe validator; the install pipeline and TUI arrive in later
// phases (docs/plans/roadmap.md).
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
  firn validate [flags] <recipe.toml>   validate a recipe against this machine
  firn version                          print the firn version

Validate flags:
  --secure-boot auto|on|off   override Secure Boot detection (default auto)
  --tpm auto|on|off           override TPM detection (default auto)
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "validate":
		return runValidate(args[1:])
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
