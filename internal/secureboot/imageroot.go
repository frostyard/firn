// Ported from frostyard/fisherman (GPL-3.0-only), fisherman/internal/install/secureroot.go.

package secureboot

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/frostyard/firn/internal/runner"
)

// SecureArtifactSubtrees holds every artifact the secure install needs from the
// image: the schema-1 contract, the MOK certificate, the PCR public key and the
// MOK-signed second stage (usr/lib/snosi), plus Debian's Microsoft-signed shim
// and MokManager (usr/lib/shim), which are what make the ESP chain bootable
// under Secure Boot.
var SecureArtifactSubtrees = []string{"usr/lib/snosi", "usr/lib/shim"}

// ExtractSecureImageRoot materialises SecureArtifactSubtrees out of the image
// into dest, producing a directory that can be read with the same relative
// paths the contract uses.
//
// Why this exists: a composefs deployment does not present a merged root under
// the target mount. Its writable /etc is at state/deploy/<hash>/etc and /usr
// comes from the composefs image, which is not a directory tree on the target
// at all — so reading <target>/usr/lib/snosi/... cannot work, and the secure
// install is composefs by contract.
//
// Reading from the image instead is sound rather than merely convenient: bootc
// pins the deployment to the image CheckImage verified, so source and
// deployment are identical by construction (ADR-0014).
//
// Divergence from fisherman: firn does not pass an explicit --root. On the RAM
// installer ISO the container store is redirected onto the target disk by a
// bind mount over /var/lib/containers (bootc.go redirectBootcStorageToDisk), so
// the default store path already points at disk; on a roomy install it is the
// real default store. Either way podman resolves the pinned image from
// /var/lib/containers/storage — the same store bootcimg.Install used — provided
// this runs before that redirect is torn down. The no_pivot_root override the
// redirect installs (CONTAINERS_CONF_OVERRIDE) is likewise still in effect.
func ExtractSecureImageRoot(ctx context.Context, r *runner.Runner, image, dest string) error {
	// --network host, like bootcimg.BuildPodmanArgs: this container only reads
	// files and writes a tar to stdout, so it needs no network of its own, and
	// host networking avoids requiring netavark + nft, which the minimal
	// installer ISO does not ship (observed live 2026-08-12: the default
	// bridge net failed the extraction with "netavark: nftables error: unable
	// to execute nft").
	args := []string{
		"run", "--rm", "--pull=never", "--privileged", "--network", "host",
		"-v", "/var/lib/containers/storage:/var/lib/containers/storage",
		image, "tar", "-cf", "-", "-C", "/",
	}
	args = append(args, SecureArtifactSubtrees...)

	out, err := r.Run(ctx, "podman", args...)
	if err != nil {
		return fmt.Errorf("secureboot: extracting %v from the verified image: %w", SecureArtifactSubtrees, err)
	}
	return untarInto(bytes.NewReader(out), dest)
}

// untarInto extracts a tar stream into dest, refusing entries that would escape
// it. The archive comes from an image we control, but path traversal is cheap
// to rule out and expensive to discover later.
func untarInto(rd io.Reader, dest string) error {
	reader := tar.NewReader(rd)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("secureboot: reading secure artifact archive: %w", err)
		}
		name := filepath.Clean(header.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("secureboot: refusing secure artifact path outside the extraction root: %q", header.Name)
		}
		target := filepath.Join(dest, name)
		if rel, relErr := filepath.Rel(dest, target); relErr != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("secureboot: refusing secure artifact path outside the extraction root: %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, reader); err != nil {
				file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
		}
		// Other entry types (symlinks, devices) are not part of this subtree
		// and are deliberately skipped rather than reproduced.
	}
}
