package steps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/flatpak"
	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/runner"
)

// coreLabelFake answers preflight for a clean UEFI machine whose image
// carries labels; every command is recorded in commands.
func coreLabelFake(t *testing.T, labels map[string]string, commands *[]string) *runner.Runner {
	return coreLabelFakeWith(t, labels, commands, nil)
}

// coreLabelFakeWith is coreLabelFake with an optional override for skopeo.
func coreLabelFakeWith(t *testing.T, labels map[string]string, commands *[]string,
	skopeo func(args []string) ([]byte, error)) *runner.Runner {
	t.Helper()
	const lsblkJSON = `{"blockdevices": [
	  {"path": "/dev/vda", "type": "disk", "size": 64000000000, "fstype": null, "label": null, "mountpoints": [null]}]}`
	inspect := `{"Digest":"` + verifiedBootcDigest + `","Labels":{"containers.bootc":"1"`
	for k, v := range labels {
		inspect += `,` + jsonString(k) + `:` + jsonString(v)
	}
	inspect += `}}`
	return runner.NewFake(
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			*commands = append(*commands, strings.Join(append([]string{name}, args...), " "))
			switch name {
			case "lsblk":
				return []byte(lsblkJSON), nil
			case "findmnt":
				return []byte("overlay\n"), nil
			case "skopeo":
				if skopeo != nil {
					return skopeo(args)
				}
				return []byte(inspect), nil
			}
			return []byte(""), nil
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
}

func jsonString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func coreRecipe(t *testing.T, coreFlatpaks bool) *recipe.Loaded {
	t.Helper()
	src := strings.Replace(bootcBase, "%s", "none", 1)
	if coreFlatpaks {
		src += "core_flatpaks = true\n"
	}
	return load(t, src)
}

func runCore(t *testing.T, l *recipe.Loaded, r *runner.Runner, dryRun bool) (*pipeline.Env, []progress.Event, error) {
	t.Helper()
	var events []progress.Event
	env := &pipeline.Env{
		Recipe: &l.Recipe, Runner: r, UEFI: true, Version: "test",
		TargetDir:  filepath.Join(t.TempDir(), "target"),
		ScratchDir: filepath.Join(t.TempDir(), "scratch"),
		Emitter: progress.EmitterFunc(func(e progress.Event) error {
			events = append(events, e)
			return nil
		}),
	}
	err := Assemble(l).Run(context.Background(), env, dryRun)
	return env, events, err
}

// A malformed label with core_flatpaks requested stops the install in
// preflight-image, before any disk write, in both dry-run and real runs
// (ADR-0018).
func TestCoreFlatpaksMalformedLabelFailsPreflight(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		var commands []string
		r := coreLabelFake(t, map[string]string{flatpak.CoreLabel: `{"version":2,"flatpaks":[]}`}, &commands)
		_, events, err := runCore(t, coreRecipe(t, true), r, dryRun)
		if !errors.Is(err, flatpak.ErrMalformedCoreLabel) {
			t.Fatalf("dryRun=%v: error = %v, want a malformed-label error", dryRun, err)
		}
		terminal, ok := events[len(events)-1].(progress.Error)
		if !ok || terminal.Step != "preflight-image" || terminal.Code != progress.CodeCoreLabelInvalid {
			t.Fatalf("dryRun=%v: terminal event = %#v", dryRun, events[len(events)-1])
		}
		for _, command := range commands {
			for _, destructive := range []string{"sfdisk", "wipefs", "mkfs", "cryptsetup", "bootc", "podman"} {
				if strings.HasPrefix(command, destructive+" ") {
					t.Fatalf("dryRun=%v: %s ran after a malformed label", dryRun, command)
				}
			}
		}
	}
}

// Without core_flatpaks the label is never parsed, so a malformed one
// cannot block the install.
func TestCoreFlatpaksLabelIgnoredWhenNotRequested(t *testing.T) {
	var commands []string
	r := coreLabelFake(t, map[string]string{flatpak.CoreLabel: "not json"}, &commands)
	env, _, err := runCore(t, coreRecipe(t, false), r, true)
	if err != nil {
		t.Fatalf("dry run = %v, want success with core_flatpaks off", err)
	}
	if env.CoreFlatpaks != nil {
		t.Fatalf("CoreFlatpaks = %v, want none", env.CoreFlatpaks)
	}
}

