package bootcimg

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// probeTimeout bounds one whole registry probe: DNS, connect, TLS and the
// request share this single deadline.
const probeTimeout = 5 * time.Second

// ErrNoNetwork marks a failed probe on a machine with no routable address: no
// non-loopback interface is up with a global unicast address. A failed attempt
// plus that fact is what justifies telling the user the network is missing.
var ErrNoNetwork = errors.New("no routable network address is configured")

// Prober reports whether the registry serving ref answers at all, returning
// nil when it does. It is supplemental evidence for a failure skopeo or
// cosign already reported, never a gate: a nil Prober disables probing, so
// tests and callers that do not wire one never touch the network.
type Prober func(ctx context.Context, ref string) error

// RegistryUnreachableError reports that an image operation failed and the
// registry could not be reached either. It wraps the original failure, which
// stays authoritative; the probe only adds the diagnosis.
type RegistryUnreachableError struct {
	Host string
	// NoNetwork is set when the machine has no network configured, as
	// opposed to one registry failing to answer.
	NoNetwork bool
	Cause     error // the probe failure
	Err       error // the original skopeo/cosign failure
}

// Error claims the network is missing only when the local check shows that.
// One endpoint failing (a route, proxy, address family or the registry
// itself) cannot establish it, so otherwise the message names the endpoint
// and the probe's cause and only says the network may be involved.
func (e *RegistryUnreachableError) Error() string {
	if e.NoNetwork {
		return fmt.Sprintf("no routable network address is configured on this machine, so registry %s cannot be reached; installing from a network image requires a network connection: %v", e.Host, e.Err)
	}
	return fmt.Sprintf("cannot reach registry %s (%v); this could be caused by the network connection or by the registry, and installing from a network image requires access to it: %v",
		e.Host, e.Cause, e.Err)
}

func (e *RegistryUnreachableError) Unwrap() error { return e.Err }

// Markers in a failure's text showing a registry (or cosign) answered, so a
// failed probe must not relabel it. A mirror or plain-HTTP registry can fail
// the probe while the tool that really talks to the registry succeeded in
// reaching it.
var (
	registryResponseMarkers = []string{
		"manifest unknown", "name unknown", "unauthorized", "denied",
		"authentication required", "forbidden", "toomanyrequests", "too many requests",
	}
	// transportMarkers positively identify a connectivity failure in tool
	// output. Without one the failure is ambiguous and keeps its own code.
	transportMarkers = []string{
		"dial tcp", "no such host", "i/o timeout", "connection refused",
		"network is unreachable", "no route to host", "tls handshake timeout",
		"context deadline exceeded", "lookup ",
	}
	cosignVerdictMarkers = []string{
		"no matching signatures", "signature verification failed",
		"no signatures found", "invalid signature", "no valid signatures",
		"no matching attestations",
	}
)

