// Ported from frostyard/snosi (GPL-3.0-only), shared/native-installer/tree/usr/libexec/snosi-install (seed_var, seed_first_user).

package sysconfig

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/firn/internal/recipe"
)

// Baseline account databases with realistic Debian-ish content. seed:1000
// (passwd) and staff:1001 (group) force the first-free-id scan to prove it
// skips ids taken on EITHER side.
const (
	basePasswd = `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
snosi:x:998:998:snosi system:/nonexistent:/usr/sbin/nologin
seed:x:1000:1000:Seed User:/var/home/seed:/bin/bash
`
	baseGroup = `root:x:0:
daemon:x:1:
sudo:x:27:
shadow:x:42:
video:x:44:existing
users:x:100:
seed:x:1000:
staff:x:1001:
`
	baseShadow = `root:*:19700:0:99999:7:::
daemon:*:19700:0:99999:7:::
seed:!:19700:0:99999:7:::
`
	baseGshadow = `root:*::
sudo:*::
shadow:*::
video:!::existing
`
)

func writeFileMode(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	mkdirs(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile's mode is masked by umask; pin the fixture mode exactly.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// newOverlayWriter builds a fixture erofs-root stand-in (pristine etc
// baseline + skel) and an empty var filesystem.
func newOverlayWriter(t *testing.T) *OverlayWriter {
	t.Helper()
	root := t.TempDir()
	etc := filepath.Join(root, ".etc.lower")
	writeFileMode(t, filepath.Join(etc, "passwd"), basePasswd, 0o644)
	writeFileMode(t, filepath.Join(etc, "group"), baseGroup, 0o644)
	writeFileMode(t, filepath.Join(etc, "shadow"), baseShadow, 0o640)
	writeFileMode(t, filepath.Join(etc, "gshadow"), baseGshadow, 0o640)
	writeFileMode(t, filepath.Join(etc, "skel", ".bashrc"), "# .bashrc\n", 0o644)
	writeFileMode(t, filepath.Join(etc, "skel", ".config", "user-dirs.conf"), "enabled=True\n", 0o600)
	if err := os.Symlink(".bashrc", filepath.Join(etc, "skel", ".profile-link")); err != nil {
		t.Fatal(err)
	}
	return &OverlayWriter{VarDir: t.TempDir(), RootDir: root}
}

type chownCall struct {
	path     string
	uid, gid int
}

// stubChown replaces the chown seam (tests run unprivileged; real chown to
// root/shadow would fail) and records every call for assertions.
func stubChown(t *testing.T) *[]chownCall {
	t.Helper()
	var calls []chownCall
	orig := chownFn
	chownFn = func(path string, uid, gid int) error {
		calls = append(calls, chownCall{path, uid, gid})
		return nil
	}
	t.Cleanup(func() { chownFn = orig })
	return &calls
}

func stubNow(t *testing.T, at time.Time) {
	t.Helper()
	orig := overlayNow
	overlayNow = func() time.Time { return at }
	t.Cleanup(func() { overlayNow = orig })
}

func readOverlayFile(t *testing.T, path string, wantMode os.FileMode) string {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if st.Mode().Perm() != wantMode {
		t.Errorf("%s mode = %o, want %o", path, st.Mode().Perm(), wantMode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// assertNoTempFiles walks the var tree asserting writeFileAtomic left no
// stray ".<name>.tmp-*" files behind — every write must end in a rename.
func assertNoTempFiles(t *testing.T, varDir string) {
	t.Helper()
	err := filepath.WalkDir(varDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(d.Name(), ".tmp-") {
			t.Errorf("stray temp file left behind: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func upperOf(w *OverlayWriter) string {
	return filepath.Join(w.VarDir, "lib", "snosi", "etc-overlay", "upper")
}

func TestOverlayWriteHostname(t *testing.T) {
	w := newOverlayWriter(t)
	if err := w.WriteHostname("myhost"); err != nil {
		t.Fatal(err)
	}
	got := readOverlayFile(t, filepath.Join(upperOf(w), "hostname"), 0o644)
	if got != "myhost\n" {
		t.Errorf("hostname = %q", got)
	}
	assertNoTempFiles(t, w.VarDir)
}

func TestOverlayWriteHostnameEmptyIsNoop(t *testing.T) {
	w := newOverlayWriter(t)
	if err := w.WriteHostname(""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.VarDir, "lib")); !os.IsNotExist(err) {
		t.Errorf("empty hostname should write nothing, found lib/ (err=%v)", err)
	}
}

func TestOverlayWriteLocale(t *testing.T) {
	w := newOverlayWriter(t)
	if err := w.WriteLocale("en_US.UTF-8"); err != nil {
		t.Fatal(err)
	}
	got := readOverlayFile(t, filepath.Join(upperOf(w), "locale.conf"), 0o644)
	if got != "LANG=en_US.UTF-8\n" {
		t.Errorf("locale.conf = %q", got)
	}
}

func TestOverlayWriteTimezone(t *testing.T) {
	w := newOverlayWriter(t)
	if err := w.WriteTimezone("America/New_York"); err != nil {
		t.Fatal(err)
	}
	got := readOverlayFile(t, filepath.Join(upperOf(w), "timezone"), 0o644)
	if got != "America/New_York\n" {
		t.Errorf("timezone = %q", got)
	}
	target, err := os.Readlink(filepath.Join(upperOf(w), "localtime"))
	if err != nil {
		t.Fatal(err)
	}
	// Exact relative form seed_var's `ln -sfn` wrote.
	if target != "../usr/share/zoneinfo/America/New_York" {
		t.Errorf("localtime target = %q", target)
	}

	// ln -sfn semantics: a second write replaces the existing symlink.
	if err := w.WriteTimezone("Europe/Berlin"); err != nil {
		t.Fatal(err)
	}
	target, err = os.Readlink(filepath.Join(upperOf(w), "localtime"))
	if err != nil {
		t.Fatal(err)
	}
	if target != "../usr/share/zoneinfo/Europe/Berlin" {
		t.Errorf("replaced localtime target = %q", target)
	}
}

func TestOverlayWriteTimezoneRejectsTraversal(t *testing.T) {
	w := newOverlayWriter(t)
	for _, tz := range []string{"America/../../etc", "/etc/passwd"} {
		if err := w.WriteTimezone(tz); err == nil {
			t.Errorf("WriteTimezone(%q) accepted a traversal", tz)
		}
	}
}

func TestOverlayWriteKeyboard(t *testing.T) {
	cases := []struct {
		spec string
		want string
	}{
		{"us", "XKBMODEL=\"pc105\"\nXKBLAYOUT=\"us\"\nXKBVARIANT=\"\"\nXKBOPTIONS=\"\"\nBACKSPACE=\"guess\"\n"},
		{"us:intl", "XKBMODEL=\"pc105\"\nXKBLAYOUT=\"us\"\nXKBVARIANT=\"intl\"\nXKBOPTIONS=\"\"\nBACKSPACE=\"guess\"\n"},
		{"de:neo:thinkpad60", "XKBMODEL=\"thinkpad60\"\nXKBLAYOUT=\"de\"\nXKBVARIANT=\"neo\"\nXKBOPTIONS=\"\"\nBACKSPACE=\"guess\"\n"},
	}
	for _, c := range cases {
		w := newOverlayWriter(t)
		if err := w.WriteKeyboard(c.spec); err != nil {
			t.Fatalf("WriteKeyboard(%q): %v", c.spec, err)
		}
		got := readOverlayFile(t, filepath.Join(upperOf(w), "default", "keyboard"), 0o644)
		if got != c.want {
			t.Errorf("keyboard for %q:\n got %q\nwant %q", c.spec, got, c.want)
		}
	}
	w := newOverlayWriter(t)
	if err := w.WriteKeyboard(":intl"); err == nil {
		t.Error("empty layout accepted")
	}
}

func TestOverlayWriteRootAuthorizedKey(t *testing.T) {
	w := newOverlayWriter(t)
	if err := w.WriteRootAuthorizedKey("ssh-ed25519 AAAAtest root@iso\n"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(upperOf(w), "ssh", "authorized_keys.d")
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Errorf("authorized_keys.d mode = %o, want 0700", st.Mode().Perm())
	}
	got := readOverlayFile(t, filepath.Join(dir, "root"), 0o600)
	if got != "ssh-ed25519 AAAAtest root@iso\n" {
		t.Errorf("root key = %q", got)
	}
	assertNoTempFiles(t, w.VarDir)
}

func TestOverlayWriteInstallInfo(t *testing.T) {
	stubNow(t, time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC))
	w := newOverlayWriter(t)
	if err := w.WriteInstallInfo("cayo-ab", "2026.08.1"); err != nil {
		t.Fatal(err)
	}
	raw := readOverlayFile(t, filepath.Join(w.VarDir, "lib", "snosi", "install-info.json"), 0o644)
	var got map[string]string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("install-info.json is not valid JSON: %v\n%s", err, raw)
	}
	want := map[string]string{
		"product":      "cayo-ab",
		"version":      "2026.08.1",
		"architecture": got["architecture"], // host-dependent, checked below
		"installed_at": "2026-08-11T09:30:00Z",
		"installer":    "firn",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("install-info = %v, want %v", got, want)
	}
	if got["architecture"] == "" || got["architecture"] == "amd64" {
		// snosi records systemd-style arch strings ("x86-64", pinned by its
		// e2e test), never Go spellings.
		t.Errorf("architecture = %q, want a systemd-style arch string", got["architecture"])
	}
}

func TestOverlayCreateUser(t *testing.T) {
	stubNow(t, time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC))
	calls := stubChown(t)
	w := newOverlayWriter(t)

	pwFile := filepath.Join(t.TempDir(), "pw")
	writeFileMode(t, pwFile, "s3cret\n", 0o600)

	missing, err := w.CreateUser(recipe.User{
		Name:         "alice",
		Fullname:     "Alice Example",
		PasswordFile: pwFile,
		Groups:       []string{"sudo", "video", "wheel", "sudo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(missing, []string{"wheel"}) {
		t.Errorf("missing = %v, want [wheel]", missing)
	}

	upper := upperOf(w)

	// passwd: baseline preserved verbatim, new entry appended. uid/gid 1002:
	// 1000 is taken in passwd (seed) and 1001 in group (staff).
	gotPasswd := readOverlayFile(t, filepath.Join(upper, "passwd"), 0o644)
	wantPasswd := basePasswd + "alice:x:1002:1002:Alice Example:/var/home/alice:/bin/bash\n"
	if gotPasswd != wantPasswd {
		t.Errorf("passwd:\n got %q\nwant %q", gotPasswd, wantPasswd)
	}

	// group: personal group appended, sudo/video joined in place without
	// duplicating (sudo requested twice), wheel absent so untouched.
	gotGroup := readOverlayFile(t, filepath.Join(upper, "group"), 0o644)
	wantGroup := `root:x:0:
daemon:x:1:
sudo:x:27:alice
shadow:x:42:
video:x:44:existing,alice
users:x:100:
seed:x:1000:
staff:x:1001:
alice:x:1002:
`
	if gotGroup != wantGroup {
		t.Errorf("group:\n got %q\nwant %q", gotGroup, wantGroup)
	}

	// shadow: seed_first_user's exact field layout name:hash:lastchg:0:99999:7:::
	gotShadow := readOverlayFile(t, filepath.Join(upper, "shadow"), 0o640)
	if !strings.HasPrefix(gotShadow, baseShadow) {
		t.Errorf("shadow baseline not preserved:\n%q", gotShadow)
	}
	entry := strings.TrimPrefix(gotShadow, baseShadow)
	fields := strings.Split(strings.TrimSuffix(entry, "\n"), ":")
	if len(fields) != 9 {
		t.Fatalf("shadow entry has %d fields, want 9: %q", len(fields), entry)
	}
	wantDays := time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC).Unix() / 86400
	if fields[0] != "alice" || fields[3] != "0" || fields[4] != "99999" || fields[5] != "7" ||
		fields[6] != "" || fields[7] != "" || fields[8] != "" {
		t.Errorf("shadow entry shape wrong: %q", entry)
	}
	if got, want := fields[2], strconv.FormatInt(wantDays, 10); got != want {
		t.Errorf("lastchg = %s, want %s", got, want)
	}
	// The hash must be a real SHA-512 crypt of the password file's plaintext.
	hash := fields[1]
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[1] != "6" {
		t.Fatalf("hash is not $6$salt$digest: %q", hash)
	}
	if sha512Crypt("s3cret", parts[2]) != hash {
		t.Errorf("hash does not verify against plaintext: %q", hash)
	}

	// gshadow: entry appended, memberships joined.
	gotGshadow := readOverlayFile(t, filepath.Join(upper, "gshadow"), 0o640)
	wantGshadow := `root:*::
sudo:*::alice
shadow:*::
video:!::existing,alice
alice:!::
`
	if gotGshadow != wantGshadow {
		t.Errorf("gshadow:\n got %q\nwant %q", gotGshadow, wantGshadow)
	}

	// Ownership intent: passwd/group root:root, shadow/gshadow root:shadow
	// by the IMAGE's numeric shadow gid (42 in the baseline).
	wantOwners := map[string][2]int{
		filepath.Join(upper, "passwd"):  {0, 0},
		filepath.Join(upper, "group"):   {0, 0},
		filepath.Join(upper, "shadow"):  {0, 42},
		filepath.Join(upper, "gshadow"): {0, 42},
	}
	seen := map[string][2]int{}
	for _, c := range *calls {
		seen[c.path] = [2]int{c.uid, c.gid}
	}
	for path, want := range wantOwners {
		if seen[path] != want {
			t.Errorf("chown %s = %v, want %v", path, seen[path], want)
		}
	}

	// Home: 0700, skel copied with modes/symlinks intact, chowned 1002:1002.
	home := filepath.Join(w.VarDir, "home", "alice")
	st, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Errorf("home mode = %o, want 0700", st.Mode().Perm())
	}
	if got := readOverlayFile(t, filepath.Join(home, ".bashrc"), 0o644); got != "# .bashrc\n" {
		t.Errorf(".bashrc = %q", got)
	}
	readOverlayFile(t, filepath.Join(home, ".config", "user-dirs.conf"), 0o600)
	if target, err := os.Readlink(filepath.Join(home, ".profile-link")); err != nil || target != ".bashrc" {
		t.Errorf(".profile-link -> %q (err %v), want .bashrc", target, err)
	}
	for _, p := range []string{home, filepath.Join(home, ".bashrc"), filepath.Join(home, ".config"),
		filepath.Join(home, ".config", "user-dirs.conf"), filepath.Join(home, ".profile-link")} {
		if seen[p] != [2]int{1002, 1002} {
			t.Errorf("chown %s = %v, want [1002 1002]", p, seen[p])
		}
	}

	// Baseline files must be untouched (read-only image contract).
	if got, _ := os.ReadFile(filepath.Join(w.RootDir, ".etc.lower", "passwd")); string(got) != basePasswd {
		t.Error("baseline passwd was modified")
	}

	assertNoTempFiles(t, w.VarDir)
}

func TestOverlayCreateUserVariants(t *testing.T) {
	stubNow(t, time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC))
	stubChown(t)

	t.Run("empty name is a no-op", func(t *testing.T) {
		w := newOverlayWriter(t)
		missing, err := w.CreateUser(recipe.User{})
		if missing != nil || err != nil {
			t.Fatalf("got %v, %v", missing, err)
		}
	})

	t.Run("existing username rejected", func(t *testing.T) {
		w := newOverlayWriter(t)
		if _, err := w.CreateUser(recipe.User{Name: "seed"}); err == nil {
			t.Fatal("existing username accepted")
		}
	})

	t.Run("existing group name rejected", func(t *testing.T) {
		w := newOverlayWriter(t)
		if _, err := w.CreateUser(recipe.User{Name: "staff"}); err == nil {
			t.Fatal("existing group name accepted")
		}
	})

	t.Run("password hash used verbatim", func(t *testing.T) {
		w := newOverlayWriter(t)
		const pre = "$6$fixedsalt$precomputedhash"
		if _, err := w.CreateUser(recipe.User{Name: "bob", PasswordHash: pre}); err != nil {
			t.Fatal(err)
		}
		got := readOverlayFile(t, filepath.Join(upperOf(w), "shadow"), 0o640)
		if !strings.Contains(got, "bob:"+pre+":") {
			t.Errorf("pre-computed hash not verbatim in shadow:\n%s", got)
		}
	})

	t.Run("no password locks the account", func(t *testing.T) {
		w := newOverlayWriter(t)
		if _, err := w.CreateUser(recipe.User{Name: "bob"}); err != nil {
			t.Fatal(err)
		}
		got := readOverlayFile(t, filepath.Join(upperOf(w), "shadow"), 0o640)
		if !strings.Contains(got, "bob:!:") {
			t.Errorf("passwordless account not locked:\n%s", got)
		}
	})

	t.Run("invalid gecos rejected before reading the image", func(t *testing.T) {
		w := newOverlayWriter(t)
		w.RootDir = t.TempDir()
		if _, err := w.CreateUser(recipe.User{Name: "bob", Fullname: "Bad:Guy\nHere"}); err == nil || !strings.Contains(err.Error(), "full name") {
			t.Fatalf("invalid full name error = %v", err)
		}
	})

	t.Run("image without gshadow", func(t *testing.T) {
		w := newOverlayWriter(t)
		if err := os.Remove(filepath.Join(w.RootDir, ".etc.lower", "gshadow")); err != nil {
			t.Fatal(err)
		}
		missing, err := w.CreateUser(recipe.User{Name: "bob", Groups: []string{"sudo"}})
		if err != nil {
			t.Fatal(err)
		}
		if missing != nil {
			t.Errorf("missing = %v", missing)
		}
		if _, err := os.Lstat(filepath.Join(upperOf(w), "gshadow")); !os.IsNotExist(err) {
			t.Errorf("gshadow written despite absent baseline (err=%v)", err)
		}
		got := readOverlayFile(t, filepath.Join(upperOf(w), "group"), 0o644)
		if !strings.Contains(got, "sudo:x:27:bob\n") {
			t.Errorf("sudo join missing:\n%s", got)
		}
	})

	t.Run("baseline modes carried onto the overlay copies", func(t *testing.T) {
		// The fixed bash matched each target's actual mode bits
		// (chmod --reference); prove non-default baseline modes carry over.
		w := newOverlayWriter(t)
		writeFileMode(t, filepath.Join(w.RootDir, ".etc.lower", "shadow"), baseShadow, 0o600)
		if _, err := w.CreateUser(recipe.User{Name: "bob"}); err != nil {
			t.Fatal(err)
		}
		readOverlayFile(t, filepath.Join(upperOf(w), "shadow"), 0o600)
		readOverlayFile(t, filepath.Join(upperOf(w), "group"), 0o644)
	})
}

func TestOverlayWriteUserAuthorizedKey(t *testing.T) {
	stubNow(t, time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC))
	calls := stubChown(t)
	w := newOverlayWriter(t)
	if _, err := w.CreateUser(recipe.User{Name: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteUserAuthorizedKey("alice", "ssh-ed25519 AAAAuser alice@laptop"); err != nil {
		t.Fatal(err)
	}
	sshDir := filepath.Join(w.VarDir, "home", "alice", ".ssh")
	st, err := os.Stat(sshDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Errorf(".ssh mode = %o, want 0700", st.Mode().Perm())
	}
	keyPath := filepath.Join(sshDir, "authorized_keys")
	got := readOverlayFile(t, keyPath, 0o600)
	if got != "ssh-ed25519 AAAAuser alice@laptop\n" {
		t.Errorf("authorized_keys = %q", got)
	}
	seen := map[string][2]int{}
	for _, c := range *calls {
		seen[c.path] = [2]int{c.uid, c.gid}
	}
	for _, p := range []string{sshDir, keyPath} {
		if seen[p] != [2]int{1002, 1002} {
			t.Errorf("chown %s = %v, want [1002 1002]", p, seen[p])
		}
	}

	// Unknown user (nothing in the overlay passwd) must error, not guess.
	if err := w.WriteUserAuthorizedKey("ghost", "ssh-ed25519 AAAA"); err == nil {
		t.Error("unknown user accepted")
	}
}
