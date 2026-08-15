package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/progress"
)

func collectEnv() (*Env, *[]progress.Event) {
	var events []progress.Event
	env := &Env{Version: "test", Emit: func(e progress.Event) { events = append(events, e) }}
	return env, &events
}

func TestCleanupUnwindsLIFOOnFailure(t *testing.T) {
	env, _ := collectEnv()
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
	env, _ := collectEnv()
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
		env, events := collectEnv()
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
	env, events := collectEnv()
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
	env, _ := collectEnv()
	p := &Pipeline{Steps: []Step{{Name: "stub"}}}
	if err := p.Run(context.Background(), env, false); err == nil {
		t.Error("nil-Run step must fail a real run")
	}
}

func TestCodedErrorSetsTerminalProgressCode(t *testing.T) {
	env, events := collectEnv()
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
