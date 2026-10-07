package bootcimg

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

func failingProbe(cause error) (Prober, *[]string) {
	var refs []string
	return func(_ context.Context, ref string) error {
		refs = append(refs, ref)
		return cause
	}, &refs
}

func TestRegistryHost(t *testing.T) {
	for ref, want := range map[string]string{
		"ghcr.io/frostyard/snow:latest":            "ghcr.io",
		"docker://ghcr.io/frostyard/snow@sha256:a": "ghcr.io",
		"registry.example.com:5000/snow:latest":    "registry.example.com:5000",
		"localhost/snow":                           "localhost",
		"localhost:5000/snow":                      "localhost:5000",
		"library/fedora:41":                        "registry-1.docker.io",
		"fedora":                                   "registry-1.docker.io",
	} {
		if got := registryHost(ref); got != want {
			t.Errorf("registryHost(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestUnsignedUnreachableRegistryIsDiagnosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cause    error
		wantText string
	}{
		{"no route", fmt.Errorf("dial tcp: %w", syscall.ENETUNREACH), "cannot reach registry ghcr.io (dial tcp: network is unreachable)"},
		{"refused", errors.New("connection refused"), "cannot reach registry ghcr.io (connection refused)"},
		{"timeout", context.DeadlineExceeded, "timed out after 5s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			skopeoErr := errors.New("skopeo: dial tcp: i/o timeout")
			r := verifyRunner(t, nil, skopeoErr, nil, errors.New("not cached"), nil, &calls)
			probe, refs := failingProbe(tc.cause)

			_, err := CheckAndPinImageProbed(context.Background(), r, "ghcr.io/frostyard/snow:latest", "", nil, probe)
			var unreachable *RegistryUnreachableError
			if !errors.As(err, &unreachable) {
				t.Fatalf("error = %v, want *RegistryUnreachableError", err)
			}
			if unreachable.Host != "ghcr.io" || strings.Contains(err.Error(), "offline") {
				t.Fatalf("diagnosis = %+v (%v)", unreachable, err)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("error %q missing %q", err, tc.wantText)
			}
			if !errors.Is(err, skopeoErr) || !strings.Contains(err.Error(), "not present in local containers-storage") {
				t.Fatalf("original failure lost: %v", err)
			}
			if len(*refs) != 1 {
				t.Fatalf("probed %d times, want 1", len(*refs))
			}
		})
	}
}

func TestReachableRegistryKeepsOriginalError(t *testing.T) {
	var calls [][]string
	r := verifyRunner(t, nil, errors.New("manifest unknown"), nil, errors.New("not cached"), nil, &calls)
	probe, refs := failingProbe(nil)

	_, err := CheckAndPinImageProbed(context.Background(), r, "ghcr.io/frostyard/snow:latest", "", nil, probe)
	var unreachable *RegistryUnreachableError
	if err == nil || errors.As(err, &unreachable) || len(*refs) != 1 {
		t.Fatalf("err = %v, probes = %d; want the plain original error", err, len(*refs))
	}
}

func TestProbeSkippedWhenNotNeeded(t *testing.T) {
	probe, refs := failingProbe(errors.New("down"))

	// A cached unsigned image succeeds without probing.
	var calls [][]string
	r := verifyRunner(t, nil, errors.New("offline"), []byte(`{"Digest":"`+localDigest+`"}`), nil, nil, &calls)
	if _, err := CheckAndPinImageProbed(context.Background(), r, "ghcr.io/frostyard/snow:latest", "", nil, probe); err != nil {
		t.Fatal(err)
	}
	// A local transport is never probed.
	r = verifyRunner(t, nil, errors.New("offline"), nil, errors.New("not cached"), nil, &calls)
	_, err := CheckAndPinImageProbed(context.Background(), r, "containers-storage:ghcr.io/x/y:1", "", nil, probe)
	if err == nil {
		t.Fatal("want the original failure")
	}
	// A nil probe leaves the error untouched.
	_, err = CheckAndPinImageProbed(context.Background(), r, "ghcr.io/x/y:1", "", nil, nil)
	var unreachable *RegistryUnreachableError
	if errors.As(err, &unreachable) || len(*refs) != 0 {
		t.Fatalf("probed %d times, err = %v", len(*refs), err)
	}
}

func TestSignedFailuresDistinguishNetworkFromSignature(t *testing.T) {
	sig := errors.New("no matching signatures")
	for _, tc := range []struct {
		name  string
		image string
		probe error
		want  bool // network diagnosis expected
	}{
		{"tag unreachable", "ghcr.io/frostyard/snow:latest", errors.New("down"), true},
		{"tag reachable", "ghcr.io/frostyard/snow:latest", nil, false},
		{"digest unreachable", "ghcr.io/frostyard/snow@" + remoteDigest, errors.New("down"), true},
		{"digest reachable", "ghcr.io/frostyard/snow@" + remoteDigest, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			// Cached locally, so only cosign needs the registry.
			r := verifyRunner(t, nil, errors.New("offline"), []byte(`{"Digest":"`+remoteDigest+`"}`), nil,
				func([]string) error { return sig }, &calls)
			r = r.WithSleep(func(time.Duration) {})
			probe, _ := failingProbe(tc.probe)

			_, err := CheckAndPinImageProbed(context.Background(), r, tc.image, "/keys/cosign.pub", nil, probe)
			var unreachable *RegistryUnreachableError
			if got := errors.As(err, &unreachable); got != tc.want {
				t.Fatalf("diagnosed = %v, want %v (err %v)", got, tc.want, err)
			}
			if !errors.Is(err, sig) {
				t.Fatalf("cosign failure lost: %v", err)
			}
		})
	}
}

