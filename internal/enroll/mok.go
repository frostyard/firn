// Ported from frostyard/snosi (GPL-3.0-only), shared/native-installer/tree/usr/libexec/snosi-install (mok_import).
package enroll

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/frostyard/firn/internal/runner"
)

// StageMOK stages a certificate for shim's MokManager on the next boot.
// mokutil requires the plaintext password on argv while generating its hash;
// it is briefly visible to other local processes through /proc and ps.
func StageMOK(ctx context.Context, r *runner.Runner, certPath, passwordFile string) error {
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("enroll: MOK certificate not found: %s: %w", certPath, err)
	}
	pw, err := os.ReadFile(passwordFile)
	if err != nil {
		return fmt.Errorf("enroll: read MOK password file: %w", err)
	}
	password := strings.TrimRight(string(pw), "\n")
	hash, err := r.Run(ctx, "mokutil", "--generate-hash="+password)
	if err != nil {
		return fmt.Errorf("enroll: mokutil --generate-hash: %w", err)
	}
	hashFile, err := os.CreateTemp("", "firn-enroll-mok-hash-")
	if err != nil {
		return fmt.Errorf("enroll: MOK hash scratch file: %w", err)
	}
	hashPath := hashFile.Name()
	defer os.Remove(hashPath)
	if err := hashFile.Chmod(0o600); err != nil {
		hashFile.Close()
		return fmt.Errorf("enroll: chmod MOK hash file: %w", err)
	}
	if _, err := hashFile.Write(hash); err != nil {
		hashFile.Close()
		return fmt.Errorf("enroll: write MOK hash file: %w", err)
	}
	if err := hashFile.Close(); err != nil {
		return fmt.Errorf("enroll: close MOK hash file: %w", err)
	}

	// mokutil wants DER; the shipped cert is PEM.
	der, err := os.CreateTemp("", "firn-enroll-mok-*.der")
	if err != nil {
		return fmt.Errorf("enroll: MOK DER scratch file: %w", err)
	}
	derPath := der.Name()
	der.Close()
	defer os.Remove(derPath)
	if _, err := r.Run(ctx, "openssl", "x509", "-in", certPath, "-outform", "DER", "-out", derPath); err != nil {
		return fmt.Errorf("enroll: openssl x509 DER conversion: %w", err)
	}
	if _, err := r.Run(ctx, "mokutil", "--import", derPath, "--hash-file", hashPath); err != nil {
		return fmt.Errorf("enroll: mokutil --import: %w", err)
	}
	return nil
}
