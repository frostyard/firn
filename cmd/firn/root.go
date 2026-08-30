// Package firn holds the CLI command handlers (frostyard house
// pattern: thin cobra shells; the install pipeline lives in
// internal/). Bare `firn` launches the TUI wizard (ADR-0007).
package firn

import (
	"fmt"

	"github.com/frostyard/clix"
	"github.com/spf13/cobra"

	"github.com/frostyard/firn/internal/platform"
)

// Version is stamped by cmd/firn-cli from the build's ldflags; it
// feeds the progress protocol's Start event and the TUI.
var Version = "dev"

// NewRootCmd creates the root command with all subcommands registered.
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firn",
		Short: "The installer for snosi images",
		Long: `firn installs every snosi image family — bootc OCI images and
native A/B disk images — from a TOML recipe, headless or through the
built-in wizard.

Run with no arguments to launch the interactive installer. Automation
uses 'firn install <recipe.toml>' (see docs/specs/recipe-schema.md).

The inherited global --dry-run flag is not a wizard or validation mode and
is rejected there. Use 'firn install --dry-run <recipe.toml>' for a
non-destructive, recipe-backed preflight.`,
		Args:              cobra.NoArgs,
		SilenceUsage:      true,
		SilenceErrors:     false,
		PersistentPreRunE: rejectUnsupportedInheritedDryRun,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Bare firn = the TUI wizard with all-auto platform
			// detection; `firn install` (no recipe) reaches the same
			// flow with overrides.
			return runTUI(cmd.Context(), tuiOptions{secureBoot: "auto", tpm: "auto", uefi: "auto"})
		},
	}
	cmd.AddCommand(newValidateCmd(), newInstallCmd())
	return cmd
}

// rejectUnsupportedInheritedDryRun prevents clix's inherited common flag
// from promising a safety mode that bare firn and validate do not implement.
// The install command's own --dry-run flag shadows the inherited flag, so the
// documented recipe-backed preflight remains accepted.
func rejectUnsupportedInheritedDryRun(cmd *cobra.Command, _ []string) error {
	if !clix.DryRun {
		return nil
	}
	return fmt.Errorf("--dry-run is not supported for %s; use 'firn install --dry-run <recipe.toml>' for non-destructive preflight", cmd.CommandPath())
}

// tristate resolves an auto|on|off flag against a platform probe.
func tristate(v string, probe func() bool) (bool, error) {
	switch v {
	case "auto":
		return probe(), nil
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, fmt.Errorf("must be auto, on, or off; got %q", v)
	}
}

// probeFlags binds the shared platform-override flags.
type probeFlags struct {
	secureBoot, tpm, uefi string
}

func (p *probeFlags) register(cmd *cobra.Command, withUEFI bool) {
	cmd.Flags().StringVar(&p.secureBoot, "secure-boot", "auto", "override Secure Boot detection (auto|on|off)")
	cmd.Flags().StringVar(&p.tpm, "tpm", "auto", "override TPM detection (auto|on|off)")
	if withUEFI {
		cmd.Flags().StringVar(&p.uefi, "uefi", "auto", "override UEFI detection (auto|on|off)")
	}
}

func (p *probeFlags) resolve() (secureBoot, tpm, uefi bool, err error) {
	if secureBoot, err = tristate(p.secureBoot, platform.SecureBoot); err != nil {
		return false, false, false, fmt.Errorf("--secure-boot: %w", err)
	}
	if tpm, err = tristate(p.tpm, platform.TPM); err != nil {
		return false, false, false, fmt.Errorf("--tpm: %w", err)
	}
	uefi = true
	if p.uefi != "" {
		if uefi, err = tristate(p.uefi, platform.UEFI); err != nil {
			return false, false, false, fmt.Errorf("--uefi: %w", err)
		}
	}
	return secureBoot, tpm, uefi, nil
}
