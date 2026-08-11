// Ported from frostyard/snosi (GPL-3.0-only), shared/native-installer/tree/usr/libexec/snosi-install (seed_var, seed_first_user).

// This file holds the A/B "overlay writer" (docs/design/architecture.md,
// "System configuration"): the counterpart of the bootc deployment writer
// for snosi-style native A/B images. The installed image's root filesystem
// is a dm-verity-sealed erofs — read-only at install time and at runtime —
// so every mutable /etc write lands in the overlayfs UPPER directory on the
// var partition, and the pristine baseline is only ever read.
package sysconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/frostyard/firn/internal/runner"
)

// OverlayWriter writes system configuration for an A/B (snosi-style)
// install. VarDir is the root of the mounted target /var filesystem (the
// ext4/btrfs volume the booted system mounts at /var); RootDir is the
// read-only mounted erofs root image, whose pristine /etc baseline lives
// at RootDir/etc and is only ever consulted, never written. Runner is kept
// for parity with DeploymentWriter; the overlay writer is pure file
// manipulation on already-mounted filesystems and does not currently need
// host command execution.
type OverlayWriter struct {
	VarDir  string
	RootDir string
	Runner  *runner.Runner
}

// overlayNow is the clock seam for the install-info timestamp and the
// shadow lastchg field; tests pin it. Restore with time.Now.
var overlayNow = time.Now

// chownFn is the ownership seam. The installer runs as root, where
// os.Lchown always succeeds; tests (typically non-root) replace it to
// record intent instead. Lchown rather than Chown so symlinks copied from
// /etc/skel get owned without following the link — on anything that is not
// a symlink it behaves exactly like Chown.
var chownFn = os.Lchown

// SetChownForTesting swaps the ownership seam and returns a restore
// func — for cross-package integration tests that run unprivileged.
func SetChownForTesting(fn func(path string, uid, gid int) error) (restore func()) {
	orig := chownFn
	chownFn = fn
	return func() { chownFn = orig }
}

// upperDir returns the /etc-overlay upper directory inside the mounted var
// filesystem: lib/snosi/etc-overlay/upper, i.e. /var/lib/snosi/etc-overlay/
// upper on the booted system. This exact path is a CONTRACT with the
// installed image, not a firn choice: the image's 95etc-overlay dracut
// module (shared/outformat/ab-root/tree/usr/lib/dracut/modules.d/
// 95etc-overlay/etc-overlay-mount.sh) mounts an overlayfs over /etc with
// precisely this upper at boot, so anything written here overrides the
// image's pristine /etc.
func (w *OverlayWriter) upperDir() string {
	return filepath.Join(w.VarDir, "lib", "snosi", "etc-overlay", "upper")
}

// ensureUpper creates the overlay upper directory (0755, as snosi's
// `install -d -m 0755` did) and returns its path.
func (w *OverlayWriter) ensureUpper() (string, error) {
	upper := w.upperDir()
	if err := os.MkdirAll(upper, 0o755); err != nil {
		return "", fmt.Errorf("mkdir overlay upper: %w", err)
	}
	return upper, nil
}

// writeFileAtomic writes data to path with the given mode via a temp file
// in the same directory plus an atomic rename, so a crash mid-write never
// leaves a truncated account database (or any half-written config) behind.
//
// os.CreateTemp creates the temp 0600 (like mktemp). Renaming a 0600 temp
// straight over /etc/group is exactly how snosi's append_group_member once
// shipped a broken system (2026-07-17): /etc/group must stay 0644 or
// NON-root group-name resolution breaks entirely (getent group empty,
// `id -nG` fails) even though numeric memberships survive. The temp is
// therefore chmodded to the target mode BEFORE the rename, so no window
// exists in which the final path carries the wrong mode.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp for %s: %w", path, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename has succeeded
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmp, path, err)
	}
	return nil
}

// WriteHostname writes the overlay upper's hostname file. Empty hostname is
// a no-op, matching seed_var's "all empty -> writes nothing".
func (w *OverlayWriter) WriteHostname(hostname string) error {
	if hostname == "" {
		return nil
	}
	upper, err := w.ensureUpper()
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(upper, "hostname"), []byte(hostname+"\n"), 0o644)
}

// WriteLocale writes the overlay upper's locale.conf with a single LANG=
// line, seed_var's exact format.
func (w *OverlayWriter) WriteLocale(locale string) error {
	if locale == "" {
		return nil
	}
	upper, err := w.ensureUpper()
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(upper, "locale.conf"), []byte("LANG="+locale+"\n"), 0o644)
}

