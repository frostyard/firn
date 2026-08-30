package firn

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/clix"
	"github.com/spf13/cobra"
)

func TestInheritedDryRunScope(t *testing.T) {
	tests := []struct {
		name        string
		args        func(*testing.T) []string
		stubCommand string
		wantGuard   bool
	}{
		{
			name:        "bare firn rejects inherited dry-run before TUI",
			args:        func(*testing.T) []string { return []string{"--dry-run"} },
			stubCommand: "firn",
			wantGuard:   true,
		},
		{
			name: "validate rejects inherited dry-run before validation",
			args: func(t *testing.T) []string {
				return []string{"validate", "--dry-run", filepath.Join(t.TempDir(), "recipe.toml")}
			},
			stubCommand: "validate",
			wantGuard:   true,
		},
		{
			name: "recipe-backed install accepts command-local dry-run",
			args: func(t *testing.T) []string {
				return []string{"install", "--dry-run", filepath.Join(t.TempDir(), "recipe.toml")}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clix.DryRun = false
			t.Cleanup(func() { clix.DryRun = false })

			root := NewRootCmd()
			root.SilenceErrors = true
			root.SilenceUsage = true
			reached := errors.New("command body reached")
			if tt.stubCommand != "" {
				command := root
				if tt.stubCommand != "firn" {
					var err error
					command, _, err = root.Find([]string{tt.stubCommand})
					if err != nil {
						t.Fatal(err)
					}
				}
				command.RunE = func(*cobra.Command, []string) error { return reached }
			}
			args := tt.args(t)
			root.SetArgs(args)

			err := (&clix.App{Version: "test"}).Run(root)
			if tt.wantGuard {
				if err == nil || !strings.Contains(err.Error(), "firn install --dry-run <recipe.toml>") {
					t.Fatalf("firn %v error = %v, want actionable inherited --dry-run rejection", args, err)
				}
				if errors.Is(err, reached) {
					t.Fatalf("firn %v reached command body before rejecting inherited --dry-run", args)
				}
				return
			}

			if err == nil {
				t.Fatalf("firn %v unexpectedly succeeded with missing recipe", args)
			}
			if strings.Contains(err.Error(), "supported only for recipe-backed installs") {
				t.Fatalf("firn %v rejected command-local --dry-run: %v", args, err)
			}
			if !strings.Contains(err.Error(), args[len(args)-1]) {
				t.Fatalf("firn %v did not reach recipe loading: %v", args, err)
			}
		})
	}
}
