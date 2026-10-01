package firn

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/runner"
	"github.com/frostyard/firn/internal/steps"
)

// assembleInstall is the command's assembly seam; tests supply a harmless
// pipeline to exercise the real command and progress emission without disks.
var assembleInstall = steps.Assemble

// deprecationEmitter keeps Start first in the progress protocol and sends
// each compatibility warning before the first step event.
type deprecationEmitter struct {
	progress.Emitter
	issues []recipe.Issue
}

func (e deprecationEmitter) Emit(event progress.Event) error {
	if err := e.Emitter.Emit(event); err != nil {
		return err
	}
	if _, ok := event.(progress.Start); ok {
		for _, issue := range e.issues {
			if err := e.Emitter.Emit(progress.Warning{Code: progress.CodeRecipeV1Deprecated, Message: issue.Message}); err != nil {
				return err
			}
		}
	}
	return nil
}

func newInstallCmd() *cobra.Command {
	var (
		probes       probeFlags
		dryRun       bool
		confirm      string
		jsonProgress bool
	)
	cmd := &cobra.Command{
		Use:   "install [recipe.toml]",
		Short: "Install a snosi image; with no recipe, launch the wizard",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateInstallMode(len(args) == 1, dryRun, confirm, jsonProgress); err != nil {
				return err
			}
			if len(args) == 0 {
				// No recipe path: the TUI wizard (ADR-0007) with this
				// invocation's platform overrides. Headless-only flags were
				// rejected by validateInstallMode before entering the wizard.
				return runTUI(cmd.Context(), tuiOptions(probes))
			}

			env := &pipeline.Env{
				Machine: recipe.Env{ZoneinfoDir: "/usr/share/zoneinfo"},
				Runner:  runner.New(),
				Version: Version,
			}
			var err error
			if env.Machine.SecureBoot, env.Machine.TPM, env.UEFI, err = probes.resolve(); err != nil {
				return err
			}

			l, err := recipe.Load(args[0])
			if err != nil {
				return err
			}
			if issues := recipe.Validate(l, env.Machine); len(issues) > 0 {
				for _, is := range issues {
					fmt.Fprintf(os.Stderr, "%v\n", is)
				}
				return fmt.Errorf("recipe is invalid (%d issue(s))", len(issues))
			}
			env.Recipe = &l.Recipe

			// Destructive installs demand typed confirmation of the
			// exact target disk — snosi-install's rule, applied on every
			// path (docs/design/architecture.md, Operational notes). The
			// wizard path enforces the same rule with its
			// typed-confirmation page.
			if !dryRun && confirm != l.Recipe.Target.Disk {
				return fmt.Errorf("destructive install requires --confirm %s (typed confirmation of the target disk)", l.Recipe.Target.Disk)
			}

			if jsonProgress {
				env.Emitter = progress.NewNDJSON(os.Stdout)
			} else {
				env.Emitter = progress.EmitterFunc(func(e progress.Event) error {
					printEvent(e)
					return nil
				})
			}
			env.Emitter = deprecationEmitter{Emitter: env.Emitter, issues: recipe.Deprecations(l)}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			p := assembleInstall(l)
			if err := p.Run(ctx, env, dryRun); err != nil {
				return err
			}
			if dryRun {
				fmt.Fprintf(os.Stderr, "firn: dry run complete — preflight passed, %d steps assembled, no disks touched\n", len(p.Steps))
			}
			return nil
		},
	}
	probes.register(cmd, true)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate, assemble, and run preflight only")
	cmd.Flags().StringVar(&confirm, "confirm", "", "typed confirmation: must equal the recipe's target disk path")
	cmd.Flags().BoolVar(&jsonProgress, "json-progress", false, "emit NDJSON progress events on stdout (requires recipe path)")
	return cmd
}

// validateInstallMode keeps flags whose output or safety contract is
// headless-only from entering the interactive wizard. In particular, the
// wizard renders terminal frames to stdout, while --json-progress promises
// that stdout is an NDJSON-only stream under the progress protocol.
func validateInstallMode(hasRecipe, dryRun bool, confirm string, jsonProgress bool) error {
	if hasRecipe {
		return nil
	}
	if dryRun {
		return fmt.Errorf("--dry-run requires a recipe path")
	}
	if confirm != "" {
		return fmt.Errorf("--confirm applies to headless installs; the wizard asks for typed confirmation itself")
	}
	if jsonProgress {
		return fmt.Errorf("--json-progress requires a recipe path; the interactive wizard renders terminal output to stdout")
	}
	return nil
}

// printEvent renders progress events for humans on stderr, keeping
// stdout clean.
func printEvent(e progress.Event) {
	printEventTo(os.Stderr, e)
}

func printEventTo(w io.Writer, e progress.Event) {
	switch ev := e.(type) {
	case progress.Start:
		fmt.Fprintf(w, "firn %s — %d steps:\n", ev.Firn, len(ev.Steps))
		for i, s := range ev.Steps {
			fmt.Fprintf(w, "  %2d. %s (weight %d)\n", i+1, s.Name, s.Weight)
		}
	case progress.StepStart:
		fmt.Fprintf(w, "→ %s\n", ev.Name)
	case progress.StepProgress:
		if ev.TotalBytes > 0 {
			fmt.Fprintf(w, "  progress: %d/%d bytes (%.0f%%)\n", ev.Bytes, ev.TotalBytes, float64(ev.Bytes)/float64(ev.TotalBytes)*100)
		} else {
			fmt.Fprintf(w, "  progress: %.0f%%\n", ev.Fraction*100)
		}
	case progress.Info:
		fmt.Fprintf(w, "  %s\n", ev.Message)
	case progress.Warning:
		fmt.Fprintf(w, "  warning [%s]: %s\n", ev.Code, ev.Message)
	case progress.Summary:
		for _, item := range ev.Items {
			fmt.Fprintf(w, "  summary [%s]: %s\n", item.Code, item.Detail)
		}
	case progress.RecoveryKey:
		fmt.Fprintf(w, "RECOVERY KEY (store it safely): %s\n", ev.Key)
	case progress.Error:
		if ev.Step == "" {
			fmt.Fprintf(w, "error [%s]: %s\n", ev.Code, ev.Message)
		} else {
			fmt.Fprintf(w, "error in %s [%s]: %s\n", ev.Step, ev.Code, ev.Message)
		}
	case progress.Done:
		fmt.Fprint(w, "done\n")
	}
}
