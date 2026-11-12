package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/frostyard/frostui"

	"github.com/frostyard/firn/internal/catalog"
	"github.com/frostyard/firn/internal/disk"
)

// --- welcome ---

func (a *app) welcomePage(c *ui.Context) {
	if c.Shortcut(0, ui.KeyEnter) {
		a.next()
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(48, 56, 40).Gap(frostui.SpaceXL).MaxWidth(860).AlignItems(ui.Stretch).Children(func() {
			frostui.Hero(c, frostui.HeroIcon{Logo: logo}, "Install snosi on this computer",
				"Answer a few questions, review every choice, and only then does anything touch your disk. Defaults are filled in for you; change whatever you like.")

			frostui.Card(c).Children(func() {
				cardHeader(c, iconChip, "This computer", "What the installer detected")
				ui.Row(c).Gap(frostui.SpaceS).Wrap().Children(func() {
					m := a.machine
					capability := func(icon, yes, no string, ok bool) {
						text, tone := no, frostui.Neutral
						if ok {
							text, tone = yes, frostui.Success
						}
						frostui.IconPill(c, icon, text, tone)
					}
					capability(iconChip, "UEFI firmware", "No UEFI firmware", m.uefi)
					capability(iconShield, "Secure Boot on", "Secure Boot off", m.env.SecureBoot)
					capability(iconKey, "TPM 2.0 present", "No TPM 2.0", m.env.TPM)
				})
				if !a.machine.uefi {
					callout(c, frostui.Danger, "This computer cannot run snosi", a.errs[fieldUEFI].msg)
				}
				for _, n := range a.notices {
					callout(c, frostui.Warning, "Image list", n)
				}
			})

			ui.Grid(c).Columns(3).Gap(frostui.SpaceM).Children(func() {
				steps := []struct {
					icon, title, body string
				}{
					{iconList, "Choose", "Pick an image, a disk, security and your account."},
					{iconEye, "Review", "See every choice and the exact recipe firn will run."},
					{iconDownload, "Install", "Confirm the disk by name; firn does the rest."},
				}
				for i, s := range steps {
					frostui.Card(c.Key(i)).Gap(10).Children(func() {
						ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
							frostui.IconTile(c, s.icon, frostui.Accent, 32)
							frostui.Label(c, fmt.Sprintf("%d", i+1))
						})
						frostui.Heading(c, s.title)
						frostui.Muted(c, s.body)
					})
				}
			})

			ui.Row(c).Gap(frostui.SpaceL).AlignItems(ui.Center).Children(func() {
				start := gradientButton(c, "Get started", iconArrowRight).Padding(10, 20).Disabled(!a.machine.uefi)
				if start.Clicked() {
					a.next()
				}
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					frostui.Caption(c, "or press")
					frostui.Shortcut(c, "Enter")
				})
				ui.Spacer(c)
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					frostui.Shortcut(c, "F1")
					frostui.Caption(c, "keyboard shortcuts")
				})
			})
		})
	})
}

// --- system image ---

