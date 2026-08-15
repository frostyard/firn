// Package steps assembles firn's install pipeline from a validated
// recipe: the family picks the backbone, options splice steps in or
// out (docs/design/architecture.md, "The step engine"). Steps whose
// Run is nil are placeholders for later roadmap phases; the assembled
// shape, weights, and tool declarations are real.
package steps

import (
	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/recipe"
)

// Assemble builds the pipeline for a validated recipe. Preflight steps
// are prepended last so the tool check can see the whole list.
func Assemble(l *recipe.Loaded) *pipeline.Pipeline {
	r := &l.Recipe
	var work []pipeline.Step
	switch r.Image.Family {
	case recipe.FamilyBootc:
		work = bootcSteps(r)
	case recipe.FamilyAB:
		work = abSteps(r)
	}

	p := &pipeline.Pipeline{Steps: work}
	p.Steps = append(preflightSteps(p, r), work...)
	return p
}

func bootcSteps(r *recipe.Recipe) []pipeline.Step {
	encrypted := r.Security.Encryption != "none"
	bootcTools := []string{"podman", "skopeo", "mount", "umount"}
	if r.Image.CosignPubKey != "" {
		bootcTools = append(bootcTools, "cosign")
	}
	steps := []pipeline.Step{
		{Name: "partition", Weight: 5, Destructive: true,
			Tools: []string{"sfdisk", "wipefs", "partprobe", "udevadm"}, Run: runPartition},
		{Name: "format-boot", Weight: 3, Destructive: true,
			Tools: []string{"mkfs.fat", "mkfs.ext4"}, Run: runFormatBoot},
	}
	if encrypted {
		steps = append(steps, pipeline.Step{Name: "luks-format", Weight: 3, Destructive: true,
			Tools: []string{"cryptsetup"}, Run: runLuksFormat})
	}
	steps = append(steps,
		pipeline.Step{Name: "format-root", Weight: 4, Destructive: true,
			Tools: rootFsTools(r.Target.Filesystem), Run: runFormatRoot},
		pipeline.Step{Name: "mount-target", Weight: 1, Destructive: false,
			Tools: []string{"mount", "umount"}, Run: runMountTarget},
		pipeline.Step{Name: "bootc-install", Weight: 55, Destructive: true,
			Tools: bootcTools, Run: runBootcInstall},
	)
	if r.Security.Encryption == "tpm2-luks" || r.Security.Encryption == "tpm2-luks-passphrase" {
		// Enroll a TPM2 token at install time bound to the deployed UKI's
		// SIGNED PCR 11 policy (the A/B path's proven scheme) -- not
		// fisherman's PCR-7 first-boot staging. PCR 11 is measured by
		// systemd-stub and is firmware-independent, so it is valid on the
		// installed system's first boot; PCR-7 staging cannot help because
		// first boot must unlock BEFORE the staged oneshot could run
		// (matrix 2026-08-12: encrypted bootc -> emergency mode). Runs
		// BEFORE retag-root, so the ESP mounted by mount-target (carrying
		// the just-deployed UKI) is untouched when the .pcrpkey is read.
		steps = append(steps, pipeline.Step{Name: "tpm-enroll", Weight: 1,
			Tools: []string{"systemd-cryptenroll", "objcopy"}, Run: runTPMEnrollBootc})
	}
	if secureBootc(r) {
		// Stage the Secure Boot ESP chain (shim -> MOK-signed systemd-boot ->
		// MokManager) after bootc-install writes plain systemd-boot, before
		// retag-root unmounts/remounts the ESP (ADR-0014). Independent of
		// tpm-enroll (which reads the UKI .pcrpkey, not EFI/BOOT); placed
		// after it to keep the "tpm-enroll before retag" invariant intact.
		steps = append(steps, pipeline.Step{Name: "esp-stage", Weight: 2, Destructive: true,
			Tools: []string{"sbverify"}, Run: runESPStageBootc})
	}
	if r.Target.Bootloader != "grub2" {
		// UKI-style BLS entries boot via gpt-auto discovery, which needs
		// the DPS root type GUID (see runRetagRoot) — for encrypted roots
		// too: gpt-auto only sets up cryptsetup for a partition it can
		// discover, so without the retag an encrypted bootc install boots
		// to emergency mode "Expecting device /dev/gpt-auto-root" (matrix
		// 2026-08-12). The encrypted retag closes/reopens the LUKS mapper
		// to free the partition for the GPT re-read.
		retagTools := []string{"sfdisk", "udevadm", "mount", "umount"}
		if encrypted {
			retagTools = append(retagTools, "cryptsetup")
		}
		steps = append(steps, pipeline.Step{Name: "retag-root", Weight: 1, Destructive: true,
			Tools: retagTools, Run: runRetagRoot})
	}
	steps = append(steps,
		pipeline.Step{Name: "flatpaks", Weight: 15, Tools: flatpakTools(r), Run: runFlatpaks},
		pipeline.Step{Name: "sysconfig", Weight: 8, Tools: []string{"useradd", "chpasswd", "chroot"}, Run: runSysconfig},
		pipeline.Step{Name: "finalize", Weight: 5, Tools: []string{"fstrim", "fsfreeze"}, Run: runFinalize},
	)
	if secureBootc(r) {
		// Stage the MOK last (like the A/B path): it writes firmware NVRAM via
		// mokutil, not the target, so it is order-independent w.r.t. the
		// target-modifying steps. Its cert dependency (env.SecureImageRoot)
		// survives because that scratch is removed only at pipeline exit.
		steps = append(steps, pipeline.Step{Name: "mok-stage", Weight: 1,
			Tools: []string{"mokutil", "openssl"}, Run: runMOKStageBootc})
	}
	return steps
}

