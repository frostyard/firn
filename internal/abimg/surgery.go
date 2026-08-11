// Ported from frostyard/snosi (GPL-3.0-only), shared/native-installer/tree/usr/libexec/snosi-install (stream_download_verify, validate_disk_image_layout, relocate_and_grow_var, format_var, enroll_tpm_var, mok_import).

package abimg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/frostyard/firn/internal/runner"
)

// Test seams: partition surgery sleeps between kernel-visible retries
// and creates scratch mount points. Tests replace both so no real time
// passes and mount-point paths are deterministic.
var (
	sleep     = time.Sleep
	mkdirTemp = os.MkdirTemp
)

// sfdiskPartition is one entry of sfdisk --json's partition array.
type sfdiskPartition struct {
	Node  string `json:"node"`
	Start int64  `json:"start"`
	Size  int64  `json:"size"`
	Name  string `json:"name"` // GPT partition label
}

// sfdiskTable is the top-level shape of sfdisk --json output.
type sfdiskTable struct {
	PartitionTable struct {
		Partitions []sfdiskPartition `json:"partitions"`
	} `json:"partitiontable"`
}

// partitions returns disk's partition entries via sfdisk --json.
func partitions(ctx context.Context, r *runner.Runner, disk string) ([]sfdiskPartition, error) {
	out, err := r.Run(ctx, "sfdisk", "--json", disk)
	if err != nil {
		return nil, fmt.Errorf("abimg: sfdisk --json %s: %w", disk, err)
	}
	var t sfdiskTable
	if err := json.Unmarshal(out, &t); err != nil {
		return nil, fmt.Errorf("abimg: parse sfdisk --json %s: %w", disk, err)
	}
	return t.PartitionTable.Partitions, nil
}

// partitionByLabel returns the single partition on disk carrying the
// given GPT label, erroring on zero or multiple matches.
func partitionByLabel(ctx context.Context, r *runner.Runner, disk, label string) (sfdiskPartition, error) {
	parts, err := partitions(ctx, r, disk)
	if err != nil {
		return sfdiskPartition{}, err
	}
	var matches []sfdiskPartition
	for _, p := range parts {
		if p.Name == label {
			matches = append(matches, p)
		}
	}
	if len(matches) != 1 {
		return sfdiskPartition{}, fmt.Errorf("abimg: expected exactly one %s partition on %s, found %d", label, disk, len(matches))
	}
	return matches[0], nil
}

// VarPartition resolves the var partition device on disk by GPT label.
func VarPartition(ctx context.Context, r *runner.Runner, disk string) (string, error) {
	p, err := partitionByLabel(ctx, r, disk, "var")
	if err != nil {
		return "", err
	}
	return p.Node, nil
}

// ESPPartition resolves the esp partition device on disk by GPT label.
func ESPPartition(ctx context.Context, r *runner.Runner, disk string) (string, error) {
	p, err := partitionByLabel(ctx, r, disk, "esp")
	if err != nil {
		return "", err
	}
	return p.Node, nil
}

