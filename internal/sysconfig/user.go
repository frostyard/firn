// Ported from frostyard/fisherman (GPL-3.0-only), fisherman/internal/post/user.go.

package sysconfig

import (
	"context"
	"crypto/rand"
	"crypto/sha512"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/frostyard/firn/internal/recipe"
)

// CreateUser creates a user account inside the installed system rooted at
// TargetDir.
//
// For ostree/bootc deployments, the deployment directory (found via DeploymentDirFn)
// is used as the --root for useradd so that the image's /etc/passwd, /etc/shadow,
// and /etc/group are updated. The sysroot root itself does not contain these files.
//
// For composefs-native deployments, the OS tree lives in a read-only EROFS
// image; the only real /etc on disk is the pristine copy under
// state/deploy/<id>/etc, so that state dir is the useradd --root. Its var
// symlink (../../os/default/var) dangles inside the chroot, so the home
// directory is created host-side in the shared var instead of via
// --create-home, and chpasswd gets a pre-computed hash with -e because the
// etc-only chroot has no PAM modules to load.
//
// Returns nil if u.Name is empty (no-op).
func (w *DeploymentWriter) CreateUser(ctx context.Context, u recipe.User) error {
	if u.Name == "" {
		return nil
	}

	lay, err := w.resolve(ctx)
	if err != nil {
		return err
	}
	root := lay.root
	var staterootHome string
	if !lay.composefs {
		// On ostree/bootc, /home inside the deployment is a symlink to
		// var/home (the stateroot var). Pre-create the stateroot home dir so
		// the relocation below has a destination.
		staterootHome = filepath.Join(w.staterootVarDir(lay), "home")
		if _, err := w.Runner.Run(ctx, "mkdir", "-p", staterootHome); err != nil {
			return fmt.Errorf("mkdir stateroot home: %w", err)
		}
	}

	// Run the TARGET's useradd via chroot rather than `useradd --root`:
	// --root chroots too, but first initializes the HOST's PAM/SELinux
	// stack — from a booted host it dies with "failure while writing
	// changes to /etc/passwd" against a target whose etc is perfectly
	// writable (wootc run 20260723T0738: the deployer-initramfs
	// environment masked this; the same call from booted Phase 2 failed
	// every time while `chroot <dep> useradd` succeeded on the same
	// files). Plain chroot uses only the target's own libraries.
	// ostree: `chroot <deployDir> useradd` (the deployDir is a full rootfs).
	// composefs-native: `useradd --root <deployRoot>` (no chroot rootfs
	// exists during deploy; --root only edits the passwd files, and the
	// booted-host PAM problem that forced chroot is ostree-only — composefs
	// deploys happen in the initramfs). composefs-native has no full
	// chrootable rootfs during deploy — the sealed image's /usr (with
	// useradd) is mounted read-only elsewhere, so `chroot <sysroot> useradd`
	// exits 127 (dakota, GH matrix 20260724T1508).
	tail := []string{"useradd", "--create-home", "--shell", "/bin/bash"}
	if u.Fullname != "" {
		tail = append(tail, "--comment", u.Fullname)
	}
	if len(u.Groups) > 0 {
		tail = append(tail, "--groups", strings.Join(u.Groups, ","))
	}
	tail = append(tail, u.Name)

	if lay.composefs {
		// tail[0] is the literal "useradd" command name, needed only by the
		// ostree branch's `chroot <root> useradd …`. Here we invoke the
		// useradd binary directly, so drop it — otherwise it becomes a second
		// positional login name alongside the username and shadow-utils exits 2
		// ("invalid command syntax", dakota GH matrix 20260724T1705).
		//
		// Also drop --create-home: on a composefs-native deploy root, /home is a
		// symlink to a stateroot var that does not exist under --root, so useradd
		// --create-home cannot create the directory and exits 12 (dakota GH
		// matrix 20260724T2128). The passwd entry is written without it, and the
		// composefs tmpfiles.d snippet below builds+labels /var/home/<user> from
		// /etc/skel on first boot — the same mechanism the ostree branch relies
		// on after its relocation.
		cargs := []string{"--root", root}
		for _, a := range tail[1:] {
			if a == "--create-home" {
				continue
			}
			cargs = append(cargs, a)
		}
		if _, err := w.Runner.Run(ctx, "useradd", cargs...); err != nil {
			return fmt.Errorf("useradd (--root %s): %w", root, err)
		}
		// First-boot home creation for composefs-native (no staterootHome
		// relocation path). /home -> var/home is a bootc invariant, so the
		// runtime /var/home/<user> path is correct regardless of the composefs
		// deploy layout. Mirrors the ostree snippet below.
		if err := writeHomeTmpfiles(root, u.Name); err != nil {
			return err
		}
	} else {
		if _, err := w.Runner.Run(ctx, "chroot", append([]string{root}, tail...)...); err != nil {
			return fmt.Errorf("useradd (chroot %s): %w", root, err)
		}
	}

	// useradd --create-home resolved /home through the deployment's
	// /home -> var/home symlink and wrote the directory into the
	// DEPLOYMENT's own var/ — which the booted system never sees: the
	// stateroot var is mounted over /var at runtime. The account then
	// boots with a passwd entry but no home directory at all (wootc E2E
	// run 20260723T0423: var/home held only the image's seed content).
	// Relocate the freshly created home into the stateroot var, and pin
	// it with a tmpfiles.d snippet so the first boot (re)creates it from
	// /etc/skel if missing and restores ownership + SELinux labels under
	// the live policy — offline useradd cannot label correctly.
	if staterootHome != "" {
		deployHome := filepath.Join(root, "var", "home", u.Name)
		stateHome := filepath.Join(staterootHome, u.Name)
		if _, err := os.Stat(deployHome); err == nil {
			if _, err := os.Stat(stateHome); os.IsNotExist(err) {
				if _, err := w.Runner.Run(ctx, "mv", deployHome, stateHome); err != nil {
					return fmt.Errorf("relocating home to stateroot var: %w", err)
				}
			}
		}
		if err := writeHomeTmpfiles(root, u.Name); err != nil {
			return err
		}
	}

	// Set the password via chpasswd stdin to avoid it appearing in ps output.
	// Same ostree-chroot vs composefs---root split as useradd above.
	hash, err := passwordHash(u)
	if err != nil {
		return err
	}
	if hash != "" {
		input := fmt.Sprintf("%s:%s\n", u.Name, hash)
		// A pre-hashed crypt(3) string ("$id$salt$hash", e.g. a recipe's
		// password_hash $6$ SHA-512) must be written verbatim with -e;
		// otherwise chpasswd treats the hash as plaintext. Plaintext
		// password files are hashed in Go above (fisherman instead passed
		// plaintext with --crypt-method SHA512 for composefs, because the
		// etc-only deploy root has no PAM modules and chpasswd otherwise
		// defaults to PAM — pre-hashing sidesteps PAM on both layouts).
		var cpErr error
		if lay.composefs {
			_, cpErr = w.Runner.RunInput(ctx, input, "chpasswd", "--root", root, "-e")
		} else {
			_, cpErr = w.Runner.RunInput(ctx, input, "chroot", root, "chpasswd", "-e")
		}
		if cpErr != nil {
			return fmt.Errorf("chpasswd (%s): %w", root, cpErr)
		}
	}

	return nil
}

