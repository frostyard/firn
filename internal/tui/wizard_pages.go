package tui

// Page builders for the wizard. Each page is one huh form kept legible
// at 80x24; dynamic follow-ups (btrfs subvolumes, passphrase entry, MOK
// password) are hidden groups toggled by the values entered before them.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/frostyard/firn/internal/disk"
	"github.com/frostyard/firn/internal/recipe"
)

// Review-page actions.
const (
	actionInstall   = "install"
	actionStartOver = "start-over"
	actionQuit      = "quit"
)

// rescanValue is the sentinel disk-picker option that re-enumerates.
const rescanValue = "\x00rescan"

func (w *wizard) welcomeForm() *huh.Form {
	var b strings.Builder
	b.WriteString("This wizard builds an install recipe, shows it to you, and only\n")
	b.WriteString("then installs. Nothing is written to disk before you confirm.\n\n")
	fmt.Fprintf(&b, "Machine: UEFI %s, Secure Boot %s, TPM 2.0 %s\n",
		yesNo(w.opts.UEFI), activeInactive(w.opts.Machine.SecureBoot), presentAbsent(w.opts.Machine.TPM))
	if len(w.opts.Notices) > 0 {
		b.WriteString("\nNOTICES:\n")
		for _, notice := range w.opts.Notices {
			fmt.Fprintf(&b, "- %s\n", notice)
		}
	}
	b.WriteString("\nPress enter to begin; esc quits at any point.")
	return huh.NewForm(huh.NewGroup(
		huh.NewNote().
			Title("firn — snosi installer").
			Description(b.String()).
			Next(true).
			NextLabel("Begin"),
	))
}

func (w *wizard) imagePage(ctx context.Context) (quit bool, err error) {
	entries := w.catalog
	if len(entries) == 0 {
		return false, errors.New("tui: catalog has no images")
	}
	idx := 0
	opts := make([]huh.Option[int], len(entries))
	for i, e := range entries {
		opts[i] = huh.NewOption(formatCatalogOption(e), i)
		if e.Name == w.c.entry.Name {
			idx = i
		}
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[int]().
			Title("Image").
			Description("What to install.").
			Options(opts...).
			Value(&idx),
	))
	if quit, err = w.page(ctx, form); quit || err != nil {
		return quit, err
	}
	w.setEntry(entries[idx])
	return false, nil
}

func (w *wizard) setEntry(entry CatalogEntry) {
	if entry.Name != w.c.entry.Name || entry.Family != w.c.entry.Family {
		w.c.userInitialized = false
	}
	w.c.entry = entry
}

// advancedImageForm exposes engine-supported image and target overrides while
// keeping the common catalog-driven path short. Image identity itself remains
// catalog-controlled; /etc/firn/catalog.json is the custom ref escape
// hatch and carries the matching trust metadata with the selection.
func (w *wizard) advancedImageForm() *huh.Form {
	hidden := func() bool { return !w.c.advancedImage }
	groups := []*huh.Group{huh.NewGroup(
		huh.NewConfirm().
			Title("Advanced image options?").
			Description("Override update tracking or bootloader. Initially No.").
			Value(&w.c.advancedImage),
	)}
	if w.c.bootloader == "" {
		w.c.bootloader = "systemd"
	}
	bootloaderOptions := []huh.Option[string]{
		huh.NewOption("systemd-boot (default)", "systemd"),
	}
	bootloaderDescription := "GRUB 2 is also available when Secure Boot is inactive."
	if !w.opts.Machine.SecureBoot {
		bootloaderOptions = append(bootloaderOptions, huh.NewOption("GRUB 2", "grub2"))
	} else {
		bootloaderDescription = "systemd-boot is required for Firn's Secure Boot enrollment path."
	}
	groups = append(groups, huh.NewGroup(
		huh.NewInput().
			Title("Post-install upgrade reference (optional)").
			Description("Leave empty to track the selected install reference.").
			Placeholder(w.c.entry.Ref).
			Value(&w.c.targetRef).
			Validate(w.skipWhenBacking(validateTargetRefInput)),
		huh.NewSelect[string]().
			Title("Bootloader").
			Description(bootloaderDescription).
			Options(bootloaderOptions...).
			Value(&w.c.bootloader),
	).WithHideFunc(hidden))
	return huh.NewForm(groups...)
}

