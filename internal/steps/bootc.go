package steps

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/frostyard/firn/internal/bootcimg"
	"github.com/frostyard/firn/internal/disk"
	"github.com/frostyard/firn/internal/flatpak"
	"github.com/frostyard/firn/internal/luks"
	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/sysconfig"
)

// DefaultTargetDir is where the target tree is mounted; tests and the
// CLI set Env.TargetDir (steps fall back to this).
const DefaultTargetDir = "/mnt/firn-target"

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
		return fmt.Errorf("zfs root is not implemented yet (fisherman parity gap, tracked in docs/plans/roadmap.md)")
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
	if err := bootcimg.CheckImage(ctx, env.Runner, r.Image.Ref); err != nil {
		return err
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
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return fmt.Errorf("creating scratch dir: %w", err)
	}
	return bootcimg.Install(ctx, env.Runner, bootcimg.Options{
		Image:        r.Image.Ref,
		TargetImgref: r.Image.TargetRef,
		TargetDir:    targetDir(env),
		Bootloader:   bootloader,
		ScratchDir:   scratch,
		// Always on: firn installs snosi images, and all snosi bootc
		// images use the composefs-native backend. Becomes a recipe
		// field only if a non-composefs snosi image ever exists.
		Composefs: true,
	})
}

func runTPMStage(ctx context.Context, env *pipeline.Env) error {
	uuid := luks.UUID(ctx, env.Runner, env.Layout.Root)
	etcDir, err := sysconfig.EtcDir(ctx, env.Runner, targetDir(env))
	if err != nil {
		return err
	}
	return luks.StageFirstBootEnrollment(etcDir, uuid, env.LuksKey)
}

func runFlatpaks(ctx context.Context, env *pipeline.Env) error {
	apps := env.Recipe.System.Flatpaks
	if env.Recipe.System.CoreFlatpaks {
		env.Emit(progress.Warning{Code: "not_implemented",
			Message: "core_flatpaks: image-defined core set resolution arrives with the A/B catalog work (roadmap phase 4/5)"})
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
	// Locale/timezone/keyboard on the bootc path arrive in phase 5
	// (docs/plans/roadmap.md); the recipe accepts them today, so be
	// loud rather than silent.
	for field, val := range map[string]string{"locale": sys.Locale, "timezone": sys.Timezone, "keyboard": sys.Keyboard} {
		if val != "" {
			msg := field + " is not applied on the bootc path yet (roadmap phase 5)"
			env.Emit(progress.Warning{Code: "not_implemented", Message: msg})
			env.AddSummary("not_implemented", msg)
		}
	}
	return nil
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
