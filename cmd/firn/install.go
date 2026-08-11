package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/platform"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/runner"
	"github.com/frostyard/firn/internal/steps"
	"github.com/frostyard/firn/internal/trust"
)

func runInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "validate, assemble, and run preflight only")
	confirm := fs.String("confirm", "", "typed confirmation: must equal the recipe's target disk path")
	jsonProgress := fs.Bool("json-progress", false, "emit NDJSON progress events on stdout")
	sb := fs.String("secure-boot", "auto", "auto|on|off")
	tpm := fs.String("tpm", "auto", "auto|on|off")
	uefi := fs.String("uefi", "auto", "auto|on|off")
	pubring := fs.String("pubring", "", "OpenPGP keyring for A/B index verification (default: search known locations)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch fs.NArg() {
	case 0:
		// No recipe path: launch the TUI wizard (ADR-0007) with this
		// invocation's platform overrides. --dry-run and --confirm are
		// headless-only concepts and would be silently meaningless here.
		if *dryRun {
			fmt.Fprint(os.Stderr, "firn install: --dry-run requires a recipe path\n")
			return 2
		}
		if *confirm != "" {
			fmt.Fprint(os.Stderr, "firn install: --confirm applies to headless installs; the TUI wizard asks for typed confirmation itself\n")
			return 2
		}
		return runTUI(tuiOptions{
			secureBoot:   *sb,
			tpm:          *tpm,
			uefi:         *uefi,
			jsonProgress: *jsonProgress,
			pubring:      *pubring,
		})
	case 1:
	default:
		fmt.Fprint(os.Stderr, "firn install: at most one recipe path is allowed\n")
		return 2
	}

	env := &pipeline.Env{
		Machine: recipe.Env{ZoneinfoDir: "/usr/share/zoneinfo"},
		Runner:  runner.New(),
		Version: version,
		Trust:   trust.Options{PubringPath: *pubring},
	}
	var err error
	if env.Machine.SecureBoot, err = tristate(*sb, platform.SecureBoot); err != nil {
		fmt.Fprintf(os.Stderr, "firn install: --secure-boot: %v\n", err)
		return 2
	}
	if env.Machine.TPM, err = tristate(*tpm, platform.TPM); err != nil {
		fmt.Fprintf(os.Stderr, "firn install: --tpm: %v\n", err)
		return 2
	}
	if env.UEFI, err = tristate(*uefi, platform.UEFI); err != nil {
		fmt.Fprintf(os.Stderr, "firn install: --uefi: %v\n", err)
		return 2
	}

	l, err := recipe.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if issues := recipe.Validate(l, env.Machine); len(issues) > 0 {
		for _, is := range issues {
			fmt.Fprintf(os.Stderr, "%v\n", is)
		}
		fmt.Fprintf(os.Stderr, "firn: recipe is invalid (%d issue(s))\n", len(issues))
		return 1
	}
	env.Recipe = &l.Recipe

	// Destructive installs demand typed confirmation of the exact
	// target disk — snosi-install's rule, applied on every path
	// (docs/design/architecture.md, Operational notes). The TUI path
	// (tui.go) intentionally bypasses this flag: the wizard's
	// typed-confirmation page makes the user type the same disk path
	// interactively, satisfying the same rule.
	if !*dryRun && *confirm != l.Recipe.Target.Disk {
		fmt.Fprintf(os.Stderr, "firn install: destructive install requires --confirm %s (typed confirmation of the target disk)\n", l.Recipe.Target.Disk)
		return 2
	}

	if *jsonProgress {
		nd := progress.NewNDJSON(os.Stdout)
		env.Emit = func(e progress.Event) { _ = nd.Emit(e) }
	} else {
		env.Emit = printEvent
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	p := steps.Assemble(l)
	if err := p.Run(ctx, env, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "firn: %v\n", err)
		return 1
	}
	if *dryRun {
		fmt.Fprintf(os.Stderr, "firn: dry run complete — preflight passed, %d steps assembled, no disks touched\n", len(p.Steps))
	}
	return 0
}

// printEvent renders progress events for humans on stderr, keeping
// stdout clean.
func printEvent(e progress.Event) {
	switch ev := e.(type) {
	case progress.Start:
		fmt.Fprintf(os.Stderr, "firn %s — %d steps:\n", ev.Firn, len(ev.Steps))
		for i, s := range ev.Steps {
			fmt.Fprintf(os.Stderr, "  %2d. %s (weight %d)\n", i+1, s.Name, s.Weight)
		}
	case progress.StepStart:
		fmt.Fprintf(os.Stderr, "→ %s\n", ev.Name)
	case progress.Info:
		fmt.Fprintf(os.Stderr, "  %s\n", ev.Message)
	case progress.Warning:
		fmt.Fprintf(os.Stderr, "  warning [%s]: %s\n", ev.Code, ev.Message)
	case progress.Summary:
		for _, item := range ev.Items {
			fmt.Fprintf(os.Stderr, "  summary [%s]: %s\n", item.Code, item.Detail)
		}
	case progress.RecoveryKey:
		fmt.Fprintf(os.Stderr, "RECOVERY KEY (store it safely): %s\n", ev.Key)
	case progress.Error:
		fmt.Fprintf(os.Stderr, "error in %s [%s]: %s\n", ev.Step, ev.Code, ev.Message)
	case progress.Done:
		fmt.Fprint(os.Stderr, "done\n")
	}
}