// secureBootc reports whether this is a bootc install that must set up UEFI
// Secure Boot: the recipe opted in with security.mok = "enroll" (ADR-0014).
// mok is a fail-closed, Secure-Boot-only recipe field, so "enroll" already
// implies Secure Boot is active on this machine (internal/recipe/validate.go).
func secureBootc(r *recipe.Recipe) bool {
	return r.Image.Family == recipe.FamilyBootc && r.Security.Mok == "enroll"
}

// flatpakTools declares the flatpak binary only when the recipe
// actually provisions flatpaks — a flatpak-less install (e.g. a server
// image) must not fail tool preflight over an unused binary.
func flatpakTools(r *recipe.Recipe) []string {
	if len(r.System.Flatpaks) > 0 || r.System.CoreFlatpaks {
		return []string{"flatpak", "du", "sh", "tar"}
	}
	return nil
}

func abSteps(r *recipe.Recipe) []pipeline.Step {
	steps := []pipeline.Step{
		{Name: "fetch-index", Weight: 2, Tools: []string{"gpgv", "blockdev"}, Run: runFetchIndex},
		{Name: "stream-write", Weight: 50, Destructive: true,
			Tools: []string{"xz", "wipefs", "blockdev", "udevadm"}, Run: runStreamWrite},
		{Name: "validate-layout", Weight: 1, Tools: []string{"sfdisk"}, Run: runValidateLayout},
		{Name: "relocate-grow", Weight: 4, Destructive: true,
			Tools: []string{"sfdisk", "udevadm", "blockdev"}, Run: runRelocateGrow},
	}
	if r.Security.Encryption != "none" {
		steps = append(steps, pipeline.Step{Name: "luks-var", Weight: 3, Destructive: true,
			Tools: []string{"cryptsetup"}, Run: runLuksVar})
	}
	steps = append(steps, pipeline.Step{Name: "format-var", Weight: 3, Destructive: true,
		Tools: varFsTools(r.Target.VarFilesystem), Run: runFormatVar})
	if r.Security.Encryption == "tpm2-luks" {
		steps = append(steps, pipeline.Step{Name: "tpm-enroll", Weight: 2,
			Tools: []string{"systemd-cryptenroll", "objcopy", "mount", "umount"}, Run: runTPMEnroll})
	}
	steps = append(steps,
		pipeline.Step{Name: "seed-var", Weight: 4, Tools: []string{"mount", "umount"}, Run: runSeedVar},
		pipeline.Step{Name: "sysconfig", Weight: 6, Run: runSysconfigAB},
		pipeline.Step{Name: "flatpaks", Weight: 20, Tools: flatpakTools(r), Run: runFlatpaksAB},
	)
	if r.Security.Mok == "enroll" {
		steps = append(steps, pipeline.Step{Name: "mok-stage", Weight: 1,
			Tools: []string{"mokutil", "openssl"}, Run: runMOKStage})
	}
	return steps
}

func rootFsTools(fs string) []string {
	switch fs {
	case "btrfs":
		return []string{"mkfs.btrfs", "btrfs"}
	case "xfs":
		return []string{"mkfs.xfs"}
	default:
		return []string{"mkfs.ext4"}
	}
}

func varFsTools(fs string) []string {
	if fs == "btrfs" {
		return []string{"mkfs.btrfs", "btrfs", "mount", "umount"}
	}
	return []string{"mkfs.ext4"}
}
