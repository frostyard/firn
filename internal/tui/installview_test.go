package tui

import (
	"math"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/frostyard/firn/internal/progress"
)

func TestFailureMessageWrapsToTerminalWidth(t *testing.T) {
	m := newInstallModel(nil, nil)
	m.width = 32
	m.result.Failed = true
	m.result.FailedStep = "bootc-install"
	m.result.ErrorMessage = "bootcimg: image ghcr.io/frostyard/snow:latest failed while pinging container registry: i/o timeout"
	m.finished = true

	for _, line := range strings.Split(m.finalView(), "\n") {
		if got := ansi.StringWidth(line); got > m.width-1 {
			t.Errorf("failure line width = %d, want <= %d: %q", got, m.width-1, line)
		}
	}
}

// apply pushes one pipeline event through Update and returns the
// resulting model and command, as the running Program would.
func apply(t *testing.T, m installModel, e progress.Event) (installModel, tea.Cmd) {
	t.Helper()
	nm, cmd := m.Update(eventMsg{event: e})
	return nm.(installModel), cmd
}

func key(t *testing.T, m installModel, k tea.KeyType) (installModel, tea.Cmd) {
	t.Helper()
	nm, cmd := m.Update(tea.KeyMsg{Type: k})
	return nm.(installModel), cmd
}

// requireQuit asserts cmd resolves to tea.QuitMsg. Run with a timeout
// so a regression to a blocking channel read fails instead of hanging.
func requireQuit(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected quit command, got nil")
	}
	got := make(chan tea.Msg, 1)
	go func() { got <- cmd() }()
	select {
	case msg := <-got:
		if _, ok := msg.(tea.QuitMsg); !ok {
			t.Fatalf("expected tea.QuitMsg, got %T", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("command did not resolve to quit (blocked)")
	}
}

// requireNoQuit asserts cmd is not the quit command — either nil, or a
// command that resolves to something other than tea.QuitMsg. Used where
// the view must stay up (holding the final screen for acknowledgement).
func requireNoQuit(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	got := make(chan tea.Msg, 1)
	go func() { got <- cmd() }()
	select {
	case msg := <-got:
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Fatal("expected the view to hold, but it quit")
		}
	case <-time.After(2 * time.Second):
		// A blocking command (e.g. a re-armed channel read) is not a quit.
	}
}

func startedModel(t *testing.T) installModel {
	t.Helper()
	m := newInstallModel(nil, nil)
	m, _ = apply(t, m, progress.Start{Protocol: progress.Protocol, Firn: "test", Steps: []progress.Step{
		{Name: "fetch", Weight: 1},
		{Name: "write", Weight: 3},
	}})
	return m
}

func TestWeightsToPercent(t *testing.T) {
	m := startedModel(t)
	if f := m.overallFraction(); f != 0 {
		t.Fatalf("before any step: fraction = %v, want 0", f)
	}

	m, _ = apply(t, m, progress.StepStart{Index: 0, Name: "fetch"})
	if f := m.overallFraction(); f != 0 {
		t.Fatalf("at step 0 start: fraction = %v, want 0", f)
	}

	m, _ = apply(t, m, progress.StepProgress{Index: 0, Fraction: 0.5})
	if f := m.overallFraction(); math.Abs(f-0.125) > 1e-9 {
		t.Fatalf("step 0 half done: fraction = %v, want 0.125", f)
	}

	m, _ = apply(t, m, progress.StepStart{Index: 1, Name: "write"})
	if f := m.overallFraction(); math.Abs(f-0.25) > 1e-9 {
		t.Fatalf("at step 1 start: fraction = %v, want 0.25", f)
	}

	// Bytes-based progress: 50/100 of a weight-3 step.
	m, _ = apply(t, m, progress.StepProgress{Index: 1, Bytes: 50, TotalBytes: 100})
	if f := m.overallFraction(); math.Abs(f-0.625) > 1e-9 {
		t.Fatalf("step 1 half done: fraction = %v, want 0.625", f)
	}

	view := m.View()
	if !strings.Contains(view, "62%") {
		t.Errorf("view should show 62%%, got:\n%s", view)
	}
	if !strings.Contains(view, "step 2/2: write") {
		t.Errorf("view should name the current step, got:\n%s", view)
	}

	// A stale StepProgress for a finished step must not move the bar.
	m, _ = apply(t, m, progress.StepProgress{Index: 0, Fraction: 1})
	if f := m.overallFraction(); math.Abs(f-0.625) > 1e-9 {
		t.Fatalf("stale step progress moved fraction to %v", f)
	}
}

