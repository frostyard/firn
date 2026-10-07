package steps

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/frostyard/firn/internal/bootcimg"
	"github.com/frostyard/firn/internal/disk"
	"github.com/frostyard/firn/internal/flatpak"
	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/platform"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
)

// preflightSteps builds the checks that run before anything
// destructive — in dry-run mode they are the only steps that execute.
// The tool check is derived from p's assembled work steps
// (docs/design/architecture.md, "Preflight").
func preflightSteps(p *pipeline.Pipeline, r *recipe.Recipe) []pipeline.Step {
	steps := []pipeline.Step{
		{
			Name: "preflight-uefi", Weight: 1, Preflight: true,
			Run: func(_ context.Context, env *pipeline.Env) error {
				if err := platform.RequireUEFI(env.UEFI); err != nil {
					return err
				}
				if !env.Machine.TPM {
					if err := env.Emit(progress.Warning{Code: progress.CodeNoTPM, Message: "no TPM device present"}); err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			Name: "preflight-tools", Weight: 1, Preflight: true,
			Tools: []string{"lsblk", "findmnt"},
			Run: func(_ context.Context, env *pipeline.Env) error {
				var missing []string
				for _, t := range p.Tools() {
					if _, err := env.Runner.LookPath(t); err != nil {
						missing = append(missing, t)
					}
				}
				if len(missing) > 0 {
					return fmt.Errorf("required tools not found on PATH: %s", strings.Join(missing, ", "))
				}
				return env.Emit(progress.Info{Message: fmt.Sprintf("all %d required tools present", len(p.Tools()))})
			},
		},
	}
	if r.Image.Family == recipe.FamilyBootc {
		steps = append(steps, pipeline.Step{
			Name: "preflight-image", Weight: 1, Preflight: true,
			Run: func(ctx context.Context, env *pipeline.Env) error {
				source, err := bootcimg.CheckAndPinImageProbed(ctx, env.Runner, env.Recipe.Image.Ref, env.Recipe.Image.CosignPubKey,
					func(msg string) {
						_ = env.Emit(progress.Warning{Code: progress.CodeImageVerifyRetried, Message: msg})
					}, env.RegistryProbe)
				if err != nil {
					// Reachability outranks the blanket verification code: an
					// unreachable registry is not a signature problem.
					var unreachable *bootcimg.RegistryUnreachableError
					if errors.As(err, &unreachable) {
						return pipeline.WithErrorCode(progress.CodeNetworkUnreachable, err)
					}
					if env.Recipe.Image.CosignPubKey != "" {
						return pipeline.WithErrorCode(progress.CodeImageVerifyFailed, err)
					}
					return err
				}
				env.BootcSourceRef = source.Ref
				if env.Recipe.Image.CosignPubKey != "" {
					if err := env.Emit(progress.Info{Message: "bootc image signature verified at immutable digest"}); err != nil {
						return err
					}
				}
				if env.Recipe.System.CoreFlatpaks {
					return readCoreFlatpaks(env, source)
				}
				return nil
			},
		})
	}
	steps = append(steps, pipeline.Step{
		Name: "preflight-disk", Weight: 1, Preflight: true,
		Run: func(ctx context.Context, env *pipeline.Env) error {
			devices, err := disk.List(ctx, env.Runner)
			if err != nil {
				return err
			}
			target := env.Recipe.Target.Disk
			dev, ok := disk.Find(devices, target)
			if !ok {
				return fmt.Errorf("target disk %s not found (whole disks present: %s)", target, diskPaths(devices))
			}
			if reason := disk.RefusalReason(dev, disk.RootDevice(ctx, env.Runner)); reason != "" {
				return fmt.Errorf("refusing to install to %s: %s", target, reason)
			}
			return env.Emit(progress.Info{Message: fmt.Sprintf("target disk %s acceptable (%d bytes)", dev.Path, dev.Size)})
		},
	})
	return steps
}

func diskPaths(devices []disk.Device) string {
	if len(devices) == 0 {
		return "none"
	}
	paths := make([]string, len(devices))
	for i, d := range devices {
		paths[i] = d.Path
	}
	return strings.Join(paths, ", ")
}

// readCoreFlatpaks parses the selected image's core Flatpak label before any
// disk write (ADR-0018): a malformed label fails the install here, and an
// image that publishes no set is reported, not fatal.
func readCoreFlatpaks(env *pipeline.Env, source bootcimg.Source) error {
	if !source.Inspected {
		// Unknown labels are not an absent label: installing without the
		// requested set, or skipping its validation, would be silent.
		return pipeline.WithErrorCode(progress.CodeCoreLabelUnreadable,
			fmt.Errorf("core_flatpaks: could not inspect image %s to read its %s label; check registry access and retry",
				env.Recipe.Image.Ref, flatpak.CoreLabel))
	}
	value, present := source.Labels[flatpak.CoreLabel]
	ids, err := flatpak.ParseCoreLabel(value, present)
	if err != nil {
		return pipeline.WithErrorCode(progress.CodeCoreLabelInvalid,
			fmt.Errorf("core_flatpaks: image %s: %w", env.Recipe.Image.Ref, err))
	}
	if len(ids) == 0 {
		msg := fmt.Sprintf("core_flatpaks: image %s publishes no core Flatpak set", env.Recipe.Image.Ref)
		if err := env.Emit(progress.Warning{Code: progress.CodeNoCoreSet, Message: msg}); err != nil {
			return err
		}
		env.AddSummary(progress.CodeNoCoreSet, msg)
		return nil
	}
	env.CoreFlatpaks = ids
	return env.Emit(progress.Info{Message: fmt.Sprintf("core_flatpaks: image publishes %d core Flatpaks", len(ids))})
}
