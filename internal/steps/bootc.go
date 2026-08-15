package steps

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/frostyard/firn/internal/abimg"
	"github.com/frostyard/firn/internal/bootcimg"
	"github.com/frostyard/firn/internal/disk"
	"github.com/frostyard/firn/internal/flatpak"
	"github.com/frostyard/firn/internal/luks"
	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/secureboot"
	"github.com/frostyard/firn/internal/sysconfig"
)

// DefaultTargetDir is where the target tree is mounted; tests and the
// CLI set Env.TargetDir (steps fall back to this). It lives under /run
// (tmpfs) rather than /mnt: on image-based hosts with a read-only root
// (snosi A/B, and fisherman's own live ISOs only escape this because
// their roots are overlays) /mnt is not writable, and mkdir fails with
// EROFS (observed live on a snow-ab host, 2026-08-11).
const DefaultTargetDir = "/run/firn/target"

const luksMapper = "firn-root"

func targetDir(env *pipeline.Env) string {
	if env.TargetDir == "" {
		env.TargetDir = DefaultTargetDir
	}
	return env.TargetDir
}

func runPartition(ctx context.Context, env *pipeline.Env) error {
	r := env.Recipe
	var layout disk.Layout
	var err error
	switch {
	case r.Target.Filesystem == "zfs":
		return fmt.Errorf("zfs root is unsupported by recipe schema version 1")
	case r.Target.Bootloader == "grub2":
		layout, err = disk.PartitionGrub2(ctx, env.Runner, r.Target.Disk)
	default: // systemd-boot is the recipe default (specs/recipe-schema.md)
		layout, err = disk.PartitionSystemdBoot(ctx, env.Runner, r.Target.Disk)
	}
	if err != nil {
		return err
	}
	env.Layout = layout
	env.RootDev = layout.Root
	return nil
}

func runFormatBoot(ctx context.Context, env *pipeline.Env) error {
	if err := disk.FormatESP(ctx, env.Runner, env.Layout.ESP); err != nil {
		return err
	}
	if env.Layout.Boot != "" {
		return disk.FormatBootExt4(ctx, env.Runner, env.Layout.Boot)
	}
	return nil
}

// resolvePassphrase returns the LUKS unlock secret for the recipe's
// encryption mode: the user's passphrase for passphrase modes, a
// generated recovery key (disclosed via the progress protocol) for
// tpm2-luks.
func resolvePassphrase(env *pipeline.Env) (string, error) {
	s := env.Recipe.Security
	switch s.Encryption {
	case "luks-passphrase", "tpm2-luks-passphrase":
		if s.PassphraseFile != "" {
			data, err := os.ReadFile(s.PassphraseFile)
			if err != nil {
				return "", fmt.Errorf("reading passphrase_file: %w", err)
			}
			return strings.TrimRight(string(data), "\n"), nil
		}
		return s.Passphrase, nil
	case "tpm2-luks":
		key, err := luks.GenerateRecoveryKey()
		if err != nil {
			return "", err
		}
		env.Emit(progress.RecoveryKey{Key: key})
		return key, nil
	}
	return "", fmt.Errorf("no passphrase source for encryption %q", s.Encryption)
}

func runLuksFormat(ctx context.Context, env *pipeline.Env) error {
	key, err := resolvePassphrase(env)
	if err != nil {
		return err
	}
	env.LuksKey = key
	if err := luks.Format(ctx, env.Runner, env.Layout.Root, key); err != nil {
		return err
	}
	mapperPath, err := luks.Open(ctx, env.Runner, env.Layout.Root, luksMapper, key)
	if err != nil {
		return err
	}
	env.RootDev = mapperPath
	env.Defer("close LUKS mapper", func() error {
		return luks.Close(context.Background(), env.Runner, luksMapper)
	})
	return nil
}

