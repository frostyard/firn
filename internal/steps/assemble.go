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
			Tools: []string{"sfdisk", "wipefs", "partprobe"}},
		{Name: "format-boot", Weight: 3, Destructive: true,
			Tools: []string{"mkfs.fat", "mkfs.ext4"}},
	}
	if encrypted {
		steps = append(steps, pipeline.Step{Name: "luks-format", Weight: 3, Destructive: true,
			Tools: []string{"cryptsetup"}})
	}
	steps = append(steps,
		pipeline.Step{Name: "format-root", Weight: 4, Destructive: true,
			Tools: rootFsTools(r.Target.Filesystem)},
		pipeline.Step{Name: "mount-target", Weight: 1, Destructive: false,
			Tools: []string{"mount", "umount"}},
		pipeline.Step{Name: "bootc-install", Weight: 55, Destructive: true,
			Tools: []string{"podman", "skopeo", "bootc"}},
	)
	if r.Security.Encryption == "tpm2-luks" || r.Security.Encryption == "tpm2-luks-passphrase" {
		// Enrollment is staged for first boot: PCR 7 differs inside the
		// live installer (fisherman's documented incident).
		steps = append(steps, pipeline.Step{Name: "tpm-stage", Weight: 1})
	}
	steps = append(steps,
		pipeline.Step{Name: "flatpaks", Weight: 15, Tools: []string{"flatpak"}},
		pipeline.Step{Name: "sysconfig", Weight: 8, Tools: []string{"useradd", "chpasswd"}},
		pipeline.Step{Name: "finalize", Weight: 5, Tools: []string{"fstrim", "fsfreeze", "efibootmgr"}},
	)
	return steps
}

func abSteps(r *recipe.Recipe) []pipeline.Step {
	steps := []pipeline.Step{
		{Name: "fetch-index", Weight: 2, Tools: []string{"gpgv"}},
		{Name: "stream-write", Weight: 50, Destructive: true,
			Tools: []string{"xz"}},
		{Name: "validate-layout", Weight: 1, Tools: []string{"sfdisk"}},
		{Name: "relocate-grow", Weight: 4, Destructive: true,
			Tools: append([]string{"sfdisk", "udevadm"}, varFsTools(r.Target.VarFilesystem)...)},
	}
	if r.Security.Encryption != "none" {
		steps = append(steps, pipeline.Step{Name: "luks-var", Weight: 3, Destructive: true,
			Tools: []string{"cryptsetup"}})
	}
	steps = append(steps, pipeline.Step{Name: "format-var", Weight: 3, Destructive: true,
		Tools: varFsTools(r.Target.VarFilesystem)})
	if r.Security.Encryption == "tpm2-luks" {
		steps = append(steps, pipeline.Step{Name: "tpm-enroll", Weight: 2,
			Tools: []string{"systemd-cryptenroll", "objcopy", "mount", "umount"}})
	}
	steps = append(steps,
		pipeline.Step{Name: "seed-var", Weight: 4, Tools: []string{"mount", "umount"}},
		pipeline.Step{Name: "sysconfig", Weight: 6},
		pipeline.Step{Name: "flatpaks", Weight: 20, Tools: []string{"flatpak"}},
	)
	if r.Security.Mok == "enroll" {
		steps = append(steps, pipeline.Step{Name: "mok-stage", Weight: 1,
			Tools: []string{"mokutil", "openssl"}})
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
		return []string{"mkfs.btrfs", "btrfs"}
	}
	return []string{"mkfs.ext4", "e2fsck", "resize2fs"}
}
