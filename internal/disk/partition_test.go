package disk

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/firn/internal/runner"
)

// call records one intercepted command invocation, including any stdin
// fed via runner.RunInput.
type call struct {
	name  string
	args  []string
	stdin string
}

// recorder captures every command run through a fake runner. errFn, if
// set, decides per call whether the command fails.
type recorder struct {
	calls []call
	errFn func(c call, nth int) error
}

func (rec *recorder) runner() *runner.Runner {
	return runner.NewFake(
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			c := call{name: name, args: args}
			if in, ok := runner.Stdin(ctx); ok {
				c.stdin = in
			}
			rec.calls = append(rec.calls, c)
			if rec.errFn != nil {
				return nil, rec.errFn(c, len(rec.calls)-1)
			}
			return nil, nil
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
}

// assertCall fails unless rec.calls[i] matches name and args exactly.
func assertCall(t *testing.T, rec *recorder, i int, name string, args ...string) {
	t.Helper()
	if i >= len(rec.calls) {
		t.Fatalf("expected call %d (%s %v), but only %d calls made: %+v", i, name, args, len(rec.calls), rec.calls)
	}
	c := rec.calls[i]
	if c.name != name || !slices.Equal(c.args, args) {
		t.Errorf("call %d = %s %v, want %s %v", i, c.name, c.args, name, args)
	}
}

// noSleep disables the real sleeps between kernel-visible partitioning
// operations for the duration of the test.
func noSleep(t *testing.T) {
	t.Helper()
	old := sleep
	sleep = func(time.Duration) {}
	t.Cleanup(func() { sleep = old })
}

func TestPartSuffix(t *testing.T) {
	tests := []struct {
		disk string
		want string
	}{
		{"/dev/nvme0n1", "p"},
		{"/dev/mmcblk0", "p"},
		{"/dev/loop0", "p"},
		{"/dev/sda", ""},
		{"/dev/vda", ""},
		{"/dev/xvda", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := PartSuffix(tt.disk); got != tt.want {
			t.Errorf("PartSuffix(%q) = %q, want %q", tt.disk, got, tt.want)
		}
	}
}

func TestPartName(t *testing.T) {
	tests := []struct {
		disk string
		num  int
		want string
	}{
		{"/dev/nvme0n1", 1, "/dev/nvme0n1p1"},
		{"/dev/nvme0n1", 3, "/dev/nvme0n1p3"},
		{"/dev/mmcblk0", 2, "/dev/mmcblk0p2"},
		{"/dev/sda", 1, "/dev/sda1"},
		{"/dev/sda", 3, "/dev/sda3"},
		{"/dev/vda", 2, "/dev/vda2"},
	}
	for _, tt := range tests {
		if got := PartName(tt.disk, tt.num); got != tt.want {
			t.Errorf("PartName(%q, %d) = %q, want %q", tt.disk, tt.num, got, tt.want)
		}
	}
}

// partLines returns the non-empty, non-label lines of an sfdisk script
// — the partition declarations.
func partLines(script string) []string {
	var lines []string
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "label:") {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// TestPartitionGrub2 verifies the 3-partition GPT layout: EFI (2 GiB) +
// /boot ext4 (2 GiB) + root (remaining). This is a regression test for
// the 2→3 partition layout change: any attempt to drop the separate
// ext4 /boot partition (GRUB's XFS driver cannot read modern XFS) will
// be caught here.
func TestPartitionGrub2(t *testing.T) {
	noSleep(t)
	rec := &recorder{}

	layout, err := PartitionGrub2(context.Background(), rec.runner(), "/dev/sda")
	if err != nil {
		t.Fatalf("PartitionGrub2: %v", err)
	}

	want := Layout{Disk: "/dev/sda", ESP: "/dev/sda1", Boot: "/dev/sda2", Root: "/dev/sda3"}
	if layout != want {
		t.Errorf("layout = %+v, want %+v", layout, want)
	}

	// Happy path: sfdisk once, then the post-write udevadm settle.
	if len(rec.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d: %+v", len(rec.calls), rec.calls)
	}
	assertCall(t, rec, 0, "sfdisk", "--wipe=always", "/dev/sda")
	assertCall(t, rec, 1, "udevadm", "settle")

	script := rec.calls[0].stdin
	if !strings.Contains(script, "label: gpt") {
		t.Errorf("sfdisk script missing 'label: gpt':\n%s", script)
	}
	lines := partLines(script)
	if len(lines) != 3 {
		t.Fatalf("expected exactly 3 partition lines, got %d:\n%s", len(lines), script)
	}
	if !strings.Contains(lines[0], "size=2GiB") || !strings.Contains(lines[0], "type=uefi") {
		t.Errorf("EFI partition (size=2GiB, type=uefi) not found: %q", lines[0])
	}
	// /boot must have an explicit size so it doesn't consume remaining space.
	if !strings.Contains(lines[1], "size=2GiB") || !strings.Contains(lines[1], "type=linux") {
		t.Errorf("/boot partition (size=2GiB, type=linux) not found: %q", lines[1])
	}
	// Root must NOT have a size= field (fills remaining space).
	if strings.Contains(lines[2], "size=") {
		t.Errorf("root partition should have no size= (fills remaining space), got %q", lines[2])
	}
	if !strings.Contains(lines[2], "type=linux") {
		t.Errorf("root partition missing type=linux: %q", lines[2])
	}
}

// TestPartitionSystemdBoot verifies the 2-partition layout with a 2 GiB
// ESP (kernel+initrd pairs live on the ESP; 2 GiB holds booted +
// rollback + staged upgrade) and nvme p-suffix partition names.
func TestPartitionSystemdBoot(t *testing.T) {
	noSleep(t)
	rec := &recorder{}

	layout, err := PartitionSystemdBoot(context.Background(), rec.runner(), "/dev/nvme0n1")
	if err != nil {
		t.Fatalf("PartitionSystemdBoot: %v", err)
	}

	want := Layout{Disk: "/dev/nvme0n1", ESP: "/dev/nvme0n1p1", Boot: "", Root: "/dev/nvme0n1p2"}
	if layout != want {
		t.Errorf("layout = %+v, want %+v", layout, want)
	}

	assertCall(t, rec, 0, "sfdisk", "--wipe=always", "/dev/nvme0n1")

	script := rec.calls[0].stdin
	if !strings.Contains(script, "label: gpt") {
		t.Errorf("sfdisk script missing 'label: gpt':\n%s", script)
	}
	lines := partLines(script)
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 partition lines, got %d:\n%s", len(lines), script)
	}
	if !strings.Contains(lines[0], "size=2GiB") || !strings.Contains(lines[0], "type=uefi") {
		t.Errorf("EFI partition (size=2GiB, type=uefi) not found: %q", lines[0])
	}
	if strings.Contains(lines[1], "size=") {
		t.Errorf("root partition should have no size= (fills remaining space), got %q", lines[1])
	}
	if !strings.Contains(lines[1], "type=linux") {
		t.Errorf("root partition missing type=linux: %q", lines[1])
	}
}

// TestPartitionZFS verifies the ZFS layout: 1 GiB ESP + pool partition
// consuming the remaining space.
func TestPartitionZFS(t *testing.T) {
	noSleep(t)
	rec := &recorder{}

	layout, err := PartitionZFS(context.Background(), rec.runner(), "/dev/sdb")
	if err != nil {
		t.Fatalf("PartitionZFS: %v", err)
	}

	want := Layout{Disk: "/dev/sdb", ESP: "/dev/sdb1", Boot: "", Root: "/dev/sdb2"}
	if layout != want {
		t.Errorf("layout = %+v, want %+v", layout, want)
	}

	script := rec.calls[0].stdin
	lines := partLines(script)
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 partition lines, got %d:\n%s", len(lines), script)
	}
	if !strings.Contains(lines[0], "size=1GiB") || !strings.Contains(lines[0], "type=uefi") {
		t.Errorf("EFI partition (size=1GiB, type=uefi) not found: %q", lines[0])
	}
	if strings.Contains(lines[1], "size=") || !strings.Contains(lines[1], `name="zfs-pool"`) {
		t.Errorf("pool partition should be size-less and named zfs-pool, got %q", lines[1])
	}
}

// TestPartitionRetryForceNoReread is the regression test for the bug
// where a disk previously used for LVM caused sfdisk to fall back to
// --force --no-reread, leaving the kernel with the OLD (stale,
// potentially much smaller) partition table. The next mkfs then
// formatted the wrong-sized partition → "no space left on device". The
// fix: after --force --no-reread, partprobe forces a kernel re-read,
// and wipefs clears stale signatures so mkfs does not hit "Device or
// resource busy" on partitions from a prior aborted install.
func TestPartitionRetryForceNoReread(t *testing.T) {
	noSleep(t)

	// Pretend partitions 1-3 exist as device nodes; partition 4 does not.
	oldStat := stat
	stat = func(path string) (os.FileInfo, error) {
		switch path {
		case "/dev/vda1", "/dev/vda2", "/dev/vda3":
			return nil, nil
		}
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() { stat = oldStat })

	rec := &recorder{}
	rec.errFn = func(c call, nth int) error {
		// Fail only the FIRST sfdisk call, simulating "disk is currently
		// in use"; the --force retry and everything after succeed.
		if c.name == "sfdisk" && nth == 0 {
			return errors.New("disk is currently in use")
		}
		return nil
	}

	layout, err := PartitionGrub2(context.Background(), rec.runner(), "/dev/vda")
	if err != nil {
		t.Fatalf("PartitionGrub2: %v", err)
	}
	if layout.Root != "/dev/vda3" {
		t.Errorf("layout.Root = %q, want /dev/vda3", layout.Root)
	}

	// Exact command sequence of the retry path.
	assertCall(t, rec, 0, "sfdisk", "--wipe=always", "/dev/vda")
	assertCall(t, rec, 1, "udevadm", "settle")
	assertCall(t, rec, 2, "sfdisk", "--wipe=always", "--force", "--no-reread", "/dev/vda")
	assertCall(t, rec, 3, "partprobe", "/dev/vda")
	assertCall(t, rec, 4, "udevadm", "settle")
	assertCall(t, rec, 5, "wipefs", "-af", "/dev/vda1")
	assertCall(t, rec, 6, "wipefs", "-af", "/dev/vda2")
	assertCall(t, rec, 7, "wipefs", "-af", "/dev/vda3")
	assertCall(t, rec, 8, "udevadm", "settle")
	assertCall(t, rec, 9, "udevadm", "settle")
	if len(rec.calls) != 10 {
		t.Errorf("expected 10 calls, got %d: %+v", len(rec.calls), rec.calls)
	}

	// The retry must feed the same script on stdin as the first attempt.
	if rec.calls[2].stdin != rec.calls[0].stdin || rec.calls[2].stdin == "" {
		t.Errorf("retry sfdisk stdin = %q, want same script as first attempt %q",
			rec.calls[2].stdin, rec.calls[0].stdin)
	}
}

// TestPartitionRetryFailurePropagates verifies that when the --force
// retry also fails, the error is returned and no layout is produced.
func TestPartitionRetryFailurePropagates(t *testing.T) {
	noSleep(t)
	rec := &recorder{}
	rec.errFn = func(c call, nth int) error {
		if c.name == "sfdisk" {
			return errors.New("disk is currently in use")
		}
		return nil
	}

	if _, err := PartitionSystemdBoot(context.Background(), rec.runner(), "/dev/sda"); err == nil {
		t.Fatal("expected error when both sfdisk attempts fail, got nil")
	}
}

// TestPartitionPartprobeFailurePropagates verifies that when the first
// sfdisk fails, the --force --no-reread retry succeeds, but the
// subsequent partprobe fails, partition() surfaces a non-nil error and
// produces no Layout. Without a successful re-read the kernel still
// holds the OLD (stale, smaller) partition table, so continuing would
// let mkfs format wrong boundaries — the install must abort instead.
func TestPartitionPartprobeFailurePropagates(t *testing.T) {
	noSleep(t)
	rec := &recorder{}
	rec.errFn = func(c call, nth int) error {
		// Fail the first sfdisk (forcing the --no-reread recovery path),
		// then fail the partprobe on that recovery path.
		if c.name == "sfdisk" && nth == 0 {
			return errors.New("disk is currently in use")
		}
		if c.name == "partprobe" {
			return errors.New("partprobe: unable to re-read partition table")
		}
		return nil
	}

	layout, err := PartitionGrub2(context.Background(), rec.runner(), "/dev/vda")
	if err == nil {
		t.Fatal("expected error when partprobe fails on the --no-reread recovery path, got nil")
	}
	if layout != (Layout{}) {
		t.Errorf("expected zero Layout on partprobe failure, got %+v", layout)
	}
}
