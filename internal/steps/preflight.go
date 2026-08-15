package steps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/frostyard/firn/internal/bootcimg"
	"github.com/frostyard/firn/internal/disk"
	"github.com/frostyard/firn/internal/luks"
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
					env.Emit(progress.Warning{Code: progress.CodeNoTPM, Message: "no TPM device present"})
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
				env.Emit(progress.Info{Message: fmt.Sprintf("all %d required tools present", len(p.Tools()))})
				return nil
			},
		},
	}
	if r.Image.Family == recipe.FamilyBootc {
		steps = append(steps, pipeline.Step{
			Name: "preflight-image", Weight: 1, Preflight: true,
			Run: func(ctx context.Context, env *pipeline.Env) error {
				pinned, err := bootcimg.CheckAndPinImage(ctx, env.Runner, env.Recipe.Image.Ref, env.Recipe.Image.CosignPubKey)
				if err != nil {
					if env.Recipe.Image.CosignPubKey != "" {
						return pipeline.WithErrorCode(progress.CodeImageVerifyFailed, err)
					}
					return err
				}
				env.BootcSourceRef = pinned
				if env.Recipe.Image.CosignPubKey != "" {
					env.Emit(progress.Info{Message: "bootc image signature verified at immutable digest"})
				}
				return nil
			},
		})
	}
	if r.Image.Family == recipe.FamilyAB && r.Security.Encryption != "none" && r.Security.RecoveryKeyOut != "" {
		steps = append(steps, pipeline.Step{
			Name: "preflight-recovery-key", Weight: 1, Preflight: true,
			Run: runPrepareRecoveryKeyOut,
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
			env.Emit(progress.Info{Message: fmt.Sprintf("target disk %s acceptable (%d bytes)", dev.Path, dev.Size)})
			return nil
		},
	})
	return steps
}

func runPrepareRecoveryKeyOut(_ context.Context, env *pipeline.Env) error {
	// snosi-install refuses an existing recovery-key path before streaming the
	// disk image. Firn additionally creates the exclusive placeholder and
	// fsyncs the complete key to a same-directory staging file here, closing
	// the parent's late create/write failure window while preserving its
	// refuse-to-overwrite policy.
	path := env.Recipe.Security.RecoveryKeyOut
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("recovery key output already exists (refusing to overwrite): %s", path)
		}
		return fmt.Errorf("reserve recovery key output %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("reserve recovery key output %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("secure recovery key output %s: %w", path, err)
	}
	key, err := luks.GenerateRecoveryKey()
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	tmp, err := stageRecoveryKey(path, key)
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("stage recovery key output %s: %w", path, err)
	}
	env.LuksKey = key
	env.RecoveryKeyOut = path
	env.RecoveryKeyTemp = tmp
	env.Defer("remove unused recovery key reservation", func() error {
		if env.RecoveryKeyTemp != "" {
			_ = os.Remove(env.RecoveryKeyTemp)
		}
		if env.RecoveryKeyWritten {
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	})
	return nil
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
