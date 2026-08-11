package pipeline

import (
	"context"
	"errors"
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