func (a *app) imagePage(c *ui.Context) {
	sel := 0
	for i, e := range a.catalog {
		if e.Name == a.c.entry.Name && e.Ref == a.c.entry.Ref {
			sel = i
		}
	}
	ui.RadioGroup(c, func() {
		for i, e := range a.catalog {
			choiceCard(c.Key(e.Name), &sel, i, imageIcon(e), displayName(e.Name), e.Description, func() {
				ui.Row(c).Gap(6).Padding(4, 0, 0).AlignItems(ui.Center).Children(func() {
					tag(c, e.Ref, frostui.Neutral, true)
					if e.CosignPubKey != "" {
						frostui.Pill(c, "signed", frostui.Success)
					}
				})
			})
		}
	}).Gap(10).Label("System image")
	if sel != indexOf(a.catalog, a.c.entry) {
		a.c.setEntry(a.catalog[sel])
	}
	if msg := a.errText("image.ref"); msg != "" {
		callout(c, frostui.Danger, "This image cannot be installed", msg)
	}
	if msg := a.errText("image.cosign_pub_key"); msg != "" {
		callout(c, frostui.Danger, "The image's signing key is missing", msg)
	}

	frostui.Card(c).Children(func() {
		ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
			frostui.IconTile(c, iconSparkle, frostui.Neutral, 36).Shrink(0)
			ui.Column(c).Gap(2).Grow(1).Shrink(1).Children(func() {
				frostui.Heading(c, "Advanced options")
				frostui.Muted(c, "Update source and bootloader. Most people can leave these alone.")
			})
			ui.Switch(c, &a.c.advanced).Label("Advanced options")
		})
		if !a.c.advanced {
			return
		}
		ui.Field(c, "Update reference", func() {
			ui.TextInput(c, &a.c.targetRef).Placeholder(a.c.entry.Ref).Font("monospace")
		}).Description("Where the installed system looks for updates. Leave empty to follow " + a.c.entry.Ref + ".").
			Error(a.errText("image.target_ref"))
		desc := "GRUB 2 is available because Secure Boot is off."
		if a.machine.env.SecureBoot {
			desc = "Secure Boot is on, so systemd-boot is required: firn's key enrollment stages a signed systemd-boot chain."
			a.c.bootloader = "systemd"
		}
		ui.Field(c, "Bootloader", func() {
			ui.RadioGroup(c, func() {
				ui.Radio(c, &a.c.bootloader, "systemd", "systemd-boot (default)")
				ui.Radio(c, &a.c.bootloader, "grub2", "GRUB 2").Disabled(a.machine.env.SecureBoot)
			}).Row().Gap(20)
		}).Description(desc).Error(a.errText("target.bootloader"))
	})
}

func indexOf(entries []catalog.Entry, e catalog.Entry) int {
	for i, x := range entries {
		if x.Name == e.Name && x.Ref == e.Ref {
			return i
		}
	}
	return -1
}

func imageIcon(e catalog.Entry) string {
	d := strings.ToLower(e.Description)
	switch {
	case strings.Contains(d, "server") || strings.Contains(d, "headless"):
		return iconServer
	case strings.Contains(d, "surface") || strings.Contains(d, "tablet"):
		return iconTablet
	}
	return iconMonitor
}

// displayName capitalizes an image name for headings.
func displayName(name string) string {
	r, size := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError {
		return name
	}
	return string(unicode.ToUpper(r)) + name[size:]
}

// --- storage ---

func (a *app) storagePage(c *ui.Context) {
	frostui.Card(c).Children(func() {
		ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
			frostui.IconTile(c, iconDrive, frostui.Accent, 36).Shrink(0)
			ui.Column(c).Gap(2).Grow(1).Shrink(1).Children(func() {
				frostui.Heading(c, "Disk")
				frostui.Muted(c, "The whole disk is used. Partitions are created for you.")
			})
			rescan := frostui.LabeledButton(c, iconRefresh, "Rescan", false).Tooltip("Rescan disks (Ctrl+R)").Disabled(a.disksLoading)
			if rescan.Clicked() {
				a.rescan()
			}
		})
		switch {
		case a.disksLoading && len(a.disks) == 0:
			frostui.Loading(c, "Looking for disks…")
		case a.disksErr != "":
			callout(c, frostui.Danger, "Could not list disks", a.disksErr)
		case len(a.disks) == 0:
			callout(c, frostui.Accent, "No disks found", "Connect a disk, then rescan.")
		default:
			ui.RadioGroup(c, func() {
				for _, d := range a.disks {
					a.diskCard(c, d)
				}
			}).Gap(frostui.SpaceS).Label("Target disk")
		}
		if a.c.disk != "" {
			a.diskNotice = "" // the user chose again
		}
		if a.diskNotice != "" {
			callout(c, frostui.Warning, "Your disk choice was cleared", a.diskNotice)
		}
		if msg := a.errText(fieldDisk); msg != "" {
			frostui.Problem(c, msg)
		}
	})

	frostui.Card(c).Children(func() {
		cardHeader(c, iconFile, "Filesystem", "How the system partition is formatted.")
		ui.RadioGroup(c, func() {
			ui.Grid(c).Columns(3).Gap(frostui.SpaceS).Children(func() {
				a.fsCard(c, "btrfs", "Btrfs", "Checksums and snapshots. Recommended.")
				a.fsCard(c, "xfs", "XFS", "Fast and proven, great with large files.")
				a.fsCard(c, "ext4", "ext4", "The classic Linux filesystem.")
			})
		}).Label("Filesystem")
		if a.c.filesystem == "btrfs" {
			ui.Row(c).Gap(frostui.SpaceM).Padding(4, 2, 0).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Gap(2).Grow(1).Shrink(1).Children(func() {
					frostui.Strong(c, "Create subvolumes")
					frostui.Muted(c, "Top-level @, @home and @snapshots, for snapshot tools such as Snapper.")
				})
				ui.Switch(c, &a.c.btrfsSubvolumes).Label("Create subvolumes")
			})
		}
	})

	if d, ok := a.chosenDisk(); ok {
		callout(c, frostui.Warning, "Everything on this disk will be erased",
			fmt.Sprintf("%s (%s, %s) is wiped when you install. Make sure anything you need from it is backed up.", d.title(), d.Path, humanBytes(d.Size)))
	}
}

