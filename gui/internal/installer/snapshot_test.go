package installer

import (
	"os"
	"testing"
)

// TestSnapshots renders every page, in both appearances, into the directory
// FIRN_GUI_SNAPSHOTS names, for reviewing the look. Without it the pages
// still render, which catches build-lifetime and layout panics.
func TestSnapshots(t *testing.T) {
	dir := os.Getenv("FIRN_GUI_SNAPSHOTS")
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, dark := range []bool{false, true} {
		a, _ := newTestApp(t, Options{SecureBoot: "on"})
		ready(t, a)
		snapshot(t, a, dir, "01-welcome", dark, nil)
		for _, pg := range []page{pageImage, pageStorage, pageSecurity, pageRegion, pageAccount, pageApps} {
			a.goTo(pg)
			snapshot(t, a, dir, pageFile(pg), dark, nil)
			snapshotSized(t, a, dir, pageFile(pg)+"-full", dark, 1300, nil)
		}
		a.c.encryption = "tpm2-luks-passphrase"
		a.c.passphrase, a.c.passphraseConfirm = "hunter22", "hunter2"
		a.goTo(pageSecurity)
		a.attempted[pageSecurity] = true
		snapshot(t, a, dir, "04b-security-errors", dark, nil)

		a.c.passphraseConfirm = a.c.passphrase
		a.c.mokPassword, a.c.mokConfirm = "snosi", "snosi"
		completeAnswers(a)
		a.c.flatpaksRaw = "org.mozilla.firefox"
		a.goTo(pageReview)
		snapshot(t, a, dir, "08-review", dark, nil)
		a.recipeOpen = true
		snapshotSized(t, a, dir, "08b-review-full", dark, 1700, nil)
		a.recipeOpen = false

		a.openConfirm()
		a.confirmText = "/dev/nvme"
		snapshot(t, a, dir, "08c-confirm", dark, nil)
		a.confirmOpen = false

		a.showHelp = true
		snapshot(t, a, dir, "10-shortcuts", dark, nil)
		a.showHelp = false

		events := recordEvents(t, a)
		a.page = pageInstall
		a.inst = newInstallState()
		a.inst.phase = phaseRunning
		a.inst.recipePath = a.session + "/recipe.toml"
		for _, e := range events[:len(events)*3/5] {
			a.inst.apply(e)
		}
		snapshot(t, a, dir, "09-installing", dark, nil)
		for _, e := range events[len(events)*3/5:] {
			a.inst.apply(e)
		}
		snapshot(t, a, dir, "09b-done", dark, nil)
		snapshotSized(t, a, dir, "09b-done-full", dark, 1300, nil)

		a.inst = newInstallState()
		a.inst.phase = phaseRunning
		for _, e := range events[:5] {
			a.inst.apply(e)
		}
		a.inst.recoveryKey = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
		snapshot(t, a, dir, "09c-recovery-key", dark, nil)
	}
}

func pageFile(p page) string {
	names := map[page]string{
		pageImage: "02-image", pageStorage: "03-storage", pageSecurity: "04-security",
		pageRegion: "05-region", pageAccount: "06-account", pageApps: "07-apps",
	}
	return names[p]
}