func runFormatRoot(ctx context.Context, env *pipeline.Env) error {
	r := env.Recipe
	if err := disk.FormatRoot(ctx, env.Runner, env.RootDev, r.Target.Filesystem); err != nil {
		return err
	}
	if r.Target.Filesystem == "btrfs" && r.Target.BtrfsSubvolumes {
		// Subvolumes are created on a scratch mount of the raw
		// filesystem; the real mount (subvol=@) happens in mount-target.
		scratch := targetDir(env) + "-subvol"
		if err := os.MkdirAll(scratch, 0o755); err != nil {
			return err
		}
		if _, err := env.Runner.Run(ctx, "mount", "-t", "btrfs", env.RootDev, scratch); err != nil {
			return err
		}
		subvolErr := disk.CreateSubvolumes(ctx, env.Runner, scratch)
		if err := disk.Unmount(ctx, env.Runner, scratch); err != nil && subvolErr == nil {
			subvolErr = err
		}
		return subvolErr
	}
	return nil
}

func runMountTarget(ctx context.Context, env *pipeline.Env) error {
	r := env.Recipe
	dir := targetDir(env)
	if err := disk.MountTarget(ctx, env.Runner, env.Layout, env.RootDev,
		r.Target.Filesystem, r.Target.BtrfsSubvolumes, dir); err != nil {
		return err
	}
	env.Defer("unmount target", func() error {
		return disk.UnmountRecursive(context.Background(), env.Runner, dir)
	})
	return nil
}

func runBootcInstall(ctx context.Context, env *pipeline.Env) error {
	r := env.Recipe
	sourceRef := env.BootcSourceRef
	if sourceRef == "" {
		return fmt.Errorf("bootc image source missing (preflight-image did not run)")
	}
	targetRef := r.Image.TargetRef
	if targetRef == "" {
		// A verified source is digest-pinned; day-two updates must continue to
		// track the recipe's original tag rather than that immutable digest.
		targetRef = r.Image.Ref
	}
	bootloader := r.Target.Bootloader
	if bootloader == "" {
		bootloader = "systemd" // recipe default (specs/recipe-schema.md)
	}
	// The podman wrapper bind-mounts the scratch dir over the
	// container's /var/tmp; it must exist before podman resolves the
	// mount source (observed: exit 125 "statfs /var/firn-tmp: no such
	// file or directory" in the loop-device E2E).
	scratch := env.ScratchDir
	if scratch == "" {
		scratch = "/var/firn-tmp"
		env.ScratchDir = scratch
	}

	// On a RAM-only installer ISO the whole rootfs is tmpfs, so podman's
	// default image store (/var/lib/containers) and the scratch dir are
	// RAM-backed: a bootc image pull unpacks into /var/lib/containers and
	// dies with ENOSPC partway through the layers (observed live: cayo
	// pull failed at "write .../solidity.vim: no space left on device",
	// 2026-08-12). Redirect the image store and bootc's deployment scratch
	// onto the target disk — which has room — for the pull+deploy, then
	// tear the redirect down before the later steps unmount the target.
	if bootcimg.StorageSpaceConstrained() {
		teardown, diskScratch, err := redirectBootcStorageToDisk(ctx, env)
		if err != nil {
			return err
		}
		defer teardown()
		scratch = diskScratch
		env.ScratchDir = scratch
	}

	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return fmt.Errorf("creating scratch dir: %w", err)
	}
	if err := bootcimg.Install(ctx, env.Runner, bootcimg.Options{
		Image:        sourceRef,
		TargetImgref: targetRef,
		TargetDir:    targetDir(env),
		Bootloader:   bootloader,
		ScratchDir:   scratch,
		// Always on: firn installs snosi images, and all snosi bootc
		// images use the composefs-native backend. Becomes a recipe
		// field only if a non-composefs snosi image ever exists.
		Composefs: true,
	}); err != nil {
		return err
	}

	// Secure Boot: extract the secure image's usr/lib/{snosi,shim} subtrees
	// NOW, while the container store is still mounted (and, on the RAM ISO,
	// still redirected onto disk with no_pivot_root in effect) — the deferred
	// teardown() above unwinds it on return. A composefs deployment has no
	// materialized /usr on the target, so the ESP chain, MOK cert and contract
	// can only be read from the image (ADR-0014). The extracted subtrees are a
	// few MB, so a host tmp dir (RAM on the ISO) is fine — unlike the multi-GB
	// image store this does not need the disk redirect.
	if secureBootc(r) {
		dest, err := os.MkdirTemp("", "firn-secure-root-")
		if err != nil {
			return fmt.Errorf("secure image root scratch: %w", err)
		}
		if err := secureboot.ExtractSecureImageRoot(ctx, env.Runner, sourceRef, dest); err != nil {
			return err
		}
		env.SecureImageRoot = dest
		env.Defer("remove secure image root", func() error { return os.RemoveAll(dest) })
	}
	return nil
}

