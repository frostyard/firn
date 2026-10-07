// Ported from frostyard/fisherman (GPL-3.0-only),
// fisherman/internal/install/verify.go. Firn preserves its existing
// embedded-image preference: when a local containers-storage image is
// available, that digest is selected before the remote digest.

package bootcimg

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/frostyard/firn/internal/runner"
)

var sha256DigestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// cosign retry budget. On 2026-08-26 a hardware install failed
// preflight with "no matching attestations: expected key signature,
// not certificate" against a digest whose signature had been in the
// registry for 5+ hours; a transient GHCR response (rate limiting or
// referrers inconsistency) made cosign fall through to attestation
// referrers, and the identical cosign invocation verified cleanly
// shortly after. Bounded retries absorb that; the retry never weakens
// verification, and the final attempt's failure fails the install.
const verifyAttempts = 3

var verifyBackoff = []time.Duration{5 * time.Second, 15 * time.Second}

type inspectManifest struct {
	Digest string            `json:"Digest"`
	Labels map[string]string `json:"Labels"`
}

// Source is the image preflight selected: the reference bootc installs and
// the config labels of that same image (firn ADR-0018 reads the core
// Flatpak set from them).
type Source struct {
	Ref    string
	Labels map[string]string
	// Inspected reports that Labels came from a successful inspection of
	// the selected image. When false the labels are unknown, not absent:
	// a signed digest can verify while its registry inspection failed.
	Inspected bool
}

// CheckAndPinImage checks that image is reachable or cached. When keyPath is
// set, it also resolves the source Firn will actually install to an immutable
// digest, verifies that digest with cosign, and returns the pinned reference.
// Resolving before verification closes the tag-movement race. Labels come
// from the inspection of the image selected: the local copy when it wins,
// otherwise the registry image. warn, when non-nil, receives one message per
// failed non-final cosign attempt (the attempt's stderr is embedded by the
// runner error).
func CheckAndPinImage(ctx context.Context, r *runner.Runner, image, keyPath string, warn func(string)) (Source, error) {
	if keyPath != "" && !IsRegistryRef(image) {
		return Source{}, fmt.Errorf("bootcimg: cosign verification requires a registry image reference, got %q", image)
	}
	bare := bareImageRef(image)
	// Local first: it needs no network, so a registry that hangs until the
	// caller's deadline cannot starve the inspection of an image that is
	// already here (and would be selected).
	localOut, localErr := r.Run(ctx, "skopeo", "inspect", "containers-storage:"+bare)
	remoteOut, remoteErr := r.Run(ctx, "skopeo", "inspect", "docker://"+bare)
	var local, remote inspectManifest
	localOK := localErr == nil && json.Unmarshal(localOut, &local) == nil
	remoteOK := remoteErr == nil && json.Unmarshal(remoteOut, &remote) == nil

	if keyPath == "" {
		// Unsigned sources keep the tag, so bootc resolves it again when it
		// pulls; a tag that moves meanwhile can deploy a newer build than
		// these labels describe (ADR-0018, firn#109).
		if localOK {
			return Source{Ref: image, Labels: local.Labels, Inspected: true}, nil
		}
		if remoteErr == nil {
			return Source{Ref: image, Labels: remote.Labels, Inspected: remoteOK}, nil
		}
		return Source{}, fmt.Errorf("bootcimg: image %q is not reachable in its registry and not present in local containers-storage: %w", image, remoteErr)
	}

	var labels map[string]string
	inspected := false
	digest, pinned := digestReference(bare)
	if !pinned {
		// Match CheckImage's embedded-image rule: a valid local manifest wins
		// even if the registry advertises a newer tag. The selected digest is
		// still verified against the registry signature before installation.
		if localOK {
			digest = local.Digest
			if !sha256DigestRE.MatchString(digest) {
				return Source{}, fmt.Errorf("bootcimg: selected local image %q has no valid sha256 digest", image)
			}
			labels, inspected = local.Labels, true
		} else {
			digest = manifestDigest(remoteOut, remoteErr)
			labels, inspected = remote.Labels, remoteOK
		}
		if digest == "" {
			if remoteErr != nil {
				return Source{}, fmt.Errorf("bootcimg: resolving verified digest for %q: %w", image, remoteErr)
			}
			return Source{}, fmt.Errorf("bootcimg: resolving verified digest for %q: skopeo returned no valid sha256 digest", image)
		}
		bare = repositoryName(bare) + "@" + digest
	} else if !sha256DigestRE.MatchString(digest) {
		return Source{}, fmt.Errorf("bootcimg: invalid immutable digest in image reference %q", image)
	} else if localOK && local.Digest == digest {
		labels, inspected = local.Labels, true
	} else if remoteOK {
		labels, inspected = remote.Labels, true
	}

	if err := verifyImageSignature(ctx, r, keyPath, bare, warn); err != nil {
		return Source{}, err
	}
	return Source{Ref: bare, Labels: labels, Inspected: inspected}, nil
}

// verifyImageSignature runs cosign verify against the pinned reference,
// retrying transient registry responses across the bounded backoff
// schedule. No attempt relaxes verification; the last failure is fatal.
func verifyImageSignature(ctx context.Context, r *runner.Runner, keyPath, ref string, warn func(string)) error {
	var lastErr error
	for attempt := 1; attempt <= verifyAttempts; attempt++ {
		if attempt > 1 {
			r.Sleep(verifyBackoff[attempt-2])
		}
		_, err := r.Run(ctx, "cosign", "verify", "--key", keyPath, ref)
		if err == nil {
			return nil
		}
		lastErr = err
		if warn != nil && attempt < verifyAttempts {
			warn(fmt.Sprintf("cosign verify attempt %d/%d for %s failed, retrying: %v", attempt, verifyAttempts, ref, err))
		}
	}
	return fmt.Errorf("bootcimg: verifying image signature for %s: %w", ref, lastErr)
}

func manifestDigest(out []byte, err error) string {
	if err != nil {
		return ""
	}
	var manifest inspectManifest
	if json.Unmarshal(out, &manifest) != nil || !sha256DigestRE.MatchString(manifest.Digest) {
		return ""
	}
	return manifest.Digest
}

func digestReference(ref string) (digest string, ok bool) {
	_, digest, ok = strings.Cut(ref, "@")
	return digest, ok
}

func repositoryName(ref string) string {
	if repository, _, ok := strings.Cut(ref, "@"); ok {
		return repository
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i]
	}
	return ref
}

// IsRegistryRef mirrors fisherman's trust boundary: docker:// and plain
// references can be independently resolved and verified; local transports
// cannot satisfy a recipe-requested cosign verification.
func IsRegistryRef(ref string) bool {
	if rest, ok := strings.CutPrefix(ref, "docker://"); ok {
		return rest != ""
	}
	if prefix, _, ok := strings.Cut(ref, ":"); ok {
		switch prefix {
		case "containers-storage", "oci", "oci-archive", "dir", "docker-archive":
			return false
		}
	}
	return ref != ""
}
