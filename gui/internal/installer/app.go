// Package installer is firn's graphical installer (ADR-0021): a mouse- and
// keyboard-driven wizard drawn with mygo's native UI. Like the TUI it is a
// recipe generator that runs firn's pipeline in-process; it adds no
// contract of its own.
package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/frostyard/firn/internal/catalog"
	"github.com/frostyard/firn/internal/platform"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/runner"
)

// Options configures one run of the installer.
type Options struct {
	// Demo simulates the machine and the install: nothing is probed and no
	// disk is touched.
	Demo bool
	// SecureBoot, TPM and UEFI override detection: auto, on or off.
	SecureBoot, TPM, UEFI string
	// Appearance is "light" or "dark"; empty follows the desktop.
	Appearance string
	// Version is firn's version, for the progress protocol's start event.
	Version string
}

// recipeRoot owns real sessions: under /run, so no recipe or plaintext
// secret survives a reboot of the installer environment.
const recipeRoot = "/run/firn"

type page int

const (
	pageWelcome page = iota
	pageImage
	pageStorage
	pageSecurity
	pageRegion
	pageAccount
	pageApps
	pageReview
	pageInstall
	pageCount
)

// pageMeta is how a page presents itself in the sidebar and header.
var pageMeta = [pageCount]struct {
	nav, title, subtitle string
	icon                 *ui.SVG
}{
	pageWelcome:  {"Welcome", "Welcome", "", iconHome},
	pageImage:    {"System", "Choose your system", "Pick the snosi image to install. Each is a signed bootc image that updates itself atomically.", iconMonitor},
	pageStorage:  {"Storage", "Where should it go?", "Choose the disk to install to and how to format it.", iconDrive},
	pageSecurity: {"Security", "Protect your data", "Encrypt the disk so its contents stay private if the computer is lost or stolen.", iconLock},
	pageRegion:   {"Region", "Name and region", "Name this computer and set its language, time zone and keyboard.", iconGlobe},
	pageAccount:  {"Account", "Create your account", "This is the account you will sign in with.", iconUser},
	pageApps:     {"Apps", "Choose your apps", "Install Flatpak apps along with the system.", iconPackage},
	pageReview:   {"Review", "Ready to install", "Check your choices. Nothing has been written to disk yet.", iconList},
	pageInstall:  {"Install", "Installing", "", iconDownload},
}

// app is the installer's state. The view reads it on the main thread; other
// goroutines change it only through post.
type app struct {
	be      backend
	machine machineInfo
	catalog []catalog.Entry
	notices []string
	lists   regionLists

	session     string
	placeholder string
	// oldSessions are earlier sessions kept for inspection (a failed
	// attempt's recipe) and, in the demo, removed on exit.
	oldSessions []string
	// persisted is set once recipe.toml is written: the session then stays
	// for the printed headless command until the environment reboots.
	persisted bool

	page      page
	furthest  page
	attempted [pageCount]bool
	c         choices
	errs      fieldErrors

	disks        []diskInfo
	disksErr     string
	disksLoading bool
	// disksScanned is set after the first scan, the only one that may
	// choose a disk by itself; later scans never switch the user's choice.
	disksScanned bool
	// diskNotice says why a rescan cleared the chosen disk.
	diskNotice string

	preview        corePreview
	previewLoading string // the ref being inspected, or ""

	reviewed   []byte
	installErr string

	inst          installState
	cancelInstall context.CancelFunc
	// pipelineDone is closed once the install's pipeline has returned,
	// its cleanups unwound.
	pipelineDone chan struct{}
	// quitWhenDone quits once a running install, canceled by a signal or
	// a quit request, has finished unwinding.
	quitWhenDone bool
	// forceQuit lets a signal close the window over an unacknowledged
	// recovery key.
	forceQuit bool
	signals   int

	themeMode   int // 0 follows the desktop, 1 light, 2 dark
	showHelp    bool
	confirmOpen bool
	confirmText string
	askQuit     bool
	askCancel   bool
	closeDenied bool
	keySaved    bool
	showSecret  bool
	advancedSSH bool
	userSSH     bool
	recipeOpen  bool
	status      string

	ctx  context.Context
	post func(func())
	// quit closes the window, asking OnClose; quitApp quits the whole
	// application, asking OnBeforeQuit. Tests replace both.
	quit    func()
	quitApp func()
	p       palette
}