// runESPStageBootc stages the Secure Boot ESP chain (shim -> MOK-signed
// systemd-boot -> MokManager) from the extracted secure image onto the freshly
// installed ESP, after verifying the image's schema-1 contract (ADR-0014).
func runESPStageBootc(ctx context.Context, env *pipeline.Env) error {
	if env.SecureImageRoot == "" {
		return fmt.Errorf("esp-stage: secure image root missing (bootc-install did not extract it)")
	}
	contract, err := secureboot.Load(env.SecureImageRoot)
	if err != nil {
		return err
	}
	return secureboot.StageESPChain(ctx, env.Runner, targetDir(env), env.SecureImageRoot, contract.MokCertificate)
}

// runMOKStageBootc stages the snosi MOK via mokutil so shim's MokManager
// prompts the user to enroll it on first boot — the human step that plants
// firmware trust in the MOK-signed second stage. The cert comes from the
// extracted image (its /usr is composefs, not on the target).
func runMOKStageBootc(ctx context.Context, env *pipeline.Env) error {
	if env.SecureImageRoot == "" {
		return fmt.Errorf("mok-stage: secure image root missing")
	}
	contract, err := secureboot.Load(env.SecureImageRoot)
	if err != nil {
		return err
	}
	cert := filepath.Join(env.SecureImageRoot, strings.TrimPrefix(contract.MokCertificate, "/"))
	return abimg.StageMOK(ctx, env.Runner, cert, env.Recipe.Security.MokPasswordFile)
}

// bootcRAMPaths are the host directories a bootc image pull writes to
// that are RAM-backed on the installer ISO and must be redirected onto
// the target disk:
//
//   - /var/lib/containers is podman's default graphroot, where pulled
//     layers are unpacked (ENOSPC at "write .../solidity.vim" without
//     this redirect).
//   - /var/tmp is where containers/image stages downloaded blobs before
//     unpacking. containers/storage hardcodes this path (store.TmpDir())
//     regardless of TMPDIR, so a multi-GB layer blob overflows the RAM
//     rootfs there too (ENOSPC at "storing blob to file
//     /var/tmp/container_images_storageXXX/..." — observed after the
//     graphroot redirect alone, 2026-08-12).
var bootcRAMPaths = []string{"/var/lib/containers", "/var/tmp"}