// diskIdentity is the hardware identity the TUI's disk picker shows beside
// vendor and model: the serial number and WWN, which tell apart two disks of
// the same model.
func diskIdentity(d diskInfo) string {
	var parts []string
	if s := strings.TrimSpace(d.Serial); s != "" {
		parts = append(parts, "serial "+s)
	}
	if w := strings.TrimSpace(d.WWN); w != "" {
		parts = append(parts, "wwn "+w)
	}
	return strings.Join(parts, "  ·  ")
}

func (a *app) chosenDisk() (diskInfo, bool) {
	for _, d := range a.disks {
		if d.Path == a.c.disk {
			return d, true
		}
	}
	return diskInfo{}, false
}

func (a *app) diskCard(c *ui.Context, d diskInfo) {
	t := a.t
	icon := iconDrive
	if d.Transport == "usb" {
		icon = iconUSB
	}
	parts := []string{d.Path, humanBytes(d.Size)}
	if d.Transport != "" {
		parts = append(parts, strings.ToUpper(d.Transport))
	}
	body := strings.Join(parts, "  ·  ")
	card := choiceCard(c.Key(d.Path), &a.c.disk, d.Path, icon, d.title(), body, func() {
		if ids := diskIdentity(d); ids != "" {
			frostui.Mono(c, ids).FontSize(frostui.TextSize(c, 11.5)).TextColor(t.TextMuted)
		}
		labels := disk.Labels(d.Device)
		if len(labels) == 0 && d.refused == "" {
			frostui.Caption(c, "No labeled partitions")
		}
		if len(labels) > 0 {
			ui.Row(c).Gap(6).Wrap().Padding(2, 0, 0).Children(func() {
				for _, l := range labels {
					tag(c, l, frostui.Neutral, false)
				}
			})
		}
		if d.refused != "" {
			ui.Row(c).Gap(6).Padding(2, 0, 0).AlignItems(ui.Center).Children(func() {
				frostui.Icon(c, iconWarning).FontSize(frostui.TextSize(c, 13)).TextColor(t.Warning)
				frostui.Toned(c, "Can't use: "+d.refused, frostui.Warning)
			})
		}
	})
	if d.refused != "" {
		card.Disabled(true).Opacity(0.72)
	}
}