// Run probes the machine, opens the installer window and returns when it
// closes.
func Run(o Options) error {
	a, err := newApp(o)
	if err != nil {
		return err
	}
	defer a.cleanup()
	mygo.App.WhenReady(func() { a.openWindow() })
	err = mygo.App.Run()
	a.shutdown()
	return err
}

// openWindow shows the installer's window and starts the first disk scan.
// It runs on the main thread once the application is ready.
func (a *app) openWindow() *mygo.Window {
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:     "Install snosi",
		Width:     1120,
		Height:    760,
		MinWidth:  920,
		MinHeight: 640,
		Content:   ui.View(a.view),
	})
	a.post = win.Update
	a.quit = win.Close
	// The window stays while an install runs, and while a recovery key
	// waits to be acknowledged: it is shown once, and closing would lose it.
	win.OnClose(func(e *mygo.CloseEvent) {
		if !a.closeAllowed() {
			e.PreventDefault()
			win.Invalidate()
		}
	})
	// Any other quit cancels a running install and waits for its cleanups.
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if a.installing() {
			e.PreventDefault()
			a.quitWhenDone = true
			a.cancel()
			win.Invalidate()
		}
	})
	a.takeSignals()
	a.rescan()
	return win
}

// takeSignals replaces mygo's SIGINT and SIGTERM handling, which quits at
// once and exits outright on a second signal. An installer must instead
// cancel a running install and quit only after its cleanups have closed
// the mounts and encrypted volumes it opened.
func (a *app) takeSignals() {
	var sigs []os.Signal
	for _, s := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
		// Keep signals the parent ignores ignored, as mygo does.
		if !signal.Ignored(s) {
			sigs = append(sigs, s)
		}
	}
	if len(sigs) == 0 {
		return
	}
	signal.Reset(sigs...)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	post := a.post
	go func() {
		for range ch {
			post(a.onSignal)
		}
	}()
}

// onSignal handles SIGINT or SIGTERM on the main thread.
func (a *app) onSignal() {
	a.signals++
	switch {
	case a.installing():
		a.quitWhenDone = true
		a.cancel()
	case a.keyPending() && a.signals == 1:
		a.closeDenied = true // a second signal quits anyway
	default:
		a.forceQuit = true
		a.quitApp()
	}
}

// shutdown makes sure no install outlives the window: if the event loop
// ended with the pipeline still running, cancel it and wait for its
// cleanups to unwind, as the TUI does.
func (a *app) shutdown() {
	if a.cancelInstall != nil && a.pipelineDone != nil {
		a.cancelInstall()
		<-a.pipelineDone
	}
}

func (a *app) keyPending() bool { return a.inst.recoveryKey != "" }

// closeAllowed decides a request to close the window: refused, with an
// explanation, while an install runs or a recovery key waits to be
// acknowledged, unless a signal forces the quit.
func (a *app) closeAllowed() bool {
	if a.installing() || (a.keyPending() && !a.forceQuit) {
		a.closeDenied = true
		return false
	}
	return true
}

func newApp(o Options) (*app, error) {
	m, err := probeMachine(o)
	if err != nil {
		return nil, err
	}
	entries, warn := catalog.Load()
	a := &app{
		machine: m,
		catalog: entries,
		ctx:     context.Background(),
		inst:    newInstallState(),
		post:    func(fn func()) { fn() },
		quit:    func() {},
		quitApp: mygo.App.Quit,
	}
	if warn != nil {
		a.notices = append(a.notices, warn.Error())
	}
	switch o.Appearance {
	case "", "auto":
	case "light":
		a.themeMode = 1
	case "dark":
		a.themeMode = 2
	default:
		return nil, fmt.Errorf("--appearance must be light or dark; got %q", o.Appearance)
	}
	if o.Demo {
		a.be = newDemoBackend(m)
	} else {
		a.be = &realBackend{machine: m, runner: runner.New()}
	}
	if err := a.openSession(); err != nil {
		return nil, err
	}
	if o.Demo {
		// The demo machine has no installer medium, so its catalog's
		// signing key is a stand-in that keeps the recipe valid.
		if err := a.useDemoKey(); err != nil {
			return nil, err
		}
	}
	zoneinfo := m.env.ZoneinfoDir
	s := detectSession(os.Getenv, zoneinfo, "/etc/localtime", "/etc/default/keyboard")
	a.lists = loadRegionLists(zoneinfo, "/usr/share/i18n/SUPPORTED", "/usr/share/X11/xkb/rules/base.lst")
	a.c = defaultChoices(a.catalog[0], s)
	return a, nil
}

