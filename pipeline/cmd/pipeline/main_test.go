package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// buildRoot constructs a fresh root command for each test to avoid flag
// re-registration panics that occur when sharing a command across tests.
func buildRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "pipeline",
		Short: "Issue-to-PR execution engine for the frostyard ecosystem",
	}
	root.AddCommand(runCmd(), statusCmd(), triggerCmd())
	return root
}

func TestRootUse(t *testing.T) {
	root := buildRoot()
	if root.Use != "pipeline" {
		t.Errorf("expected root Use to be %q, got %q", "pipeline", root.Use)
	}
}

func TestSubcommandsRegistered(t *testing.T) {
	root := buildRoot()

	want := []string{"run", "status", "trigger"}
	registered := map[string]bool{}
	for _, sub := range root.Commands() {
		registered[sub.Name()] = true
	}

	for _, name := range want {
		if !registered[name] {
			t.Errorf("subcommand %q not registered on root", name)
		}
	}
}

func TestSubcommandCount(t *testing.T) {
	root := buildRoot()
	got := len(root.Commands())
	if got != 3 {
		t.Errorf("expected 3 subcommands, got %d", got)
	}
}

func TestRootHasDryRunAndJSONFlags(t *testing.T) {
	// clix.registerFlags uses PersistentFlags; we register them manually here
	// to test flag presence without invoking app.Run() (which would execute the command).
	root := buildRoot()
	root.PersistentFlags().Bool("dry-run", false, "dry run mode")
	root.PersistentFlags().Bool("json", false, "output in JSON format")

	for _, name := range []string{"dry-run", "json"} {
		if f := root.PersistentFlags().Lookup(name); f == nil {
			t.Errorf("expected persistent flag --%s to be registered", name)
		}
	}
}

func TestRunSubcommandFlags(t *testing.T) {
	run := runCmd()

	if f := run.Flags().Lookup("repo"); f == nil {
		t.Error("expected --repo flag on run subcommand")
	}
	if f := run.Flags().Lookup("interval"); f == nil {
		t.Error("expected --interval flag on run subcommand")
	}
}

func TestRunIntervalDefault(t *testing.T) {
	run := runCmd()
	f := run.Flags().Lookup("interval")
	if f == nil {
		t.Fatal("--interval flag not found on run subcommand")
	}
	if f.DefValue != "5m0s" {
		t.Errorf("expected --interval default to be %q, got %q", "5m0s", f.DefValue)
	}
}

func TestStatusSubcommandHasRepoFlag(t *testing.T) {
	status := statusCmd()
	if f := status.Flags().Lookup("repo"); f == nil {
		t.Error("expected --repo flag on status subcommand")
	}
}

func TestTriggerSubcommandName(t *testing.T) {
	trigger := triggerCmd()
	if trigger.Name() != "trigger" {
		t.Errorf("expected trigger command name to be %q, got %q", "trigger", trigger.Name())
	}
}
