package abimg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// sfdiskJSON builds an sfdisk --json fixture from (node, start, label)
// triples.
func sfdiskJSON(parts ...[3]string) []byte {
	entries := make([]string, len(parts))
	for i, p := range parts {
		entries[i] = fmt.Sprintf(`{"node": %q, "start": %s, "size": 2048, "type": "0FC63DAF-8483-4772-8E79-3D69D8477DE4", "name": %q}`, p[0], p[1], p[2])
	}
	return []byte(`{"partitiontable": {"label": "gpt", "device": "/dev/vdb", "unit": "sectors", "partitions": [` + strings.Join(entries, ",") + `]}}`)
}

// goodLayout is the on-disk shape the A/B image contract requires:
// esp, two _empty A/B slots, and var last.
func goodLayout() []byte {
	return sfdiskJSON(
		[3]string{"/dev/vdb1", "2048", "esp"},
		[3]string{"/dev/vdb2", "411648", "_empty"},
		[3]string{"/dev/vdb3", "413696", "_empty"},
		[3]string{"/dev/vdb4", "415744", "var"},
	)
}

// respondWith routes fake command output by command name (with special
// cases layered by tests).
func sfdiskResponder(layout []byte, diskSize string, fsType string) func(c call) ([]byte, error) {
	return func(c call) ([]byte, error) {
		switch {
		case c.name == "sfdisk" && slices.Equal(c.args[:1], []string{"--json"}):
			return layout, nil
		case c.name == "blockdev" && c.args[0] == "--getsize64":
			return []byte(diskSize + "\n"), nil
		case c.name == "blkid":
			return []byte(fsType + "\n"), nil
		}
		return nil, nil
	}
}

func TestValidateLayoutAccepts(t *testing.T) {
	rec := &recorder{respond: sfdiskResponder(goodLayout(), "0", "")}
	if err := ValidateLayout(context.Background(), rec.runner(), "/dev/vdb"); err != nil {
		t.Fatalf("ValidateLayout: %v", err)
	}
	assertCall(t, rec, 0, "sfdisk", "--json", "/dev/vdb")
}

func TestValidateLayoutMissingLabel(t *testing.T) {
	layout := sfdiskJSON(
		[3]string{"/dev/vdb1", "2048", "esp"},
		[3]string{"/dev/vdb2", "411648", "_empty"},
		[3]string{"/dev/vdb3", "413696", "_empty"},
	)
	rec := &recorder{respond: sfdiskResponder(layout, "0", "")}
	err := ValidateLayout(context.Background(), rec.runner(), "/dev/vdb")
	if err == nil || !strings.Contains(err.Error(), "missing required GPT label: var") {
		t.Fatalf("err = %v, want missing var label", err)
	}
}

func TestValidateLayoutSingleEmptySlot(t *testing.T) {
	// Pinned by snosi test/snosi-install-test.sh: the single-empty-slot
	// error message is specific ("two empty A/B slots").
	layout := sfdiskJSON(
		[3]string{"/dev/vdb1", "2048", "esp"},
		[3]string{"/dev/vdb2", "411648", "_empty"},
		[3]string{"/dev/vdb3", "415744", "var"},
	)
	rec := &recorder{respond: sfdiskResponder(layout, "0", "")}
	err := ValidateLayout(context.Background(), rec.runner(), "/dev/vdb")
	if err == nil || !strings.Contains(err.Error(), "two empty A/B slots") {
		t.Fatalf("err = %v, want two-empty-slots error", err)
	}
}

func TestVarAndESPPartition(t *testing.T) {
	rec := &recorder{respond: sfdiskResponder(goodLayout(), "0", "")}
	r := rec.runner()
	varPart, err := VarPartition(context.Background(), r, "/dev/vdb")
	if err != nil || varPart != "/dev/vdb4" {
		t.Errorf("VarPartition = %q, %v; want /dev/vdb4", varPart, err)
	}
	esp, err := ESPPartition(context.Background(), r, "/dev/vdb")
	if err != nil || esp != "/dev/vdb1" {
		t.Errorf("ESPPartition = %q, %v; want /dev/vdb1", esp, err)
	}
}

func TestVarPartitionMissing(t *testing.T) {
	layout := sfdiskJSON([3]string{"/dev/vdb1", "2048", "esp"})
	rec := &recorder{respond: sfdiskResponder(layout, "0", "")}
	if _, err := VarPartition(context.Background(), rec.runner(), "/dev/vdb"); err == nil {
		t.Fatal("want error for missing var partition")
	}
}

func TestRereadptSettleBusyRetry(t *testing.T) {
	noSleep(t)
	attempts := 0
	rec := &recorder{respond: func(c call) ([]byte, error) {
		if c.name == "blockdev" {
			attempts++
			if attempts == 1 {
				return nil, errors.New("blockdev: ioctl error on BLKRRPART: Device or resource busy")
			}
		}
		return nil, nil
	}}
	if err := RereadptSettle(context.Background(), rec.runner(), "/dev/vdb"); err != nil {
		t.Fatalf("RereadptSettle: %v", err)
	}
	// settle, rereadpt(busy), settle, rereadpt(ok), settle
	assertCall(t, rec, 0, "udevadm", "settle")
	assertCall(t, rec, 1, "blockdev", "--rereadpt", "/dev/vdb")
	assertCall(t, rec, 2, "udevadm", "settle")
	assertCall(t, rec, 3, "blockdev", "--rereadpt", "/dev/vdb")
	assertCall(t, rec, 4, "udevadm", "settle")
}