// probeMachine resolves the machine facts, honoring overrides. The demo
// machine is UEFI with a TPM and Secure Boot off unless overridden.
func probeMachine(o Options) (machineInfo, error) {
	m := machineInfo{
		env:     recipe.Env{ZoneinfoDir: "/usr/share/zoneinfo"},
		version: o.Version,
		demo:    o.Demo,
	}
	probe := func(name, v string, real func() bool, demo bool) (bool, error) {
		switch v {
		case "", "auto":
			if o.Demo {
				return demo, nil
			}
			return real(), nil
		case "on":
			return true, nil
		case "off":
			return false, nil
		}
		return false, fmt.Errorf("--%s must be auto, on, or off; got %q", name, v)
	}
	var err error
	if m.env.SecureBoot, err = probe("secure-boot", o.SecureBoot, platform.SecureBoot, false); err != nil {
		return m, err
	}
	if m.env.TPM, err = probe("tpm", o.TPM, platform.TPM, true); err != nil {
		return m, err
	}
	if m.uefi, err = probe("uefi", o.UEFI, platform.UEFI, true); err != nil {
		return m, err
	}
	return m, nil
}

func newSession(root string) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, "gui-session-")
}

func (a *app) useDemoKey() error {
	dir, err := os.MkdirTemp("", "firn-gui-demo-key-")
	if err != nil {
		return err
	}
	a.oldSessions = append(a.oldSessions, dir)
	key := filepath.Join(dir, "cosign.pub")
	if err := os.WriteFile(key, []byte("-----BEGIN PUBLIC KEY-----\ndemo\n-----END PUBLIC KEY-----\n"), 0o644); err != nil {
		return err
	}
	for i := range a.catalog {
		if a.catalog[i].CosignPubKey != "" {
			a.catalog[i].CosignPubKey = key
		}
	}
	return nil
}

// cleanup removes abandoned sessions. A persisted real session stays for
// the headless reproduction command, as the TUI's does; the demo leaves
// nothing behind.
func (a *app) cleanup() {
	if a.machine.demo {
		for _, dir := range append(a.oldSessions, a.session) {
			_ = os.RemoveAll(dir)
		}
		return
	}
	if !a.persisted && a.session != "" {
		_ = os.RemoveAll(a.session)
	}
}

func (a *app) installing() bool { return a.inst.phase == phaseRunning }

// --- background work ---

// rescan lists disks in the background, keeping the choice when the disk is
// still there and otherwise choosing the first usable one.
func (a *app) rescan() {
	if a.disksLoading {
		return
	}
	a.disksLoading = true
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		defer cancel()
		disks, err := a.be.Disks(ctx)
		a.post(func() { a.setDisks(disks, err) })
	}()
}

// setDisks records a scan. Only the first scan chooses a disk by itself,
// the first usable one; a later scan never moves the choice to another
// disk. When the chosen disk is gone or refused, the choice is cleared with
// a notice, so the user picks the target again.
func (a *app) setDisks(disks []diskInfo, err error) {
	a.disksLoading = false
	a.disks, a.disksErr = disks, ""
	if err != nil {
		a.disksErr = err.Error()
		return
	}
	first := !a.disksScanned
	a.disksScanned = true
	if prev := a.c.disk; prev != "" {
		for _, d := range disks {
			if d.Path != prev {
				continue
			}
			if d.refused != "" {
				a.c.disk = ""
				a.diskNotice = fmt.Sprintf("%s can no longer be used (%s). Choose the disk to install to.", prev, d.refused)
			}
			return
		}
		a.c.disk = ""
		a.diskNotice = prev + " is no longer present. Choose the disk to install to."
		return
	}
	if !first {
		return
	}
	for _, d := range disks {
		if d.refused == "" {
			a.c.disk = d.Path
			return
		}
	}
}

func (a *app) refusedDisks() map[string]string {
	out := make(map[string]string, len(a.disks))
	for _, d := range a.disks {
		if d.refused != "" {
			out[d.Path] = d.refused
		}
	}
	return out
}

