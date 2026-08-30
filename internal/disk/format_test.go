package disk

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/runner"
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

// thawRecorder is a recorder whose fake exec honors context cancellation
// the way exec.CommandContext does: a command handed an already-cancelled
// context fails without running, and the cancellation state is recorded
// per call. That is what makes "the recovery thaw ran on a context
// detached from the caller's" a mechanical assertion rather than a
// comment.
type thawRecorder struct {
	calls   []call
	ctxDone []bool
	failFn  func(c call, nth int) error
}

func (rec *thawRecorder) runner() *runner.Runner {
	return runner.NewFake(
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			c := call{name: name, args: args}
			rec.calls = append(rec.calls, c)
			nth := len(rec.calls) - 1
			cerr := ctx.Err()
			rec.ctxDone = append(rec.ctxDone, cerr != nil)
			if cerr != nil {
				return nil, fmt.Errorf("%s: %w", name, cerr)
			}
			if rec.failFn != nil {
				return nil, rec.failFn(c, nth)
			}
			return nil, nil
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
}

func (rec *thawRecorder) assertCall(t *testing.T, i int, name string, args ...string) {
	t.Helper()
	if i >= len(rec.calls) {
		t.Fatalf("expected call %d (%s %v), but only %d calls made: %+v", i, name, args, len(rec.calls), rec.calls)
	}
	c := rec.calls[i]
	if c.name != name || !slices.Equal(c.args, args) {
		t.Errorf("call %d = %s %v, want %s %v", i, c.name, c.args, name, args)
	}
}

// assertRecoveredThawSequence checks the five-call shape every recovery
// case shares: the unchanged fstrim → remount-ro → freeze prefix, the
// normal thaw, and the recovery thaw — which must have run on a live
// context, or it could not have thawed anything.
func assertRecoveredThawSequence(t *testing.T, rec *thawRecorder) {
	t.Helper()
	if len(rec.calls) != 5 {
		t.Fatalf("expected 5 calls (finalize + recovery thaw), got %d: %+v", len(rec.calls), rec.calls)
	}
	rec.assertCall(t, 0, "fstrim", "--quiet-unsupported", "-v", "/mnt/target")
	rec.assertCall(t, 1, "mount", "-o", "remount,ro", "/mnt/target")
	rec.assertCall(t, 2, "fsfreeze", "-f", "/mnt/target")
	rec.assertCall(t, 3, "fsfreeze", "-u", "/mnt/target")
	rec.assertCall(t, 4, "fsfreeze", "-u", "/mnt/target")
	if rec.ctxDone[4] {
		t.Error("recovery thaw ran on an already-cancelled context; it cannot thaw the target")
	}
}

// A cancelled context is the case that matters most: it is what kills the
// normal thaw, and a retry inheriting it would be killed the same way,
// leaving the target frozen.
func TestFinalizeFilesystemThawsAfterCancellation(t *testing.T) {
	rec := &thawRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel the moment the freeze lands: from here the target is frozen
	// and the caller's context is dead.
	rec.failFn = func(c call, nth int) error {
		if c.name == "fsfreeze" && len(c.args) > 0 && c.args[0] == "-f" {
			cancel()
		}
		return nil
	}

	err := FinalizeFilesystem(ctx, rec.runner(), "/mnt/target")
	if err == nil {
		t.Fatal("expected the cancelled thaw to be reported, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected the cancellation to be preserved in %v", err)
	}
	assertRecoveredThawSequence(t, rec)
	if !rec.ctxDone[3] {
		t.Error("expected the normal thaw to have seen the cancelled context")
	}
}

func TestFinalizeFilesystemRecoversFromFailedThaw(t *testing.T) {
	rec := &thawRecorder{}
	errThaw := errors.New("fsfreeze: /mnt/target: Device or resource busy")
	rec.failFn = func(c call, nth int) error {
		if nth == 3 {
			return errThaw
		}
		return nil
	}

	err := FinalizeFilesystem(context.Background(), rec.runner(), "/mnt/target")
	if !errors.Is(err, errThaw) {
		t.Fatalf("expected the original thaw failure to be preserved, got %v", err)
	}
	assertRecoveredThawSequence(t, rec)
}

func TestFinalizeFilesystemReportsFailedRecoveryThaw(t *testing.T) {
	rec := &thawRecorder{}
	errThaw := errors.New("fsfreeze: /mnt/target: Device or resource busy")
	errRecovery := errors.New("fsfreeze: /mnt/target: Invalid argument")
	rec.failFn = func(c call, nth int) error {
		switch nth {
		case 3:
			return errThaw
		case 4:
			return errRecovery
		}
		return nil
	}

	err := FinalizeFilesystem(context.Background(), rec.runner(), "/mnt/target")
	if err == nil {
		t.Fatal("expected an error when both thaw attempts fail, got nil")
	}
	// Neither failure may be hidden by the other.
	if !errors.Is(err, errThaw) {
		t.Errorf("original thaw failure missing from %v", err)
	}
	if !errors.Is(err, errRecovery) {
		t.Errorf("recovery thaw failure missing from %v", err)
	}
	if !strings.Contains(err.Error(), "may still be frozen") {
		t.Errorf("expected the error to warn that the target is still frozen, got %v", err)
	}
	assertRecoveredThawSequence(t, rec)
}

// The unchanged path: a successful finalize issues exactly one thaw and
// never reaches the recovery.
func TestFinalizeFilesystemSuccessThawsOnce(t *testing.T) {
	rec := &thawRecorder{}
	if err := FinalizeFilesystem(context.Background(), rec.runner(), "/mnt/target"); err != nil {
		t.Fatalf("FinalizeFilesystem: %v", err)
	}
	thaws := 0
	for _, c := range rec.calls {
		if c.name == "fsfreeze" && len(c.args) > 0 && c.args[0] == "-u" {
			thaws++
		}
	}
	if thaws != 1 {
		t.Errorf("expected exactly 1 thaw on the success path, got %d: %+v", thaws, rec.calls)
	}
}
