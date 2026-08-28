package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/frostyard/firn/internal/abimg"
	"github.com/frostyard/firn/internal/flatpak"
	"github.com/frostyard/firn/internal/luks"
	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/sysconfig"
	"github.com/frostyard/firn/internal/trust"
)

const varMapper = "firn-var-install"

// DefaultMOKCert is where snosi installer media ship the MOK
// certificate staged for enrollment.
const DefaultMOKCert = "/usr/lib/snosi/mok.crt"

func runFetchIndex(ctx context.Context, env *pipeline.Env) error {
	r := env.Recipe
	env.Trust.Product = r.Image.Product
	if r.Image.Origin != "" {
		env.Trust.Origin = r.Image.Origin
	}

	idx, err := trust.FetchIndex(ctx, env.Runner, env.Trust)
	if err != nil {
		return err
	}
	env.ABIndex = idx

	version := r.Image.Release
	if version == "" {
		if version, err = idx.LatestVersion(r.Image.Product); err != nil {
			return err
		}
	} else if _, ok := idx.Sha256(r.Image.Product + "_" + version + ".manifest.json"); !ok {
		return fmt.Errorf("pinned release %s not present in the signed index", version)
	}
	env.ABVersion = version
	if err := env.Emit(progress.Info{Message: fmt.Sprintf("installing %s release %s", r.Image.Product, version)}); err != nil {
		return err
	}

	// Capacity check BEFORE the multi-gigabyte download — derived from
	// the artifact's own xz metadata, not a hand-copied table
	// (docs/design/architecture.md, Preflight).
	need, err := trust.MinimumDiskBytes(ctx, env.Trust, idx, r.Image.Product, version)
	if err != nil {
		return err
	}
	out, err := env.Runner.Run(ctx, "blockdev", "--getsize64", r.Target.Disk)
	if err != nil {
		return err
	}
	have, err := strconv.ParseInt(string(trimSpaceBytes(out)), 10, 64)
	if err != nil {
		return fmt.Errorf("parsing disk size: %w", err)
	}
	if have < need {
		return fmt.Errorf("disk %s is too small: %d bytes available, %d required for %s %s",
			r.Target.Disk, have, need, r.Image.Product, version)
	}
	return nil
}

func trimSpaceBytes(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}

func runStreamWrite(ctx context.Context, env *pipeline.Env) error {
	r := env.Recipe
	name := r.Image.Product + "_" + env.ABVersion + ".disk.raw.xz"
	sum, ok := env.ABIndex.Sha256(name)
	if !ok {
		return fmt.Errorf("artifact %s not present in the signed index", name)
	}
	step := env.CurrentStep
	size, err := abimg.StreamWrite(ctx, env.Runner, abimg.StreamOpts{
		URL:    env.ABIndex.ArtifactURL(env.Trust, name),
		Sha256: sum,
		Disk:   r.Target.Disk,
		Client: env.Trust.Client,
		Progress: func(bytes, total int64) {
			ev := progress.StepProgress{Index: step, Bytes: bytes, TotalBytes: total}
			if total > 0 {
				ev.Fraction = float64(bytes) / float64(total)
			}
			_ = env.Emit(ev)
		},
	})
	if err != nil {
		return err
	}
	env.ABSize = size
	return abimg.RereadptSettle(ctx, env.Runner, r.Target.Disk)
}

func runValidateLayout(ctx context.Context, env *pipeline.Env) error {
	return abimg.ValidateLayout(ctx, env.Runner, env.Recipe.Target.Disk)
}

func runRelocateGrow(ctx context.Context, env *pipeline.Env) error {
	varPart, err := abimg.RelocateAndGrowVar(ctx, env.Runner,
		env.Recipe.Target.Disk, env.ABSize)
	if err != nil {
		return err
	}
	env.VarPart = varPart
	env.VarDev = varPart
	return nil
}

func varFilesystem(r *recipe.Recipe) string {
	if r.Target.VarFilesystem != "" {
		return r.Target.VarFilesystem
	}
	return "ext4"
}