// refreshPreview inspects the chosen image's core Flatpak set when the
// preview describes another image and no inspection of it is running.
func (a *app) refreshPreview() {
	ref := a.c.entry.Ref
	if a.preview.ref == ref || a.previewLoading == ref {
		return
	}
	a.previewLoading = ref
	go func() {
		p := a.be.InspectCore(a.ctx, ref)
		a.post(func() { a.setPreview(p) })
	}()
}

// setPreview records an inspection and applies the image's initial
// core-flatpaks selection once (ADR-0019): on whenever the toggle is
// offered, off when there is no set to offer.
func (a *app) setPreview(p corePreview) {
	if a.previewLoading == p.ref {
		a.previewLoading = ""
	}
	if p.ref != a.c.entry.Ref {
		return // the user chose another image meanwhile
	}
	a.preview = p
	switch p.state {
	case corePreviewNone, corePreviewInvalid:
		a.c.coreFlatpaks = false
	default:
		if a.c.coreRef != p.ref {
			a.c.coreFlatpaks = true
		}
	}
	a.c.coreRef = p.ref
}

// --- validation and navigation ---

// validate recomputes every field problem for the current choices, adding
// what only the app knows: whether the chosen image's app list has been read
// (its initial selection depends on it) and whether the machine is UEFI.
func (a *app) validate() fieldErrors {
	errs := validateChoices(a.c, a.machine.env, a.placeholder, a.refusedDisks())
	if a.preview.ref != a.c.entry.Ref {
		errs[fieldCorePreview] = fieldError{msg: "Reading this image's app list…", required: true}
	}
	if !a.machine.uefi {
		errs[fieldUEFI] = fieldError{msg: sentence(platform.RequireUEFI(false).Error())}
	}
	return errs
}

// errText is the message to show under field: a wrong value at once, a
// missing one after the user tried to leave the page.
func (a *app) errText(field string) string {
	e, ok := a.errs[field]
	if !ok || (e.required && !a.attempted[pageOf(field)]) {
		return ""
	}
	return e.msg
}

func (a *app) pageValid(p page) bool { return len(a.errs.onPage(p)) == 0 }

// canReach reports whether the sidebar or Alt+number may jump to p: a page
// already visited, with every page before it complete.
func (a *app) canReach(p page) bool {
	if a.page == pageInstall || p >= pageInstall || p > a.furthest {
		return false
	}
	for q := pageImage; q < p; q++ {
		if !a.pageValid(q) {
			return false
		}
	}
	return true
}

func (a *app) goTo(p page) {
	if p > pageImage && p < pageInstall {
		a.refreshPreview() // past the image page, its app list is needed
	}
	if p == pageReview {
		a.prepareReview()
	}
	a.page = p
	if p > a.furthest {
		a.furthest = p
	}
}

// next advances when the page is complete, and otherwise shows its errors.
func (a *app) next() {
	switch a.page {
	case pageInstall:
		return
	case pageReview:
		a.openConfirm()
		return
	}
	if !a.pageValid(a.page) {
		a.attempted[a.page] = true
		return
	}
	a.goTo(a.page + 1)
}

func (a *app) back() {
	if a.page > pageWelcome && a.page < pageInstall {
		a.goTo(a.page - 1)
	}
}

func (a *app) jump(p page) {
	if a.canReach(p) {
		a.goTo(p)
	}
}

func (a *app) prepareReview() {
	a.installErr = ""
	data, err := reviewBytes(a.c, a.machine.env.SecureBoot, a.session)
	if err != nil {
		a.reviewed = nil
		a.installErr = err.Error()
		return
	}
	a.reviewed = data
}

func (a *app) openConfirm() {
	for p := pageImage; p < pageReview; p++ {
		if !a.pageValid(p) {
			a.attempted[p] = true
			a.goTo(p)
			return
		}
	}
	if a.reviewed == nil || len(a.errs.onPage(pageReview)) > 0 {
		return
	}
	a.confirmText = ""
	a.confirmOpen = true
}

// --- install ---

