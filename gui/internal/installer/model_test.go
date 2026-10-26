package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/catalog"
	"github.com/frostyard/firn/internal/recipe"
)

func testEntry() catalog.Entry {
	return catalog.Entry{Family: recipe.FamilyBootc, Name: "snow", Description: "GNOME desktop", Ref: "ghcr.io/frostyard/snow:latest", DefaultGroups: []string{"sudo", "video"}}
}

func completeChoices(t *testing.T) choices {
	t.Helper()
	c := defaultChoices(testEntry(), sessionDefaults{})
	c.disk = "/dev/vda"
	c.password, c.passwordConfirm = "pw", "pw"
	c.setFullname("Zoë Ångström")
	return c
}

func TestDefaultChoicesMirrorTUIInitialSelections(t *testing.T) {
	c := defaultChoices(testEntry(), sessionDefaults{locale: "de_DE.UTF-8", timezone: "Europe/Berlin", keyboard: "de"})
	if c.encryption != "none" || c.mok != "enroll" || c.filesystem != "btrfs" || c.btrfsSubvolumes || c.bootloader != "systemd" || !c.createUser {
		t.Fatalf("initial selections differ from the TUI's: %+v", c)
	}
	if c.hostname != "snow" || !c.hostnameAuto {
		t.Errorf("hostname prefill = %q (auto %v), want snow", c.hostname, c.hostnameAuto)
	}
	if c.locale != "de_DE.UTF-8" || c.timezone != "Europe/Berlin" || c.keyboard != "de" {
		t.Errorf("session prefills not applied: %+v", c)
	}
	if got := c.selectedGroups(); !slices.Equal(got, []string{"sudo", "video"}) {
		t.Errorf("groups = %v, want the entry's defaults", got)
	}
}

func TestSetEntryRefillsOnlyAutomaticValues(t *testing.T) {
	c := defaultChoices(testEntry(), sessionDefaults{})
	floe := catalog.Entry{Family: recipe.FamilyBootc, Name: "floe", Ref: "ghcr.io/frostyard/floe:latest"}
	c.setEntry(floe)
	if c.hostname != "floe" {
		t.Errorf("automatic hostname not refilled: %q", c.hostname)
	}
	if got := c.selectedGroups(); !slices.Equal(got, []string{"sudo"}) {
		t.Errorf("an entry without defaults should choose sudo, got %v", got)
	}
	c.hostname, c.hostnameAuto = "mine", false
	c.setEntry(testEntry())
	if c.hostname != "mine" {
		t.Errorf("an edited hostname was replaced: %q", c.hostname)
	}
}

func TestGroupChoicesOfferOverrideGroups(t *testing.T) {
	e := testEntry()
	e.DefaultGroups = []string{"sudo", "wheel"}
	var names []string
	for _, g := range groupChoices(e) {
		if g.on {
			names = append(names, g.Name)
		}
	}
	if !slices.Equal(names, []string{"sudo", "wheel"}) {
		t.Errorf("chosen = %v, want sudo and the override's wheel", names)
	}
}

func TestDeriveUsername(t *testing.T) {
	for in, want := range map[string]string{
		"Zoë Ångström":    "zoe",
		"Ada Lovelace":    "ada",
		"  Grace  Hopper": "grace",
		"42 Douglas":      "",
		"O'Brien":         "obrien",
		"":                "",
	} {
		if got := deriveUsername(in); got != want {
			t.Errorf("deriveUsername(%q) = %q, want %q", in, got, want)
		}
	}
	c := choices{}
	c.setFullname("Ada Lovelace")
	c.username, c.usernameAuto = "countess", false
	c.setFullname("Ada King")
	if c.username != "countess" {
		t.Errorf("an edited username was replaced: %q", c.username)
	}
}

func TestSanitizeHostname(t *testing.T) {
	for in, want := range map[string]string{"snow": "snow", "Snow Field": "snow-field", "__": "snosi", "a.b": "a-b"} {
		if got := sanitizeHostname(in); got != want {
			t.Errorf("sanitizeHostname(%q) = %q, want %q", in, got, want)
		}
	}
}

