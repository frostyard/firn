// Package runner is the single seam through which firn invokes host
// tools. Tests replace Exec/LookPath; production code never calls
// os/exec directly outside this package.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// Runner executes host commands. The zero value is not usable; use New.
type Runner struct {
	exec     func(ctx context.Context, name string, args ...string) ([]byte, error)
	lookPath func(name string) (string, error)
}

// New returns a Runner backed by os/exec.
func New() *Runner {
	return &Runner{
		exec: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				return stdout.Bytes(), fmt.Errorf("%s: %w (stderr: %s)", name, err, bytes.TrimSpace(stderr.Bytes()))
			}
			return stdout.Bytes(), nil
		},
		lookPath: exec.LookPath,
	}
}

// NewFake returns a Runner with injected behavior, for tests.
func NewFake(
	execFn func(ctx context.Context, name string, args ...string) ([]byte, error),
	lookPathFn func(name string) (string, error),
) *Runner {
	return &Runner{exec: execFn, lookPath: lookPathFn}
}

// Run executes name with args and returns its stdout.
func (r *Runner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return r.exec(ctx, name, args...)
}

// LookPath reports where name resolves on PATH.
func (r *Runner) LookPath(name string) (string, error) {
	return r.lookPath(name)
}
