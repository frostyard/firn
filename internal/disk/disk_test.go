package disk

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/runner"
)

const lsblkFixture = `{
  "blockdevices": [
    {"path": "/dev/sda", "type": "disk", "size": 500107862016,
     "vendor": "ATA", "model": "System SSD", "serial": "SYSTEM01", "wwn": "0x500-system", "tran": "sata",
     "fstype": null, "label": null,
     "mountpoints": [null],
     "children": [
       {"path": "/dev/sda1", "type": "part", "size": 536870912, "fstype": "vfat", "label": null, "mountpoints": ["/boot/efi"]},
       {"path": "/dev/sda2", "type": "part", "size": 499569000000, "fstype": "ext4", "label": null, "mountpoints": ["/"]}
     ]},
    {"path": "/dev/sdb", "type": "disk", "size": 1000204886016,
     "vendor": "WDC", "model": "WD_BLACK SN850X 2000GB", "serial": "WD-KEEP01", "wwn": "eui.keep01", "tran": "nvme",
     "fstype": null, "label": null,
     "mountpoints": [null],
     "children": [
       {"path": "/dev/sdb1", "type": "part", "size": 1000203000000, "fstype": "linux_raid_member", "label": "backups", "mountpoints": [null]}
     ]},
    {"path": "/dev/sdc", "type": "disk", "size": 256060514304, "fstype": null, "label": null, "mountpoints": [null]},
    {"path": "/dev/sdd", "type": "disk", "size": 16008609792, "fstype": "iso9660", "label": "SNOSI_INSTALLER_20260801120000", "mountpoints": [null]},
    {"path": "/dev/loop0", "type": "loop", "size": 4096, "fstype": null, "label": null, "mountpoints": [null]}
  ]
}`

// lsblkDeepFixture holds device trees deeper than one level, the shape
// LUKS produces: disk → part (crypto_LUKS) → crypt → lvm. The refusal
// rules must see every descendant, not just the disk's own children.
const lsblkDeepFixture = `{
  "blockdevices": [
    {"path": "/dev/sde", "type": "disk", "size": 500107862016, "fstype": null, "label": null,
     "mountpoints": [null],
     "children": [
       {"path": "/dev/sde1", "type": "part", "size": 499569000000, "fstype": "crypto_LUKS", "label": null,
        "mountpoints": [null],
        "children": [
          {"path": "/dev/mapper/cryptroot", "type": "crypt", "size": 499560000000, "fstype": "ext4", "label": null, "mountpoints": ["/"]}
        ]}
     ]},
    {"path": "/dev/sdf", "type": "disk", "size": 1000204886016, "fstype": null, "label": null,
     "mountpoints": [null],
     "children": [
       {"path": "/dev/sdf1", "type": "part", "size": 1000203000000, "fstype": "crypto_LUKS", "label": null,
        "mountpoints": [null],
        "children": [
          {"path": "/dev/mapper/vault", "type": "crypt", "size": 1000200000000, "fstype": null, "label": null,
           "mountpoints": [null],
           "children": [
             {"path": "/dev/mapper/vault-data", "type": "lvm", "size": 900000000000, "fstype": "xfs", "label": null, "mountpoints": ["/data"]}
           ]}
        ]}
     ]},
    {"path": "/dev/sdg", "type": "disk", "size": 256060514304, "fstype": null, "label": null,
     "mountpoints": [null],
     "children": [
       {"path": "/dev/sdg1", "type": "part", "size": 256000000000, "fstype": "crypto_LUKS", "label": null, "mountpoints": [null]}
     ]}
  ]
}`

