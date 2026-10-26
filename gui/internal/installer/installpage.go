package installer

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

// installHeadline is the install page's title and subtitle for its phase.
func (a *app) installHeadline() (string, string) {
	name := displayName(a.c.entry.Name)
	switch a.inst.phase {
	case phaseDone:
		return name + " is installed", "Remove the installation media, then restart into your new system."
	case phaseFailed:
		if a.inst.canceling {
			return "Installation stopped", "The install was stopped before it finished."
		}
		return "Installation failed", "Nothing more will be written to the disk."
	}
	if a.inst.canceling {
		return "Stopping…", "Undoing mounts and closing the disk. This can take a moment."
	}
	return "Installing " + name, fmt.Sprintf("to %s with %s. This usually takes several minutes.", a.c.disk, a.c.filesystem)
}

func (a *app) installPage(c *ui.Context) {
	p := a.p
	s := &a.inst
	if s.phase == phaseRunning {
		c.After(time.Second) // keep the elapsed time ticking
	}
	switch s.phase {
	case phaseDone:
		a.outcome(c, calloutSuccess)
	case phaseFailed:
		a.outcome(c, calloutDanger)
	}

	card(c, p).Children(func() {
		ui.Row(c).Gap(16).AlignItems(ui.End).Children(func() {
			ui.Column(c).Gap(4).Grow(1).Shrink(1).Children(func() {
				label := stepTitle(s.currentName())
				switch {
				case s.phase == phaseDone:
					label = "Complete"
				case s.phase == phaseFailed:
					label = "Stopped at: " + stepTitle(s.failedStep)
				case s.current < 0:
					label = "Starting…"
				}
				ui.Text(c, label).FontSize(16).FontWeight(600)
				detail := ""
				if s.stepTotal > 0 && s.phase == phaseRunning {
					detail = humanBytes(s.stepBytes) + " of " + humanBytes(s.stepTotal)
				}
				if !s.started.IsZero() {
					end := time.Now()
					if !s.finished.IsZero() {
						end = s.finished
					}
					if detail != "" {
						detail += "  ·  "
					}
					detail += humanDuration(end.Sub(s.started)) + " elapsed"
				}
				ui.Text(c, detail).FontSize(13).TextColor(p.muted).FontFeatures("tnum")
			})
			ui.Text(c, fmt.Sprintf("%d%%", int(s.overall()*100+0.5))).FontSize(34).FontWeight(800).
				FontFeatures("tnum").TextColor(p.accent)
		})
		bar := ui.Progress(c, s.overall()).Label("Overall progress")
		if s.phase == phaseFailed {
			bar.TextColor(p.danger)
		}
	})

	ui.Grid(c).ColumnTracks(ui.Fr(1), ui.Fr(1.3)).Gap(14).AlignItems(ui.Start).Children(func() {
		card(c, p).Gap(4).Children(func() {
			ui.Text(c, "Steps").FontSize(15).FontWeight(600).Padding(0, 0, 8)
			for i, st := range s.steps {
				a.stepRow(c, i, stepTitle(st.Name))
			}
			if len(s.steps) == 0 {
				ui.Row(c).Gap(10).Children(func() {
					ui.Spinner(c)
					ui.Text(c, "Preparing…").TextColor(p.muted)
				})
			}
		})
		card(c, p).Children(func() {
			ui.Text(c, "Activity").FontSize(15).FontWeight(600)
			ui.Column(c).Gap(6).Padding(12, 14).Radius(10).Background(p.inset).Border(1, p.cardBorder).MinHeight(180).Selectable().Children(func() {
				lines := s.log
				if len(lines) > 12 {
					lines = lines[len(lines)-12:]
				}
				if len(lines) == 0 {
					ui.Text(c, "Waiting for the first step…").FontSize(12.5).TextColor(p.faint).Font("monospace")
				}
				for _, l := range lines {
					t := ui.Text(c, l.text).FontSize(12.5).Font("monospace").LineHeight(1.4)
					if l.warning {
						t.TextColor(p.warning)
						ui.Text(c, "["+l.code+"]").FontSize(11.5).Font("monospace").TextColor(p.faint)
					}
				}
			})
		})
	})
}

