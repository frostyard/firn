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
	p.Steps = append(preflightSteps(p), work...)
	return p
}

func bootcSteps(r *recipe.Recipe) []pipeline.Step {
	encrypted := r.Security.Encryption != "none"
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
			Tools: []string{"podman", "skopeo"}, Run: runBootcInstall},
	)
	if r.Security.Encryption == "tpm2-luks" || r.Security.Encryption == "tpm2-luks-passphrase" {
		// Enrollment is staged for first boot: PCR 7 differs inside the
		// live installer (fisherman's documented incident).
		steps = append(steps, pipeline.Step{Name: "tpm-stage", Weight: 1, Run: runTPMStage})
	}
	steps = append(steps,
		pipeline.Step{Name: "flatpaks", Weight: 15, Tools: flatpakTools(r), Run: runFlatpaks},
		pipeline.Step{Name: "sysconfig", Weight: 8, Tools: []string{"useradd", "chpasswd", "chroot"}, Run: runSysconfig},
		pipeline.Step{Name: "finalize", Weight: 5, Tools: []string{"fstrim", "fsfreeze"}, Run: runFinalize},
	)
	return steps
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
	case "zfs":
		return []string{"zpool", "zfs"}
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