// WriteTimezone writes the overlay upper's timezone file and the localtime
// symlink. The symlink target is RELATIVE — ../usr/share/zoneinfo/<tz>,
// the exact form seed_var's `ln -sfn` wrote: resolved from /etc on the
// booted system it lands on /usr/share/zoneinfo/<tz>, and it must stay
// relative so it resolves inside the overlay-mounted root rather than on
// the installer's host.
func (w *OverlayWriter) WriteTimezone(tz string) error {
	if tz == "" {
		return nil
	}
	// tz becomes a symlink target component; snosi validated it at the CLI
	// (TIMEZONE_RE rejects e.g. "America/../../etc"). Guard here too since
	// this method is the last stop before the path is written.
	if strings.HasPrefix(tz, "/") || strings.Contains(tz, "..") {
		return fmt.Errorf("invalid timezone %q", tz)
	}
	upper, err := w.ensureUpper()
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(upper, "timezone"), []byte(tz+"\n"), 0o644); err != nil {
		return err
	}
	link := filepath.Join(upper, "localtime")
	// ln -sfn semantics: replace any existing link.
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing old localtime symlink: %w", err)
	}
	if err := os.Symlink("../usr/share/zoneinfo/"+tz, link); err != nil {
		return fmt.Errorf("symlinking localtime: %w", err)
	}
	return nil
}

// WriteKeyboard writes the overlay upper's default/keyboard file from a
// LAYOUT[:VARIANT[:MODEL]] spec.
//
// Debian images ship /etc/vconsole.conf as a SYMLINK to default/keyboard
// (keyboard-configuration owns the keymap; console-setup.service reasserts
// that shape on first boot — observed live 2026-07-17: a plain-file
// vconsole.conf write was replaced by the symlink on the installed system's
// first boot). So, exactly like seed_var, write the file the symlink
// resolves to, in its keyboard(5) format.
func (w *OverlayWriter) WriteKeyboard(spec string) error {
	if spec == "" {
		return nil
	}
	parts := strings.SplitN(spec, ":", 3)
	layout := parts[0]
	if layout == "" {
		return fmt.Errorf("invalid keyboard spec %q: empty layout", spec)
	}
	var variant, model string
	if len(parts) > 1 {
		variant = parts[1]
	}
	if len(parts) > 2 {
		model = parts[2]
	}
	if model == "" {
		model = "pc105" // seed_var's ${kb_model:-pc105} default
	}
	upper, err := w.ensureUpper()
	if err != nil {
		return err
	}
	dir := filepath.Join(upper, "default")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// Field order and quoting match seed_var byte for byte.
	content := fmt.Sprintf("XKBMODEL=\"%s\"\nXKBLAYOUT=\"%s\"\nXKBVARIANT=\"%s\"\nXKBOPTIONS=\"\"\nBACKSPACE=\"guess\"\n",
		model, layout, variant)
	return writeFileAtomic(filepath.Join(dir, "keyboard"), []byte(content), 0o644)
}

// WriteRootAuthorizedKey writes root's SSH key into the overlay upper at
// ssh/authorized_keys.d/root (0700 dir, 0600 file). The location is a
// contract with the image's sshd config: the sealed root filesystem means
// /root/.ssh/authorized_keys can never be written directly, so sshd is
// pointed at the /etc overlay via shared/outformat/ab-root/tree/etc/ssh/
// sshd_config.d/10-snosi-authorized-keys.conf (AuthorizedKeysFile
// /etc/ssh/authorized_keys.d/%u).
func (w *OverlayWriter) WriteRootAuthorizedKey(key string) error {
	if key == "" {
		return nil
	}
	upper, err := w.ensureUpper()
	if err != nil {
		return err
	}
	dir := filepath.Join(upper, "ssh", "authorized_keys.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// MkdirAll's mode is masked by umask; pin the key directory explicitly.
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return writeFileAtomic(filepath.Join(dir, "root"), []byte(strings.TrimSpace(key)+"\n"), 0o600)
}

// installInfo mirrors the JSON seed_var wrote to lib/snosi/install-info.json,
// plus an installer field identifying firn. seed_var also wrote a "channel"
// field, which this API does not carry (firn recipes have no snosi channel);
// callers needing it must extend this signature.
type installInfo struct {
	Product      string `json:"product"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	InstalledAt  string `json:"installed_at"`
	Installer    string `json:"installer"`
}

// WriteInstallInfo writes lib/snosi/install-info.json on the var filesystem
// (runtime /var/lib/snosi/install-info.json), the record snosi's update
// tooling and e2e checks read on the booted system.
func (w *OverlayWriter) WriteInstallInfo(product, version string) error {
	dir := filepath.Join(w.VarDir, "lib", "snosi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	info := installInfo{
		Product: product,
		Version: version,
		// snosi recorded the artifact's architecture string in systemd style
		// ("x86-64" — pinned by test/native-installer-e2e-test.sh); the
		// installer runs on the target hardware, so map the running GOARCH
		// to the same spelling.
		Architecture: systemdArch(runtime.GOARCH),
		InstalledAt:  overlayNow().UTC().Format("2006-01-02T15:04:05Z"), // date -u +%FT%TZ
		Installer:    "firn",
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling install info: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, "install-info.json"), append(data, '\n'), 0o644)
}

// systemdArch maps a Go GOARCH to systemd's architecture spelling (the one
// snosi's artifact index and install-info.json use). Unmapped values pass
// through unchanged.
func systemdArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86-64"
	case "386":
		return "x86"
	default:
		return goarch
	}
}
