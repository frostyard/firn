// Ported from frostyard/fisherman (GPL-3.0-only), fisherman/internal/disk/partition.go.

package disk

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/frostyard/firn/internal/runner"
)

// Test seams: partitioning sleeps between kernel-visible operations and
// stats partition device nodes before wiping them. Tests replace both
// so no real time passes and no real /dev nodes are consulted.
var (
	sleep = time.Sleep
	stat  = os.Stat
)

// SwapStat replaces the stat function WaitForPartition (and the partition
// wipe poller) consult, returning a restore func. Exported only so
// pipeline-level tests in OTHER packages — which drive WaitForPartition
// through an assembled step (e.g. retag-root) rather than calling it
// directly — can simulate a device node appearing without a real block
// device. Not for production use.
func SwapStat(fn func(string) (os.FileInfo, error)) (restore func()) {
	old := stat
	stat = fn
	return func() { stat = old }
}

// Layout names the partitions created on a disk. Boot is empty for
// layouts without a separate /boot (systemd-boot, ZFS).
type Layout struct {
	Disk string // whole disk, e.g. /dev/sda
	ESP  string // EFI System Partition
	Boot string // separate ext4 /boot (grub2 layout only)
	Root string // root filesystem / LUKS / ZFS pool partition
}

// PartSuffix returns "p" when partition device names on disk need a
// "p" separator (nvme, mmcblk, loop — any name ending in a digit).
func PartSuffix(disk string) string {
	if len(disk) == 0 {
		return ""
	}
	if unicode.IsDigit(rune(disk[len(disk)-1])) {
		return "p"
	}
	return ""
}

// PartName returns the full device path for partition num on disk.
// e.g. PartName("/dev/sda", 2) → "/dev/sda2"
//
//	PartName("/dev/nvme0n1", 2) → "/dev/nvme0n1p2"
func PartName(disk string, num int) string {
	return fmt.Sprintf("%s%s%d", disk, PartSuffix(disk), num)
}

// PartitionGrub2 wipes disk and creates a three-partition GPT layout
// using sfdisk:
//
//	Partition 1 – EFI System (2 GiB)
//	Partition 2 – /boot     (2 GiB, ext4 — GRUB reads this)
//	Partition 3 – Linux root (remaining space)
//
// 2 GiB ESP for fleet consistency with dakota (systemd-boot). Fedora/Anaconda
// defaults to 500-600 MiB but that's for interactive installs that don't need
// to hold multiple kernels on the ESP. Uniform 2 GiB simplifies fleet tooling.
//
// A separate /boot (ext4) is required because GRUB's built-in XFS driver
// does not support newer XFS features (nrext64, rmapbt) on el10.
//
// On real block devices sfdisk notifies the kernel via BLKRRPART, so
// partition devices appear after udevadm settle.
func PartitionGrub2(ctx context.Context, r *runner.Runner, disk string) (Layout, error) {
	script := strings.Join([]string{
		"label: gpt",
		"",
		`size=2GiB, type=uefi, name="EFI-SYSTEM"`,
		`size=2GiB,   type=linux, name="boot"`,
		`type=linux, name="root"`,
	}, "\n") + "\n"
	if err := partition(ctx, r, disk, script); err != nil {
		return Layout{}, err
	}
	return Layout{
		Disk: disk,
		ESP:  PartName(disk, 1),
		Boot: PartName(disk, 2),
		Root: PartName(disk, 3),
	}, nil
}

// PartitionSystemdBoot wipes disk and creates a two-partition GPT
// layout for systemd-boot installs:
//
//	Partition 1 – EFI System (2 GiB — holds bootloader binary, kernel and initrd)
//	Partition 2 – Linux root (remaining space)
//
// Unlike grub2 installs, systemd-boot can only read FAT32 via UEFI firmware
// protocols. A separate ext4 /boot would be unreadable, so everything lands
// directly on the FAT32 ESP. Each kernel+initrd pair is ~200 MiB; 2 GiB
// comfortably accommodates the booted entry, rollback, and a staged upgrade
// without running out of space even if stale entries accumulate.
// This layout is used for both unencrypted and encrypted systemd-boot installs
// (encrypted: LUKS wraps partition 2).
func PartitionSystemdBoot(ctx context.Context, r *runner.Runner, disk string) (Layout, error) {
	script := strings.Join([]string{
		"label: gpt",
		"",
		`size=2GiB, type=uefi, name="EFI-SYSTEM"`,
		`type=linux, name="root"`,
	}, "\n") + "\n"
	if err := partition(ctx, r, disk, script); err != nil {
		return Layout{}, err
	}
	return Layout{
		Disk: disk,
		ESP:  PartName(disk, 1),
		Root: PartName(disk, 2),
	}, nil
}