func (a *app) stepRow(c *ui.Context, i int, title string) {
	p := a.p
	s := &a.inst
	done := i < s.current || s.phase == phaseDone
	active := i == s.current && s.phase == phaseRunning
	failed := s.phase == phaseFailed && i == s.current
	ui.Row(c.Key(i)).Gap(10).Padding(5, 4).Children(func() {
		box := ui.Row(c).Size(20, 20).Center().Shrink(0)
		switch {
		case failed:
			box.Children(func() { ui.Icon(c, iconError).FontSize(18).TextColor(p.danger) })
		case done:
			box.Radius(10).Background(p.successSoft).Children(func() {
				ui.Icon(c, iconCheck).FontSize(13).TextColor(p.success)
			})
		case active:
			box.Children(func() { ui.Spinner(c).Size(16, 16) })
		default:
			box.Children(func() { ui.Box(c).Size(8, 8).Radius(4).Background(p.cardBorder) })
		}
		t := ui.Text(c, title).FontSize(13.5).Grow(1).SingleLine()
		switch {
		case active:
			t.FontWeight(600)
		case failed:
			t.TextColor(p.danger).FontWeight(600)
		case !done:
			t.TextColor(p.faint)
		}
		if active && s.stepFraction > 0 {
			ui.Text(c, fmt.Sprintf("%d%%", int(s.stepFraction*100))).FontSize(12).TextColor(p.muted).FontFeatures("tnum")
		}
	})
}

// outcome is the success or failure panel above the progress.
func (a *app) outcome(c *ui.Context, kind calloutKind) {
	p := a.p
	s := &a.inst
	fg, bg, ic := p.success, p.successSoft, iconSuccess
	if kind == calloutDanger {
		fg, bg, ic = p.danger, p.dangerSoft, iconError
	}
	ui.Column(c).Padding(22).Gap(14).Radius(16).Background(bg).Border(1, fg.Alpha(0.35)).Children(func() {
		ui.Row(c).Gap(14).Children(func() {
			ui.Icon(c, ic).FontSize(34).TextColor(fg)
			ui.Column(c).Gap(3).Grow(1).Shrink(1).Children(func() {
				switch {
				case kind == calloutSuccess:
					ui.Text(c, "Your new system is ready").FontSize(18).FontWeight(700)
					next := "Restart when you are ready."
					switch a.c.encryption {
					case "luks-passphrase":
						next = "At startup you will be asked for your disk passphrase."
					case "tpm2-luks", "tpm2-luks-passphrase":
						next = "The TPM unlocks the disk automatically at startup."
					}
					if a.machine.env.SecureBoot && a.c.mok == "enroll" {
						next += " MokManager will ask for your one-time password: choose Enroll MOK."
					}
					ui.Text(c, next).FontSize(13.5).TextColor(p.text)
				case s.canceling:
					ui.Text(c, "The installation was stopped").FontSize(18).FontWeight(700)
					ui.Text(c, "The disk will not boot until you install again.").FontSize(13.5)
				default:
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, "Something went wrong").FontSize(18).FontWeight(700)
						if s.errCode != "" {
							chip(c, s.errCode, fg, p.card).Font("monospace")
						}
					})
					ui.Text(c, s.errMessage).FontSize(13.5).Selectable()
				}
			})
		})
		for _, item := range s.summary {
			callout(c, p, calloutWarning, summaryTitle(item.Code), item.Detail)
		}
		if s.recipePath != "" {
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "Reproduce this install without the wizard:").FontSize(12.5).TextColor(p.muted)
				codeBlock(c, p, reproduceCommand(a.c.disk, s.recipePath))
				if !a.machine.demo {
					ui.Text(c, "The recipe and its secret files remain until this environment restarts.").FontSize(12).TextColor(p.faint)
				}
			})
		}
		if a.status != "" {
			ui.Text(c, a.status).FontSize(13).TextColor(p.muted)
		}
	})
}

func (a *app) installFooter(c *ui.Context) {
	p := a.p
	s := &a.inst
	switch s.phase {
	case phaseRunning:
		ui.Row(c).Gap(8).Grow(1).Shrink(1).Children(func() {
			ui.Icon(c, iconPower).FontSize(15).TextColor(p.faint)
			ui.Text(c, "Keep the computer plugged in and awake until the install finishes.").FontSize(13).TextColor(p.faint).SingleLine()
		})
		stop := ui.Button(c, "Stop installation").Disabled(s.canceling)
		if stop.Clicked() {
			a.askCancel = true
		}
	case phaseDone:
		ui.Spacer(c)
		if ui.Button(c, "Close").Clicked() {
			a.quit()
		}
		if ui.PrimaryButton(c, "").Label("Restart now").Padding(9, 18).Children(func() {
			ui.Icon(c, iconPower).FontSize(15)
			ui.Text(c, "Restart now").FontWeight(600).SingleLine()
		}).Clicked() {
			a.reboot()
		}
	case phaseFailed:
		ui.Spacer(c)
		if ui.Button(c, "Close").Clicked() {
			a.quit()
		}
		if ui.PrimaryButton(c, "").Label("Back to review").Padding(9, 18).Children(func() {
			ui.Icon(c, iconArrowLeft).FontSize(15)
			ui.Text(c, "Back to review").FontWeight(600).SingleLine()
		}).Clicked() {
			a.retry()
		}
	}
}