// diskPage enumerates disks, showing refused ones inline with the
// reason; selecting a refused disk re-prompts with that reason, and the
// rescan entry re-enumerates (e.g. after unplugging or unmounting).
func (w *wizard) diskPage(ctx context.Context) (quit bool, err error) {
	if w.opts.Runner == nil {
		return false, errors.New("tui: disk enumeration requires a runner")
	}
	for {
		devices, err := disk.List(ctx, w.opts.Runner)
		if err != nil {
			return false, err
		}
		rootDev := disk.RootDevice(ctx, w.opts.Runner)

		reasons := make(map[string]string, len(devices))
		opts := make([]huh.Option[string], 0, len(devices)+1)
		for _, d := range devices {
			reason := disk.RefusalReason(d, rootDev)
			reasons[d.Path] = reason
			opts = append(opts, huh.NewOption(formatDiskOption(d, reason), d.Path))
		}
		opts = append(opts, huh.NewOption("Rescan disks", rescanValue))

		choice := rescanValue
		if len(devices) > 0 {
			choice = devices[0].Path
		}
		for _, d := range devices {
			if d.Path == w.c.disk {
				choice = w.c.disk
				break
			}
		}
		form := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Target disk").
				Description("The selected disk will be completely erased.").
				Options(opts...).
				Value(&choice).
				Validate(func(v string) error {
					return diskChoiceError(reasons, v)
				}),
		))
		if quit, err = w.page(ctx, form); quit || err != nil {
			return quit, err
		}
		if isRescanChoice(choice) {
			continue
		}
		w.c.disk = choice
		return false, nil
	}
}

func diskChoiceError(reasons map[string]string, choice string) error {
	if reason := reasons[choice]; reason != "" {
		return fmt.Errorf("cannot install to %s: %s", choice, reason)
	}
	return nil
}

func isRescanChoice(choice string) bool { return choice == rescanValue }

func (w *wizard) filesystemForm() *huh.Form {
	// ZFS is deliberately absent: the schema rejects it until the
	// installer has a complete, bootable ZFS path.
	if w.c.filesystem == "" {
		w.c.filesystem = "btrfs"
	}
	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Root filesystem").
				Options(
					huh.NewOption("btrfs (default)", "btrfs"),
					huh.NewOption("xfs", "xfs"),
					huh.NewOption("ext4", "ext4"),
				).
				Value(&w.c.filesystem),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Create btrfs subvolumes?").
				Description("Top-level @, @home, and @snapshots subvolumes.").
				Value(&w.c.btrfsSubvolumes),
		).WithHideFunc(func() bool { return w.c.filesystem != "btrfs" }),
	)
}

// securityForm covers encryption (always explicit, ADR-0004) and, for
// under Secure Boot, the MOK enrollment choice (ADR-0014).
func (w *wizard) securityForm() *huh.Form {
	tpm := w.opts.Machine.TPM
	noTPMNote := ""
	if !tpm {
		noTPMNote = "\nTPM2-backed modes are unavailable: no TPM 2.0 device was detected."
	}

	var groups []*huh.Group
	// bootc.
	encOpts := []huh.Option[string]{
		huh.NewOption("none — no encryption", "none"),
		huh.NewOption("luks-passphrase — LUKS2, passphrase at boot", "luks-passphrase"),
	}
	if tpm {
		encOpts = append(encOpts,
			huh.NewOption("tpm2-luks — LUKS2, unlocked automatically by the TPM", "tpm2-luks"),
			huh.NewOption("tpm2-luks-passphrase — TPM unlock plus fallback passphrase", "tpm2-luks-passphrase"),
		)
	}
	if w.c.encryption == "" {
		w.c.encryption = "none"
	}
	var passConfirm string
	groups = append(groups,
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Disk encryption").
				Description("Choose explicitly. Initial selection: none (no encryption)."+noTPMNote).
				Options(encOpts...).
				Value(&w.c.encryption),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("Encryption passphrase").
				EchoMode(huh.EchoModePassword).
				Value(&w.c.passphrase).
				Validate(w.skipWhenBacking(requireNonEmpty("a passphrase"))),
			huh.NewInput().
				Title("Confirm passphrase").
				EchoMode(huh.EchoModePassword).
				Value(&passConfirm).
				Validate(w.skipWhenBacking(func(s string) error {
					if s != w.c.passphrase {
						return errors.New("passphrases do not match")
					}
					return nil
				})),
		).WithHideFunc(func() bool { return !needsPassphrase(w.c.encryption) }),
	)
	groups = w.appendMOKGroups(groups)
	return huh.NewForm(groups...)
}

