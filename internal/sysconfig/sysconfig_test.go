package sysconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/runner"
)

// call records one fake-runner invocation, including any stdin fed via
// RunInput.
type call struct {
	name  string
	args  []string
	stdin string
}

// fakeRunner returns a Runner whose exec records every call and emulates
// the few filesystem commands the writer relies on (ls, mkdir -p, mv)
// against the real fixture tree. Everything else succeeds silently.
func fakeRunner(calls *[]call) *runner.Runner {
	return runner.NewFake(func(ctx context.Context, name string, args ...string) ([]byte, error) {
		c := call{name: name, args: args}
		if in, ok := runner.Stdin(ctx); ok {
			c.stdin = in
		}
		*calls = append(*calls, c)
		switch name {
		case "ls":
			if _, err := os.Stat(args[0]); err != nil {
				return nil, err
			}
		case "mkdir": // always invoked as mkdir -p <dir>
			return nil, os.MkdirAll(args[len(args)-1], 0o755)
		case "mv":
			return nil, os.Rename(args[0], args[1])
		}
		return nil, nil
	}, func(name string) (string, error) { return "/usr/bin/" + name, nil })
}

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdirs(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// composefsTarget builds a composefs-native fixture: state/deploy/<hash>/etc
// plus a BLS loader entry naming the hash.
func composefsTarget(t *testing.T, hash string) string {
	t.Helper()
	target := t.TempDir()
	mkdirs(t, filepath.Join(target, "state", "deploy", hash, "etc"))
	writeFile(t, filepath.Join(target, "boot", "loader", "entries", "test.conf"),
		"title Test\noptions root=UUID=abc composefs="+hash+" rw\n")
	return target
}

// ostreeTarget builds an ostree fixture: ostree/deploy/default/deploy/<id>
// with an etc subtree. The fake runner's `ostree admin` returns empty
// output, so DefaultDeploymentDir exercises its glob fallback.
func ostreeTarget(t *testing.T) (target, deployDir string) {
	t.Helper()
	target = t.TempDir()
	deployDir = filepath.Join(target, "ostree", "deploy", "default", "deploy", "abcd1234.0")
	mkdirs(t, filepath.Join(deployDir, "etc"))
	return target, deployDir
}

func findCall(t *testing.T, calls []call, name string) call {
	t.Helper()
	for _, c := range calls {
		if c.name == name {
			return c
		}
	}
	t.Fatalf("no %q call recorded; calls: %+v", name, calls)
	return call{}
}

func TestDefaultComposeFsDeployEtcDir(t *testing.T) {
	t.Run("BLS entry wins over newer directory", func(t *testing.T) {
		target := composefsTarget(t, "aaa")
		// A newer sibling deployment the mtime fallback would prefer.
		newer := filepath.Join(target, "state", "deploy", "bbb")
		mkdirs(t, filepath.Join(newer, "etc"))
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(filepath.Join(target, "state", "deploy", "aaa"), old, old); err != nil {
			t.Fatal(err)
		}
		got, err := DefaultComposeFsDeployEtcDir(target)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(target, "state", "deploy", "aaa", "etc")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("fallback picks newest deploy dir", func(t *testing.T) {
		target := t.TempDir() // no BLS entries at all
		oldDir := filepath.Join(target, "state", "deploy", "older")
		newDir := filepath.Join(target, "state", "deploy", "newer")
		mkdirs(t, oldDir, newDir)
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(oldDir, old, old); err != nil {
			t.Fatal(err)
		}
		got, err := DefaultComposeFsDeployEtcDir(target)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(newDir, "etc"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("no deployments is an error", func(t *testing.T) {
		if _, err := DefaultComposeFsDeployEtcDir(t.TempDir()); err == nil {
			t.Error("want error for empty target")
		}
	})
}

func TestDefaultDeploymentDir(t *testing.T) {
	ctx := context.Background()

	t.Run("uses ostree admin output", func(t *testing.T) {
		r := runner.NewFake(func(ctx context.Context, name string, args ...string) ([]byte, error) {
			wantArgs := []string{"admin", "--sysroot=/sysroot", "--print-current-dir"}
			if name != "ostree" || !reflect.DeepEqual(args, wantArgs) {
				return nil, fmt.Errorf("unexpected call %s %v", name, args)
			}
			return []byte("/sysroot/ostree/deploy/default/deploy/x.0\n"), nil
		}, nil)
		got, err := DefaultDeploymentDir(ctx, r, "/sysroot")
		if err != nil {
			t.Fatal(err)
		}
		if want := "/sysroot/ostree/deploy/default/deploy/x.0"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("glob fallback on fresh install", func(t *testing.T) {
		// --print-current-dir exits 1 on a never-booted target.
		target, deployDir := ostreeTarget(t)
		r := runner.NewFake(func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return nil, fmt.Errorf("exit status 1")
		}, nil)
		got, err := DefaultDeploymentDir(ctx, r, target)
		if err != nil {
			t.Fatal(err)
		}
		if got != deployDir {
			t.Errorf("got %q, want %q", got, deployDir)
		}
	})

	t.Run("no deployment is an error", func(t *testing.T) {
		r := runner.NewFake(func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return nil, fmt.Errorf("exit status 1")
		}, nil)
		if _, err := DefaultDeploymentDir(ctx, r, t.TempDir()); err == nil {
			t.Error("want error when no deployment exists")
		}
	})
}

func TestWriteHostname(t *testing.T) {
	ctx := context.Background()

	t.Run("composefs", func(t *testing.T) {
		target := composefsTarget(t, "aaa")
		var calls []call
		w := &DeploymentWriter{TargetDir: target, Runner: fakeRunner(&calls)}
		if err := w.WriteHostname(ctx, "frostbox"); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(target, "state", "deploy", "aaa", "etc", "hostname"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "frostbox\n" {
			t.Errorf("hostname content = %q, want %q", got, "frostbox\n")
		}
	})

	t.Run("ostree", func(t *testing.T) {
		target, deployDir := ostreeTarget(t)
		var calls []call
		w := &DeploymentWriter{TargetDir: target, Runner: fakeRunner(&calls)}
		if err := w.WriteHostname(ctx, "frostbox"); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(deployDir, "etc", "hostname"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "frostbox\n" {
			t.Errorf("hostname content = %q, want %q", got, "frostbox\n")
		}
	})
}

func TestCreateUser_EmptyNameIsNoop(t *testing.T) {
	var calls []call
	w := &DeploymentWriter{TargetDir: t.TempDir(), Runner: fakeRunner(&calls)}
	if _, err := w.CreateUser(context.Background(), recipe.User{}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Errorf("expected no runner calls, got %+v", calls)
	}
}

func TestCreateUser_Ostree(t *testing.T) {
	target, deployDir := ostreeTarget(t)
	// Simulate useradd --create-home having written the home into the
	// DEPLOYMENT's own var (via the /home -> var/home symlink).
	deployHome := filepath.Join(deployDir, "var", "home", "dev")
	writeFile(t, filepath.Join(deployHome, ".bashrc"), "# skel\n")

	// Deployment etc/group defines which groups exist in the image;
	// "missing" must be filtered out and reported (join-where-exists).
	writeFile(t, filepath.Join(deployDir, "etc", "group"),
		"root:x:0:\nwheel:x:10:\ndocker:x:970:\n")

	var calls []call
	w := &DeploymentWriter{TargetDir: target, Runner: fakeRunner(&calls)}
	u := recipe.User{
		Name:         "dev",
		Fullname:     "Dev Eloper",
		Groups:       []string{"wheel", "docker", "missing"},
		PasswordHash: "$6$salt$hash",
	}
	missing, err := w.CreateUser(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0] != "missing" {
		t.Errorf("missing groups = %v, want [missing]", missing)
	}

	// useradd runs inside the deployment via chroot, with full argv.
	chrootCall := findCall(t, calls, "chroot")
	wantArgs := []string{
		deployDir, "useradd", "--create-home", "--shell", "/bin/bash",
		"--comment", "Dev Eloper", "--groups", "wheel,docker", "dev",
	}
	if !reflect.DeepEqual(chrootCall.args, wantArgs) {
		t.Errorf("chroot args = %v, want %v", chrootCall.args, wantArgs)
	}

	// Home relocated from the deployment var into the stateroot var.
	stateHome := filepath.Join(target, "ostree", "deploy", "default", "var", "home", "dev")
	if _, err := os.Stat(filepath.Join(stateHome, ".bashrc")); err != nil {
		t.Errorf("home not relocated to stateroot var: %v", err)
	}
	if _, err := os.Stat(deployHome); !os.IsNotExist(err) {
		t.Errorf("deployment-var home still present: %v", err)
	}

	// tmpfiles.d snippet pins first-boot creation + relabel.
	snippet, err := os.ReadFile(filepath.Join(deployDir, "etc", "tmpfiles.d", "firn-home-dev.conf"))
	if err != nil {
		t.Fatal(err)
	}
	want := "C /var/home/dev 0700 dev dev - /etc/skel\nZ /var/home/dev - dev dev -\n"
	if string(snippet) != want {
		t.Errorf("tmpfiles snippet = %q, want %q", snippet, want)
	}

	// Pre-hashed password goes through the target's chpasswd -e via stdin.
	var cp *call
	for i := range calls {
		if calls[i].stdin != "" {
			cp = &calls[i]
		}
	}
	if cp == nil {
		t.Fatal("no stdin-fed call recorded for chpasswd")
	}
	if cp.name != "chroot" || !reflect.DeepEqual(cp.args, []string{deployDir, "chpasswd", "-e"}) {
		t.Errorf("chpasswd call = %s %v, want chroot [%s chpasswd -e]", cp.name, cp.args, deployDir)
	}
	if cp.stdin != "dev:$6$salt$hash\n" {
		t.Errorf("chpasswd stdin = %q", cp.stdin)
	}
}

func TestCreateUser_Composefs(t *testing.T) {
	target := composefsTarget(t, "aaa")
	deployRoot := filepath.Join(target, "state", "deploy", "aaa")

	passwordFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordFile, []byte("hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(deployRoot, "etc", "group"), "root:x:0:\nwheel:x:10:\n")

	var calls []call
	w := &DeploymentWriter{TargetDir: target, Runner: fakeRunner(&calls)}
	u := recipe.User{
		Name:         "dev",
		Fullname:     "Dev Eloper",
		Groups:       []string{"wheel"},
		PasswordFile: passwordFile,
	}
	if _, err := w.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}

	// useradd invoked directly with --root, without the literal "useradd"
	// argv[0] and without --create-home.
	ua := findCall(t, calls, "useradd")
	wantArgs := []string{
		"--root", deployRoot, "--shell", "/bin/bash",
		"--comment", "Dev Eloper", "--groups", "wheel", "dev",
	}
	if !reflect.DeepEqual(ua.args, wantArgs) {
		t.Errorf("useradd args = %v, want %v", ua.args, wantArgs)
	}
	for _, c := range calls {
		if c.name == "chroot" {
			t.Errorf("unexpected chroot call on composefs: %v", c.args)
		}
	}

	// tmpfiles.d snippet under the deploy root's etc.
	if _, err := os.Stat(filepath.Join(deployRoot, "etc", "tmpfiles.d", "firn-home-dev.conf")); err != nil {
		t.Errorf("tmpfiles snippet missing: %v", err)
	}

	// Plaintext password file is SHA-512-crypt hashed in Go, then applied
	// with chpasswd --root -e.
	cp := findCall(t, calls, "chpasswd")
	if !reflect.DeepEqual(cp.args, []string{"--root", deployRoot, "-e"}) {
		t.Errorf("chpasswd args = %v", cp.args)
	}
	prefix := "dev:$6$"
	if !strings.HasPrefix(cp.stdin, prefix) || !strings.HasSuffix(cp.stdin, "\n") {
		t.Fatalf("chpasswd stdin = %q, want %q…\\n", cp.stdin, prefix)
	}
	hash := strings.TrimSuffix(strings.TrimPrefix(cp.stdin, "dev:"), "\n")
	parts := strings.Split(hash, "$") // "", "6", salt, digest
	if len(parts) != 4 {
		t.Fatalf("malformed crypt hash %q", hash)
	}
	if got := sha512Crypt("hunter2", parts[2]); got != hash {
		t.Errorf("hash does not verify against trimmed plaintext: got %q, sent %q", got, hash)
	}
}

func TestSha512Crypt(t *testing.T) {
	// Vectors cross-checked with `openssl passwd -6 -salt <salt> <pw>`;
	// the first is also the reference vector from Drepper's spec.
	tests := []struct {
		password, salt, want string
	}{
		{
			"Hello world!", "saltstring",
			"$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1",
		},
		{
			"firn-secret", "1234567890123456",
			"$6$1234567890123456$i6a2E278CSowRa.WbLntzx1jUlbCPMwGLZxOchVqakbp/x0z4E8LRdWVj5f27kWvG///9SlByYbxmB3J5Pgt81",
		},
	}
	for _, tt := range tests {
		if got := sha512Crypt(tt.password, tt.salt); got != tt.want {
			t.Errorf("sha512Crypt(%q, %q) = %q, want %q", tt.password, tt.salt, got, tt.want)
		}
	}
}

func TestNewCryptSalt(t *testing.T) {
	salt, err := newCryptSalt()
	if err != nil {
		t.Fatal(err)
	}
	if len(salt) != 16 {
		t.Errorf("salt length = %d, want 16", len(salt))
	}
	for _, c := range salt {
		if !strings.ContainsRune(sha512CryptAlphabet, c) {
			t.Errorf("salt char %q outside crypt alphabet", c)
		}
	}
}

func TestWriteRootAuthorizedKey(t *testing.T) {
	ctx := context.Background()
	const key = "ssh-ed25519 AAAAC3Nza root@example"

	for _, tc := range []struct {
		name string
		make func(t *testing.T) (target, staterootVar string)
	}{
		{"composefs", func(t *testing.T) (string, string) {
			target := composefsTarget(t, "aaa")
			return target, filepath.Join(target, "state", "os", "default", "var")
		}},
		{"ostree", func(t *testing.T) (string, string) {
			target, _ := ostreeTarget(t)
			return target, filepath.Join(target, "ostree", "deploy", "default", "var")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, staterootVar := tc.make(t)
			var calls []call
			w := &DeploymentWriter{TargetDir: target, Runner: fakeRunner(&calls)}
			if err := w.WriteRootAuthorizedKey(ctx, key); err != nil {
				t.Fatal(err)
			}
			// Runtime /root is /var/roothome on the STATEROOT var — a
			// deployment-root write would be invisible to the booted
			// system.
			sshDir := filepath.Join(staterootVar, "roothome", ".ssh")
			keyFile := filepath.Join(sshDir, "authorized_keys")
			got, err := os.ReadFile(keyFile)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != key+"\n" {
				t.Errorf("authorized_keys = %q, want %q", got, key+"\n")
			}
			if fi, _ := os.Stat(sshDir); fi.Mode().Perm() != 0o700 {
				t.Errorf(".ssh mode = %o, want 0700", fi.Mode().Perm())
			}
			if fi, _ := os.Stat(keyFile); fi.Mode().Perm() != 0o600 {
				t.Errorf("authorized_keys mode = %o, want 0600", fi.Mode().Perm())
			}
		})
	}
}

func TestWriteUserAuthorizedKey(t *testing.T) {
	ctx := context.Background()
	const key = "ssh-ed25519 AAAAC3Nza dev@example"
	const passwd = "root:x:0:0:root:/root:/bin/bash\ndev:x:1000:1001:Dev Eloper:/home/dev:/bin/bash\n"

	for _, tc := range []struct {
		name string
		make func(t *testing.T) (target, etcDir, varDir string)
	}{
		{"composefs", func(t *testing.T) (string, string, string) {
			target := composefsTarget(t, "aaa")
			return target,
				filepath.Join(target, "state", "deploy", "aaa", "etc"),
				filepath.Join(target, "state", "os", "default", "var")
		}},
		{"ostree", func(t *testing.T) (string, string, string) {
			target, deployDir := ostreeTarget(t)
			return target,
				filepath.Join(deployDir, "etc"),
				filepath.Join(target, "ostree", "deploy", "default", "var")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, etcDir, varDir := tc.make(t)
			writeFile(t, filepath.Join(etcDir, "passwd"), passwd)
			var calls []call
			w := &DeploymentWriter{TargetDir: target, Runner: fakeRunner(&calls)}
			if err := w.WriteUserAuthorizedKey(ctx, "dev", key); err != nil {
				t.Fatal(err)
			}

			home := filepath.Join(varDir, "home", "dev")
			sshDir := filepath.Join(home, ".ssh")
			keyFile := filepath.Join(sshDir, "authorized_keys")
			got, err := os.ReadFile(keyFile)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != key+"\n" {
				t.Errorf("authorized_keys = %q, want %q", got, key+"\n")
			}
			if fi, _ := os.Stat(sshDir); fi.Mode().Perm() != 0o700 {
				t.Errorf(".ssh mode = %o, want 0700", fi.Mode().Perm())
			}
			if fi, _ := os.Stat(keyFile); fi.Mode().Perm() != 0o600 {
				t.Errorf("authorized_keys mode = %o, want 0600", fi.Mode().Perm())
			}

			// Ownership fixed to the uid/gid useradd chose, read back from
			// the deployment's etc/passwd.
			ch := findCall(t, calls, "chown")
			if want := []string{"-R", "1000:1001", home}; !reflect.DeepEqual(ch.args, want) {
				t.Errorf("chown args = %v, want %v", ch.args, want)
			}
		})
	}
}

// A wizard-pasted SSH key is what first creates the stateroot home on the
// composefs path, and CreateUser's tmpfiles.d "C" entry only copies
// /etc/skel into a MISSING home on first boot -- so the pre-created home
// must get the skel copy here, or images that ship their desktop config in
// skel (flurry: the entire omarchy dotfile set) produce a bare session.
func TestWriteUserAuthorizedKeyCopiesSkelIntoNewHome(t *testing.T) {
	ctx := context.Background()
	target := composefsTarget(t, "aaa")
	etcDir := filepath.Join(target, "state", "deploy", "aaa", "etc")
	writeFile(t, filepath.Join(etcDir, "passwd"),
		"root:x:0:0:root:/root:/bin/bash\ndev:x:1000:1001:Dev:/home/dev:/bin/bash\n")
	writeFile(t, filepath.Join(etcDir, "skel", ".bashrc"), "export SKEL=1\n")
	writeFile(t, filepath.Join(etcDir, "skel", ".config", "hypr", "hyprland.lua"), "-- cfg\n")

	var calls []call
	w := &DeploymentWriter{TargetDir: target, Runner: fakeRunner(&calls)}
	if err := w.WriteUserAuthorizedKey(ctx, "dev", "ssh-ed25519 AAAA dev@x"); err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(target, "state", "os", "default", "var", "home", "dev")
	for _, rel := range []string{".bashrc", ".config/hypr/hyprland.lua", ".ssh/authorized_keys"} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("%s missing from pre-created home: %v", rel, err)
		}
	}
	if fi, _ := os.Stat(home); fi.Mode().Perm() != 0o700 {
		t.Errorf("home mode = %o, want 0700", fi.Mode().Perm())
	}
}

// An already-existing home (any earlier step created it) must NOT be
// re-seeded: skel only fills a home this call itself creates.
func TestWriteUserAuthorizedKeyLeavesExistingHomeAlone(t *testing.T) {
	ctx := context.Background()
	target := composefsTarget(t, "aaa")
	etcDir := filepath.Join(target, "state", "deploy", "aaa", "etc")
	writeFile(t, filepath.Join(etcDir, "passwd"),
		"dev:x:1000:1001:Dev:/home/dev:/bin/bash\n")
	writeFile(t, filepath.Join(etcDir, "skel", ".bashrc"), "export SKEL=1\n")
	home := filepath.Join(target, "state", "os", "default", "var", "home", "dev")
	writeFile(t, filepath.Join(home, ".profile"), "existing\n")

	var calls []call
	w := &DeploymentWriter{TargetDir: target, Runner: fakeRunner(&calls)}
	if err := w.WriteUserAuthorizedKey(ctx, "dev", "ssh-ed25519 AAAA dev@x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".bashrc")); err == nil {
		t.Error("skel was copied into a pre-existing home")
	}
}

func TestWriteUserAuthorizedKey_UnknownUser(t *testing.T) {
	target := composefsTarget(t, "aaa")
	writeFile(t, filepath.Join(target, "state", "deploy", "aaa", "etc", "passwd"),
		"root:x:0:0:root:/root:/bin/bash\n")
	var calls []call
	w := &DeploymentWriter{TargetDir: target, Runner: fakeRunner(&calls)}
	err := w.WriteUserAuthorizedKey(context.Background(), "ghost", "ssh-ed25519 AAAA")
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("want user-not-found error, got %v", err)
	}
}
