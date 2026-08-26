// Package runner is the single seam through which firn invokes host
// tools. Tests replace Exec/LookPath; production code never calls
// os/exec directly outside this package.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Runner executes host commands. The zero value is not usable; use New.
type Runner struct {
	exec     func(ctx context.Context, name string, args ...string) ([]byte, error)
	lookPath func(name string) (string, error)
	sleep    func(d time.Duration)
}

// New returns a Runner backed by os/exec.
func New() *Runner {
	return &Runner{
		exec: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, name, args...)
			if input, ok := Stdin(ctx); ok {
				cmd.Stdin = strings.NewReader(input)
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if in, out, ok := Stream(ctx); ok {
				cmd.Stdin = in
				cmd.Stdout = out
			}
			if err := cmd.Run(); err != nil {
				return stdout.Bytes(), fmt.Errorf("%s: %w (stderr: %s)", name, err, bytes.TrimSpace(stderr.Bytes()))
			}
			return stdout.Bytes(), nil
		},
		lookPath: exec.LookPath,
		sleep:    time.Sleep,
	}
}

// NewFake returns a Runner with injected behavior, for tests. Fake
// runners never sleep; tests that assert backoff install a recorder
// with WithSleep.
func NewFake(
	execFn func(ctx context.Context, name string, args ...string) ([]byte, error),
	lookPathFn func(name string) (string, error),
) *Runner {
	return &Runner{exec: execFn, lookPath: lookPathFn, sleep: func(time.Duration) {}}
}

// Run executes name with args and returns its stdout.
func (r *Runner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return r.exec(ctx, name, args...)
}

// Sleep pauses for d between retry attempts through the same seam
// tests replace for command execution, so retrying callers need no
// per-package sleep hook.
func (r *Runner) Sleep(d time.Duration) {
	r.sleep(d)
}

// WithSleep returns a copy of r whose Sleep calls f instead. Tests use
// it to record backoff durations without real delays.
func (r *Runner) WithSleep(f func(d time.Duration)) *Runner {
	c := *r
	c.sleep = f
	return &c
}

// stdinKey carries RunInput's input through the context so the exec
// seam's signature (and NewFake's) stays unchanged.
type stdinKey struct{}

// RunInput executes name with args, feeding input to its stdin, and
// returns its stdout. Mirrors Run otherwise. Fake exec functions
// recover the input with Stdin.
func (r *Runner) RunInput(ctx context.Context, input string, name string, args ...string) ([]byte, error) {
	return r.exec(context.WithValue(ctx, stdinKey{}, input), name, args...)
}

// Stdin reports the stdin input attached by RunInput, if any.
func Stdin(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(stdinKey{}).(string)
	return s, ok
}

// streamKey carries RunStream's stdin/stdout pair through the context
// so the exec seam's signature (and NewFake's) stays unchanged.
type streamKey struct{}

// streamIO is the reader/writer pair RunStream attaches to the context.
type streamIO struct {
	stdin  io.Reader
	stdout io.Writer
}

// RunStream executes name with args, streaming stdin from a reader and
// stdout to a writer instead of buffering them in memory. Stderr is
// captured into the returned error like Run. Fake exec functions
// recover the pair with Stream.
func (r *Runner) RunStream(ctx context.Context, stdin io.Reader, stdout io.Writer, name string, args ...string) error {
	_, err := r.exec(context.WithValue(ctx, streamKey{}, streamIO{stdin: stdin, stdout: stdout}), name, args...)
	return err
}

// Stream reports the stdin reader and stdout writer attached by
// RunStream, if any.
func Stream(ctx context.Context) (io.Reader, io.Writer, bool) {
	s, ok := ctx.Value(streamKey{}).(streamIO)
	return s.stdin, s.stdout, ok
}

// LookPath reports where name resolves on PATH.
func (r *Runner) LookPath(name string) (string, error) {
	return r.lookPath(name)
}
