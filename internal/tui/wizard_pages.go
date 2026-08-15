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

// familyPage offers only families represented by the loaded catalog. A
// one-family catalog proceeds directly to image selection without presenting
// a choice that cannot lead to a valid recipe.
func (w *wizard) familyPage(ctx context.Context, preferred string) (family string, quit bool, err error) {
	families := catalogFamilies(w.catalog)
	if len(families) == 0 {
		return "", false, errors.New("tui: image catalog has no families")
	}
	if len(families) == 1 {
		return families[0], false, nil
	}
	family = preferredFamily(families, preferred)
	opts := make([]huh.Option[string], 0, len(families))
	for _, available := range families {
		switch available {
		case recipe.FamilyBootc:
			opts = append(opts, huh.NewOption("bootc — the long-term path, a little bolder today", available))
		case recipe.FamilyAB:
			opts = append(opts, huh.NewOption("A/B  — the proven path, for now", available))
		}
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Update mechanism").
			Description("Both are image-based and update atomically; they differ in how.\n\n" +
				"bootc is where frostyard images are headed long-term — updates\n" +
				"arrive as OCI container pulls. It is still maturing on Debian,\n" +
				"so it carries a little more risk today.\n\n" +
				"A/B keeps two complete system copies and flips between them —\n" +
				"proven and boring, at the cost of extra disk space. It will\n" +
				"retire once bootc is rock solid.").
			Options(opts...).
			Value(&family),
	))
	if quit, err = w.page(ctx, form); quit || err != nil {
		return "", quit, err
	}
	return family, false, nil
}

func preferredFamily(families []string, preferred string) string {
	for _, family := range families {
		if family == preferred {
			return preferred
		}
	}
	return families[0]
}

func (w *wizard) imagePage(ctx context.Context, family string) (quit bool, err error) {
	var entries []CatalogEntry
	for _, e := range w.catalog {
		if e.Family == family {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return false, fmt.Errorf("tui: catalog has no %s images", family)
	}
	idx := 0
	opts := make([]huh.Option[int], len(entries))
	for i, e := range entries {
		opts[i] = huh.NewOption(formatCatalogOption(e), i)
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
	w.c.entry = entries[idx]
	return false, nil
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
	if w.c.entry.Family == recipe.FamilyAB {
		if w.c.varFilesystem == "" {
			w.c.varFilesystem = "ext4"
		}
		return huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("/var filesystem").
					Description("The A/B root is part of the image; only /var is formatted here.").
					Options(
						huh.NewOption("ext4 (default)", "ext4"),
						huh.NewOption("btrfs", "btrfs"),
					).
					Value(&w.c.varFilesystem),
			),
			huh.NewGroup(
				huh.NewConfirm().
					Title("Create btrfs subvolumes in /var?").
					Description("Nested subvolumes home and snapshots inside /var.").
					Value(&w.c.varSubvolumes),
			).WithHideFunc(func() bool { return w.c.varFilesystem != "btrfs" }),
		)
	}
	// bootc. ZFS is deliberately absent: schema v1 rejects it until the
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
// either family under Secure Boot, the MOK enrollment choice (ADR-0014).
func (w *wizard) securityForm() *huh.Form {
	tpm := w.opts.Machine.TPM
	noTPMNote := ""
	if !tpm {
		noTPMNote = "\nTPM2-backed modes are unavailable: no TPM 2.0 device was detected."
	}

	var groups []*huh.Group
	if w.c.entry.Family == recipe.FamilyAB {
		encOpts := []huh.Option[string]{
			huh.NewOption("none — no encryption", "none"),
			huh.NewOption("luks — encrypted /var, generated recovery key only", "luks"),
		}
		if tpm {
			encOpts = append(encOpts,
				huh.NewOption("tpm2-luks — encrypted /var, TPM unlock plus recovery key", "tpm2-luks"))
		}
		if w.c.encryption == "" {
			w.c.encryption = "none"
		}
		groups = append(groups, huh.NewGroup(
			huh.NewSelect[string]().
				Title("Disk encryption").
				Description("Choose explicitly. Initial selection: none (no encryption)."+noTPMNote).
				Options(encOpts...).
				Value(&w.c.encryption),
		))
		groups = w.appendMOKGroups(groups)
		return huh.NewForm(groups...)
	}

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
				Validate(requireNonEmpty("a passphrase")),
			huh.NewInput().
				Title("Confirm passphrase").
				EchoMode(huh.EchoModePassword).
				Value(&passConfirm).
				Validate(func(s string) error {
					if s != w.c.passphrase {
						return errors.New("passphrases do not match")
					}
					return nil
				}),
		).WithHideFunc(func() bool { return !needsPassphrase(w.c.encryption) }),
	)
	groups = w.appendMOKGroups(groups)
	return huh.NewForm(groups...)
}

