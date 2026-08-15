package tui

// The wizard is a recipe generator (ADR-0005/ADR-0007): it collects
// choices page by page and assembles a recipe.Recipe. The wizard marshals it
// once for review and returns those exact bytes for persistence and install.
// Spec rule 5 (recipe-schema) is enforced by round-tripping the reviewed bytes
// through recipe.Parse + recipe.Validate before returning them.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/huh"

	"github.com/frostyard/firn/internal/platform"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/runner"
)

// WizardOpts carries what the wizard needs from the caller.
type WizardOpts struct {
	Runner     *runner.Runner
	Machine    recipe.Env
	UEFI       bool
	Catalog    []CatalogEntry
	Notices    []string
	SessionDir string
}

// RunWizard walks the user through building a recipe. It returns the exact
// TOML bytes shown on the accepted review page. A nil slice means the user
// quit or aborted; errors are reserved for real failures (I/O, disk
// enumeration, or an internal wizard bug).
func RunWizard(ctx context.Context, o WizardOpts) ([]byte, error) {
	if err := platform.RequireUEFI(o.UEFI); err != nil {
		return nil, err
	}
	catalog := o.Catalog
	if len(catalog) == 0 {
		var warn error
		catalog, warn = LoadCatalog()
		if warn != nil {
			// Built-ins are returned alongside the warning. Put the
			// problem on the welcome page because stderr is journal-only
			// on the installer kiosk.
			o.Notices = append(o.Notices, warn.Error())
		}
	}
	if err := checkCatalog(catalog); err != nil {
		return nil, fmt.Errorf("tui: image catalog: %w", err)
	}
	if o.SessionDir == "" {
		return nil, errors.New("tui: wizard session directory is required")
	}
	w := &wizard{
		opts:       o,
		catalog:    orderedCatalog(catalog),
		theme:      huh.ThemeBase16(), // ANSI 16-color: safe on consoles and serial terminals
		secretsDir: o.SessionDir,
	}
	return w.run(ctx)
}

// wizard holds the flow state for one RunWizard call.
type wizard struct {
	opts       WizardOpts
	catalog    []CatalogEntry
	theme      *huh.Theme
	secretsDir string
	c          wizardChoices
}

// wizardChoices is everything the pages collect, kept apart from
// recipe.Recipe so assembly (choices -> recipe + secret files) is a pure
// function tests drive directly.
type wizardChoices struct {
	entry CatalogEntry

	disk string

	// bootc filesystem choices.
	filesystem      string
	btrfsSubvolumes bool
	// ab filesystem choices.
	varFilesystem string
	varSubvolumes bool

	encryption  string
	passphrase  string // secret; never enters the recipe inline
	mok         string // "", "enroll", or "skip" (ab + Secure Boot only)
	mokPassword string // secret

	hostname   string
	locale     string
	timezone   string
	keyboard   string
	rootSSHKey string

	createUser      bool
	userInitialized bool
	username        string
	fullname        string
	password        string // secret
	groups          []string
	extraGroups     string
	userSSHKey      string

	coreFlatpaks bool
	flatpaksRaw  string
}

// run drives the page flow: welcome, then a collection pass
// (image, disk, filesystem, security, system, user, flatpaks), then the
// review / typed-confirmation / validation loop.
func (w *wizard) run(ctx context.Context) ([]byte, error) {
	if len(w.catalog) == 0 {
		return nil, errors.New("tui: image catalog is empty")
	}
	if quit, err := w.page(ctx, w.welcomeForm()); quit || err != nil {
		return nil, err
	}
	preferredFamily := ""
	for {
		family, quit, err := w.familyPage(ctx, preferredFamily)
		if quit || err != nil {
			return nil, err
		}
		if quit, err := w.imagePage(ctx, family); quit || err != nil {
			return nil, err
		}
		if quit, err := w.diskPage(ctx); quit || err != nil {
			return nil, err
		}
		if quit, err := w.page(ctx, w.filesystemForm()); quit || err != nil {
			return nil, err
		}
		if quit, err := w.page(ctx, w.securityForm()); quit || err != nil {
			return nil, err
		}
		if quit, err := w.page(ctx, w.systemForm()); quit || err != nil {
			return nil, err
		}
		if quit, err := w.page(ctx, w.userForm()); quit || err != nil {
			return nil, err
		}
		if quit, err := w.page(ctx, w.flatpaksForm()); quit || err != nil {
			return nil, err
		}

		startOver, reviewed, err := w.reviewLoop(ctx)
		if err != nil || reviewed != nil {
			return reviewed, err
		}
		if !startOver { // user quit from review
			return nil, nil
		}
		preferredFamily = w.c.entry.Family
		w.c = wizardChoices{} // start over: fresh choices, same catalog
	}
}

