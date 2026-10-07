package bootcimg

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
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
		"docker.io/library/fedora":                 "registry-1.docker.io",
		"index.docker.io/library/fedora":           "registry-1.docker.io",
		"registry:ghcr.io/frostyard/snow:latest":   "ghcr.io",
		"docker://localhost:5000/snow":             "localhost:5000",
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

			_, err := CheckAndPinImage(context.Background(), r, "ghcr.io/frostyard/snow:latest", "", nil, probe)
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
	r := verifyRunner(t, nil, errors.New("i/o timeout"), nil, errors.New("not cached"), nil, &calls)
	probe, refs := failingProbe(nil)

	_, err := CheckAndPinImage(context.Background(), r, "ghcr.io/frostyard/snow:latest", "", nil, probe)
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
	if _, err := CheckAndPinImage(context.Background(), r, "ghcr.io/frostyard/snow:latest", "", nil, probe); err != nil {
		t.Fatal(err)
	}
	// A local transport is never probed.
	r = verifyRunner(t, nil, errors.New("offline"), nil, errors.New("not cached"), nil, &calls)
	_, err := CheckAndPinImage(context.Background(), r, "containers-storage:ghcr.io/x/y:1", "", nil, probe)
	if err == nil {
		t.Fatal("want the original failure")
	}
	// A nil probe leaves the error untouched.
	_, err = CheckAndPinImage(context.Background(), r, "ghcr.io/x/y:1", "", nil, nil)
	var unreachable *RegistryUnreachableError
	if errors.As(err, &unreachable) || len(*refs) != 0 {
		t.Fatalf("probed %d times, err = %v", len(*refs), err)
	}
}

func TestSignedFailuresDistinguishNetworkFromSignature(t *testing.T) {
	sig := errors.New("no matching signatures")
	transport := errors.New("Get https://ghcr.io/v2/: dial tcp: i/o timeout")
	for _, tc := range []struct {
		name      string
		image     string
		cosignErr error
		remoteErr error // skopeo's remote inspect; nil means it reached the registry
		probe     error
		want      bool // network diagnosis expected
	}{
		{"tag transport failure", "ghcr.io/frostyard/snow:latest", transport, errors.New("offline"), errors.New("down"), true},
		{"digest transport failure", "ghcr.io/frostyard/snow@" + remoteDigest, transport, errors.New("offline"), errors.New("down"), true},
		{"transport failure, registry answers probe", "ghcr.io/frostyard/snow:latest", transport, errors.New("offline"), nil, false},
		{"cosign verdict is never a network error", "ghcr.io/frostyard/snow:latest", sig, errors.New("offline"), errors.New("down"), false},
		{"digest cosign verdict", "ghcr.io/frostyard/snow@" + remoteDigest, sig, errors.New("offline"), errors.New("down"), false},
		{"skopeo reached the registry", "ghcr.io/frostyard/snow:latest", transport, nil, errors.New("down"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][]string
			remote := []byte(nil)
			if tc.remoteErr == nil {
				remote = []byte(`{"Digest":"` + remoteDigest + `"}`)
			}
			// Cached locally, so only cosign needs the registry.
			r := verifyRunner(t, remote, tc.remoteErr, []byte(`{"Digest":"`+remoteDigest+`"}`), nil,
				func([]string) error { return tc.cosignErr }, &calls)
			r = r.WithSleep(func(time.Duration) {})
			probe, _ := failingProbe(tc.probe)

			_, err := CheckAndPinImage(context.Background(), r, tc.image, "/keys/cosign.pub", nil, probe)
			var unreachable *RegistryUnreachableError
			if got := errors.As(err, &unreachable); got != tc.want {
				t.Fatalf("diagnosed = %v, want %v (err %v)", got, tc.want, err)
			}
			if !errors.Is(err, tc.cosignErr) {
				t.Fatalf("cosign failure lost: %v", err)
			}
		})
	}
}

func TestRegistryLevelSkopeoErrorsAreNotDiagnosed(t *testing.T) {
	for _, msg := range []string{
		"manifest unknown", "unauthorized: authentication required",
		"requested access to the resource is denied", "toomanyrequests: rate limit",
	} {
		var calls [][]string
		r := verifyRunner(t, nil, errors.New(msg), nil, errors.New("not cached"), nil, &calls)
		probe, refs := failingProbe(errors.New("down"))

		_, err := CheckAndPinImage(context.Background(), r, "ghcr.io/frostyard/snow:latest", "", nil, probe)
		var unreachable *RegistryUnreachableError
		if err == nil || errors.As(err, &unreachable) || len(*refs) != 0 {
			t.Fatalf("%q: err = %v, probes = %d; want the plain error, unprobed", msg, err, len(*refs))
		}
	}
}

func TestNoNetworkIsDiagnosedAsSuch(t *testing.T) {
	var calls [][]string
	r := verifyRunner(t, nil, errors.New("dial tcp: network is unreachable"), nil, errors.New("not cached"), nil, &calls)
	probe, _ := failingProbe(fmt.Errorf("probe: %w", ErrNoNetwork))

	_, err := CheckAndPinImage(context.Background(), r, "ghcr.io/frostyard/snow:latest", "", nil, probe)
	var unreachable *RegistryUnreachableError
	if !errors.As(err, &unreachable) || !unreachable.NoNetwork {
		t.Fatalf("err = %v, want NoNetwork diagnosis", err)
	}
	if !strings.Contains(err.Error(), "no routable network address is configured") {
		t.Fatalf("message = %q", err)
	}
}