// confirmDialog is the typed confirmation: the user must type the exact
// disk path, the rule headless installs enforce with --confirm.
func (a *app) confirmDialog(c *ui.Context) {
	p := a.p
	d, _ := a.chosenDisk()
	matches := func() bool { return a.confirmText == a.c.disk }
	ui.DialogBase(c, &a.confirmOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(4, 10, 22, 0.55))
		panel.Width(500).Padding(26).Gap(16).Radius(18).Background(p.card).Border(1, p.cardBorder).
			Shadow(0, 24, 60, -12, ui.RGBA(0, 0, 0, 0.45)).Role(ui.RoleAlertDialog).Label("Confirm install")
		ui.Row(c).Gap(14).AlignItems(ui.Start).Children(func() {
			iconTile(c, p, iconWarning, p.danger, p.dangerSoft, 44)
			ui.Column(c).Gap(6).Grow(1).Shrink(1).Children(func() {
				ui.Text(c, "Erase "+d.title()+" and install "+displayName(a.c.entry.Name)+"?").FontSize(17).FontWeight(700)
				ui.Text(c, fmt.Sprintf("Everything on %s (%s) will be permanently destroyed. This cannot be undone.", a.c.disk, humanBytes(d.Size))).
					FontSize(13.5).TextColor(p.muted)
			})
		})
		if ids := diskIdentity(d); ids != "" {
			ui.Text(c, ids).Font("monospace").FontSize(12.5).TextColor(p.muted).
				Padding(10, 12).Radius(8).Background(p.inset).Border(1, p.cardBorder).Selectable()
		}
		ui.Field(c, "Type "+a.c.disk+" to confirm", func() {
			in := ui.TextInput(c, &a.confirmText).Placeholder(a.c.disk).Font("monospace").AutoFocus()
			if in.Submitted() && matches() {
				a.startInstall()
			}
		})
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			if ui.Button(c, "Cancel").Clicked() {
				a.confirmOpen = false
			}
			erase := ui.PrimaryButton(c, "Erase and install").Disabled(!matches()).Background(p.danger)
			if erase.Clicked() && matches() {
				a.startInstall()
			}
		})
	})
}

// keyGroups splits a recovery key into groups of n characters for reading.
func keyGroups(key string, n int) []string {
	var out []string
	for len(key) > n {
		out = append(out, key[:n])
		key = key[n:]
	}
	if key != "" {
		out = append(out, key)
	}
	return out
}

// recoveryKeyDialog shows a generated recovery key until the user says it
// is stored, then forgets it (progress protocol, recovery-key disclosure).
func (a *app) recoveryKeyDialog(c *ui.Context) {
	p := a.p
	open := a.inst.recoveryKey != ""
	if !open {
		return
	}
	ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(4, 10, 22, 0.6))
		panel.Width(540).Padding(26).Gap(16).Radius(18).Background(p.card).Border(1, p.cardBorder).
			Shadow(0, 24, 60, -12, ui.RGBA(0, 0, 0, 0.45)).Role(ui.RoleAlertDialog).Label("Recovery key")
		ui.Row(c).Gap(14).AlignItems(ui.Start).Children(func() {
			iconTile(c, p, iconKey, p.warning, p.warningSoft, 44)
			ui.Column(c).Gap(6).Grow(1).Shrink(1).Children(func() {
				ui.Text(c, "Save your recovery key").FontSize(17).FontWeight(700)
				ui.Text(c, "If the TPM ever cannot unlock the disk — after a firmware update or on another computer — this key is the only way in. Write it down or photograph it now; it will not be shown again.").
					FontSize(13.5).TextColor(p.muted)
			})
		})
		ui.Column(c).Gap(10).Padding(16).Radius(12).Background(p.inset).Border(1, p.cardBorder).Unselectable().Children(func() {
			ui.Grid(c).Columns(4).GapX(10).GapY(8).Children(func() {
				for _, group := range keyGroups(a.inst.recoveryKey, 8) {
					ui.Text(c, group).Font("monospace").FontSize(18).FontWeight(600).LetterSpacing(0.5).
						TextAlign(ui.Center).FontFeatures("tnum")
				}
			})
			ui.Text(c, "Grouped for reading: type it as one string, without spaces.").FontSize(12).TextColor(p.faint).TextAlign(ui.Center)
		})
		// No Copy button and no text selection: the clipboard outlives the
		// installer (mygo hands it to the clipboard manager on quit), and the
		// key must stay in-process (progress protocol, recovery-key
		// disclosure). The window cannot close until the key is acknowledged.
		ui.Row(c).Gap(10).Children(func() {
			ui.Checkbox(c, &a.keySaved, "I have written down or photographed this key")
			ui.Spacer(c)
			if ui.PrimaryButton(c, "Continue").Disabled(!a.keySaved).Clicked() && a.keySaved {
				a.inst.recoveryKey = ""
			}
		})
	})
}
