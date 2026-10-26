// Package disk enumerates block devices and applies firn's disk
// refusal rules — the union of both parent installers' guards.
//
// Disk identity fields and the refusal rules are ported from snosi-install's
// list_installable_disks() and disk_refusal_reason() (frostyard/snosi,
// shared/native-installer, GPL-3.0-only): enumerate enough hardware metadata
// to distinguish same-size disks, never the running system's own disk, never
// anything mounted, never RAID/LVM members.
package disk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/frostyard/firn/internal/runner"
)

// Test seam: resolving a /dev/disk/by-* symlink to its canonical device
// goes through filepath.EvalSymlinks. Tests replace it so a by-id target
// resolves without real udev links or block devices.
var evalSymlinks = filepath.EvalSymlinks

// SwapEvalSymlinks replaces the symlink resolver CanonicalPath and Find
// consult, returning a restore func. Exported only so pipeline-level tests
// in OTHER packages — which drive resolution through an assembled step —
// can simulate a by-id target's link without a real block device. Not for
// production use.
func SwapEvalSymlinks(fn func(string) (string, error)) (restore func()) {
	old := evalSymlinks
	evalSymlinks = fn
	return func() { evalSymlinks = old }
}

// linkDir is where udev keeps its by-id, by-uuid, by-partuuid and by-path
// symlinks: /dev/disk/by-id/<name> -> ../../sda. A target under it names a
// link, never a device node.
const linkDir = "/dev/disk/"

// Device is one lsblk block device (disks with partition children).
type Device struct {
	Path        string   `json:"path"`
	Type        string   `json:"type"`
	Size        int64    `json:"size"`
	Vendor      string   `json:"vendor"`
	Model       string   `json:"model"`
	Serial      string   `json:"serial"`
	WWN         string   `json:"wwn"`
	Transport   string   `json:"tran"`
	Fstype      string   `json:"fstype"`
	Label       string   `json:"label"`
	Mountpoints []string `json:"mountpoints"`
	Children    []Device `json:"children"`
}

type lsblkOutput struct {
	Blockdevices []Device `json:"blockdevices"`
}

// List enumerates whole disks via lsblk.
func List(ctx context.Context, r *runner.Runner) ([]Device, error) {
	out, err := r.Run(ctx, "lsblk", "-b", "-J", "-o", "PATH,TYPE,SIZE,VENDOR,MODEL,SERIAL,WWN,TRAN,FSTYPE,LABEL,MOUNTPOINTS")
	if err != nil {
		return nil, fmt.Errorf("disk: %w", err)
	}
	var parsed lsblkOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("disk: parsing lsblk output: %w", err)
	}
	var disks []Device
	for _, d := range parsed.Blockdevices {
		// Loop devices are legitimate whole-disk targets (the E2E
		// harness installs to one); the refusal rules still reject any
		// that are mounted or backing the running system.
		if d.Type == "disk" || d.Type == "loop" {
			disks = append(disks, d)
		}
	}
	return disks, nil
}

// Labels returns the distinct filesystem labels found on dev or any of its
// descendants, in lsblk traversal order. Whole disks normally have no label;
// the useful label is commonly on a partition below them.
func Labels(dev Device) []string {
	var labels []string
	seen := make(map[string]bool)
	walk(dev, func(d Device) bool {
		label := strings.TrimSpace(d.Label)
		if label != "" && !seen[label] {
			labels = append(labels, label)
			seen[label] = true
		}
		return true
	})
	return labels
}

// CanonicalPath resolves target to the canonical device path it names.
//
// A /dev/disk/by-* target (by-id, by-uuid, ...) MUST resolve: udev names a
// by-id partition link <link>-part<N>, so appending a bare partition number
// to the link gives paths that do not exist, and udev may not have created
// the <link>-part<N> link yet when the next step looks for it (lab run
// run-snow-bootc-lifecycle-lnzgv, firn 0.9.0: "mkfs.fat: unable to open
// /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root1: No such file or
// directory"). This is a deliberate divergence from fisherman, which
// appended the partition number to whatever path it was handed.
//
// A plain device path that does not exist on this host is returned
// unchanged: there is no link to follow, and Resolve's lsblk lookup still
// has to accept it (dry runs and unit-test fixtures name devices that are
// not present on the machine).
func CanonicalPath(target string) (string, error) {
	resolved, err := evalSymlinks(target)
	if err == nil {
		return resolved, nil
	}
	if errors.Is(err, fs.ErrNotExist) && !strings.HasPrefix(target, linkDir) {
		return target, nil
	}
	return "", err
}

