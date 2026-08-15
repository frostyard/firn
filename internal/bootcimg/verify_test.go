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

	got, err := CheckAndPinImage(context.Background(), r, "docker://ghcr.io/frostyard/snow:latest", "/keys/cosign.pub")
	if err != nil {
		t.Fatal(err)
	}
	if want := "ghcr.io/frostyard/snow@" + remoteDigest; got != want {
		t.Fatalf("pinned source = %q, want %q", got, want)
	}
}

func TestCheckAndPinImageOfflineCachedImage(t *testing.T) {
	var calls [][]string
	r := verifyRunner(t,
		nil, errors.New("network unreachable"),
		[]byte(`{"Digest":"`+localDigest+`"}`), nil,
		nil, &calls)

	got, err := CheckAndPinImage(context.Background(), r, "ghcr.io/frostyard/snow:latest", "/keys/cosign.pub")
	if err != nil {
		t.Fatal(err)
	}
	if want := "ghcr.io/frostyard/snow@" + localDigest; got != want {
		t.Fatalf("offline pinned source = %q, want cached %q", got, want)
	}
}

func TestCheckAndPinImageBindsVerificationToSelectedLocalDigest(t *testing.T) {
	var calls [][]string
	var verified string
	r := verifyRunner(t,
		[]byte(`{"Digest":"`+remoteDigest+`"}`), nil,
		[]byte(`{"Digest":"`+localDigest+`"}`), nil,
		func(args []string) error { verified = args[len(args)-1]; return nil }, &calls)

	got, err := CheckAndPinImage(context.Background(), r, "registry.example.com:5000/snow:latest", "/keys/cosign.pub")
	if err != nil {
		t.Fatal(err)
	}
	want := "registry.example.com:5000/snow@" + localDigest
	if got != want || verified != want {
		t.Fatalf("source = %q, verified = %q, want selected digest %q", got, verified, want)
	}
}

func TestCheckAndPinImageVerificationFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		err  error
	}{
		{name: "bad signature", key: "/keys/cosign.pub", err: errors.New("no matching signatures")},
		{name: "wrong key", key: "/keys/wrong.pub", err: errors.New("signature verification failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			r := verifyRunner(t,
				[]byte(`{"Digest":"`+remoteDigest+`"}`), nil,
				nil, errors.New("not cached"),
				func(args []string) error {
					if args[2] != tc.key {
						t.Fatalf("cosign key = %q, want %q", args[2], tc.key)
					}
					return tc.err
				}, &calls)

			_, err := CheckAndPinImage(context.Background(), r, "ghcr.io/frostyard/snow:latest", tc.key)
			if err == nil || !strings.Contains(err.Error(), tc.err.Error()) {
				t.Fatalf("verification error = %v, want %v", err, tc.err)
			}
		})
	}
}

func TestCheckAndPinImageRejectsMalformedDigest(t *testing.T) {
	var calls [][]string
	r := verifyRunner(t, []byte(`{"Digest":"sha256:not-a-digest"}`), nil, nil, errors.New("not cached"), nil, &calls)
	_, err := CheckAndPinImage(context.Background(), r, "ghcr.io/frostyard/snow:latest", "/keys/cosign.pub")
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
	_, err := CheckAndPinImage(context.Background(), r, "ghcr.io/frostyard/snow:latest", "/keys/cosign.pub")
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
	_, err := CheckAndPinImage(context.Background(), r, "containers-storage:ghcr.io/frostyard/snow:latest", "/keys/cosign.pub")
	if err == nil || !strings.Contains(err.Error(), "requires a registry image reference") {
		t.Fatalf("local transport error = %v", err)
	}
}