func TestRereadptSettleNonBusyFatal(t *testing.T) {
	noSleep(t)
	rec := &recorder{respond: func(c call) ([]byte, error) {
		if c.name == "blockdev" {
			return nil, errors.New("blockdev: cannot open /dev/vdb: No such file or directory")
		}
		return nil, nil
	}}
	err := RereadptSettle(context.Background(), rec.runner(), "/dev/vdb")
	if err == nil || !strings.Contains(err.Error(), "No such file") {
		t.Fatalf("err = %v, want immediate non-busy failure", err)
	}
	if len(rec.calls) != 2 { // settle + single rereadpt, no retries
		t.Errorf("calls = %d, want 2: %+v", len(rec.calls), rec.calls)
	}
}

func TestRereadptSettleBusyExhausted(t *testing.T) {
	noSleep(t)
	rec := &recorder{respond: func(c call) ([]byte, error) {
		if c.name == "blockdev" {
			return nil, errors.New("Device or resource busy")
		}
		return nil, nil
	}}
	err := RereadptSettle(context.Background(), rec.runner(), "/dev/vdb")
	if err == nil || !strings.Contains(err.Error(), "after 5 attempts") {
		t.Fatalf("err = %v, want retry-budget exhaustion", err)
	}
}

func TestRelocateAndGrowVar(t *testing.T) {
	noSleep(t)
	// Disk (100 GiB) larger than the image (10 GiB) -> full grow path.
	rec := &recorder{respond: sfdiskResponder(goodLayout(), fmt.Sprint(int64(100)<<30), "ext4")}
	varPart, err := RelocateAndGrowVar(context.Background(), rec.runner(), "/dev/vdb", 10<<30)
	if err != nil {
		t.Fatalf("RelocateAndGrowVar: %v", err)
	}
	// The var partition path is a Go return value — in the bash
	// original it traveled via command-substituted stdout and was once
	// corrupted by sfdisk/e2fsck/resize2fs stdout noise.
	if varPart != "/dev/vdb4" {
		t.Errorf("varPart = %q, want /dev/vdb4", varPart)
	}
	assertCall(t, rec, 0, "blockdev", "--getsize64", "/dev/vdb")
	assertCall(t, rec, 1, "sfdisk", "--lock=yes", "--relocate", "gpt-bak-std", "/dev/vdb")
	assertCall(t, rec, 2, "udevadm", "settle")
	assertCall(t, rec, 3, "blockdev", "--rereadpt", "/dev/vdb")
	assertCall(t, rec, 4, "udevadm", "settle")
	assertCall(t, rec, 5, "sfdisk", "--json", "/dev/vdb")
	assertCall(t, rec, 6, "sfdisk", "--lock=yes", "--no-reread", "-N", "4", "/dev/vdb")
	if rec.calls[6].stdin != "start=415744,size=+\n" {
		t.Errorf("sfdisk grow stdin = %q, want start=415744,size=+", rec.calls[6].stdin)
	}
	assertCall(t, rec, 7, "udevadm", "settle")
	assertCall(t, rec, 8, "blockdev", "--rereadpt", "/dev/vdb")
	assertCall(t, rec, 9, "udevadm", "settle")
	assertCall(t, rec, 10, "sfdisk", "--json", "/dev/vdb") // start-unchanged check
	// No filesystem operations: the fs is recreated at full size by
	// FormatVar right after (divergence from snosi, see RelocateAndGrowVar).
	for _, banned := range []string{"e2fsck", "resize2fs", "blkid", "mount"} {
		if i := rec.findCall(banned); i != -1 {
			t.Errorf("%s must not run in partition-only grow: %v", banned, rec.calls[i].argv())
		}
	}
	if len(rec.calls) != 11 {
		t.Errorf("calls = %d, want 11: %+v", len(rec.calls), rec.calls)
	}
}

func TestRelocateAndGrowVarNoGrow(t *testing.T) {
	// Disk exactly the image size: no relocate, no fsck, no resize —
	// just resolution and sanity checks.
	rec := &recorder{respond: sfdiskResponder(goodLayout(), fmt.Sprint(int64(10)<<30), "ext4")}
	varPart, err := RelocateAndGrowVar(context.Background(), rec.runner(), "/dev/vdb", 10<<30)
	if err != nil {
		t.Fatalf("RelocateAndGrowVar: %v", err)
	}
	if varPart != "/dev/vdb4" {
		t.Errorf("varPart = %q, want /dev/vdb4", varPart)
	}
	for _, banned := range []string{"e2fsck", "resize2fs", "mount"} {
		if i := rec.findCall(banned); i != -1 {
			t.Errorf("%s must not run without grow: %v", banned, rec.calls[i].argv())
		}
	}
	for _, c := range rec.calls {
		if c.name == "sfdisk" && slices.Contains(c.args, "--relocate") {
			t.Errorf("relocate must not run without grow: %v", c.argv())
		}
	}
}

func TestRelocateAndGrowVarNotLast(t *testing.T) {
	noSleep(t)
	layout := sfdiskJSON(
		[3]string{"/dev/vdb1", "2048", "esp"},
		[3]string{"/dev/vdb2", "411648", "var"},
		[3]string{"/dev/vdb3", "413696", "_empty"},
		[3]string{"/dev/vdb4", "415744", "_empty"},
	)
	rec := &recorder{respond: sfdiskResponder(layout, fmt.Sprint(int64(100)<<30), "ext4")}
	_, err := RelocateAndGrowVar(context.Background(), rec.runner(), "/dev/vdb", 10<<30)
	if err == nil || !strings.Contains(err.Error(), "not the final physical partition") {
		t.Fatalf("err = %v, want var-not-last error", err)
	}
}
