// Ported from frostyard/fisherman (GPL-3.0-only), fisherman/internal/secure/espstage.go.

// Package secureboot stages the UEFI Secure Boot chain for bootc installs:
// the ESP boot components (shim + MOK-signed second stage + MokManager)
// and the secure-image contract they come from. It is the installer-side
// half of snosi's secure bootc design (ADR-0014); the runtime bootloader
// reconciler is image-owned and firn ships none.
package secureboot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/frostyard/firn/internal/runner"
)

// ESP chain component paths, relative to the image root.
//
// The `.signed` suffixes are load-bearing. Debian splits shim across two
// packages that install into the SAME directory:
//
//	shim-unsigned          usr/lib/shim/shimx64.efi          <- NO signature
//	shim-signed            usr/lib/shim/shimx64.efi.signed   <- Microsoft-signed
//	shim-helpers-*-signed  usr/lib/shim/mmx64.efi.signed     <- Debian-signed
//
// Both names are present in the image, and the unsigned one has the more
// obvious spelling. Staging it produces an ESP that firmware refuses at the
// very first hop, with only `BdsDxe: failed to load Boot0001 ... Access
// Denied` on the console to say so — no shim, so no `Security Violation`
// either. snosi's own native-installer takes the `.signed` binaries
// (shared/native-installer/tools/build-iso.sh); this must match it.
const (
	imageShim        = "usr/lib/shim/shimx64.efi.signed"
	imageMokManager  = "usr/lib/shim/mmx64.efi.signed"
	imageSecondStage = "usr/lib/snosi/bootc/systemd-bootx64.efi"
)

// StageESPChain writes the Secure Boot chain onto a freshly installed ESP.
//
// `bootc install --bootloader systemd` writes plain systemd-boot as
// EFI/BOOT/BOOTX64.EFI and knows nothing about shim, so a secure install ends
// with an ESP that firmware will not accept. The chain the secure design needs
// is the one the native A/B image already ships:
//
//	EFI/BOOT/BOOTX64.EFI   shim, Microsoft-signed -- what firmware runs
//	EFI/BOOT/grubx64.efi   MOK-signed systemd-boot -- what shim chainloads
//	EFI/BOOT/mmx64.efi     MokManager -- enrolls the MOK on first boot
//
// This is the step that was missing entirely on firn's bootc path (ADR-0014):
// on the native A/B path the chain is placed by mkosi/repart, and on the bootc
// path it was placed by nobody.
//
// targetDir is the mounted target root (its ESP is at boot/efi/EFI/BOOT);
// imageRoot is the extracted secure-image tree (see ExtractSecureImageRoot);
// mokCertificate is the contract's absolute in-image path (e.g.
// /usr/lib/snosi/mok.crt), resolved against imageRoot.
//
// Idempotent: an ESP that already carries the chain is left alone, so this is
// safe to call on an existing install.
func StageESPChain(ctx context.Context, r *runner.Runner, targetDir, imageRoot, mokCertificate string) error {
	boot := filepath.Join(targetDir, "boot/efi/EFI/BOOT")
	if _, err := os.Stat(boot); err != nil {
		return fmt.Errorf("secureboot: locating installed ESP boot directory: %w", err)
	}

	staged := 0
	for _, name := range []string{"BOOTX64.EFI", "grubx64.efi", "mmx64.efi"} {
		if _, err := os.Stat(filepath.Join(boot, name)); err == nil {
			staged++
		}
	}
	if staged == 3 {
		return nil
	}

	// The second stage is checked against the MOK, because that is the binary
	// snosi signs with it. shim and MokManager are signed by Microsoft and
	// Debian respectively, so the MOK is the wrong certificate for them and
	// their trust is established by firmware at boot rather than here.
	//
	// They are still checked, just for a weaker property: that they carry a
	// signature at all. That is exactly the property that distinguishes
	// `shimx64.efi` from `shimx64.efi.signed`, and an earlier version of the
	// fisherman original staged the unsigned pair while a comment there
	// asserted they were Microsoft-signed. An assertion in a comment cannot
	// fail; this can.
	if mokCertificate == "" || !strings.HasPrefix(mokCertificate, "/") {
		return fmt.Errorf("secureboot: validating MOK certificate path for ESP staging")
	}
	certificate := filepath.Join(imageRoot, strings.TrimPrefix(mokCertificate, "/"))
	secondStage := filepath.Join(imageRoot, imageSecondStage)
	if _, err := r.Run(ctx, "sbverify", "--cert", certificate, secondStage); err != nil {
		return fmt.Errorf("secureboot: verifying MOK-signed second stage before staging: %w", err)
	}
	for _, component := range []string{imageShim, imageMokManager} {
		if err := assertPESigned(ctx, r, filepath.Join(imageRoot, component)); err != nil {
			return err
		}
	}

	// shim LAST: until it is in place the firmware entry point is still bootc's
	// plain systemd-boot, which at least boots without Secure Boot. Writing
	// shim before its chainload target exists would leave an ESP that cannot
	// boot at all if this is interrupted.
	for _, component := range []struct{ source, target string }{
		{imageSecondStage, "grubx64.efi"},
		{imageMokManager, "mmx64.efi"},
		{imageShim, "BOOTX64.EFI"},
	} {
		if err := copyESPComponent(filepath.Join(imageRoot, component.source), filepath.Join(boot, component.target)); err != nil {
			return err
		}
	}
	return nil
}

// assertPESigned refuses a PE binary that carries no Authenticode signature.
//
// It deliberately does not check WHO signed it: shim is Microsoft-signed and
// MokManager is Debian-signed, the trusted CAs live in the firmware's db, and
// pinning issuer strings here would break on the next Debian signing-key
// rotation for no security gain. "Has a signature table" is the weakest useful
// property and the one that catches staging an unsigned variant by name.
//
// `sbverify --list` exits 0 for signed and unsigned binaries alike — it is
// reporting, not verifying — so the exit code says nothing and the output has
// to be read.
func assertPESigned(ctx context.Context, r *runner.Runner, path string) error {
	out, err := r.Run(ctx, "sbverify", "--list", path)
	if err != nil {
		return fmt.Errorf("secureboot: listing signatures on ESP component %s: %w", filepath.Base(path), err)
	}
	if !strings.Contains(string(out), "image signature issuers:") {
		return fmt.Errorf("secureboot: refusing to stage unsigned ESP component %s: firmware would reject it with EFI_ACCESS_DENIED (want the .signed variant)", filepath.Base(path))
	}
	return nil
}

// copyESPComponent writes one binary to the ESP and flushes it. The ESP is FAT
// on a disk about to be rebooted into; an unflushed write is a brick. Pure file
// manipulation on the mounted tree, so it stays on os/filepath rather than the
// runner seam (port-from-parents: the sysconfig split).
func copyESPComponent(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("secureboot: reading ESP component %s: %w", filepath.Base(source), err)
	}
	if len(data) == 0 {
		return fmt.Errorf("secureboot: refusing to stage empty ESP component %s", filepath.Base(source))
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("secureboot: opening ESP component %s: %w", filepath.Base(target), err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("secureboot: writing ESP component %s: %w", filepath.Base(target), err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("secureboot: flushing ESP component %s: %w", filepath.Base(target), err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("secureboot: closing ESP component %s: %w", filepath.Base(target), err)
	}
	return nil
}
