package installer

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/firn/internal/progress"
)

// installHeadline is the install page's title and subtitle for its phase.
func (a *app) installHeadline() (string, string) {
	name := displayName(a.c.entry.Name)
	switch a.inst.phase {
	case phaseDone:
		return name + " is installed", "Remove the installation media, then restart into your new system."
	case phaseFailed:
		if a.inst.stoppedByUser() {
			return "Installation stopped", "The install was stopped before it finished."
		}
		if a.inst.errCode == progress.CodeInstallInProgress {
			return "Installation not started", "Another install is running on this machine. No disk was touched."
		}
		return "Installation failed", "Nothing more will be written to the disk."
	}
	if a.inst.canceling {
		return "Stopping…", "Undoing mounts and closing the disk. This can take a moment."
	}
	return "Installing " + name, fmt.Sprintf("to %s with %s. This usually takes several minutes.", a.c.disk, a.c.filesystem)
}

func (a *app) installPage(c *ui.Context) {
	t := a.t
	s := &a.inst
	if s.phase == phaseRunning {
		c.After(time.Second) // keep the elapsed time ticking
	}
	switch s.phase {
	case phaseDone:
		a.outcome(c, frostui.Success)
	case phaseFailed:
		a.outcome(c, frostui.Danger)
	}

	frostui.Card(c).Children(func() {
		ui.Row(c).Gap(frostui.SpaceL).AlignItems(ui.End).Children(func() {
			ui.Column(c).Gap(4).Grow(1).Shrink(1).Children(func() {
				label := stepTitle(s.currentName())
				switch {
				case s.phase == phaseDone:
					label = "Complete"
				case s.phase == phaseFailed:
					label = failedStepLabel(s.errCode, s.failedStep)
				case s.current < 0:
					label = "Starting…"
				}
				frostui.Heading(c, label).FontSize(frostui.TextSize(c, 16))
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
				frostui.Muted(c, detail).FontFeatures("tnum")
			})
			size := frostui.TextSize(c, 32)
			ui.Text(c, fmt.Sprintf("%d%%", int(s.overall()*100+0.5))).FontSize(size).Bold().LetterSpacing(-0.05 * size).
				FontFeatures("tnum").TextColor(t.Accent)
		})
		bar := ui.Progress(c, s.overall()).Label("Overall progress")
		if s.phase == phaseFailed {
			bar.TextColor(t.Danger)
		}
	})

	ui.Grid(c).ColumnTracks(ui.Fr(1), ui.Fr(1.3)).Gap(frostui.SpaceM).AlignItems(ui.Start).Children(func() {
		frostui.Card(c).Gap(4).Children(func() {
			frostui.Heading(c, "Steps").Padding(0, 0, 8)
			for i, st := range s.steps {
				a.stepRow(c, i, stepTitle(st.Name))
			}
			if len(s.steps) == 0 {
				frostui.Loading(c, "Preparing…")
			}
		})
		frostui.Card(c).Children(func() {
			frostui.Heading(c, "Activity")
			ui.Column(c).Gap(6).Padding(12, 14).Radius(frostui.RadiusChip).Background(frostui.ColorsOf(t).Code).
				Border(1, t.Border).MinHeight(180).Selectable().Children(func() {
				lines := s.log
				if len(lines) > 12 {
					lines = lines[len(lines)-12:]
				}
				if len(lines) == 0 {
					frostui.Mono(c, "Waiting for the first step…").TextColor(t.TextMuted)
				}
				for _, l := range lines {
					line := frostui.Mono(c, l.text).LineHeight(1.4)
					if l.warning {
						line.TextColor(t.Warning)
						frostui.Mono(c, "["+l.code+"]").FontSize(frostui.TextSize(c, 11.5)).TextColor(t.TextMuted)
					}
				}
			})
		})
	})
}

