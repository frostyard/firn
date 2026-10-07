package bootcimg

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// probeTimeout bounds one whole registry probe: DNS, connect, TLS and the
// request share this single deadline.
const probeTimeout = 5 * time.Second

// Prober reports whether the registry serving ref answers at all, returning
// nil when it does. It is supplemental evidence for a failure skopeo or
// cosign already reported, never a gate: a nil Prober disables probing, so
// tests and callers that do not wire one never touch the network.
type Prober func(ctx context.Context, ref string) error

// RegistryUnreachableError reports that an image operation failed and the
// registry could not be reached either. It wraps the original failure, which
// stays authoritative; the probe only adds the diagnosis.
type RegistryUnreachableError struct {
	Host  string
	Cause error // the probe failure
	Err   error // the original skopeo/cosign failure
}

// Error deliberately does not claim the machine is offline: one endpoint
// failing (a route, proxy, address family or the registry itself) cannot
// establish that, so it names the endpoint and the probe's cause instead.
func (e *RegistryUnreachableError) Error() string {
	return fmt.Sprintf("cannot reach registry %s (%v); installing from a network image requires a working network connection to it: %v",
		e.Host, e.Cause, e.Err)
}

func (e *RegistryUnreachableError) Unwrap() error { return e.Err }

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
		Host:  registryHost(ref),
		Cause: cause,
		Err:   orig,
	}
}

// registryHost returns the host[:port] skopeo would contact for ref, with
// bare names mapping to Docker Hub.
func registryHost(ref string) string {
	ref = strings.TrimPrefix(ref, "docker://")
	first, _, hasPath := strings.Cut(ref, "/")
	if hasPath && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return first
	}
	return "registry-1.docker.io"
}

// HTTPRegistryProbe is the production Prober: an HTTPS request to the
// registry's /v2/ endpoint through the environment's proxy settings. Any HTTP
// response, including 401, means the registry is reachable.
//
// Only failures that mean "no route to a server" are reported. A TLS
// trust or protocol failure (a certificate Go does not trust but skopeo's
// configuration may, or a plain-HTTP registry) proves a server answered, so it
// counts as reachable and the original skopeo/cosign failure keeps its own
// code. registries.conf mirrors are not consulted: the probe asks only about
// the registry the reference names.
func HTTPRegistryProbe(ctx context.Context, ref string) error {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	defer tr.CloseIdleConnections()
	return probeRegistry(ctx, &http.Client{Transport: tr}, ref)
}

func probeRegistry(ctx context.Context, client *http.Client, ref string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+registryHost(ref)+"/v2/", nil)
	if err != nil {
		return err
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		if answeredButUntrusted(err) {
			return nil
		}
		return err
	}
	return resp.Body.Close()
}

// answeredButUntrusted reports TLS-layer failures that occur only after a
// server accepted the connection.
func answeredButUntrusted(err error) bool {
	var (
		certVerify *tls.CertificateVerificationError
		unknownCA  x509.UnknownAuthorityError
		invalid    x509.CertificateInvalidError
		hostname   x509.HostnameError
		record     tls.RecordHeaderError
	)
	return errors.As(err, &certVerify) || errors.As(err, &unknownCA) || errors.As(err, &invalid) ||
		errors.As(err, &hostname) || errors.As(err, &record) ||
		strings.Contains(err.Error(), "server gave HTTP response to HTTPS client")
}