func fakeRunner(t *testing.T, lsblkJSON, rootSource string) *runner.Runner {
	t.Helper()
	return runner.NewFake(
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			switch name {
			case "lsblk":
				return []byte(lsblkJSON), nil
			case "findmnt":
				return []byte(rootSource + "\n"), nil
			}
			return nil, errors.New("unexpected command " + name)
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
}

func TestListReturnsWholeDisksAndLoops(t *testing.T) {
	devices, err := List(context.Background(), fakeRunner(t, lsblkFixture, "/dev/sda2"))
	if err != nil {
		t.Fatal(err)
	}
	// Whole disks plus loop devices (valid E2E targets); partitions
	// never appear as top-level devices.
	if len(devices) != 5 {
		t.Fatalf("got %d devices, want 5", len(devices))
	}
	if _, ok := Find(devices, "/dev/loop0"); !ok {
		t.Error("loop device should be listed as a target candidate")
	}
	wd, ok := Find(devices, "/dev/sdb")
	if !ok {
		t.Fatal("/dev/sdb not found")
	}
	if wd.Vendor != "WDC" || wd.Model != "WD_BLACK SN850X 2000GB" || wd.Serial != "WD-KEEP01" || wd.WWN != "eui.keep01" || wd.Transport != "nvme" {
		t.Errorf("disk identity metadata was not decoded: %+v", wd)
	}
	if got := Labels(wd); len(got) != 1 || got[0] != "backups" {
		t.Errorf("Labels(/dev/sdb) = %v, want [backups]", got)
	}
}

func TestRefusalReasons(t *testing.T) {
	devices, err := List(context.Background(), fakeRunner(t, lsblkFixture, "/dev/sda2"))
	if err != nil {
		t.Fatal(err)
	}
	rootDev := "/dev/sda2"

	cases := []struct {
		path    string
		refused bool
	}{
		{"/dev/sda", true},  // running system's own disk (also mounted)
		{"/dev/sdb", true},  // RAID member
		{"/dev/sdc", false}, // clean disk
		{"/dev/sdd", true},  // installer medium label
	}
	for _, tc := range cases {
		dev, ok := Find(devices, tc.path)
		if !ok {
			t.Fatalf("%s not found", tc.path)
		}
		reason := RefusalReason(dev, rootDev)
		if tc.refused && reason == "" {
			t.Errorf("%s: expected refusal", tc.path)
		}
		if !tc.refused && reason != "" {
			t.Errorf("%s: unexpected refusal: %s", tc.path, reason)
		}
	}
}

func TestMountedDiskRefusedEvenWhenNotRoot(t *testing.T) {
	devices, _ := List(context.Background(), fakeRunner(t, lsblkFixture, ""))
	dev, _ := Find(devices, "/dev/sda")
	if reason := RefusalReason(dev, ""); reason == "" {
		t.Error("mounted disk must be refused even with no root device (RAM-booted installer)")
	}
}

// A mounted grandchild must refuse its disk. This is the encrypted
// running root: findmnt reports /dev/mapper/cryptroot, which shares no
// prefix with /dev/sde, so only the mount on the crypt descendant can
// catch it.
func TestRefusalReasonRefusesMountedGrandchild(t *testing.T) {
	devices, err := List(context.Background(), fakeRunner(t, lsblkDeepFixture, "/dev/mapper/cryptroot"))
	if err != nil {
		t.Fatal(err)
	}
	dev, ok := Find(devices, "/dev/sde")
	if !ok {
		t.Fatal("/dev/sde not found")
	}
	reason := RefusalReason(dev, "/dev/mapper/cryptroot")
	if reason == "" {
		t.Fatal("disk with an encrypted root mounted on a crypt child must be refused")
	}
	if !strings.Contains(reason, "mounted at /") {
		t.Errorf("refusal reason %q does not report the grandchild's mountpoint %q", reason, "/")
	}
}

// The walk must be fully recursive, not merely one level deeper: only
// the depth-3 lvm device is mounted here, and nothing above it carries
// a member fstype or a label.
func TestRefusalReasonRefusesMountedGreatGrandchild(t *testing.T) {
	devices, err := List(context.Background(), fakeRunner(t, lsblkDeepFixture, ""))
	if err != nil {
		t.Fatal(err)
	}
	dev, ok := Find(devices, "/dev/sdf")
	if !ok {
		t.Fatal("/dev/sdf not found")
	}
	reason := RefusalReason(dev, "")
	if reason == "" {
		t.Fatal("disk with a mounted depth-3 lvm descendant must be refused")
	}
	if !strings.Contains(reason, "/data") {
		t.Errorf("refusal reason %q does not report the depth-3 mountpoint %q", reason, "/data")
	}
}

// No regression: a locked, unmounted LUKS partition is not a refusal.
// Reinstalling over a previously encrypted disk stays permitted.
func TestRefusalReasonAllowsUnmountedLUKSDisk(t *testing.T) {
	devices, err := List(context.Background(), fakeRunner(t, lsblkDeepFixture, ""))
	if err != nil {
		t.Fatal(err)
	}
	dev, ok := Find(devices, "/dev/sdg")
	if !ok {
		t.Fatal("/dev/sdg not found")
	}
	if reason := RefusalReason(dev, ""); reason != "" {
		t.Errorf("unmounted crypto_LUKS partition must not refuse the disk, got %q", reason)
	}
}

func TestRootDevice(t *testing.T) {
	if got := RootDevice(context.Background(), fakeRunner(t, lsblkFixture, "/dev/sda2")); got != "/dev/sda2" {
		t.Errorf("RootDevice = %q", got)
	}
	// Live environments: rootfs/overlay sources are not block devices.
	if got := RootDevice(context.Background(), fakeRunner(t, lsblkFixture, "overlay")); got != "" {
		t.Errorf("RootDevice for overlay = %q, want empty", got)
	}
}
