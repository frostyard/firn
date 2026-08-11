package steps

import (
	"context"
	"fmt"
	"strings"

	"github.com/frostyard/firn/internal/disk"
	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/progress"
)

// preflightSteps builds the checks that run before anything
// destructive — in dry-run mode they are the only steps that execute.
// The tool check is derived from p's assembled work steps
// (docs/design/architecture.md, "Preflight").
func preflightSteps(p *pipeline.Pipeline) []pipeline.Step {
	return []pipeline.Step{
		{
			Name: "preflight-uefi", Weight: 1, Preflight: true,
			Run: func(_ context.Context, env *pipeline.Env) error {
				if !env.UEFI {
					return fmt.Errorf("this machine did not boot via UEFI; firn supports UEFI machines only (docs/adr/0004-single-installer-scope-and-support-matrix.md)")
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
		{
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
		},
	}
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