func (a *app) startInstall() {
	a.confirmOpen = false
	if a.installing() || a.cancelInstall != nil {
		return // one install at a time: Enter and a click can land together
	}
	path, l, err := persist(a.c, a.machine.env, a.session, a.reviewed)
	if err != nil {
		a.installErr = err.Error()
		if cerr := cleanupSecretFiles(a.session); cerr != nil {
			a.installErr += "; " + cerr.Error()
		}
		return
	}
	a.persisted = true
	a.inst = newInstallState()
	a.inst.phase = phaseRunning
	a.inst.recipePath = path
	a.inst.started = time.Now()
	a.keySaved = false
	a.page = pageInstall

	ctx, cancel := context.WithCancel(a.ctx)
	a.cancelInstall = cancel
	done := make(chan struct{})
	a.pipelineDone = done
	ch := progress.NewChannel(64)
	var pipeErr error
	go func() {
		defer close(done)
		defer func() { _ = ch.Close() }()
		pipeErr = a.be.Install(ctx, l, ch)
	}()
	// The consumer keeps draining after the window is gone (posts are then
	// dropped), so the pipeline never blocks on a full channel.
	go func() {
		for e := range ch.Events() {
			a.post(func() { a.inst.apply(e) })
		}
		<-done
		err := pipeErr
		a.post(func() {
			a.inst.finish(err)
			cancel()
			a.cancelInstall = nil
			if a.quitWhenDone {
				a.forceQuit = true
				a.quitApp()
			}
		})
	}()
}

func (a *app) cancel() {
	a.askCancel = false
	if a.cancelInstall != nil && a.installing() {
		a.inst.canceling = true
		a.cancelInstall()
	}
}

// retry returns to the review page after a failed install, keeping every
// answer. The failed attempt's session stays for inspection; the next
// attempt persists into a fresh one.
func (a *app) retry() {
	if err := a.openSession(); err != nil {
		a.installErr = err.Error()
	}
	a.inst = newInstallState()
	a.persisted = false
	a.goTo(pageReview)
}

// openSession starts a private session directory with the placeholder file
// live validation points every secret at.
func (a *app) openSession() error {
	var dir string
	var err error
	if a.machine.demo {
		dir, err = os.MkdirTemp("", "firn-gui-demo-")
	} else {
		dir, err = newSession(recipeRoot)
	}
	if err != nil {
		return fmt.Errorf("create installer session: %w", err)
	}
	placeholder, err := writeSecretFile(dir, ".placeholder", "")
	if err != nil {
		return errors.Join(err, os.RemoveAll(dir))
	}
	if a.session != "" {
		a.oldSessions = append(a.oldSessions, a.session)
	}
	a.session, a.placeholder = dir, placeholder
	return nil
}

func (a *app) reboot() {
	if a.machine.demo {
		a.status = "Demo mode: this is where the computer would restart."
		return
	}
	if err := a.be.Reboot(); err != nil {
		a.status = "Could not restart: " + err.Error()
	}
}

func (a *app) requestQuit() {
	switch {
	case a.installing(), a.keyPending():
		a.closeDenied = true
	case a.page == pageWelcome || a.inst.phase != phaseIdle:
		a.quit()
	default:
		a.askQuit = true
	}
}

// --- the view ---

func (a *app) dark(c *ui.Context) bool {
	switch a.themeMode {
	case 1:
		return false
	case 2:
		return true
	}
	return c.Theme().Dark
}

func (a *app) view(c *ui.Context) {
	a.p = newPalette(a.dark(c))
	c.SetTheme(a.p.theme(*c.Theme()))
	a.errs = a.validate()
	a.shortcuts(c)

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.sidebar(c)
		ui.Column(c).Grow(1).Shrink(1).Background(a.p.window).Children(func() {
			if a.page == pageWelcome {
				a.welcomePage(c)
				return
			}
			a.header(c)
			ui.Scroll(c.Key(a.page)).Grow(1).Children(func() {
				ui.Column(c).Padding(8, 40, 32).Gap(18).MaxWidth(880).Children(func() {
					a.pageBody(c)
				})
			})
			a.footer(c)
		})
	})
	a.dialogs(c)
}

func (a *app) pageBody(c *ui.Context) {
	switch a.page {
	case pageImage:
		a.imagePage(c)
	case pageStorage:
		a.storagePage(c)
	case pageSecurity:
		a.securityPage(c)
	case pageRegion:
		a.regionPage(c)
	case pageAccount:
		a.accountPage(c)
	case pageApps:
		a.appsPage(c)
	case pageReview:
		a.reviewPage(c)
	case pageInstall:
		a.installPage(c)
	}
}

