package installer

// The wizard is a recipe generator (ADR-0005, ADR-0021): it collects choices
// page by page and assembles a recipe.Recipe, exactly as the TUI does. The
// review page shows the marshaled bytes, and the install persists and runs
// those same bytes (recipe-schema rule 5).

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/frostyard/firn/internal/catalog"
	"github.com/frostyard/firn/internal/recipe"
)

// choices is everything the pages collect, kept apart from recipe.Recipe so
// assembly (choices -> recipe + secret files) is a pure function tests drive
// directly.
type choices struct {
	entry catalog.Entry

	// Advanced image options. When advanced is false these are ignored, so
	// closing the section restores catalog/default behavior without losing
	// the answers.
	advanced   bool
	targetRef  string
	bootloader string

	disk            string
	filesystem      string
	btrfsSubvolumes bool

	encryption        string
	passphrase        string // secret; never enters the recipe inline
	passphraseConfirm string
	mok               string // "enroll" or "skip"; used only under Secure Boot
	mokPassword       string // secret
	mokConfirm        string

	hostname string
	// hostnameAuto marks a hostname the wizard filled in from the image
	// name; choosing another image refills it until the user edits it.
	hostnameAuto bool
	locale       string
	timezone     string
	keyboard     string
	rootSSHKey   string

	createUser bool
	fullname   string
	username   string
	// usernameAuto marks a username derived from the full name.
	usernameAuto    bool
	password        string // secret
	passwordConfirm string
	groups          []groupChoice
	extraGroups     string
	userSSHKey      string

	coreFlatpaks bool
	// coreRef is the image ref whose initial core-flatpaks selection has
	// been applied (ADR-0019): once per chosen image.
	coreRef     string
	flatpaksRaw string
}

// groupChoice is one offered supplementary group and whether it is chosen.
type groupChoice struct {
	catalog.Group
	on bool
}

// Secret file names inside the session directory.
const (
	passphraseSecretName = "passphrase"
	mokSecretName        = "mok-password"
	userSecretName       = "user-password"
)

var generatedSecretNames = []string{passphraseSecretName, mokSecretName, userSecretName}

// defaultChoices returns the wizard's initial selections: the TUI's
// documented ones (encryption none, MOK enroll, a user with the image's
// default groups, btrfs without subvolumes, systemd-boot), plus prefills of
// optional fields from the running session (ADR-0021).
func defaultChoices(entry catalog.Entry, s sessionDefaults) choices {
	c := choices{
		bootloader: "systemd",
		filesystem: "btrfs",
		encryption: "none",
		mok:        "enroll",
		createUser: true,
		locale:     s.locale,
		timezone:   s.timezone,
		keyboard:   s.keyboard,
	}
	c.setEntry(entry)
	return c
}

// setEntry selects an image. A different image refills what follows from
// it: the automatic hostname, the default groups, and the core-flatpaks
// initial selection (applied when its preview arrives).
func (c *choices) setEntry(entry catalog.Entry) {
	changed := entry.Name != c.entry.Name || entry.Ref != c.entry.Ref
	c.entry = entry
	if !changed {
		return
	}
	if c.hostname == "" || c.hostnameAuto {
		c.hostname = sanitizeHostname(entry.Name)
		c.hostnameAuto = true
	}
	c.groups = groupChoices(entry)
}

// groupChoices offers catalog.Groups plus any group the entry preselects
// that the shared list lacks (an override catalog may name one), with the
// entry's defaults chosen, or sudo when it declares none.
func groupChoices(entry catalog.Entry) []groupChoice {
	defaults := entry.DefaultGroups
	if len(defaults) == 0 {
		defaults = []string{"sudo"}
	}
	chosen := make(map[string]bool, len(defaults))
	for _, g := range defaults {
		chosen[g] = true
	}
	var out []groupChoice
	offered := make(map[string]bool)
	for _, g := range catalog.Groups() {
		out = append(out, groupChoice{Group: g, on: chosen[g.Name]})
		offered[g.Name] = true
	}
	for _, g := range defaults {
		if !offered[g] {
			out = append(out, groupChoice{Group: catalog.Group{Name: g}, on: true})
			offered[g] = true
		}
	}
	return out
}