func hasMarker(err error, markers []string) bool {
	msg := strings.ToLower(err.Error())
	for _, m := range markers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// diagnose upgrades orig to a RegistryUnreachableError when probe shows the
// registry for ref cannot be reached. It returns orig unchanged when probing
// is off, ref is not a registry reference, the registry answers, or the
// caller's context ended (cancellation is never a network verdict).
func diagnose(ctx context.Context, probe Prober, ref string, orig error) error {
	if probe == nil || ctx.Err() != nil || !IsRegistryRef(ref) {
		return orig
	}
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cause := probe(pctx, ref)
	if cause == nil || ctx.Err() != nil {
		return orig
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		cause = fmt.Errorf("timed out after %s", probeTimeout)
	}
	return &RegistryUnreachableError{
		Host:      registryHost(ref),
		NoNetwork: errors.Is(cause, ErrNoNetwork),
		Cause:     cause,
		Err:       orig,
	}
}

// registryHost returns the host[:port] skopeo would contact for ref: the
// first path component when it names a registry, else Docker Hub, whose
// docker.io aliases are served from registry-1.docker.io.
func registryHost(ref string) string {
	const hub = "registry-1.docker.io"
	first, _, hasPath := strings.Cut(bareImageRef(ref), "/")
	if !hasPath || (!strings.ContainsAny(first, ".:") && first != "localhost") {
		return hub
	}
	switch first {
	case "docker.io", "index.docker.io":
		return hub
	}
	return first
}

// HTTPRegistryProbe is the production Prober: an HTTPS request to the
// registry's /v2/ endpoint through the environment's proxy settings. Any HTTP
// response, including 401, means the registry is reachable. If the attempt
// fails on a machine with no routable address, the failure wraps ErrNoNetwork.
//
// Only positively identified connectivity failures are reported: DNS and dial
// failures, timeouts, and a proxy that could not reach the registry (502, 503,
// 504). A TLS trust, alert or protocol failure proves a server answered, and
// anything else (a proxy demanding credentials with 407, unknown errors) is
// ambiguous; all of those give no verdict, so the original skopeo/cosign
// failure keeps its own code. registries.conf mirrors are not consulted.
func HTTPRegistryProbe(ctx context.Context, ref string) error {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	defer tr.CloseIdleConnections()
	return probeWith(ctx, &http.Client{Transport: tr}, ref, func() bool {
		return networkConfigured(net.Interfaces, (*net.Interface).Addrs)
	})
}

// probeWith tries the registry first and consults configured only to word a
// failure, so the interface check can never block a route that works.
func probeWith(ctx context.Context, client *http.Client, ref string, configured func() bool) error {
	err := probeRegistry(ctx, client, ref)
	if err != nil && !configured() {
		return fmt.Errorf("%w: %w", ErrNoNetwork, err)
	}
	return err
}

// networkConfigured reports whether any non-loopback interface is up with a
// global unicast address. Failing to enumerate counts as configured: the
// check may only ever add a diagnosis, never invent one.
func networkConfigured(list func() ([]net.Interface, error), addrs func(*net.Interface) ([]net.Addr, error)) bool {
	ifaces, err := list()
	if err != nil {
		return true
	}
	for i := range ifaces {
		if ifaces[i].Flags&net.FlagUp == 0 || ifaces[i].Flags&net.FlagLoopback != 0 {
			continue
		}
		as, err := addrs(&ifaces[i])
		if err != nil {
			return true
		}
		for _, a := range as {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.IsGlobalUnicast() {
				return true
			}
		}
	}
	return false
}

func probeRegistry(ctx context.Context, client *http.Client, ref string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+registryHost(ref)+"/v2/", nil)
	if err != nil {
		return nil // not a transport failure: no verdict
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		if !connectivityFailure(err) {
			return nil
		}
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// connectivityFailure positively identifies errors that mean no route to a
// server. TLS-layer failures (trust, alerts, protocol) occur only after a
// server accepted the connection, so they are excluded explicitly.
func connectivityFailure(err error) bool {
	var (
		certVerify *tls.CertificateVerificationError
		record     tls.RecordHeaderError
		opErr      *net.OpError
		dnsErr     *net.DNSError
		netErr     net.Error
	)
	switch {
	case errors.As(err, &certVerify), errors.As(err, &record),
		strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		return false
	case errors.As(err, &dnsErr), errors.Is(err, context.DeadlineExceeded):
		return true
	case errors.As(err, &opErr) && (opErr.Op == "dial" || opErr.Op == "proxyconnect"):
		return true
	case errors.As(err, &netErr) && netErr.Timeout():
		return true
	}
	// A proxy that answered CONNECT with a gateway failure could not reach
	// the registry; other statuses (407, 403) are not connectivity.
	msg := err.Error()
	return strings.Contains(msg, "Bad Gateway") || strings.Contains(msg, "Service Unavailable") ||
		strings.Contains(msg, "Gateway Timeout")
}
