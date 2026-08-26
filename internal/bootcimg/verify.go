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
	Digest string `json:"Digest"`
}

// CheckAndPinImage checks that image is reachable or cached. When keyPath is
// set, it also resolves the source Firn will actually install to an immutable
// digest, verifies that digest with cosign, and returns the pinned reference.
// Resolving before verification closes the tag-movement race. warn, when
// non-nil, receives one message per failed non-final cosign attempt (the
// attempt's stderr is embedded by the runner error).
func CheckAndPinImage(ctx context.Context, r *runner.Runner, image, keyPath string, warn func(string)) (string, error) {
	if keyPath != "" && !IsRegistryRef(image) {
		return "", fmt.Errorf("bootcimg: cosign verification requires a registry image reference, got %q", image)
	}
	bare := bareImageRef(image)
	remoteOut, remoteErr := r.Run(ctx, "skopeo", "inspect", "docker://"+bare)
	localOut, localErr := r.Run(ctx, "skopeo", "inspect", "containers-storage:"+bare)

	if keyPath == "" {
		var local inspectManifest
		if localErr == nil && json.Unmarshal(localOut, &local) == nil {
			return image, nil
		}
		if remoteErr == nil {
			return image, nil
		}
		return "", fmt.Errorf("bootcimg: image %q is not reachable in its registry and not present in local containers-storage: %w", image, remoteErr)
	}

	digest, pinned := digestReference(bare)
	if !pinned {
		// Match CheckImage's embedded-image rule: a valid local manifest wins
		// even if the registry advertises a newer tag. The selected digest is
		// still verified against the registry signature before installation.
		var local inspectManifest
		localSelected := localErr == nil && json.Unmarshal(localOut, &local) == nil
		if localSelected {
			digest = local.Digest
			if !sha256DigestRE.MatchString(digest) {
				return "", fmt.Errorf("bootcimg: selected local image %q has no valid sha256 digest", image)
			}
		} else {
			digest = manifestDigest(remoteOut, remoteErr)
		}
		if digest == "" {
			if remoteErr != nil {
				return "", fmt.Errorf("bootcimg: resolving verified digest for %q: %w", image, remoteErr)
			}
			return "", fmt.Errorf("bootcimg: resolving verified digest for %q: skopeo returned no valid sha256 digest", image)
		}
		bare = repositoryName(bare) + "@" + digest
	} else if !sha256DigestRE.MatchString(digest) {
		return "", fmt.Errorf("bootcimg: invalid immutable digest in image reference %q", image)
	}

	if err := verifyImageSignature(ctx, r, keyPath, bare, warn); err != nil {
		return "", err
	}
	return bare, nil
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
