// Package platform probes install-machine facts (UEFI, Secure Boot,
// TPM) that validation and preflight depend on. Probes only read; they
// never modify firmware state.
package platform

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrUEFIRequired is the shared hardware-floor diagnostic for every
// frontend. Firn supports no legacy-BIOS install path (ADR-0004).
var ErrUEFIRequired = errors.New("this machine did not boot via UEFI; firn supports UEFI machines only (docs/adr/0004-single-installer-scope-and-support-matrix.md)")

// EFIVarsDir and device paths are variables for tests.
var (
	efiDir      = "/sys/firmware/efi"
	efiVarsGlob = "/sys/firmware/efi/efivars/SecureBoot-*"
	tpmDevs     = []string{"/dev/tpmrm0", "/dev/tpm0"}
)

// UEFI reports whether the machine booted via UEFI.
func UEFI() bool {
	fi, err := os.Stat(efiDir)
	return err == nil && fi.IsDir()
}

// RequireUEFI enforces firn's hardware floor after probing or applying a
// command-line override.
func RequireUEFI(uefi bool) error {
	if !uefi {
		return ErrUEFIRequired
	}
	return nil
}

// SecureBoot reports whether Secure Boot is active. The SecureBoot
// efivar's payload is 4 bytes of attributes followed by the value
// byte; anything unreadable counts as inactive.
func SecureBoot() bool {
	matches, _ := filepath.Glob(efiVarsGlob)
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err == nil && len(data) >= 5 && data[4] == 1 {
			return true
		}
	}
	return false
}

// TPM reports whether a TPM character device is present.
func TPM() bool {
	for _, dev := range tpmDevs {
		if _, err := os.Stat(dev); err == nil {
			return true
		}
	}
	return false
}