func (a *app) fsCard(c *ui.Context, value, title, body string) {
	t := a.t
	k := frostui.ColorsOf(t)
	chosen := a.c.filesystem == value
	b := ui.RadioBase(c.Key(value), &a.c.filesystem, value).Padding(12, 14).Gap(6).Radius(frostui.RadiusCard).Column().AlignItems(ui.Stretch).
		Background(k.Panel).Border(1, t.Border).Label(title).Cursor(ui.CursorPointer)
	switch {
	case chosen:
		b.Background(k.SelectedFill.Over(k.Panel)).Border(1, t.Accent)
	case b.Hovered():
		b.Border(1, frostui.ToneLine(t, frostui.Accent))
	}
	b.Children(func() {
		ui.Row(c).Gap(frostui.SpaceS).AlignItems(ui.Center).Children(func() {
			name := frostui.Heading(c, title).Grow(1)
			if chosen {
				name.TextColor(k.SelectedText)
			}
			radioMark(c, chosen)
		})
		frostui.Muted(c, body)
	})
}

// --- security ---

func (a *app) securityPage(c *ui.Context) {
	t := a.t
	m := a.machine
	frostui.Card(c).Children(func() {
		cardHeader(c, iconLock, "Disk encryption", "Choose explicitly; the installer starts at no encryption.")
		noTPM := "Needs a TPM 2.0 chip, and none was found."
		ui.RadioGroup(c, func() {
			choiceCard(c.Key("none"), &a.c.encryption, "none", iconUnlock, "No encryption",
				"Simplest. Anyone with physical access to the disk can read it.", nil)
			choiceCard(c.Key("luks"), &a.c.encryption, "luks-passphrase", iconLock, "Passphrase",
				"LUKS2 encryption. Type the passphrase each time the computer starts.", nil)
			tpm := choiceCard(c.Key("tpm"), &a.c.encryption, "tpm2-luks", iconChip, "Automatic unlock with the TPM",
				"This computer's TPM unlocks the disk at boot. You get a recovery key to store safely.", func() {
					if !m.env.TPM {
						frostui.Toned(c, noTPM, frostui.Warning)
					}
				})
			both := choiceCard(c.Key("tpm-pass"), &a.c.encryption, "tpm2-luks-passphrase", iconShield, "TPM with a passphrase fallback",
				"The TPM unlocks automatically; the passphrase opens the disk if it cannot.", func() {
					if !m.env.TPM {
						frostui.Toned(c, noTPM, frostui.Warning)
					}
				})
			if !m.env.TPM {
				tpm.Disabled(true).Opacity(0.6)
				both.Disabled(true).Opacity(0.6)
			}
		}).Gap(frostui.SpaceS).Label("Disk encryption")
		if msg := a.errText("security.encryption"); msg != "" {
			frostui.Problem(c, msg)
		}
		if needsPassphrase(a.c.encryption) {
			frostui.RowDivider(c).Margin(4, 0)
			a.secretPair(c, "Encryption passphrase", &a.c.passphrase, &a.c.passphraseConfirm,
				fieldPassphrase, fieldPassphraseConfirm, "You will type this at every boot (or when the TPM cannot unlock). It cannot be recovered if lost.")
		}
	})

	frostui.Card(c).Children(func() {
		cardHeader(c, iconShield, "Secure Boot", "")
		if !m.env.SecureBoot {
			frostui.Body(c, "Secure Boot is off on this computer, so no key needs enrolling. You can turn Secure Boot on later from the firmware settings.").
				TextColor(t.TextMuted)
			return
		}
		frostui.Body(c, "Secure Boot is on. snosi's bootloader is signed with snosi's Machine Owner Key (MOK), which the firmware must trust.").
			TextColor(t.TextMuted)
		ui.RadioGroup(c, func() {
			choiceCard(c.Key("enroll"), &a.c.mok, "enroll", iconKey, "Enroll snosi's key",
				"At the next boot, MokManager asks for a one-time password to trust the key. Recommended.", nil)
			choiceCard(c.Key("skip"), &a.c.mok, "skip", iconSparkle, "Skip enrollment",
				"For people who manage Secure Boot keys themselves.", nil)
		}).Gap(frostui.SpaceS).Label("MOK enrollment")
		if a.c.mok == "enroll" {
			frostui.RowDivider(c).Margin(4, 0)
			a.secretPair(c, "One-time MOK password", &a.c.mokPassword, &a.c.mokConfirm,
				fieldMokPassword, fieldMokConfirm, "Choose something simple: you type it once in MokManager at the next boot, with a US keyboard layout.")
		}
	})
}