// redirectBootcStorageToDisk points podman's image store and bootc's
// deployment scratch at disk-backed directories on the (already-mounted)
// target root, so a large bootc image can be pulled and unpacked without
// exhausting the RAM-backed installer rootfs. It bind-mounts a directory
// on the target over /var/lib/containers so podman's default graphroot
// lands on disk without changing the podman-run argv (the existing,
// E2E-proven composefs flow still bind-mounts /var/lib/containers into
// the container unchanged).
//
// This deliberately does NOT adopt fisherman's later composefs storage
// dance (redirect via --root, skopeo-export to an OCI layout, run bootc
// with --source-imgref oci:<path>). That path solves the same live-media
// ENOSPC, but makes bootc open the source image over the *oci:* transport,
// which a hardened snosi image's own /etc/containers/policy.json rejects
// (reject-by-default, exception only for containers-storage) — forcing
// fisherman's writeLocalTransportPolicy override and a day lost to secure
// -image signature-policy failures. Keeping the store at the default path
// and on the containers-storage transport preserves the exact flow the
// bootc loop-device E2E already validated against a hardened image, and
// stays out of that policy rabbit hole entirely. The tradeoff is space:
// the unpacked store and the deployment coexist on the target during the
// install (both freed of the store afterward), so very small targets that
// fisherman's store-dropping would have fit could ENOSPC here. Snosi's
// disk floor makes that acceptable; revisit if a tighter target appears.
//
// It returns the disk-backed scratch path and a teardown func that
// unmounts the store bind and removes the scratch tree. The caller must
// run teardown before the target is unmounted (i.e. via a plain defer in
// the step, not env.Defer, which fires after finalize has remounted the
// root read-only).
func redirectBootcStorageToDisk(ctx context.Context, env *pipeline.Env) (teardown func(), scratch string, err error) {
	base := filepath.Join(targetDir(env), ".firn-install")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, "", fmt.Errorf("creating install scratch base: %w", err)
	}

	// Track every mount so teardown unmounts exactly those, LIFO, even if
	// a later step fails partway.
	var mounted []string
	undo := func() {
		for i := len(mounted) - 1; i >= 0; i-- {
			// Recursive: podman leaves its overlay driver mount under
			// /var/lib/containers/storage, so a plain umount of the bind
			// fails "target is busy" and the store is never reclaimed
			// (observed live 2026-08-12). Fall back to a lazy detach so a
			// stubborn mount still releases the underlying tree for removal.
			if _, e := env.Runner.Run(context.Background(), "umount", "-R", mounted[i]); e != nil {
				if _, e2 := env.Runner.Run(context.Background(), "umount", "-R", "-l", mounted[i]); e2 != nil {
					env.Emit(progress.Warning{Code: "store_umount_failed", Message: e2.Error()})
				}
			}
		}
	}

	// The only disk-backed space on a RAM installer is the target root
	// itself, but `bootc install to-filesystem` requires /target to
	// contain only mount points and fails on any stray directory
	// ("Found empty directory: scratch", observed live 2026-08-12). Bind
	// the scratch base onto itself so it presents as a mount point: bootc
	// treats it as a foreign mount and does not recurse into it, leaving
	// our store tree invisible to the empty-rootfs check. Teardown
	// removes it before the deployment is finalized.
	if _, err := env.Runner.Run(ctx, "mount", "--bind", base, base); err != nil {
		return nil, "", fmt.Errorf("making install scratch a mount point: %w", err)
	}
	mounted = append(mounted, base)

	scratch = filepath.Join(base, "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		undo()
		return nil, "", fmt.Errorf("creating disk-backed scratch dir: %w", err)
	}

	// Bind a disk-backed directory (under the self-bound base) over each
	// RAM-backed path the pull writes to.
	for i, mnt := range bootcRAMPaths {
		disk := filepath.Join(base, fmt.Sprintf("bind%d", i))
		if err := os.MkdirAll(disk, 0o755); err != nil {
			undo()
			return nil, "", fmt.Errorf("creating disk-backed dir for %s: %w", mnt, err)
		}
		if err := os.MkdirAll(mnt, 0o755); err != nil {
			undo()
			return nil, "", fmt.Errorf("ensuring mountpoint %s: %w", mnt, err)
		}
		if _, err := env.Runner.Run(ctx, "mount", "--bind", disk, mnt); err != nil {
			undo()
			return nil, "", fmt.Errorf("bind-mounting disk-backed %s: %w", mnt, err)
		}
		mounted = append(mounted, mnt)
	}
	env.Emit(progress.Info{Message: "redirected container image store and blob staging to target disk (installer runs in RAM)"})

	// The installer's root is the initramfs (a rootfs/ramfs), and
	// pivot_root(2) cannot pivot off it: the bootc container fails to
	// start with "crun: pivot_root: Invalid argument" (observed live
	// once the ENOSPC was cleared, 2026-08-12). Tell podman to fall back
	// to MS_MOVE+chroot via no_pivot_root, scoped to this process with a
	// merged CONTAINERS_CONF_OVERRIDE so no global podman config is
	// touched. Only reached on the RAM-installer path, so hosts where
	// pivot_root works (the loop-device E2E) are unaffected.
	restoreEnv, err := enableNoPivotRoot(base)
	if err != nil {
		undo()
		return nil, "", err
	}

	teardown = func() {
		// Unmount the binds and reclaim their space before the target is
		// finalized/unmounted. Best-effort: a failure here must not mask
		// the install result, so warn rather than fail.
		restoreEnv()
		undo()
		if e := os.RemoveAll(base); e != nil {
			env.Emit(progress.Warning{Code: "store_cleanup_failed", Message: e.Error()})
		}
	}
	return teardown, scratch, nil
}