// selectedGroups returns the chosen groups merged with the free-form extras,
// deduplicated in order.
func (c *choices) selectedGroups() []string {
	var names []string
	for _, g := range c.groups {
		if g.on {
			names = append(names, g.Name)
		}
	}
	return splitList(strings.Join(names, ",") + "," + c.extraGroups)
}

// setFullname updates the full name and, until the user edits the username,
// derives the username from it.
func (c *choices) setFullname(name string) {
	c.fullname = name
	if c.username == "" || c.usernameAuto {
		c.username = deriveUsername(name)
		c.usernameAuto = c.username != ""
	}
}

// secretWriter turns a secret into the path the recipe references. The
// install writes real 0600 files; live validation and the review page use
// paths only.
type secretWriter func(name, content string) (string, error)

// assemble turns choices into a recipe, carrying every secret only as a
// *_file path from write (recipe-schema rule 6).
func assemble(c choices, secureBoot bool, write secretWriter) (*recipe.Recipe, error) {
	r := &recipe.Recipe{Version: recipe.SchemaVersion}
	if c.entry.Family != recipe.FamilyBootc {
		return nil, fmt.Errorf("catalog entry %q has unknown family %q", c.entry.Name, c.entry.Family)
	}
	r.Image.Family = c.entry.Family
	r.Image.Ref = c.entry.Ref
	r.Image.CosignPubKey = c.entry.CosignPubKey
	r.Target.Disk = c.disk
	r.Target.Filesystem = c.filesystem
	r.Target.BtrfsSubvolumes = c.btrfsSubvolumes && c.filesystem == "btrfs"
	if c.advanced {
		r.Image.TargetRef = strings.TrimSpace(c.targetRef)
		r.Target.Bootloader = c.bootloader
	}

	r.Security.Encryption = c.encryption
	if needsPassphrase(c.encryption) {
		p, err := write(passphraseSecretName, c.passphrase)
		if err != nil {
			return nil, err
		}
		r.Security.PassphraseFile = p
	}
	// MOK enrollment uses the one-time MokManager password (ADR-0014); the
	// fields exist only while Secure Boot is active.
	if secureBoot {
		r.Security.Mok = c.mok
		if c.mok == "enroll" {
			p, err := write(mokSecretName, c.mokPassword)
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
	// Inline SSH keys, as in the TUI: users paste keys instead of naming
	// paths in the ephemeral installer environment.
	r.System.RootSSHAuthorizedKey = strings.TrimSpace(c.rootSSHKey)
	r.System.CoreFlatpaks = c.coreFlatpaks
	r.System.Flatpaks = splitList(c.flatpaksRaw)

	if c.createUser {
		u := &recipe.User{
			Name:             strings.TrimSpace(c.username),
			Fullname:         strings.TrimSpace(c.fullname),
			Groups:           c.selectedGroups(),
			SSHAuthorizedKey: strings.TrimSpace(c.userSSHKey),
		}
		// The wizard writes its own private password file; password_hash
		// stays a headless-only automation surface.
		p, err := write(userSecretName, c.password)
		if err != nil {
			return nil, err
		}
		u.PasswordFile = p
		r.System.User = u
	}
	return r, nil
}

// requireSecrets reports a wizard bug when an assembled recipe would
// reference an empty secret; the pages refuse to advance without them.
func requireSecrets(c choices, secureBoot bool) error {
	if needsPassphrase(c.encryption) && strings.TrimSpace(c.passphrase) == "" {
		return fmt.Errorf("encryption %q selected without a passphrase (wizard bug)", c.encryption)
	}
	if secureBoot && c.mok == "enroll" && strings.TrimSpace(c.mokPassword) == "" {
		return errors.New("MOK enrollment selected without a MOK password (wizard bug)")
	}
	if c.createUser && strings.TrimSpace(c.password) == "" {
		return errors.New("user creation selected without a password (wizard bug)")
	}
	return nil
}

// pathsIn returns a writer that names secret files in dir without writing
// them, for the review page.
func pathsIn(dir string) secretWriter {
	return func(name, _ string) (string, error) { return filepath.Join(dir, name), nil }
}

// filesIn returns a writer that writes each secret to dir/name with 0600
// permissions (dir created 0700).
func filesIn(dir string) secretWriter {
	return func(name, content string) (string, error) {
		return writeSecretFile(dir, name, content)
	}
}

func writeSecretFile(dir, name, content string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating secrets dir: %w", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("writing secret file: %w", err)
	}
	// WriteFile keeps existing permissions on overwrite; pin them.
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("securing secret file: %w", err)
	}
	return path, nil
}

