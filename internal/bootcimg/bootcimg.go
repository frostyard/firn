// Ported from frostyard/fisherman (GPL-3.0-only), fisherman/internal/install/bootc.go.

// Package bootcimg builds and runs the `bootc install to-filesystem`
// invocation for firn's bootc image family. bootc is run natively when
// present on the host, otherwise inside the source image via
// `podman run --privileged`, per the bootc requirement that install must
// run FROM the container image.
//
// Only fisherman's non-secure install path is ported. The secure
// (schema-1 Type #2) path, the composefs backend, unified storage,
// generic-image, and additional-image-stores plumbing are intentionally
// absent: firn's recipe schema has no fields that would drive them.
package bootcimg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/frostyard/firn/internal/runner"
)

// Options configures a bootc installation.
//
// The field set is derived from what fisherman's BuildBootcArgs and
// bootcViaContainer consume on the non-secure path.
type Options struct {
	// Image is the OCI image to install (fisherman's SourceImgref). In
	// podman mode it is the image `podman run` executes bootc from; in
	// direct mode it is the containers-storage source bootc reads.
	Image string
	// TargetImgref is the update-tracking reference written into the
	// installed system for day-2 updates (--target-imgref). If empty,
	// defaults to Image.
	TargetImgref string
	// TargetDir is the path to the mounted root filesystem on the host
	// (fisherman's Options.Target).
	TargetDir string
	// Bootloader selects the bootloader passed to bootc via --bootloader.
	// Empty or "grub2" uses the default (grub2). "systemd" passes
	// --bootloader systemd.
	Bootloader string
	// SELinuxDisabled passes --disable-selinux when true.
	SELinuxDisabled bool
	// Composefs passes --composefs-backend. Required for images using the
	// composefs-native deployment backend — which includes every snosi
	// bootc image (they enable composefs at runtime; the default ostree
	// deploy path then fails outright). Ported from fisherman's
	// Options.ComposeFsBackend.
	Composefs bool
	// BtrfsSubvolumes records the recipe's target.btrfs_subvolumes choice.
	// It is retained in the schema but NOT emitted as a bootc flag —
	// `bootc install to-filesystem` has no subvolume option; the subvolume
	// layout is created by the disk-preparation step before bootc runs.
	// (Same retained-but-not-emitted pattern as fisherman's
	// Options.UnifiedStorage.)
	BtrfsSubvolumes bool
	// ScratchDir is the host-side directory used for temporary I/O during
	// installation. It is bind-mounted at /var/tmp inside the podman
	// container. Defaults to "/var/firn-tmp" when empty (fisherman's
	// default was "/var/fisherman-tmp").
	ScratchDir string
}

// scratchDir returns the host-side scratch directory from o, falling back
// to the default "/var/firn-tmp" when unset.
func (o Options) scratchDir() string {
	if o.ScratchDir != "" {
		return o.ScratchDir
	}
	return "/var/firn-tmp"
}

// resolvedTargetImgref returns the --target-imgref value: TargetImgref,
// falling back to Image, with any transport prefix stripped.
//
// Fisherman (bootcViaContainer): strip transport prefixes
// (containers-storage:, docker://, etc.) from the target-imgref so the
// installed system tracks a clean registry URL for day-2 bootc updates.
func (o Options) resolvedTargetImgref() string {
	ref := o.TargetImgref
	if ref == "" {
		ref = o.Image
	}
	return bareImageRef(ref)
}

// bareImageRef strips any OCI transport prefix from image, returning the bare
// registry reference. This handles both "scheme://ref" (e.g. "docker://") and
// "scheme:ref" (e.g. "containers-storage:") styles. Recipes may carry a
// "containers-storage:" prefix on the image field; the functions below must
// not double-prepend it.
func bareImageRef(image string) string {
	if idx := strings.Index(image, "://"); idx >= 0 {
		return image[idx+3:]
	}
	if idx := strings.Index(image, ":"); idx > 0 {
		if transport := image[:idx]; !strings.ContainsAny(transport, "/.") {
			return image[idx+1:]
		}
	}
	return image
}

// BuildArgs builds the argument slice for a DIRECT (native, no container)
// `bootc install to-filesystem` run, installing to o.TargetDir.
//
// Ported from fisherman's BuildBootcArgs; the argument order is preserved.
func BuildArgs(o Options) []string {
	return buildArgs(o, o.TargetDir, true)
}

