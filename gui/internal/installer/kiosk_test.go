package installer

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/frostyard/firn/internal/progress"
)

// powerBackend counts the restarts and power offs a test asks the machine
// for, and fails them when err is set. Everything else is the demo's.
type powerBackend struct {
	backend
	reboots, powerOffs int
	err                error
}

func (b *powerBackend) Reboot() error   { b.reboots++; return b.err }
func (b *powerBackend) PowerOff() error { b.powerOffs++; return b.err }

// realPower makes a's machine a real one for restarts and power offs, with
// a powerBackend behind it. The cleanup order matters: a.cleanup, which
// newTestApp registered first, runs last and must still see the demo.
func realPower(t *testing.T, a *app) *powerBackend {
	t.Helper()
	b := &powerBackend{backend: a.be}
	a.be = b
	a.machine.demo = false
	t.Cleanup(func() { a.machine.demo = true })
	return b
}

func kioskApp(t *testing.T) *app {
	t.Helper()
	a, _ := newTestApp(t, Options{Kiosk: true})
	ready(t, a)
	return a
}

const testRecoveryKey = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

// outcomes are the ways an install ends, each put on screen by setOutcome.
var outcomes = []string{"done", "failed", "stopped", "in-progress"}

// setOutcome shows how an install ended on the install page.
func setOutcome(a *app, kind string) {
	a.page = pageInstall
	a.inst = newInstallState()
	a.inst.phase = phaseDone
	if kind == "done" {
		return
	}
	a.inst.phase = phaseFailed
	a.inst.errCode = progress.CodeStepFailed
	a.inst.errMessage = "bootc failed"
	switch kind {
	case "stopped":
		a.inst.canceling = true
	case "in-progress":
		a.inst.errCode = progress.CodeInstallInProgress
	}
}

func TestKioskCtrlQDoesNothing(t *testing.T) {
	a := kioskApp(t)
	tt := ui.NewTester(a.view, 1120, 760)
	check := func(where string) {
		t.Helper()
		tt.Key(ui.Ctrl, ui.KeyQ)
		if a.status != "" || a.askQuit || a.closeDenied || a.leaving {
			t.Fatalf("Ctrl+Q %s: status %q askQuit %v closeDenied %v leaving %v", where, a.status, a.askQuit, a.closeDenied, a.leaving)
		}
	}
	check("on the welcome page")
	a.goTo(pageImage)
	check("mid-wizard")
	for _, kind := range outcomes {
		setOutcome(a, kind)
		check("on the " + kind + " outcome")
	}
	a.page, a.inst = pageInstall, newInstallState()
	a.inst.phase = phaseRunning
	check("while installing")
}

func TestKioskHoldsCloseAndQuit(t *testing.T) {
	a := kioskApp(t)
	states := map[string]func(){
		"welcome":    func() { a.page = pageWelcome },
		"mid-wizard": func() { a.page = pageStorage },
		"review":     func() { a.page = pageReview },
	}
	for _, kind := range outcomes {
		states[kind] = func() { setOutcome(a, kind) }
	}
	for name, set := range states {
		a.leaving, a.forceQuit, a.closeDenied = false, false, false
		a.page, a.inst = pageWelcome, newInstallState()
		set()
		if a.closeAllowed() || !a.beforeQuitPrevented() {
			t.Errorf("%s: kiosk let a close or quit through", name)
		}
		if a.closeDenied || a.quitWhenDone {
			t.Errorf("%s: a refused close must be silent (closeDenied %v, quitWhenDone %v)", name, a.closeDenied, a.quitWhenDone)
		}
		a.leaving = true
		if !a.closeAllowed() || a.beforeQuitPrevented() {
			t.Errorf("%s: leaving for the console installer must close", name)
		}
		a.leaving, a.forceQuit = false, true
		if !a.closeAllowed() || a.beforeQuitPrevented() {
			t.Errorf("%s: a signal's quit must close", name)
		}
	}

	// A running install is not canceled by a refused quit.
	a.leaving, a.forceQuit = false, false
	a.page, a.inst = pageInstall, newInstallState()
	a.inst.phase = phaseRunning
	if a.closeAllowed() || !a.beforeQuitPrevented() || a.quitWhenDone || a.closeDenied {
		t.Error("kiosk must refuse a close and a quit during an install without a word or a cancel")
	}
}