func TestProbeWithWordsFailuresOnlyAfterTrying(t *testing.T) {
	down := errors.New("dial tcp: connection refused")
	for _, tc := range []struct {
		name       string
		probeErr   error
		configured bool
		wantNoNet  bool
		wantErr    bool
	}{
		{"works with no routable address (loopback registry)", nil, false, false, false},
		{"fails with no routable address", down, false, true, true},
		{"fails with a configured network", down, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// probeWith drives a real client; point it at a server or a dead port.
			var ref string
			if tc.probeErr == nil {
				srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
				defer srv.Close()
				ref = strings.TrimPrefix(srv.URL, "https://") + "/x/y:1"
				tc2 := srv.Client()
				err := probeWith(context.Background(), tc2, ref, func() bool { return tc.configured })
				if err != nil {
					t.Fatalf("probe = %v, want success", err)
				}
				return
			}
			ln, _ := net.Listen("tcp", "127.0.0.1:0")
			ref = ln.Addr().String() + "/x/y:1"
			ln.Close()
			err := probeWith(context.Background(), &http.Client{}, ref, func() bool { return tc.configured })
			if (err != nil) != tc.wantErr || errors.Is(err, ErrNoNetwork) != tc.wantNoNet {
				t.Fatalf("err = %v, want err=%v noNetwork=%v", err, tc.wantErr, tc.wantNoNet)
			}
		})
	}
}

func TestNetworkConfigured(t *testing.T) {
	up := net.FlagUp | net.FlagBroadcast
	list := func(ifs ...net.Interface) func() ([]net.Interface, error) {
		return func() ([]net.Interface, error) { return ifs, nil }
	}
	addr := func(cidr string) []net.Addr {
		ip, n, _ := net.ParseCIDR(cidr)
		n.IP = ip
		return []net.Addr{n}
	}
	byName := map[string][]net.Addr{
		"lo":   addr("127.0.0.1/8"),
		"eth0": addr("192.168.1.5/24"),
		"ll0":  addr("fe80::1/64"),
		"dn0":  addr("10.0.0.2/24"),
	}
	addrs := func(i *net.Interface) ([]net.Addr, error) { return byName[i.Name], nil }
	loop := net.Interface{Name: "lo", Flags: up | net.FlagLoopback}
	for _, tc := range []struct {
		name string
		list func() ([]net.Interface, error)
		want bool
	}{
		{"only loopback", list(loop), false},
		{"address on a down interface", list(loop, net.Interface{Name: "eth0"}), false},
		{"configured interface", list(loop, net.Interface{Name: "eth0", Flags: up}), true},
		{"private address counts", list(net.Interface{Name: "dn0", Flags: up}), true},
		{"enumeration failure fails open", func() ([]net.Interface, error) { return nil, errors.New("boom") }, true},
	} {
		if got := networkConfigured(tc.list, addrs); got != tc.want {
			t.Errorf("%s: networkConfigured = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestBareImageRefKeepsHostPort(t *testing.T) {
	for in, want := range map[string]string{
		"localhost:5000/snow":              "localhost:5000/snow",
		"registry:ghcr.io/frostyard/snow":  "ghcr.io/frostyard/snow",
		"containers-storage:ghcr.io/x/y:1": "ghcr.io/x/y:1",
		"docker://ghcr.io/x/y:1":           "ghcr.io/x/y:1",
		"ghcr.io/x/y:1":                    "ghcr.io/x/y:1",
	} {
		if got := bareImageRef(in); got != want {
			t.Errorf("bareImageRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCancellationIsNotANetworkVerdict(t *testing.T) {
	var calls [][]string
	r := verifyRunner(t, nil, errors.New("canceled"), nil, errors.New("not cached"), nil, &calls)
	ctx, cancel := context.WithCancel(context.Background())
	probe := func(context.Context, string) error { cancel(); return context.Canceled }

	_, err := CheckAndPinImage(ctx, r, "ghcr.io/frostyard/snow:latest", "", nil, probe)
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

	t.Run("TLS alert from the server counts as answered", func(t *testing.T) {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		srv.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
		srv.Config.ErrorLog = log.New(io.Discard, "", 0)
		srv.StartTLS()
		defer srv.Close()
		if err := probeRegistry(context.Background(), srv.Client(), hostRef(srv)); err != nil {
			t.Fatalf("probe = %v, want reachable despite the server's TLS alert", err)
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

	t.Run("proxy demanding credentials is not a reachability verdict", func(t *testing.T) {
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusProxyAuthRequired)
		}))
		defer proxy.Close()
		proxyURL, _ := url.Parse(proxy.URL)
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

		if err := probeRegistry(context.Background(), client, "registry.invalid:5000/x/y:1"); err != nil {
			t.Fatalf("probe = %v, want no verdict for a 407", err)
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