func TestRecoveryKeyGateBlocksUntilKeypress(t *testing.T) {
	const theKey = "1234-ABCD-5678-EFGH"
	m := startedModel(t)
	m, _ = apply(t, m, progress.StepStart{Index: 0, Name: "fetch"})

	m, _ = apply(t, m, progress.RecoveryKey{Key: theKey})
	if !m.gated {
		t.Fatal("recovery key did not gate the view")
	}
	view := m.View()
	if !strings.Contains(view, theKey) {
		t.Fatalf("gate view missing the key, got:\n%s", view)
	}
	if !strings.Contains(view, "WRITE THIS DOWN") {
		t.Fatalf("gate view missing WRITE THIS DOWN, got:\n%s", view)
	}

	// A terminal event while gated must not quit past the gate.
	m, cmd := apply(t, m, progress.Done{OK: true})
	if cmd != nil {
		t.Fatal("Done while gated should not produce a command")
	}
	if !m.gated || !strings.Contains(m.View(), theKey) {
		t.Fatal("gate dropped by terminal event")
	}

	// Non-Enter keys do not dismiss the gate.
	m, _ = key(t, m, tea.KeySpace)
	if !m.gated {
		t.Fatal("gate dismissed by a non-Enter key")
	}

	m, cmd = key(t, m, tea.KeyEnter)
	if m.gated {
		t.Fatal("Enter did not dismiss the gate")
	}
	if m.result.RecoveryKey != "" {
		t.Fatal("acknowledged recovery key was retained for the command layer")
	}
	// Dismissing the gate reveals the final screen — it does not quit yet.
	requireNoQuit(t, cmd)
	if !m.result.Done {
		t.Fatalf("result = %+v, want Done", m.result)
	}
	// The final screen then dismisses on any key.
	_, cmd = key(t, m, tea.KeyEnter)
	requireQuit(t, cmd)
}

func TestWarningsAndInfoTail(t *testing.T) {
	m := startedModel(t)
	m, _ = apply(t, m, progress.StepStart{Index: 0, Name: "fetch"})
	m, _ = apply(t, m, progress.Info{Message: "copying files"})
	m, _ = apply(t, m, progress.Warning{Code: progress.CodeNoTPM, Message: "no TPM found"})

	view := m.View()
	if !strings.Contains(view, "copying files") {
		t.Errorf("info line missing from view:\n%s", view)
	}
	if !strings.Contains(view, "warning: no TPM found") {
		t.Errorf("warning line missing its warning: prefix in view:\n%s", view)
	}

	// Tail scrolls: only the last tailLen lines survive.
	for i := 0; i < tailLen+3; i++ {
		m, _ = apply(t, m, progress.Info{Message: "filler"})
	}
	if len(m.tail) != tailLen {
		t.Fatalf("tail length = %d, want %d", len(m.tail), tailLen)
	}
	if strings.Contains(m.View(), "copying files") {
		t.Error("oldest line should have scrolled out of the tail")
	}
}

func TestErrorProducesFailedResult(t *testing.T) {
	m := startedModel(t)
	m, _ = apply(t, m, progress.StepStart{Index: 1, Name: "write"})
	m, cmd := apply(t, m, progress.Error{Step: "write", Code: "disk_full", Message: "no space left on device"})
	// The failure screen must NOT self-quit: the kiosk restarts firn on
	// exit, so an auto-quitting failure would bounce back to page 1 before
	// it could be read (the live bug this guards).
	requireNoQuit(t, cmd)
	if !m.finished {
		t.Fatal("Error should mark the view finished")
	}

	want := InstallResult{Failed: true, FailedStep: "write", ErrorMessage: "no space left on device"}
	if m.result.Failed != want.Failed || m.result.FailedStep != want.FailedStep || m.result.ErrorMessage != want.ErrorMessage || m.result.Done {
		t.Fatalf("result = %+v, want %+v", m.result, want)
	}
	view := m.View()
	if !strings.Contains(view, "write") || !strings.Contains(view, "no space left on device") {
		t.Errorf("failure view missing step or message:\n%s", view)
	}
	// A keypress then dismisses it and quits.
	_, cmd = key(t, m, tea.KeyEnter)
	requireQuit(t, cmd)
}