func TestOutOfKioskCloseIsUnchanged(t *testing.T) {
	a, _ := newTestApp(t, Options{})
	if !a.closeAllowed() || a.beforeQuitPrevented() {
		t.Error("a close or quit on the welcome page must go through")
	}
	setOutcome(a, "done")
	if !a.closeAllowed() || a.beforeQuitPrevented() {
		t.Error("a close or quit on an outcome must go through")
	}
	a.inst.recoveryKey = testRecoveryKey
	if a.closeAllowed() || !a.closeDenied {
		t.Error("a close over a recovery key must be refused with an explanation")
	}
	a.inst.recoveryKey, a.closeDenied = "", false
	a.inst.phase = phaseRunning
	if a.closeAllowed() || !a.closeDenied {
		t.Error("a close during an install must be refused with an explanation")
	}
	a.closeDenied = false
	canceled := false
	a.cancelInstall = func() { canceled = true }
	if !a.beforeQuitPrevented() || !a.quitWhenDone || !canceled {
		t.Error("a quit during an install must cancel it and wait")
	}
	// The kiosk's shortcuts and buttons do not exist outside it.
	a.useConsole()
	if a.leaving || a.askConsole || a.status != "" {
		t.Error("useConsole must do nothing outside kiosk mode")
	}
}

func TestKioskOutcomeButtons(t *testing.T) {
	for _, kiosk := range []bool{true, false} {
		for _, kind := range outcomes {
			a, _ := newTestApp(t, Options{Kiosk: kiosk})
			ready(t, a)
			setOutcome(a, kind)
			tt := ui.NewTester(a.view, 1120, 760)
			shown := func(text string) bool { _, ok := tt.Find(text); return ok }
			for _, text := range []string{"Restart", "Power off", "Use the console installer"} {
				if shown(text) != kiosk {
					t.Errorf("kiosk %v, %s: %q shown = %v", kiosk, kind, text, shown(text))
				}
			}
			if shown("Close") == kiosk {
				t.Errorf("kiosk %v, %s: \"Close\" shown = %v", kiosk, kind, shown("Close"))
			}
			if want := kind != "done"; shown("Back to review") != want {
				t.Errorf("kiosk %v, %s: \"Back to review\" shown = %v, want %v", kiosk, kind, shown("Back to review"), want)
			}
			if !kiosk && shown("Restart now") != (kind == "done") {
				t.Errorf("%s: \"Restart now\" shown = %v", kind, shown("Restart now"))
			}
		}
	}
}

func TestKioskRestartAndPowerOff(t *testing.T) {
	for _, kind := range outcomes {
		a := kioskApp(t)
		be := realPower(t, a)
		setOutcome(a, kind)
		tt := ui.NewTester(a.view, 1120, 760)
		if err := tt.Click("Restart"); err != nil {
			t.Fatal(err)
		}
		if be.reboots != 1 || be.powerOffs != 0 {
			t.Fatalf("%s: after Restart: %d reboots, %d power offs", kind, be.reboots, be.powerOffs)
		}
		if err := tt.Click("Power off"); err != nil {
			t.Fatal(err)
		}
		if be.reboots != 1 || be.powerOffs != 1 {
			t.Fatalf("%s: after Power off: %d reboots, %d power offs", kind, be.reboots, be.powerOffs)
		}
		tt.Key(ui.Ctrl|ui.Shift, ui.KeyR)
		tt.Key(ui.Ctrl|ui.Shift, ui.KeyP)
		tt.Key(ui.Ctrl|ui.Shift, ui.KeyP)
		if be.reboots != 2 || be.powerOffs != 3 {
			t.Fatalf("%s: after the shortcuts: %d reboots, %d power offs", kind, be.reboots, be.powerOffs)
		}
		if a.closeDenied || a.status == "quit" {
			t.Fatalf("%s: restart and power off must not close the window or warn", kind)
		}
	}
}