// PartitionZFS wipes disk and creates a two-partition GPT layout for
// ZFS installs:
//
//	Partition 1 – EFI System (1 GiB — holds bootloader, kernel, initrd)
//	Partition 2 – ZFS pool   (remaining space)
//
// ZFS installs use systemd-boot which reads directly from the FAT32 ESP.
func PartitionZFS(ctx context.Context, r *runner.Runner, disk string) (Layout, error) {
	script := strings.Join([]string{
		"label: gpt",
		"",
		`size=1GiB, type=uefi, name="EFI-SYSTEM"`,
		`type=linux, name="zfs-pool"`,
	}, "\n") + "\n"
	if err := partition(ctx, r, disk, script); err != nil {
		return Layout{}, err
	}
	return Layout{
		Disk: disk,
		ESP:  PartName(disk, 1),
		Root: PartName(disk, 2),
	}, nil
}

// partition is the shared implementation for the Partition* layouts.
// The sfdisk script is fed on stdin.
func partition(ctx context.Context, r *runner.Runner, disk, script string) error {
	// First attempt without --force.
	_, err := r.RunInput(ctx, script, "sfdisk", "--wipe=always", disk)
	if err != nil {
		// The disk may still be considered "in use" by the kernel even after
		// aggressive unmounting (e.g. a race with udev re-probing). Retry once
		// with --force --no-reread so sfdisk writes the table unconditionally.
		sleep(500 * time.Millisecond)
		_, _ = r.Run(ctx, "udevadm", "settle")
		if _, err2 := r.RunInput(ctx, script, "sfdisk", "--wipe=always", "--force", "--no-reread", disk); err2 != nil {
			return fmt.Errorf("disk: sfdisk: %w", err2)
		}
		// --no-reread means the kernel still holds the OLD partition table.
		// Force a re-read with partprobe so subsequent mkfs calls see the
		// correct partition sizes. Without this, mkfs operates on stale
		// (possibly much smaller) partition boundaries → "no space left".
		sleep(500 * time.Millisecond)
		_, _ = r.Run(ctx, "partprobe", disk)
		_, _ = r.Run(ctx, "udevadm", "settle")
		// Wipe stale filesystem signatures that udev re-probes after partprobe.
		// Without this, mkfs fails with "Device or resource busy" on disks
		// that carried a filesystem from a prior aborted install: the kernel's
		// btrfs/ext4 drivers re-open the partition node on udev probe, and
		// mkfs then sees a busy device even though the partition is unmounted.
		for i := 1; i <= 4; i++ {
			part := PartName(disk, i)
			if _, statErr := stat(part); statErr == nil {
				_, _ = r.Run(ctx, "wipefs", "-af", part)
			}
		}
		_, _ = r.Run(ctx, "udevadm", "settle")
	}

	// Brief sleep then settle — mirrors bootc's approach.
	sleep(200 * time.Millisecond)
	_, _ = r.Run(ctx, "udevadm", "settle")

	return nil
}

// LinuxRootX8664GUID is the Discoverable Partitions Spec type for an
// x86-64 root partition — what systemd-gpt-auto-generator discovers.
const LinuxRootX8664GUID = "4F68BCE3-E8CD-4DB1-96E7-FBCAF984B709"

// Ported from frostyard/fisherman (GPL-3.0-only),
// fisherman/internal/disk/partition.go (SetPartitionType,
// WaitForPartition).
//
// SetPartitionType retypes one partition. sfdisk --part-type triggers a
// BLKRRPART: udev briefly removes and re-adds the kernel's partition
// block devices, so /dev/<disk>pN can transiently disappear (fisherman
// issue #32: "/dev/loop0p2 disappearing after sfdisk --part-type"). A
// caller that mounts immediately afterward can race the node's
// reappearance — settle here and use WaitForPartition before reopening.
func SetPartitionType(ctx context.Context, r *runner.Runner, disk string, partNum int, partType string) error {
	if _, err := r.Run(ctx, "sfdisk", "--part-type", disk, strconv.Itoa(partNum), partType); err != nil {
		return fmt.Errorf("disk: setting partition type: %w", err)
	}
	_, _ = r.Run(ctx, "udevadm", "settle")
	return nil
}

// WaitForPartition blocks until partition partNum's device node exists,
// or the timeout elapses.
func WaitForPartition(ctx context.Context, r *runner.Runner, diskDev string, partNum int, timeout time.Duration) error {
	part := PartName(diskDev, partNum)
	deadline := time.Now().Add(timeout)
	for {
		if _, err := stat(part); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("disk: partition %s did not appear within %s", part, timeout)
		}
		_, _ = r.Run(ctx, "udevadm", "settle")
		sleep(100 * time.Millisecond)
	}
}
