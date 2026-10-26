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

	"github.com/frostyard/firn/internal/catalog"
	"github.com/frostyard/firn/internal/disk"
)

// --- welcome ---

func (a *app) welcomePage(c *ui.Context) {
	p := a.p
	if c.Shortcut(0, ui.KeyEnter) {
		a.next()
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(64, 56, 40).Gap(28).MaxWidth(860).Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				ui.Text(c, "SNOSI INSTALLER").FontSize(12).FontWeight(700).LetterSpacing(1.6).TextColor(p.accent)
				ui.Text(c, "Install snosi on this computer").FontSize(38).FontWeight(800).LetterSpacing(-0.6)
				ui.Text(c, "Answer a few questions, review every choice, and only then does anything touch your disk. Defaults are filled in for you; change whatever you like.").
					FontSize(16).LineHeight(1.45).TextColor(p.muted).MaxWidth(640)
			})

			card(c, p).Children(func() {
				cardHeader(c, p, iconChip, "This computer", "What the installer detected")
				ui.Row(c).Gap(8).Wrap().Children(func() {
					m := a.machine
					statusChip(c, p, iconChip, map[bool]string{true: "UEFI firmware", false: "No UEFI firmware"}[m.uefi], m.uefi)
					statusChip(c, p, iconShield, map[bool]string{true: "Secure Boot on", false: "Secure Boot off"}[m.env.SecureBoot], m.env.SecureBoot)
					statusChip(c, p, iconKey, map[bool]string{true: "TPM 2.0 present", false: "No TPM 2.0"}[m.env.TPM], m.env.TPM)
				})
				if !a.machine.uefi {
					callout(c, p, calloutDanger, "This computer cannot run snosi", a.errs[fieldUEFI].msg)
				}
				for _, n := range a.notices {
					callout(c, p, calloutWarning, "Image list", n)
				}
			})

			ui.Grid(c).Columns(3).Gap(14).Children(func() {
				steps := []struct {
					ic          *ui.SVG
					title, body string
				}{
					{iconList, "Choose", "Pick an image, a disk, security and your account."},
					{iconEye, "Review", "See every choice and the exact recipe firn will run."},
					{iconDownload, "Install", "Confirm the disk by name; firn does the rest."},
				}
				for i, s := range steps {
					ui.Column(c).Gap(10).Padding(18).Radius(14).Background(p.card).Border(1, p.cardBorder).Children(func() {
						ui.Row(c).Gap(10).Children(func() {
							iconTile(c, p, s.ic, p.accent, p.accentSoft, 32)
							ui.Text(c, fmt.Sprintf("%d", i+1)).FontSize(13).FontWeight(700).TextColor(p.faint)
						})
						ui.Text(c, s.title).FontSize(15).FontWeight(600)
						ui.Text(c, s.body).FontSize(13).TextColor(p.muted)
					})
				}
			})

			ui.Row(c).Gap(16).Children(func() {
				start := ui.PrimaryButton(c, "").Label("Get started").Padding(12, 22).Disabled(!a.machine.uefi).Children(func() {
					ui.Text(c, "Get started").FontSize(15).FontWeight(600).SingleLine()
					ui.Icon(c, iconArrowRight).FontSize(16)
				})
				if start.Clicked() {
					a.next()
				}
				ui.Row(c).Gap(6).Children(func() {
					ui.Text(c, "or press").FontSize(13).TextColor(p.faint)
					keys(c, p, "Enter")
				})
				ui.Spacer(c)
				ui.Row(c).Gap(6).Children(func() {
					keys(c, p, "F1")
					ui.Text(c, "keyboard shortcuts").FontSize(13).TextColor(p.faint)
				})
			})
		})
	})
}

// --- system image ---

