package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/disk"
	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/runner"
)

// byIDDisk is the recipe target.disk from the lab run that failed
// (run-snow-bootc-lifecycle-lnzgv, firn 0.9.0).
const byIDDisk = "/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root"

const sdaLSBLK = `{"blockdevices": [
  {"path": "/dev/sda", "type": "disk", "size": 500107862016, "fstype": null, "label": null, "mountpoints": [null]}]}`

// byIDRecipe loads an unencrypted bootc recipe whose target.disk is the
// by-id link, and returns the assembled pipeline, the env, and the recorded
// argv and events of a fake runner and emitter.
func byIDRecipe(t *testing.T, link func(string) (string, error)) (*pipeline.Pipeline, *pipeline.Env, *[][]string, *[]progress.Event) {
	t.Helper()
	t.Cleanup(disk.SwapEvalSymlinks(link))
	// WaitForPartition stats the partition node; make it appear at once.
	t.Cleanup(disk.SwapStat(func(string) (os.FileInfo, error) { return nil, nil }))

	src := strings.Replace(strings.Replace(bootcBase, "%s", "none", 1), "/dev/vda", byIDDisk, 1)
	l := load(t, src)

	var argvs [][]string
	fake := runner.NewFake(
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			argvs = append(argvs, append([]string{name}, args...))
			switch name {
			case "lsblk":
				return []byte(sdaLSBLK), nil
			case "findmnt":
				return []byte("overlay\n"), nil
			}
			return nil, nil
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
	var events []progress.Event
	env := &pipeline.Env{
		Recipe: &l.Recipe, Runner: fake, UEFI: true, Version: "test",
		TargetDir: filepath.Join(t.TempDir(), "target"),
		Emitter: progress.EmitterFunc(func(e progress.Event) error {
			events = append(events, e)
			return nil
		}),
	}
	p := &pipeline.Pipeline{Steps: bootcSteps(&l.Recipe)}
	p.Steps = append(preflightSteps(p, &l.Recipe), p.Steps...)
	return p, env, &argvs, &events
}

func stepNamed(t *testing.T, p *pipeline.Pipeline, name string) pipeline.Step {
	t.Helper()
	for _, s := range p.Steps {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("step %s not assembled", name)
	return pipeline.Step{}
}

// TestByIDTargetUsesCanonicalDevice is the regression test for the lab run
// (run-snow-bootc-lifecycle-lnzgv, firn 0.9.0) where a by-id target.disk
// partitioned /dev/sda but then tried to mkfs
// /dev/disk/by-id/scsi-....incus_root1, which does not exist. Every step
// must use the canonical device: preflight resolves it once, the layout
// names /dev/sda1, and the retag retypes /dev/sda's partition 2.
func TestByIDTargetUsesCanonicalDevice(t *testing.T) {
	link := func(path string) (string, error) {
		if path == byIDDisk {
			return "/dev/sda", nil
		}
		return "", fmt.Errorf("lstat %s: %w", path, os.ErrNotExist)
	}
	p, env, argvs, events := byIDRecipe(t, link)
	ctx := context.Background()

	if err := stepNamed(t, p, "preflight-disk").Run(ctx, env); err != nil {
		t.Fatalf("preflight-disk: %v", err)
	}
	// The operator sees the path as written and what it resolved to.
	if len(*events) != 1 {
		t.Fatalf("preflight emitted %d events, want 1: %+v", len(*events), *events)
	}
	if info, ok := (*events)[0].(progress.Info); !ok || !strings.Contains(info.Message, "resolves to /dev/sda") {
		t.Errorf("preflight must report the resolution, got %+v", (*events)[0])
	}
	if env.TargetDisk != "/dev/sda" {
		t.Fatalf("preflight resolved target disk %q, want /dev/sda", env.TargetDisk)
	}
	// The recipe keeps the path the user wrote; only the resolved device
	// drives disk work.
	if env.Recipe.Target.Disk != byIDDisk {
		t.Errorf("recipe target.disk changed to %q", env.Recipe.Target.Disk)
	}

	if err := runPartition(ctx, env); err != nil {
		t.Fatalf("partition: %v", err)
	}
	wantLayout := disk.Layout{Disk: "/dev/sda", ESP: "/dev/sda1", Root: "/dev/sda2"}
	if env.Layout != wantLayout {
		t.Errorf("layout = %+v, want %+v", env.Layout, wantLayout)
	}

	if err := runRetagRoot(ctx, env); err != nil {
		t.Fatalf("retag-root: %v", err)
	}

	joined := make([]string, len(*argvs))
	for i, a := range *argvs {
		joined[i] = strings.Join(a, " ")
	}
	all := strings.Join(joined, "\n")
	for _, want := range []string{
		"sfdisk --wipe=always /dev/sda",
		"sfdisk --part-type /dev/sda 2 " + disk.LinuxRootX8664GUID,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("expected %q in command sequence:\n%s", want, all)
		}
	}
	for _, line := range joined {
		if strings.Contains(line, "/dev/disk/") {
			t.Errorf("a by-id path reached a command: %s", line)
		}
	}
	// Resolved once: preflight's lsblk, and no second lookup in the disk
	// steps.
	if got := strings.Count(all, "lsblk"); got != 1 {
		t.Errorf("lsblk ran %d times, want 1 (resolution cached in env):\n%s", got, all)
	}
}

// TestUnresolvableByIDTargetFailsBeforeDiskWrites pins the fail-closed
// rule: a by-id target that does not resolve stops the install before any
// destructive command, from preflight and from the partition step alike.
func TestUnresolvableByIDTargetFailsBeforeDiskWrites(t *testing.T) {
	link := func(path string) (string, error) {
		return "", fmt.Errorf("lstat %s: %w", path, os.ErrNotExist)
	}
	p, env, argvs, _ := byIDRecipe(t, link)
	ctx := context.Background()

	err := stepNamed(t, p, "preflight-disk").Run(ctx, env)
	if err == nil || !strings.Contains(err.Error(), "cannot be resolved") || !strings.Contains(err.Error(), byIDDisk) {
		t.Fatalf("preflight-disk = %v, want a clear resolution error naming %s", err, byIDDisk)
	}
	if env.TargetDisk != "" {
		t.Errorf("a failed resolution must not record a target disk, got %q", env.TargetDisk)
	}
	assertNoDiskWrite(t, *argvs)

	// The partition step resolves on its own when preflight did not run
	// (step-level tests, and any future caller that skips preflight).
	if err := runPartition(ctx, env); err == nil || !strings.Contains(err.Error(), "cannot be resolved") {
		t.Fatalf("partition = %v, want a clear resolution error", err)
	}
	if env.Layout != (disk.Layout{}) {
		t.Errorf("a failed resolution must leave no layout, got %+v", env.Layout)
	}
	assertNoDiskWrite(t, *argvs)
}

func assertNoDiskWrite(t *testing.T, argvs [][]string) {
	t.Helper()
	for _, a := range argvs {
		switch a[0] {
		case "lsblk", "findmnt":
			continue
		}
		t.Errorf("destructive command ran before resolution succeeded: %v", a)
	}
}