// sidebar shows the brand, the steps and the window's tools.
func (a *app) sidebar(c *ui.Context) {
	p := a.p
	ui.Column(c).Width(264).Shrink(0).Padding(28, 18, 18).Gap(4).
		LinearGradient(ui.LinearGradient{From: p.sideTop, To: p.sideBottom, Angle: 160, Oklab: true}).
		TextColor(p.sideText).Children(func() {
		ui.Row(c).Gap(12).Padding(0, 8, 24).Children(func() {
			ui.Row(c).Size(42, 42).Radius(12).Center().
				LinearGradient(ui.LinearGradient{From: ui.Hex("#7cc4ff"), To: ui.Hex("#3b6ff5"), Angle: 135, Oklab: true}).
				Shadow(0, 6, 16, -4, ui.RGBA(40, 120, 255, 0.55)).Children(func() {
				ui.Icon(c, iconSnowflake).FontSize(24).TextColor(ui.Hex("#ffffff"))
			})
			ui.Column(c).Gap(1).Children(func() {
				ui.Text(c, "snosi").FontSize(19).FontWeight(700).LetterSpacing(0.2)
				ui.Text(c, "Installer").FontSize(12.5).TextColor(p.sideMuted)
			})
		})
		for pg := pageWelcome; pg < pageCount; pg++ {
			a.stepItem(c, pg)
		}
		ui.Spacer(c)
		if a.machine.demo {
			ui.Row(c).Gap(8).Padding(10, 12).Radius(10).Background(ui.RGBA(255, 196, 0, 0.16)).Children(func() {
				ui.Icon(c, iconSparkle).FontSize(15).TextColor(ui.Hex("#ffd166"))
				ui.Column(c).Gap(1).Shrink(1).Children(func() {
					ui.Text(c, "Demo mode").FontSize(12.5).FontWeight(600).TextColor(ui.Hex("#ffd166"))
					ui.Text(c, "Nothing is written to disk.").FontSize(11.5).TextColor(p.sideMuted)
				})
			})
		}
		ui.Row(c).Gap(6).Padding(10, 0, 0).Children(func() {
			a.sideTool(c, iconKeyboard, "Keyboard shortcuts (F1)", func() { a.showHelp = true })
			themeIcon, themeTip := iconMoon, "Dark appearance (Ctrl+Shift+L)"
			if a.p.dark {
				themeIcon, themeTip = iconSun, "Light appearance (Ctrl+Shift+L)"
			}
			a.sideTool(c, themeIcon, themeTip, a.toggleTheme)
			ui.Spacer(c)
			v := a.machine.version
			if v == "" {
				v = "dev"
			}
			ui.Text(c, "firn "+v).FontSize(11.5).TextColor(p.sideMuted).SingleLine()
		})
	})
}

func (a *app) sideTool(c *ui.Context, ic *ui.SVG, tip string, fn func()) {
	b := ui.ButtonBase(c).Size(34, 34).Radius(9).Center().Label(tip).Tooltip(tip).Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(a.p.sideActive)
	}
	b.Children(func() { ui.Icon(c, ic).FontSize(17).TextColor(a.p.sideText) })
	if b.Clicked() {
		fn()
	}
}

func (a *app) toggleTheme() {
	if a.p.dark {
		a.themeMode = 1
	} else {
		a.themeMode = 2
	}
}