func TestCancellationIsNotANetworkVerdict(t *testing.T) {
	var calls [][]string
	r := verifyRunner(t, nil, errors.New("canceled"), nil, errors.New("not cached"), nil, &calls)
	ctx, cancel := context.WithCancel(context.Background())
	probe := func(context.Context, string) error { cancel(); return context.Canceled }

	_, err := CheckAndPinImageProbed(ctx, r, "ghcr.io/frostyard/snow:latest", "", nil, probe)
	var unreachable *RegistryUnreachableError
	if err == nil || errors.As(err, &unreachable) {
		t.Fatalf("err = %v, want the original error undiagnosed", err)
	}
}

func TestProbeRunsUnderBoundedContext(t *testing.T) {
	var deadline time.Time
	probe := func(ctx context.Context, _ string) error {
		deadline, _ = ctx.Deadline()
		return errors.New("down")
	}
	_ = diagnose(context.Background(), probe, "ghcr.io/x/y:1", errors.New("orig"))
	if deadline.IsZero() || time.Until(deadline) > probeTimeout {
		t.Fatalf("probe deadline = %v, want within %s", deadline, probeTimeout)
	}
}

func TestProbeRegistry(t *testing.T) {
	tlsServer := func(status int) *httptest.Server {
		return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v2/" {
				t.Errorf("probe requested %q, want /v2/", r.URL.Path)
			}
			w.WriteHeader(status)
		}))
	}
	hostRef := func(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "https://") + "/x/y:1" }

	t.Run("401 means reachable", func(t *testing.T) {
		srv := tlsServer(http.StatusUnauthorized)
		defer srv.Close()
		if err := probeRegistry(context.Background(), srv.Client(), hostRef(srv)); err != nil {
			t.Fatalf("probe = %v, want reachable", err)
		}
	})

	t.Run("untrusted certificate still means a server answered", func(t *testing.T) {
		srv := tlsServer(http.StatusOK)
		defer srv.Close()
		if err := probeRegistry(context.Background(), &http.Client{}, hostRef(srv)); err != nil {
			t.Fatalf("probe = %v, want reachable despite certificate failure", err)
		}
	})

	t.Run("plain HTTP server counts as answered", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer srv.Close()
		ref := strings.TrimPrefix(srv.URL, "http://") + "/x/y:1"
		if err := probeRegistry(context.Background(), &http.Client{}, ref); err != nil {
			t.Fatalf("probe = %v, want reachable", err)
		}
	})

	t.Run("routes through the configured proxy", func(t *testing.T) {
		var connectTarget string
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			connectTarget = r.Method + " " + r.Host
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer proxy.Close()
		proxyURL, _ := url.Parse(proxy.URL)
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

		err := probeRegistry(context.Background(), client, "registry.invalid:5000/x/y:1")
		if err == nil {
			t.Fatal("probe through a failing proxy = nil, want error")
		}
		if connectTarget != "CONNECT registry.invalid:5000" {
			t.Fatalf("proxy saw %q, want CONNECT registry.invalid:5000", connectTarget)
		}
	})

	t.Run("closed port is unreachable", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		ln.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := probeRegistry(ctx, &http.Client{}, addr+"/x/y:1"); err == nil {
			t.Fatal("probe of a closed port = nil, want error")
		}
	})
}
