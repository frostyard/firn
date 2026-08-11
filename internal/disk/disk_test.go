package disk

import (
	"context"
	"errors"
	"testing"

	"github.com/frostyard/firn/internal/runner"
)

const lsblkFixture = `{
  "blockdevices": [
    {"path": "/dev/sda", "type": "disk", "size": 500107862016, "fstype": null, "label": null,
     "mountpoints": [null],
     "children": [
       {"path": "/dev/sda1", "type": "part", "size": 536870912, "fstype": "vfat", "label": null, "mountpoints": ["/boot/efi"]},
       {"path": "/dev/sda2", "type": "part", "size": 499569000000, "fstype": "ext4", "label": null, "mountpoints": ["/"]}
     ]},
    {"path": "/dev/sdb", "type": "disk", "size": 1000204886016, "fstype": null, "label": null,
     "mountpoints": [null],
     "children": [
       {"path": "/dev/sdb1", "type": "part", "size": 1000203000000, "fstype": "linux_raid_member", "label": null, "mountpoints": [null]}
     ]},
    {"path": "/dev/sdc", "type": "disk", "size": 256060514304, "fstype": null, "label": null, "mountpoints": [null]},
    {"path": "/dev/sdd", "type": "disk", "size": 16008609792, "fstype": "iso9660", "label": "SNOSI_INSTALLER_20260801120000", "mountpoints": [null]},
    {"path": "/dev/loop0", "type": "loop", "size": 4096, "fstype": null, "label": null, "mountpoints": [null]}
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

func TestListReturnsWholeDisksOnly(t *testing.T) {
	devices, err := List(context.Background(), fakeRunner(t, lsblkFixture, "/dev/sda2"))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 4 {
		t.Fatalf("got %d disks, want 4 (loop devices excluded)", len(devices))
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

func TestRootDevice(t *testing.T) {
	if got := RootDevice(context.Background(), fakeRunner(t, lsblkFixture, "/dev/sda2")); got != "/dev/sda2" {
		t.Errorf("RootDevice = %q", got)
	}
	// Live environments: rootfs/overlay sources are not block devices.
	if got := RootDevice(context.Background(), fakeRunner(t, lsblkFixture, "overlay")); got != "" {
		t.Errorf("RootDevice for overlay = %q, want empty", got)
	}
}
