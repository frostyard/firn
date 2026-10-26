package installer

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/frostyard/firn/internal/progress"
)

func TestKeyboardWalksTheWizard(t *testing.T) {
	a, _ := newTestApp(t, Options{})
	ready(t, a)
	tt := ui.NewTester(a.view, 1120, 760)

	tt.Key(0, ui.KeyEnter)
	if a.page != pageImage {
		t.Fatalf("Enter on the welcome page: page %v, want image", a.page)
	}
	for _, want := range []page{pageStorage, pageSecurity, pageRegion, pageAccount} {
		tt.Key(ui.Alt, ui.KeyRight)
		if a.page != want {
			t.Fatalf("Alt+Right: page %v, want %v", a.page, want)
		}
	}
	// The account page needs a password: continuing shows the errors and
	// stays.
	tt.Key(ui.Ctrl, ui.KeyEnter)
	if a.page != pageAccount || !a.attempted[pageAccount] {
		t.Fatalf("Ctrl+Enter on an incomplete page moved on (page %v)", a.page)
	}
	tt.Frame()
	for _, want := range []string{"Enter a password.", "Choose a username.", "2 fields need attention"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q after the attempt: %q", want, tt.Texts())
		}
	}
	tt.Key(ui.Alt, ui.KeyLeft)
	if a.page != pageRegion {
		t.Fatalf("Alt+Left: page %v, want region", a.page)
	}
	// Alt+number jumps back, but not past the incomplete page.
	tt.Key(ui.Alt, ui.Key2)
	if a.page != pageImage {
		t.Fatalf("Alt+2: page %v, want image", a.page)
	}
	tt.Key(ui.Alt, ui.Key7)
	if a.page != pageImage {
		t.Fatalf("Alt+7 jumped past the incomplete account page to %v", a.page)
	}
	a.c.setFullname("Ada Lovelace")
	a.c.password, a.c.passwordConfirm = "pw", "pw"
	tt.Frame()
	tt.Key(ui.Alt, ui.Key6)
	if a.page != pageAccount {
		t.Fatalf("Alt+6 after completing: page %v, want account", a.page)
	}
	tt.Key(ui.Alt, ui.KeyRight)
	tt.Key(ui.Alt, ui.KeyRight)
	if a.page != pageReview {
		t.Fatalf("page %v, want review", a.page)
	}
	if a.reviewed == nil {
		t.Fatal("the review page has no recipe")
	}
}

func TestHelpAndAppearanceShortcuts(t *testing.T) {
	a, _ := newTestApp(t, Options{Appearance: "light"})
	tt := ui.NewTester(a.view, 1120, 760)
	tt.Key(0, ui.KeyF1)
	if !a.showHelp || !tt.HasText("Keyboard shortcuts") {
		t.Fatal("F1 did not open the shortcut list")
	}
	tt.Key(0, ui.KeyEscape)
	if a.showHelp {
		t.Fatal("Escape did not close the shortcut list")
	}
	tt.Key(ui.Ctrl, ui.KeySlash)
	if !a.showHelp {
		t.Fatal("Ctrl+/ did not open the shortcut list")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyL)
	tt.Frame()
	if a.themeMode != 2 || !a.p.dark {
		t.Fatalf("Ctrl+Shift+L did not switch to dark (mode %d)", a.themeMode)
	}
}

func TestRescanShortcut(t *testing.T) {
	a, q := newTestApp(t, Options{})
	a.goTo(pageStorage)
	tt := ui.NewTester(a.view, 1120, 760)
	tt.Key(ui.Ctrl, ui.KeyR)
	if !a.disksLoading {
		t.Fatal("Ctrl+R did not start a rescan")
	}
	deadline := time.Now().Add(5 * time.Second)
	for q.drain() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if a.disksLoading || len(a.disks) == 0 || a.c.disk != "/dev/nvme0n1" {
		t.Fatalf("rescan result not applied: loading=%v disks=%d chosen=%q", a.disksLoading, len(a.disks), a.c.disk)
	}
}

func TestQuitAsksOnceAnswersExist(t *testing.T) {
	a, _ := newTestApp(t, Options{})
	tt := ui.NewTester(a.view, 1120, 760)
	tt.Key(ui.Ctrl, ui.KeyQ)
	if a.status != "quit" {
		t.Fatal("Ctrl+Q on the welcome page should quit at once")
	}
	a.status = ""
	a.goTo(pageImage)
	tt.Key(ui.Ctrl, ui.KeyQ)
	if !a.askQuit || a.status == "quit" {
		t.Fatal("Ctrl+Q mid-wizard should ask first")
	}
	a.askQuit = false
	a.inst.phase = phaseRunning
	a.requestQuit()
	if !a.closeDenied {
		t.Fatal("quitting during an install must be refused")
	}
}