// reviewLoop shows the assembled recipe, requires the typed disk-path
// confirmation, and validates the result. A validation issue at this
// point is a wizard bug: it is displayed on a second review pass, and if
// it persists the issues are returned as an error (spec rule 5 says this
// must be unreachable).
func (w *wizard) reviewLoop(ctx context.Context) (startOver bool, result []byte, err error) {
	defer func() {
		if result != nil {
			return
		}
		if cleanupErr := cleanupSecretFiles(w.secretsDir); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()
	var issues []recipe.Issue
	retried := false
	for {
		rec, err := assembleRecipe(w.c, w.secretsDir)
		if err != nil {
			return false, nil, err
		}
		reviewed, err := marshalAssembled(rec)
		if err != nil {
			return false, nil, err
		}
		action := actionInstall
		quit, err := w.page(ctx, w.reviewForm(string(reviewed), issues, &action))
		if quit || err != nil {
			return false, nil, err
		}
		startOver, quit := reviewAction(action)
		switch {
		case startOver:
			return true, nil, nil
		case quit:
			return false, nil, nil
		}
		if quit, err := w.page(ctx, w.confirmForm()); quit || err != nil {
			return false, nil, err
		}
		issues = validateAssembled(reviewed, w.opts.Machine)
		if len(issues) == 0 {
			return false, reviewed, nil
		}
		if retried {
			return false, nil, issuesError(issues)
		}
		retried = true // loop back to review with the issues displayed
	}
}

const (
	passphraseSecretName = "passphrase"
	mokSecretName        = "mok-password"
	userSecretName       = "user-password"
)

var generatedSecretNames = []string{passphraseSecretName, mokSecretName, userSecretName}

func cleanupSecretFiles(dir string) error {
	var errs []error
	for _, name := range generatedSecretNames {
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("tui: removing abandoned secret %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

func reviewAction(action string) (startOver, quit bool) {
	return action == actionStartOver, action == actionQuit
}

// page runs one huh form, translating a user abort (esc / ctrl-c) into
// quit=true and a context cancellation into its error.
func (w *wizard) page(ctx context.Context, f *huh.Form) (quit bool, err error) {
	// huh renders to os.Stderr by default (huh form.go: tea.WithOutput
	// (os.Stderr)). On the installer kiosk, systemd routes the unit's
	// stderr to the journal while the TTY is stdout — so the wizard was
	// invisible on every console and fully present in the journal
	// (root-caused on the incus VGA demo, 2026-08-12: /dev/vcs1 blank,
	// journal full of frames). Render to the TTY explicitly.
	err = f.WithTheme(w.theme).WithOutput(os.Stdout).WithInput(os.Stdin).RunWithContext(ctx)
	if err == nil {
		return false, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if errors.Is(err, huh.ErrUserAborted) {
		return true, nil
	}
	return false, err
}

// --- assembly (pure: choices -> recipe + secret files) ---

// assembleRecipe turns collected choices into a recipe, writing every
// secret to a 0600 file under secretsDir (created 0700) and carrying
// only the *_file paths (spec rule 6).
func assembleRecipe(c wizardChoices, secretsDir string) (*recipe.Recipe, error) {
	r := &recipe.Recipe{Version: recipe.SchemaVersion}
	r.Image.Family = c.entry.Family
	r.Target.Disk = c.disk

	switch c.entry.Family {
	case recipe.FamilyBootc:
		r.Image.Ref = c.entry.Ref
		r.Image.CosignPubKey = c.entry.CosignPubKey
		r.Target.Filesystem = c.filesystem
		r.Target.BtrfsSubvolumes = c.btrfsSubvolumes && c.filesystem == "btrfs"
	case recipe.FamilyAB:
		r.Image.Product = c.entry.Product
		r.Target.VarFilesystem = c.varFilesystem
		r.Target.VarSubvolumes = c.varSubvolumes && c.varFilesystem == "btrfs"
	default:
		return nil, fmt.Errorf("tui: catalog entry %q has unknown family %q", c.entry.Name, c.entry.Family)
	}

	r.Security.Encryption = c.encryption
	if c.entry.Family == recipe.FamilyAB && c.encryption != "none" {
		r.Security.RecoveryKeyOut = filepath.Join(secretsDir, "recovery-key")
	}
	if c.entry.Family == recipe.FamilyBootc && needsPassphrase(c.encryption) {
		if strings.TrimSpace(c.passphrase) == "" {
			return nil, fmt.Errorf("tui: encryption %q selected without a passphrase (wizard bug)", c.encryption)
		}
		p, err := writeSecretFile(secretsDir, passphraseSecretName, c.passphrase)
		if err != nil {
			return nil, err
		}
		r.Security.PassphraseFile = p
	}
	// MOK enrollment is family-independent under Secure Boot. A/B and
	// bootc use different pipeline staging implementations, but share the
	// same recipe fields and one-time MokManager password (ADR-0014).
	if c.mok != "" {
		r.Security.Mok = c.mok
		if c.mok == "enroll" {
			if strings.TrimSpace(c.mokPassword) == "" {
				return nil, errors.New("tui: MOK enrollment selected without a MOK password (wizard bug)")
			}
			p, err := writeSecretFile(secretsDir, mokSecretName, c.mokPassword)
			if err != nil {
				return nil, err
			}
			r.Security.MokPasswordFile = p
		}
	}

	r.System.Hostname = strings.TrimSpace(c.hostname)
	r.System.Locale = strings.TrimSpace(c.locale)
	r.System.Timezone = strings.TrimSpace(c.timezone)
	r.System.Keyboard = strings.TrimSpace(c.keyboard)
	r.System.RootSSHAuthorizedKey = strings.TrimSpace(c.rootSSHKey)
	r.System.CoreFlatpaks = c.coreFlatpaks
	r.System.Flatpaks = splitList(c.flatpaksRaw)

	if c.createUser {
		u := &recipe.User{
			Name:             strings.TrimSpace(c.username),
			Fullname:         strings.TrimSpace(c.fullname),
			Groups:           mergeGroups(c.groups, c.extraGroups),
			SSHAuthorizedKey: strings.TrimSpace(c.userSSHKey),
		}
		if strings.TrimSpace(c.password) == "" {
			return nil, errors.New("tui: user creation selected without a password (wizard bug)")
		}
		p, err := writeSecretFile(secretsDir, userSecretName, c.password)
		if err != nil {
			return nil, err
		}
		u.PasswordFile = p
		r.System.User = u
	}
	return r, nil
}

// writeSecretFile writes content to dir/name with 0600 permissions
// (dir created 0700), returning the path for the recipe's *_file field.
func writeSecretFile(dir, name, content string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("tui: creating secrets dir: %w", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("tui: writing secret file: %w", err)
	}
	// WriteFile keeps existing permissions on overwrite; pin them.
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("tui: securing secret file: %w", err)
	}
	return path, nil
}

// marshalAssembled is the wizard's sole serialization boundary. Its returned
// bytes are shown on review, validated, persisted, and installed unchanged.
func marshalAssembled(r *recipe.Recipe) ([]byte, error) {
	data, err := recipe.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("tui: cannot marshal assembled recipe (wizard bug): %w", err)
	}
	return data, nil
}

// validateAssembled parses the reviewed bytes with the canonical loader and
// validates against the machine environment — exactly what headless
// `firn install` will do.
func validateAssembled(reviewed []byte, env recipe.Env) []recipe.Issue {
	l, err := recipe.Parse(reviewed)
	if err != nil {
		return []recipe.Issue{{Code: "render", Field: "recipe", Message: err.Error()}}
	}
	return recipe.Validate(l, env)
}

func issuesError(issues []recipe.Issue) error {
	msgs := make([]string, len(issues))
	for i, is := range issues {
		msgs[i] = is.Error()
	}
	return fmt.Errorf("tui: assembled recipe failed validation (wizard bug): %s", strings.Join(msgs, "; "))
}

// renderIssues formats validation issues for the review page.
func renderIssues(issues []recipe.Issue) string {
	if len(issues) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("VALIDATION ISSUES (this is a wizard bug — please report it):\n")
	for _, is := range issues {
		fmt.Fprintf(&b, "  - %s\n", is.Error())
	}
	return b.String()
}

// --- small pure helpers the pages and assembly share ---

// needsPassphrase reports whether a bootc encryption mode requires a
// user-supplied passphrase.
func needsPassphrase(encryption string) bool {
	return encryption == "luks-passphrase" || encryption == "tpm2-luks-passphrase"
}

// splitList splits a free-form comma/whitespace-separated entry into a
// deduplicated list, preserving first-seen order. Empty input yields nil.
func splitList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	var out []string
	seen := make(map[string]bool, len(fields))
	for _, f := range fields {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// mergeGroups combines the multi-select choices with the free-add entry,
// deduplicated in order.
func mergeGroups(selected []string, extra string) []string {
	return splitList(strings.Join(selected, ",") + "," + extra)
}

// --- live input validation ---
//
// These mirror internal/recipe/validate.go so users hear about bad input
// on the field, not at the end. Keep the patterns in sync; the assembled
// recipe is still round-tripped through recipe.Validate, which remains
// the only authority (spec rule 5).

var (
	hostnameLabelInputRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)
	localeInputRe        = regexp.MustCompile(`^[a-z]{2,3}_[A-Z]{2}(\.[A-Za-z0-9-]+)?$`)
	timezoneInputRe      = regexp.MustCompile(`^[A-Za-z_+-]+(/[A-Za-z0-9_+-]+)*$`)
	keyboardInputRe      = regexp.MustCompile(`^[a-z0-9_-]+(:[a-z0-9_-]+(:[a-z0-9_-]+)?)?$`)
	usernameInputRe      = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
)

func validateHostnameInput(h string) error {
	h = strings.TrimSpace(h)
	if h == "" {
		return errors.New("hostname is required")
	}
	if len(h) > 253 {
		return errors.New("hostname must be at most 253 characters")
	}
	for label := range strings.SplitSeq(h, ".") {
		if len(label) == 0 || len(label) > 63 || !hostnameLabelInputRe.MatchString(label) {
			return errors.New("must be RFC 1123 host label(s), e.g. frost01")
		}
	}
	return nil
}

func validateUsernameInput(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("username is required")
	}
	if !usernameInputRe.MatchString(name) || len(name) > 32 {
		return errors.New("must match [a-z_][a-z0-9_-]* and be at most 32 characters")
	}
	return nil
}

func validateFullnameInput(fullname string) error {
	return recipe.ValidateFullname(strings.TrimSpace(fullname))
}

func validateLocaleInput(locale string) error {
	locale = strings.TrimSpace(locale)
	if locale != "" && !localeInputRe.MatchString(locale) {
		return errors.New("must look like ll_CC or ll_CC.ENC, e.g. en_US.UTF-8")
	}
	return nil
}

// validateTimezoneInput checks the zone name format and, when a
// zoneinfo dir is available, that the zone exists in it.
func validateTimezoneInput(zoneinfoDir string) func(string) error {
	return func(tz string) error {
		tz = strings.TrimSpace(tz)
		if tz == "" {
			return nil
		}
		if !timezoneInputRe.MatchString(tz) || strings.HasPrefix(tz, "/") {
			return errors.New("must be an IANA zone name, e.g. America/Chicago")
		}
		if zoneinfoDir != "" {
			if _, err := os.Stat(filepath.Join(zoneinfoDir, tz)); err != nil {
				return fmt.Errorf("%q not found in zoneinfo", tz)
			}
		}
		return nil
	}
}

func validateKeyboardInput(kb string) error {
	kb = strings.TrimSpace(kb)
	if kb != "" && !keyboardInputRe.MatchString(kb) {
		return errors.New("must be LAYOUT[:VARIANT[:MODEL]], e.g. us or de:nodeadkeys")
	}
	return nil
}

func validateSSHKeyInput(key string) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	return recipe.ValidateSSHAuthorizedKeys(key)
}

func validateGroupListInput(raw string) error {
	for _, g := range splitList(raw) {
		if !usernameInputRe.MatchString(g) {
			return fmt.Errorf("invalid group name %q", g)
		}
	}
	return nil
}

// timezoneSuggestions lists zone names from zoneinfoDir (up to three
// levels, e.g. America/Argentina/Buenos_Aires), falling back to a small
// common list when the dir is empty or unreadable.
func timezoneSuggestions(zoneinfoDir string) []string {
	if zoneinfoDir == "" {
		return commonTimezones
	}
	skip := map[string]bool{
		"posix": true, "right": true, "SECURITY": true, "leapseconds": true,
		"leap-seconds.list": true, "zone.tab": true, "zone1970.tab": true,
		"iso3166.tab": true, "tzdata.zi": true,
	}
	var zones []string
	var collect func(rel string, depth int)
	collect = func(rel string, depth int) {
		entries, err := os.ReadDir(filepath.Join(zoneinfoDir, rel))
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if rel == "" {
				if skip[name] || strings.Contains(name, ".") {
					continue
				}
				if r := rune(name[0]); !unicode.IsUpper(r) {
					continue // localtime, posixrules, and friends
				}
			}
			full := name
			if rel != "" {
				full = rel + "/" + name
			}
			if e.IsDir() {
				if depth < 3 {
					collect(full, depth+1)
				}
				continue
			}
			zones = append(zones, full)
		}
	}
	collect("", 1)
	if len(zones) == 0 {
		return commonTimezones
	}
	sort.Strings(zones)
	return zones
}

var commonTimezones = []string{
	"UTC",
	"America/Chicago", "America/Denver", "America/Los_Angeles", "America/New_York",
	"Asia/Shanghai", "Asia/Tokyo", "Australia/Sydney",
	"Europe/Amsterdam", "Europe/Berlin", "Europe/London", "Europe/Paris",
}

var commonLocales = []string{
	"en_US.UTF-8", "en_GB.UTF-8", "de_DE.UTF-8", "fr_FR.UTF-8", "es_ES.UTF-8",
	"it_IT.UTF-8", "pt_BR.UTF-8", "nl_NL.UTF-8", "pl_PL.UTF-8", "sv_SE.UTF-8",
	"da_DK.UTF-8", "fi_FI.UTF-8", "nb_NO.UTF-8", "cs_CZ.UTF-8", "ru_RU.UTF-8",
	"ja_JP.UTF-8", "ko_KR.UTF-8", "zh_CN.UTF-8",
}

var commonKeyboards = []string{
	"us", "us:intl", "gb", "de", "de:nodeadkeys", "fr", "es", "it", "pt",
	"br", "nl", "pl", "se", "no", "dk", "fi", "cz", "jp", "ru",
}