// secretPair asks for a secret twice, with a show/hide toggle and a
// strength meter.
func (a *app) secretPair(c *ui.Context, label string, secret, confirm *string, field, confirmField, help string) {
	ui.Grid(c).Columns(2).GapX(14).GapY(4).Children(func() {
		ui.Field(c, label, func() {
			in := ui.TextInput(c, secret).Label(label)
			if !a.showSecret {
				in.Password()
			}
		}).Error(a.errText(field))
		ui.Field(c, "Confirm", func() {
			in := ui.TextInput(c, confirm).Label("Confirm " + strings.ToLower(label))
			if !a.showSecret {
				in.Password()
			}
		}).Error(a.errText(confirmField))
	})
	strengthMeter(c, *secret)
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		frostui.Muted(c, help).Grow(1).Shrink(1)
		eye, word := iconEye, "Show"
		if a.showSecret {
			eye, word = iconEyeOff, "Hide"
		}
		if frostui.LabeledButton(c, eye, word, false).Label(word + " passwords").Clicked() {
			a.showSecret = !a.showSecret
		}
	})
}

// --- region ---

func (a *app) regionPage(c *ui.Context) {
	frostui.Card(c).Children(func() {
		cardHeader(c, iconMonitor, "Computer name", "")
		hostname := a.c.hostname
		ui.Field(c, "Hostname", func() {
			ui.TextInput(c, &a.c.hostname).Placeholder("snow")
		}).Description("How this computer appears on the network: letters, digits and hyphens.").Error(a.errText("system.hostname"))
		if a.c.hostname != hostname {
			a.c.hostnameAuto = false
		}
	})

	frostui.Card(c).Children(func() {
		cardHeader(c, iconGlobe, "Language and region", "Leave a field empty to keep the image's default.")
		ui.Field(c, "Language and formats", func() {
			ui.Autocomplete(c, &a.c.locale, a.lists.locales).Placeholder("Image default")
		}).Description("A glibc locale such as en_US.UTF-8.").Error(a.errText("system.locale"))
		ui.Field(c, "Time zone", func() {
			ui.Autocomplete(c, &a.c.timezone, a.lists.timezones).Placeholder("Image default")
		}).Description(a.zoneDescription()).Error(a.errText("system.timezone"))
		ui.Field(c, "Keyboard layout", func() {
			ui.Autocomplete(c, &a.c.keyboard, a.lists.keyboards).Placeholder("Image default")
		}).Description(a.keyboardDescription()).Error(a.errText("system.keyboard"))
	})

	frostui.Card(c).Children(func() {
		ui.Collapsible(c, "Remote access for root (SSH)", &a.advancedSSH, func() {
			ui.Field(c, "Authorized SSH keys", func() {
				ui.TextArea(c, &a.c.rootSSHKey).Lines(3, 6).Font("monospace").Placeholder("ssh-ed25519 AAAA… you@laptop")
			}).Description("Paste one or more OpenSSH public keys, one per line, to allow root to sign in over SSH.").
				Error(a.errText("system.root_ssh_authorized_key"))
		})
	})
}

func (a *app) zoneDescription() string {
	tz := strings.TrimSpace(a.c.timezone)
	if tz == "" {
		return "An IANA zone such as America/Chicago."
	}
	if loc, err := loadZone(a.machine.env.ZoneinfoDir, tz); err == nil {
		return "It is " + time.Now().In(loc).Format("15:04 on Monday") + " there."
	}
	return "An IANA zone such as America/Chicago."
}

func loadZone(dir, name string) (*time.Location, error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	return time.LoadLocationFromTZData(name, data)
}

func (a *app) keyboardDescription() string {
	kb := strings.TrimSpace(a.c.keyboard)
	if name := a.lists.keyboardNames[kb]; name != "" {
		return name + "."
	}
	return "An XKB layout, optionally with a variant: us, or de:nodeadkeys."
}

// --- account ---