// buildArgs is the shared core of BuildArgs and BuildPodmanArgs.
// installTarget is the final positional argument ("/target" in container
// mode, o.TargetDir in direct mode). direct selects the native-bootc
// --source-imgref behavior.
func buildArgs(o Options, installTarget string, direct bool) []string {
	args := []string{"install", "to-filesystem"}
	resolved := o.resolvedTargetImgref()
	if resolved != "" {
		args = append(args, "--target-imgref", resolved)
	}
	if o.SELinuxDisabled {
		args = append(args, "--disable-selinux")
	}
	// Placement matches fisherman: after --disable-selinux, before
	// --source-imgref (fisherman bootc.go:194-196). Snosi images enable
	// composefs at runtime; without this flag bootc's default ostree
	// deploy path fails "composefs: enabled at runtime, but support is
	// not compiled in" (observed in the loop-device E2E).
	if o.Composefs {
		args = append(args, "--composefs-backend")
	}
	if direct && resolved != "" {
		// Direct mode: bootc runs natively (not in a container) and needs
		// an explicit --source-imgref.  Use containers-storage transport
		// to read the image from local storage.  No network pull needed.
		// (In container mode bootc auto-detects the image it runs from,
		// so no --source-imgref is emitted.)
		args = append(args, "--source-imgref", "containers-storage:"+resolved)
	}
	if o.Bootloader != "" && o.Bootloader != "grub2" {
		args = append(args, "--bootloader", o.Bootloader)
	}
	// --skip-finalize keeps the target writable for post-install writes
	// (system config, users, flatpaks). The finalize step must later
	// replicate what bootc's finalize_filesystem() does internally:
	// fstrim, remount ro, fsfreeze/thaw. See fisherman
	// cmd/fisherman/main.go (Step 9) and internal/disk/format.go
	// FinalizeFilesystem.
	args = append(args, "--skip-finalize")
	args = append(args, installTarget)
	return args
}

// efiVarsPresentFn reports whether the host exposes the EFI variable
// store. A package var so tests can force both branches deterministically.
var efiVarsPresentFn = func() bool {
	_, err := os.Stat("/sys/firmware/efi/efivars")
	return err == nil
}

// selinuxActiveFn reports whether the host kernel has the SELinux security
// module loaded and active. The presence of /sys/fs/selinux/enforce is the
// standard indicator used by libselinux's is_selinux_enabled(). A package
// var so tests can force both branches deterministically.
var selinuxActiveFn = func() bool {
	_, err := os.Stat("/sys/fs/selinux/enforce")
	return err == nil
}

// BuildPodmanArgs builds the full `podman run --privileged ...` argument
// slice that wraps `bootc install to-filesystem` when bootc is not
// available natively. Ported from fisherman's bootcViaContainer
// (standard containers-storage path, no OCI-layout redirect).
func BuildPodmanArgs(o Options) []string {
	args := []string{
		"run", "--rm",
		"--privileged",
		"--pid=host",
		// The install container needs no network of its own (the image comes
		// from a bind mount); host networking also avoids requiring netavark's
		// nft/nftables stack, which minimal environments (initramfs) lack.
		"--network", "host",
		"--security-opt", "label=disable",
		"-v", "/dev:/dev",
		"-v", "/sys:/sys",
	}

	// Bind-mount the EFI variable store for efibootmgr, when present.
	if efiVarsPresentFn() {
		args = append(args, "-v", "/sys/firmware/efi/efivars:/sys/firmware/efi/efivars")
	}

	// bootc needs disk-backed /var/tmp for deployment scratch; tmpfs would
	// consume RAM and OOM-kill on large images.
	args = append(args, "-v", o.scratchDir()+":/var/tmp:z")

	args = append(args,
		"--mount", fmt.Sprintf("type=bind,src=%s,dst=/target,bind-propagation=rslave", o.TargetDir),
	)

	// Bind-mount /var/lib/containers so bootc can find the source image
	// layers. Fisherman skips this only for composefs (not ported); on
	// the standard path it is always required
	// (fisherman's NeedsContainerStorageMount).
	args = append(args, "-v", "/var/lib/containers:/var/lib/containers")

	args = append(args, o.Image)
	args = append(args, "bootc")
	args = append(args, buildArgs(o, "/target", false)...)
	return args
}

