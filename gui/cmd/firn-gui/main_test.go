package main

import (
	"slices"
	"testing"
)

func TestSessionVarsForwardOnlyTheAllowlist(t *testing.T) {
	env := map[string]string{
		"WAYLAND_DISPLAY": "wayland-0",
		"XDG_RUNTIME_DIR": "/run/user/1000",
		"DISPLAY":         ":0",
		"LANG":            "en_US.UTF-8",
		"LD_PRELOAD":      "/tmp/evil.so",
	}
	got := sessionVars(func(k string) string { return env[k] })
	want := []string{"WAYLAND_DISPLAY=/run/user/1000/wayland-0", "DISPLAY=:0", "LANG=en_US.UTF-8"}
	if !slices.Equal(got, want) {
		t.Errorf("sessionVars = %v, want %v", got, want)
	}
}

func TestGuardUpdateCleanupAllowsAPrivateDirectory(t *testing.T) {
	// The test binary runs from a fresh temporary directory, where mygo
	// would find nothing to delete.
	if err := guardUpdateCleanup(); err != nil {
		t.Fatalf("guard refused a clean directory: %v", err)
	}
}