func TestDoneShowsSummary(t *testing.T) {
	m := startedModel(t)
	m, _ = apply(t, m, progress.Summary{Items: []progress.SummaryItem{
		{Code: progress.CodeNoTPM, Detail: "recovery key required at boot"},
	}})
	m, cmd := apply(t, m, progress.Done{OK: true, BootEntry: "snosi"})
	// The success screen also holds until acknowledged.
	requireNoQuit(t, cmd)

	if !m.result.Done || m.result.Failed {
		t.Fatalf("result = %+v, want Done", m.result)
	}
	if len(m.result.Summary) != 1 || m.result.Summary[0].Code != progress.CodeNoTPM {
		t.Fatalf("summary not carried into result: %+v", m.result.Summary)
	}
	if !strings.Contains(m.View(), "recovery key required at boot") {
		t.Errorf("final view missing summary detail:\n%s", m.View())
	}
	_, cmd = key(t, m, tea.KeyEnter)
	requireQuit(t, cmd)
}

func TestCtrlCCancelsAndWaits(t *testing.T) {
	calls := 0
	m := newInstallModel(nil, func() { calls++ })
	m, _ = apply(t, m, progress.Start{Steps: []progress.Step{{Name: "fetch", Weight: 1}}})
	m, _ = apply(t, m, progress.StepStart{Index: 0, Name: "fetch"})

	m, cmd := key(t, m, tea.KeyCtrlC)
	if calls != 1 {
		t.Fatalf("cancel called %d times, want 1", calls)
	}
	if cmd != nil {
		t.Fatal("ctrl+c must not quit while the pipeline is running")
	}
	if !strings.Contains(m.View(), "cleaning up") {
		t.Errorf("cancel view missing cleaning-up notice:\n%s", m.View())
	}

	// A second ctrl+c stays patient and does not re-cancel.
	m, cmd = key(t, m, tea.KeyCtrlC)
	if calls != 1 || cmd != nil {
		t.Fatalf("second ctrl+c: calls = %d, quit = %v", calls, cmd != nil)
	}

	// The pipeline's terminal event lands the final screen (which then
	// holds for acknowledgement).
	m, cmd = apply(t, m, progress.Error{Step: "fetch", Code: "canceled", Message: "context canceled"})
	requireNoQuit(t, cmd)
	if !m.result.Failed {
		t.Fatalf("result = %+v, want Failed after canceled install", m.result)
	}
	// A keypress dismisses the screen and quits.
	_, cmd = key(t, m, tea.KeyEnter)
	requireQuit(t, cmd)
}

func TestChannelCloseWithoutTerminalEventHolds(t *testing.T) {
	m := startedModel(t)
	nm, cmd := m.Update(channelClosedMsg{})
	m = nm.(installModel)
	// A channel close with no terminal event still holds the final screen
	// (rather than vanishing) so the user sees the "did not finish" state.
	requireNoQuit(t, cmd)
	if !m.finished {
		t.Fatal("channel close should mark the view finished")
	}
	if m.result.Done || m.result.Failed {
		t.Fatalf("result = %+v, want neither Done nor Failed", m.result)
	}
	_, cmd = key(t, m, tea.KeyEnter)
	requireQuit(t, cmd)
}

func TestEventLoopRearmsChannelRead(t *testing.T) {
	ch := make(chan progress.Event, 1)
	m := newInstallModel(ch, nil)

	// Init arms the first read; a delivered event yields an eventMsg.
	ch <- progress.Info{Message: "hello"}
	msg := m.Init()()
	em, ok := msg.(eventMsg)
	if !ok {
		t.Fatalf("Init cmd returned %T, want eventMsg", msg)
	}
	nm, cmd := m.Update(em)
	if _, ok := nm.(installModel); !ok {
		t.Fatalf("Update returned %T, want installModel", nm)
	}
	if cmd == nil {
		t.Fatal("non-terminal event must re-arm the channel read")
	}

	// Closing the channel turns the armed read into channelClosedMsg.
	close(ch)
	if _, ok := cmd().(channelClosedMsg); !ok {
		t.Fatal("closed channel should produce channelClosedMsg")
	}
}