func TestKioskRestartAndPowerOffFailuresAreShown(t *testing.T) {
	a := kioskApp(t)
	be := realPower(t, a)
	be.err = errors.New("no systemd")
	setOutcome(a, "done")
	a.powerOff()
	if a.status != "Could not power off: no systemd" {
		t.Errorf("status = %q", a.status)
	}
	a.reboot()
	if a.status != "Could not restart: no systemd" {
		t.Errorf("status = %q", a.status)
	}
}

func TestDemoRestartAndPowerOffTouchNothing(t *testing.T) {
	a := kioskApp(t)
	setOutcome(a, "done")
	a.powerOff()
	if a.status != "Demo mode: this is where the computer would power off." {
		t.Errorf("status = %q", a.status)
	}
	a.reboot()
	if a.status != "Demo mode: this is where the computer would restart." {
		t.Errorf("status = %q", a.status)
	}
}

// Recovery-key disclosure: no restart, power off or exit while a key waits
// or an install runs.
func TestKioskExitsAreRefusedWhileTheKeyIsUnsavedOrInstalling(t *testing.T) {
	hold := map[string]func(a *app){
		"a recovery key waits": func(a *app) { setOutcome(a, "done"); a.inst.recoveryKey = testRecoveryKey },
		"the key waits during the install": func(a *app) {
			a.page, a.inst = pageInstall, newInstallState()
			a.inst.phase = phaseRunning
			a.inst.recoveryKey = testRecoveryKey
		},
		"installing": func(a *app) { a.page, a.inst = pageInstall, newInstallState(); a.inst.phase = phaseRunning },
	}
	actions := map[string]func(a *app){
		"restart": func(a *app) { a.reboot() },
		"power":   func(a *app) { a.powerOff() },
		"console": func(a *app) { a.useConsole() },
		"leaving": func(a *app) { a.leaveForConsole() },
	}
	for why, set := range hold {
		for name, act := range actions {
			a := kioskApp(t)
			be := realPower(t, a)
			set(a)
			act(a)
			if !a.closeDenied || be.reboots+be.powerOffs != 0 || a.leaving || a.status != "" || a.askConsole {
				t.Errorf("%s, %s: closeDenied %v, reboots %d, power offs %d, leaving %v, status %q", why, name, a.closeDenied, be.reboots, be.powerOffs, a.leaving, a.status)
			}
		}
	}

	// The same refusals from the keyboard, on the frames the dialog allows.
	for why, set := range hold {
		for _, key := range []ui.Key{ui.KeyR, ui.KeyP, ui.KeyT} {
			a := kioskApp(t)
			be := realPower(t, a)
			set(a)
			ui.NewTester(a.view, 1120, 760).Key(ui.Ctrl|ui.Shift, key)
			if be.reboots+be.powerOffs != 0 || a.leaving || a.status != "" {
				t.Errorf("%s, Ctrl+Shift+%v: reboots %d, power offs %d, leaving %v", why, key, be.reboots, be.powerOffs, a.leaving)
			}
		}
	}
}

func TestKioskRestartIsInertOutsideTheOutcomeScreen(t *testing.T) {
	a := kioskApp(t)
	be := realPower(t, a)
	tt := ui.NewTester(a.view, 1120, 760)
	for _, pg := range []page{pageWelcome, pageImage, pageStorage, pageReview} {
		a.page = pg
		tt.Key(ui.Ctrl|ui.Shift, ui.KeyR)
		tt.Key(ui.Ctrl|ui.Shift, ui.KeyP)
	}
	a.page, a.inst = pageInstall, newInstallState()
	a.inst.phase = phaseRunning
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyR)
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyP)
	if be.reboots+be.powerOffs != 0 || a.closeDenied {
		t.Fatalf("restart or power off outside the outcome screen: %d, %d", be.reboots, be.powerOffs)
	}
}

