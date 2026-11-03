// Package pipeline is firn's step engine: an install is an assembled,
// ordered list of steps executed with progress events and a cleanup
// stack that unwinds on every exit path
// (docs/design/architecture.md, "The step engine").
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/frostyard/firn/internal/bootcimg"
	"github.com/frostyard/firn/internal/disk"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/runner"
)

// Step is one unit of install work. Steps are assembled once, up
// front; the list is inspectable (dry-run, progress totals) before
// anything runs.
type Step struct {
	// Name is the stable step identifier used in progress events.
	Name string
	// Weight is the step's share of overall progress.
	Weight int
	// Preflight steps only inspect; they run even in dry-run mode.
	Preflight bool
	// Destructive marks steps that modify the target disk.
	Destructive bool
	// Tools lists host binaries this step invokes. The preflight tool
	// check is derived from the assembled list, so it cannot drift
	// from the code.
	Tools []string
	// Run does the work. nil marks a step whose implementation is due
	// in a later roadmap phase; running it non-dry is an error.
	Run func(ctx context.Context, env *Env) error
}

// Env is the shared state steps read and write.
type Env struct {
	Recipe  *recipe.Recipe
	Machine recipe.Env
	// UEFI reports whether this machine booted via UEFI (ADR-0004
	// hardware floor); probed or overridden by the caller.
	UEFI    bool
	Version string
	Runner  *runner.Runner
	Emitter progress.Emitter
	// RegistryProbe, when set, lets preflight-image diagnose an
	// unreachable registry behind an image failure; nil disables probing.
	RegistryProbe bootcimg.Prober
	// InstallLockPath is the machine-wide install lock the pipeline takes
	// for a non-dry run. Empty means DefaultInstallLockPath; tests set a
	// temporary path.
	InstallLockPath string

	emitErr error

	// Install state shared between steps, populated as steps run.
	Layout disk.Layout
	// TargetDisk is the canonical whole-disk device path that
	// recipe target.disk names (preflight-disk resolves it; the first
	// destructive disk step resolves it lazily when preflight did not
	// run). Partitioning, the layout and the retag use it, so a
	// /dev/disk/by-id target gets /dev/sda1-style partition names;
	// user-facing text and --confirm keep the recipe's path as written.
	TargetDisk string
	RootDev    string // device holding the root fs (mapper path when encrypted)
	LuksKey    string // transient unlock key for TPM enrollment
	TargetDir  string // where the target filesystem tree is mounted
	ScratchDir string // disk-backed scratch space (podman tmp etc.)
	// BootcSourceRef is the source selected by preflight-image. When
	// cosign verification is requested, it is the verified immutable digest;
	// the original recipe ref remains the day-two tracking reference.
	BootcSourceRef string
	// CoreFlatpaks is the core Flatpak set preflight-image read from the
	// selected image's label when the recipe sets core_flatpaks (ADR-0018);
	// empty when the image publishes none.
	CoreFlatpaks []string
	// SecureImageRoot is an extracted tree of the secure image's
	// usr/lib/{snosi,shim} subtrees (secureboot.ExtractSecureImageRoot),
	// populated by bootc-install for a secure bootc install and consumed by
	// the esp-stage / mok-stage steps. Empty on non-secure installs.
	SecureImageRoot string
	Summary         []progress.SummaryItem

	// CurrentStep is the 0-based index of the running step, for
	// StepProgress events emitted from inside step Runs.
	CurrentStep int

	cleanup []cleanupEntry
}

// Emit sends one progress event and retains the first emitter failure. Step
// implementations may emit from callbacks that cannot return an error; Run
// checks the retained failure before starting another step and returns it to
// the caller after cleanup has unwound.
func (e *Env) Emit(event progress.Event) error {
	if e.emitErr != nil {
		return e.emitErr
	}
	if e.Emitter == nil {
		e.emitErr = errors.New("progress: emitter is not configured")
		return e.emitErr
	}
	if err := e.Emitter.Emit(event); err != nil {
		e.emitErr = fmt.Errorf("emit progress %s: %w", event.Kind(), err)
	}
	return e.emitErr
}

// AddSummary queues an item for the pre-terminal summary event.
func (e *Env) AddSummary(code, detail string) {
	e.Summary = append(e.Summary, progress.SummaryItem{Code: code, Detail: detail})
}

type cleanupEntry struct {
	name string
	fn   func() error
}

// Defer registers an undo (unmount, close mapper, remove scratch) that
// runs LIFO when the pipeline exits, success or failure. Undos must be
// idempotent: a failed install leaves no mounts and no open mappers.
func (e *Env) Defer(name string, fn func() error) {
	e.cleanup = append(e.cleanup, cleanupEntry{name: name, fn: fn})
}

// unwind runs the cleanup stack LIFO, reporting (not aborting on)
// individual failures. This closes the mount-leak class of bug
// documented in snosi-install.
func (e *Env) unwind() []error {
	var errs []error
	for i := len(e.cleanup) - 1; i >= 0; i-- {
		c := e.cleanup[i]
		if err := c.fn(); err != nil {
			errs = append(errs, fmt.Errorf("cleanup %s: %w", c.name, err))
		}
	}
	e.cleanup = nil
	return errs
}

// Pipeline is an assembled step list ready to run or inspect.
type Pipeline struct {
	Steps []Step
}

// DefaultInstallLockPath is where a non-dry firn run takes its exclusive
// install lock. The parent directory is created on demand; the file is never
// removed.
const DefaultInstallLockPath = "/run/firn/install.lock"

// installLockPath is the lock path this run uses: InstallLockPath when the
// caller set one, DefaultInstallLockPath otherwise.
func (e *Env) installLockPath() string {
	if e.InstallLockPath != "" {
		return e.InstallLockPath
	}
	return DefaultInstallLockPath
}

