// TUI entry point: bare `firn` (or `firn install` with no recipe path)
// runs the wizard, writes the generated recipe to /run/firn, and drives
// the install pipeline in-process with progress over a Go channel
// (ADR-0007 — no subprocess seam, one binary).
package firn

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/BurntSushi/toml"

	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/platform"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/runner"
	"github.com/frostyard/firn/internal/steps"
	"github.com/frostyard/firn/internal/trust"
	"github.com/frostyard/firn/internal/tui"
)

// tuiOptions carries CLI overrides into the TUI flow. Bare `firn` uses
// all-auto; `firn install` with no recipe path passes its flags through
// so the same overrides work on both spellings.
type tuiOptions struct {
	secureBoot string // tristate: auto|on|off
	tpm        string
	uefi       string
	pubring    string
}

// recipeDir is where the wizard's generated recipe artifact lands.
// Under /run so it never survives a reboot of the installer
// environment; 0700/0600 because it can carry a password hash and
// SSH keys.
const recipeDir = "/run/firn"

func runTUI(parent context.Context, o tuiOptions) error {
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	env := &pipeline.Env{
		Machine: recipe.Env{ZoneinfoDir: "/usr/share/zoneinfo"},
		Runner:  runner.New(),
		Version: Version,
		Trust:   trust.Options{PubringPath: o.pubring},
	}
	var err error
	if env.Machine.SecureBoot, err = tristate(o.secureBoot, platform.SecureBoot); err != nil {
		return tui.HoldError(ctx, "installer setup failed", fmt.Errorf("--secure-boot: %w", err))
	}
	if env.Machine.TPM, err = tristate(o.tpm, platform.TPM); err != nil {
		return tui.HoldError(ctx, "installer setup failed", fmt.Errorf("--tpm: %w", err))
	}
	if env.UEFI, err = tristate(o.uefi, platform.UEFI); err != nil {
		return tui.HoldError(ctx, "installer setup failed", fmt.Errorf("--uefi: %w", err))
	}

	// The catalog is a convenience, not a gate: a load problem is a
	// note on stderr and the wizard falls back to built-ins.
	catalog, warn := tui.LoadCatalog()
	var notices []string
	if warn != nil {
		fmt.Fprintf(os.Stderr, "firn: note: %v\n", warn)
		notices = append(notices, warn.Error())
	}

	r, err := tui.RunWizard(ctx, tui.WizardOpts{
		Runner:  env.Runner,
		Machine: env.Machine,
		UEFI:    env.UEFI,
		Catalog: catalog,
		Notices: notices,
	})
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		return tui.HoldError(ctx, "installer setup failed", err)
	}
	if r == nil {
		return nil // user quit the wizard; nothing was touched
	}

	path, l, err := writeRecipe(r, env.Machine)
	if err != nil {
		return tui.HoldError(ctx, "could not prepare the install", err)
	}
	env.Recipe = &l.Recipe

	// No typed --confirm on this path: the wizard's typed-confirmation
	// page already made the user type the exact target disk path — the
	// same rule the install command enforces via --confirm for headless
	// runs (docs/design/architecture.md, Operational notes).

	// The in-process channel is the interactive flow's only progress
	// consumer (ADR-0007). NDJSON is headless-only because the TUI owns
	// stdout for terminal rendering.
	ch := progress.NewChannel(64)
	env.Emit = func(e progress.Event) { _ = ch.Emit(e) }

	ictx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := steps.Assemble(l)
	pipeDone := make(chan error, 1)
	go func() {
		defer ch.Close()
		pipeDone <- p.Run(ictx, env, false)
	}()

	res, uiErr := tui.RunInstall(ctx, ch.Events(), cancel)
	if uiErr != nil {
		// The view died; stop the pipeline so its unwind releases
		// mounts and mappers before we exit.
		cancel()
	}
	// Drain any events emitted after the view stopped consuming, then
	// wait for the pipeline's cleanup unwind to finish.
	go func() {
		for range ch.Events() {
		}
	}()
	<-pipeDone

	var runErr error
	switch {
	case uiErr != nil:
		runErr = uiErr
	case res.Done:
		if res.RecoveryKey != "" {
			// Mirror headless printEvent: keep the key in scrollback
			// after the TUI's alternate screen is gone.
			fmt.Fprintf(os.Stderr, "RECOVERY KEY (store it safely): %s\n", res.RecoveryKey)
		}
	case res.Failed:
		runErr = fmt.Errorf("install failed at %s: %s", res.FailedStep, res.ErrorMessage)
	default:
		runErr = fmt.Errorf("install cancelled")
	}
	fmt.Fprintf(os.Stderr, "firn: generated recipe saved at %s\n", path)
	fmt.Fprintf(os.Stderr, "firn: reproduce headless with: firn install --confirm %s %s\n",
		l.Recipe.Target.Disk, path)
	return runErr
}

// writeRecipe persists the wizard's recipe and re-validates the FILE:
// the written artifact is what must validate (recipe-schema spec rule
// 5), and it is exactly what the printed reproduce-headless command
// will consume. Any failure here is a firn bug — the wizard generated
// something the schema rejects — never a user error.
func writeRecipe(r *recipe.Recipe, machine recipe.Env) (string, *recipe.Loaded, error) {
	data, err := toml.Marshal(*r)
	if err != nil {
		return "", nil, fmt.Errorf("BUG: cannot marshal the wizard's recipe: %w", err)
	}
	if err := os.MkdirAll(recipeDir, 0o700); err != nil {
		return "", nil, err
	}
	path := filepath.Join(recipeDir, fmt.Sprintf("recipe-%d.toml", os.Getpid()))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", nil, err
	}
	l, err := recipe.Load(path)
	if err != nil {
		return "", nil, fmt.Errorf("BUG: the wizard wrote an unloadable recipe: %w", err)
	}
	if issues := recipe.Validate(l, machine); len(issues) > 0 {
		for _, is := range issues {
			fmt.Fprintf(os.Stderr, "%v\n", is)
		}
		return "", nil, fmt.Errorf("BUG: the wizard generated an invalid recipe (%d issue(s), kept at %s)", len(issues), path)
	}
	return path, l, nil
}