// Ctrl+R rescans the disks on the storage page; Ctrl+Shift+R must not, and
// Ctrl+R must not restart.
func TestKioskRestartShortcutIsNotTheRescanShortcut(t *testing.T) {
	a := kioskApp(t)
	be := realPower(t, a)
	a.goTo(pageStorage)
	tt := ui.NewTester(a.view, 1120, 760)
	a.disksLoading = false
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyR)
	if a.disksLoading {
		t.Fatal("Ctrl+Shift+R rescanned the disks")
	}
	tt.Key(ui.Ctrl, ui.KeyR)
	if !a.disksLoading {
		t.Fatal("Ctrl+R no longer rescans the disks")
	}
	if be.reboots != 0 {
		t.Fatal("Ctrl+R restarted the computer")
	}
	setOutcome(a, "done")
	a.disksLoading = false
	tt.Frame()
	tt.Key(ui.Ctrl, ui.KeyR)
	if be.reboots != 0 {
		t.Fatal("Ctrl+R restarted the computer")
	}
}

func TestKioskUseConsole(t *testing.T) {
	// On the welcome page and on every outcome screen it leaves at once.
	for _, where := range append([]string{"welcome"}, outcomes...) {
		a := kioskApp(t)
		if where != "welcome" {
			setOutcome(a, where)
		}
		a.useConsole()
		if a.status != "quit" || !a.leaving || !a.closeAllowed() || a.askConsole || a.closeDenied {
			t.Errorf("%s: status %q leaving %v askConsole %v closeDenied %v", where, a.status, a.leaving, a.askConsole, a.closeDenied)
		}
	}

	// Mid-wizard it asks first.
	a := kioskApp(t)
	a.goTo(pageStorage)
	a.useConsole()
	if !a.askConsole || a.status == "quit" || a.leaving || a.closeAllowed() {
		t.Fatalf("mid-wizard: askConsole %v status %q leaving %v", a.askConsole, a.status, a.leaving)
	}
	tt := ui.NewTester(a.view, 1120, 760)
	for _, text := range []string{"Use the console installer?", "Nothing has been written to disk. Your answers will be lost.", "Keep going"} {
		if !tt.HasText(text) {
			t.Errorf("the dialog lacks %q: %q", text, tt.Texts())
		}
	}
	if err := tt.Click("Keep going"); err != nil {
		t.Fatal(err)
	}
	if a.askConsole || a.status == "quit" || a.leaving {
		t.Fatalf("Keep going: askConsole %v status %q leaving %v", a.askConsole, a.status, a.leaving)
	}
	a.useConsole()
	tt.Frame()
	if err := tt.Click("Use the console installer"); err != nil {
		t.Fatal(err)
	}
	if a.status != "quit" || !a.leaving || !a.closeAllowed() {
		t.Fatalf("confirming: status %q leaving %v", a.status, a.leaving)
	}
}

func TestKioskUseConsoleButtons(t *testing.T) {
	// The welcome page offers it as a button; the rail offers it before the
	// install and no longer once it has begun.
	a := kioskApp(t)
	tt := ui.NewTester(a.view, 1120, 760)
	if _, ok := tt.Find("Use the console installer"); !ok {
		t.Fatalf("the welcome page lacks the button: %q", tt.Texts())
	}
	if _, ok := tt.Find("Use the console installer (Ctrl+Shift+T)"); !ok {
		t.Fatalf("the rail lacks the button: %q", tt.Texts())
	}
	if err := tt.Click("Use the console installer (Ctrl+Shift+T)"); err != nil {
		t.Fatal(err)
	}
	if a.status != "quit" || !a.leaving {
		t.Fatal("the rail's button did not leave")
	}
	a = kioskApp(t)
	a.goTo(pageImage)
	tt = ui.NewTester(a.view, 1120, 760)
	if _, ok := tt.Find("Use the console installer (Ctrl+Shift+T)"); !ok {
		t.Fatal("the rail lacks the button mid-wizard")
	}
	if _, ok := tt.Find("Use the console installer"); ok {
		t.Fatal("only the welcome page has the text button")
	}
	setOutcome(a, "done")
	tt.Frame()
	if _, ok := tt.Find("Use the console installer (Ctrl+Shift+T)"); ok {
		t.Fatal("the rail offers the console installer once the install has ended")
	}

	// Outside kiosk mode none of it exists.
	b, _ := newTestApp(t, Options{})
	tt = ui.NewTester(b.view, 1120, 760)
	if tt.HasText("console installer") {
		t.Fatalf("console installer offered outside kiosk mode: %q", tt.Texts())
	}
}