func runLuksVar(ctx context.Context, env *pipeline.Env) error {
	key := env.LuksKey
	if key == "" {
		var err error
		key, err = luks.GenerateRecoveryKey()
		if err != nil {
			return err
		}
		env.LuksKey = key
	}
	if err := env.Emit(progress.RecoveryKey{Key: key}); err != nil {
		return err
	}
	if out := env.Recipe.Security.RecoveryKeyOut; out != "" {
		// No trailing newline: the hex string is byte-exactly the
		// passphrase (snosi-install's printf '%s' rule).
		if out != env.RecoveryKeyOut {
			return fmt.Errorf("recovery key output %s was not reserved by preflight", out)
		}
		if env.RecoveryKeyTemp == "" {
			return fmt.Errorf("recovery key output %s has no staged key from preflight", out)
		}
		if err := commitRecoveryKey(out, env.RecoveryKeyTemp); err != nil {
			return fmt.Errorf("writing recovery key file: %w", err)
		}
		env.RecoveryKeyTemp = ""
		env.RecoveryKeyWritten = true
	}
	if err := luks.Format(ctx, env.Runner, env.VarPart, key); err != nil {
		return err
	}
	mapperPath, err := luks.Open(ctx, env.Runner, env.VarPart, varMapper, key)
	if err != nil {
		return err
	}
	env.VarDev = mapperPath
	env.Defer("close var LUKS mapper", func() error {
		return luks.Close(context.Background(), env.Runner, varMapper)
	})
	return nil
}

func stageRecoveryKey(path, key string) (string, error) {
	// Staging in the destination directory makes the later rename atomic and
	// allocation-free after destructive work begins.
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return "", err
	}
	if _, err := tmp.WriteString(key); err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	keep = true
	return tmpPath, nil
}

