// Ported from frostyard/snosi (GPL-3.0-only), shared/native-installer/tree/usr/libexec/snosi-install (stream_download_verify, validate_disk_image_layout, relocate_and_grow_var, format_var, enroll_tpm_var, mok_import).

package abimg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/frostyard/firn/internal/runner"
)

// EnrollTPMVar ports extract_pcrpkey_from_disk + enroll_tpm_var:
// enroll a TPM2 unlock token on luksDev bound to signed PCR policy —
// empty raw PCR set, signed PCR 11 only, pcrlock explicitly disabled
// (mirroring snosi's enroll_token contract EXACTLY). Binding only the
// SIGNED PCR 11 policy means any UKI whose PCR 11 measurement is
// signed by the same key unlocks /var — updates keep working without
// re-enrollment — while raw-PCR and pcrlock bindings, which would break
// on every firmware or kernel change, are explicitly emptied so
// systemd-cryptenroll cannot default them on.
//
// The signing public key comes from the disk's OWN just-written UKI's
// .pcrpkey PE section (objcopy --dump-section), never an
// externally-supplied key — self-contained, no separate key
// distribution needed at install time. The UKI is expected at
// EFI/Linux/<channel>_<version>.efi under espMount (the initial
// install always ships exactly this name, no +l-d update suffix).
//
// The caller mounts the ESP at espMount and opens the LUKS device;
// this function only extracts and enrolls. unlockKeyFile is a 0600
// file holding the existing LUKS passphrase (the generated recovery
// key) — cryptenroll needs it to unlock the volume before it can add
// the TPM2 token (snosi passed --unlock-key-file the same way).
func EnrollTPMVar(ctx context.Context, r *runner.Runner, espMount, channel, version, luksDev, unlockKeyFile string) error {
	uki := filepath.Join(espMount, "EFI", "Linux", channel+"_"+version+".efi")
	if _, err := os.Stat(uki); err != nil {
		return fmt.Errorf("abimg: installed UKI not found at EFI/Linux/%s_%s.efi: %w", channel, version, err)
	}

	pcrpkey, err := os.CreateTemp("", "firn-abimg-pcrpkey-")
	if err != nil {
		return fmt.Errorf("abimg: pcrpkey scratch file: %w", err)
	}
	pcrpkeyPath := pcrpkey.Name()
	pcrpkey.Close()
	defer os.Remove(pcrpkeyPath)

	// The COPY TARGET must be a real scratch file, never /dev/null:
	// root-caused live (snosi test/native-installer-e2e-test.sh's first
	// real install, 2026-07-15) — objcopy still WRITES the dump-section
	// output correctly when the copy target is /dev/null, but then
	// reports "objcopy: /dev/null: file truncated" and exits 1 anyway
	// (confirmed independently against a real signed .efi), which
	// fatally aborted the whole installer immediately after the desired
	// section had already been extracted — the non-empty-output check
	// below, which is the ACTUAL success signal, never even ran.
	copyTarget, err := os.CreateTemp("", "firn-abimg-uki-copy-")
	if err != nil {
		return fmt.Errorf("abimg: objcopy scratch file: %w", err)
	}
	copyTargetPath := copyTarget.Name()
	copyTarget.Close()
	defer os.Remove(copyTargetPath)

	if _, err := r.Run(ctx, "objcopy", "--dump-section", ".pcrpkey="+pcrpkeyPath, uki, copyTargetPath); err != nil {
		return fmt.Errorf("abimg: objcopy --dump-section .pcrpkey from %s: %w", uki, err)
	}
	if fi, err := os.Stat(pcrpkeyPath); err != nil || fi.Size() == 0 {
		return fmt.Errorf("abimg: extracted .pcrpkey section is empty")
	}

	if _, err := r.Run(ctx, "systemd-cryptenroll",
		"--tpm2-device=auto",
		"--tpm2-pcrs=",
		"--tpm2-pcrlock=",
		"--tpm2-public-key="+pcrpkeyPath,
		"--tpm2-public-key-pcrs=11",
		"--unlock-key-file="+unlockKeyFile,
		luksDev,
	); err != nil {
		return fmt.Errorf("abimg: systemd-cryptenroll %s: %w", luksDev, err)
	}
	return nil
}
