package disk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMountTargetGrub2 verifies the grub2 mount order — root, then the
// separate ext4 /boot, then the ESP at boot/efi — with the root mounted
// filesystem-typed (-t): a typeless mount of a freshly-created xfs/ext4
// root can be misdetected in the deployer initramfs.
func TestMountTargetGrub2(t *testing.T) {
	rec := &recorder{}
	target := filepath.Join(t.TempDir(), "target")
	layout := Layout{Disk: "/dev/sda", ESP: "/dev/sda1", Boot: "/dev/sda2", Root: "/dev/sda3"}

	if err := MountTarget(context.Background(), rec.runner(), layout, "/dev/sda3", "ext4", false, target); err != nil {
		t.Fatalf("MountTarget: %v", err)
	}

	if len(rec.calls) != 3 {
		t.Fatalf("expected 3 mount calls, got %d: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "mount", "-t", "ext4", "/dev/sda3", target)
	assertCall(t, rec, 1, "mount", "/dev/sda2", filepath.Join(target, "boot"))
	assertCall(t, rec, 2, "mount", "/dev/sda1", filepath.Join(target, "boot", "efi"))

	// The mountpoint directories must have been created.
	for _, dir := range []string{target, filepath.Join(target, "boot"), filepath.Join(target, "boot", "efi")} {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Errorf("mountpoint dir %s not created: %v", dir, err)
		}
	}
}

// TestMountTargetSystemdBootBtrfs verifies the systemd-boot layout (no
// separate /boot) with btrfs subvolumes: the root mounts with subvol=@
// so writes land inside the @ subvolume where the composefs deployment
// (state/deploy) lives, and the root device is the one passed by the
// caller (here a LUKS mapper), not layout.Root.
func TestMountTargetSystemdBootBtrfs(t *testing.T) {
	rec := &recorder{}
	target := filepath.Join(t.TempDir(), "target")
	layout := Layout{Disk: "/dev/nvme0n1", ESP: "/dev/nvme0n1p1", Root: "/dev/nvme0n1p2"}

	if err := MountTarget(context.Background(), rec.runner(), layout, "/dev/mapper/firn-root", "btrfs", true, target); err != nil {
		t.Fatalf("MountTarget: %v", err)
	}

	if len(rec.calls) != 2 {
		t.Fatalf("expected 2 mount calls (no separate /boot), got %d: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "mount", "-t", "btrfs", "-o", "subvol=@,compress=zstd:1", "/dev/mapper/firn-root", target)
	assertCall(t, rec, 1, "mount", "/dev/nvme0n1p1", filepath.Join(target, "boot", "efi"))
}

// TestMountTargetBtrfsWithoutSubvolumes: plain btrfs mounts typed but
// without subvol options.
func TestMountTargetBtrfsWithoutSubvolumes(t *testing.T) {
	rec := &recorder{}
	target := filepath.Join(t.TempDir(), "target")
	layout := Layout{Disk: "/dev/vda", ESP: "/dev/vda1", Root: "/dev/vda2"}

	if err := MountTarget(context.Background(), rec.runner(), layout, "/dev/vda2", "btrfs", false, target); err != nil {
		t.Fatalf("MountTarget: %v", err)
	}
	assertCall(t, rec, 0, "mount", "-t", "btrfs", "/dev/vda2", target)
}

// TestMountTargetDiagnosticsOnFailure verifies the ported failure
// diagnostics: a failed mount probes the device with blkid and appends
// the dmesg tail, because in the deployer initramfs "exit status 32"
// otherwise arrives with zero diagnosable context.
func TestMountTargetDiagnosticsOnFailure(t *testing.T) {
	rec := &recorder{}
	rec.errFn = func(c call, nth int) error {
		if c.name == "mount" {
			return errors.New("exit status 32")
		}
		return nil
	}
	target := filepath.Join(t.TempDir(), "target")
	layout := Layout{Disk: "/dev/sda", ESP: "/dev/sda1", Root: "/dev/sda3"}

	err := MountTarget(context.Background(), rec.runner(), layout, "/dev/sda3", "xfs", false, target)
	if err == nil {
		t.Fatal("expected error from failing mount, got nil")
	}
	if !strings.Contains(err.Error(), "blkid") || !strings.Contains(err.Error(), "dmesg") {
		t.Errorf("error missing diagnostics: %v", err)
	}
	if len(rec.calls) != 3 {
		t.Fatalf("expected mount + blkid + dmesg, got %d calls: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "mount", "-t", "xfs", "/dev/sda3", target)
	assertCall(t, rec, 1, "blkid", "/dev/sda3")
	assertCall(t, rec, 2, "sh", "-c", "dmesg | tail -8")
}

// TestMountTargetUnwindsOnBootFailure verifies that when the separate
// /boot mount fails after the root has mounted, MountTarget unwinds the
// whole target tree with `umount -R <targetDir>` before returning, and the
// returned error still names the /boot stage.
func TestMountTargetUnwindsOnBootFailure(t *testing.T) {
	rec := &recorder{}
	target := filepath.Join(t.TempDir(), "target")
	layout := Layout{Disk: "/dev/sda", ESP: "/dev/sda1", Boot: "/dev/sda2", Root: "/dev/sda3"}
	rec.errFn = func(c call, _ int) error {
		if c.name == "mount" && len(c.args) > 0 && c.args[len(c.args)-1] == filepath.Join(target, "boot") {
			return errors.New("exit status 32")
		}
		return nil
	}

	err := MountTarget(context.Background(), rec.runner(), layout, "/dev/sda3", "ext4", false, target)
	if err == nil {
		t.Fatal("expected error from failing /boot mount, got nil")
	}
	if !strings.Contains(err.Error(), "mounting /boot") {
		t.Errorf("error lost the original /boot stage context: %v", err)
	}
	// root mount, /boot mount (fails) + blkid + dmesg, then umount -R.
	last := rec.calls[len(rec.calls)-1]
	assertCall(t, rec, len(rec.calls)-1, "umount", "-R", target)
	if last.name != "umount" {
		t.Fatalf("expected the final call to be the recursive unmount, got %+v", last)
	}
}

// TestMountTargetUnwindsOnESPFailure verifies that when the ESP mount fails
// after the root and separate /boot have mounted, MountTarget unwinds the
// whole target tree with `umount -R <targetDir>` before returning, and the
// returned error still names the ESP stage.
func TestMountTargetUnwindsOnESPFailure(t *testing.T) {
	rec := &recorder{}
	target := filepath.Join(t.TempDir(), "target")
	layout := Layout{Disk: "/dev/sda", ESP: "/dev/sda1", Boot: "/dev/sda2", Root: "/dev/sda3"}
	rec.errFn = func(c call, _ int) error {
		if c.name == "mount" && len(c.args) > 0 && c.args[len(c.args)-1] == filepath.Join(target, "boot", "efi") {
			return errors.New("exit status 32")
		}
		return nil
	}

	err := MountTarget(context.Background(), rec.runner(), layout, "/dev/sda3", "ext4", false, target)
	if err == nil {
		t.Fatal("expected error from failing ESP mount, got nil")
	}
	if !strings.Contains(err.Error(), "mounting ESP") {
		t.Errorf("error lost the original ESP stage context: %v", err)
	}
	assertCall(t, rec, len(rec.calls)-1, "umount", "-R", target)
}

// TestMountTargetJoinsCleanupFailure verifies that when a post-root mount
// fails AND the recursive unmount cleanup also fails, both errors remain
// represented in the returned error.
func TestMountTargetJoinsCleanupFailure(t *testing.T) {
	rec := &recorder{}
	target := filepath.Join(t.TempDir(), "target")
	layout := Layout{Disk: "/dev/sda", ESP: "/dev/sda1", Boot: "/dev/sda2", Root: "/dev/sda3"}
	rec.errFn = func(c call, _ int) error {
		if c.name == "mount" && len(c.args) > 0 && c.args[len(c.args)-1] == filepath.Join(target, "boot") {
			return errors.New("exit status 32")
		}
		if c.name == "umount" {
			return errors.New("target is busy")
		}
		return nil
	}

	err := MountTarget(context.Background(), rec.runner(), layout, "/dev/sda3", "ext4", false, target)
	if err == nil {
		t.Fatal("expected a joined error, got nil")
	}
	if !strings.Contains(err.Error(), "mounting /boot") {
		t.Errorf("joined error dropped the original /boot stage: %v", err)
	}
	if !strings.Contains(err.Error(), "umount -R") || !strings.Contains(err.Error(), "target is busy") {
		t.Errorf("joined error dropped the cleanup failure: %v", err)
	}
	assertCall(t, rec, len(rec.calls)-1, "umount", "-R", target)
}

// TestMountTargetRootFailureDoesNotUnmount verifies that when the initial
// root mount itself fails, MountTarget does not attempt a recursive unmount
// (there is nothing mounted to unwind).
func TestMountTargetRootFailureDoesNotUnmount(t *testing.T) {
	rec := &recorder{}
	target := filepath.Join(t.TempDir(), "target")
	layout := Layout{Disk: "/dev/sda", ESP: "/dev/sda1", Boot: "/dev/sda2", Root: "/dev/sda3"}
	rec.errFn = func(c call, _ int) error {
		if c.name == "mount" && len(c.args) > 0 && c.args[len(c.args)-1] == target {
			return errors.New("exit status 32")
		}
		return nil
	}

	err := MountTarget(context.Background(), rec.runner(), layout, "/dev/sda3", "ext4", false, target)
	if err == nil {
		t.Fatal("expected error from failing root mount, got nil")
	}
	for i, c := range rec.calls {
		if c.name == "umount" {
			t.Fatalf("call %d attempted an unmount after a root-mount failure: %+v", i, c)
		}
	}
}

func TestUnmount(t *testing.T) {
	rec := &recorder{}
	if err := Unmount(context.Background(), rec.runner(), "/mnt/target/boot/efi"); err != nil {
		t.Fatalf("Unmount: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "umount", "/mnt/target/boot/efi")
}

func TestUnmountRecursive(t *testing.T) {
	rec := &recorder{}
	if err := UnmountRecursive(context.Background(), rec.runner(), "/mnt/target"); err != nil {
		t.Fatalf("UnmountRecursive: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "umount", "-R", "/mnt/target")
}
