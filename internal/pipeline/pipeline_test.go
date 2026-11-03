package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/frostyard/firn/internal/progress"
)

func collectEnv() (*Env, *[]progress.Event) {
	var events []progress.Event
	env := &Env{Version: "test", Emitter: progress.EmitterFunc(func(e progress.Event) error {
		events = append(events, e)
		return nil
	})}
	return env, &events
}

// lockEnv is collectEnv with the install lock pointed at a temporary path, so
// a non-dry run never needs /run/firn — which a non-root test user cannot
// create.
func lockEnv(t *testing.T) (*Env, *[]progress.Event) {
	t.Helper()
	env, events := collectEnv()
	env.InstallLockPath = filepath.Join(t.TempDir(), "install.lock")
	return env, events
}

// holdLock takes path's exclusive install lock from a separate open file
// description, exactly as a second firn process would, and returns the
// function that releases it.
func holdLock(t *testing.T, path string) func() {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		t.Fatalf("hold install lock %s: %v", path, err)
	}
	return func() { f.Close() }
}

// lockIsFree reports whether a new open file description can take the lock,
// which is what a later firn process would try.
func lockIsFree(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

type failWriter struct {
	writes int
	failAt int
	err    error
}

func (w *failWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return len(p), nil
}

func TestEmitFailureStopsBeforeNextStepAndUnwinds(t *testing.T) {
	writeErr := errors.New("writer failed")
	w := &failWriter{failAt: 3, err: writeErr}
	env := &Env{Version: "test", Emitter: progress.NewNDJSON(w),
		InstallLockPath: filepath.Join(t.TempDir(), "install.lock")}
	cleaned := false
	secondRan := false
	p := &Pipeline{Steps: []Step{
		{Name: "first", Run: func(_ context.Context, env *Env) error {
			env.Defer("first", func() error {
				cleaned = true
				return nil
			})
			_ = env.Emit(progress.Info{Message: "fails on this write"})
			return nil
		}},
		{Name: "second", Run: func(_ context.Context, _ *Env) error {
			secondRan = true
			return nil
		}},
	}}

	err := p.Run(context.Background(), env, false)
	if !errors.Is(err, writeErr) {
		t.Fatalf("Run error = %v, want writer failure", err)
	}
	if secondRan {
		t.Error("step after emitter failure must not run")
	}
	if !cleaned {
		t.Error("cleanup must unwind after emitter failure")
	}
	if w.writes != 3 {
		t.Fatalf("writer calls = %d, want 3 (failed emitter must not be reused)", w.writes)
	}
	if !lockIsFree(t, env.InstallLockPath) {
		t.Error("the install lock must be released after an emitter failure")
	}
}

func TestCleanupUnwindsLIFOOnFailure(t *testing.T) {
	env, _ := lockEnv(t)
	var order []string
	p := &Pipeline{Steps: []Step{
		{Name: "a", Run: func(_ context.Context, e *Env) error {
			e.Defer("undo-a", func() error { order = append(order, "undo-a"); return nil })
			return nil
		}},
		{Name: "b", Run: func(_ context.Context, e *Env) error {
			e.Defer("undo-b", func() error { order = append(order, "undo-b"); return nil })
			return errors.New("boom")
		}},
		{Name: "never", Run: func(_ context.Context, _ *Env) error {
			t.Error("step after failure must not run")
			return nil
		}},
	}}
	if err := p.Run(context.Background(), env, false); err == nil {
		t.Fatal("expected failure")
	}
	if len(order) != 2 || order[0] != "undo-b" || order[1] != "undo-a" {
		t.Errorf("cleanup order = %v, want [undo-b undo-a]", order)
	}
}

func TestCleanupRunsOnSuccessAndFailuresAreJoined(t *testing.T) {
	env, _ := lockEnv(t)
	ran := false
	p := &Pipeline{Steps: []Step{
		{Name: "a", Run: func(_ context.Context, e *Env) error {
			e.Defer("bad-undo", func() error { ran = true; return errors.New("undo failed") })
			return nil
		}},
	}}
	err := p.Run(context.Background(), env, false)
	if !ran {
		t.Error("cleanup must run on success too")
	}
	if err == nil {
		t.Error("failed cleanup must surface as an error")
	}
}

func TestTerminalEventIsLast(t *testing.T) {
	for _, fail := range []bool{true, false} {
		env, events := lockEnv(t)
		step := Step{Name: "s", Run: func(_ context.Context, e *Env) error {
			e.Defer("undo", func() error { return errors.New("undo broke") })
			if fail {
				return errors.New("boom")
			}
			return nil
		}}
		_ = (&Pipeline{Steps: []Step{step}}).Run(context.Background(), env, false)

		evs := *events
		if len(evs) == 0 {
			t.Fatal("no events")
		}
		last := evs[len(evs)-1].Kind()
		if fail && last != "error" {
			t.Errorf("fail=true: last event %q, want error", last)
		}
		if !fail && last != "done" && last != "error" {
			// success + failed cleanup is still a failed install.
			t.Errorf("fail=false: last event %q, want terminal", last)
		}
		if evs[0].Kind() != "start" {
			t.Errorf("first event %q, want start", evs[0].Kind())
		}
		for i, e := range evs[:len(evs)-1] {
			if e.Kind() == "done" || e.Kind() == "error" {
				t.Errorf("terminal event at index %d of %d", i, len(evs))
			}
		}
	}
}

func TestFailureEmitsCleanupWarningAndSummaryBeforeError(t *testing.T) {
	env, events := lockEnv(t)
	p := &Pipeline{Steps: []Step{{Name: "configure", Run: func(_ context.Context, e *Env) error {
		e.AddSummary(progress.CodeFlatpakUnreachable, "org.gnome.Foo")
		e.Defer("target", func() error { return errors.New("unmount failed") })
		return errors.New("configuration failed")
	}}}}

	if err := p.Run(context.Background(), env, false); err == nil {
		t.Fatal("expected failure")
	}
	evs := *events
	wantKinds := []string{"start", "step_start", "warning", "summary", "error"}
	if len(evs) != len(wantKinds) {
		t.Fatalf("events = %v, want kinds %v", eventKinds(evs), wantKinds)
	}
	for i, want := range wantKinds {
		if got := evs[i].Kind(); got != want {
			t.Fatalf("event %d kind = %q, want %q; all=%v", i, got, want, eventKinds(evs))
		}
	}
	warning, ok := evs[2].(progress.Warning)
	if !ok || warning.Code != progress.CodeCleanupFailed || !strings.Contains(warning.Message, "unmount failed") {
		t.Fatalf("cleanup warning = %#v", evs[2])
	}
	summary, ok := evs[3].(progress.Summary)
	if !ok || len(summary.Items) != 1 || summary.Items[0].Code != progress.CodeFlatpakUnreachable || summary.Items[0].Detail != "org.gnome.Foo" {
		t.Fatalf("summary = %#v", evs[3])
	}
	terminal, ok := evs[4].(progress.Error)
	if !ok || terminal.Step != "configure" || !strings.Contains(terminal.Message, "configuration failed") || !strings.Contains(terminal.Message, "unmount failed") {
		t.Fatalf("terminal error = %#v", evs[4])
	}
}

func eventKinds(events []progress.Event) []string {
	kinds := make([]string, len(events))
	for i, event := range events {
		kinds[i] = event.Kind()
	}
	return kinds
}

func TestDryRunExecutesOnlyPreflight(t *testing.T) {
	env, _ := collectEnv()
	var ran []string
	mk := func(name string, preflight bool) Step {
		return Step{Name: name, Preflight: preflight, Run: func(_ context.Context, _ *Env) error {
			ran = append(ran, name)
			return nil
		}}
	}
	p := &Pipeline{Steps: []Step{mk("pre", true), mk("work", false), {Name: "stub", Preflight: false, Run: nil}}}
	if err := p.Run(context.Background(), env, true); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(ran) != 1 || ran[0] != "pre" {
		t.Errorf("ran %v, want [pre]", ran)
	}
}

func TestNilRunStepFailsOutsideDryRun(t *testing.T) {
	env, _ := lockEnv(t)
	p := &Pipeline{Steps: []Step{{Name: "stub"}}}
	if err := p.Run(context.Background(), env, false); err == nil {
		t.Error("nil-Run step must fail a real run")
	}
}

func TestCodedErrorSetsTerminalProgressCode(t *testing.T) {
	env, events := lockEnv(t)
	p := &Pipeline{Steps: []Step{{Name: "verify", Run: func(_ context.Context, _ *Env) error {
		return WithErrorCode(progress.CodeImageVerifyFailed, errors.New("bad signature"))
	}}}}
	if err := p.Run(context.Background(), env, false); err == nil {
		t.Fatal("expected verification failure")
	}
	terminal, ok := (*events)[len(*events)-1].(progress.Error)
	if !ok {
		t.Fatalf("terminal event = %T, want progress.Error", (*events)[len(*events)-1])
	}
	if terminal.Step != "verify" || terminal.Code != progress.CodeImageVerifyFailed {
		t.Fatalf("terminal error = %+v", terminal)
	}
}

func TestToolsDeduplicated(t *testing.T) {
	p := &Pipeline{Steps: []Step{
		{Name: "a", Tools: []string{"sfdisk", "mount"}},
		{Name: "b", Tools: []string{"mount", "xz"}},
	}}
	got := p.Tools()
	want := []string{"sfdisk", "mount", "xz"}
	if len(got) != len(want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tools[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHeldLockFailsRunBeforeFirstStep(t *testing.T) {
	env, events := lockEnv(t)
	defer holdLock(t, env.InstallLockPath)()

	ran := false
	p := &Pipeline{Steps: []Step{{Name: "partition", Run: func(_ context.Context, _ *Env) error {
		ran = true
		return nil
	}}}}

	err := p.Run(context.Background(), env, false)
	if err == nil {
		t.Fatal("a held install lock must fail the run")
	}
	if code := errorCode(err); code != progress.CodeInstallInProgress {
		t.Errorf("error code = %q, want %q", code, progress.CodeInstallInProgress)
	}
	if ran {
		t.Error("no step may run while another process holds the install lock")
	}
	evs := *events
	if len(evs) < 2 {
		t.Fatalf("events = %v, want start and a terminal error", eventKinds(evs))
	}
	if evs[0].Kind() != "start" {
		t.Errorf("first event = %q, want start", evs[0].Kind())
	}
	for _, e := range evs {
		if e.Kind() == "step_start" {
			t.Errorf("events must carry no step_start, got %v", eventKinds(evs))
		}
	}
	terminal, ok := evs[len(evs)-1].(progress.Error)
	if !ok {
		t.Fatalf("terminal event = %T, want progress.Error", evs[len(evs)-1])
	}
	if terminal.Code != progress.CodeInstallInProgress || terminal.Step != "" {
		t.Errorf("terminal error = %+v, want code %q and empty step", terminal, progress.CodeInstallInProgress)
	}
	if !strings.Contains(terminal.Message, env.InstallLockPath) {
		t.Errorf("message %q must name the lock file %s", terminal.Message, env.InstallLockPath)
	}
}

func TestDryRunSucceedsWhileLockHeld(t *testing.T) {
	env, _ := lockEnv(t)
	defer holdLock(t, env.InstallLockPath)()

	ran := []string{}
	p := &Pipeline{Steps: []Step{
		{Name: "preflight-disk", Preflight: true, Run: func(_ context.Context, _ *Env) error {
			ran = append(ran, "preflight-disk")
			return nil
		}},
		{Name: "partition", Run: func(_ context.Context, _ *Env) error {
			ran = append(ran, "partition")
			return nil
		}},
	}}
	if err := p.Run(context.Background(), env, true); err != nil {
		t.Fatalf("dry run while the lock is held: %v", err)
	}
	if len(ran) != 1 || ran[0] != "preflight-disk" {
		t.Errorf("ran %v, want [preflight-disk]", ran)
	}
}

func TestLockHeldDuringSteps(t *testing.T) {
	env, _ := lockEnv(t)
	checked := false
	p := &Pipeline{Steps: []Step{{Name: "partition", Run: func(_ context.Context, e *Env) error {
		path := e.installLockPath()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		ferr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(ferr, syscall.EWOULDBLOCK) {
			t.Errorf("flock during a step = %v, want EWOULDBLOCK (lock must be held)", ferr)
		}
		checked = true
		return nil
	}}}}
	if err := p.Run(context.Background(), env, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !checked {
		t.Fatal("the step did not run")
	}
}

func TestLockHeldDuringCleanup(t *testing.T) {
	env, _ := lockEnv(t)
	heldInCleanup := false
	p := &Pipeline{Steps: []Step{{Name: "mount-target", Run: func(_ context.Context, e *Env) error {
		e.Defer("unmount", func() error {
			// Cleanup still touches the disk, so the lock must outlive it.
			heldInCleanup = !lockIsFree(t, e.installLockPath())
			return nil
		})
		return nil
	}}}}
	if err := p.Run(context.Background(), env, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !heldInCleanup {
		t.Error("the install lock must still be held while cleanup runs")
	}
}

func TestLockReleasedAfterRun(t *testing.T) {
	for _, fail := range []bool{false, true} {
		env, _ := lockEnv(t)
		path := env.InstallLockPath
		step := Step{Name: "finalize", Run: func(_ context.Context, _ *Env) error {
			if fail {
				return errors.New("boom")
			}
			return nil
		}}
		p := &Pipeline{Steps: []Step{step}}
		err := p.Run(context.Background(), env, false)
		if fail && err == nil {
			t.Fatal("expected failure")
		}
		if !fail && err != nil {
			t.Fatalf("run: %v", err)
		}
		if !lockIsFree(t, path) {
			t.Fatalf("fail=%v: lock still held after Run returned", fail)
		}
		// A second sequential run reuses the same lock file and succeeds.
		if err := p.Run(context.Background(), env, false); (err != nil) != fail {
			t.Fatalf("fail=%v: second run error = %v", fail, err)
		}
	}
}

func TestLockParentDirectoryIsCreated(t *testing.T) {
	env, _ := lockEnv(t)
	env.InstallLockPath = filepath.Join(t.TempDir(), "run", "firn", "install.lock")
	p := &Pipeline{Steps: []Step{{Name: "finalize", Run: func(_ context.Context, _ *Env) error { return nil }}}}
	if err := p.Run(context.Background(), env, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(env.InstallLockPath); err != nil {
		t.Fatalf("lock file %s: %v", env.InstallLockPath, err)
	}
}

func TestUnusableLockPathFailsClosed(t *testing.T) {
	env, events := lockEnv(t)
	// The lock's parent is a regular file, so the directory cannot be made.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	env.InstallLockPath = filepath.Join(blocker, "install.lock")

	ran := false
	p := &Pipeline{Steps: []Step{{Name: "partition", Run: func(_ context.Context, _ *Env) error {
		ran = true
		return nil
	}}}}
	err := p.Run(context.Background(), env, false)
	if err == nil {
		t.Fatal("an unusable lock path must fail the run")
	}
	if ran {
		t.Error("no step may run when the install lock cannot be taken")
	}
	evs := *events
	terminal, ok := evs[len(evs)-1].(progress.Error)
	if !ok || terminal.Code != progress.CodeStepFailed || terminal.Step != "" {
		t.Fatalf("terminal error = %#v, want step_failed with an empty step", evs[len(evs)-1])
	}
}

// A canceled run still unwinds and drops the install lock, so the next
// install can start without a reboot.
func TestLockReleasedAfterCancel(t *testing.T) {
	env, _ := lockEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pipeline{Steps: []Step{{Name: "write", Destructive: true, Run: func(ctx context.Context, _ *Env) error {
		cancel()
		return ctx.Err()
	}}}}
	if err := p.Run(ctx, env, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
	if !lockIsFree(t, env.InstallLockPath) {
		t.Error("the install lock must be released after a canceled run")
	}
}

// The locked descriptor is close-on-exec, so a child tool (bootc, mkfs,
// mount) cannot inherit the lock and hold it after firn exits.
func TestLockDescriptorIsCloseOnExec(t *testing.T) {
	env, _ := lockEnv(t)
	want, err := filepath.EvalSymlinks(filepath.Dir(env.InstallLockPath))
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Join(want, filepath.Base(env.InstallLockPath))
	checked := false
	p := &Pipeline{Steps: []Step{{Name: "write", Destructive: true, Run: func(context.Context, *Env) error {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Skipf("no /proc/self/fd: %v", err)
		}
		for _, e := range entries {
			target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
			if err != nil || target != want {
				continue
			}
			var fd int
			if _, err := fmt.Sscanf(e.Name(), "%d", &fd); err != nil {
				t.Fatal(err)
			}
			flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
			if errno != 0 {
				t.Fatalf("fcntl F_GETFD: %v", errno)
			}
			if flags&syscall.FD_CLOEXEC == 0 {
				t.Errorf("lock descriptor %d is inheritable (flags %#x)", fd, flags)
			}
			checked = true
		}
		return nil
	}}}}
	if err := p.Run(context.Background(), env, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !checked {
		t.Fatal("no open descriptor named the install lock during the step")
	}
}