func (a *app) imagePage(c *ui.Context) {
	p := a.p
	sel := 0
	for i, e := range a.catalog {
		if e.Name == a.c.entry.Name && e.Ref == a.c.entry.Ref {
			sel = i
		}
	}
	ui.RadioGroup(c, func() {
		for i, e := range a.catalog {
			choiceCard(c.Key(e.Name), p, &sel, i, imageIcon(e), displayName(e.Name), e.Description, func() {
				ui.Row(c).Gap(6).Padding(4, 0, 0).Children(func() {
					chip(c, e.Ref, p.muted, p.card).Border(1, p.cardBorder).Font("monospace")
					if e.CosignPubKey != "" {
						chip(c, "signed", p.success, p.successSoft)
					}
				})
			})
		}
	}).Gap(12).Label("System image")
	if sel != indexOf(a.catalog, a.c.entry) {
		a.c.setEntry(a.catalog[sel])
	}
	if msg := a.errText("image.ref"); msg != "" {
		callout(c, p, calloutDanger, "This image cannot be installed", msg)
	}
	if msg := a.errText("image.cosign_pub_key"); msg != "" {
		callout(c, p, calloutDanger, "The image's signing key is missing", msg)
	}

	card(c, p).Children(func() {
		ui.Row(c).Gap(12).Children(func() {
			iconTile(c, p, iconSparkle, p.muted, p.subtle, 36)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, "Advanced options").FontSize(15).FontWeight(600)
				ui.Text(c, "Update source and bootloader. Most people can leave these alone.").FontSize(13).TextColor(p.muted)
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

func imageIcon(e catalog.Entry) *ui.SVG {
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
	p := a.p
	card(c, p).Children(func() {
		ui.Row(c).Gap(12).Children(func() {
			iconTile(c, p, iconDrive, p.accent, p.accentSoft, 36)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, "Disk").FontSize(15).FontWeight(600)
				ui.Text(c, "The whole disk is used. Partitions are created for you.").FontSize(13).TextColor(p.muted)
			})
			rescan := ui.Button(c, "").Label("Rescan").Tooltip("Rescan disks (Ctrl+R)").Disabled(a.disksLoading).Children(func() {
				ui.Icon(c, iconRefresh).FontSize(14)
				ui.Text(c, "Rescan").SingleLine()
			})
			if rescan.Clicked() {
				a.rescan()
			}
		})
		switch {
		case a.disksLoading && len(a.disks) == 0:
			ui.Row(c).Gap(10).Padding(12, 0).Children(func() {
				ui.Spinner(c).Label("Looking for disks")
				ui.Text(c, "Looking for disks…").TextColor(p.muted)
			})
		case a.disksErr != "":
			callout(c, p, calloutDanger, "Could not list disks", a.disksErr)
		case len(a.disks) == 0:
			callout(c, p, calloutInfo, "No disks found", "Connect a disk, then rescan.")
		default:
			ui.RadioGroup(c, func() {
				for _, d := range a.disks {
					a.diskCard(c, d)
				}
			}).Gap(10).Label("Target disk")
		}
		if msg := a.errText(fieldDisk); msg != "" {
			ui.Text(c, msg).FontSize(13).TextColor(p.danger)
		}
	})

	card(c, p).Children(func() {
		cardHeader(c, p, iconFile, "Filesystem", "How the system partition is formatted.")
		ui.RadioGroup(c, func() {
			ui.Grid(c).Columns(3).Gap(10).Children(func() {
				a.fsCard(c, "btrfs", "Btrfs", "Checksums and snapshots. Recommended.")
				a.fsCard(c, "xfs", "XFS", "Fast and proven, great with large files.")
				a.fsCard(c, "ext4", "ext4", "The classic Linux filesystem.")
			})
		}).Label("Filesystem")
		if a.c.filesystem == "btrfs" {
			ui.Row(c).Gap(12).Padding(4, 2, 0).Children(func() {
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					ui.Text(c, "Create subvolumes").FontWeight(600)
					ui.Text(c, "Top-level @, @home and @snapshots, for snapshot tools such as Snapper.").FontSize(13).TextColor(p.muted)
				})
				ui.Switch(c, &a.c.btrfsSubvolumes).Label("Create subvolumes")
			})
		}
	})

	if d, ok := a.chosenDisk(); ok {
		callout(c, p, calloutWarning, "Everything on this disk will be erased",
			fmt.Sprintf("%s (%s, %s) is wiped when you install. Make sure anything you need from it is backed up.", d.title(), d.Path, humanBytes(d.Size)))
	}
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
	p := a.p
	ic := iconDrive
	if d.Transport == "usb" {
		ic = iconUSB
	}
	parts := []string{d.Path, humanBytes(d.Size)}
	if d.Transport != "" {
		parts = append(parts, strings.ToUpper(d.Transport))
	}
	body := strings.Join(parts, "  ·  ")
	card := choiceCard(c.Key(d.Path), p, &a.c.disk, d.Path, ic, d.title(), body, func() {
		labels := disk.Labels(d.Device)
		if len(labels) == 0 && d.refused == "" {
			ui.Text(c, "No labeled partitions").FontSize(12).TextColor(p.faint)
		}
		if len(labels) > 0 {
			ui.Row(c).Gap(6).Wrap().Padding(2, 0, 0).Children(func() {
				for _, l := range labels {
					chip(c, l, p.muted, p.card).Border(1, p.cardBorder)
				}
			})
		}
		if d.refused != "" {
			ui.Row(c).Gap(6).Padding(2, 0, 0).Children(func() {
				ui.Icon(c, iconWarning).FontSize(13).TextColor(p.warning)
				ui.Text(c, "Can't use: "+d.refused).FontSize(12.5).TextColor(p.warning)
			})
		}
	})
	if d.refused != "" {
		card.Disabled(true).Opacity(0.72)
	}
}

