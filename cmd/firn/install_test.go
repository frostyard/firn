package firn

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/progress"
)

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