// appendMOKGroups adds the Secure Boot choice (ADR-0014).
func (w *wizard) appendMOKGroups(groups []*huh.Group) []*huh.Group {
	if !w.opts.Machine.SecureBoot {
		return groups
	}
	// huh visually focuses the first option when the bound value is empty,
	// but does not write it unless the cursor moves. Initialize the visible
	// choice explicitly so accepting it cannot leave security.mok empty.
	if w.c.mok == "" {
		w.c.mok = "enroll"
	}
	var mokConfirm string
	return append(groups,
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Secure Boot key (MOK) enrollment").
				Description("Secure Boot is active. Initial selection: enroll snosi's Machine\nOwner Key; choose skip if you manage Secure Boot keys yourself.").
				Options(
					huh.NewOption("enroll — enroll the key (one-time password at next boot)", "enroll"),
					huh.NewOption("skip — do not enroll (manage Secure Boot keys yourself)", "skip"),
				).
				Value(&w.c.mok),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("MOK enrollment password").
				Description("MokManager asks for this once at the next boot.").
				EchoMode(huh.EchoModePassword).
				Value(&w.c.mokPassword).
				Validate(w.skipWhenBacking(requireNonEmpty("a MOK password"))),
			huh.NewInput().
				Title("Confirm MOK password").
				EchoMode(huh.EchoModePassword).
				Value(&mokConfirm).
				Validate(w.skipWhenBacking(func(s string) error {
					if s != w.c.mokPassword {
						return errors.New("passwords do not match")
					}
					return nil
				})),
		).WithHideFunc(func() bool { return w.c.mok != "enroll" }),
	)
}

func (w *wizard) systemForm() *huh.Form {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Hostname").
				Placeholder("frost01").
				Value(&w.c.hostname).
				Validate(w.skipWhenBacking(validateHostnameInput)),
			huh.NewInput().
				Title("Locale").
				Description("Tab completes suggestions; empty keeps the image default.").
				Placeholder("en_US.UTF-8").
				Suggestions(commonLocales).
				Value(&w.c.locale).
				Validate(w.skipWhenBacking(validateLocaleInput)),
			huh.NewInput().
				Title("Timezone").
				Description("IANA zone name, e.g. America/Chicago; empty keeps the image default.").
				Suggestions(timezoneSuggestions(w.opts.Machine.ZoneinfoDir)).
				Value(&w.c.timezone).
				Validate(w.skipWhenBacking(validateTimezoneInput(w.opts.Machine.ZoneinfoDir))),
			huh.NewInput().
				Title("Keyboard layout").
				Description("XKB LAYOUT[:VARIANT[:MODEL]], e.g. us or de:nodeadkeys; empty keeps the image default.").
				Placeholder("us").
				Suggestions(commonKeyboards).
				Value(&w.c.keyboard).
				Validate(w.skipWhenBacking(validateKeyboardInput)),
		),
		huh.NewGroup(
			huh.NewText().
				Title("Root SSH authorized key (optional)").
				Description("Paste one or more OpenSSH public key lines, or leave empty.").
				Lines(3).
				Value(&w.c.rootSSHKey).
				Validate(w.skipWhenBacking(validateSSHKeyInput)),
		),
	)
}

// groupOptions is the single source of the group multi-select's offered
// set. Every group a catalog entry's default_groups may preselect MUST be
// listed here -- huh's MultiSelect silently drops selected values it has
// no option for (pinned by TestGroupOptionsCoverAllBuiltinDefaults).
func groupOptions() []huh.Option[string] {
	return []huh.Option[string]{
		huh.NewOption("sudo — administrator", "sudo"),
		huh.NewOption("docker — run containers without sudo", "docker"),
		huh.NewOption("incus-admin — manage incus VMs/containers", "incus-admin"),
		huh.NewOption("lpadmin — manage printers", "lpadmin"),
		huh.NewOption("scanner — use scanners", "scanner"),
		huh.NewOption("adm — read system logs", "adm"),
		huh.NewOption("audio", "audio"),
		huh.NewOption("video", "video"),
		huh.NewOption("input — raw input devices", "input"),
		huh.NewOption("render — GPU compute/render nodes", "render"),
		huh.NewOption("dialout — serial devices", "dialout"),
		huh.NewOption("plugdev — removable devices", "plugdev"),
		huh.NewOption("netdev — network configuration", "netdev"),
	}
}