// enableNoPivotRoot writes a minimal containers.conf fragment enabling
// no_pivot_root and points podman at it via CONTAINERS_CONF_OVERRIDE
// (which merges over the defaults rather than replacing them). It
// returns a func that restores the prior env value.
func enableNoPivotRoot(dir string) (restore func(), err error) {
	conf := filepath.Join(dir, "podman-override.conf")
	if err := os.WriteFile(conf, []byte("[engine]\nno_pivot_root = true\n"), 0o644); err != nil {
		return nil, fmt.Errorf("writing podman no-pivot override: %w", err)
	}
	const key = "CONTAINERS_CONF_OVERRIDE"
	prev, had := os.LookupEnv(key)
	if err := os.Setenv(key, conf); err != nil {
		return nil, fmt.Errorf("setting %s: %w", key, err)
	}
	return func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	}, nil
}

// runRetagRoot retypes the root partition to the DPS root-x86-64 GUID
// after bootc install (systemd-boot path) — ported from fisherman's
// documented retag dance. Newer snosi bootc images write UKI-style BLS
// entries (a `uki` line, no `options` line): the kernel cmdline is baked
// into the UKI and carries no install-time root=UUID, so the booted
// initramfs finds root ONLY via systemd-gpt-auto discovery — which
// requires the DPS type GUID, not the generic Linux one our partitioning
// writes (observed live: 90s of silence then initramfs emergency mode,
// TUI bootc E2E 2026-08-11).
//
// This holds for ENCRYPTED roots too: gpt-auto sets up cryptsetup only
// for a partition it can discover, so without the retag an encrypted
// bootc install boots to "Expecting device /dev/gpt-auto-root" then
// emergency mode (matrix 2026-08-12). Retyping needs the partition free
// for the GPT re-read: unmount the tree, and on the encrypted path also
// close the LUKS mapper first (the open mapper holds the partition busy),
// then reopen it under the same name for the remaining steps.
func runRetagRoot(ctx context.Context, env *pipeline.Env) error {
	r := env.Recipe
	dir := targetDir(env)
	partNum, err := rootPartNum(env.Layout)
	if err != nil {
		return err
	}
	encrypted := r.Security.Encryption != "none"
	if err := disk.UnmountRecursive(ctx, env.Runner, dir); err != nil {
		return err
	}
	if encrypted {
		if err := luks.Close(ctx, env.Runner, luksMapper); err != nil {
			return err
		}
	}
	if err := disk.SetPartitionType(ctx, env.Runner, r.Target.Disk, partNum, disk.LinuxRootX8664GUID); err != nil {
		return err
	}
	if err := disk.WaitForPartition(ctx, env.Runner, r.Target.Disk, partNum, 10*time.Second); err != nil {
		return err
	}
	if encrypted {
		// Reopen under the same mapper name so env.RootDev still resolves;
		// the mapper stays open for tpm-enroll/sysconfig/finalize, and the
		// luks-format cleanup defer closes it at the end.
		if _, err := luks.Open(ctx, env.Runner, env.Layout.Root, luksMapper, env.LuksKey); err != nil {
			return err
		}
	}
	return disk.MountTarget(ctx, env.Runner, env.Layout, env.RootDev,
		r.Target.Filesystem, r.Target.BtrfsSubvolumes, dir)
}