// passwordHash resolves the recipe's password choice to a crypt(5) hash:
// PasswordHash is used verbatim; PasswordFile is read as plaintext and
// hashed with SHA-512 crypt in Go. Empty when neither is set.
func passwordHash(u recipe.User) (string, error) {
	if u.PasswordHash != "" {
		return u.PasswordHash, nil
	}
	if u.PasswordFile == "" {
		return "", nil
	}
	data, err := os.ReadFile(u.PasswordFile)
	if err != nil {
		return "", fmt.Errorf("reading password file: %w", err)
	}
	plain := strings.TrimRight(string(data), "\r\n")
	salt, err := newCryptSalt()
	if err != nil {
		return "", err
	}
	return sha512Crypt(plain, salt), nil
}

// writeHomeTmpfiles drops a tmpfiles.d snippet under <root>/etc/tmpfiles.d that
// makes the first boot create /var/home/<user> from /etc/skel if missing and
// restore ownership + SELinux labels under the live policy — offline useradd
// cannot label correctly, and on composefs-native it cannot create the home at
// all. /home -> var/home is a bootc invariant, so the runtime /var/home path is
// correct for both ostree and composefs deploys.
func writeHomeTmpfiles(root, username string) error {
	tmpfilesDir := filepath.Join(root, "etc", "tmpfiles.d")
	if err := os.MkdirAll(tmpfilesDir, 0o755); err != nil {
		return fmt.Errorf("mkdir tmpfiles.d: %w", err)
	}
	// Z mode "-": fix ownership and restore SELinux contexts recursively but
	// keep each file's own mode — 0700 here would mark every migrated document
	// executable.
	snippet := fmt.Sprintf(
		"C /var/home/%[1]s 0700 %[1]s %[1]s - /etc/skel\nZ /var/home/%[1]s - %[1]s %[1]s -\n",
		username)
	snippetPath := filepath.Join(tmpfilesDir, "firn-home-"+username+".conf")
	if err := os.WriteFile(snippetPath, []byte(snippet), 0o644); err != nil {
		return fmt.Errorf("writing home tmpfiles snippet: %w", err)
	}
	return nil
}