func cleanupSecretFiles(dir string) error {
	var errs []error
	for _, name := range generatedSecretNames {
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("removing abandoned secret %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

// reviewBytes is the wizard's sole serialization boundary: the bytes the
// review page shows are the bytes persisted and installed.
func reviewBytes(c choices, secureBoot bool, sessionDir string) ([]byte, error) {
	r, err := assemble(c, secureBoot, pathsIn(sessionDir))
	if err != nil {
		return nil, err
	}
	data, err := recipe.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal assembled recipe (wizard bug): %w", err)
	}
	return data, nil
}

// persist writes the secrets and the reviewed recipe into sessionDir, then
// reloads and validates the written file — what the printed headless
// command will consume. Any failure is a wizard bug, never a user error.
func persist(c choices, env recipe.Env, sessionDir string, reviewed []byte) (string, *recipe.Loaded, error) {
	if err := requireSecrets(c, env.SecureBoot); err != nil {
		return "", nil, err
	}
	r, err := assemble(c, env.SecureBoot, filesIn(sessionDir))
	if err != nil {
		return "", nil, err
	}
	written, err := recipe.Marshal(r)
	if err != nil {
		return "", nil, fmt.Errorf("cannot marshal assembled recipe (wizard bug): %w", err)
	}
	if !bytes.Equal(written, reviewed) {
		return "", nil, errors.New("the recipe changed after review (wizard bug)")
	}
	path := filepath.Join(sessionDir, "recipe.toml")
	if err := os.WriteFile(path, reviewed, 0o600); err != nil {
		return "", nil, err
	}
	l, err := recipe.Load(path)
	if err != nil {
		return "", nil, fmt.Errorf("the wizard wrote an unloadable recipe (wizard bug): %w", err)
	}
	if issues := recipe.Validate(l, env); len(issues) > 0 {
		msgs := make([]string, len(issues))
		for i, is := range issues {
			msgs[i] = is.Error()
		}
		return "", nil, fmt.Errorf("the wizard generated an invalid recipe (wizard bug): %s", strings.Join(msgs, "; "))
	}
	return path, l, nil
}

// needsPassphrase reports whether an encryption mode requires a
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

// deriveUsername suggests a POSIX username from a full name: the first word,
// lowercased, keeping only [a-z0-9_-] and starting with a letter or _.
func deriveUsername(fullname string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(fullname), " ")
	var b strings.Builder
	for _, r := range strings.ToLower(foldASCII(first)) {
		switch {
		case r >= 'a' && r <= 'z', r == '_':
			b.WriteRune(r)
		case (r >= '0' && r <= '9') || r == '-':
			if b.Len() > 0 {
				b.WriteRune(r)
			}
		}
		if b.Len() == 32 {
			break
		}
	}
	return b.String()
}

// sanitizeHostname turns an image name into an RFC 1123 host label.
func sanitizeHostname(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if b.Len() > 0 {
				b.WriteRune('-')
			}
		}
	}
	h := strings.Trim(b.String(), "-")
	if len(h) > 63 {
		h = strings.Trim(h[:63], "-")
	}
	if h == "" {
		return "snosi"
	}
	return h
}

// foldASCII maps common Latin letters with diacritics to their base letter,
// so "Zoë" suggests "zoe".
func foldASCII(s string) string {
	return asciiFolder.Replace(s)
}

var asciiFolder = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "å", "a", "æ", "ae",
	"ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "ñ", "n",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o", "ø", "o", "œ", "oe",
	"ß", "ss", "ú", "u", "ù", "u", "û", "u", "ü", "u", "ý", "y", "ÿ", "y",
	"Á", "a", "À", "a", "Â", "a", "Ä", "a", "Ã", "a", "Å", "a", "Æ", "ae",
	"Ç", "c", "É", "e", "È", "e", "Ê", "e", "Ë", "e",
	"Í", "i", "Ì", "i", "Î", "i", "Ï", "i", "Ñ", "n",
	"Ó", "o", "Ò", "o", "Ô", "o", "Ö", "o", "Õ", "o", "Ø", "o", "Œ", "oe",
	"Ú", "u", "Ù", "u", "Û", "u", "Ü", "u", "Ý", "y",
)
