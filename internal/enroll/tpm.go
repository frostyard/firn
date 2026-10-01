// Ported from frostyard/snosi (GPL-3.0-only), shared/native-installer/tree/usr/libexec/snosi-install (extract_pcrpkey_from_disk, enroll_tpm_var).
package enroll

import (
	"context"
	"fmt"
	"os"

	"github.com/frostyard/firn/internal/runner"
)

// EnrollTPMFromUKI enrolls a TPM token bound only to signed PCR 11, with
// raw PCRs and pcrlock disabled. The signing key is extracted from the
// installed UKI's own .pcrpkey section so updates signed by the same key
// remain unlockable without re-enrollment. unlockKeyFile holds the current
// LUKS passphrase needed to add the token.
func EnrollTPMFromUKI(ctx context.Context, r *runner.Runner, uki, luksDev, unlockKeyFile string) error {
	pcrpkey, err := os.CreateTemp("", "firn-enroll-pcrpkey-")
	if err != nil {
		return fmt.Errorf("enroll: pcrpkey scratch file: %w", err)
	}
	pcrpkeyPath := pcrpkey.Name()
	pcrpkey.Close()
	defer os.Remove(pcrpkeyPath)

	// The COPY TARGET must be a real scratch file, never /dev/null:
	// root-caused live (snosi test/native-installer-e2e-test.sh's first
	// real install, 2026-07-15) — objcopy writes the dump-section but
	// reports "objcopy: /dev/null: file truncated" and exits 1.
	copyTarget, err := os.CreateTemp("", "firn-enroll-uki-copy-")
	if err != nil {
		return fmt.Errorf("enroll: objcopy scratch file: %w", err)
	}
	copyTargetPath := copyTarget.Name()
	copyTarget.Close()
	defer os.Remove(copyTargetPath)
	if _, err := r.Run(ctx, "objcopy", "--dump-section", ".pcrpkey="+pcrpkeyPath, uki, copyTargetPath); err != nil {
		return fmt.Errorf("enroll: objcopy --dump-section .pcrpkey from %s: %w", uki, err)
	}
	if fi, err := os.Stat(pcrpkeyPath); err != nil || fi.Size() == 0 {
		return fmt.Errorf("enroll: extracted .pcrpkey section is empty")
	}
	if _, err := r.Run(ctx, "systemd-cryptenroll",
		"--tpm2-device=auto", "--tpm2-pcrs=", "--tpm2-pcrlock=",
		"--tpm2-public-key="+pcrpkeyPath, "--tpm2-public-key-pcrs=11",
		"--unlock-key-file="+unlockKeyFile, luksDev); err != nil {
		return fmt.Errorf("enroll: systemd-cryptenroll %s: %w", luksDev, err)
	}
	return nil
}