// RootPartition resolves the populated A-slot root partition: the one
// whose GPT label ends in "_r" (repart names slot A
// "<channel>_<version>_r"; the B slot ships as "_empty"). The erofs
// baseline /etc that overlay seeding reads lives on it.
func RootPartition(ctx context.Context, r *runner.Runner, disk string) (string, error) {
	parts, err := partitions(ctx, r, disk)
	if err != nil {
		return "", err
	}
	var matches []sfdiskPartition
	for _, p := range parts {
		if strings.HasSuffix(p.Name, "_r") && p.Name != "_empty" {
			matches = append(matches, p)
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("abimg: expected exactly one populated root slot (label *_r) on %s, found %d", disk, len(matches))
	}
	return matches[0].Node, nil
}

// ValidateLayout ports validate_disk_image_layout: a POST-WRITE sanity
// check, run right after StreamWrite + RereadptSettle, BEFORE
// RelocateAndGrowVar does any partition surgery on the disk. disk here
// is the just-written TARGET disk (or a regular file in tests), not a
// separate downloaded image file — sfdisk --json works identically
// against either. A checksum-verified download (StreamWrite already
// confirmed the exact compressed bytes match the signed index) that
// nonetheless produces the wrong GPT shape on disk means either the
// index pointed at the wrong artifact for this product/channel or the
// on-disk partition layout contract itself drifted — both are bugs
// worth stopping for loudly, not silently working around. The error
// return aborts here WITHOUT calling WipeTarget: unlike a download/
// checksum failure, the disk already holds exactly the bytes that were
// verified, and there is nothing unsafe about leaving them in place for
// a human to inspect.
func ValidateLayout(ctx context.Context, r *runner.Runner, disk string) error {
	parts, err := partitions(ctx, r, disk)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, p := range parts {
		counts[p.Name]++
	}
	for _, label := range []string{"esp", "_empty", "var"} {
		if counts[label] == 0 {
			return fmt.Errorf("abimg: image is missing required GPT label: %s", label)
		}
	}
	if counts["_empty"] != 2 {
		return fmt.Errorf("abimg: image must contain two empty A/B slots, found %d", counts["_empty"])
	}
	return nil
}

// RereadptSettle ports rereadpt_settle: tells the kernel to reread
// disk's partition table (blockdev --rereadpt) and waits for udev to
// finish processing the resulting partition device nodes (udevadm
// settle). Issuing --rereadpt immediately after a partition-table-
// modifying command (sfdisk --relocate, sfdisk -N, or streaming a full
// disk image onto the target) can race systemd-udevd's own re-probe of
// that same change: the kernel returns BLKRRPART EBUSY ("Device or
// resource busy") when a partition node the previous probe still has
// open. This is transient — settling and retrying resolves it — so
// only a failure that persists across the retry budget is treated as
// fatal. Reproduced ~1/6 runs under snosi's test/snosi-install-test.sh
// root-gated harness (2026-07-15).
func RereadptSettle(ctx context.Context, r *runner.Runner, disk string) error {
	if _, err := r.Run(ctx, "udevadm", "settle"); err != nil {
		return fmt.Errorf("abimg: udevadm settle: %w", err)
	}
	var lastErr error
	for attempt := 1; attempt <= 5; attempt++ {
		out, err := r.Run(ctx, "blockdev", "--rereadpt", disk)
		if err == nil {
			if _, err := r.Run(ctx, "udevadm", "settle"); err != nil {
				return fmt.Errorf("abimg: udevadm settle: %w", err)
			}
			return nil
		}
		lastErr = err
		if strings.Contains(err.Error(), "busy") || strings.Contains(string(out), "busy") {
			_, _ = r.Run(ctx, "udevadm", "settle")
			sleep(time.Second)
			continue
		}
		return fmt.Errorf("abimg: blockdev --rereadpt %s failed: %w", disk, err)
	}
	return fmt.Errorf("abimg: blockdev --rereadpt %s failed after 5 attempts (device or resource busy): %w", disk, lastErr)
}

// RelocateAndGrowVar ports relocate_and_grow_var: after the image is
// streamed and validated, relocate the backup GPT to the disk's real
// end and — only when the disk is larger than the image — grow the
// final var PARTITION into the reclaimed space.
// decompressedSize is StreamWrite's decompressed byte count — the true
// image size the disk is compared against.
//
// Divergence from snosi: the bash also e2fsck'd and resize2fs'd the
// image's shipped ext4 inside the grown partition — dead work, because
// its own format_var (and firn's format-var step) immediately
// recreates the filesystem from scratch anyway; and under ADR-0008 the
// recipe's chosen filesystem may not even match the image's shipped
// one, so a pre-format fs assertion is wrong by design. Firn grows the
// partition only; the fresh filesystem is created at full size by
// FormatVar right after.
//
// It returns the var partition device path as a Go return value. In
// the bash original this function's ONLY contract with its caller was
// stdout == exactly one line, the var partition device path (main()
// called it as `var_part="$(relocate_and_grow_var ...)"`), and that
// contract was broken live (test/native-installer-e2e-test.sh's first
// real install, 2026-07-15): sfdisk/e2fsck/resize2fs all write their
// normal progress/summary output to STDOUT, not stderr — with no
// redirection, command substitution captured ALL of that noise
// (partition table dumps, filesystem-check summaries, resize progress)
// into $var_part instead of just the intended device path, so every
// later use of $var_part (cryptsetup, mkfs.ext4, systemd-cryptenroll,
// ...) received a garbage multi-line string instead of e.g.
// "/dev/vdb6" — surfacing as cryptsetup/systemd-cryptenroll "Block
// device required" or mapper-not-found errors deep inside
// format_var()/enroll_tpm_var(), nowhere near the actual root cause.
// The bash fix was blanket `>&2` redirection on every noisy command; in
// Go the device path travels as a typed return value and command stdout
// is never conflated with results, so that entire hazard class is
// structurally eliminated — no redirection discipline required.
func RelocateAndGrowVar(ctx context.Context, r *runner.Runner, disk string, decompressedSize int64) (varPart string, err error) {
	diskSize, err := diskSizeBytes(ctx, r, disk)
	if err != nil {
		return "", err
	}
	grow := diskSize > decompressedSize

	if grow {
		// The image's backup GPT sits where the IMAGE ended, not where
		// the disk ends; move it to the disk's real end so the grown
		// partition entry is legal.
		if _, err := r.Run(ctx, "sfdisk", "--lock=yes", "--relocate", "gpt-bak-std", disk); err != nil {
			return "", fmt.Errorf("abimg: sfdisk --relocate gpt-bak-std %s: %w", disk, err)
		}
		if err := RereadptSettle(ctx, r, disk); err != nil {
			return "", err
		}
	}

	parts, err := partitions(ctx, r, disk)
	if err != nil {
		return "", err
	}
	var vars []sfdiskPartition
	last := sfdiskPartition{Start: -1}
	for _, p := range parts {
		if p.Name == "var" {
			vars = append(vars, p)
		}
		if p.Start > last.Start {
			last = p
		}
	}
	if len(vars) != 1 {
		return "", fmt.Errorf("abimg: expected exactly one var partition, found %d", len(vars))
	}
	v := vars[0]
	// Growing in place is only sound if no partition lives beyond var.
	if last.Node != v.Node {
		return "", fmt.Errorf("abimg: var is not the final physical partition")
	}
	if grow {
		varNum, err := partitionNumber(v.Node)
		if err != nil {
			return "", err
		}
		script := fmt.Sprintf("start=%d,size=+\n", v.Start)
		if _, err := r.RunInput(ctx, script, "sfdisk", "--lock=yes", "--no-reread", "-N", strconv.Itoa(varNum), disk); err != nil {
			return "", fmt.Errorf("abimg: sfdisk grow partition %d on %s: %w", varNum, disk, err)
		}
		if err := RereadptSettle(ctx, r, disk); err != nil {
			return "", err
		}
		// The grow script pins start= explicitly; verify sfdisk did not
		// move the partition (a moved start would shear the filesystem).
		after, err := partitionByLabel(ctx, r, disk, "var")
		if err != nil {
			return "", err
		}
		if after.Start != v.Start {
			return "", fmt.Errorf("abimg: var partition start changed unexpectedly (%d -> %d)", v.Start, after.Start)
		}
	}

	return v.Node, nil
}

// diskSizeBytes returns disk's size in bytes.
func diskSizeBytes(ctx context.Context, r *runner.Runner, disk string) (int64, error) {
	out, err := r.Run(ctx, "blockdev", "--getsize64", disk)
	if err != nil {
		return 0, fmt.Errorf("abimg: blockdev --getsize64 %s: %w", disk, err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("abimg: parse disk size %q: %w", strings.TrimSpace(string(out)), err)
	}
	return n, nil
}

// partitionNumber extracts the partition number from a partition device
// node's trailing digits (/dev/vdb6 -> 6, /dev/nvme0n1p3 -> 3).
func partitionNumber(node string) (int, error) {
	i := len(node)
	for i > 0 && node[i-1] >= '0' && node[i-1] <= '9' {
		i--
	}
	if i == len(node) {
		return 0, fmt.Errorf("abimg: no partition number in device node %q", node)
	}
	n, err := strconv.Atoi(node[i:])
	if err != nil {
		return 0, fmt.Errorf("abimg: parse partition number of %q: %w", node, err)
	}
	return n, nil
}