// appendMOKGroups adds the family-independent Secure Boot choice. The parent
// secure bootc installer required a MOK password file unconditionally; firn's
// recipe-driven divergence permits an explicit skip (ADR-0014), identically
// for bootc and A/B.
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
				Validate(requireNonEmpty("a MOK password")),
			huh.NewInput().
				Title("Confirm MOK password").
				EchoMode(huh.EchoModePassword).
				Value(&mokConfirm).
				Validate(func(s string) error {
					if s != w.c.mokPassword {
						return errors.New("passwords do not match")
					}
					return nil
				}),
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
				Validate(validateHostnameInput),
			huh.NewInput().
				Title("Locale").
				Description("Tab completes suggestions; empty keeps the image default.").
				Placeholder("en_US.UTF-8").
				Suggestions(commonLocales).
				Value(&w.c.locale).
				Validate(validateLocaleInput),
			huh.NewInput().
				Title("Timezone").
				Description("IANA zone name, e.g. America/Chicago; empty keeps the image default.").
				Suggestions(timezoneSuggestions(w.opts.Machine.ZoneinfoDir)).
				Value(&w.c.timezone).
				Validate(validateTimezoneInput(w.opts.Machine.ZoneinfoDir)),
			huh.NewInput().
				Title("Keyboard layout").
				Description("XKB LAYOUT[:VARIANT[:MODEL]], e.g. us or de:nodeadkeys; empty keeps the image default.").
				Placeholder("us").
				Suggestions(commonKeyboards).
				Value(&w.c.keyboard).
				Validate(validateKeyboardInput),
		),
		huh.NewGroup(
			huh.NewText().
				Title("Root SSH authorized key (optional)").
				Description("Paste one or more OpenSSH public key lines, or leave empty.").
				Lines(3).
				Value(&w.c.rootSSHKey).
				Validate(validateSSHKeyInput),
		),
	)
}

func (w *wizard) userForm() *huh.Form {
	// These are TUI initial selections, applied once. Keeping the marker in
	// wizardChoices distinguishes a first visit from a user who answered No
	// or removed every group before the form is rebuilt.
	if !w.c.userInitialized {
		w.c.createUser = true
		w.c.groups = []string{"sudo"}
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
				Validate(validateUsernameInput),
			huh.NewInput().
				Title("Full name (optional)").
				Description("Unicode is supported; ':' and line breaks are not.").
				Value(&w.c.fullname).
				Validate(validateFullnameInput),
			huh.NewInput().
				Title("Password").
				EchoMode(huh.EchoModePassword).
				Value(&w.c.password).
				Validate(requireNonEmpty("a password")),
			huh.NewInput().
				Title("Confirm password").
				EchoMode(huh.EchoModePassword).
				Value(&passConfirm).
				Validate(func(s string) error {
					if s != w.c.password {
						return errors.New("passwords do not match")
					}
					return nil
				}),
		).WithHideFunc(hidden),
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Groups").
				Description("Common groups plus the ones snosi's sysexts provide (docker,\n"+
					"incus, printing, scanning). Any not present in the chosen image\n"+
					"are skipped at install time.").
				Options(
					huh.NewOption("sudo — administrator", "sudo"),
					huh.NewOption("docker — run containers without sudo", "docker"),
					huh.NewOption("incus-admin — manage incus VMs/containers", "incus-admin"),
					huh.NewOption("lpadmin — manage printers", "lpadmin"),
					huh.NewOption("scanner — use scanners", "scanner"),
					huh.NewOption("adm — read system logs", "adm"),
					huh.NewOption("audio", "audio"),
					huh.NewOption("video", "video"),
					huh.NewOption("dialout — serial devices", "dialout"),
					huh.NewOption("plugdev — removable devices", "plugdev"),
					huh.NewOption("netdev — network configuration", "netdev"),
				).
				Value(&w.c.groups),
			huh.NewInput().
				Title("Additional groups (optional)").
				Description("Comma- or space-separated.").
				Value(&w.c.extraGroups).
				Validate(validateGroupListInput),
		).WithHideFunc(hidden),
		huh.NewGroup(
			huh.NewText().
				Title("User SSH authorized key (optional)").
				Description("Paste one or more OpenSSH public key lines, or leave empty.").
				Lines(3).
				Value(&w.c.userSSHKey).
				Validate(validateSSHKeyInput),
		).WithHideFunc(hidden),
	)
}

func (w *wizard) flatpaksForm() *huh.Form {
	return huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Install this image's core app set?").
			Description("Initially No. The curated Flatpak set published for this image,\ninstalled offline from the installer medium where available.").
			Value(&w.c.coreFlatpaks),
		huh.NewText().
			Title("Extra Flatpak apps (optional)").
			Description("Application IDs, e.g. org.mozilla.firefox — comma, space, or\nnewline separated.").
			Lines(3).
			Value(&w.c.flatpaksRaw),
	))
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
	desc = strings.NewReplacer("_", "\\_", "*", "\\*").Replace(desc)
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

// formatDiskOption renders one disk picker line: path, size, label, and
// the inline refusal reason for disks that must not be selected.
func formatDiskOption(d disk.Device, reason string) string {
	parts := []string{d.Path, humanSize(d.Size)}
	if d.Label != "" {
		parts = append(parts, d.Label)
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
