package abimg

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFormatVarExt4(t *testing.T) {
	rec := &recorder{}
	if err := FormatVar(context.Background(), rec.runner(), "/dev/mapper/var", "ext4", false); err != nil {
		t.Fatalf("FormatVar: %v", err)
	}
	assertCall(t, rec, 0, "mkfs.ext4", "-F", "-L", "var", "/dev/mapper/var")
	if len(rec.calls) != 1 {
		t.Errorf("calls = %d, want 1: %+v", len(rec.calls), rec.calls)
	}
}

func TestFormatVarBtrfsPlain(t *testing.T) {
	rec := &recorder{}
	if err := FormatVar(context.Background(), rec.runner(), "/dev/vdb4", "btrfs", false); err != nil {
		t.Fatalf("FormatVar: %v", err)
	}
	assertCall(t, rec, 0, "mkfs.btrfs", "-f", "-L", "var", "/dev/vdb4")
	if len(rec.calls) != 1 {
		t.Errorf("calls = %d, want 1 (no scratch mount without subvolumes): %+v", len(rec.calls), rec.calls)
	}
}

func TestFormatVarBtrfsSubvolumes(t *testing.T) {
	mnt := t.TempDir()
	pinMkdirTemp(t, mnt)
	rec := &recorder{}
	if err := FormatVar(context.Background(), rec.runner(), "/dev/vdb4", "btrfs", true); err != nil {
		t.Fatalf("FormatVar: %v", err)
	}
	assertCall(t, rec, 0, "mkfs.btrfs", "-f", "-L", "var", "/dev/vdb4")
	assertCall(t, rec, 1, "mount", "-t", "btrfs", "/dev/vdb4", mnt)
	// Nested subvolumes inside the fs root — plain directories to the
	// booted system, no mount units (ADR-0008).
	assertCall(t, rec, 2, "btrfs", "subvolume", "create", filepath.Join(mnt, "home"))
	assertCall(t, rec, 3, "btrfs", "subvolume", "create", filepath.Join(mnt, "snapshots"))
	assertCall(t, rec, 4, "umount", mnt)
}

func TestFormatVarUnsupported(t *testing.T) {
	rec := &recorder{}
	if err := FormatVar(context.Background(), rec.runner(), "/dev/vdb4", "xfs", false); err == nil {
		t.Fatal("want error for unsupported filesystem")
	}
}
