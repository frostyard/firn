// Ported from frostyard/fisherman (GPL-3.0-only), fisherman/internal/disk/format.go
// (Mount, MountType, MountBoot, MountEFI, Umount, UmountRecursive).

package disk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/frostyard/firn/internal/runner"
)

// BtrfsRootMountOpts are the mount options used for the root btrfs subvolume
// layout (@, @home, @snapshots). The installed system's state/deploy tree lives
// inside the @ subvolume, so any (re)mount of the root partition that post-install
// steps write through must use these options; a bare mount would expose the
// btrfs top-level instead, where state/deploy does not exist.
const BtrfsRootMountOpts = "subvol=@,compress=zstd:1"

// MountTarget mounts the install target tree under targetDir: the root
// filesystem first, then the separate /boot (grub2 layout only), then
// the ESP at boot/efi, creating directories as needed. Mounting the
// ESP under the root is required before running `bootc install
// to-filesystem` so bootc can find and install the bootloader.
//
// rootDev is mounted as the root — not layout.Root — because LUKS
// remaps the root partition to a /dev/mapper device. When
// btrfsSubvolumes is set (btrfs only), the root is mounted with
// subvol=@ so writes land inside the @ subvolume where the composefs
// deployment (state/deploy) lives.
func MountTarget(ctx context.Context, r *runner.Runner, layout Layout, rootDev, filesystem string, btrfsSubvolumes bool, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("disk: mkdir %s: %w", targetDir, err)
	}
	opts := ""
	if filesystem == "btrfs" && btrfsSubvolumes {
		opts = BtrfsRootMountOpts
	}
	if err := mount(ctx, r, rootDev, targetDir, filesystem, opts); err != nil {
		return fmt.Errorf("disk: mounting root: %w", err)
	}

	// The root is mounted. Any subsequent mkdir or mount failure must unwind
	// every mount already made under targetDir (root included) so a failed
	// install attempt does not leak the target's mount tree onto the deployer.
	// The cleanup uses a background context so it still runs when ctx is
	// already cancelled, and the original stage error is preserved: a cleanup
	// failure is joined onto it rather than replacing it. A root-mount failure
	// is handled above and never reaches here, so nothing is unmounted when the
	// initial root mount itself failed.
	if err := mountBootAndESP(ctx, r, layout, targetDir); err != nil {
		if cleanupErr := UnmountRecursive(context.Background(), r, targetDir); cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
		return err
	}
	return nil
}

// mountBootAndESP performs the post-root mount stages: the separate /boot
// (grub2 layout only), then the ESP at boot/efi. It assumes the root is
// already mounted at targetDir; MountTarget unwinds the whole tree if this
// returns an error.
func mountBootAndESP(ctx context.Context, r *runner.Runner, layout Layout, targetDir string) error {
	// grub2 layout: separate ext4 /boot. Must be mounted before the ESP
	// so boot/efi can be created inside it.
	if layout.Boot != "" {
		bootDir := filepath.Join(targetDir, "boot")
		if err := os.MkdirAll(bootDir, 0o755); err != nil {
			return fmt.Errorf("disk: mkdir %s: %w", bootDir, err)
		}
		if err := mount(ctx, r, layout.Boot, bootDir, "", ""); err != nil {
			return fmt.Errorf("disk: mounting /boot: %w", err)
		}
	}

	efiDir := filepath.Join(targetDir, "boot", "efi")
	if err := os.MkdirAll(efiDir, 0o755); err != nil {
		return fmt.Errorf("disk: mkdir %s: %w", efiDir, err)
	}
	if err := mount(ctx, r, layout.ESP, efiDir, "", ""); err != nil {
		return fmt.Errorf("disk: mounting ESP: %w", err)
	}
	return nil
}

// mount mounts dev at target. A non-empty fstype (other than zfs) is
// passed explicitly via -t. Prefer an explicit type whenever it is
// known: the deployer initramfs lacks the libblkid probe path mount
// uses to auto-detect, so a typeless mount of a freshly-created xfs
// root was attempted as ext4 and failed "Can't find ext4 filesystem"
// (GH bonito repro 20260724T04xx) — kernel guessing, not a real error.
//
// On failure the error carries a blkid probe of the device: in the
// deployer initramfs, streamed command output never reaches the serial
// console, so "exit status 32" arrived with zero diagnosable context
// (GH run 20260723T2257 burned an hour of archaeology on a message
// that mount itself had already explained). The kernel's ring buffer
// is the only place that says WHY a mount was rejected (unsupported
// feature bits, SB validation, missing module): mount's own stderr
// came back empty in the deployer initramfs (GH bonito repro
// 20260724T0335), so the dmesg tail is appended too.
func mount(ctx context.Context, r *runner.Runner, dev, target, fstype, opts string) error {
	var args []string
	if fstype != "" && fstype != "zfs" {
		args = append(args, "-t", fstype)
	}
	if opts != "" {
		args = append(args, "-o", opts)
	}
	args = append(args, dev, target)
	if _, err := r.Run(ctx, "mount", args...); err != nil {
		probe, _ := r.Run(ctx, "blkid", dev)
		dmesg, _ := r.Run(ctx, "sh", "-c", "dmesg | tail -8")
		return fmt.Errorf("%w (blkid: %s) (dmesg: %s)", err,
			strings.TrimSpace(string(probe)), strings.TrimSpace(string(dmesg)))
	}
	return nil
}

// Unmount unmounts mountpoint (simple, non-recursive).
func Unmount(ctx context.Context, r *runner.Runner, mountpoint string) error {
	if _, err := r.Run(ctx, "umount", mountpoint); err != nil {
		return fmt.Errorf("disk: umount %s: %w", mountpoint, err)
	}
	return nil
}

// UnmountRecursive unmounts targetDir and all mounts beneath it.
func UnmountRecursive(ctx context.Context, r *runner.Runner, targetDir string) error {
	if _, err := r.Run(ctx, "umount", "-R", targetDir); err != nil {
		return fmt.Errorf("disk: umount -R %s: %w", targetDir, err)
	}
	return nil
}