func commitRecoveryKey(path, tmpPath string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("checking reserved output: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != 0 {
		return fmt.Errorf("reserved output changed before key write")
	}
	tmpInfo, err := os.Stat(tmpPath)
	if err != nil {
		return fmt.Errorf("checking staged key: %w", err)
	}
	if !tmpInfo.Mode().IsRegular() || tmpInfo.Mode().Perm() != 0o600 || tmpInfo.Size() != 64 || filepath.Dir(tmpPath) != filepath.Dir(path) {
		return fmt.Errorf("staged recovery key changed before commit")
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func runFormatVar(ctx context.Context, env *pipeline.Env) error {
	r := env.Recipe
	return abimg.FormatVar(ctx, env.Runner, env.VarDev, varFilesystem(r), r.Target.VarSubvolumes)
}

func runTPMEnroll(ctx context.Context, env *pipeline.Env) error {
	espPart, err := abimg.ESPPartition(ctx, env.Runner, env.Recipe.Target.Disk)
	if err != nil {
		return err
	}
	espMount, err := mountRO(ctx, env, espPart, "esp")
	if err != nil {
		return err
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

	// Enroll against the LUKS PARTITION (VarPart), which holds the
	// LUKS2 superblock the token is written into — NOT VarDev, the
	// opened mapper (a plaintext view with no LUKS header:
	// systemd-cryptenroll fails "Failed to load LUKS2 superblock",
	// caught only by the on-hardware ISO E2E; the argv unit test used a
	// fake device and could not see the mix-up).
	return abimg.EnrollTPMVar(ctx, env.Runner, espMount,
		env.Recipe.Image.Product, env.ABVersion, env.VarPart, keyFile.Name())
}

// mountRO mounts dev read-only at a scratch dir and registers cleanup.
func mountRO(ctx context.Context, env *pipeline.Env, dev, tag string) (string, error) {
	dir, err := os.MkdirTemp("", "firn-"+tag+"-")
	if err != nil {
		return "", err
	}
	if _, err := env.Runner.Run(ctx, "mount", "-o", "ro", dev, dir); err != nil {
		// No mount was registered, so nothing will unwind this
		// scratch dir later; drop it here. os.Remove (not
		// RemoveAll) keeps the failure path incapable of
		// deleting anything a partially-succeeded mount exposed.
		_ = os.Remove(dir)
		return "", err
	}
	env.Defer("unmount "+tag, func() error {
		if _, err := env.Runner.Run(context.Background(), "umount", dir); err != nil {
			return err
		}
		// The scratch mountpoint dir is cosmetic; only the umount
		// result decides success.
		_ = os.Remove(dir)
		return nil
	})
	return dir, nil
}

func runSeedVar(ctx context.Context, env *pipeline.Env) error {
	varMount, err := os.MkdirTemp("", "firn-var-")
	if err != nil {
		return err
	}
	if _, err := env.Runner.Run(ctx, "mount", env.VarDev, varMount); err != nil {
		return err
	}
	env.Defer("unmount var", func() error {
		if _, err := env.Runner.Run(context.Background(), "umount", varMount); err != nil {
			return err
		}
		_ = os.Remove(varMount)
		return nil
	})

	rootPart, err := abimg.RootPartition(ctx, env.Runner, env.Recipe.Target.Disk)
	if err != nil {
		return err
	}
	rootMount, err := mountRO(ctx, env, rootPart, "root")
	if err != nil {
		return err
	}

	env.VarMount = varMount
	env.RootMount = rootMount

	w := &sysconfig.OverlayWriter{VarDir: varMount, RootDir: rootMount, Runner: env.Runner}
	return w.WriteInstallInfo(env.Recipe.Image.Product, env.ABVersion)
}

func runSysconfigAB(ctx context.Context, env *pipeline.Env) error {
	sys := env.Recipe.System
	w := &sysconfig.OverlayWriter{VarDir: env.VarMount, RootDir: env.RootMount, Runner: env.Runner}

	if err := w.WriteHostname(sys.Hostname); err != nil {
		return err
	}
	if err := w.WriteLocale(sys.Locale); err != nil {
		return err
	}
	if err := w.WriteTimezone(sys.Timezone); err != nil {
		return err
	}
	if err := w.WriteKeyboard(sys.Keyboard); err != nil {
		return err
	}
	if key, err := resolveKey(sys.RootSSHAuthorizedKey, sys.RootSSHAuthorizedKeyFile); err != nil {
		return err
	} else if key != "" {
		if err := w.WriteRootAuthorizedKey(key); err != nil {
			return err
		}
	}
	if sys.User != nil {
		missing, err := w.CreateUser(*sys.User)
		if err != nil {
			return err
		}
		for _, g := range missing {
			msg := fmt.Sprintf("group %q does not exist in the image; user %s not joined to it", g, sys.User.Name)
			if err := env.Emit(progress.Warning{Code: progress.CodeGroupMissing, Message: msg}); err != nil {
				return err
			}
			env.AddSummary(progress.CodeGroupMissing, msg)
		}
		if key, err := resolveKey(sys.User.SSHAuthorizedKey, sys.User.SSHAuthorizedKeyFile); err != nil {
			return err
		} else if key != "" {
			if err := w.WriteUserAuthorizedKey(sys.User.Name, key); err != nil {
				return err
			}
		}
	}
	return nil
}

func runFlatpaksAB(ctx context.Context, env *pipeline.Env) error {
	apps := env.Recipe.System.Flatpaks
	if env.Recipe.System.CoreFlatpaks {
		// The A/B erofs root is fully materialized: the image-defined
		// core set is readable directly (unlike composefs bootc
		// targets).
		core, ok, err := flatpak.CoreSet(env.RootMount)
		if err != nil {
			return err
		}
		if !ok {
			msg := "core_flatpaks: this image publishes no core set"
			if err := env.Emit(progress.Warning{Code: progress.CodeNoCoreSet, Message: msg}); err != nil {
				return err
			}
			env.AddSummary(progress.CodeNoCoreSet, msg)
		}
		apps = append(apps, core...)
	}
	if len(apps) == 0 {
		return nil
	}
	// The var FILESYSTEM is mounted (its runtime path is /var), so the
	// flatpak installation lives at <varMount>/lib/flatpak.
	unreachable, err := flatpak.Provision(ctx, env.Runner, flatpak.Opts{
		TargetDir:       env.VarMount,
		InstallationDir: filepath.Join(env.VarMount, "lib", "flatpak"),
		Apps:            apps,
	})
	if err != nil {
		return err
	}
	for _, app := range unreachable {
		if err := env.Emit(progress.Warning{Code: progress.CodeFlatpakUnreachable, Message: app}); err != nil {
			return err
		}
		env.AddSummary(progress.CodeFlatpakUnreachable, app)
	}
	return nil
}

func runMOKStage(ctx context.Context, env *pipeline.Env) error {
	cert := DefaultMOKCert
	if _, err := os.Stat(cert); err != nil {
		return fmt.Errorf("MOK certificate not found at %s (installer media ship it there): %w", cert, err)
	}
	return abimg.StageMOK(ctx, env.Runner, cert, env.Recipe.Security.MokPasswordFile)
}
