package disk

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/runner"
)

// The by-id paths from the lab run that exposed the bug, plus an nvme one
// (whose partition names carry the "p" separator).
const (
	scsiByID = "/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root"
	nvmeByID = "/dev/disk/by-id/nvme-eui.000000000000000100a0757065f0000"
)

// lsblkOf renders a minimal lsblk -J fixture listing whole disks (and
// whatever nodes the caller adds with lsblkOfChild).
func lsblkOf(disks ...string) string {
	nodes := make([]string, len(disks))
	for i, d := range disks {
		nodes[i] = fmt.Sprintf(`{"path":%q,"type":"disk","size":500107862016,"fstype":null,"label":null,"mountpoints":[null]}`, d)
	}
	return `{"blockdevices":[` + strings.Join(nodes, ",") + `]}`
}

// fakeLinks makes the resolver behave as if udev had created exactly the
// given /dev/disk/by-* links; every other path is missing.
func fakeLinks(t *testing.T, links map[string]string) {
	t.Helper()
	t.Cleanup(SwapEvalSymlinks(func(path string) (string, error) {
		if to, ok := links[path]; ok {
			return to, nil
		}
		return "", fmt.Errorf("lstat %s: %w", path, os.ErrNotExist)
	}))
}

// TestResolveByIDTargets pins the whole fix: a by-id target resolves to its
// canonical device, and partitions are named after the device
// (/dev/sda1, /dev/nvme0n1p1) — never after the link. Plain and loop paths
// keep their old names, and a target that cannot be resolved or is not a
// whole disk fails before any disk command runs.
func TestResolveByIDTargets(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		links    map[string]string
		devices  []string
		wantDisk string
		wantESP  string
		wantRoot string
		wantErr  string
	}{
		{
			name:   "by-id scsi link resolves to sda",
			target: scsiByID, links: map[string]string{scsiByID: "/dev/sda"},
			devices:  []string{"/dev/sda"},
			wantDisk: "/dev/sda", wantESP: "/dev/sda1", wantRoot: "/dev/sda2",
		},
		{
			name:   "by-id nvme link resolves to nvme0n1",
			target: nvmeByID, links: map[string]string{nvmeByID: "/dev/nvme0n1"},
			devices:  []string{"/dev/nvme0n1"},
			wantDisk: "/dev/nvme0n1", wantESP: "/dev/nvme0n1p1", wantRoot: "/dev/nvme0n1p2",
		},
		{
			name:   "plain path unchanged",
			target: "/dev/vda", devices: []string{"/dev/vda"},
			wantDisk: "/dev/vda", wantESP: "/dev/vda1", wantRoot: "/dev/vda2",
		},
		{
			name:   "loop path unchanged",
			target: "/dev/loop0", devices: []string{"/dev/loop0"},
			wantDisk: "/dev/loop0", wantESP: "/dev/loop0p1", wantRoot: "/dev/loop0p2",
		},
		{
			name:   "unresolvable by-id link fails",
			target: scsiByID, devices: []string{"/dev/sda"},
			wantErr: "cannot be resolved",
		},
		{
			name:   "by-id link to a partition is not a whole disk",
			target: scsiByID, links: map[string]string{scsiByID: "/dev/sda1"},
			devices: []string{"/dev/sda"},
			wantErr: "not a whole disk",
		},
		{
			name:   "by-id link to an unlisted device is not a whole disk",
			target: scsiByID, links: map[string]string{scsiByID: "/dev/mapper/cryptroot"},
			devices: []string{"/dev/sda"},
			wantErr: "not a whole disk",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			noSleep(t)
			fakeLinks(t, tc.links)
			lsblk := lsblkOf(tc.devices...)
			var calls []call
			fake := runner.NewFake(
				func(ctx context.Context, name string, args ...string) ([]byte, error) {
					c := call{name: name, args: args}
					if in, ok := runner.Stdin(ctx); ok {
						c.stdin = in
					}
					calls = append(calls, c)
					if name == "lsblk" {
						return []byte(lsblk), nil
					}
					return nil, nil
				},
				func(name string) (string, error) { return "/usr/bin/" + name, nil },
			)
			ctx := context.Background()
			devices, err := List(ctx, fake)
			if err != nil {
				t.Fatal(err)
			}

			dev, err := Resolve(devices, tc.target)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Resolve(%s) = %v, want error containing %q", tc.target, err, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.target) {
					t.Errorf("error should name the recipe path %s: %v", tc.target, err)
				}
				if len(calls) != 1 { // the lsblk listing only
					t.Fatalf("a failed resolution must run no disk command, got %+v", calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%s): %v", tc.target, err)
			}
			if dev.Path != tc.wantDisk {
				t.Fatalf("resolved path = %q, want %q", dev.Path, tc.wantDisk)
			}

			layout, err := PartitionSystemdBoot(ctx, fake, dev.Path)
			if err != nil {
				t.Fatalf("PartitionSystemdBoot(%s): %v", dev.Path, err)
			}
			want := Layout{Disk: tc.wantDisk, ESP: tc.wantESP, Root: tc.wantRoot}
			if layout != want {
				t.Errorf("layout = %+v, want %+v", layout, want)
			}

			// sfdisk wrote the canonical device, and no command ever saw the
			// by-id link.
			if len(calls) < 2 || calls[1].name != "sfdisk" {
				t.Fatalf("expected sfdisk as the second call, got %+v", calls)
			}
			if got := calls[1].args; len(got) != 2 || got[0] != "--wipe=always" || got[1] != tc.wantDisk {
				t.Errorf("sfdisk args = %v, want [--wipe=always %s]", got, tc.wantDisk)
			}
			for _, c := range calls {
				for _, a := range c.args {
					if strings.HasPrefix(a, linkDir) {
						t.Errorf("%s was given the by-id link %q", c.name, a)
					}
				}
			}
		})
	}
}

// TestCanonicalPathPlainPathNeedsNoLink pins the compatibility rule: a
// plain device path that is absent on this host (dry-run and test
// fixtures) is returned unchanged instead of failing resolution, while an
// absent /dev/disk/by-* link is always an error.
func TestCanonicalPathPlainPathNeedsNoLink(t *testing.T) {
	fakeLinks(t, map[string]string{nvmeByID: "/dev/nvme0n1"})

	for _, path := range []string{"/dev/sda", "/dev/vda", "/dev/loop0"} {
		got, err := CanonicalPath(path)
		if err != nil || got != path {
			t.Errorf("CanonicalPath(%s) = %q, %v; want the path unchanged", path, got, err)
		}
	}
	if got, err := CanonicalPath(nvmeByID); err != nil || got != "/dev/nvme0n1" {
		t.Errorf("CanonicalPath(%s) = %q, %v", nvmeByID, got, err)
	}
	if _, err := CanonicalPath(scsiByID); err == nil {
		t.Errorf("CanonicalPath(%s) must fail: the link does not exist", scsiByID)
	}
}
