// Ported from frostyard/snosi (GPL-3.0-only), shared/native-installer/tree/usr/libexec/snosi-install (stream_download_verify, validate_disk_image_layout, relocate_and_grow_var, format_var, enroll_tpm_var, mok_import).

// Package abimg streams, verifies, and adapts prebuilt A/B disk images
// onto a target disk: streamed download + compressed-hash verify,
// post-write layout validation, backup-GPT relocation and /var grow,
// /var formatting, TPM enrollment against the image's own UKI, and MOK
// staging.
package abimg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/frostyard/firn/internal/runner"
)

// syncEvery is how many decompressed bytes are written to the target
// between explicit sync calls. Periodic sync keeps the page cache from
// absorbing tens of gigabytes and then stalling at the end; the final
// fsync preserves dd conv=fsync's completion semantics (data durable
// before the pipeline is declared successful).
const syncEvery = 256 << 20 // 256 MiB

// StreamOpts configures StreamWrite.
type StreamOpts struct {
	URL    string // image URL (xz-compressed raw disk image)
	Sha256 string // expected hex sha256 of the COMPRESSED stream, from the signed index
	Disk   string // target block device (or regular file in tests)
	Client *http.Client
	// Progress, if non-nil, is called as compressed bytes arrive.
	// totalBytes is the Content-Length when known, else 0.
	Progress func(bytes, totalBytes int64)
}

// StreamWrite ports stream_download_verify: it streams the compressed
// image from o.URL through xz -dc onto o.Disk in one pass, hashing the
// compressed stream as it goes, and returns the exact number of
// DEcompressed bytes written — the true "image size" needed later to
// decide whether the target disk has grow-worthy extra capacity
// (RelocateAndGrowVar). This is NOT the same number as the target
// disk's own size (which is normally >= the image and is what gets
// grown INTO).
//
// The bash pipeline needed a tee + FIFO + backgrounded sha256sum for
// the compressed hash and a second tee + FIFO + backgrounded `wc -c`
// for the decompressed byte count; here the hash is a sha256 tee
// (hash.Hash) on the HTTP body and the count is an io.Writer tee in
// front of the disk — no FIFOs, no background processes, no PIPESTATUS
// bookkeeping.
//
// The compressed sha256 is verified AFTER the write completes: that is
// the accepted stream-then-verify posture (no 2x scratch space on live
// media — see docs/design/architecture.md, "A/B stream-then-verify
// residue"). On a hash mismatch, or any earlier download/decompress/
// write failure, WipeTarget is invoked so nothing on the disk is
// bootable or auto-discoverable, and the original error is returned.
func StreamWrite(ctx context.Context, r *runner.Runner, o StreamOpts) (decompressed int64, err error) {
	n, err := streamWrite(ctx, r, o)
	if err != nil {
		// Any failure or mismatch leaves unauthenticated bytes on the
		// disk; discard the GPT regions and signatures before
		// reporting. The wipe's own error (if any) is joined so it is
		// not silently lost, but the pipeline error stays primary.
		if werr := WipeTarget(ctx, r, o.Disk); werr != nil {
			return 0, errors.Join(err, werr)
		}
		return 0, err
	}
	return n, nil
}

