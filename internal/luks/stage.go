// Ported from frostyard/fisherman (GPL-3.0-only),
// fisherman/internal/luks/luks.go (StageFirstBootEnrollment, UUID).
package luks

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/frostyard/firn/internal/runner"
)

// UUID returns the UUID of the LUKS container on partition, or "" on
// any error.
func UUID(ctx context.Context, r *runner.Runner, partition string) string {
	out, err := r.Run(ctx, "cryptsetup", "luksUUID", partition)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// StageFirstBootEnrollment installs a oneshot into the target that
// enrolls the TPM2 token on the FIRST BOOT of the installed system,
// then shreds the transient key and disables itself.
//
// Why not enroll during install: systemd-cryptenroll --tpm2-pcrs=7
// seals against PCR 7 (Secure Boot state) as measured RIGHT NOW —
// inside the live installer. The installed system boots a different
// chain and measures a different PCR 7, so an install-time enrollment
// can never unseal on first boot (observed: the LUKS E2E dropped to a
// passphrase prompt). Enrolling in the booted target captures the
// correct PCR 7.
//
// Deviation from fisherman: fisherman wrote the unit into the physical
// root's /usr/lib/systemd/system, which is inert on composefs targets
// (the booted /usr comes from the deployment image, not the physical
// root). Firn stages both the key and the unit into the deployment's
// writable /etc (etcDir), enabling via a wants symlink to
// /etc/systemd/system.
//
// luksUUID identifies the LUKS partition (by-uuid is stable across the
// install→installed device renumbering); key is the transient unlock
// passphrase/recovery key.
func StageFirstBootEnrollment(etcDir, luksUUID, key string) error {
	if luksUUID == "" {
		return fmt.Errorf("first-boot TPM2 enrollment needs the LUKS UUID")
	}
	keyDir := etcDir + "/firn"
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", keyDir, err)
	}
	keyPath := keyDir + "/tpm2-enroll.key"
	if err := os.WriteFile(keyPath, []byte(key), 0o600); err != nil {
		return fmt.Errorf("write transient key: %w", err)
	}

	// The oneshot: enroll against the running system's PCR 7, shred the
	// key, and disable itself so it never runs again. Idempotent — a
	// second run (key already gone) is a clean no-op.
	unit := `[Unit]
Description=Firn first-boot TPM2 LUKS enrollment
Documentation=https://github.com/frostyard/firn
ConditionPathExists=/etc/firn/tpm2-enroll.key
After=basic.target
DefaultDependencies=no

[Service]
Type=oneshot
RemainAfterExit=no
ExecStart=/usr/bin/systemd-cryptenroll --tpm2-device=auto --tpm2-pcrs=7 --unlock-key-file=/etc/firn/tpm2-enroll.key /dev/disk/by-uuid/` + luksUUID + `
ExecStartPost=-/usr/bin/shred -u /etc/firn/tpm2-enroll.key
ExecStartPost=-/usr/bin/systemctl disable firn-tpm2-enroll.service

[Install]
WantedBy=multi-user.target
`
	unitDir := etcDir + "/systemd/system"
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", unitDir, err)
	}
	unitPath := unitDir + "/firn-tpm2-enroll.service"
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	// Enable via wants symlink (systemctl enable isn't available
	// offline).
	wantsDir := unitDir + "/multi-user.target.wants"
	if err := os.MkdirAll(wantsDir, 0o755); err != nil {
		return fmt.Errorf("mkdir wants: %w", err)
	}
	link := wantsDir + "/firn-tpm2-enroll.service"
	_ = os.Remove(link)
	if err := os.Symlink("/etc/systemd/system/firn-tpm2-enroll.service", link); err != nil {
		return fmt.Errorf("enable unit: %w", err)
	}
	return nil
}
