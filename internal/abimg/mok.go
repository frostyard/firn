// Ported from frostyard/snosi (GPL-3.0-only), shared/native-installer/tree/usr/libexec/snosi-install (stream_download_verify, validate_disk_image_layout, relocate_and_grow_var, format_var, enroll_tpm_var, mok_import).

package abimg

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/frostyard/firn/internal/runner"
)

// StageMOK ports mok_import: generate a password hash and stage the
// MOK certificate import request non-interactively via
// `mokutil --import --hash-file`, for shim's MokManager to complete on
// the next boot. The one-time password is read from passwordFile,
// which the caller creates with mode 0600.
//
// HONEST NOTE on exposure: `mokutil --generate-hash=<password>` takes
// the plaintext password as a CLI argument — mokutil has no stdin/file
// input for hashing — so for the brief duration that one mokutil
// invocation runs, the password IS visible system-wide via
// /proc/<pid>/cmdline and `ps`, to any other local user or process
// that looks in time, not just to this function. This is an inherent
// limitation of mokutil's own interface, not something mitigated here;
// the follow-on hash file it writes IS kept private (0600 scratch
// file, removed immediately after use), and the resulting hash itself
// (not the plaintext) is what mokutil --import stages persistently.
func StageMOK(ctx context.Context, r *runner.Runner, certPath, passwordFile string) error {
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("abimg: MOK certificate not found: %s: %w", certPath, err)
	}
	pw, err := os.ReadFile(passwordFile)
	if err != nil {
		return fmt.Errorf("abimg: read MOK password file: %w", err)
	}
	password := strings.TrimRight(string(pw), "\n")

	hash, err := r.Run(ctx, "mokutil", "--generate-hash="+password)
	if err != nil {
		return fmt.Errorf("abimg: mokutil --generate-hash: %w", err)
	}
	hashFile, err := os.CreateTemp("", "firn-abimg-mok-hash-")
	if err != nil {
		return fmt.Errorf("abimg: MOK hash scratch file: %w", err)
	}
	hashPath := hashFile.Name()
	defer os.Remove(hashPath)
	if err := hashFile.Chmod(0o600); err != nil {
		hashFile.Close()
		return fmt.Errorf("abimg: chmod MOK hash file: %w", err)
	}
	if _, err := hashFile.Write(hash); err != nil {
		hashFile.Close()
		return fmt.Errorf("abimg: write MOK hash file: %w", err)
	}
	if err := hashFile.Close(); err != nil {
		return fmt.Errorf("abimg: close MOK hash file: %w", err)
	}

	// mokutil wants DER; the shipped cert is PEM.
	der, err := os.CreateTemp("", "firn-abimg-mok-*.der")
	if err != nil {
		return fmt.Errorf("abimg: MOK DER scratch file: %w", err)
	}
	derPath := der.Name()
	der.Close()
	defer os.Remove(derPath)
	if _, err := r.Run(ctx, "openssl", "x509", "-in", certPath, "-outform", "DER", "-out", derPath); err != nil {
		return fmt.Errorf("abimg: openssl x509 DER conversion: %w", err)
	}

	if _, err := r.Run(ctx, "mokutil", "--import", derPath, "--hash-file", hashPath); err != nil {
		return fmt.Errorf("abimg: mokutil --import: %w", err)
	}
	return nil
}