// groupOptionValues returns just the option values, for coverage checks.
func groupOptionValues() []string {
	opts := groupOptions()
	vals := make([]string, 0, len(opts))
	for _, o := range opts {
		vals = append(vals, o.Value)
	}
	return vals
}

func (w *wizard) userForm() *huh.Form {
	// These are TUI initial selections, applied once. Keeping the marker in
	// wizardChoices distinguishes a first visit from a user who answered No
	// or removed every group before the form is rebuilt.
	if !w.c.userInitialized {
		w.c.createUser = true
		// Preselect the chosen image's default group set (catalog
		// default_groups, frostyard/snosi#789); generic fallback when the
		// entry declares none. Copy -- huh mutates the bound slice and must
		// never write into the catalog entry.
		if len(w.c.entry.DefaultGroups) > 0 {
			w.c.groups = append([]string(nil), w.c.entry.DefaultGroups...)
		} else {
			w.c.groups = []string{"sudo"}
		}
		w.c.userInitialized = true
	}
	var passConfirm string
	hidden := func() bool { return !w.c.createUser }
	return huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Create a user account?").
				Description("Initially Yes. Without one, only root (via the SSH key, if any) can log in.").
				Value(&w.c.createUser),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("Username").
				Value(&w.c.username).
				Validate(w.skipWhenBacking(validateUsernameInput)),
			huh.NewInput().
				Title("Full name (optional)").
				Description("Unicode is supported; ':' and line breaks are not.").
				Value(&w.c.fullname).
				Validate(w.skipWhenBacking(validateFullnameInput)),
			huh.NewInput().
				Title("Password").
				EchoMode(huh.EchoModePassword).
				Value(&w.c.password).
				Validate(w.skipWhenBacking(requireNonEmpty("a password"))),
			huh.NewInput().
				Title("Confirm password").
				EchoMode(huh.EchoModePassword).
				Value(&passConfirm).
				Validate(w.skipWhenBacking(func(s string) error {
					if s != w.c.password {
						return errors.New("passwords do not match")
					}
					return nil
				})),
		).WithHideFunc(hidden),
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Groups").
				Description("Common groups plus the ones snosi's sysexts provide (docker,\n"+
					"incus, printing, scanning). Any not present in the chosen image\n"+
					"are skipped at install time.").
				Options(groupOptions()...).
				Value(&w.c.groups),
			huh.NewInput().
				Title("Additional groups (optional)").
				Description("Comma- or space-separated.").
				Value(&w.c.extraGroups).
				Validate(w.skipWhenBacking(validateGroupListInput)),
		).WithHideFunc(hidden),
		huh.NewGroup(
			huh.NewText().
				Title("User SSH authorized key (optional)").
				Description("Paste one or more OpenSSH public key lines, or leave empty.").
				Lines(3).
				Value(&w.c.userSSHKey).
				Validate(w.skipWhenBacking(validateSSHKeyInput)),
		).WithHideFunc(hidden),
	)
}