func (a *app) stepRow(c *ui.Context, i int, title string) {
	t := a.t
	s := &a.inst
	done := i < s.current || s.phase == phaseDone
	active := i == s.current && s.phase == phaseRunning
	failed := s.phase == phaseFailed && i == s.current
	ui.Row(c.Key(i)).Gap(10).Padding(5, 4).AlignItems(ui.Center).Children(func() {
		box := ui.Row(c).Size(20, 20).Center().Shrink(0)
		switch {
		case failed:
			box.Children(func() { frostui.Icon(c, iconError).FontSize(18).TextColor(t.Danger) })
		case done:
			box.Radius(frostui.RadiusChip).Background(frostui.ToneFill(t, frostui.Success)).Children(func() {
				frostui.Icon(c, iconCheck).FontSize(13).TextColor(t.Success)
			})
		case active:
			box.Children(func() { ui.Spinner(c).Size(16, 16) })
		default:
			box.Children(func() { ui.Box(c).Size(6, 6).Radius(3).Background(frostui.ToneLine(t, frostui.Neutral)) })
		}
		label := frostui.Body(c, title).Grow(1).SingleLine()
		switch {
		case active:
			label.FontWeight(600)
		case failed:
			label.TextColor(t.Danger).FontWeight(600)
		case !done:
			label.TextColor(t.TextMuted)
		}
		if active && s.stepFraction > 0 {
			frostui.Caption(c, fmt.Sprintf("%d%%", int(s.stepFraction*100))).FontFeatures("tnum")
		}
	})
}

// outcome is the success or failure panel above the progress, in tone.
func (a *app) outcome(c *ui.Context, tone frostui.Tone) {
	t := a.t
	s := &a.inst
	color := frostui.ToneColor(t, tone)
	ui.Column(c).Padding(20).Gap(14).Radius(frostui.RadiusCard).AlignItems(ui.Stretch).
		Background(frostui.ToneFill(t, tone)).Border(1, frostui.ToneLine(t, tone)).Children(func() {
		ui.Row(c).Gap(14).AlignItems(ui.Start).Children(func() {
			frostui.Icon(c, toneIcon(tone)).FontSize(32).TextColor(color).Shrink(0)
			ui.Column(c).Gap(3).Grow(1).Shrink(1).Children(func() {
				headline := func(text string) ui.Element {
					return ui.Text(c, text).FontSize(frostui.TextSize(c, 18)).Bold()
				}
				switch {
				case tone == frostui.Success:
					headline("Your new system is ready")
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
					frostui.Body(c, next)
				case s.stoppedByUser():
					headline("The installation was stopped")
					frostui.Body(c, "The disk will not boot until you install again.")
				default:
					ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
						headline(failureTitle(s.errCode))
						if s.errCode != "" {
							tag(c, s.errCode, frostui.Danger, true)
						}
					})
					if hint := failureHint(s.errCode); hint != "" {
						frostui.Body(c, hint)
					}
					frostui.Body(c, s.errMessage).Selectable()
				}
			})
		})
		for _, item := range s.summary {
			callout(c, frostui.Warning, summaryTitle(item.Code), item.Detail)
		}
		if s.recipePath != "" {
			ui.Column(c).Gap(6).AlignItems(ui.Stretch).Children(func() {
				frostui.Muted(c, "Reproduce this install without the wizard:")
				codeBlock(c, reproduceCommand(a.c.disk, s.recipePath))
				if !a.machine.demo {
					frostui.Caption(c, "The recipe and its secret files remain until this environment restarts.")
				}
			})
		}
		if a.status != "" {
			frostui.Muted(c, a.status)
		}
	})
}

func (a *app) installFooter(c *ui.Context) {
	t := a.t
	s := &a.inst
	switch s.phase {
	case phaseRunning:
		ui.Row(c).Gap(frostui.SpaceS).Grow(1).Shrink(1).AlignItems(ui.Center).Children(func() {
			frostui.Icon(c, iconPower).TextColor(t.TextMuted)
			frostui.Muted(c, "Keep the computer plugged in and awake until the install finishes.").SingleLine()
		})
		stop := ghostButton(c, "Stop installation").Disabled(s.canceling)
		if stop.Clicked() {
			a.askCancel = true
		}
	case phaseDone:
		ui.Spacer(c)
		if ghostButton(c, "Close").Clicked() {
			a.quit()
		}
		if frostui.LabeledButton(c, iconPower, "Restart now", true).Clicked() {
			a.reboot()
		}
	case phaseFailed:
		ui.Spacer(c)
		if ghostButton(c, "Close").Clicked() {
			a.quit()
		}
		if frostui.LabeledButton(c, iconArrowLeft, "Back to review", true).Clicked() {
			a.retry()
		}
	}
}