// rootPartNum extracts the root partition's number from the layout.
func rootPartNum(l disk.Layout) (int, error) {
	suffix := strings.TrimPrefix(l.Root, l.Disk)
	suffix = strings.TrimPrefix(suffix, "p")
	n, err := strconv.Atoi(suffix)
	if err != nil {
		return 0, fmt.Errorf("cannot derive partition number of %s on %s: %w", l.Root, l.Disk, err)
	}
	return n, nil
}

// runTPMEnrollBootc enrolls a TPM2 unlock token on the encrypted bootc
// root at install time, bound to the deployed UKI's signed PCR 11 policy
// (abimg.EnrollTPMFromUKI — the A/B path's proven scheme), so the
// installed system auto-unlocks on first boot with no recovery-key
// prompt. The UKI is the one bootc just wrote under the target ESP's
// EFI/Linux/; its name is kernel-version-based (unlike A/B's
// channel_version), so glob for it.
func runTPMEnrollBootc(ctx context.Context, env *pipeline.Env) error {
	// The UKI's path under the target varies (ESP at boot/efi vs boot,
	// deployment boot), and its name is kernel-version-based, so search
	// the whole target tree for an *.efi under an EFI/Linux/ directory.
	uki, allEFI := findBootcUKI(filepath.Join(targetDir(env), "boot"))
	if uki == "" {
		// Diagnostic: report every .efi we DID see so a wrong path or an
		// unmounted ESP is obvious from the console.
		seen := "(none)"
		if len(allEFI) > 0 {
			seen = strings.Join(allEFI, ", ")
		}
		env.Emit(progress.Info{Message: "tpm-enroll: no EFI/Linux UKI under target; .efi files present: " + seen})
		return fmt.Errorf("tpm-enroll: no UKI under the target's EFI/Linux (a UKI-entry bootc image is required for TPM2 unlock); saw .efi: %s", seen)
	}

	// The unlock key travels via a 0600 file, never argv.
	keyFile, err := os.CreateTemp("", "firn-unlock-")
	if err != nil {
		return err
	}
	defer os.Remove(keyFile.Name())
	if err := os.Chmod(keyFile.Name(), 0o600); err != nil {
		return err
	}
	if _, err := keyFile.WriteString(env.LuksKey); err != nil {
		return err
	}
	keyFile.Close()

	// Enroll on the LUKS PARTITION (which holds the LUKS2 superblock the
	// token is written into), never the opened mapper — same rule as the
	// A/B path (runTPMEnroll).
	return abimg.EnrollTPMFromUKI(ctx, env.Runner, uki, env.Layout.Root, keyFile.Name())
}

// findBootcUKI walks a boot tree for the deployed UKI: the first *.efi
// whose directory ends in EFI/Linux (its name is kernel-version-based).
// It also returns every *.efi seen, for a diagnostic when none matches
// (a wrong path or an unmounted ESP is then obvious). Walk errors are
// ignored so a partial tree still yields what is there.
func findBootcUKI(root string) (uki string, allEFI []string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".efi") {
			allEFI = append(allEFI, p)
			// bootc drops its UKI in an EFI/Linux/<vendor>/ subdirectory
			// (observed: EFI/Linux/bootc/bootc_composefs-<hash>.efi), so
			// match anywhere under EFI/Linux/, not just directly in it.
			// systemd-bootx64.efi lives under EFI/systemd/ and is excluded.
			if uki == "" && strings.Contains(filepath.ToSlash(p), "/EFI/Linux/") {
				uki = p
			}
		}
		return nil
	})
	return uki, allEFI
}