func (a *app) fsCard(c *ui.Context, value, title, body string) {
	p := a.p
	chosen := a.c.filesystem == value
	b := ui.RadioBase(c.Key(value), &a.c.filesystem, value).Padding(14).Gap(8).Radius(12).Column().AlignItems(ui.Stretch).
		Background(p.card).Border(1, p.cardBorder).Label(title).Cursor(ui.CursorPointer)
	if b.Hovered() && !chosen {
		b.Background(p.inset)
	}
	if chosen {
		b.Background(p.accentSoft).Border(2, p.accent).Padding(13)
	}
	b.Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, title).FontSize(15).FontWeight(600).Grow(1)
			radioMark(c, p, chosen)
		})
		ui.Text(c, body).FontSize(12.5).TextColor(p.muted)
	})
}

// --- security ---

func (a *app) securityPage(c *ui.Context) {
	p := a.p
	m := a.machine
	card(c, p).Children(func() {
		cardHeader(c, p, iconLock, "Disk encryption", "Choose explicitly; the installer starts at no encryption.")
		noTPM := "Needs a TPM 2.0 chip, and none was found."
		ui.RadioGroup(c, func() {
			choiceCard(c.Key("none"), p, &a.c.encryption, "none", iconUnlock, "No encryption",
				"Simplest. Anyone with physical access to the disk can read it.", nil)
			choiceCard(c.Key("luks"), p, &a.c.encryption, "luks-passphrase", iconLock, "Passphrase",
				"LUKS2 encryption. Type the passphrase each time the computer starts.", nil)
			tpm := choiceCard(c.Key("tpm"), p, &a.c.encryption, "tpm2-luks", iconChip, "Automatic unlock with the TPM",
				"This computer's TPM unlocks the disk at boot. You get a recovery key to store safely.", func() {
					if !m.env.TPM {
						ui.Text(c, noTPM).FontSize(12.5).TextColor(p.warning)
					}
				})
			both := choiceCard(c.Key("tpm-pass"), p, &a.c.encryption, "tpm2-luks-passphrase", iconShield, "TPM with a passphrase fallback",
				"The TPM unlocks automatically; the passphrase opens the disk if it cannot.", func() {
					if !m.env.TPM {
						ui.Text(c, noTPM).FontSize(12.5).TextColor(p.warning)
					}
				})
			if !m.env.TPM {
				tpm.Disabled(true).Opacity(0.6)
				both.Disabled(true).Opacity(0.6)
			}
		}).Gap(10).Label("Disk encryption")
		if msg := a.errText("security.encryption"); msg != "" {
			ui.Text(c, msg).FontSize(13).TextColor(p.danger)
		}
		if needsPassphrase(a.c.encryption) {
			ui.Divider(c).Margin(4, 0)
			a.secretPair(c, "Encryption passphrase", &a.c.passphrase, &a.c.passphraseConfirm,
				fieldPassphrase, fieldPassphraseConfirm, "You will type this at every boot (or when the TPM cannot unlock). It cannot be recovered if lost.")
		}
	})

	card(c, p).Children(func() {
		cardHeader(c, p, iconShield, "Secure Boot", "")
		if !m.env.SecureBoot {
			ui.Text(c, "Secure Boot is off on this computer, so no key needs enrolling. You can turn Secure Boot on later from the firmware settings.").
				FontSize(13.5).TextColor(p.muted)
			return
		}
		ui.Text(c, "Secure Boot is on. snosi's bootloader is signed with snosi's Machine Owner Key (MOK), which the firmware must trust.").
			FontSize(13.5).TextColor(p.muted)
		ui.RadioGroup(c, func() {
			choiceCard(c.Key("enroll"), p, &a.c.mok, "enroll", iconKey, "Enroll snosi's key",
				"At the next boot, MokManager asks for a one-time password to trust the key. Recommended.", nil)
			choiceCard(c.Key("skip"), p, &a.c.mok, "skip", iconSparkle, "Skip enrollment",
				"For people who manage Secure Boot keys themselves.", nil)
		}).Gap(10).Label("MOK enrollment")
		if a.c.mok == "enroll" {
			ui.Divider(c).Margin(4, 0)
			a.secretPair(c, "One-time MOK password", &a.c.mokPassword, &a.c.mokConfirm,
				fieldMokPassword, fieldMokConfirm, "Choose something simple: you type it once in MokManager at the next boot, with a US keyboard layout.")
		}
	})
}

