package recipe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSecret creates a 0600 file for *_file fields.
func writeSecret(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// swapLine replaces the whole line beginning with prefix.
func swapLine(base, prefix, replacement string) string {
	start := strings.Index(base, "\n"+prefix) + 1
	end := strings.Index(base[start:], "\n") + start
	return base[:start] + replacement + base[end:]
}

func validBootc() string {
	return `
version = 1

[image]
family = "bootc"
ref = "ghcr.io/frostyard/snow:latest"

[target]
disk = "/dev/vda"
filesystem = "btrfs"
btrfs_subvolumes = true

[security]
encryption = "none"
mok = "skip"

[system]
hostname = "frost01"
`
}

func validBootcWithUser(t *testing.T) string {
	mok := writeSecret(t, "mok-pw", "hunter2")
	pw := writeSecret(t, "user-pw", "hunter2")
	return `
version = 1

[image]
family = "bootc"
ref = "ghcr.io/frostyard/snow:latest"

[target]
disk = "/dev/nvme0n1"
filesystem = "ext4"

[security]
encryption = "tpm2-luks"
mok = "enroll"
mok_password_file = "` + mok + `"

[system]
hostname = "frost01"

[system.user]
name = "bjk"
password_file = "` + pw + `"
groups = ["wheel"]
`
}

var fullEnv = Env{SecureBoot: true, TPM: true}

func mustParse(t *testing.T, src string) *Loaded {
	t.Helper()
	l, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return l
}

func TestSpecExamplesValidate(t *testing.T) {
	for name, src := range map[string]string{"bootc": validBootc(), "bootc user": validBootcWithUser(t)} {
		if issues := Validate(mustParse(t, src), fullEnv); len(issues) != 0 {
			t.Errorf("%s: expected valid, got %v", name, issues)
		}
	}
}

func TestUnknownFieldsRejectedAtParse(t *testing.T) {
	if _, err := Parse([]byte(validBootc() + "\nsurprise = true\n")); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("expected unknown-field parse error, got %v", err)
	}
}

// mutate applies a line-level edit to a valid recipe source.
func replace(src, old, new string) string { return strings.Replace(src, old, new, 1) }