// Resolve returns the whole-disk Device that target names, with its path
// already resolved to the canonical device (CanonicalPath). Callers MUST
// use the returned Path — not the recipe's text — for partitioning,
// PartName, SetPartitionType and WaitForPartition, so by-id targets get
// /dev/sda1-style names instead of /dev/disk/by-id/<link>1.
//
// It fails before anything destructive runs when the target cannot be
// resolved, or when it resolves to something that is not a whole disk
// (a partition, or a link to a device lsblk does not list as a disk).
func Resolve(devices []Device, target string) (Device, error) {
	canonical, err := CanonicalPath(target)
	if err != nil {
		return Device{}, fmt.Errorf("target disk %s cannot be resolved: %w", target, err)
	}
	for _, d := range devices {
		if d.Path == canonical {
			return d, nil
		}
	}
	if canonical != target {
		return Device{}, fmt.Errorf("target disk %s resolves to %s, which is not a whole disk (whole disks present: %s)",
			target, canonical, diskPaths(devices))
	}
	return Device{}, fmt.Errorf("target disk %s not found (whole disks present: %s)", target, diskPaths(devices))
}

func diskPaths(devices []Device) string {
	if len(devices) == 0 {
		return "none"
	}
	paths := make([]string, len(devices))
	for i, d := range devices {
		paths[i] = d.Path
	}
	return strings.Join(paths, ", ")
}

// Find returns the device for path from a List result, resolving
// /dev/disk/by-* symlinks to their canonical device path. It reports
// false when nothing matches; Resolve reports why.
func Find(devices []Device, path string) (Device, bool) {
	resolved, err := CanonicalPath(path)
	if err != nil {
		resolved = path
	}
	for _, d := range devices {
		if d.Path == resolved || d.Path == path {
			return d, true
		}
	}
	return Device{}, false
}

// RefusalReason reports why dev must not be installed to, or "" when
// it is acceptable. rootDev is the device backing the running system
// ("" when the system runs from RAM, e.g. the installer ISO).
func RefusalReason(dev Device, rootDev string) string {
	if rootDev != "" && strings.HasPrefix(rootDev, dev.Path) {
		return "it is the disk this system is running from"
	}
	if mp := mountedAnywhere(dev); mp != "" {
		return fmt.Sprintf("it has a filesystem mounted at %s; unmount everything on it first", mp)
	}
	if r := memberReason(dev); r != "" {
		return r
	}
	// Installer-medium label guard, ported from snosi-install's
	// disk_is_installer_medium(): a relabeled/odd boot stick may not be
	// caught by rootDev when the installer runs entirely from RAM.
	label := ""
	walk(dev, func(d Device) bool {
		if strings.HasPrefix(d.Label, "SNOSI_INSTALLER_") || strings.HasPrefix(d.Label, "FIRN_INSTALLER_") {
			label = d.Label
			return false
		}
		return true
	})
	if label != "" {
		return "it looks like the installer medium (label " + label + ")"
	}
	return ""
}

// walk visits dev and every descendant in depth-first pre-order, so the
// refusal rules see the whole lsblk tree — a mounted dm/crypt child of a
// LUKS partition, or an LVM volume under it, is as disqualifying as a
// mounted partition. visit returns false to stop the walk.
func walk(dev Device, visit func(Device) bool) bool {
	if !visit(dev) {
		return false
	}
	for _, child := range dev.Children {
		if !walk(child, visit) {
			return false
		}
	}
	return true
}

func mountedAnywhere(dev Device) string {
	found := ""
	walk(dev, func(d Device) bool {
		for _, mp := range d.Mountpoints {
			if mp != "" {
				found = mp
				return false
			}
		}
		return true
	})
	return found
}

func memberReason(dev Device) string {
	reason := ""
	walk(dev, func(d Device) bool {
		switch d.Fstype {
		case "linux_raid_member":
			reason = "it is part of a RAID array (" + d.Path + ")"
			return false
		case "LVM2_member":
			reason = "it is an LVM physical volume (" + d.Path + ")"
			return false
		}
		return true
	})
	return reason
}

// RootDevice reports the block device backing /, or "" when the root
// is not block-backed (tmpfs/overlay live environments). Ported from
// snosi-install's self_boot_device().
func RootDevice(ctx context.Context, r *runner.Runner) string {
	out, err := r.Run(ctx, "findmnt", "-n", "-o", "SOURCE", "/")
	if err != nil {
		return ""
	}
	src := strings.TrimSpace(string(out))
	if !strings.HasPrefix(src, "/dev/") {
		return ""
	}
	return src
}
