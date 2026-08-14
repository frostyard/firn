package firn

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/tui"
)

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
