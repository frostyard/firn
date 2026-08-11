// Ported from frostyard/fisherman (GPL-3.0-only), fisherman/internal/disk/format.go
// (CreateZFSPool from fisherman/internal/disk/zfs.go).

package disk

import (
	"context"
	"fmt"

	"github.com/frostyard/firn/internal/runner"
)

// FormatESP formats a partition as FAT32 for use as the EFI System
// Partition.
func FormatESP(ctx context.Context, r *runner.Runner, dev string) error {
	if _, err := r.Run(ctx, "mkfs.fat", "-F32", "-n", "EFI-SYSTEM", dev); err != nil {
		return fmt.Errorf("disk: formatting ESP: %w", err)
	}
	return nil
}

// FormatBootExt4 formats a partition as ext4 for use as /boot.
// ext4 is used for broad bootloader compatibility.
func FormatBootExt4(ctx context.Context, r *runner.Runner, dev string) error {
	if _, err := r.Run(ctx, "mkfs.ext4", "-L", "boot", "-F", dev); err != nil {
		return fmt.Errorf("disk: formatting /boot: %w", err)
	}
	return nil
}

// FormatRoot formats a device as the root filesystem (btrfs, xfs, or
// ext4).
func FormatRoot(ctx context.Context, r *runner.Runner, dev, filesystem string) error {
	var err error
	switch filesystem {
	case "xfs":
		_, err = r.Run(ctx, "mkfs.xfs", "-f", "-L", "root", dev)
	case "ext4":
		// -O verity is required for composefs (bootc's --composefs-backend uses
		// FS_IOC_ENABLE_VERITY on individual files; ext4 only supports this when
		// the verity feature is enabled at format time).
		_, err = r.Run(ctx, "mkfs.ext4", "-F", "-L", "root", "-O", "verity", dev)
	case "btrfs":
		_, err = r.Run(ctx, "mkfs.btrfs", "-f", "-L", "root", dev)
	default:
		return fmt.Errorf("disk: unsupported filesystem: %q", filesystem)
	}
	if err != nil {
		return fmt.Errorf("disk: formatting root: %w", err)
	}
	return nil
}

// CreateZFSPool creates a new ZFS pool on dev, mounting its root
// dataset at altroot. The pool is created with sane defaults for a
// bootc system:
//   - ashift=12 (4K sectors, recommended for most SSDs/NVMe)
//   - compression=zstd
//   - acltype=posixacl (required for systemd)
//   - xattr=sa (store xattrs in SA for performance)
//   - dnodesize=auto (for large xattrs)
//   - relatime=on (reduce atime write overhead)
//
// The pool root dataset gets mountpoint=/ and is mounted at altroot.
func CreateZFSPool(ctx context.Context, r *runner.Runner, dev, poolName, altroot string) error {
	_, err := r.Run(ctx, "zpool", "create",
		"-R", altroot,
		"-O", "compression=zstd",
		"-O", "mountpoint=/",
		"-O", "acltype=posixacl",
		"-O", "xattr=sa",
		"-O", "dnodesize=auto",
		"-O", "relatime=on",
		"-o", "ashift=12",
		"-f",
		poolName, dev,
	)
	if err != nil {
		return fmt.Errorf("disk: creating ZFS pool %s: %w", poolName, err)
	}
	return nil
}

// CreateSubvolumes creates the @, @home, and @snapshots subvolumes on
// a freshly-formatted btrfs filesystem mounted bare (no subvol option)
// at mountpoint, then sets @ as the btrfs default subvolume.
//
// Setting @ as the default subvolume is required for boot: composefs +
// systemd-boot installs rely on systemd GPT auto-discovery to mount the root
// partition, and the generator mounts whatever btrfs subvolume is default
// (top-level, subvolid 5, unless changed). Without set-default @, the booted
// system would mount the btrfs top-level — where the composefs deployment
// (state/deploy) does not exist — and fail exactly as the install-time bug did,
// only relocated to first boot.
func CreateSubvolumes(ctx context.Context, r *runner.Runner, mountpoint string) error {
	for _, sv := range []string{"@", "@home", "@snapshots"} {
		if _, err := r.Run(ctx, "btrfs", "subvolume", "create", mountpoint+"/"+sv); err != nil {
			return fmt.Errorf("disk: create subvolume %s: %w", sv, err)
		}
	}
	// Make @ the default subvolume so systemd GPT auto-discovery mounts it at
	// boot instead of the btrfs top-level.
	if _, err := r.Run(ctx, "btrfs", "subvolume", "set-default", mountpoint+"/@"); err != nil {
		return fmt.Errorf("disk: set default subvolume @: %w", err)
	}
	return nil
}

// FinalizeFilesystem replicates the three operations bootc performs when
// --skip-finalize is NOT passed (bootc's internal finalize_filesystem()):
//  1. fstrim  — discards unused blocks for SSD health
//  2. remount ro — flushes writeback and locks the deployment read-only
//  3. fsfreeze/thaw — flushes the journal so the first boot is clean
func FinalizeFilesystem(ctx context.Context, r *runner.Runner, mountpoint string) error {
	if _, err := r.Run(ctx, "fstrim", "--quiet-unsupported", "-v", mountpoint); err != nil {
		return fmt.Errorf("disk: fstrim: %w", err)
	}
	if _, err := r.Run(ctx, "mount", "-o", "remount,ro", mountpoint); err != nil {
		return fmt.Errorf("disk: remount ro: %w", err)
	}
	if _, err := r.Run(ctx, "fsfreeze", "-f", mountpoint); err != nil {
		return fmt.Errorf("disk: fsfreeze: %w", err)
	}
	if _, err := r.Run(ctx, "fsfreeze", "-u", mountpoint); err != nil {
		return fmt.Errorf("disk: fsfreeze -u: %w", err)
	}
	return nil
}