// The install needs the disk path typed exactly, then runs the demo
// pipeline to completion through the in-process channel.
func TestTypedConfirmationStartsTheInstall(t *testing.T) {
	a, q := newTestApp(t, Options{})
	ready(t, a)
	completeAnswers(a)
	a.furthest = pageReview
	a.goTo(pageReview)
	tt := ui.NewTester(a.view, 1120, 760)
	tt.Key(ui.Ctrl, ui.KeyEnter)
	if !a.confirmOpen {
		t.Fatal("Ctrl+Enter on the review did not ask for confirmation")
	}
	tt.Type("/dev/sda")
	tt.Key(0, ui.KeyEnter)
	if a.page == pageInstall {
		t.Fatal("a wrong disk path started the install")
	}
	a.confirmText = ""
	tt.Frame()
	tt.Type("/dev/nvme0n1")
	tt.Key(0, ui.KeyEnter)
	if a.page != pageInstall || a.inst.phase != phaseRunning {
		t.Fatalf("typed confirmation did not start the install (page %v, err %q)", a.page, a.installErr)
	}
	deadline := time.Now().Add(10 * time.Second)
	for a.inst.phase == phaseRunning && time.Now().Before(deadline) {
		q.drain()
		time.Sleep(2 * time.Millisecond)
	}
	q.drain()
	if a.inst.phase != phaseDone {
		t.Fatalf("install ended in phase %v: %s %s", a.inst.phase, a.inst.errCode, a.inst.errMessage)
	}
	data, err := os.ReadFile(a.inst.recipePath)
	if err != nil || !strings.Contains(string(data), `disk = "/dev/nvme0n1"`) {
		t.Fatalf("persisted recipe: %v\n%s", err, data)
	}
	tt.Frame()
	if !tt.HasText("Your new system is ready") {
		t.Errorf("done screen missing: %q", tt.Texts())
	}
	// Navigation shortcuts are inert once installing.
	tt.Key(ui.Alt, ui.KeyLeft)
	if a.page != pageInstall {
		t.Error("Alt+Left left the install page")
	}
}

func TestPreviewInitialSelection(t *testing.T) {
	a, _ := newTestApp(t, Options{})
	ref := a.c.entry.Ref
	a.setPreview(corePreview{ref: ref, state: corePreviewAvailable})
	if !a.c.coreFlatpaks {
		t.Fatal("an offered core set starts on (ADR-0019)")
	}
	a.c.coreFlatpaks = false
	a.setPreview(corePreview{ref: ref, state: corePreviewAvailable})
	if a.c.coreFlatpaks {
		t.Fatal("a second preview of the same image overrode the user's answer")
	}
	a.setPreview(corePreview{ref: ref, state: corePreviewNone})
	if a.c.coreFlatpaks {
		t.Fatal("no core set must clear the toggle")
	}
	a.setPreview(corePreview{ref: "other", state: corePreviewAvailable})
	if a.preview.ref == "other" {
		t.Fatal("a preview of an image no longer chosen was applied")
	}
}

func TestAppsPageWaitsForThePreview(t *testing.T) {
	a, _ := newTestApp(t, Options{})
	ready(t, a)
	completeAnswers(a)
	a.preview = corePreview{}
	a.errs = a.validate()
	if a.pageValid(pageApps) {
		t.Fatal("the apps page must wait for the chosen image's app list")
	}
}

func TestInstallReducer(t *testing.T) {
	s := newInstallState()
	s.phase = phaseRunning
	s.apply(progress.Start{Steps: []progress.Step{{Name: "a", Weight: 1}, {Name: "b", Weight: 3}}})
	s.apply(progress.StepStart{Index: 1, Name: "b"})
	s.apply(progress.StepProgress{Index: 1, Bytes: 50, TotalBytes: 100})
	if got := s.overall(); got != (1+1.5)/4 {
		t.Errorf("overall = %v, want 0.625", got)
	}
	s.apply(progress.Warning{Code: progress.CodeGroupMissing, Message: "docker"})
	if !s.log[0].warning || !strings.Contains(s.log[0].text, "docker") {
		t.Errorf("warning not logged: %+v", s.log)
	}
	s.apply(progress.RecoveryKey{Key: "k"})
	if s.recoveryKey != "k" {
		t.Error("recovery key not held for acknowledgement")
	}
	s.finish(errors.New("emitter broke"))
	if s.phase != phaseFailed || s.errCode != progress.CodeStreamTruncated || !strings.Contains(s.errMessage, "emitter broke") {
		t.Errorf("a stream without a terminal event must fail as stream_truncated with the pipeline's error, got %v %q %q", s.phase, s.errCode, s.errMessage)
	}

	done := newInstallState()
	done.phase = phaseRunning
	done.apply(progress.Done{OK: true})
	done.finish(errors.New("late failure"))
	if done.phase != phaseFailed || !strings.Contains(done.errMessage, "late failure") {
		t.Errorf("a pipeline error the stream did not report must fail the install, got %v %q", done.phase, done.errMessage)
	}
}

func TestKeyGroups(t *testing.T) {
	got := keyGroups(strings.Repeat("ab", 32), 8)
	if len(got) != 8 || strings.Join(got, "") != strings.Repeat("ab", 32) {
		t.Errorf("keyGroups = %v", got)
	}
}

