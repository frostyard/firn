package firn

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/platform"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/tui"
)

func TestRunTUIRejectsNonUEFIBeforeWizardOrPipeline(t *testing.T) {
	displayed := errors.New("displayed")
	var shown error
	wizardCalls := 0
	rt := tuiRuntime{
		holdError: func(_ context.Context, title string, err error) error {
			if title != "unsupported machine" {
				t.Fatalf("error title = %q, want unsupported machine", title)
			}
			shown = err
			return displayed
		},
		runWizard: func(context.Context, tui.WizardOpts) (*recipe.Recipe, error) {
			wizardCalls++
			return nil, nil
		},
	}

	err := runTUIWithRuntime(context.Background(), tuiOptions{
		secureBoot: "off",
		tpm:        "off",
		uefi:       "off",
	}, rt)
	if !errors.Is(err, displayed) {
		t.Fatalf("runTUIWithRuntime error = %v, want displayed sentinel", err)
	}
	if !errors.Is(shown, platform.ErrUEFIRequired) {
		t.Fatalf("displayed error = %v, want shared UEFI diagnostic", shown)
	}
	if wizardCalls != 0 {
		t.Fatalf("wizard called %d times on unsupported machine; confirmation and pipeline became reachable", wizardCalls)
	}
}

func TestReportTUIResultDoesNotLogRecoveryKey(t *testing.T) {
	const (
		theKey = "1234-ABCD-5678-EFGH"
		path   = "/run/firn/recipe-test.toml"
		disk   = "/dev/vda"
	)
	var stderr bytes.Buffer

	err := reportTUIResult(&stderr, tui.InstallResult{
		Done:        true,
		RecoveryKey: theKey,
	}, nil, path, disk)
	if err != nil {
		t.Fatalf("reportTUIResult: %v", err)
	}
	got := stderr.String()
	if strings.Contains(got, theKey) || strings.Contains(got, "RECOVERY KEY") {
		t.Fatalf("TUI command path logged the recovery key after RunInstall returned: %q", got)
	}
	if !strings.Contains(got, path) || !strings.Contains(got, disk) {
		t.Fatalf("TUI command path lost non-secret reproduction details: %q", got)
	}
}
