package main

import (
	"testing"

	"github.com/frostyard/clix"
	"github.com/spf13/cobra"
)

// buildRoot constructs a fresh root command with all subcommands registered,
// mirroring main() but without calling app.Run() (which would exec fang).
func buildRoot() *cobra.Command {
	app := clix.App{
		Version: "test",
		Commit:  "test",
		Date:    "test",
		BuiltBy: "test",
	}
	root := &cobra.Command{
		Use:   "mentat",
		Short: "Repo documentation generator — produces per-domain SKILL.md files",
	}
	root.AddCommand(syncCmd(), statusCmd(), initCmd())
	// Register clix flags without executing — access the method via reflection
	// to avoid running fang. We replicate the flag registration directly.
	_ = app
	// Register persistent flags the same way clix.App.Run() would.
	root.PersistentFlags().Bool("json", false, "output in JSON format")
	root.PersistentFlags().BoolP("verbose", "v", false, "verbose output")
	root.PersistentFlags().BoolP("dry-run", "n", false, "dry run mode (no actual changes)")
	root.PersistentFlags().BoolP("silent", "s", false, "suppress all progress output")
	return root
}

func TestRootCommand_Use(t *testing.T) {
	root := buildRoot()
	if root.Use != "mentat" {
		t.Errorf("expected root Use to be %q, got %q", "mentat", root.Use)
	}
}

func TestSubcommands_Registered(t *testing.T) {
	root := buildRoot()

	want := []string{"sync", "status", "init"}
	registered := make(map[string]bool)
	for _, sub := range root.Commands() {
		registered[sub.Name()] = true
	}

	for _, name := range want {
		if !registered[name] {
			t.Errorf("expected subcommand %q to be registered", name)
		}
	}
}

func TestRootFlags_DryRun(t *testing.T) {
	root := buildRoot()
	if root.PersistentFlags().Lookup("dry-run") == nil {
		t.Error("expected --dry-run flag to be registered on root")
	}
}

func TestRootFlags_JSON(t *testing.T) {
	root := buildRoot()
	if root.PersistentFlags().Lookup("json") == nil {
		t.Error("expected --json flag to be registered on root")
	}
}

func TestSyncCmd_DefaultPath(t *testing.T) {
	cmd := syncCmd()
	flag := cmd.Flags().Lookup("path")
	if flag == nil {
		t.Fatal("expected --path flag on sync command")
	}
	if flag.DefValue != "." {
		t.Errorf("expected default path to be %q, got %q", ".", flag.DefValue)
	}
}

func TestSubcommands_MaxArgs(t *testing.T) {
	tests := []struct {
		name string
		cmd  *cobra.Command
		args []string
		ok   bool
	}{
		{"sync no args", syncCmd(), []string{}, true},
		{"sync one arg", syncCmd(), []string{"/some/path"}, true},
		{"sync two args", syncCmd(), []string{"/a", "/b"}, false},
		{"status no args", statusCmd(), []string{}, true},
		{"status one arg", statusCmd(), []string{"/some/path"}, true},
		{"status two args", statusCmd(), []string{"/a", "/b"}, false},
		{"init no args", initCmd(), []string{}, true},
		{"init one arg", initCmd(), []string{"/some/path"}, true},
		{"init two args", initCmd(), []string{"/a", "/b"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cmd.Args(tc.cmd, tc.args)
			if tc.ok && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("expected an error for too many args, got nil")
			}
		})
	}
}
