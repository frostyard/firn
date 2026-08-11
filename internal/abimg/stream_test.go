package abimg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// recorder captures every command run through a fake runner. respond,
// if set, supplies per-call output/error; RunStream calls additionally
// see the attached reader/writer via runner.Stream and by default copy
// stdin to stdout (a stand-in for xz -dc).
type recorder struct {
	calls   []call
	respond func(c call) ([]byte, error)
}

func (rec *recorder) runner() *runner.Runner {
	return runner.NewFake(
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			c := call{name: name, args: args}
			if in, ok := runner.Stdin(ctx); ok {
				c.stdin = in
			}
			rec.calls = append(rec.calls, c)
			var out []byte
			var err error
			if rec.respond != nil {
				out, err = rec.respond(c)
			}
			if in, w, ok := runner.Stream(ctx); ok && err == nil {
				if _, cerr := io.Copy(w, in); cerr != nil {
					return nil, cerr
				}
			}
			return out, err
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
}

// argv flattens a call for comparison.
func (c call) argv() []string {
	return append([]string{c.name}, c.args...)
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

// findCall returns the first recorded call of name, or -1.
func (rec *recorder) findCall(name string) int {
	for i, c := range rec.calls {
		if c.name == name {
			return i
		}
	}
	return -1
}

// noSleep disables the real retry sleeps for the duration of the test.
func noSleep(t *testing.T) {
	t.Helper()
	old := sleep
	sleep = func(time.Duration) {}
	t.Cleanup(func() { sleep = old })
}

// pinMkdirTemp makes scratch mount-point creation deterministic.
func pinMkdirTemp(t *testing.T, dir string) {
	t.Helper()
	old := mkdirTemp
	mkdirTemp = func(string, string) (string, error) { return dir, nil }
	t.Cleanup(func() { mkdirTemp = old })
}

// sum256 returns the hex sha256 of b.
func sum256(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestStreamWriteSuccess(t *testing.T) {
	// "Compressed" fixture bytes; the fake xz copies stdin to stdout,
	// so the decompressed image equals the payload.
	payload := bytes.Repeat([]byte("firn-ab-image!"), 512)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.Write(payload)
	}))
	defer srv.Close()

	disk := filepath.Join(t.TempDir(), "disk.img")
	rec := &recorder{}
	var lastBytes, lastTotal int64
	n, err := StreamWrite(context.Background(), rec.runner(), StreamOpts{
		URL:      srv.URL,
		Sha256:   sum256(payload),
		Disk:     disk,
		Client:   srv.Client(),
		Progress: func(b, total int64) { lastBytes, lastTotal = b, total },
	})
	if err != nil {
		t.Fatalf("StreamWrite: %v", err)
	}
	if n != int64(len(payload)) {
		t.Errorf("decompressed = %d, want %d", n, len(payload))
	}
	if lastBytes != int64(len(payload)) || lastTotal != int64(len(payload)) {
		t.Errorf("progress = (%d, %d), want (%d, %d)", lastBytes, lastTotal, len(payload), len(payload))
	}
	got, err := os.ReadFile(disk)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("disk content differs from decompressed stream")
	}
	assertCall(t, rec, 0, "xz", "-dc")
	if i := rec.findCall("wipefs"); i != -1 {
		t.Errorf("wipefs must not run on success: %v", rec.calls[i].argv())
	}
}

func TestStreamWriteHashMismatchWipesTarget(t *testing.T) {
	noSleep(t)
	payload := []byte("not-the-expected-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	// A 5 MiB pre-filled "disk": big enough that the first/last 2 MiB
	// wipe regions do not cover the middle.
	disk := filepath.Join(t.TempDir(), "disk.img")
	const diskSize = 5 << 20
	if err := os.WriteFile(disk, bytes.Repeat([]byte{0xAA}, diskSize), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := &recorder{}
	_, err := StreamWrite(context.Background(), rec.runner(), StreamOpts{
		URL:    srv.URL,
		Sha256: sum256([]byte("something else entirely")),
		Disk:   disk,
		Client: srv.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}

	// Hardened wipe (a): first and last 2 MiB zeroed, middle untouched.
	got, rerr := os.ReadFile(disk)
	if rerr != nil {
		t.Fatal(rerr)
	}
	zeros := make([]byte, wipeSpan)
	if !bytes.Equal(got[:wipeSpan], zeros) {
		t.Error("first 2 MiB (primary GPT region) not zeroed")
	}
	if !bytes.Equal(got[diskSize-wipeSpan:], zeros) {
		t.Error("last 2 MiB (backup GPT region) not zeroed")
	}
	// 2.5 MiB sits strictly between the zeroed head (first 2 MiB) and
	// the zeroed tail (last 2 MiB of a 5 MiB disk, i.e. from 3 MiB).
	if got[5<<19] != 0xAA {
		t.Error("middle of disk was modified; wipe must only zero the GPT regions")
	}

	// Hardened wipe (b): wipefs --all, then reread the partition table.
	i := rec.findCall("wipefs")
	if i == -1 {
		t.Fatalf("wipefs never ran; calls: %+v", rec.calls)
	}
	assertCall(t, rec, i, "wipefs", "--all", disk)
	assertCall(t, rec, i+1, "udevadm", "settle")
	assertCall(t, rec, i+2, "blockdev", "--rereadpt", disk)
}

func TestStreamWriteHTTPErrorWipesTarget(t *testing.T) {
	noSleep(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	disk := filepath.Join(t.TempDir(), "disk.img")
	if err := os.WriteFile(disk, bytes.Repeat([]byte{0xAA}, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	_, err := StreamWrite(context.Background(), rec.runner(), StreamOpts{
		URL:    srv.URL,
		Sha256: sum256([]byte("x")),
		Disk:   disk,
		Client: srv.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected status") {
		t.Fatalf("err = %v, want unexpected status", err)
	}
	if i := rec.findCall("wipefs"); i == -1 {
		t.Fatalf("wipefs must run on download failure; calls: %+v", rec.calls)
	}
}

func TestStreamWriteDecompressErrorWipesTarget(t *testing.T) {
	noSleep(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("garbage"))
	}))
	defer srv.Close()

	disk := filepath.Join(t.TempDir(), "disk.img")
	if err := os.WriteFile(disk, bytes.Repeat([]byte{0xAA}, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{respond: func(c call) ([]byte, error) {
		if c.name == "xz" {
			return nil, errors.New("xz: (stdin): File format not recognized")
		}
		return nil, nil
	}}
	_, err := StreamWrite(context.Background(), rec.runner(), StreamOpts{
		URL:    srv.URL,
		Sha256: sum256([]byte("garbage")),
		Disk:   disk,
		Client: srv.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "download/decompress/write failed") {
		t.Fatalf("err = %v, want decompress failure", err)
	}
	if i := rec.findCall("wipefs"); i == -1 {
		t.Fatalf("wipefs must run on decompress failure; calls: %+v", rec.calls)
	}
}

func TestWipeTargetSmallFile(t *testing.T) {
	// A target smaller than the wipe span is zeroed in full without
	// erroring (regular-file targets in tests; block devices are always
	// larger).
	noSleep(t)
	disk := filepath.Join(t.TempDir(), "tiny.img")
	if err := os.WriteFile(disk, []byte("tiny"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	if err := WipeTarget(context.Background(), rec.runner(), disk); err != nil {
		t.Fatalf("WipeTarget: %v", err)
	}
	got, err := os.ReadFile(disk)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, make([]byte, 4)) {
		t.Errorf("tiny target not fully zeroed: %q", got)
	}
}
