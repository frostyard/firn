package firn

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
)

func commandRecipeFile(t *testing.T, family string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recipe.toml")
	data := "version = 1\n[image]\nfamily = \"" + family + "\"\n"
	if family == "bootc" {
		data += "ref = \"ghcr.io/frostyard/floe:latest\"\n"
	} else {
		data += "product = \"floe-ab\"\n"
	}
	data += "[target]\ndisk = \"/dev/vda\"\nfilesystem = \"btrfs\"\n[security]\nencryption = \"none\"\n[system]\nhostname = \"frost01\"\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallV1WarnsBeforeFirstStep(t *testing.T) {
	old := assembleInstall
	t.Cleanup(func() { assembleInstall = old })
	assembleInstall = func(l *recipe.Loaded) *pipeline.Pipeline {
		return &pipeline.Pipeline{Steps: []pipeline.Step{{Name: "fake", Preflight: true, Run: func(_ context.Context, _ *pipeline.Env) error { return nil }}}}
	}
	output := filepath.Join(t.TempDir(), "events.jsonl")
	f, err := os.Create(output)
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = f
	t.Cleanup(func() { os.Stdout = stdout })
	cmd := newInstallCmd()
	cmd.SetArgs([]string{"--secure-boot=off", "--tpm=off", "--uefi=on", "--dry-run", "--json-progress", commandRecipeFile(t, "bootc")})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if len(events) < 3 || events[1]["event"] != "warning" || events[1]["code"] != progress.CodeRecipeV1Deprecated || events[2]["event"] != "step_start" {
		t.Fatalf("event order: %+v", events)
	}
	if events[1]["message"] != recipe.Deprecations(mustLoadRecipe(t, commandRecipeFile(t, "bootc")))[0].Message {
		t.Fatalf("wrong deprecation message: %+v", events[1])
	}
}

func TestInstallWiresRegistryProbe(t *testing.T) {
	old := assembleInstall
	t.Cleanup(func() { assembleInstall = old })
	var wired bool
	assembleInstall = func(*recipe.Loaded) *pipeline.Pipeline {
		return &pipeline.Pipeline{Steps: []pipeline.Step{{Name: "fake", Preflight: true, Run: func(_ context.Context, env *pipeline.Env) error {
			wired = env.RegistryProbe != nil
			return nil
		}}}}
	}
	cmd := newInstallCmd()
	cmd.SetArgs([]string{"--secure-boot=off", "--tpm=off", "--uefi=on", "--dry-run", commandRecipeFile(t, "bootc")})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !wired {
		t.Fatal("install env has no registry probe wired")
	}
}

func mustLoadRecipe(t *testing.T, path string) *recipe.Loaded {
	t.Helper()
	l, err := recipe.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestInstallABRejectsBeforeAssemble(t *testing.T) {
	old := assembleInstall
	t.Cleanup(func() { assembleInstall = old })
	assembleInstall = func(*recipe.Loaded) *pipeline.Pipeline { t.Fatal("assembled A/B recipe"); return nil }
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = f
	t.Cleanup(func() { os.Stderr = stderr })
	cmd := newInstallCmd()
	cmd.SetArgs([]string{"--secure-boot=off", "--tpm=off", "--uefi=on", "--dry-run", commandRecipeFile(t, "ab")})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "recipe is invalid") {
		t.Fatalf("A/B install error: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "A/B version 1 is unsupported") || !strings.Contains(string(data), "no conversion was performed") {
		t.Fatalf("missing A/B diagnostic: %s", data)
	}
}

func TestInstallPubringUnknown(t *testing.T) {
	cmd := newInstallCmd()
	cmd.SetArgs([]string{"--pubring", "/tmp/key"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag: --pubring") {
		t.Fatalf("--pubring error: %v", err)
	}
}

func TestValidateV1DeprecationExitsZero(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = f
	t.Cleanup(func() { os.Stderr = old })
	cmd := newValidateCmd()
	cmd.SetArgs([]string{"--secure-boot=off", "--tpm=off", commandRecipeFile(t, "bootc")})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "bootc recipe version 1 is deprecated") {
		t.Fatalf("stderr: %s", data)
	}
}

func TestValidateInstallMode(t *testing.T) {
	tests := []struct {
		name         string
		hasRecipe    bool
		dryRun       bool
		confirm      string
		jsonProgress bool
		want         string
	}{
		{name: "wizard default"},
		{name: "wizard dry run", dryRun: true, want: "--dry-run requires a recipe path"},
		{name: "wizard confirm", confirm: "/dev/vda", want: "--confirm applies to headless installs"},
		{name: "wizard JSON", jsonProgress: true, want: "--json-progress requires a recipe path"},
		{
			name: "headless accepts all headless flags", hasRecipe: true,
			dryRun: true, confirm: "/dev/vda", jsonProgress: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInstallMode(tt.hasRecipe, tt.dryRun, tt.confirm, tt.jsonProgress)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("validateInstallMode: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateInstallMode error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestPrintEventRendersStepProgress(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event progress.StepProgress
		want  string
	}{
		{name: "fraction including zero", event: progress.StepProgress{Index: 1, Fraction: 0}, want: "progress: 0%"},
		{name: "fraction", event: progress.StepProgress{Index: 1, Fraction: 0.25}, want: "progress: 25%"},
		{name: "bytes", event: progress.StepProgress{Index: 1, Bytes: 512, TotalBytes: 1024}, want: "progress: 512/1024 bytes (50%)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			printEventTo(&out, tc.event)
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("printEventTo() = %q, want containing %q", out.String(), tc.want)
			}
		})
	}
}

func TestInstallCommandRejectsWizardJSONProgressBeforeLaunchingTUI(t *testing.T) {
	cmd := newInstallCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--json-progress"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--json-progress requires a recipe path") {
		t.Fatalf("install --json-progress error = %v", err)
	}
}

func TestInstallCommandAllowsHeadlessJSONProgress(t *testing.T) {
	cmd := newInstallCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	missingRecipe := filepath.Join(t.TempDir(), "missing.toml")
	cmd.SetArgs([]string{"--json-progress", missingRecipe})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("install --json-progress with a recipe path unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "--json-progress requires a recipe path") {
		t.Fatalf("headless install rejected --json-progress: %v", err)
	}
	if !strings.Contains(err.Error(), missingRecipe) {
		t.Fatalf("headless install did not reach recipe loading: %v", err)
	}
}

func TestREADMEHeadlessInstallConfirmationExample(t *testing.T) {
	const documented = "firn install --confirm /dev/nvme0n1 recipe.toml"

	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if !strings.Contains(string(readme), documented) {
		t.Fatalf("README.md does not contain complete destructive install example %q", documented)
	}

	cmd := newInstallCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--confirm", "/dev/nvme0n1", "recipe.toml"})

	err = cmd.Execute()
	if err == nil {
		t.Fatal("documented command with missing recipe unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "recipe.toml") {
		t.Fatalf("documented command did not parse --confirm value and reach recipe loading: %v", err)
	}
}