func TestValidateRejections(t *testing.T) {
	worldReadable := filepath.Join(t.TempDir(), "leaky")
	if err := os.WriteFile(worldReadable, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	cosignKey := filepath.Join(t.TempDir(), "cosign.pub")
	if err := os.WriteFile(cosignKey, []byte("public key"), 0o644); err != nil {
		t.Fatal(err)
	}
	badSSHKeyFile := filepath.Join(t.TempDir(), "authorized_keys")
	if err := os.WriteFile(badSSHKeyFile, []byte("ssh-ed25519 AAAA first@host\nnot a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		src  string
		env  Env
		code string
	}{
		{"unsupported version", replace(validBootc(), "version = 1", "version = 3"), fullEnv, CodeVersion},
		{"missing family", replace(validBootc(), `family = "bootc"`, ""), fullEnv, CodeRequired},
		{"bad family", replace(validBootc(), `family = "bootc"`, `family = "ostree"`), fullEnv, CodeEnum},
		{"missing ref", replace(validBootc(), `ref = "ghcr.io/frostyard/snow:latest"`, ""), fullEnv, CodeRequired},
		{"bad target ref", replace(validBootc(), `ref = "ghcr.io/frostyard/snow:latest"`, `ref = "ghcr.io/frostyard/snow:latest"`+"\ntarget_ref = \"ghcr.io/frostyard/snow: bad\""), fullEnv, CodeEnum},
		{"cosign key with local ref", replace(validBootc(), `ref = "ghcr.io/frostyard/snow:latest"`, `ref = "containers-storage:ghcr.io/frostyard/snow:latest"`+"\ncosign_pub_key = \""+cosignKey+"\""), fullEnv, CodeEnum},
		{"missing disk", replace(validBootc(), `disk = "/dev/vda"`, ""), fullEnv, CodeRequired},
		{"partition disk", replace(validBootc(), `disk = "/dev/vda"`, `disk = "vda"`), fullEnv, CodeDisk},
		{"missing filesystem", replace(validBootc(), `filesystem = "btrfs"`, ""), fullEnv, CodeRequired},
		{"bad filesystem", replace(validBootc(), `filesystem = "btrfs"`, `filesystem = "fat12"`), fullEnv, CodeEnum},
		{"unsupported zfs filesystem", replace(validBootc(), `filesystem = "btrfs"`, `filesystem = "zfs"`), fullEnv, CodeEnum},
		{"subvolumes without btrfs", replace(validBootc(), `filesystem = "btrfs"`, `filesystem = "ext4"`), fullEnv, CodeSubvolumes},
		{"bad bootloader", replace(validBootc(), `filesystem = "btrfs"`, `filesystem = "btrfs"`+"\nbootloader = \"uboot\""), fullEnv, CodeEnum},
		{"missing encryption", replace(validBootc(), `encryption = "none"`, ""), fullEnv, CodeRequired},
		{"bad encryption bootc", replace(validBootc(), `encryption = "none"`, `encryption = "luks"`), fullEnv, CodeEnum},
		{"tpm mode without tpm", validBootcWithUser(t), Env{SecureBoot: true, TPM: false}, CodeTPM},
		{"passphrase missing", replace(validBootc(), `encryption = "none"`, `encryption = "luks-passphrase"`), fullEnv, CodePassphrase},
		{"passphrase both", replace(validBootc(), `encryption = "none"`, `encryption = "luks-passphrase"`+"\npassphrase = \"x\"\npassphrase_file = \"/nope\""), fullEnv, CodePassphrase},
		{"passphrase on none", replace(validBootc(), `encryption = "none"`, `encryption = "none"`+"\npassphrase = \"x\""), fullEnv, CodePassphrase},
		{"mok missing under sb", replace(validBootcWithUser(t), `mok = "enroll"`, ""), fullEnv, CodeRequired},
		{"mok without sb", validBootcWithUser(t), Env{SecureBoot: false, TPM: true}, CodeMok},
		{"mok skip with password", replace(validBootcWithUser(t), `mok = "enroll"`, `mok = "skip"`), fullEnv, CodeMok},
		{"world-readable secret", swapLine(validBootcWithUser(t), "mok_password_file = ", `mok_password_file = "`+worldReadable+`"`), fullEnv, CodeSecretFile},
		{"missing hostname", replace(validBootc(), `hostname = "frost01"`, ""), fullEnv, CodeRequired},
		{"bad hostname", replace(validBootc(), `hostname = "frost01"`, `hostname = "-frost"`), fullEnv, CodeHostname},
		{"bad locale", replace(validBootc(), `hostname = "frost01"`, `hostname = "frost01"`+"\nlocale = \"american\""), fullEnv, CodeLocale},
		{"bad timezone", replace(validBootc(), `hostname = "frost01"`, `hostname = "frost01"`+"\ntimezone = \"/etc/shadow\""), fullEnv, CodeTimezone},
		{"bad keyboard", replace(validBootc(), `hostname = "frost01"`, `hostname = "frost01"`+"\nkeyboard = \"us:intl:pc105:extra\""), fullEnv, CodeKeyboard},
		{"bad root ssh key", replace(validBootc(), `hostname = "frost01"`, `hostname = "frost01"`+"\nroot_ssh_authorized_key = \"not a key\""), fullEnv, CodeSSHKey},
		{"bad second root ssh key", replace(validBootc(), `hostname = "frost01"`, `hostname = "frost01"`+"\nroot_ssh_authorized_key = \"ssh-ed25519 AAAA first@host\\nnot a key\""), fullEnv, CodeSSHKey},
		{"bad root ssh key file content", replace(validBootc(), `hostname = "frost01"`, `hostname = "frost01"`+"\nroot_ssh_authorized_key_file = \""+badSSHKeyFile+"\""), fullEnv, CodeSSHKey},
		{"user without name", validBootc() + "\n[system.user]\nfullname = \"X\"\n", fullEnv, CodeRequired},
		{"bad username", replace(validBootcWithUser(t), `name = "bjk"`, `name = "9lives"`), fullEnv, CodeUsername},
		{"fullname with colon", replace(validBootcWithUser(t), `name = "bjk"`, `name = "bjk"`+"\nfullname = \"Bad:Name\""), fullEnv, CodeFullname},
		{"fullname with newline", replace(validBootcWithUser(t), `name = "bjk"`, `name = "bjk"`+"\nfullname = \"Bad\\nName\""), fullEnv, CodeFullname},
		{"password both", replace(validBootcWithUser(t), `groups = ["wheel"]`, `groups = ["wheel"]`+"\npassword_hash = \"$6$x\""), fullEnv, CodeMutex},
		{"bad hash", swapLine(validBootcWithUser(t), "password_file = ", `password_hash = "plaintext"`), fullEnv, CodeHash},
		{"bad group", replace(validBootcWithUser(t), `groups = ["wheel"]`, `groups = ["whe el"]`), fullEnv, CodeGroup},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := Validate(mustParse(t, tc.src), tc.env)
			for _, is := range issues {
				if is.Code == tc.code {
					return
				}
			}
			t.Errorf("expected an issue with code %q, got %v", tc.code, issues)
		})
	}
}