// streamWrite runs the pipeline; StreamWrite owns the failure-path wipe.
func streamWrite(ctx context.Context, r *runner.Runner, o StreamOpts) (int64, error) {
	client := o.Client
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.URL, nil)
	if err != nil {
		return 0, fmt.Errorf("abimg: request %s: %w", o.URL, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("abimg: download %s: %w", o.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("abimg: download %s: unexpected status %s", o.URL, resp.Status)
	}

	var total int64
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}

	// Stage 1→2: HTTP body → sha256 tee. The hash covers the COMPRESSED
	// stream, matching the signed index's digest. Progress counts
	// compressed bytes for the same reason: Content-Length is the
	// compressed size.
	hasher := sha256.New()
	var body io.Reader = &progressReader{r: resp.Body, total: total, fn: o.Progress}
	body = io.TeeReader(body, hasher)

	// Stage 4→5: count decompressed bytes, then write to the target
	// with periodic sync and a final fsync (dd conv=fsync semantics).
	f, err := os.OpenFile(o.Disk, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return 0, fmt.Errorf("abimg: open target %s: %w", o.Disk, err)
	}
	sw := &syncWriter{f: f}
	counter := &countWriter{w: sw}

	// Stage 3: xz -dc between the hash tee and the counting disk writer.
	streamErr := r.RunStream(ctx, body, counter, "xz", "-dc")

	syncErr := f.Sync()
	closeErr := f.Close()
	if streamErr != nil {
		return 0, fmt.Errorf("abimg: download/decompress/write failed: %w", streamErr)
	}
	if syncErr != nil {
		return 0, fmt.Errorf("abimg: fsync %s: %w", o.Disk, syncErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("abimg: close %s: %w", o.Disk, closeErr)
	}

	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != o.Sha256 {
		return 0, fmt.Errorf("abimg: checksum mismatch on %s: expected %s, got %s", o.Disk, o.Sha256, actual)
	}
	return counter.n, nil
}

// progressReader counts compressed bytes off the HTTP body and reports
// them to fn (when set).
type progressReader struct {
	r     io.Reader
	n     int64
	total int64
	fn    func(bytes, totalBytes int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.n += int64(n)
		if p.fn != nil {
			p.fn(p.n, p.total)
		}
	}
	return n, err
}

// countWriter counts decompressed bytes on their way to the disk —
// the Go replacement for the bash version's second tee + FIFO +
// backgrounded `wc -c`.
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n += int64(n)
	return n, err
}

// syncWriter writes to f, calling Sync every syncEvery bytes.
type syncWriter struct {
	f          *os.File
	sinceFsync int64
}

func (s *syncWriter) Write(b []byte) (int, error) {
	n, err := s.f.Write(b)
	if err != nil {
		return n, err
	}
	s.sinceFsync += int64(n)
	if s.sinceFsync >= syncEvery {
		s.sinceFsync = 0
		if err := s.f.Sync(); err != nil {
			return n, err
		}
	}
	return n, nil
}

// wipeSpan is how much of each end of the disk WipeTarget zeroes: the
// primary GPT (header + entries) lives in the first sectors and the
// backup GPT in the last, and 2 MiB per end covers both with margin at
// any sector size.
const wipeSpan = 2 << 20 // 2 MiB

// WipeTarget renders a partially-written or verify-failed disk
// non-bootable and non-discoverable.
//
// DELIBERATE HARDENING over snosi's wipe_target, which ran only
// `wipefs --all` (best-effort, errors swallowed): wipefs erases known
// signature magic but a torn write can leave structures wipefs does not
// recognize, and a wipefs failure left everything in place silently.
// Here the first 2 MiB and last 2 MiB of the disk (primary GPT + backup
// GPT regions) are zeroed outright FIRST, then `wipefs --all` clears
// any remaining filesystem/RAID signatures, then the kernel rereads the
// partition table — and every step's failure is reported, not
// swallowed. Unauthenticated image bytes may still remain in the middle
// of the platter (the accepted stream-then-verify residue), but nothing
// on the disk is bootable or auto-discoverable afterwards.
func WipeTarget(ctx context.Context, r *runner.Runner, disk string) error {
	if err := zeroGPTRegions(disk); err != nil {
		return err
	}
	if _, err := r.Run(ctx, "wipefs", "--all", disk); err != nil {
		return fmt.Errorf("abimg: wipefs --all %s: %w", disk, err)
	}
	return RereadptSettle(ctx, r, disk)
}

// zeroGPTRegions zeroes the first and last wipeSpan bytes of disk.
func zeroGPTRegions(disk string) error {
	f, err := os.OpenFile(disk, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("abimg: wipe open %s: %w", disk, err)
	}
	defer f.Close()
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("abimg: wipe seek %s: %w", disk, err)
	}
	zeros := make([]byte, wipeSpan)
	head := int64(wipeSpan)
	if head > size {
		head = size
	}
	if _, err := f.WriteAt(zeros[:head], 0); err != nil {
		return fmt.Errorf("abimg: zero primary GPT region on %s: %w", disk, err)
	}
	if tailStart := size - wipeSpan; tailStart > head {
		if _, err := f.WriteAt(zeros, tailStart); err != nil {
			return fmt.Errorf("abimg: zero backup GPT region on %s: %w", disk, err)
		}
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("abimg: wipe sync %s: %w", disk, err)
	}
	return nil
}