// The wizard must never produce a recipe the schema rejects (recipe-schema
// rule 5): every assembly of complete choices validates, with or without
// Secure Boot.
func TestAssembledRecipesValidate(t *testing.T) {
	dir := t.TempDir()
	for _, sb := range []bool{false, true} {
		for _, enc := range []string{"none", "luks-passphrase", "tpm2-luks", "tpm2-luks-passphrase"} {
			c := completeChoices(t)
			c.encryption = enc
			c.passphrase, c.passphraseConfirm = "secret", "secret"
			c.mokPassword, c.mokConfirm = "mok", "mok"
			env := recipe.Env{SecureBoot: sb, TPM: true}
			reviewed, err := reviewBytes(c, sb, dir)
			if err != nil {
				t.Fatal(err)
			}
			path, l, err := persist(c, env, dir, reviewed)
			if err != nil {
				t.Fatalf("sb=%v enc=%s: %v", sb, enc, err)
			}
			written, _ := os.ReadFile(path)
			if !bytes.Equal(written, reviewed) {
				t.Errorf("persisted bytes differ from the reviewed ones")
			}
			if strings.Contains(string(written), "secret") || strings.Contains(string(written), `"pw"`) {
				t.Errorf("a secret was serialized inline:\n%s", written)
			}
			if l.Recipe.Security.Encryption != enc {
				t.Errorf("encryption = %q, want %q", l.Recipe.Security.Encryption, enc)
			}
			if sb != (l.Recipe.Security.Mok == "enroll") {
				t.Errorf("sb=%v: mok = %q", sb, l.Recipe.Security.Mok)
			}
		}
	}
}

func TestSecretFilesArePrivate(t *testing.T) {
	dir := t.TempDir()
	c := completeChoices(t)
	reviewed, _ := reviewBytes(c, false, dir)
	if _, _, err := persist(c, recipe.Env{}, dir, reviewed); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{userSecretName, "recipe.toml"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", name, fi.Mode().Perm())
		}
	}
	if err := cleanupSecretFiles(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, userSecretName)); !os.IsNotExist(err) {
		t.Errorf("cleanup left the user password behind: %v", err)
	}
}

func TestPersistRefusesChangedOrIncompleteChoices(t *testing.T) {
	dir := t.TempDir()
	c := completeChoices(t)
	reviewed, _ := reviewBytes(c, false, dir)
	c.hostname = "changed"
	if _, _, err := persist(c, recipe.Env{}, dir, reviewed); err == nil || !strings.Contains(err.Error(), "changed after review") {
		t.Errorf("persist after a change: %v, want a wizard-bug refusal", err)
	}
	c = completeChoices(t)
	c.password = ""
	if _, _, err := persist(c, recipe.Env{}, dir, reviewed); err == nil {
		t.Error("persist without a password must fail")
	}
}

func TestAdvancedOptionsApplyOnlyWhenOn(t *testing.T) {
	c := completeChoices(t)
	c.targetRef, c.bootloader = "ghcr.io/me/snow:stable", "grub2"
	r, _ := assemble(c, false, pathsIn(t.TempDir()))
	if r.Image.TargetRef != "" || r.Target.Bootloader != "" {
		t.Errorf("advanced values leaked while off: %+v %+v", r.Image, r.Target)
	}
	c.advanced = true
	r, _ = assemble(c, false, pathsIn(t.TempDir()))
	if r.Image.TargetRef != "ghcr.io/me/snow:stable" || r.Target.Bootloader != "grub2" {
		t.Errorf("advanced values missing while on: %+v %+v", r.Image, r.Target)
	}
}

func TestSplitList(t *testing.T) {
	got := splitList("a, b\nc  a,,d")
	if !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("splitList = %v", got)
	}
	if splitList("  ") != nil {
		t.Error("blank input should yield nil")
	}
}
