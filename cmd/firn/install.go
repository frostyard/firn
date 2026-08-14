package firn

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/runner"
	"github.com/frostyard/firn/internal/steps"
	"github.com/frostyard/firn/internal/trust"
)

func newInstallCmd() *cobra.Command {
	var (
		probes       probeFlags
		dryRun       bool
		confirm      string
		jsonProgress bool
		pubring      string
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
				return runTUI(cmd.Context(), tuiOptions{
					secureBoot: probes.secureBoot,
					tpm:        probes.tpm,
					uefi:       probes.uefi,
					pubring:    pubring,
				})
			}

			env := &pipeline.Env{
				Machine: recipe.Env{ZoneinfoDir: "/usr/share/zoneinfo"},
				Runner:  runner.New(),
				Version: Version,
				Trust:   trust.Options{PubringPath: pubring},
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
				nd := progress.NewNDJSON(os.Stdout)
				env.Emit = func(e progress.Event) { _ = nd.Emit(e) }
			} else {
				env.Emit = printEvent
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			p := steps.Assemble(l)
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
	cmd.Flags().StringVar(&pubring, "pubring", "", "OpenPGP keyring for A/B index verification (default: search known locations)")
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
