package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/disk"
	"github.com/frostyard/firn/internal/platform"
	"github.com/frostyard/firn/internal/recipe"
)

func TestRunWizardRejectsNonUEFIBeforeCatalogOrChoices(t *testing.T) {
	rec, err := RunWizard(context.Background(), WizardOpts{UEFI: false})
	if !errors.Is(err, platform.ErrUEFIRequired) {
		t.Fatalf("RunWizard error = %v, want shared UEFI diagnostic", err)
	}
	if rec != nil {
		t.Fatalf("RunWizard returned recipe on unsupported machine: %+v", rec)
	}
}

func TestRunWizardRejectsInvalidProvidedCatalogBeforeUI(t *testing.T) {
	rec, err := RunWizard(context.Background(), WizardOpts{
		UEFI: true,
		Catalog: []CatalogEntry{{
			Family: recipe.FamilyBootc,
			Name:   "broken",
			Ref:    "ghcr.io/foo bar:latest",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "image.ref") {
		t.Fatalf("RunWizard error = %v, want canonical image.ref rejection", err)
	}
	if rec != nil {
		t.Fatalf("RunWizard returned recipe for invalid catalog: %+v", rec)
	}
}

func TestRunWizardRequiresSessionDirectoryBeforeUI(t *testing.T) {
	rec, err := RunWizard(context.Background(), WizardOpts{
		UEFI:    true,
		Catalog: []CatalogEntry{bootcEntry()},
	})
	if err == nil || !strings.Contains(err.Error(), "session directory is required") {
		t.Fatalf("RunWizard error = %v, want session-directory rejection", err)
	}
	if rec != nil {
		t.Fatalf("RunWizard returned recipe without session ownership: %+v", rec)
	}
}

func bootcEntry() CatalogEntry {
	return CatalogEntry{Family: recipe.FamilyBootc, Name: "snow", Description: "d", Ref: "ghcr.io/frostyard/snow:latest"}
}

func abEntry() CatalogEntry {
	return CatalogEntry{Family: recipe.FamilyAB, Name: "snow-ab", Description: "d", Product: "snow-ab"}
}

func baseChoices(entry CatalogEntry) wizardChoices {
	return wizardChoices{
		entry:      entry,
		disk:       "/dev/vdz",
		hostname:   "frost01",
		locale:     "en_US.UTF-8",
		timezone:   "America/Chicago",
		keyboard:   "us",
		encryption: "none",
	}
}

// TestAssembleRecipeMatrix is the spec-rule-5 guarantee: every
// family x encryption (x mok, x filesystem) combination the wizard can
// produce validates cleanly against recipe.Validate with the matching
// machine env.
func TestAssembleRecipeMatrix(t *testing.T) {
	withUser := func(c wizardChoices) wizardChoices {
		c.createUser = true
		c.username = "bjk"
		c.fullname = "Brian K"
		c.password = "hunter2"
		c.groups = []string{"sudo"}
		c.extraGroups = "libvirt, docker"
		c.userSSHKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKq7 test@host"
		return c
	}
	cases := []struct {
		name string
		c    wizardChoices
		env  recipe.Env
	}{
		{
			name: "bootc none xfs",
			c: func() wizardChoices {
				c := baseChoices(bootcEntry())
				c.filesystem = "xfs"
				return c
			}(),
		},
		{
			name: "bootc luks-passphrase ext4 user",
			c: func() wizardChoices {
				c := withUser(baseChoices(bootcEntry()))
				c.filesystem = "ext4"
				c.encryption = "luks-passphrase"
				c.passphrase = "s3cret"
				return c
			}(),
		},
		{
			name: "bootc tpm2-luks btrfs subvolumes",
			c: func() wizardChoices {
				c := baseChoices(bootcEntry())
				c.filesystem = "btrfs"
				c.btrfsSubvolumes = true
				c.encryption = "tpm2-luks"
				c.coreFlatpaks = true
				c.flatpaksRaw = "org.mozilla.firefox org.gnome.Maps"
				return c
			}(),
			env: recipe.Env{TPM: true},
		},
		{
			name: "bootc tpm2-luks-passphrase btrfs user",
			c: func() wizardChoices {
				c := withUser(baseChoices(bootcEntry()))
				c.filesystem = "btrfs"
				c.encryption = "tpm2-luks-passphrase"
				c.passphrase = "s3cret"
				c.rootSSHKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKq7 root@host"
				return c
			}(),
			env: recipe.Env{TPM: true},
		},
		{
			name: "bootc secure boot mok enroll",
			c: func() wizardChoices {
				c := baseChoices(bootcEntry())
				c.filesystem = "btrfs"
				c.mok = "enroll"
				c.mokPassword = "mok-pw"
				return c
			}(),
			env: recipe.Env{SecureBoot: true},
		},
		{
			name: "bootc secure boot mok skip",
			c: func() wizardChoices {
				c := baseChoices(bootcEntry())
				c.filesystem = "btrfs"
				c.mok = "skip"
				return c
			}(),
			env: recipe.Env{SecureBoot: true},
		},
		{
			name: "ab none ext4 no secure boot",
			c: func() wizardChoices {
				c := baseChoices(abEntry())
				c.varFilesystem = "ext4"
				return c
			}(),
		},
		{
			name: "ab luks btrfs subvolumes mok skip",
			c: func() wizardChoices {
				c := withUser(baseChoices(abEntry()))
				c.varFilesystem = "btrfs"
				c.varSubvolumes = true
				c.encryption = "luks"
				c.mok = "skip"
				return c
			}(),
			env: recipe.Env{SecureBoot: true},
		},
		{
			name: "ab tpm2-luks mok enroll",
			c: func() wizardChoices {
				c := withUser(baseChoices(abEntry()))
				c.varFilesystem = "ext4"
				c.encryption = "tpm2-luks"
				c.mok = "enroll"
				c.mokPassword = "mok-pw"
				return c
			}(),
			env: recipe.Env{SecureBoot: true, TPM: true},
		},
		{
			name: "ab none mok enroll",
			c: func() wizardChoices {
				c := baseChoices(abEntry())
				c.varFilesystem = "ext4"
				c.mok = "enroll"
				c.mokPassword = "mok-pw"
				return c
			}(),
			env: recipe.Env{SecureBoot: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := assembleRecipe(tc.c, t.TempDir())
			if err != nil {
				t.Fatalf("assembleRecipe: %v", err)
			}
			reviewed, err := marshalAssembled(rec)
			if err != nil {
				t.Fatalf("marshalAssembled: %v", err)
			}
			if issues := validateAssembled(reviewed, tc.env); len(issues) != 0 {
				t.Fatalf("assembled recipe has validation issues (wizard bug):\n%v\nTOML:\n%s", issues, reviewed)
			}
		})
	}
}

// TestAssembleRecipeRoundTrip checks that the rendered TOML re-parses
// into the identical recipe struct — the wizard's output and headless
// mode see the same install.
func TestAssembleRecipeRoundTrip(t *testing.T) {
	c := baseChoices(bootcEntry())
	c.filesystem = "btrfs"
	c.btrfsSubvolumes = true
	c.encryption = "luks-passphrase"
	c.passphrase = "s3cret"
	c.createUser = true
	c.username = "bjk"
	c.password = "hunter2"
	c.groups = []string{"sudo"}
	c.coreFlatpaks = true
	c.flatpaksRaw = "org.mozilla.firefox"

	rec, err := assembleRecipe(c, t.TempDir())
	if err != nil {
		t.Fatalf("assembleRecipe: %v", err)
	}
	reviewed, err := marshalAssembled(rec)
	if err != nil {
		t.Fatalf("marshalAssembled: %v", err)
	}
	l, err := recipe.Parse(reviewed)
	if err != nil {
		t.Fatalf("re-parsing rendered TOML: %v", err)
	}
	if !reflect.DeepEqual(&l.Recipe, rec) {
		t.Errorf("round trip changed the recipe:\nassembled: %+v\nreparsed:  %+v", rec, &l.Recipe)
	}
}

func TestAssembleRecipeCarriesCatalogCosignKey(t *testing.T) {
	key := filepath.Join(t.TempDir(), "cosign.pub")
	if err := os.WriteFile(key, []byte("public key"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := bootcEntry()
	entry.CosignPubKey = key
	c := baseChoices(entry)
	c.filesystem = "btrfs"
	rec, err := assembleRecipe(c, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if rec.Image.CosignPubKey != key {
		t.Fatalf("cosign key = %q, want catalog key %q", rec.Image.CosignPubKey, key)
	}
	reviewed, err := marshalAssembled(rec)
	if err != nil {
		t.Fatal(err)
	}
	if issues := validateAssembled(reviewed, recipe.Env{}); len(issues) != 0 {
		t.Fatalf("catalog trust recipe failed validation: %v", issues)
	}
}

func TestAssembleRecipeSecretFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	c := baseChoices(abEntry())
	c.varFilesystem = "ext4"
	c.encryption = "tpm2-luks"
	c.mok = "enroll"
	c.mokPassword = "mok-pw"
	c.createUser = true
	c.username = "bjk"
	c.password = "hunter2"

	rec, err := assembleRecipe(c, dir)
	if err != nil {
		t.Fatalf("assembleRecipe: %v", err)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat secrets dir: %v", err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("secrets dir mode = %o, want 700", got)
	}
	checkSecret := func(path, want string) {
		t.Helper()
		if path == "" {
			t.Fatal("secret file path not set in recipe")
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 600", path, got)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(data) != want {
			t.Errorf("%s content = %q, want %q", path, data, want)
		}
	}
	checkSecret(rec.Security.MokPasswordFile, "mok-pw")
	checkSecret(rec.System.User.PasswordFile, "hunter2")

	// bootc passphrase file too.
	cb := baseChoices(bootcEntry())
	cb.filesystem = "btrfs"
	cb.encryption = "luks-passphrase"
	cb.passphrase = "s3cret"
	recb, err := assembleRecipe(cb, dir)
	if err != nil {
		t.Fatalf("assembleRecipe (bootc): %v", err)
	}
	checkSecret(recb.Security.PassphraseFile, "s3cret")
}

func TestAssembleRecipeDefaultsABRecoveryKeyToSession(t *testing.T) {
	dir := t.TempDir()
	for _, encryption := range []string{"luks", "tpm2-luks"} {
		c := baseChoices(abEntry())
		c.varFilesystem = "ext4"
		c.encryption = encryption
		rec, err := assembleRecipe(c, dir)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(dir, "recovery-key"); rec.Security.RecoveryKeyOut != want {
			t.Fatalf("%s recovery_key_out = %q, want %q", encryption, rec.Security.RecoveryKeyOut, want)
		}
	}
}

func TestAssembleRecipeSessionsDoNotOverwriteSecrets(t *testing.T) {
	root := t.TempDir()
	assemble := func(name, secret string) *recipe.Recipe {
		t.Helper()
		c := baseChoices(bootcEntry())
		c.filesystem = "btrfs"
		c.encryption = "luks-passphrase"
		c.passphrase = secret + "-passphrase"
		c.mok = "enroll"
		c.mokPassword = secret + "-mok"
		c.createUser = true
		c.username = name
		c.password = secret + "-user"
		rec, err := assembleRecipe(c, filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	first := assemble("first", "alpha")
	second := assemble("second", "beta")
	checks := []struct {
		path, dir, want string
	}{
		{first.Security.PassphraseFile, "first", "alpha-passphrase"},
		{first.Security.MokPasswordFile, "first", "alpha-mok"},
		{first.System.User.PasswordFile, "first", "alpha-user"},
		{second.Security.PassphraseFile, "second", "beta-passphrase"},
		{second.Security.MokPasswordFile, "second", "beta-mok"},
		{second.System.User.PasswordFile, "second", "beta-user"},
	}
	for _, check := range checks {
		if filepath.Dir(check.path) != filepath.Join(root, check.dir) {
			t.Fatalf("secret path %q escaped session %q", check.path, check.dir)
		}
		data, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != check.want {
			t.Fatalf("secret %s = %q, want %q", check.path, data, check.want)
		}
	}
}

func TestCleanupSecretFilesRemovesAbandonedReviewSecrets(t *testing.T) {
	dir := t.TempDir()
	c := baseChoices(bootcEntry())
	c.filesystem = "btrfs"
	c.encryption = "luks-passphrase"
	c.passphrase = "abandoned-passphrase"
	c.mok = "enroll"
	c.mokPassword = "abandoned-mok"
	c.createUser = true
	c.username = "abandoned"
	c.password = "abandoned-user"
	if _, err := assembleRecipe(c, dir); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "catalog-note")
	if err := os.WriteFile(keep, []byte("not a secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupSecretFiles(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range generatedSecretNames {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("abandoned secret %q remains: %v", name, err)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("cleanup removed unrelated session file: %v", err)
	}
}

func TestAssembleRecipeWizardBugGuards(t *testing.T) {
	dir := t.TempDir()

	c := baseChoices(bootcEntry())
	c.filesystem = "btrfs"
	c.encryption = "luks-passphrase" // no passphrase provided
	if _, err := assembleRecipe(c, dir); err == nil {
		t.Error("passphrase mode without passphrase: want error, got nil")
	}

	c = baseChoices(abEntry())
	c.varFilesystem = "ext4"
	c.mok = "enroll" // no MOK password provided
	if _, err := assembleRecipe(c, dir); err == nil {
		t.Error("mok enroll without password: want error, got nil")
	}

	c = baseChoices(abEntry())
	c.varFilesystem = "ext4"
	c.createUser = true
	c.username = "bjk" // no password provided
	if _, err := assembleRecipe(c, dir); err == nil {
		t.Error("user without password: want error, got nil")
	}

	c = baseChoices(CatalogEntry{Family: "weird", Name: "x"})
	if _, err := assembleRecipe(c, dir); err == nil {
		t.Error("unknown family: want error, got nil")
	}

	c = baseChoices(bootcEntry())
	c.filesystem = "btrfs"
	c.encryption = "luks-passphrase"
	c.passphrase = " \t "
	if _, err := assembleRecipe(c, dir); err == nil {
		t.Error("whitespace-only passphrase: want error, got nil")
	}

	c = baseChoices(abEntry())
	c.varFilesystem = "ext4"
	c.mok = "enroll"
	c.mokPassword = " \t "
	if _, err := assembleRecipe(c, dir); err == nil {
		t.Error("whitespace-only MOK password: want error, got nil")
	}

	c = baseChoices(abEntry())
	c.varFilesystem = "ext4"
	c.createUser = true
	c.username = "bjk"
	c.password = " \t "
	if _, err := assembleRecipe(c, dir); err == nil {
		t.Error("whitespace-only user password: want error, got nil")
	}
}

func TestWizardInteractiveBranchContracts(t *testing.T) {
	t.Run("one-family catalogs skip the family form", func(t *testing.T) {
		for _, entry := range []CatalogEntry{bootcEntry(), abEntry()} {
			w := &wizard{catalog: []CatalogEntry{entry}}
			family, quit, err := w.familyPage(context.Background(), "")
			if err != nil || quit || family != entry.Family {
				t.Fatalf("familyPage(%s-only) = (%q, %v, %v)", entry.Family, family, quit, err)
			}
		}
	})

	t.Run("start-over family preference", func(t *testing.T) {
		families := []string{recipe.FamilyBootc, recipe.FamilyAB}
		if got := preferredFamily(families, recipe.FamilyAB); got != recipe.FamilyAB {
			t.Fatalf("preferredFamily() = %q, want prior A/B choice", got)
		}
		if got := preferredFamily(families, "missing"); got != recipe.FamilyBootc {
			t.Fatalf("preferredFamily() fallback = %q, want first represented family", got)
		}
	})

	t.Run("review start-over quit and install", func(t *testing.T) {
		for _, tc := range []struct {
			action          string
			startOver, quit bool
		}{
			{action: actionInstall},
			{action: actionStartOver, startOver: true},
			{action: actionQuit, quit: true},
		} {
			startOver, quit := reviewAction(tc.action)
			if startOver != tc.startOver || quit != tc.quit {
				t.Errorf("reviewAction(%q) = (%v,%v), want (%v,%v)", tc.action, startOver, quit, tc.startOver, tc.quit)
			}
		}
	})

	t.Run("disk refusal and rescan", func(t *testing.T) {
		reasons := map[string]string{"/dev/vda": "it is the disk this system is running from", "/dev/vdb": ""}
		if err := diskChoiceError(reasons, "/dev/vda"); err == nil || !strings.Contains(err.Error(), "cannot install") {
			t.Fatalf("refused disk error = %v", err)
		}
		if err := diskChoiceError(reasons, "/dev/vdb"); err != nil {
			t.Fatalf("acceptable disk rejected: %v", err)
		}
		if !isRescanChoice(rescanValue) || isRescanChoice("/dev/vdb") {
			t.Fatal("rescan sentinel branch is ambiguous with a real disk")
		}
	})

	t.Run("no user", func(t *testing.T) {
		c := baseChoices(abEntry())
		c.varFilesystem = "ext4"
		rec, err := assembleRecipe(c, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if rec.System.User != nil {
			t.Fatalf("declined user creation produced %+v", rec.System.User)
		}
	})

	t.Run("alternate filesystem with TPM encryption", func(t *testing.T) {
		c := baseChoices(bootcEntry())
		c.filesystem = "xfs"
		c.encryption = "tpm2-luks"
		rec, err := assembleRecipe(c, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if rec.Target.Filesystem != "xfs" || rec.Security.Encryption != "tpm2-luks" {
			t.Fatalf("alternate TPM recipe = %+v", rec)
		}
		reviewed, err := marshalAssembled(rec)
		if err != nil {
			t.Fatal(err)
		}
		if issues := validateAssembled(reviewed, recipe.Env{TPM: true}); len(issues) != 0 {
			t.Fatalf("alternate TPM recipe failed validation: %v", issues)
		}
	})

	t.Run("Secure Boot MOK enrollment", func(t *testing.T) {
		c := baseChoices(bootcEntry())
		c.filesystem = "btrfs"
		c.mok = "enroll"
		c.mokPassword = "one-time-password"
		rec, err := assembleRecipe(c, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if rec.Security.Mok != "enroll" || rec.Security.MokPasswordFile == "" {
			t.Fatalf("MOK enrollment recipe = %+v", rec.Security)
		}
		reviewed, err := marshalAssembled(rec)
		if err != nil {
			t.Fatal(err)
		}
		if issues := validateAssembled(reviewed, recipe.Env{SecureBoot: true}); len(issues) != 0 {
			t.Fatalf("MOK enrollment recipe failed validation: %v", issues)
		}
	})
}

func TestSecurityFormDefaultsMOKWhenSecureBootIsActive(t *testing.T) {
	for _, entry := range []CatalogEntry{abEntry(), bootcEntry()} {
		w := &wizard{
			opts: WizardOpts{Machine: recipe.Env{SecureBoot: true}},
			c:    wizardChoices{entry: entry},
		}
		w.securityForm()
		if w.c.mok != "enroll" {
			t.Errorf("%s Secure Boot MOK default = %q, want enroll", entry.Family, w.c.mok)
		}
	}

	// Preserve an explicit choice when rebuilding the form, and do not
	// populate an irrelevant A/B field when Secure Boot is inactive.
	w := &wizard{opts: WizardOpts{Machine: recipe.Env{SecureBoot: true}}, c: wizardChoices{entry: abEntry()}}
	w.c.mok = "skip"
	w.securityForm()
	if w.c.mok != "skip" {
		t.Fatalf("explicit MOK choice changed to %q", w.c.mok)
	}
	w.opts.Machine.SecureBoot = false
	w.c.mok = ""
	w.securityForm()
	if w.c.mok != "" {
		t.Fatalf("MOK defaulted without Secure Boot: %q", w.c.mok)
	}
}

func TestSystemFormDoesNotSeedOptionalImageDefaults(t *testing.T) {
	w := &wizard{}
	w.systemForm()
	if w.c.locale != "" || w.c.timezone != "" || w.c.keyboard != "" {
		t.Fatalf("system form seeded optional values: locale=%q timezone=%q keyboard=%q", w.c.locale, w.c.timezone, w.c.keyboard)
	}

	w.c.locale = "de_DE.UTF-8"
	w.c.timezone = "Europe/Berlin"
	w.c.keyboard = "de:nodeadkeys"
	w.systemForm()
	if w.c.locale != "de_DE.UTF-8" || w.c.timezone != "Europe/Berlin" || w.c.keyboard != "de:nodeadkeys" {
		t.Fatalf("system form changed explicit values: %+v", w.c)
	}
}

// TestReviewedTOMLScoping verifies that the canonical serializer emits only
// the recipe's family fields and never exposes wizard-entered secrets on the
// review page (spec rules 1 and 6).
func TestReviewedTOMLScoping(t *testing.T) {
	c := baseChoices(bootcEntry())
	c.filesystem = "btrfs"
	c.encryption = "luks-passphrase"
	c.passphrase = "sup3r-secret-passphrase"
	c.createUser = true
	c.username = "bjk"
	c.password = "sup3r-secret-password"
	rec, err := assembleRecipe(c, t.TempDir())
	if err != nil {
		t.Fatalf("assembleRecipe: %v", err)
	}
	reviewed, err := marshalAssembled(rec)
	if err != nil {
		t.Fatal(err)
	}
	tomlStr := string(reviewed)
	for _, banned := range []string{"product", "var_filesystem", "mok", "recovery_key_out", "sup3r-secret"} {
		if strings.Contains(tomlStr, banned) {
			t.Errorf("bootc TOML must not contain %q:\n%s", banned, tomlStr)
		}
	}

	ca := baseChoices(abEntry())
	ca.varFilesystem = "btrfs"
	ca.varSubvolumes = true
	reca, err := assembleRecipe(ca, t.TempDir())
	if err != nil {
		t.Fatalf("assembleRecipe (ab): %v", err)
	}
	reviewedA, err := marshalAssembled(reca)
	if err != nil {
		t.Fatal(err)
	}
	tomlA := string(reviewedA)
	for _, banned := range []string{"ref =", "\nfilesystem =", "btrfs_subvolumes", "bootloader", "passphrase"} {
		if strings.Contains(tomlA, banned) {
			t.Errorf("ab TOML must not contain %q:\n%s", banned, tomlA)
		}
	}
}

func TestSplitListAndMergeGroups(t *testing.T) {
	got := splitList("org.mozilla.firefox, org.gnome.Maps\ncom.spotify.Client org.mozilla.firefox")
	want := []string{"org.mozilla.firefox", "org.gnome.Maps", "com.spotify.Client"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("splitList = %v, want %v", got, want)
	}
	if splitList("  \n ") != nil {
		t.Error("splitList of blanks should be nil")
	}
	g := mergeGroups([]string{"sudo", "audio"}, "docker, sudo libvirt")
	wantG := []string{"sudo", "audio", "docker", "libvirt"}
	if !reflect.DeepEqual(g, wantG) {
		t.Errorf("mergeGroups = %v, want %v", g, wantG)
	}
}

func TestFormatDiskOption(t *testing.T) {
	d := disk.Device{Path: "/dev/sda", Size: 500107862016, Label: "data"}
	ok := formatDiskOption(d, "")
	for _, want := range []string{"/dev/sda", "465.8 GiB", "data"} {
		if !strings.Contains(ok, want) {
			t.Errorf("formatDiskOption = %q, want it to contain %q", ok, want)
		}
	}
	if strings.Contains(ok, "REFUSED") {
		t.Errorf("acceptable disk must not be marked refused: %q", ok)
	}
	refused := formatDiskOption(d, "it has a filesystem mounted at /home")
	if !strings.Contains(refused, "REFUSED: it has a filesystem mounted at /home") {
		t.Errorf("refusal reason not shown inline: %q", refused)
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		512:            "512 B",
		2048:           "2.0 KiB",
		1073741824:     "1.0 GiB",
		42949672960:    "40.0 GiB",
		2199023255552:  "2.0 TiB",
		500107862016:   "465.8 GiB",
		16106127360000: "14.6 TiB",
	}
	for in, want := range cases {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestTimezoneSuggestions(t *testing.T) {
	dir := t.TempDir()
	mk := func(path string) {
		t.Helper()
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("TZif"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("America/Chicago")
	mk("America/Argentina/Buenos_Aires")
	mk("Europe/Berlin")
	mk("UTC")
	mk("posix/UTC")
	mk("zone.tab")
	mk("localtime")

	got := timezoneSuggestions(dir)
	has := func(z string) bool {
		for _, g := range got {
			if g == z {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"America/Chicago", "America/Argentina/Buenos_Aires", "Europe/Berlin", "UTC"} {
		if !has(want) {
			t.Errorf("suggestions missing %q: %v", want, got)
		}
	}
	for _, banned := range []string{"posix/UTC", "zone.tab", "localtime"} {
		if has(banned) {
			t.Errorf("suggestions must not contain %q: %v", banned, got)
		}
	}

	if fall := timezoneSuggestions(""); len(fall) == 0 {
		t.Error("empty zoneinfo dir must fall back to the common list")
	}
}

// TestLiveValidators spot-checks the per-field validators the pages use
// so bad input is caught on the field; recipe.Validate remains the
// final authority via TestAssembleRecipeMatrix.
func TestLiveValidators(t *testing.T) {
	if err := requireNonEmpty("a password")(" \t "); err == nil {
		t.Error("whitespace-only secret accepted")
	}
	if err := validateHostnameInput("frost01.example.org"); err != nil {
		t.Errorf("valid hostname rejected: %v", err)
	}
	for _, bad := range []string{"", "-x", "x-", "a..b", strings.Repeat("a", 64)} {
		if validateHostnameInput(bad) == nil {
			t.Errorf("hostname %q accepted, want rejection", bad)
		}
	}
	if err := validateUsernameInput("bjk"); err != nil {
		t.Errorf("valid username rejected: %v", err)
	}
	for _, bad := range []string{"", "1abc", "Upper", strings.Repeat("a", 33)} {
		if validateUsernameInput(bad) == nil {
			t.Errorf("username %q accepted, want rejection", bad)
		}
	}
	for _, valid := range []string{"", "Ada Lovelace", "Zoë 雪"} {
		if err := validateFullnameInput(valid); err != nil {
			t.Errorf("full name %q rejected: %v", valid, err)
		}
	}
	for _, bad := range []string{"Bad:Name", "Bad\nName", "Bad\rName"} {
		if validateFullnameInput(bad) == nil {
			t.Errorf("full name %q accepted, want rejection", bad)
		}
	}
	if err := validateLocaleInput("en_US.UTF-8"); err != nil {
		t.Errorf("valid locale rejected: %v", err)
	}
	if validateLocaleInput("english") == nil {
		t.Error("locale \"english\" accepted, want rejection")
	}
	if err := validateKeyboardInput("de:nodeadkeys"); err != nil {
		t.Errorf("valid keyboard rejected: %v", err)
	}
	if validateKeyboardInput("DE Layout") == nil {
		t.Error("keyboard \"DE Layout\" accepted, want rejection")
	}
	if err := validateSSHKeyInput("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKq7 me@host"); err != nil {
		t.Errorf("valid ssh key rejected: %v", err)
	}
	if validateSSHKeyInput("not a key") == nil {
		t.Error("garbage ssh key accepted, want rejection")
	}
	if validateGroupListInput("sudo, docker") != nil {
		t.Error("valid group list rejected")
	}
	if validateGroupListInput("Bad Group") == nil {
		t.Error("invalid group list accepted")
	}

	tzdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tzdir, "America"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tzdir, "America", "Chicago"), []byte("TZif"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := validateTimezoneInput(tzdir)
	if err := v("America/Chicago"); err != nil {
		t.Errorf("existing timezone rejected: %v", err)
	}
	if v("America/Nowhere") == nil {
		t.Error("nonexistent timezone accepted with zoneinfo dir present")
	}
	if v("") != nil {
		t.Error("empty timezone must be accepted (optional field)")
	}
}

func TestRenderIssues(t *testing.T) {
	if renderIssues(nil) != "" {
		t.Error("no issues must render empty")
	}
	out := renderIssues([]recipe.Issue{{Code: "enum", Field: "target.filesystem", Message: "bad"}})
	if !strings.Contains(out, "target.filesystem") || !strings.Contains(out, "wizard bug") {
		t.Errorf("issue rendering missing detail: %q", out)
	}
}