// WriteRootAuthorizedKey writes an SSH authorized_keys entry for root into
// the deployment's root home (root/.ssh/authorized_keys under the
// deployment root). New in firn: fisherman's TUI collected SSH keys but
// dropped them before install — a known bug this closes.
func (w *DeploymentWriter) WriteRootAuthorizedKey(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	lay, err := w.resolve(ctx)
	if err != nil {
		return err
	}
	rootHome := filepath.Join(lay.root, "root")
	if err := os.MkdirAll(rootHome, 0o700); err != nil {
		return fmt.Errorf("mkdir root home: %w", err)
	}
	return appendAuthorizedKey(filepath.Join(rootHome, ".ssh"), key)
}

// WriteUserAuthorizedKey writes an SSH authorized_keys entry for username
// into the user's stateroot home (var/home/<user>/.ssh/authorized_keys under
// the stateroot var — the path the booted system mounts at /var). New in
// firn: fisherman's TUI collected SSH keys but dropped them before install.
//
// Ownership must match the user created by useradd: the uid/gid useradd
// chose are read back from the deployment's etc/passwd, since a host-side
// lookup would consult the wrong passwd database.
//
// Note: pre-creating the stateroot home means the tmpfiles.d "C" entry from
// CreateUser finds the directory existing on first boot and skips the
// /etc/skel copy; the "Z" entry still fixes ownership and SELinux labels.
func (w *DeploymentWriter) WriteUserAuthorizedKey(ctx context.Context, username, key string) error {
	if username == "" || key == "" {
		return nil
	}
	lay, err := w.resolve(ctx)
	if err != nil {
		return err
	}
	uid, gid, err := lookupUser(lay.etcDir, username)
	if err != nil {
		return err
	}
	home := filepath.Join(w.staterootVarDir(lay), "home", username)
	if err := appendAuthorizedKey(filepath.Join(home, ".ssh"), key); err != nil {
		return err
	}
	// chown recursively so a freshly created home (composefs path, where no
	// home exists yet) ends up owned by the user, not root.
	if _, err := w.Runner.Run(ctx, "chown", "-R", fmt.Sprintf("%d:%d", uid, gid), home); err != nil {
		return fmt.Errorf("chown %s: %w", home, err)
	}
	return nil
}

// appendAuthorizedKey appends key to sshDir/authorized_keys, creating the
// directory with mode 0700 and the file with mode 0600 (sshd refuses
// group/world-accessible key files).
func appendAuthorizedKey(sshDir, key string) error {
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", sshDir, err)
	}
	// MkdirAll's mode is masked by umask; pin it explicitly.
	if err := os.Chmod(sshDir, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", sshDir, err)
	}
	path := filepath.Join(sshDir, "authorized_keys")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.TrimSpace(key) + "\n"); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// lookupUser reads uid and gid for username from the deployment's
// etc/passwd — the database useradd just wrote, which is authoritative for
// the installed system (the host's passwd would give the wrong ids).
func lookupUser(etcDir, username string) (uid, gid int, err error) {
	passwdPath := filepath.Join(etcDir, "passwd")
	data, err := os.ReadFile(passwdPath)
	if err != nil {
		return 0, 0, fmt.Errorf("reading %s: %w", passwdPath, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 4 || fields[0] != username {
			continue
		}
		uid, uidErr := strconv.Atoi(fields[2])
		gid, gidErr := strconv.Atoi(fields[3])
		if uidErr != nil || gidErr != nil {
			return 0, 0, fmt.Errorf("malformed passwd entry for %q in %s", username, passwdPath)
		}
		return uid, gid, nil
	}
	return 0, 0, fmt.Errorf("user %q not found in %s", username, passwdPath)
}

// sha512CryptAlphabet is the crypt(5) base64 alphabet (not RFC 4648).
const sha512CryptAlphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// newCryptSalt returns a random 16-character salt from the crypt alphabet.
func newCryptSalt() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}
	for i := range b {
		b[i] = sha512CryptAlphabet[int(b[i])%len(sha512CryptAlphabet)]
	}
	return string(b), nil
}

