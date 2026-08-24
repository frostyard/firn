package luks

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/runner"
)

// call records one intercepted command invocation, including any stdin
// fed via runner.RunInput.
type call struct {
	name  string
	args  []string
	stdin string
}

type recorder struct {
	calls []call
	err   error // returned for every call; nil means success
}

func (rec *recorder) runner() *runner.Runner {
	return runner.NewFake(
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			c := call{name: name, args: args}
			if in, ok := runner.Stdin(ctx); ok {
				c.stdin = in
			}
			rec.calls = append(rec.calls, c)
			return nil, rec.err
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
}

func assertCall(t *testing.T, rec *recorder, i int, name string, args ...string) {
	t.Helper()
	if i >= len(rec.calls) {
		t.Fatalf("expected call %d (%s %v), but only %d calls made: %+v", i, name, args, len(rec.calls), rec.calls)
	}
	c := rec.calls[i]
	if c.name != name || !slices.Equal(c.args, args) {
		t.Errorf("call %d = %s %v, want %s %v", i, c.name, c.args, name, args)
	}
}

// noStaleMapper makes the stale-mapper probe report that no
// /dev/mapper node exists.
func noStaleMapper(t *testing.T) {
	t.Helper()
	old := stat
	stat = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	t.Cleanup(func() { stat = old })
}

// staleMapper makes the stale-mapper probe report that path exists.
func staleMapper(t *testing.T, path string) {
	t.Helper()
	old := stat
	stat = func(p string) (os.FileInfo, error) {
		if p == path {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() { stat = old })
}

func TestMapperPath(t *testing.T) {
	if got := MapperPath("firn-root"); got != "/dev/mapper/firn-root" {
		t.Errorf("MapperPath = %q, want /dev/mapper/firn-root", got)
	}
}

func TestFormat(t *testing.T) {
	const dev = "/dev/sda3"
	const pass = "hunter2"

	rec := &recorder{}
	if err := Format(context.Background(), rec.runner(), dev, pass); err != nil {
		t.Fatalf("Format: %v", err)
	}

	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "cryptsetup", "luksFormat", "--batch-mode", "--type=luks2", "--key-file=-", dev)
	if rec.calls[0].stdin != pass {
		t.Errorf("stdin = %q, want passphrase %q", rec.calls[0].stdin, pass)
	}
	// Passphrase must not appear in the argv (process table is public).
	for _, arg := range rec.calls[0].args {
		if strings.Contains(arg, pass) {
			t.Errorf("passphrase leaked into argv: %q", arg)
		}
	}
}

func TestOpen(t *testing.T) {
	const dev = "/dev/sda3"
	const pass = "hunter2"
	const mapper = "firn-root"

	noStaleMapper(t)
	rec := &recorder{}
	got, err := Open(context.Background(), rec.runner(), dev, mapper, pass)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != "/dev/mapper/firn-root" {
		t.Errorf("Open returned %q, want /dev/mapper/firn-root", got)
	}

	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "cryptsetup", "luksOpen", "--key-file=-", dev, mapper)
	if rec.calls[0].stdin != pass {
		t.Errorf("stdin = %q, want passphrase %q", rec.calls[0].stdin, pass)
	}
	for _, arg := range rec.calls[0].args {
		if strings.Contains(arg, pass) {
			t.Errorf("passphrase leaked into argv: %q", arg)
		}
	}
}

// TestOpenClosesStaleMapper: a previous interrupted run may have left
// the mapper open; Open must luksClose it before luksOpen so the open
// succeeds cleanly.
func TestOpenClosesStaleMapper(t *testing.T) {
	const mapper = "firn-root"

	staleMapper(t, "/dev/mapper/"+mapper)
	rec := &recorder{}
	if _, err := Open(context.Background(), rec.runner(), "/dev/sda3", mapper, "pass"); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if len(rec.calls) != 2 {
		t.Fatalf("expected luksClose then luksOpen, got %d calls: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "cryptsetup", "luksClose", mapper)
	assertCall(t, rec, 1, "cryptsetup", "luksOpen", "--key-file=-", "/dev/sda3", mapper)
}

// TestOpenStaleMapperCloseFailure: when the stale-mapper luksClose
// fails, Open must surface that failure immediately and never attempt
// luksOpen against a mapper that failed to close.
func TestOpenStaleMapperCloseFailure(t *testing.T) {
	const mapper = "firn-root"
	closeErr := errors.New("cryptsetup: device or resource busy")

	staleMapper(t, "/dev/mapper/"+mapper)
	var calls []call
	r := runner.NewFake(
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			c := call{name: name, args: args}
			if in, ok := runner.Stdin(ctx); ok {
				c.stdin = in
			}
			calls = append(calls, c)
			if len(args) > 0 && args[0] == "luksClose" {
				return nil, closeErr
			}
			return nil, nil
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)

	_, err := Open(context.Background(), r, "/dev/sda3", mapper, "pass")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, closeErr) {
		t.Errorf("Open error = %v, want it to wrap %v", err, closeErr)
	}
	if len(calls) != 1 {
		t.Fatalf("expected only luksClose to be attempted, got %d calls: %+v", len(calls), calls)
	}
	assertCall(t, &recorder{calls: calls}, 0, "cryptsetup", "luksClose", mapper)
}

func TestClose(t *testing.T) {
	rec := &recorder{}
	if err := Close(context.Background(), rec.runner(), "firn-root"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "cryptsetup", "luksClose", "firn-root")
}

func TestFormatErrorPropagation(t *testing.T) {
	rec := &recorder{err: errors.New("cryptsetup: device busy")}
	if err := Format(context.Background(), rec.runner(), "/dev/sda3", "pass"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestOpenErrorPropagation(t *testing.T) {
	noStaleMapper(t)
	rec := &recorder{err: errors.New("cryptsetup: no key available")}
	if _, err := Open(context.Background(), rec.runner(), "/dev/sda3", "mapper", "pass"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGenerateRecoveryKey(t *testing.T) {
	k1, err := GenerateRecoveryKey()
	if err != nil {
		t.Fatalf("GenerateRecoveryKey: %v", err)
	}
	k2, err := GenerateRecoveryKey()
	if err != nil {
		t.Fatalf("GenerateRecoveryKey: %v", err)
	}

	// Must be 64 hex characters (32 bytes encoded as hex).
	if len(k1) != 64 {
		t.Errorf("key length = %d, want 64", len(k1))
	}
	for i, c := range k1 {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("key[%d] = %q is not lowercase hex", i, c)
			break
		}
	}
	// Two calls must not return the same value.
	if k1 == k2 {
		t.Error("GenerateRecoveryKey returned the same value twice (collision or non-random)")
	}
}