// stepItem is one step of the sidebar: done, current, or ahead.
func (a *app) stepItem(c *ui.Context, pg page) {
	p := a.p
	current := pg == a.page
	done := (pg < a.page && a.pageValid(pg)) || (pg == pageInstall && a.inst.phase == phaseDone)
	reachable := a.canReach(pg) && !current
	// Only steps the user can jump to are buttons; the rest are labels, so
	// Tab skips them and they keep full contrast.
	var row ui.Element
	if reachable {
		row = ui.ButtonBase(c.Key(pg)).Label(pageMeta[pg].nav).
			Tooltip(fmt.Sprintf("Go to %s (Alt+%d)", pageMeta[pg].nav, int(pg)+1)).Cursor(ui.CursorPointer)
		if row.Hovered() {
			row.Background(ui.RGBA(255, 255, 255, 0.07))
		}
	} else {
		row = ui.Row(c.Key(pg))
	}
	row.Gap(12).Padding(8, 10).Radius(10).Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
	if current {
		row.Background(p.sideActive)
	}
	row.Children(func() {
		dot := ui.Row(c).Size(26, 26).Radius(13).Center().Shrink(0)
		switch {
		case done:
			dot.Background(ui.Hex("#3fcf8e")).Children(func() {
				ui.Icon(c, iconCheck).FontSize(15).TextColor(ui.Hex("#06301c"))
			})
		case current:
			dot.Background(ui.Hex("#ffffff")).Children(func() {
				ui.Icon(c, pageMeta[pg].icon).FontSize(14).TextColor(p.sideTop)
			})
		default:
			dot.Border(1.5, p.sideLine).Children(func() {
				ui.Icon(c, pageMeta[pg].icon).FontSize(13).TextColor(p.sideMuted)
			})
		}
		label := ui.Text(c, pageMeta[pg].nav).FontSize(14).Grow(1).SingleLine()
		switch {
		case current:
			label.FontWeight(600)
		case !done:
			label.TextColor(p.sideMuted)
		}
		if reachable && row.Hovered() {
			ui.Text(c, fmt.Sprintf("Alt+%d", int(pg)+1)).FontSize(11).TextColor(p.sideMuted)
		}
	})
	if reachable && row.Clicked() {
		a.jump(pg)
	}
}

func (a *app) header(c *ui.Context) {
	p := a.p
	meta := pageMeta[a.page]
	ui.Column(c).Padding(32, 40, 18).Gap(6).MaxWidth(880).Children(func() {
		if a.page < pageInstall {
			ui.Text(c, fmt.Sprintf("STEP %d OF %d", int(a.page), int(pageReview))).
				FontSize(11.5).FontWeight(700).LetterSpacing(1.2).TextColor(p.accent)
		}
		title, subtitle := meta.title, meta.subtitle
		if a.page == pageInstall {
			title, subtitle = a.installHeadline()
		}
		ui.Text(c, title).FontSize(27).FontWeight(700).LetterSpacing(-0.3)
		if subtitle != "" {
			ui.Text(c, subtitle).FontSize(14.5).TextColor(p.muted)
		}
	})
}

// footer holds the page's actions and the keyboard hints for them.
func (a *app) footer(c *ui.Context) {
	p := a.p
	ui.Row(c).Padding(14, 40).Gap(10).Background(p.card).BorderWidth(1, 0, 0, 0).BorderColor(p.cardBorder).Children(func() {
		if a.page == pageInstall {
			a.installFooter(c)
			return
		}
		ui.Row(c).Gap(14).Grow(1).Shrink(1).Children(func() {
			hint := func(label string, combo ...string) {
				ui.Row(c).Gap(6).Children(func() {
					keys(c, p, combo...)
					ui.Text(c, label).FontSize(12).TextColor(p.faint).SingleLine()
				})
			}
			hint("Back", "Alt", "←")
			hint("Continue", "Alt", "→")
			hint("Shortcuts", "F1")
		})
		if msg := a.blockedHint(); msg != "" {
			ui.Text(c, msg).FontSize(13).TextColor(p.danger).SingleLine()
		}
		if ui.Button(c, "").Label("Back").Children(func() {
			ui.Icon(c, iconArrowLeft).FontSize(15)
			ui.Text(c, "Back").SingleLine()
		}).Padding(9, 16).Clicked() {
			a.back()
		}
		label, ic := "Continue", iconArrowRight
		if a.page == pageReview {
			label, ic = "Install…", iconDownload
		}
		next := ui.PrimaryButton(c, "").Label(label).Padding(9, 18).Children(func() {
			ui.Text(c, label).SingleLine().FontWeight(600)
			ui.Icon(c, ic).FontSize(15)
		})
		if a.page == pageReview {
			next.Background(p.danger).Tooltip("Erase the disk and install (Ctrl+Enter)")
		} else {
			next.Tooltip("Continue (Alt+→ or Ctrl+Enter)")
		}
		if next.Clicked() {
			a.next()
		}
	})
}

// blockedHint explains, after a failed attempt to continue, how many fields
// need attention.
func (a *app) blockedHint() string {
	if !a.attempted[a.page] {
		return ""
	}
	if n := len(a.errs.onPage(a.page)); n > 0 {
		if n == 1 {
			return "1 field needs attention"
		}
		return fmt.Sprintf("%d fields need attention", n)
	}
	return ""
}