// acquireInstallLock takes an exclusive, non-blocking flock on path so only
// one firn install (headless, TUI, or GUI) writes disks on this machine at a
// time. It returns a release function that drops the lock by closing the file
// descriptor; closing is what releases the flock, so release is safe to call
// on every exit path and never removes the file. Go opens files with O_CLOEXEC,
// so child processes (bootc, mkfs, mount) do not inherit the locked
// descriptor and cannot hold the lock after firn exits.
//
// A lock already held by another process is a coded failure
// (CodeInstallInProgress) that names the lock file; every other failure is
// plain, so the run fails closed with step_failed and no disk is touched.
func acquireInstallLock(path string) (release func(), err error) {
	release = func() {}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return release, fmt.Errorf("take install lock %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return release, fmt.Errorf("take install lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return release, WithErrorCode(progress.CodeInstallInProgress, fmt.Errorf(
				"another firn install is already running on this machine (it holds %s); no disk was touched: wait for it to finish, then try again", path))
		}
		return release, fmt.Errorf("take install lock %s: %w", path, err)
	}
	return func() { f.Close() }, nil
}

type codedError struct {
	code string
	err  error
}

func (e codedError) Error() string { return e.err.Error() }
func (e codedError) Unwrap() error { return e.err }

// WithErrorCode marks err with the stable progress-protocol code the terminal
// error event should carry. Step context is still added by Pipeline.Run.
func WithErrorCode(code string, err error) error {
	if err == nil {
		return nil
	}
	return codedError{code: code, err: err}
}

func errorCode(err error) string {
	var coded codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return progress.CodeStepFailed
}

// ProgressSteps returns the step list in progress-protocol form.
func (p *Pipeline) ProgressSteps() []progress.Step {
	out := make([]progress.Step, len(p.Steps))
	for i, s := range p.Steps {
		out[i] = progress.Step{Name: s.Name, Weight: s.Weight}
	}
	return out
}

// Tools returns the deduplicated union of every assembled step's
// declared tools, in first-seen order.
func (p *Pipeline) Tools() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range p.Steps {
		for _, t := range s.Tools {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// Run executes the pipeline: Start event, the machine-wide install lock (a
// non-dry run only), steps in order, cleanup unwind on every exit path, any
// cleanup warnings, any accumulated summary, then exactly one terminal event
// (Done or Error), per the progress protocol spec. The lock is released only
// after cleanup has unwound, because cleanup still touches the disk; a run
// that cannot take it fails before the first step with an empty step and the
// install_in_progress code.
// In dry-run mode only preflight steps execute; the rest are announced
// and skipped, and no lock is taken.
func (p *Pipeline) Run(ctx context.Context, env *Env, dryRun bool) error {
	startErr := env.Emit(progress.Start{Protocol: progress.Protocol, Firn: env.Version, Steps: p.ProgressSteps()})

	failedStep, runErr := "", startErr
	releaseLock := func() {}
	if runErr == nil && !dryRun {
		var lockErr error
		if releaseLock, lockErr = acquireInstallLock(env.installLockPath()); lockErr != nil {
			// No step ran and no step_start was emitted: the failure is
			// run-level, so the terminal event carries an empty step.
			releaseLock = func() {}
			runErr = lockErr
		}
	}
	if runErr == nil {
		failedStep, runErr = p.runSteps(ctx, env, dryRun)
	}
	for _, cerr := range env.unwind() {
		if env.emitErr == nil {
			_ = env.Emit(progress.Warning{Code: progress.CodeCleanupFailed, Message: cerr.Error()})
		}
		runErr = errors.Join(runErr, cerr)
	}
	// Cleanup is done touching the disk; the lock is no longer needed and is
	// dropped before the summary and terminal events.
	releaseLock()
	if env.emitErr != nil && !errors.Is(runErr, env.emitErr) {
		runErr = errors.Join(runErr, env.emitErr)
	}
	if len(env.Summary) > 0 && env.emitErr == nil {
		if err := env.Emit(progress.Summary{Items: env.Summary}); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}
	if runErr != nil {
		// An emitter cannot be used to report its own failure. Other run
		// failures still receive the protocol's terminal error event.
		if env.emitErr == nil {
			if err := env.Emit(progress.Error{Step: failedStep, Code: errorCode(runErr), Message: runErr.Error()}); err != nil {
				runErr = errors.Join(runErr, err)
			}
		}
		return runErr
	}
	return env.Emit(progress.Done{OK: true})
}

func (p *Pipeline) runSteps(ctx context.Context, env *Env, dryRun bool) (string, error) {
	for i, s := range p.Steps {
		env.CurrentStep = i
		if err := env.Emit(progress.StepStart{Index: i, Name: s.Name}); err != nil {
			return s.Name, err
		}
		if dryRun && !s.Preflight {
			if err := env.Emit(progress.Info{Message: fmt.Sprintf("dry-run: skipping %s", s.Name)}); err != nil {
				return s.Name, err
			}
			continue
		}
		if s.Run == nil {
			return s.Name, fmt.Errorf("step %s: not implemented yet (see docs/plans/roadmap.md)", s.Name)
		}
		stepErr := s.Run(ctx, env)
		if env.emitErr != nil {
			if stepErr != nil {
				stepErr = fmt.Errorf("step %s: %w", s.Name, stepErr)
			}
			return s.Name, errors.Join(stepErr, env.emitErr)
		}
		if stepErr != nil {
			return s.Name, fmt.Errorf("step %s: %w", s.Name, stepErr)
		}
	}
	return "", nil
}