func (a *app) accountPage(c *ui.Context) {
	frostui.Card(c).Children(func() {
		ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
			frostui.IconTile(c, iconUser, frostui.Accent, 36).Shrink(0)
			ui.Column(c).Gap(2).Grow(1).Shrink(1).Children(func() {
				frostui.Heading(c, "User account")
				frostui.Muted(c, "Recommended. You can add more accounts after installing.")
			})
			ui.Switch(c, &a.c.createUser).Label("Create a user account")
		})
		if !a.c.createUser {
			callout(c, frostui.Warning, "No user account", "Only root can sign in, and only over SSH with a key added on the Region step.")
			return
		}
		ui.Grid(c).Columns(2).GapX(14).GapY(12).Children(func() {
			fullname := a.c.fullname
			ui.Field(c, "Full name", func() {
				ui.TextInput(c, &fullname).Placeholder("Ada Lovelace").AutoFocus()
			}).Description("Optional; shown on the sign-in screen.").Error(a.errText("system.user.fullname"))
			if fullname != a.c.fullname {
				a.c.setFullname(fullname)
			}
			username := a.c.username
			ui.Field(c, "Username", func() {
				ui.TextInput(c, &a.c.username).Placeholder("ada")
			}).Description("Lowercase letters, digits, - and _.").Error(a.errText("system.user.name"))
			if a.c.username != username {
				a.c.usernameAuto = false
			}
		})
		frostui.RowDivider(c).Margin(2, 0)
		a.secretPair(c, "Password", &a.c.password, &a.c.passwordConfirm, fieldPassword, fieldPasswordConfirm,
			"Used to sign in and for administrator tasks with sudo.")
	})

	if !a.c.createUser {
		return
	}
	frostui.Card(c).Children(func() {
		cardHeader(c, iconShield, "Groups", "Preselected for "+displayName(a.c.entry.Name)+". Groups the image lacks are skipped at install.")
		ui.Grid(c).Columns(2).GapX(16).GapY(10).Children(func() {
			for i := range a.c.groups {
				g := &a.c.groups[i]
				ui.Row(c.Key(g.Name)).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Checkbox(c, &g.on, g.Name)
					if g.Description != "" {
						frostui.Muted(c, "— "+g.Description).SingleLine().Shrink(1)
					}
				})
			}
		})
		ui.Field(c, "Additional groups", func() {
			ui.TextInput(c, &a.c.extraGroups).Placeholder("wheel, games")
		}).Description("Comma- or space-separated.").Error(a.errText("system.user.groups"))
	})

	frostui.Card(c).Children(func() {
		who := a.c.username
		if who == "" {
			who = "this user"
		}
		ui.Collapsible(c, "Remote access for "+who+" (SSH)", &a.userSSH, func() {
			ui.Field(c, "Authorized SSH keys", func() {
				ui.TextArea(c, &a.c.userSSHKey).Lines(3, 6).Font("monospace").Placeholder("ssh-ed25519 AAAA… you@laptop")
			}).Description("Paste one or more OpenSSH public keys, one per line.").Error(a.errText("system.user.ssh_authorized_key"))
		})
	})
}

// --- apps ---

