// Package disk enumerates block devices and applies firn's disk
// refusal rules — the union of both parent installers' guards.
//
// The refusal rules are ported from snosi-install's
// disk_refusal_reason() (frostyard/snosi, shared/native-installer,
// GPL-3.0-only): never the running system's own disk, never anything
// mounted, never RAID/LVM members.
package disk

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/frostyard/firn/internal/runner"
)

// Device is one lsblk block device (disks with partition children).
type Device struct {
	Path        string   `json:"path"`
	Type        string   `json:"type"`
	Size        int64    `json:"size"`
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
	out, err := r.Run(ctx, "lsblk", "-b", "-J", "-o", "PATH,TYPE,SIZE,FSTYPE,LABEL,MOUNTPOINTS")
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

// Find returns the device for path from a List result, resolving
// /dev/disk/by-* symlinks to their canonical device path.
func Find(devices []Device, path string) (Device, bool) {
	resolved := path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		resolved = r
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
	for _, d := range append([]Device{dev}, dev.Children...) {
		if strings.HasPrefix(d.Label, "SNOSI_INSTALLER_") || strings.HasPrefix(d.Label, "FIRN_INSTALLER_") {
			return "it looks like the installer medium (label " + d.Label + ")"
		}
	}
	return ""
}

func mountedAnywhere(dev Device) string {
	for _, d := range append([]Device{dev}, dev.Children...) {
		for _, mp := range d.Mountpoints {
			if mp != "" {
				return mp
			}
		}
	}
	return ""
}

func memberReason(dev Device) string {
	for _, d := range append([]Device{dev}, dev.Children...) {
		switch d.Fstype {
		case "linux_raid_member":
			return "it is part of a RAID array (" + d.Path + ")"
		case "LVM2_member":
			return "it is an LVM physical volume (" + d.Path + ")"
		}
	}
	return ""
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
