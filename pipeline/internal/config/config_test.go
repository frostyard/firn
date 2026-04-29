package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/frostyard/firn/pipeline/internal/config"
)

// TestDefault verifies that Default returns the documented conservative values.
func TestDefault(t *testing.T) {
	t.Parallel()

	cfg := config.Default()

	if cfg.Pipeline.PRThrottle != 3 {
		t.Errorf("PRThrottle: got %d, want 3", cfg.Pipeline.PRThrottle)
	}
	if cfg.Pipeline.CIFixerMaxAttempts != 3 {
		t.Errorf("CIFixerMaxAttempts: got %d, want 3", cfg.Pipeline.CIFixerMaxAttempts)
	}
	if !cfg.Pipeline.DraftFirst {
		t.Errorf("DraftFirst: got false, want true")
	}
}

// TestLoad_nonExistentPath verifies that a missing config file returns
// defaults without error.
func TestLoad_nonExistentPath(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load("/nonexistent/path/to/config.toml")
	if err != nil {
		t.Fatalf("Load with nonexistent path returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load returned nil config")
	}

	want := config.Default()
	if cfg.Pipeline.PRThrottle != want.Pipeline.PRThrottle {
		t.Errorf("PRThrottle: got %d, want %d", cfg.Pipeline.PRThrottle, want.Pipeline.PRThrottle)
	}
	if cfg.Pipeline.CIFixerMaxAttempts != want.Pipeline.CIFixerMaxAttempts {
		t.Errorf("CIFixerMaxAttempts: got %d, want %d", cfg.Pipeline.CIFixerMaxAttempts, want.Pipeline.CIFixerMaxAttempts)
	}
	if cfg.Pipeline.DraftFirst != want.Pipeline.DraftFirst {
		t.Errorf("DraftFirst: got %v, want %v", cfg.Pipeline.DraftFirst, want.Pipeline.DraftFirst)
	}
}

// TestLoad_emptyPath verifies that an empty path triggers ConfigPath resolution
// and returns defaults when no config file is present anywhere predictable.
func TestLoad_emptyPath(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv.

	// Temporarily redirect HOME so that ~/.config/firn/config.toml doesn't
	// accidentally exist on the test runner.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load(\"\") returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load returned nil config")
	}

	want := config.Default()
	if cfg.Pipeline.PRThrottle != want.Pipeline.PRThrottle {
		t.Errorf("PRThrottle: got %d, want %d", cfg.Pipeline.PRThrottle, want.Pipeline.PRThrottle)
	}
}

// TestLoad_overrideSingleValue verifies that a TOML file that sets only one
// field leaves the other fields at their defaults.
func TestLoad_overrideSingleValue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.toml")

	toml := `[pipeline]
pr_throttle = 10
`
	if err := os.WriteFile(cfgFile, []byte(toml), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Pipeline.PRThrottle != 10 {
		t.Errorf("PRThrottle: got %d, want 10", cfg.Pipeline.PRThrottle)
	}
	// Unset values must stay at defaults.
	if cfg.Pipeline.CIFixerMaxAttempts != 3 {
		t.Errorf("CIFixerMaxAttempts: got %d, want 3 (default)", cfg.Pipeline.CIFixerMaxAttempts)
	}
	if !cfg.Pipeline.DraftFirst {
		t.Errorf("DraftFirst: got false, want true (default)")
	}
}

// TestLoad_allValues verifies that all three knobs can be overridden.
func TestLoad_allValues(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.toml")

	toml := `[pipeline]
pr_throttle = 5
ci_fixer_max_attempts = 7
draft_first = false
`
	if err := os.WriteFile(cfgFile, []byte(toml), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Pipeline.PRThrottle != 5 {
		t.Errorf("PRThrottle: got %d, want 5", cfg.Pipeline.PRThrottle)
	}
	if cfg.Pipeline.CIFixerMaxAttempts != 7 {
		t.Errorf("CIFixerMaxAttempts: got %d, want 7", cfg.Pipeline.CIFixerMaxAttempts)
	}
	if cfg.Pipeline.DraftFirst {
		t.Errorf("DraftFirst: got true, want false")
	}
}

// TestConfigPath_repoRoot verifies that a .firn/config.toml inside the repo root
// is returned when it exists.
func TestConfigPath_repoRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	firnDir := filepath.Join(dir, ".firn")
	if err := os.MkdirAll(firnDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfgFile := filepath.Join(firnDir, "config.toml")
	if err := os.WriteFile(cfgFile, []byte(""), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	got := config.ConfigPath(dir)
	if got != cfgFile {
		t.Errorf("ConfigPath(%q) = %q, want %q", dir, got, cfgFile)
	}
}

// TestConfigPath_globalFallback verifies that ~/.config/firn/config.toml is
// used when no repo-level file exists.
func TestConfigPath_globalFallback(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv.

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	globalDir := filepath.Join(tmp, ".config", "firn")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfgFile := filepath.Join(globalDir, "config.toml")
	if err := os.WriteFile(cfgFile, []byte(""), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	// Pass an empty repoRoot so the repo-level check is skipped.
	got := config.ConfigPath("")
	if got != cfgFile {
		t.Errorf("ConfigPath(\"\") = %q, want %q", got, cfgFile)
	}
}

// TestConfigPath_noFile verifies that an empty string is returned when neither
// the repo-level nor global config file exists.
func TestConfigPath_noFile(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv.

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	got := config.ConfigPath("")
	if got != "" {
		t.Errorf("ConfigPath(\"\") = %q, want \"\"", got)
	}
}
