package steps

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/platform"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/runner"
)

func load(t *testing.T, src string) *recipe.Loaded {
	t.Helper()
	l, err := recipe.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func names(p *pipeline.Pipeline) []string {
	out := make([]string, len(p.Steps))
	for i, s := range p.Steps {
		out[i] = s.Name
	}
	return out
}

const bootcBase = `
version = 1
[image]
family = "bootc"
ref = "ghcr.io/frostyard/snow:latest"
[target]
disk = "/dev/vda"
filesystem = "btrfs"
[security]
encryption = "%s"
[system]
hostname = "h"
`

const abBase = `
version = 1
[image]
family = "ab"
product = "snow-ab"
[target]
disk = "/dev/vda"
[security]
encryption = "%s"
[system]
hostname = "h"
`

func TestAssemblySplicesByOptions(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		want    []string
		notWant []string
	}{
		{"bootc unencrypted", strings.Replace(bootcBase, "%s", "none", 1),
			[]string{"preflight-uefi", "preflight-tools", "preflight-disk", "partition", "bootc-install", "retag-root", "finalize"},
			[]string{"luks-format", "tpm-enroll", "stream-write", "esp-stage", "mok-stage"}},
		{"bootc tpm2", strings.Replace(bootcBase, "%s", "tpm2-luks", 1),
			[]string{"luks-format", "tpm-enroll", "retag-root"},
			[]string{"stream-write", "tpm-stage", "esp-stage", "mok-stage"}},
		{"bootc secure boot (mok enroll)", strings.Replace(strings.Replace(bootcBase, "%s", "tpm2-luks", 1),
			`encryption = "tpm2-luks"`, "encryption = \"tpm2-luks\"\nmok = \"enroll\"\nmok_password_file = \"/x\"", 1),
			[]string{"bootc-install", "tpm-enroll", "esp-stage", "retag-root", "mok-stage"},
			[]string{"stream-write"}},
		{"ab plain", strings.Replace(abBase, "%s", "none", 1),
			[]string{"preflight-uefi", "fetch-index", "stream-write", "format-var", "seed-var"},
			[]string{"luks-var", "tpm-enroll", "mok-stage", "partition", "bootc-install"}},
		{"ab tpm2", strings.Replace(abBase, "%s", "tpm2-luks", 1),
			[]string{"luks-var", "tpm-enroll"},
			nil},
		{"ab mok", strings.Replace(strings.Replace(abBase, "%s", "tpm2-luks", 1),
			`encryption = "tpm2-luks"`, "encryption = \"tpm2-luks\"\nmok = \"enroll\"\nmok_password_file = \"/x\"", 1),
			[]string{"mok-stage"},
			nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := names(Assemble(load(t, tc.src)))
			for _, w := range tc.want {
				if !slices.Contains(got, w) {
					t.Errorf("missing step %q in %v", w, got)
				}
			}
			for _, nw := range tc.notWant {
				if slices.Contains(got, nw) {
					t.Errorf("unexpected step %q in %v", nw, got)
				}
			}
		})
	}
}

func TestPreflightStepsComeFirstAndDeclareTools(t *testing.T) {
	p := Assemble(load(t, strings.Replace(bootcBase, "%s", "none", 1)))
	for i, s := range p.Steps[:3] {
		if !s.Preflight {
			t.Errorf("step %d (%s) should be preflight", i, s.Name)
		}
	}
	tools := p.Tools()
	for _, want := range []string{"lsblk", "sfdisk", "podman"} {
		if !slices.Contains(tools, want) {
			t.Errorf("tool union missing %q: %v", want, tools)
		}
	}
	// No flatpaks in the recipe → the flatpak binary must NOT be
	// demanded by preflight (server images / flatpak-less installers).
	if slices.Contains(tools, "flatpak") {
		t.Errorf("flatpak demanded without any flatpaks in the recipe: %v", tools)
	}
	withFlatpaks := strings.Replace(strings.Replace(bootcBase, "%s", "none", 1),
		`hostname = "h"`, "hostname = \"h\"\nflatpaks = [\"org.gnome.Calculator\"]", 1)
	if !slices.Contains(Assemble(load(t, withFlatpaks)).Tools(), "flatpak") {
		t.Error("flatpak tool must be demanded when the recipe requests flatpaks")
	}
}

func TestVarFilesystemChoiceChangesTools(t *testing.T) {
	src := strings.Replace(abBase, "[security]", "var_filesystem = \"btrfs\"\nvar_subvolumes = true\n[security]", 1)
	p := Assemble(load(t, strings.Replace(src, "%s", "none", 1)))
	if !slices.Contains(p.Tools(), "mkfs.btrfs") {
		t.Errorf("btrfs var should require mkfs.btrfs: %v", p.Tools())
	}
	if slices.Contains(p.Tools(), "resize2fs") {
		t.Errorf("btrfs var should not require resize2fs: %v", p.Tools())
	}
}

// End-to-end dry run against fakes: BIOS refused; busy disk refused;
// clean disk passes.
func TestDryRunVerdicts(t *testing.T) {
	const lsblkJSON = `{"blockdevices": [
	  {"path": "/dev/vda", "type": "disk", "size": 64000000000, "fstype": null, "label": null, "mountpoints": [null]},
	  {"path": "/dev/vdb", "type": "disk", "size": 64000000000, "fstype": null, "label": null,
	   "mountpoints": [null], "children": [
	     {"path": "/dev/vdb1", "type": "part", "size": 100, "fstype": "ext4", "label": null, "mountpoints": ["/mnt"]}]}
	]}`
	fake := runner.NewFake(
		func(_ context.Context, name string, _ ...string) ([]byte, error) {
			switch name {
			case "lsblk":
				return []byte(lsblkJSON), nil
			case "findmnt":
				return []byte("overlay\n"), nil
			}
			return nil, errors.New("unexpected " + name)
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)

	run := func(disk string, uefi bool) error {
		src := strings.Replace(strings.Replace(bootcBase, "%s", "none", 1), "/dev/vda", disk, 1)
		l := load(t, src)
		env := &pipeline.Env{
			Recipe: &l.Recipe, Runner: fake, UEFI: uefi, Version: "test",
			Emit: func(progress.Event) {},
		}
		return Assemble(l).Run(context.Background(), env, true)
	}

	if err := run("/dev/vda", true); err != nil {
		t.Errorf("clean disk on UEFI should pass dry run: %v", err)
	}
	if err := run("/dev/vda", false); !errors.Is(err, platform.ErrUEFIRequired) {
		t.Errorf("BIOS machine must be refused with the shared UEFI diagnostic, got %v", err)
	}
	if err := run("/dev/vdb", true); err == nil || !strings.Contains(err.Error(), "mounted") {
		t.Errorf("busy disk must be refused, got %v", err)
	}
	if err := run("/dev/vdz", true); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("absent disk must be refused, got %v", err)
	}
}

func TestMissingToolsFailPreflight(t *testing.T) {
	fake := runner.NewFake(
		func(_ context.Context, name string, _ ...string) ([]byte, error) {
			return nil, errors.New("unexpected " + name)
		},
		func(name string) (string, error) {
			if name == "sfdisk" {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + name, nil
		},
	)
	l := load(t, strings.Replace(bootcBase, "%s", "none", 1))
	env := &pipeline.Env{Recipe: &l.Recipe, Runner: fake, UEFI: true, Version: "test", Emit: func(progress.Event) {}}
	err := Assemble(l).Run(context.Background(), env, true)
	if err == nil || !strings.Contains(err.Error(), "sfdisk") {
		t.Errorf("missing sfdisk must fail the tool preflight, got %v", err)
	}
}