// secretPair asks for a secret twice, with a show/hide toggle and a
// strength meter.
func (a *app) secretPair(c *ui.Context, label string, secret, confirm *string, field, confirmField, help string) {
	p := a.p
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
	strengthMeter(c, p, *secret)
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, help).FontSize(12.5).TextColor(p.muted).Grow(1).Shrink(1)
		eye, word := iconEye, "Show"
		if a.showSecret {
			eye, word = iconEyeOff, "Hide"
		}
		if ui.Button(c, "").Label(word + " passwords").Children(func() {
			ui.Icon(c, eye).FontSize(14)
			ui.Text(c, word).SingleLine()
		}).Clicked() {
			a.showSecret = !a.showSecret
		}
	})
}

// --- region ---

func (a *app) regionPage(c *ui.Context) {
	p := a.p
	card(c, p).Children(func() {
		cardHeader(c, p, iconMonitor, "Computer name", "")
		hostname := a.c.hostname
		ui.Field(c, "Hostname", func() {
			ui.TextInput(c, &a.c.hostname).Placeholder("snow")
		}).Description("How this computer appears on the network: letters, digits and hyphens.").Error(a.errText("system.hostname"))
		if a.c.hostname != hostname {
			a.c.hostnameAuto = false
		}
	})

	card(c, p).Children(func() {
		cardHeader(c, p, iconGlobe, "Language and region", "Leave a field empty to keep the image's default.")
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

	card(c, p).Children(func() {
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
	p := a.p
	card(c, p).Children(func() {
		ui.Row(c).Gap(12).Children(func() {
			iconTile(c, p, iconUser, p.accent, p.accentSoft, 36)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, "User account").FontSize(15).FontWeight(600)
				ui.Text(c, "Recommended. You can add more accounts after installing.").FontSize(13).TextColor(p.muted)
			})
			ui.Switch(c, &a.c.createUser).Label("Create a user account")
		})
		if !a.c.createUser {
			callout(c, p, calloutWarning, "No user account", "Only root can sign in, and only over SSH with a key added on the Region step.")
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
		ui.Divider(c).Margin(2, 0)
		a.secretPair(c, "Password", &a.c.password, &a.c.passwordConfirm, fieldPassword, fieldPasswordConfirm,
			"Used to sign in and for administrator tasks with sudo.")
	})

	if !a.c.createUser {
		return
	}
	card(c, p).Children(func() {
		cardHeader(c, p, iconShield, "Groups", "Preselected for "+displayName(a.c.entry.Name)+". Groups the image lacks are skipped at install.")
		ui.Grid(c).Columns(2).GapX(16).GapY(10).Children(func() {
			for i := range a.c.groups {
				g := &a.c.groups[i]
				ui.Row(c.Key(g.Name)).Gap(8).Children(func() {
					ui.Checkbox(c, &g.on, g.Name)
					if g.Description != "" {
						ui.Text(c, "— "+g.Description).FontSize(12.5).TextColor(p.muted).SingleLine().Shrink(1)
					}
				})
			}
		})
		ui.Field(c, "Additional groups", func() {
			ui.TextInput(c, &a.c.extraGroups).Placeholder("wheel, games")
		}).Description("Comma- or space-separated.").Error(a.errText("system.user.groups"))
	})

	card(c, p).Children(func() {
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
	p := a.p
	name := displayName(a.c.entry.Name)
	card(c, p).Children(func() {
		cardHeader(c, p, iconPackage, name+"'s core apps", "The everyday apps this image is designed around, as Flatpaks.")
		if a.preview.ref != a.c.entry.Ref {
			ui.Row(c).Gap(10).Padding(8, 0).Children(func() {
				ui.Spinner(c).Label("Reading the app list")
				ui.Text(c, "Reading "+name+"'s app list…").TextColor(p.muted)
			})
			return
		}
		toggle := func(title string) {
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, title).FontWeight(600).Grow(1)
				ui.Switch(c, &a.c.coreFlatpaks).Label(title)
			})
		}
		switch a.preview.state {
		case corePreviewAvailable:
			toggle(fmt.Sprintf("Install %d core apps", len(a.preview.apps)))
			ui.Row(c).Gap(6).Wrap().Opacity(map[bool]float32{true: 1, false: 0.5}[a.c.coreFlatpaks]).Children(func() {
				for _, app := range a.preview.apps {
					chip(c, app.Name, p.text, p.subtle)
				}
			})
		case corePreviewNone:
			callout(c, p, calloutInfo, "No core app set", name+" publishes no core apps.")
		case corePreviewInvalid:
			callout(c, p, calloutWarning, "Core app set unavailable", "This image's app list is malformed, so it cannot be installed. "+a.preview.detail)
		default:
			toggle("Install the core apps")
			detail := "The list could not be read right now; if this stays on, it is read again during the install."
			if a.preview.detail != "" {
				detail += " (" + a.preview.detail + ")"
			}
			ui.Text(c, detail).FontSize(13).TextColor(p.muted)
		}
	})

	card(c, p).Children(func() {
		cardHeader(c, p, iconDownload, "More apps", "Any Flatpak from Flathub, by application ID.")
		ui.Field(c, "Application IDs", func() {
			ui.TextArea(c, &a.c.flatpaksRaw).Lines(3, 8).Font("monospace").Placeholder("org.mozilla.firefox\nio.github.celluloid_player.Celluloid")
		}).Description("Separate IDs with spaces, commas or new lines.").Error(a.errText("system.flatpaks"))
		if ids := splitList(a.c.flatpaksRaw); len(ids) > 0 {
			ui.Row(c).Gap(6).Wrap().Children(func() {
				for _, id := range ids {
					chip(c, id, p.accent, p.accentSoft)
				}
			})
		}
	})
}

// --- review ---

func (a *app) reviewPage(c *ui.Context) {
	p := a.p
	if a.installErr != "" {
		callout(c, p, calloutDanger, "Could not start the install", a.installErr)
	}
	if errs := a.errs.onPage(pageReview); len(errs) > 0 {
		var msgs []string
		for field, e := range errs {
			msgs = append(msgs, field+": "+e.msg)
		}
		callout(c, p, calloutDanger, "The recipe has problems (a wizard bug — please report it)", strings.Join(msgs, "\n"))
	}
	if d, ok := a.chosenDisk(); ok {
		callout(c, p, calloutDanger, "Installing erases "+d.title(),
			fmt.Sprintf("All data on %s (%s) will be destroyed. You will confirm by typing the disk's path.", d.Path, humanBytes(d.Size)))
	}

	ui.Grid(c).Columns(2).Gap(14).Children(func() {
		e := a.c.entry
		a.summary(c, pageImage, [][2]string{
			{"Image", displayName(e.Name)},
			{"Source", e.Ref},
			{"Updates from", a.updateRef()},
			{"Bootloader", a.bootloaderName()},
		})
		storage := [][2]string{{"Disk", a.c.disk}}
		if d, ok := a.chosenDisk(); ok {
			storage = [][2]string{{"Disk", d.title()}, {"Device", d.Path + " · " + humanBytes(d.Size)}}
		}
		fs := a.c.filesystem
		if fs == "btrfs" && a.c.btrfsSubvolumes {
			fs += " with subvolumes"
		}
		a.summary(c, pageStorage, append(storage, [2]string{"Filesystem", fs}))
		sec := [][2]string{{"Encryption", encryptionName(a.c.encryption)}}
		if a.machine.env.SecureBoot {
			sec = append(sec, [2]string{"Secure Boot key", map[string]string{"enroll": "Enroll at next boot", "skip": "Not enrolled"}[a.c.mok]})
		} else {
			sec = append(sec, [2]string{"Secure Boot", "Off"})
		}
		a.summary(c, pageSecurity, sec)
		a.summary(c, pageRegion, [][2]string{
			{"Hostname", a.c.hostname},
			{"Language", orDefault(a.c.locale)},
			{"Time zone", orDefault(a.c.timezone)},
			{"Keyboard", orDefault(a.c.keyboard)},
			{"Root SSH", keyCount(a.c.rootSSHKey)},
		})
		acct := [][2]string{{"User", "None"}}
		if a.c.createUser {
			who := a.c.username
			if a.c.fullname != "" {
				who = a.c.fullname + " (" + a.c.username + ")"
			}
			acct = [][2]string{{"User", who}, {"Groups", strings.Join(a.c.selectedGroups(), ", ")}, {"SSH", keyCount(a.c.userSSHKey)}}
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
		a.summary(c, pageApps, [][2]string{{"Core apps", core}, {"More apps", extra}})
	})

	if a.reviewed != nil {
		card(c, p).Children(func() {
			ui.Collapsible(c, "Show the recipe", &a.recipeOpen, func() {
				ui.Text(c, "This exact TOML is saved and installed. Secrets are referenced as private files under "+a.session+", never written inline.").
					FontSize(13).TextColor(p.muted)
				codeBlock(c, p, strings.TrimRight(string(a.reviewed), "\n"))
			})
		})
	}
}

// summary is one review card: a page's answers with a way back to it.
func (a *app) summary(c *ui.Context, pg page, rows [][2]string) {
	p := a.p
	ui.Column(c.Key(pg)).Padding(18).Gap(12).Radius(14).Background(p.card).Border(1, p.cardBorder).Shadow(0, 1, 2, 0, p.shadow).Children(func() {
		ui.Row(c).Gap(10).Children(func() {
			iconTile(c, p, pageMeta[pg].icon, p.accent, p.accentSoft, 30)
			ui.Text(c, pageMeta[pg].nav).FontSize(15).FontWeight(600).Grow(1)
			edit := ui.Button(c, "").Label("Edit "+pageMeta[pg].nav).Tooltip(fmt.Sprintf("Edit (Alt+%d)", int(pg)+1)).Padding(4, 10).Children(func() {
				ui.Icon(c, iconPencil).FontSize(13)
				ui.Text(c, "Edit").FontSize(13).SingleLine()
			})
			if edit.Clicked() {
				a.jump(pg)
			}
		})
		ui.Grid(c).ColumnTracks(ui.FitContent(), ui.Fr(1)).GapX(14).GapY(6).Children(func() {
			for _, r := range rows {
				ui.Text(c, r[0]).FontSize(13).TextColor(p.muted).SingleLine()
				ui.Text(c, r[1]).FontSize(13).FontWeight(500).Selectable()
			}
		})
	})
}

func (a *app) updateRef() string {
	if a.c.advanced && strings.TrimSpace(a.c.targetRef) != "" {
		return strings.TrimSpace(a.c.targetRef)
	}
	return "Same as source"
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