// An image that publishes no core set is reported in preflight and the
// install continues.
func TestCoreFlatpaksAbsentLabelWarns(t *testing.T) {
	for name, labels := range map[string]map[string]string{
		"no label":    nil,
		"empty array": {flatpak.CoreLabel: `{"version":1,"flatpaks":[]}`},
	} {
		var commands []string
		env, events, err := runCore(t, coreRecipe(t, true), coreLabelFake(t, labels, &commands), true)
		if err != nil {
			t.Fatalf("%s: dry run = %v, want success", name, err)
		}
		var warned bool
		for _, e := range events {
			if w, ok := e.(progress.Warning); ok && w.Code == progress.CodeNoCoreSet {
				warned = true
			}
		}
		if !warned {
			t.Fatalf("%s: no %s warning in %v", name, progress.CodeNoCoreSet, events)
		}
		if len(env.Summary) != 1 || env.Summary[0].Code != progress.CodeNoCoreSet {
			t.Fatalf("%s: summary = %v", name, env.Summary)
		}
		if env.CoreFlatpaks != nil {
			t.Fatalf("%s: CoreFlatpaks = %v, want none", name, env.CoreFlatpaks)
		}
	}
}

func TestCoreFlatpaksValidLabelIsKeptForTheFlatpakStep(t *testing.T) {
	var commands []string
	label := `{"version":1,"flatpaks":[{"id":"org.kde.okular","name":"Okular"},{"id":"org.kde.kcalc","name":"KCalc"}]}`
	env, _, err := runCore(t, coreRecipe(t, true), coreLabelFake(t, map[string]string{flatpak.CoreLabel: label}, &commands), true)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"org.kde.okular", "org.kde.kcalc"}; !slices.Equal(env.CoreFlatpaks, want) {
		t.Fatalf("CoreFlatpaks = %v, want %v", env.CoreFlatpaks, want)
	}
	if len(env.Summary) != 0 {
		t.Fatalf("summary = %v, want none", env.Summary)
	}
}

// runFlatpaks installs explicit apps first, then the core set, each ID
// once at its first occurrence.
func TestRunFlatpaksMergesExplicitThenCore(t *testing.T) {
	target := t.TempDir()
	var installed []string
	r := runner.NewFake(
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			switch name {
			case "ls":
				return nil, errors.New("no such file") // plain var/lib/flatpak target
			case "du":
				return []byte("0\t/x\n"), nil
			case "env":
				if i := slices.Index(args, "install"); i >= 0 {
					installed = append(installed, args[len(args)-1])
				}
			}
			return []byte(""), nil
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
	l := load(t, strings.Replace(bootcBase, "%s", "none", 1)+
		`flatpaks = ["org.mozilla.firefox", "org.kde.okular", "org.mozilla.firefox"]`+"\n")
	env := &pipeline.Env{
		Recipe: &l.Recipe, Runner: r, TargetDir: target,
		CoreFlatpaks: []string{"org.kde.okular", "org.kde.kcalc"},
		Emitter:      progress.EmitterFunc(func(progress.Event) error { return nil }),
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runFlatpaks(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if want := []string{"org.mozilla.firefox", "org.kde.okular", "org.kde.kcalc"}; !slices.Equal(installed, want) {
		t.Fatalf("installed = %v, want %v", installed, want)
	}
}

// A signed digest can verify while its registry inspection failed. Its
// labels are then unknown, which must not pass as "no core set".
func TestCoreFlatpaksUninspectableImageFailsPreflight(t *testing.T) {
	cosignKey := filepath.Join(t.TempDir(), "cosign.pub")
	if err := os.WriteFile(cosignKey, []byte("public key"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(strings.Replace(bootcBase, "%s", "none", 1),
		`ref = "ghcr.io/frostyard/snow:latest"`,
		`ref = "ghcr.io/frostyard/snow@`+verifiedBootcDigest+`"`+"\ncosign_pub_key = \""+cosignKey+`"`, 1)
	offline := func([]string) ([]byte, error) { return nil, errors.New("registry 503") }
	for _, core := range []bool{true, false} {
		var commands []string
		body := src
		if core {
			body += "core_flatpaks = true\n"
		}
		_, events, err := runCore(t, load(t, body), coreLabelFakeWith(t, nil, &commands, offline), true)
		if !core {
			if err != nil {
				t.Fatalf("core_flatpaks off: dry run = %v, want success (labels not needed)", err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), "could not inspect image") {
			t.Fatalf("core_flatpaks on: error = %v, want an inspection failure", err)
		}
		terminal, ok := events[len(events)-1].(progress.Error)
		if !ok || terminal.Step != "preflight-image" {
			t.Fatalf("terminal event = %#v, want a preflight-image error", events[len(events)-1])
		}
		for _, e := range events {
			if w, ok := e.(progress.Warning); ok && w.Code == progress.CodeNoCoreSet {
				t.Fatal("unknown labels were reported as no_core_set")
			}
		}
	}
}