// Install runs `bootc install to-filesystem` against o.TargetDir.
//
// Fisherman selects between its direct and container paths by whether a
// SourceImgref was supplied (live-ISO mode runs bootc directly from
// inside the image; see fisherman's BootcInstall). firn always has the
// image reference and runs on arbitrary hosts, so tool availability
// decides instead: bootc on PATH runs natively (fisherman's bootcDirect
// shape), otherwise podman wraps it (fisherman's bootcViaContainer shape).
func Install(ctx context.Context, r *runner.Runner, o Options) error {
	if o.Image == "" {
		return fmt.Errorf("bootcimg: no image specified")
	}
	if o.TargetDir == "" {
		return fmt.Errorf("bootcimg: no target directory specified")
	}

	if _, err := r.LookPath("bootc"); err == nil {
		if _, err := r.Run(ctx, "bootc", BuildArgs(o)...); err != nil {
			return fmt.Errorf("bootc install to-filesystem: %w", err)
		}
		return nil
	}

	if _, err := r.LookPath("podman"); err != nil {
		return fmt.Errorf("bootcimg: neither bootc nor podman found on PATH: %w", err)
	}

	// When the target system has SELinux disabled and the host has SELinux
	// active, the host's loaded policy may not recognise the image's label
	// types (e.g. AlmaLinux label types on a Fedora host): bootc's
	// imgstorage lsetxattr and libostree's fsetxattr writes of
	// "security.selinux" fail with EINVAL from the kernel. Fisherman works
	// around this by compiling an LD_PRELOAD shim at runtime that silently
	// drops those xattr writes (see fisherman's BuildSelinuxBypassShim in
	// fisherman/internal/install/bootc.go). firn does not port the
	// compiled-at-runtime shim; fail up front with a clear error rather
	// than mid-install with an opaque EINVAL.
	if o.SELinuxDisabled && selinuxActiveFn() {
		return fmt.Errorf("bootcimg: installing with SELinux disabled on an SELinux-active host requires the LD_PRELOAD xattr shim, which firn does not implement (see fisherman's BuildSelinuxBypassShim); disable SELinux on the host or install from a host whose policy matches the image")
	}

	if _, err := r.Run(ctx, "podman", BuildPodmanArgs(o)...); err != nil {
		return fmt.Errorf("bootc install to-filesystem (via container): %w", err)
	}
	return nil
}

// CheckImage is the pre-flight image probe: it verifies that image is
// either reachable in its remote registry or already cached in local
// containers-storage, and returns an error when it is neither.
//
// Ported from fisherman's CheckImage. Fisherman returns a richer
// ImageCheck{NeedsPull, LayerCount, Offline} used to drive an explicit
// retrying pull with layer-progress reporting; firn's podman path pulls
// implicitly, so that result is reduced to reachable-or-not.
// TODO: port NeedsPull/LayerCount if firn grows explicit pull progress.
func CheckImage(ctx context.Context, r *runner.Runner, image string) error {
	type manifest struct {
		Digest string   `json:"Digest"`
		Layers []string `json:"Layers"`
	}
	bare := bareImageRef(image)

	// 1. Fetch remote normalized manifest (resolves fat/multi-arch manifests).
	_, remoteErr := r.Run(ctx, "skopeo", "inspect", "docker://"+bare)

	// 2. Fetch local digest from containers-storage.
	localOut, localErr := r.Run(ctx, "skopeo", "inspect", "containers-storage:"+bare)

	// If the image is present locally it is always used — even when the
	// remote is newer. Fisherman: the installer's job is to deploy the
	// embedded image; post-install updates are handled by `bootc update`.
	// Pulling a newer image during install would exceed the live-ISO's
	// scratch space and defeats the purpose of embedding the image in the
	// ISO in the first place. This is also the offline fallback: a full
	// offline install works when the image has been pre-pulled into
	// podman storage.
	if localErr == nil {
		var local manifest
		if json.Unmarshal(localOut, &local) == nil {
			return nil
		}
	}
	if remoteErr == nil {
		return nil
	}
	return fmt.Errorf("bootcimg: image %q is not reachable in its registry and not present in local containers-storage: %w", image, remoteErr)
}