// confirmDialog is the typed confirmation: the user must type the exact
// disk path, the rule headless installs enforce with --confirm.
func (a *app) confirmDialog(c *ui.Context) {
	t := a.t
	d, _ := a.chosenDisk()
	matches := func() bool { return a.confirmText == a.c.disk }
	ui.DialogBase(c, &a.confirmOpen, func(backdrop, panel ui.Element) {
		dialogPanel(c, backdrop, panel, 500, "Confirm install")
		ui.Row(c).Gap(14).AlignItems(ui.Start).Children(func() {
			frostui.IconTile(c, iconWarning, frostui.Danger, 44).Shrink(0)
			ui.Column(c).Gap(6).Grow(1).Shrink(1).Children(func() {
				ui.Text(c, "Erase "+d.title()+" and install "+displayName(a.c.entry.Name)+"?").FontSize(frostui.TextSize(c, 17)).Bold()
				frostui.Body(c, fmt.Sprintf("Everything on %s (%s) will be permanently destroyed. This cannot be undone.", a.c.disk, humanBytes(d.Size))).
					TextColor(t.TextMuted)
			})
		})
		if ids := diskIdentity(d); ids != "" {
			frostui.Mono(c, ids).TextColor(t.TextMuted).Padding(10, 12).Radius(frostui.RadiusChip).
				Background(frostui.ColorsOf(t).Code).Border(1, t.Border).Selectable()
		}
		ui.Field(c, "Type "+a.c.disk+" to confirm", func() {
			in := ui.TextInput(c, &a.confirmText).Placeholder(a.c.disk).Font("monospace").AutoFocus()
			if in.Submitted() && matches() {
				a.startInstall()
			}
		})
		ui.Row(c).Gap(frostui.SpaceS).Justify(ui.End).Children(func() {
			if ghostButton(c, "Cancel").Clicked() {
				a.confirmOpen = false
			}
			erase := dangerButton(c, "Erase and install", "").Disabled(!matches())
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
	t := a.t
	open := a.inst.recoveryKey != ""
	if !open {
		return
	}
	ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
		dialogPanel(c, backdrop, panel, 540, "Recovery key")
		ui.Row(c).Gap(14).AlignItems(ui.Start).Children(func() {
			frostui.IconTile(c, iconKey, frostui.Warning, 44).Shrink(0)
			ui.Column(c).Gap(6).Grow(1).Shrink(1).Children(func() {
				ui.Text(c, "Save your recovery key").FontSize(frostui.TextSize(c, 17)).Bold()
				frostui.Body(c, "If the TPM ever cannot unlock the disk — after a firmware update or on another computer — this key is the only way in. Write it down or photograph it now; it will not be shown again.").
					TextColor(t.TextMuted)
			})
		})
		ui.Column(c).Gap(10).Padding(16).Radius(frostui.RadiusChip).Background(frostui.ColorsOf(t).Code).
			Border(1, t.Border).Unselectable().Children(func() {
			ui.Grid(c).Columns(4).GapX(10).GapY(8).Children(func() {
				for _, group := range keyGroups(a.inst.recoveryKey, 8) {
					ui.Text(c, group).Font("monospace").FontSize(frostui.TextSize(c, 18)).FontWeight(600).LetterSpacing(0.5).
						TextAlign(ui.Center).FontFeatures("tnum")
				}
			})
			frostui.Caption(c, "Grouped for reading: type it as one string, without spaces.").TextAlign(ui.Center)
		})
		// No Copy button and no text selection: the clipboard outlives the
		// installer (mygo hands it to the clipboard manager on quit), and the
		// key must stay in-process (progress protocol, recovery-key
		// disclosure). The window cannot close until the key is acknowledged.
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c, &a.keySaved, "I have written down or photographed this key")
			ui.Spacer(c)
			if gradientButton(c, "Continue", iconArrowRight).Disabled(!a.keySaved).Clicked() && a.keySaved {
				a.inst.recoveryKey = ""
			}
		})
	})
}