// The recovery key is shown once: neither closing nor quitting may lose it
// before the user acknowledges it, and it never reaches the clipboard.
func TestRecoveryKeyHoldsTheWindow(t *testing.T) {
	a, _ := newTestApp(t, Options{})
	a.page = pageInstall
	a.inst.phase = phaseDone
	a.inst.recoveryKey = strings.Repeat("ab", 32)
	tt := ui.NewTester(a.view, 1120, 760)
	if tt.HasText("Copy") {
		t.Error("the recovery key dialog offers to copy the key to the clipboard")
	}
	if a.closeAllowed() || !a.closeDenied {
		t.Fatal("the window may close over an unacknowledged recovery key")
	}
	a.closeDenied = false
	a.requestQuit()
	if a.status == "quit" || !a.closeDenied {
		t.Fatal("a quit request closed the window over an unacknowledged recovery key")
	}
	a.closeDenied = false
	a.onSignal()
	if a.status != "" || !a.closeDenied {
		t.Fatal("the first signal quit over an unacknowledged recovery key")
	}
	a.onSignal()
	if a.status != "quit app" || !a.forceQuit {
		t.Fatal("a second signal must quit anyway")
	}
}

// A signal during an install cancels it and quits only once the pipeline
// has unwound; the pipeline's error is kept.
func TestSignalCancelsTheInstallThenQuits(t *testing.T) {
	a, q := newTestApp(t, Options{})
	ready(t, a)
	completeAnswers(a)
	a.furthest = pageReview
	a.goTo(pageReview)
	a.be.(*demoBackend).pace = time.Millisecond // slow enough to interrupt
	a.startInstall()
	if !a.installing() {
		t.Fatalf("install did not start: %s", a.installErr)
	}
	a.startInstall()
	if a.pipelineDone == nil {
		t.Fatal("no pipeline is tracked")
	}
	a.onSignal()
	if !a.quitWhenDone || !a.inst.canceling {
		t.Fatal("a signal during the install did not cancel it")
	}
	if a.status == "quit app" {
		t.Fatal("quit before the pipeline unwound")
	}
	select {
	case <-a.pipelineDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the canceled pipeline never returned")
	}
	deadline := time.Now().Add(5 * time.Second)
	for a.status != "quit app" && time.Now().Before(deadline) {
		q.drain()
		time.Sleep(2 * time.Millisecond)
	}
	if a.status != "quit app" || a.inst.phase != phaseFailed {
		t.Fatalf("after unwinding: status %q phase %v", a.status, a.inst.phase)
	}
}

func TestOneInstallAtATime(t *testing.T) {
	a, _ := newTestApp(t, Options{})
	ready(t, a)
	completeAnswers(a)
	a.goTo(pageReview)
	a.be.(*demoBackend).pace = time.Millisecond
	a.startInstall()
	first := a.pipelineDone
	a.startInstall()
	if a.pipelineDone != first {
		t.Fatal("a second start launched another pipeline")
	}
	a.cancelInstall()
	<-first
}

func TestRescanNeverSwitchesTheDisk(t *testing.T) {
	a, _ := newTestApp(t, Options{})
	disks, _ := a.be.Disks(t.Context())
	a.setDisks(disks, nil)
	if a.c.disk != "/dev/nvme0n1" {
		t.Fatalf("first scan chose %q, want the first usable disk", a.c.disk)
	}
	a.c.disk = "/dev/sda"
	a.setDisks(disks, nil)
	if a.c.disk != "/dev/sda" {
		t.Fatalf("a rescan moved the choice to %q", a.c.disk)
	}
	a.setDisks(disks[:1], nil) // /dev/sda unplugged
	if a.c.disk != "" || !strings.Contains(a.diskNotice, "/dev/sda") {
		t.Fatalf("a vanished disk must clear the choice with a notice, got %q %q", a.c.disk, a.diskNotice)
	}
	a.setDisks(disks, nil)
	if a.c.disk != "" {
		t.Fatalf("a later scan chose %q by itself", a.c.disk)
	}
	a.c.disk = "/dev/nvme0n1"
	refused := append([]diskInfo(nil), disks...)
	refused[0].refused = "it is mounted"
	a.setDisks(refused, nil)
	if a.c.disk != "" || !strings.Contains(a.diskNotice, "it is mounted") {
		t.Fatalf("a newly refused disk must clear the choice, got %q %q", a.c.disk, a.diskNotice)
	}
}

func TestDiskIdentityShowsSerialAndWWN(t *testing.T) {
	a, _ := newTestApp(t, Options{})
	ready(t, a)
	a.goTo(pageStorage)
	tt := ui.NewTester(a.view, 1120, 760)
	if !tt.HasText("serial S5GXNF0R123456  ·  wwn eui.002538b111b2c3d4") {
		t.Errorf("disk card lacks serial and WWN: %q", tt.Texts())
	}
	completeAnswers(a)
	a.goTo(pageReview)
	tt.Frame()
	a.openConfirm()
	tt.Frame()
	if !a.confirmOpen || !tt.HasText("serial S5GXNF0R123456  ·  wwn eui.002538b111b2c3d4") {
		t.Errorf("confirmation lacks serial and WWN: %q", tt.Texts())
	}
}