// shortcuts binds the window's keyboard shortcuts. Text inputs leave Alt with
// the arrows and the function keys alone, so these work while typing.
func (a *app) shortcuts(c *ui.Context) {
	if c.Shortcut(0, ui.KeyF1) || c.Shortcut(ui.Ctrl, ui.KeySlash) || c.Shortcut(ui.Ctrl|ui.Shift, ui.KeySlash) {
		a.showHelp = true
	}
	if c.Shortcut(ui.Ctrl|ui.Shift, ui.KeyL) {
		a.toggleTheme()
	}
	if c.Shortcut(ui.Ctrl, ui.KeyQ) {
		a.requestQuit()
	}
	if a.page == pageInstall {
		return
	}
	if c.Shortcut(ui.Alt, ui.KeyRight) || c.Shortcut(0, ui.KeyForward) || c.Shortcut(ui.Ctrl, ui.KeyEnter) {
		a.next()
	}
	if c.Shortcut(ui.Alt, ui.KeyLeft) || c.Shortcut(0, ui.KeyBack) {
		a.back()
	}
	digits := []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8}
	for i, k := range digits {
		if c.Shortcut(ui.Alt, k) {
			a.jump(page(i))
		}
	}
	if a.page == pageStorage && (c.Shortcut(ui.Ctrl, ui.KeyR) || c.Shortcut(0, ui.KeyF5)) {
		a.rescan()
	}
}

// shortcutList is every shortcut, for the help overlay.
var shortcutList = []struct {
	combo []string
	what  string
}{
	{[]string{"Alt", "→"}, "Continue to the next step"},
	{[]string{"Ctrl", "Enter"}, "Continue, or install from the review"},
	{[]string{"Alt", "←"}, "Go back a step"},
	{[]string{"Alt", "1…8"}, "Jump to a step you have reached"},
	{[]string{"Tab"}, "Move to the next control (Shift+Tab: previous)"},
	{[]string{"↑", "↓"}, "Choose among cards and options"},
	{[]string{"Space"}, "Select a card, toggle a switch or check box"},
	{[]string{"Ctrl", "R"}, "Rescan disks (Storage step; also F5)"},
	{[]string{"Ctrl", "Shift", "L"}, "Switch between light and dark"},
	{[]string{"F1"}, "Show this list (also Ctrl+/)"},
	{[]string{"Esc"}, "Close a dialog, this one included"},
	{[]string{"Ctrl", "Q"}, "Quit the installer"},
}

func (a *app) dialogs(c *ui.Context) {
	p := a.p
	ui.Modal(c, &a.showHelp, func() {
		ui.Column(c).Width(460).Gap(14).Children(func() {
			ui.Row(c).Gap(10).Children(func() {
				ui.Icon(c, iconKeyboard).FontSize(20).TextColor(p.accent)
				ui.Text(c, "Keyboard shortcuts").FontSize(17).FontWeight(700)
			})
			ui.Grid(c).ColumnTracks(ui.FitContent(), ui.Fr(1)).GapX(18).GapY(10).AlignItems(ui.Center).Children(func() {
				for _, s := range shortcutList {
					keys(c, p, s.combo...)
					ui.Text(c, s.what).FontSize(13.5)
				}
			})
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.PrimaryButton(c, "Done").Clicked() {
					a.showHelp = false
				}
			})
		})
	})

	a.confirmDialog(c)
	a.recoveryKeyDialog(c)

	switch ui.AlertDialog(c, &a.askQuit, "Quit the installer?", "Nothing has been written to disk. Your answers will be lost.", "Keep going", "Quit") {
	case 1:
		a.quit()
	}
	switch ui.AlertDialog(c, &a.askCancel, "Stop the installation?", "The disk has already been changed and will not boot until you install again.", "Keep installing", "Stop") {
	case 1:
		a.cancel()
	}
	if a.keyPending() && !a.installing() {
		ui.AlertDialog(c, &a.closeDenied, "Save the recovery key first", "It is shown only once. Confirm you have stored it, then close the installer.", "OK")
	} else {
		ui.AlertDialog(c, &a.closeDenied, "Installation in progress", "Wait for the installation to finish, or stop it first.", "OK")
	}
}