func runFlatpaks(ctx context.Context, env *pipeline.Env) error {
	apps := env.Recipe.System.Flatpaks
	if env.Recipe.System.CoreFlatpaks {
		// The core set ships in the image's /usr. On an ostree
		// deployment that tree is materialized on disk; on
		// composefs-native targets /usr lives in composefs objects and
		// is not readable as plain files at install time — report,
		// don't guess.
		etcDir, err := sysconfig.EtcDir(ctx, env.Runner, targetDir(env))
		if err != nil {
			return err
		}
		core, ok, err := flatpak.CoreSet(filepath.Dir(etcDir))
		if err != nil {
			return err
		}
		if !ok {
			// Composefs-native deployments (every snosi desktop image)
			// keep /usr in composefs objects unreadable at install time,
			// so the deployment publishes no readable core set. Fall back
			// to the copy the installer medium embeds.
			core, ok, err = flatpak.InstallerCoreSet()
			if err != nil {
				return err
			}
		}
		if !ok {
			msg := "core_flatpaks: no readable core set in the deployment or on the installer medium"
			env.Emit(progress.Warning{Code: "no_core_set", Message: msg})
			env.AddSummary("no_core_set", msg)
		}
		apps = append(apps, core...)
	}
	if len(apps) == 0 {
		return nil
	}
	unreachable, err := flatpak.Provision(ctx, env.Runner, flatpak.Opts{
		TargetDir: targetDir(env), Apps: apps,
	})
	if err != nil {
		return err
	}
	for _, app := range unreachable {
		env.Emit(progress.Warning{Code: progress.CodeFlatpakUnreachable, Message: app})
		env.AddSummary(progress.CodeFlatpakUnreachable, app)
	}
	return nil
}

func runSysconfig(ctx context.Context, env *pipeline.Env) error {
	sys := env.Recipe.System
	w := &sysconfig.DeploymentWriter{TargetDir: targetDir(env), Runner: env.Runner}

	if err := w.WriteHostname(ctx, sys.Hostname); err != nil {
		return err
	}
	if sys.User != nil {
		missing, err := w.CreateUser(ctx, *sys.User)
		if err != nil {
			return err
		}
		for _, g := range missing {
			msg := fmt.Sprintf("group %q does not exist in the image; user %s not joined to it", g, sys.User.Name)
			env.Emit(progress.Warning{Code: "group_missing", Message: msg})
			env.AddSummary("group_missing", msg)
		}
		if key, err := resolveKey(sys.User.SSHAuthorizedKey, sys.User.SSHAuthorizedKeyFile); err != nil {
			return err
		} else if key != "" {
			if err := w.WriteUserAuthorizedKey(ctx, sys.User.Name, key); err != nil {
				return err
			}
		}
	}
	if key, err := resolveKey(sys.RootSSHAuthorizedKey, sys.RootSSHAuthorizedKeyFile); err != nil {
		return err
	} else if key != "" {
		if err := w.WriteRootAuthorizedKey(ctx, key); err != nil {
			return err
		}
	}
	if err := w.WriteLocale(ctx, sys.Locale); err != nil {
		return err
	}
	if err := w.WriteTimezone(ctx, sys.Timezone); err != nil {
		return err
	}
	return w.WriteKeyboard(ctx, sys.Keyboard)
}

func resolveKey(inline, file string) (string, error) {
	if inline != "" {
		return inline, nil
	}
	if file == "" {
		return "", nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("reading ssh key file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func runFinalize(ctx context.Context, env *pipeline.Env) error {
	return disk.FinalizeFilesystem(ctx, env.Runner, targetDir(env))
}