func TestKioskConsoleShortcut(t *testing.T) {
	a := kioskApp(t)
	tt := ui.NewTester(a.view, 1120, 760)
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyT)
	if a.status != "quit" || !a.leaving {
		t.Fatal("Ctrl+Shift+T on the welcome page did not leave")
	}

	a = kioskApp(t)
	a.goTo(pageSecurity)
	tt = ui.NewTester(a.view, 1120, 760)
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyT)
	if !a.askConsole || a.leaving {
		t.Fatal("Ctrl+Shift+T mid-wizard must ask first")
	}

	a = kioskApp(t)
	setOutcome(a, "failed")
	tt = ui.NewTester(a.view, 1120, 760)
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyT)
	if a.status != "quit" || !a.leaving {
		t.Fatal("Ctrl+Shift+T on an outcome did not leave")
	}

	a, _ = newTestApp(t, Options{})
	tt = ui.NewTester(a.view, 1120, 760)
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyT)
	if a.status != "" || a.leaving || a.askConsole {
		t.Fatal("Ctrl+Shift+T did something outside kiosk mode")
	}
}

// In kiosk mode a signal still cancels a running install and quits once the
// pipeline has unwound, and a recovery key still holds the first signal.
func TestKioskSignalCancelsTheInstallThenQuits(t *testing.T) {
	a, q := newTestApp(t, Options{Kiosk: true})
	ready(t, a)
	completeAnswers(a)
	a.furthest = pageReview
	a.goTo(pageReview)
	a.be.(*demoBackend).pace = time.Millisecond
	a.startInstall()
	if !a.installing() {
		t.Fatalf("install did not start: %s", a.installErr)
	}
	if !a.beforeQuitPrevented() || a.quitWhenDone {
		t.Fatal("a quit that is not a signal must do nothing in kiosk mode")
	}
	a.onSignal()
	if !a.quitWhenDone || !a.inst.canceling || a.status == "quit app" {
		t.Fatal("a signal during the install did not cancel it and wait")
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
	if a.status != "quit app" || a.inst.phase != phaseFailed || !a.forceQuit {
		t.Fatalf("after unwinding: status %q phase %v forceQuit %v", a.status, a.inst.phase, a.forceQuit)
	}
	if a.beforeQuitPrevented() || !a.closeAllowed() {
		t.Fatal("the quit a signal forces must go through")
	}
}

func TestKioskSignalOverAnUnsavedKey(t *testing.T) {
	a := kioskApp(t)
	setOutcome(a, "done")
	a.inst.recoveryKey = testRecoveryKey
	a.onSignal()
	if a.status != "" || !a.closeDenied || a.forceQuit {
		t.Fatal("the first signal must be refused over an unsaved recovery key")
	}
	a.onSignal()
	if a.status != "quit app" || !a.forceQuit || a.beforeQuitPrevented() {
		t.Fatal("a second signal must quit")
	}
}

func TestKioskHelpListsItsShortcuts(t *testing.T) {
	a := kioskApp(t)
	a.showHelp = true
	tt := ui.NewTester(a.view, 1120, 900)
	for _, text := range []string{"Use the console installer", "Restart (when the install has ended)", "Power off (when the install has ended)"} {
		if !tt.HasText(text) {
			t.Errorf("the kiosk help lacks %q: %q", text, tt.Texts())
		}
	}
	if tt.HasText("Quit the installer") {
		t.Errorf("the kiosk help lists Ctrl+Q: %q", tt.Texts())
	}

	b, _ := newTestApp(t, Options{})
	b.showHelp = true
	tt = ui.NewTester(b.view, 1120, 900)
	if !tt.HasText("Quit the installer") || tt.HasText("console installer") {
		t.Errorf("the help outside kiosk mode changed: %q", tt.Texts())
	}
	if got := helpShortcuts(false); len(got) != len(shortcutList) {
		t.Errorf("helpShortcuts(false) has %d entries, want %d", len(got), len(shortcutList))
	}
	for _, e := range helpShortcuts(true) {
		if strings.Contains(strings.Join(e.Keys, "+"), "Ctrl+Q") {
			t.Errorf("the kiosk list holds %v", e)
		}
	}
}
