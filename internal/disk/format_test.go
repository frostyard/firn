package disk

import (
	"context"
	"errors"
	"testing"
)

func TestFormatESP(t *testing.T) {
	rec := &recorder{}
	if err := FormatESP(context.Background(), rec.runner(), "/dev/sda1"); err != nil {
		t.Fatalf("FormatESP: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "mkfs.fat", "-F32", "-n", "EFI-SYSTEM", "/dev/sda1")
}

func TestFormatBootExt4(t *testing.T) {
	rec := &recorder{}
	if err := FormatBootExt4(context.Background(), rec.runner(), "/dev/sda2"); err != nil {
		t.Fatalf("FormatBootExt4: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "mkfs.ext4", "-L", "boot", "-F", "/dev/sda2")
}

func TestFormatRoot(t *testing.T) {
	tests := []struct {
		name       string
		filesystem string
		wantName   string
		wantArgs   []string
		wantErr    bool
	}{
		{
			name:       "xfs",
			filesystem: "xfs",
			wantName:   "mkfs.xfs",
			wantArgs:   []string{"-f", "-L", "root", "/dev/sda3"},
		},
		{
			// -O verity is load-bearing: composefs enables fs-verity on
			// individual files, and ext4 only supports that when the
			// feature is set at format time.
			name:       "ext4 enables verity",
			filesystem: "ext4",
			wantName:   "mkfs.ext4",
			wantArgs:   []string{"-F", "-L", "root", "-O", "verity", "/dev/sda3"},
		},
		{
			name:       "btrfs",
			filesystem: "btrfs",
			wantName:   "mkfs.btrfs",
			wantArgs:   []string{"-f", "-L", "root", "/dev/sda3"},
		},
		{
			name:       "unsupported empty",
			filesystem: "",
			wantErr:    true,
		},
		{
			name:       "unsupported zfs (use CreateZFSPool)",
			filesystem: "zfs",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			err := FormatRoot(context.Background(), rec.runner(), "/dev/sda3", tt.filesystem)
			if tt.wantErr {
				if err == nil {
					t.Errorf("FormatRoot(%q): expected error, got nil", tt.filesystem)
				}
				if len(rec.calls) != 0 {
					t.Errorf("FormatRoot(%q): unexpected commands run: %+v", tt.filesystem, rec.calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("FormatRoot: %v", err)
			}
			if len(rec.calls) != 1 {
				t.Fatalf("expected 1 call, got %d: %+v", len(rec.calls), rec.calls)
			}
			assertCall(t, rec, 0, tt.wantName, tt.wantArgs...)
		})
	}
}

func TestCreateZFSPool(t *testing.T) {
	rec := &recorder{}
	if err := CreateZFSPool(context.Background(), rec.runner(), "/dev/sdb2", "rpool", "/mnt/target"); err != nil {
		t.Fatalf("CreateZFSPool: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "zpool", "create",
		"-R", "/mnt/target",
		"-O", "compression=zstd",
		"-O", "mountpoint=/",
		"-O", "acltype=posixacl",
		"-O", "xattr=sa",
		"-O", "dnodesize=auto",
		"-O", "relatime=on",
		"-o", "ashift=12",
		"-f",
		"rpool", "/dev/sdb2",
	)
}

// TestCreateSubvolumes verifies the @/@home/@snapshots creation order
// and, critically, that @ is set as the btrfs default subvolume:
// systemd GPT auto-discovery mounts the default subvolume at boot, and
// without set-default the booted system would land on the btrfs
// top-level where state/deploy does not exist.
func TestCreateSubvolumes(t *testing.T) {
	rec := &recorder{}
	if err := CreateSubvolumes(context.Background(), rec.runner(), "/mnt/target"); err != nil {
		t.Fatalf("CreateSubvolumes: %v", err)
	}
	if len(rec.calls) != 4 {
		t.Fatalf("expected 4 calls, got %d: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "btrfs", "subvolume", "create", "/mnt/target/@")
	assertCall(t, rec, 1, "btrfs", "subvolume", "create", "/mnt/target/@home")
	assertCall(t, rec, 2, "btrfs", "subvolume", "create", "/mnt/target/@snapshots")
	assertCall(t, rec, 3, "btrfs", "subvolume", "set-default", "/mnt/target/@")
}

// TestFinalizeFilesystem verifies the exact fstrim → remount-ro →
// fsfreeze/thaw sequence replicating bootc's finalize_filesystem().
func TestFinalizeFilesystem(t *testing.T) {
	rec := &recorder{}
	if err := FinalizeFilesystem(context.Background(), rec.runner(), "/mnt/target"); err != nil {
		t.Fatalf("FinalizeFilesystem: %v", err)
	}
	if len(rec.calls) != 4 {
		t.Fatalf("expected 4 calls, got %d: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "fstrim", "--quiet-unsupported", "-v", "/mnt/target")
	assertCall(t, rec, 1, "mount", "-o", "remount,ro", "/mnt/target")
	assertCall(t, rec, 2, "fsfreeze", "-f", "/mnt/target")
	assertCall(t, rec, 3, "fsfreeze", "-u", "/mnt/target")
}

func TestFinalizeFilesystemStopsOnError(t *testing.T) {
	rec := &recorder{}
	rec.errFn = func(c call, nth int) error {
		if c.name == "fstrim" {
			return errors.New("fstrim: not supported")
		}
		return nil
	}
	if err := FinalizeFilesystem(context.Background(), rec.runner(), "/mnt/target"); err == nil {
		t.Fatal("expected error from failing fstrim, got nil")
	}
	if len(rec.calls) != 1 {
		t.Errorf("expected finalize to stop after failing fstrim, got calls: %+v", rec.calls)
	}
}