// flatpaksForm offers the chosen image's core app set according to
// w.preview (refreshed by the run loop before this page), plus extra apps.
// An image with no set, or a malformed one, gets an explanation instead of
// the toggle, and the toggle's value is cleared so the recipe never asks
// for a set the wizard did not offer.
//
// The toggle starts on for a set the preview read, and off when the label
// could not be read: preflight fails with core_flatpaks_label_unreadable
// if it still cannot read it. That initial selection applies once per
// chosen image, so a rebuilt form or a return visit keeps the user's answer.
func (w *wizard) flatpaksForm() *huh.Form {
	if ref := w.c.entry.Ref; w.c.coreFlatpaksRef != ref {
		w.c.coreFlatpaksRef = ref
		w.c.coreFlatpaks = w.preview.state == corePreviewAvailable
	}
	var core huh.Field
	switch w.preview.state {
	case corePreviewNone:
		w.c.coreFlatpaks = false
		core = huh.NewNote().
			Title("No core app set").
			Description("This image publishes no core Flatpak apps.")
	case corePreviewInvalid:
		w.c.coreFlatpaks = false
		core = huh.NewNote().
			Title("Core app set unavailable").
			Description(escapeMarkdown("This image's core app list is malformed, so it cannot be installed.\n" + w.preview.detail))
	case corePreviewAvailable:
		core = huh.NewConfirm().
			Title("Install this image's core app set?").
			Description("Initially Yes. This image publishes these Flatpak apps:\n" + wrapNames(w.preview.apps, 64)).
			Value(&w.c.coreFlatpaks)
	default:
		desc := "Initially No. This image's list could not be read now; it is read\nagain at install time."
		if w.preview.detail != "" {
			desc += "\n(" + w.preview.detail + ")"
		}
		core = huh.NewConfirm().
			Title("Install this image's core app set?").
			Description(desc).
			Value(&w.c.coreFlatpaks)
	}
	return huh.NewForm(huh.NewGroup(
		core,
		huh.NewText().
			Title("Extra Flatpak apps (optional)").
			Description("Application IDs, e.g. org.mozilla.firefox — comma, space, or\nnewline separated.").
			Lines(3).
			Value(&w.c.flatpaksRaw),
	))
}

// escapeMarkdown keeps note text literal: huh renders note descriptions as
// markdown, so paired underscores (core_flatpaks) would vanish.
func escapeMarkdown(s string) string {
	return strings.NewReplacer("_", "\\_", "*", "\\*").Replace(s)
}

func (w *wizard) reviewForm(recipeTOML string, issues []recipe.Issue, action *string) *huh.Form {
	desc := recipeTOML
	if txt := renderIssues(issues); txt != "" {
		desc = txt + "\n" + desc
	}
	// huh renders note descriptions as markdown: paired underscores in
	// TOML keys/values (core_flatpaks, en_US.UTF-8) become italics and
	// the characters VANISH from the "review this exact recipe" screen
	// (observed live in the TUI E2E). Escape markdown emphasis so the
	// TOML shows byte-exact.
	desc = escapeMarkdown(desc)
	return huh.NewForm(huh.NewGroup(
		huh.NewNote().
			Title("Review — this exact recipe will be installed").
			Description(desc),
		huh.NewSelect[string]().
			Options(
				huh.NewOption("Install", actionInstall),
				huh.NewOption("Start over", actionStartOver),
				huh.NewOption("Quit without installing", actionQuit),
			).
			Value(action),
	))
}

// confirmForm is the typed confirmation: the user must type the exact
// disk path (snosi's rule); anything else re-prompts.
func (w *wizard) confirmForm() *huh.Form {
	var typed string
	return huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Point of no return").
			Description(fmt.Sprintf("Installing %s to %s will DESTROY all data on that disk.\nType the disk path exactly to continue.",
				w.c.entry.Name, w.c.disk)).
			Placeholder(w.c.disk).
			Value(&typed).
			Validate(func(s string) error {
				if s != w.c.disk {
					return fmt.Errorf("type %s exactly to confirm (esc aborts)", w.c.disk)
				}
				return nil
			}),
	))
}

// formatDiskOption renders one disk picker option with the hardware identity
// fields snosi-install exposes plus filesystem labels from the whole device
// tree. huh wraps long options to the terminal width, keeping every available
// identifier visible instead of truncating it.
func formatDiskOption(d disk.Device, reason string) string {
	parts := []string{d.Path, humanSize(d.Size)}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"vendor", d.Vendor},
		{"model", d.Model},
		{"serial", d.Serial},
		{"wwn", d.WWN},
		{"transport", d.Transport},
	} {
		if value := strings.TrimSpace(field.value); value != "" {
			parts = append(parts, field.name+"="+value)
		}
	}
	if labels := disk.Labels(d); len(labels) > 0 {
		parts = append(parts, "labels="+strings.Join(labels, ","))
	}
	line := strings.Join(parts, "  ")
	if reason != "" {
		line += "  [REFUSED: " + reason + "]"
	}
	return line
}

// humanSize renders bytes in IEC units with one decimal.
func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func requireNonEmpty(what string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("enter " + what)
		}
		return nil
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func activeInactive(v bool) string {
	if v {
		return "active"
	}
	return "inactive"
}

func presentAbsent(v bool) string {
	if v {
		return "present"
	}
	return "absent"
}