func (a *app) appsPage(c *ui.Context) {
	name := displayName(a.c.entry.Name)
	frostui.Card(c).Children(func() {
		cardHeader(c, iconPackage, name+"'s core apps", "The everyday apps this image is designed around, as Flatpaks.")
		if a.preview.ref != a.c.entry.Ref {
			frostui.Loading(c, "Reading "+name+"'s app list…")
			return
		}
		toggle := func(title string) {
			ui.Row(c).Gap(frostui.SpaceM).AlignItems(ui.Center).Children(func() {
				frostui.Strong(c, title).Grow(1)
				ui.Switch(c, &a.c.coreFlatpaks).Label(title)
			})
		}
		switch a.preview.state {
		case corePreviewAvailable:
			toggle(fmt.Sprintf("Install %d core apps", len(a.preview.apps)))
			ui.Row(c).Gap(6).Wrap().Opacity(map[bool]float32{true: 1, false: 0.5}[a.c.coreFlatpaks]).Children(func() {
				for _, app := range a.preview.apps {
					tag(c, app.Name, frostui.Neutral, false)
				}
			})
		case corePreviewNone:
			callout(c, frostui.Accent, "No core app set", name+" publishes no core apps.")
		case corePreviewInvalid:
			callout(c, frostui.Warning, "Core app set unavailable", "This image's app list is malformed, so it cannot be installed. "+a.preview.detail)
		default:
			toggle("Install the core apps")
			detail := "The list could not be read right now; if this stays on, it is read again during the install."
			if a.preview.detail != "" {
				detail += " (" + a.preview.detail + ")"
			}
			frostui.Muted(c, detail)
		}
	})

	frostui.Card(c).Children(func() {
		cardHeader(c, iconDownload, "More apps", "Any Flatpak from Flathub, by application ID.")
		ui.Field(c, "Application IDs", func() {
			ui.TextArea(c, &a.c.flatpaksRaw).Lines(3, 8).Font("monospace").Placeholder("org.mozilla.firefox\nio.github.celluloid_player.Celluloid")
		}).Description("Separate IDs with spaces, commas or new lines.").Error(a.errText("system.flatpaks"))
		if ids := splitList(a.c.flatpaksRaw); len(ids) > 0 {
			ui.Row(c).Gap(6).Wrap().Children(func() {
				for _, id := range ids {
					tag(c, id, frostui.Accent, true)
				}
			})
		}
	})
}

// --- review ---

func (a *app) reviewPage(c *ui.Context) {
	if a.installErr != "" {
		callout(c, frostui.Danger, "Could not start the install", a.installErr)
	}
	if errs := a.errs.onPage(pageReview); len(errs) > 0 {
		var msgs []string
		for field, e := range errs {
			msgs = append(msgs, field+": "+e.msg)
		}
		callout(c, frostui.Danger, "The recipe has problems (a wizard bug — please report it)", strings.Join(msgs, "\n"))
	}
	if d, ok := a.chosenDisk(); ok {
		callout(c, frostui.Danger, "Installing erases "+d.title(),
			fmt.Sprintf("All data on %s (%s) will be destroyed. You will confirm by typing the disk's path.", d.Path, humanBytes(d.Size)))
	}

	ui.Grid(c).Columns(2).Gap(frostui.SpaceM).Children(func() {
		e := a.c.entry
		a.summary(c, pageImage, []frostui.KeyValue{
			{Key: "Image", Value: displayName(e.Name)},
			{Key: "Source", Value: e.Ref, Mono: true},
			{Key: "Updates from", Value: a.updateRef(), Mono: a.updateRef() != sameAsSource},
			{Key: "Bootloader", Value: a.bootloaderName()},
		})
		storage := []frostui.KeyValue{{Key: "Disk", Value: a.c.disk, Mono: true}}
		if d, ok := a.chosenDisk(); ok {
			storage = []frostui.KeyValue{{Key: "Disk", Value: d.title()}, {Key: "Device", Value: d.Path + " · " + humanBytes(d.Size)}}
			if ids := diskIdentity(d); ids != "" {
				storage = append(storage, frostui.KeyValue{Key: "Identity", Value: ids, Mono: true})
			}
		}
		fs := a.c.filesystem
		if fs == "btrfs" && a.c.btrfsSubvolumes {
			fs += " with subvolumes"
		}
		a.summary(c, pageStorage, append(storage, frostui.KeyValue{Key: "Filesystem", Value: fs}))
		sec := []frostui.KeyValue{{Key: "Encryption", Value: encryptionName(a.c.encryption)}}
		if a.machine.env.SecureBoot {
			sec = append(sec, frostui.KeyValue{Key: "Secure Boot key", Value: map[string]string{"enroll": "Enroll at next boot", "skip": "Not enrolled"}[a.c.mok]})
		} else {
			sec = append(sec, frostui.KeyValue{Key: "Secure Boot", Value: "Off"})
		}
		a.summary(c, pageSecurity, sec)
		a.summary(c, pageRegion, []frostui.KeyValue{
			{Key: "Hostname", Value: a.c.hostname},
			{Key: "Language", Value: orDefault(a.c.locale)},
			{Key: "Time zone", Value: orDefault(a.c.timezone)},
			{Key: "Keyboard", Value: orDefault(a.c.keyboard)},
			{Key: "Root SSH", Value: keyCount(a.c.rootSSHKey)},
		})
		acct := []frostui.KeyValue{{Key: "User", Value: "None"}}
		if a.c.createUser {
			who := a.c.username
			if a.c.fullname != "" {
				who = a.c.fullname + " (" + a.c.username + ")"
			}
			acct = []frostui.KeyValue{{Key: "User", Value: who}, {Key: "Groups", Value: strings.Join(a.c.selectedGroups(), ", ")}, {Key: "SSH", Value: keyCount(a.c.userSSHKey)}}
		}
		a.summary(c, pageAccount, acct)
		core := "Off"
		switch {
		case a.preview.state == corePreviewNone || a.preview.state == corePreviewInvalid:
			core = "None offered"
		case a.c.coreFlatpaks && a.preview.state == corePreviewAvailable:
			core = fmt.Sprintf("%d apps", len(a.preview.apps))
		case a.c.coreFlatpaks:
			core = "On (read at install)"
		}
		extra := "None"
		if ids := splitList(a.c.flatpaksRaw); len(ids) > 0 {
			extra = strings.Join(ids, ", ")
		}
		a.summary(c, pageApps, []frostui.KeyValue{{Key: "Core apps", Value: core}, {Key: "More apps", Value: extra}})
	})

	if a.reviewed != nil {
		frostui.Card(c).Children(func() {
			ui.Collapsible(c, "Show the recipe", &a.recipeOpen, func() {
				frostui.Muted(c, "This exact TOML is saved and installed. Secrets are referenced as private files under "+a.session+", never written inline.")
				codeBlock(c, strings.TrimRight(string(a.reviewed), "\n"))
			})
		})
	}
}