// sha512Crypt implements crypt(5) sha512crypt (Drepper's SHA-512 scheme,
// id $6$) in pure Go with the default 5000 rounds, so plaintext password
// files never leave the installer process unhashed and chpasswd can always
// run with -e (no PAM needed in an etc-only composefs deploy root).
func sha512Crypt(password, salt string) string {
	if len(salt) > 16 {
		salt = salt[:16]
	}
	key := []byte(password)
	sb := []byte(salt)

	// Digest B = SHA512(password + salt + password).
	h := sha512.New()
	h.Write(key)
	h.Write(sb)
	h.Write(key)
	B := h.Sum(nil)

	// Digest A = SHA512(password + salt + B repeated to len(password) +
	// per-bit-of-len(password) B-or-password blocks).
	h.Reset()
	h.Write(key)
	h.Write(sb)
	for cnt := len(key); cnt > 0; cnt -= 64 {
		if cnt > 64 {
			h.Write(B)
		} else {
			h.Write(B[:cnt])
		}
	}
	for cnt := len(key); cnt > 0; cnt >>= 1 {
		if cnt&1 != 0 {
			h.Write(B)
		} else {
			h.Write(key)
		}
	}
	A := h.Sum(nil)

	// P sequence: SHA512(password repeated len(password) times), truncated
	// and repeated to len(password) bytes.
	h.Reset()
	for i := 0; i < len(key); i++ {
		h.Write(key)
	}
	DP := h.Sum(nil)
	P := make([]byte, 0, len(key))
	for cnt := len(key); cnt > 0; cnt -= 64 {
		if cnt > 64 {
			P = append(P, DP...)
		} else {
			P = append(P, DP[:cnt]...)
		}
	}

	// S sequence: SHA512(salt repeated 16+A[0] times), truncated and
	// repeated to len(salt) bytes.
	h.Reset()
	for i := 0; i < 16+int(A[0]); i++ {
		h.Write(sb)
	}
	DS := h.Sum(nil)
	S := make([]byte, 0, len(sb))
	for cnt := len(sb); cnt > 0; cnt -= 64 {
		if cnt > 64 {
			S = append(S, DS...)
		} else {
			S = append(S, DS[:cnt]...)
		}
	}

	// 5000 rounds of alternating recombination.
	C := A
	for i := 0; i < 5000; i++ {
		h.Reset()
		if i%2 != 0 {
			h.Write(P)
		} else {
			h.Write(C)
		}
		if i%3 != 0 {
			h.Write(S)
		}
		if i%7 != 0 {
			h.Write(P)
		}
		if i%2 != 0 {
			h.Write(C)
		} else {
			h.Write(P)
		}
		C = h.Sum(nil)
	}

	// crypt base64 with the sha512crypt byte permutation.
	out := make([]byte, 0, 86)
	b64 := func(b2, b1, b0 byte, n int) {
		v := uint32(b2)<<16 | uint32(b1)<<8 | uint32(b0)
		for i := 0; i < n; i++ {
			out = append(out, sha512CryptAlphabet[v&0x3f])
			v >>= 6
		}
	}
	b64(C[0], C[21], C[42], 4)
	b64(C[22], C[43], C[1], 4)
	b64(C[44], C[2], C[23], 4)
	b64(C[3], C[24], C[45], 4)
	b64(C[25], C[46], C[4], 4)
	b64(C[47], C[5], C[26], 4)
	b64(C[6], C[27], C[48], 4)
	b64(C[28], C[49], C[7], 4)
	b64(C[50], C[8], C[29], 4)
	b64(C[9], C[30], C[51], 4)
	b64(C[31], C[52], C[10], 4)
	b64(C[53], C[11], C[32], 4)
	b64(C[12], C[33], C[54], 4)
	b64(C[34], C[55], C[13], 4)
	b64(C[56], C[14], C[35], 4)
	b64(C[15], C[36], C[57], 4)
	b64(C[37], C[58], C[16], 4)
	b64(C[59], C[17], C[38], 4)
	b64(C[18], C[39], C[60], 4)
	b64(C[40], C[61], C[19], 4)
	b64(C[62], C[20], C[41], 4)
	b64(0, 0, C[63], 2)
	return "$6$" + salt + "$" + string(out)
}
