// TUI entry point: bare `firn` (or `firn install` with no recipe path)
// runs the wizard, writes the generated recipe to /run/firn, and drives
// the install pipeline in-process with progress over a Go channel
// (ADR-0007 — no subprocess seam, one binary).
package main

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
	secureBoot   string // tristate: auto|on|off
	tpm          string
	uefi         string
	jsonProgress bool
	pubring      string
}

// recipeDir is where the wizard's generated recipe artifact lands.
// Under /run so it never survives a reboot of the installer
// environment; 0700/0600 because it can carry a password hash and
// SSH keys.
const recipeDir = "/run/firn"

func runTUI(o tuiOptions) int {
	env := &pipeline.Env{
		Machine: recipe.Env{ZoneinfoDir: "/usr/share/zoneinfo"},
		Runner:  runner.New(),
		Version: version,
		Trust:   trust.Options{PubringPath: o.pubring},
	}
	var err error
	if env.Machine.SecureBoot, err = tristate(o.secureBoot, platform.SecureBoot); err != nil {
		fmt.Fprintf(os.Stderr, "firn: --secure-boot: %v\n", err)
		return 2
	}
	if env.Machine.TPM, err = tristate(o.tpm, platform.TPM); err != nil {
		fmt.Fprintf(os.Stderr, "firn: --tpm: %v\n", err)
		return 2
	}
	if env.UEFI, err = tristate(o.uefi, platform.UEFI); err != nil {
		fmt.Fprintf(os.Stderr, "firn: --uefi: %v\n", err)
		return 2
	}

	// The catalog is a convenience, not a gate: a load problem is a
	// note on stderr and the wizard falls back to manual entry.
	catalog, warn := tui.LoadCatalog()
	if warn != nil {
		fmt.Fprintf(os.Stderr, "firn: note: %v\n", warn)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	r, err := tui.RunWizard(ctx, tui.WizardOpts{
		Runner:  env.Runner,
		Machine: env.Machine,
		UEFI:    env.UEFI,
		Catalog: catalog,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "firn: %v\n", err)
		return 1
	}
	if r == nil {
		return 0 // user quit the wizard; nothing was touched
	}

	path, l, ok := writeRecipe(r, env.Machine)
	if !ok {
		return 1
	}
	env.Recipe = &l.Recipe

	// No typed --confirm on this path: the wizard's typed-confirmation
	// page already made the user type the exact target disk path — the
	// same rule install.go enforces via --confirm for headless runs
	// (docs/design/architecture.md, Operational notes).

	// Two progress consumers (ADR-0007): the in-process channel feeds
	// the install view; --json-progress additionally mirrors the
	// spec'd NDJSON stream to stdout for external consumers.
	ch := progress.NewChannel(64)
	emit := func(e progress.Event) { _ = ch.Emit(e) }
	if o.jsonProgress {
		nd := progress.NewNDJSON(os.Stdout)
		chEmit := emit
		emit = func(e progress.Event) {
			_ = nd.Emit(e)
			chEmit(e)
		}
	}
	env.Emit = emit

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

	rc := 0
	switch {
	case uiErr != nil:
		fmt.Fprintf(os.Stderr, "firn: %v\n", uiErr)
		rc = 1
	case res.Done:
		if res.RecoveryKey != "" {
			// Mirror headless printEvent: keep the key in scrollback
			// after the TUI's alternate screen is gone.
			fmt.Fprintf(os.Stderr, "RECOVERY KEY (store it safely): %s\n", res.RecoveryKey)
		}
	case res.Failed:
		fmt.Fprintf(os.Stderr, "firn: install failed at %s: %s\n", res.FailedStep, res.ErrorMessage)
		rc = 1
	default:
		fmt.Fprint(os.Stderr, "firn: install cancelled\n")
		rc = 1
	}
	fmt.Fprintf(os.Stderr, "firn: generated recipe saved at %s\n", path)
	fmt.Fprintf(os.Stderr, "firn: reproduce headless with: firn install --confirm %s %s\n",
		l.Recipe.Target.Disk, path)
	return rc
}

// writeRecipe persists the wizard's recipe and re-validates the FILE:
// the written artifact is what must validate (recipe-schema spec rule
// 5), and it is exactly what the printed reproduce-headless command
// will consume. Any failure here is a firn bug — the wizard generated
// something the schema rejects — never a user error.
func writeRecipe(r *recipe.Recipe, machine recipe.Env) (string, *recipe.Loaded, bool) {
	data, err := toml.Marshal(*r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "firn: BUG: cannot marshal the wizard's recipe: %v\n", err)
		return "", nil, false
	}
	if err := os.MkdirAll(recipeDir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "firn: %v\n", err)
		return "", nil, false
	}
	path := filepath.Join(recipeDir, fmt.Sprintf("recipe-%d.toml", os.Getpid()))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "firn: %v\n", err)
		return "", nil, false
	}
	l, err := recipe.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "firn: BUG: the wizard wrote an unloadable recipe: %v\n", err)
		return "", nil, false
	}
	if issues := recipe.Validate(l, machine); len(issues) > 0 {
		for _, is := range issues {
			fmt.Fprintf(os.Stderr, "%v\n", is)
		}
		fmt.Fprintf(os.Stderr, "firn: BUG: the wizard generated an invalid recipe (%d issue(s), kept at %s)\n",
			len(issues), path)
		return "", nil, false
	}
	return path, l, true
}
