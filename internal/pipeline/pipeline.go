// Package pipeline is firn's step engine: an install is an assembled,
// ordered list of steps executed with progress events and a cleanup
// stack that unwinds on every exit path
// (docs/design/architecture.md, "The step engine").
package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/frostyard/firn/internal/disk"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/runner"
	"github.com/frostyard/firn/internal/trust"
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
	Emit    func(progress.Event)

	// Install state shared between steps, populated as steps run.
	Layout     disk.Layout
	RootDev    string // device holding the root fs (mapper path when encrypted)
	LuksKey    string // transient unlock key (first-boot TPM staging / A/B enrollment)
	TargetDir  string // where the target filesystem tree is mounted
	ScratchDir string // disk-backed scratch space (podman tmp etc.)
	// BootcSourceRef is the source selected by preflight-image. When
	// cosign verification is requested, it is the verified immutable digest;
	// the original recipe ref remains the day-two tracking reference.
	BootcSourceRef string
	// SecureImageRoot is an extracted tree of the secure image's
	// usr/lib/{snosi,shim} subtrees (secureboot.ExtractSecureImageRoot),
	// populated by bootc-install for a secure bootc install and consumed by
	// the esp-stage / mok-stage steps. Empty on non-secure installs.
	SecureImageRoot string
	Summary         []progress.SummaryItem

	// A/B path state.
	Trust     trust.Options // origin/product/arch/pubring for artifact fetches
	ABIndex   *trust.Index  // signed, verified artifact index
	ABVersion string        // resolved release version (14 digits)
	ABSize    int64         // decompressed disk-image size in bytes
	VarDev    string        // var filesystem device (mapper path when encrypted)
	VarPart   string        // var partition node
	VarMount  string        // where the target /var filesystem is mounted
	RootMount string        // where the read-only erofs root is mounted
	// Recovery-key output reservation is created by A/B preflight before any
	// target write and retained only after the atomic key commit.
	RecoveryKeyOut     string
	RecoveryKeyTemp    string
	RecoveryKeyWritten bool

	// CurrentStep is the 0-based index of the running step, for
	// StepProgress events emitted from inside step Runs.
	CurrentStep int

	cleanup []cleanupEntry
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

// Run executes the pipeline: Start event, steps in order, cleanup unwind on
// every exit path, any cleanup warnings, any accumulated summary, then exactly
// one terminal event (Done or Error), per the progress protocol spec.
// In dry-run mode only preflight steps execute; the rest are announced
// and skipped.
func (p *Pipeline) Run(ctx context.Context, env *Env, dryRun bool) error {
	env.Emit(progress.Start{Protocol: progress.Protocol, Firn: env.Version, Steps: p.ProgressSteps()})

	failedStep, runErr := p.runSteps(ctx, env, dryRun)
	for _, cerr := range env.unwind() {
		env.Emit(progress.Warning{Code: progress.CodeCleanupFailed, Message: cerr.Error()})
		runErr = errors.Join(runErr, cerr)
	}
	if len(env.Summary) > 0 {
		env.Emit(progress.Summary{Items: env.Summary})
	}
	if runErr != nil {
		env.Emit(progress.Error{Step: failedStep, Code: errorCode(runErr), Message: runErr.Error()})
		return runErr
	}
	env.Emit(progress.Done{OK: true})
	return nil
}

func (p *Pipeline) runSteps(ctx context.Context, env *Env, dryRun bool) (string, error) {
	for i, s := range p.Steps {
		env.CurrentStep = i
		env.Emit(progress.StepStart{Index: i, Name: s.Name})
		if dryRun && !s.Preflight {
			env.Emit(progress.Info{Message: fmt.Sprintf("dry-run: skipping %s", s.Name)})
			continue
		}
		if s.Run == nil {
			return s.Name, fmt.Errorf("step %s: not implemented yet (see docs/plans/roadmap.md)", s.Name)
		}
		if err := s.Run(ctx, env); err != nil {
			return s.Name, fmt.Errorf("step %s: %w", s.Name, err)
		}
	}
	return "", nil
}