func TestValidateSSHAuthorizedKeys(t *testing.T) {
	valid := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKq7 first@host\n" +
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7 second@host\n"
	if err := ValidateSSHAuthorizedKeys(valid); err != nil {
		t.Fatalf("valid multiline keys rejected: %v", err)
	}
	src := replace(validBootc(), `hostname = "frost01"`, `hostname = "frost01"`+
		"\nroot_ssh_authorized_key = \"ssh-ed25519 AAAA first@host\\nssh-rsa BBBB second@host\"")
	if issues := Validate(mustParse(t, src), fullEnv); len(issues) != 0 {
		t.Fatalf("valid multiline recipe keys rejected: %v", issues)
	}
	for _, tc := range []struct {
		name string
		keys string
		line string
	}{
		{name: "empty", keys: "", line: "at least one"},
		{name: "bad second line", keys: "ssh-ed25519 AAAA ok\ngarbage", line: "line 2"},
		{name: "blank middle line", keys: "ssh-ed25519 AAAA ok\n\nssh-rsa BBBB ok", line: "line 2"},
		{name: "repeated trailing newline", keys: "ssh-ed25519 AAAA ok\n\n", line: "line 2"},
		{name: "trailing garbage without comment separator", keys: "ssh-ed25519 AAAA!garbage", line: "line 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSSHAuthorizedKeys(tc.keys)
			if err == nil || !strings.Contains(err.Error(), tc.line) {
				t.Fatalf("ValidateSSHAuthorizedKeys() error = %v, want %q", err, tc.line)
			}
		})
	}
}

func TestLoadFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.toml")
	if err := os.WriteFile(path, []byte(validBootc()), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if l.Recipe.Image.Family != FamilyBootc {
		t.Errorf("family = %q", l.Recipe.Image.Family)
	}
	if !l.IsSet("target", "btrfs_subvolumes") {
		t.Error("IsSet should report btrfs_subvolumes present")
	}
}

// TestMarshalRoundTripValidates guards the TUI wizard's write-then-
// re-validate path (spec rule 5: the written artifact must validate).
func TestMarshalRoundTripValidates(t *testing.T) {
	cases := map[string]Recipe{
		"bootc": {
			Version:  SchemaVersion,
			Image:    Image{Family: FamilyBootc, Ref: "ghcr.io/frostyard/snow:latest"},
			Target:   Target{Disk: "/dev/vda", Filesystem: "btrfs", BtrfsSubvolumes: true},
			Security: Security{Encryption: "none"},
			System:   System{Hostname: "frost01"},
		},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := Marshal(&r)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			path := filepath.Join(t.TempDir(), "wizard.toml")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			l, err := Load(path)
			if err != nil {
				t.Fatalf("re-load of the written artifact: %v\n%s", err, data)
			}
			if issues := Validate(l, Env{}); len(issues) != 0 {
				t.Errorf("written artifact must validate, got %v\n%s", issues, data)
			}
		})
	}
}

// writeSecretMode creates a *_file input at an exact mode, defeating umask so
// the permission boundary under test is the file's own mode.
func writeSecretMode(t *testing.T, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("hunter2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != mode {
		t.Fatalf("%s: got mode %#o, want %#o", path, got, mode)
	}
	return path
}

// TestSecretFileModeBoundary pins the rule-2 boundary the recipe schema
// documents for operator-supplied secret files: an existing regular file that
// is not world-readable, not an exact 0600 mode. Group-readable 0640 is
// accepted; world-readable 0644 is rejected with CodeSecretFile.
func TestSecretFileModeBoundary(t *testing.T) {
	for _, field := range []string{"mok_password_file = ", "password_file = "} {
		for _, tc := range []struct {
			name     string
			mode     os.FileMode
			rejected bool
		}{
			{"group-readable 0640 accepted", 0o640, false},
			{"world-readable 0644 rejected", 0o644, true},
		} {
			t.Run(strings.TrimSuffix(field, " = ")+"/"+tc.name, func(t *testing.T) {
				secret := writeSecretMode(t, "secret", tc.mode)
				src := swapLine(validBootcWithUser(t), field, strings.TrimSuffix(field, " = ")+` = "`+secret+`"`)
				issues := Validate(mustParse(t, src), fullEnv)

				found := false
				for _, is := range issues {
					if is.Code == CodeSecretFile {
						found = true
					}
				}
				if tc.rejected && !found {
					t.Errorf("mode %#o: expected a %s issue, got %v", tc.mode, CodeSecretFile, issues)
				}
				if !tc.rejected && len(issues) != 0 {
					t.Errorf("mode %#o: expected no issues, got %v", tc.mode, issues)
				}
			})
		}
	}
}
