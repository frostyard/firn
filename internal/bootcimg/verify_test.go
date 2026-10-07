// Ported from frostyard/fisherman (GPL-3.0-only),
// fisherman/internal/install/verify_test.go. Firn-specific cases pin the
// embedded local image selected by CheckImage when the registry is offline.

package bootcimg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/firn/internal/runner"
)

const (
	remoteDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	localDigest  = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func verifyRunner(t *testing.T, remoteOut []byte, remoteErr error, localOut []byte, localErr error, verify func([]string) error, calls *[][]string) *runner.Runner {
	t.Helper()
	return runner.NewFake(
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			*calls = append(*calls, append([]string{name}, args...))
			switch name {
			case "skopeo":
				if strings.HasPrefix(args[1], "docker://") {
					return remoteOut, remoteErr
				}
				return localOut, localErr
			case "cosign":
				if verify != nil {
					return nil, verify(args)
				}
				return nil, nil
			default:
				t.Fatalf("unexpected command %s %q", name, args)
				return nil, nil
			}
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
}

func TestCheckAndPinImageVerifiesResolvedDigest(t *testing.T) {
	var calls [][]string
	r := verifyRunner(t,
		[]byte(`{"Digest":"`+remoteDigest+`"}`), nil,
		nil, errors.New("not cached"),
		func(args []string) error {
			want := []string{"verify", "--key", "/keys/cosign.pub", "ghcr.io/frostyard/snow@" + remoteDigest}
			if !slices.Equal(args, want) {
				t.Fatalf("cosign args = %q, want %q", args, want)
			}
			return nil
		}, &calls)

	got, err := CheckAndPinImage(context.Background(), r, "docker://ghcr.io/frostyard/snow:latest", "/keys/cosign.pub", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ghcr.io/frostyard/snow@" + remoteDigest; got.Ref != want {
		t.Fatalf("pinned source = %q, want %q", got.Ref, want)
	}
}

func TestCheckAndPinImageOfflineCachedImage(t *testing.T) {
	var calls [][]string
	r := verifyRunner(t,
		nil, errors.New("network unreachable"),
		[]byte(`{"Digest":"`+localDigest+`"}`), nil,
		nil, &calls)

	got, err := CheckAndPinImage(context.Background(), r, "ghcr.io/frostyard/snow:latest", "/keys/cosign.pub", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ghcr.io/frostyard/snow@" + localDigest; got.Ref != want {
		t.Fatalf("offline pinned source = %q, want cached %q", got.Ref, want)
	}
}

func TestCheckAndPinImageBindsVerificationToSelectedLocalDigest(t *testing.T) {
	var calls [][]string
	var verified string
	r := verifyRunner(t,
		[]byte(`{"Digest":"`+remoteDigest+`"}`), nil,
		[]byte(`{"Digest":"`+localDigest+`"}`), nil,
		func(args []string) error { verified = args[len(args)-1]; return nil }, &calls)

	got, err := CheckAndPinImage(context.Background(), r, "registry.example.com:5000/snow:latest", "/keys/cosign.pub", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "registry.example.com:5000/snow@" + localDigest
	if got.Ref != want || verified != want {
		t.Fatalf("source = %q, verified = %q, want selected digest %q", got.Ref, verified, want)
	}
}

func TestCheckAndPinImageVerificationFailures(t *testing.T) {
	badSig := errors.New("no matching signatures")
	wrongKey := errors.New("signature verification failed")
	// The transient shape from the 2026-08-26 GHCR incident: cosign fell
	// through to attestation referrers although the key signature existed.
	transient := errors.New("no matching attestations: expected key signature, not certificate")
	for _, tc := range []struct {
		name string
		key  string
		errs []error // cosign result per attempt; a nil entry succeeds
		err  error   // final error the caller sees; nil means success
	}{
		{name: "bad signature", key: "/keys/cosign.pub", errs: []error{badSig, badSig, badSig}, err: badSig},
		{name: "wrong key", key: "/keys/wrong.pub", errs: []error{wrongKey, wrongKey, wrongKey}, err: wrongKey},
		{name: "transient then success", key: "/keys/cosign.pub", errs: []error{transient, nil}},
		{name: "transient until last attempt", key: "/keys/cosign.pub", errs: []error{transient, transient, nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			attempt := 0
			r := verifyRunner(t,
				[]byte(`{"Digest":"`+remoteDigest+`"}`), nil,
				nil, errors.New("not cached"),
				func(args []string) error {
					if args[2] != tc.key {
						t.Fatalf("cosign key = %q, want %q", args[2], tc.key)
					}
					err := tc.errs[attempt]
					attempt++
					return err
				}, &calls)
			var slept []time.Duration
			r = r.WithSleep(func(d time.Duration) { slept = append(slept, d) })
			var warnings []string
			warn := func(msg string) { warnings = append(warnings, msg) }

			got, err := CheckAndPinImage(context.Background(), r, "ghcr.io/frostyard/snow:latest", tc.key, warn, nil)
			if attempt != len(tc.errs) {
				t.Fatalf("cosign attempts = %d, want %d", attempt, len(tc.errs))
			}
			if tc.err == nil {
				if err != nil {
					t.Fatalf("verification after transient failures = %v, want success", err)
				}
				if want := "ghcr.io/frostyard/snow@" + remoteDigest; got.Ref != want {
					t.Fatalf("pinned source = %q, want %q", got.Ref, want)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tc.err.Error()) {
					t.Fatalf("verification error = %v, want %v", err, tc.err)
				}
				if !strings.Contains(err.Error(), "bootcimg: verifying image signature for ghcr.io/frostyard/snow@"+remoteDigest) {
					t.Fatalf("verification error lost its shape: %v", err)
				}
			}
			// Backoff runs before every attempt but the first, never after
			// the last; every failed non-final attempt surfaces a warning
			// carrying that attempt's error text.
			if want := verifyBackoff[:len(tc.errs)-1]; !slices.Equal(slept, want) {
				t.Fatalf("backoff sleeps = %v, want %v", slept, want)
			}
			if len(warnings) != len(tc.errs)-1 {
				t.Fatalf("retry warnings = %q, want %d of them", warnings, len(tc.errs)-1)
			}
			for i, w := range warnings {
				if !strings.Contains(w, tc.errs[i].Error()) {
					t.Fatalf("warning %d = %q, missing attempt error %q", i+1, w, tc.errs[i])
				}
			}
		})
	}
}

func TestCheckAndPinImageRejectsMalformedDigest(t *testing.T) {
	var calls [][]string
	r := verifyRunner(t, []byte(`{"Digest":"sha256:not-a-digest"}`), nil, nil, errors.New("not cached"), nil, &calls)
	_, err := CheckAndPinImage(context.Background(), r, "ghcr.io/frostyard/snow:latest", "/keys/cosign.pub", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no valid sha256 digest") {
		t.Fatalf("malformed digest error = %v", err)
	}
	for _, call := range calls {
		if call[0] == "cosign" {
			t.Fatalf("cosign ran for malformed digest: %v", calls)
		}
	}
}

func TestCheckAndPinImageDoesNotReplaceSelectedLocalImage(t *testing.T) {
	var calls [][]string
	r := verifyRunner(t,
		[]byte(`{"Digest":"`+remoteDigest+`"}`), nil,
		[]byte(`{"Digest":"not-a-digest"}`), nil,
		nil, &calls)
	_, err := CheckAndPinImage(context.Background(), r, "ghcr.io/frostyard/snow:latest", "/keys/cosign.pub", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "selected local image") {
		t.Fatalf("selected local digest error = %v", err)
	}
	for _, call := range calls {
		if call[0] == "cosign" {
			t.Fatalf("remote digest replaced an unverifiable selected local image: %v", calls)
		}
	}
}

func TestCheckAndPinImageRejectsLocalTransportWithVerification(t *testing.T) {
	var calls [][]string
	r := verifyRunner(t, nil, fmt.Errorf("offline"), []byte(`{"Digest":"`+localDigest+`"}`), nil, nil, &calls)
	_, err := CheckAndPinImage(context.Background(), r, "containers-storage:ghcr.io/frostyard/snow:latest", "/keys/cosign.pub", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "requires a registry image reference") {
		t.Fatalf("local transport error = %v", err)
	}
}

// TestCheckAndPinImageLabelsFollowSelectedImage pins ADR-0018's rule that
// the core Flatpak label is read from the image preflight selects, never
// from the other inspection.
func TestCheckAndPinImageLabelsFollowSelectedImage(t *testing.T) {
	inspect := func(digest, set string) []byte {
		return []byte(`{"Digest":"` + digest + `","Labels":{"containers.bootc":"1","set":"` + set + `"}}`)
	}
	notCached := errors.New("not cached")
	offline := errors.New("network unreachable")
	for _, tc := range []struct {
		name      string
		image     string
		key       string
		remoteOut []byte
		remoteErr error
		localOut  []byte
		localErr  error
		wantRef   string
		wantSet   string // "" means no labels
		unknown   bool   // labels could not be read (Inspected false)
	}{
		{name: "unsigned, local copy wins over a newer registry image",
			image:     "ghcr.io/frostyard/snow:latest",
			remoteOut: inspect(remoteDigest, "remote"), localOut: inspect(localDigest, "local"),
			wantRef: "ghcr.io/frostyard/snow:latest", wantSet: "local"},
		{name: "unsigned, registry when nothing is cached",
			image:     "ghcr.io/frostyard/snow:latest",
			remoteOut: inspect(remoteDigest, "remote"), localErr: notCached,
			wantRef: "ghcr.io/frostyard/snow:latest", wantSet: "remote"},
		{name: "signed tag, local copy wins",
			image: "ghcr.io/frostyard/snow:latest", key: "/keys/cosign.pub",
			remoteOut: inspect(remoteDigest, "remote"), localOut: inspect(localDigest, "local"),
			wantRef: "ghcr.io/frostyard/snow@" + localDigest, wantSet: "local"},
		{name: "signed tag, registry when nothing is cached",
			image: "ghcr.io/frostyard/snow:latest", key: "/keys/cosign.pub",
			remoteOut: inspect(remoteDigest, "remote"), localErr: notCached,
			wantRef: "ghcr.io/frostyard/snow@" + remoteDigest, wantSet: "remote"},
		{name: "signed digest, local copy of that digest",
			image: "ghcr.io/frostyard/snow@" + localDigest, key: "/keys/cosign.pub",
			remoteOut: inspect(localDigest, "remote"), localOut: inspect(localDigest, "local"),
			wantRef: "ghcr.io/frostyard/snow@" + localDigest, wantSet: "local"},
		{name: "signed digest, local copy of another digest is ignored",
			image: "ghcr.io/frostyard/snow@" + remoteDigest, key: "/keys/cosign.pub",
			remoteOut: inspect(remoteDigest, "remote"), localOut: inspect(localDigest, "local"),
			wantRef: "ghcr.io/frostyard/snow@" + remoteDigest, wantSet: "remote"},
		{name: "signed digest, offline with no matching local copy: labels unknown",
			image: "ghcr.io/frostyard/snow@" + remoteDigest, key: "/keys/cosign.pub",
			remoteErr: offline, localErr: notCached,
			wantRef: "ghcr.io/frostyard/snow@" + remoteDigest, unknown: true},
		{name: "unsigned, unreadable registry inspection: labels unknown",
			image:     "ghcr.io/frostyard/snow:latest",
			remoteOut: []byte("not json"), localErr: notCached,
			wantRef: "ghcr.io/frostyard/snow:latest", unknown: true},
		{name: "inspection without labels",
			image: "ghcr.io/frostyard/snow:latest", key: "/keys/cosign.pub",
			remoteOut: []byte(`{"Digest":"` + remoteDigest + `"}`), localErr: notCached,
			wantRef: "ghcr.io/frostyard/snow@" + remoteDigest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			r := verifyRunner(t, tc.remoteOut, tc.remoteErr, tc.localOut, tc.localErr, nil, &calls)
			got, err := CheckAndPinImage(context.Background(), r, tc.image, tc.key, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.Ref != tc.wantRef {
				t.Fatalf("source ref = %q, want %q", got.Ref, tc.wantRef)
			}
			if gotSet := got.Labels["set"]; gotSet != tc.wantSet {
				t.Fatalf("labels came from %q, want %q (labels %v)", gotSet, tc.wantSet, got.Labels)
			}
			if got.Inspected == tc.unknown {
				t.Fatalf("Inspected = %v, want %v", got.Inspected, !tc.unknown)
			}
		})
	}
}

// A registry that hangs until the caller's deadline must not starve the
// inspection of a local copy, which is the one selected.
func TestCheckAndPinImageLocalInspectSurvivesHangingRegistry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r := runner.NewFake(
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if strings.HasPrefix(args[1], "docker://") {
				<-ctx.Done() // the registry hangs until the deadline
				return nil, ctx.Err()
			}
			if ctx.Err() != nil {
				return nil, ctx.Err() // exec.CommandContext would refuse to start
			}
			return []byte(`{"Digest":"` + localDigest + `","Labels":{"set":"local"}}`), nil
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
	got, err := CheckAndPinImage(ctx, r, "ghcr.io/frostyard/snow:latest", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Inspected || got.Labels["set"] != "local" {
		t.Fatalf("source = %+v, want the local copy's labels", got)
	}
}
