// Package config loads and resolves pipeline configuration from a TOML file.
//
// Resolution order (first file found wins):
//  1. Explicit path passed to Load.
//  2. .firn/config.toml relative to the target repo root.
//  3. ~/.config/firn/config.toml (global user config).
//
// If no file is found, Default() values are returned — Load never returns an
// error solely because the file is absent.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config is the top-level configuration structure.
type Config struct {
	Pipeline PipelineConfig `toml:"pipeline" mapstructure:"pipeline"`
}

// PipelineConfig holds behaviour knobs for the pipeline daemon.
type PipelineConfig struct {
	// PRThrottle is the maximum number of concurrent open agent PRs per repo.
	// Default: 3.
	PRThrottle int `toml:"pr_throttle" mapstructure:"pr_throttle"`

	// CIFixerMaxAttempts is the number of CI-fixer retry loops before the
	// pipeline gives up and adds a needs-human label.
	// Default: 3.
	CIFixerMaxAttempts int `toml:"ci_fixer_max_attempts" mapstructure:"ci_fixer_max_attempts"`

	// DraftFirst controls whether all agent PRs open as drafts until
	// behavioral invariants pass.
	// Default: true.
	DraftFirst bool `toml:"draft_first" mapstructure:"draft_first"`
}

// Default returns a Config populated with conservative defaults.
func Default() *Config {
	return &Config{
		Pipeline: PipelineConfig{
			PRThrottle:         3,
			CIFixerMaxAttempts: 3,
			DraftFirst:         true,
		},
	}
}

// Load reads configuration from path.
//
//   - If path is non-empty and the file exists, it is parsed and its values
//     overlay the defaults.
//   - If path is empty, Load looks for a config file using ConfigPath("").
//   - If no config file is found, defaults are returned without error.
//   - A file that exists but cannot be parsed returns a wrapped error.
func Load(path string) (*Config, error) {
	cfg := Default()

	v := viper.New()
	v.SetConfigType("toml")

	// Seed viper with our defaults so partial files still get correct values.
	v.SetDefault("pipeline.pr_throttle", cfg.Pipeline.PRThrottle)
	v.SetDefault("pipeline.ci_fixer_max_attempts", cfg.Pipeline.CIFixerMaxAttempts)
	v.SetDefault("pipeline.draft_first", cfg.Pipeline.DraftFirst)

	if path == "" {
		path = ConfigPath("")
	}

	if path == "" {
		// No config file anywhere — return defaults.
		return cfg, nil
	}

	// Check whether the file exists before asking viper to read it; a missing
	// file is not an error per the contract above.
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}

	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	return cfg, nil
}

// ConfigPath returns the config file path that Load will use when no explicit
// path is given.
//
// Resolution order:
//  1. <repoRoot>/.firn/config.toml  (if repoRoot is non-empty and the file exists)
//  2. ~/.config/firn/config.toml    (if the file exists)
//  3. "" (empty string — caller treats as "no config file")
func ConfigPath(repoRoot string) string {
	if repoRoot != "" {
		p := filepath.Join(repoRoot, ".firn", "config.toml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, ".config", "firn", "config.toml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}