// summary is one review card: a page's answers with a way back to it.
func (a *app) summary(c *ui.Context, pg page, rows []frostui.KeyValue) {
	frostui.Card(c.Key(pg)).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			frostui.IconTile(c, pageMeta[pg].icon, frostui.Accent, 28).Shrink(0)
			frostui.Heading(c, pageMeta[pg].nav).Grow(1)
			edit := frostui.LabeledButton(c, iconPencil, "Edit", false).Label("Edit " + pageMeta[pg].nav).
				Tooltip(fmt.Sprintf("Edit (Alt+%d)", int(pg)+1))
			if edit.Clicked() {
				a.jump(pg)
			}
		})
		frostui.KeyValues(c, rows, frostui.KeyValuesStyle{KeyWidth: 104})
	})
}

// sameAsSource is what the review says when updates come from the image's
// own reference.
const sameAsSource = "Same as source"

func (a *app) updateRef() string {
	if a.c.advanced && strings.TrimSpace(a.c.targetRef) != "" {
		return strings.TrimSpace(a.c.targetRef)
	}
	return sameAsSource
}

func (a *app) bootloaderName() string {
	if a.c.advanced && a.c.bootloader == "grub2" {
		return "GRUB 2"
	}
	return "systemd-boot"
}

func encryptionName(e string) string {
	switch e {
	case "none":
		return "None"
	case "luks-passphrase":
		return "Passphrase (LUKS2)"
	case "tpm2-luks":
		return "TPM automatic unlock"
	case "tpm2-luks-passphrase":
		return "TPM with passphrase fallback"
	}
	return e
}

func orDefault(v string) string {
	if strings.TrimSpace(v) == "" {
		return "Image default"
	}
	return strings.TrimSpace(v)
}

func keyCount(keys string) string {
	n := 0
	for _, line := range strings.Split(keys, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	switch n {
	case 0:
		return "No keys"
	case 1:
		return "1 key"
	}
	return fmt.Sprintf("%d keys", n)
}
