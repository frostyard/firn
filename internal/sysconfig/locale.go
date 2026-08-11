// Locale, timezone, and keyboard configuration shared by both target
// writers (docs/design/architecture.md "System configuration"): the
// content formats are identical on both paths — snosi's bootc and A/B
// images are the same Debian userland — so the builders live here and
// each writer only decides WHERE the files land (deployment /etc vs
// the /etc-overlay upper). Formats ported from frostyard/snosi
// (GPL-3.0-only), snosi-install seed_var.
package sysconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// localeConfContent is seed_var's exact single-line format.
func localeConfContent(locale string) []byte {
	return []byte("LANG=" + locale + "\n")
}

// timezoneTarget returns the RELATIVE localtime symlink target —
// ../usr/share/zoneinfo/<tz>, the exact form seed_var's `ln -sfn`
// wrote: resolved from /etc on the booted system it lands on
// /usr/share/zoneinfo/<tz>, and it must stay relative so it resolves
// inside the target root rather than on the installer's host.
func timezoneTarget(tz string) (string, error) {
	// tz becomes a symlink target component; snosi validated it at the
	// CLI (TIMEZONE_RE rejects e.g. "America/../../etc"). Guard here
	// too since this is the last stop before the path is written.
	if tz == "" || strings.HasPrefix(tz, "/") || strings.Contains(tz, "..") {
		return "", fmt.Errorf("invalid timezone %q", tz)
	}
	return "../usr/share/zoneinfo/" + tz, nil
}

// keyboardFileContent renders a LAYOUT[:VARIANT[:MODEL]] spec in
// keyboard(5) format, field order and quoting matching seed_var byte
// for byte (model defaults to pc105 like seed_var's ${kb_model:-pc105}).
func keyboardFileContent(spec string) ([]byte, error) {
	parts := strings.SplitN(spec, ":", 3)
	layout := parts[0]
	if layout == "" {
		return nil, fmt.Errorf("invalid keyboard spec %q: empty layout", spec)
	}
	var variant, model string
	if len(parts) > 1 {
		variant = parts[1]
	}
	if len(parts) > 2 {
		model = parts[2]
	}
	if model == "" {
		model = "pc105"
	}
	return fmt.Appendf(nil, "XKBMODEL=%q\nXKBLAYOUT=%q\nXKBVARIANT=%q\nXKBOPTIONS=\"\"\nBACKSPACE=\"guess\"\n",
		model, layout, variant), nil
}

// writeLocaleTo, writeTimezoneTo, writeKeyboardTo write the three
// artifacts into an /etc-shaped directory (a deployment's /etc or the
// overlay upper). Empty values are no-ops (recipe fields are optional).
func writeLocaleTo(etcDir, locale string) error {
	if locale == "" {
		return nil
	}
	return writeFileAtomic(filepath.Join(etcDir, "locale.conf"), localeConfContent(locale), 0o644)
}

func writeTimezoneTo(etcDir, tz string) error {
	if tz == "" {
		return nil
	}
	target, err := timezoneTarget(tz)
	if err != nil {
		return err
	}
	// Debian keeps /etc/timezone alongside the symlink; write both,
	// exactly as seed_var did.
	if err := writeFileAtomic(filepath.Join(etcDir, "timezone"), []byte(tz+"\n"), 0o644); err != nil {
		return err
	}
	link := filepath.Join(etcDir, "localtime")
	// ln -sfn semantics: replace any existing link.
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing old localtime symlink: %w", err)
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("symlinking localtime: %w", err)
	}
	return nil
}

func writeKeyboardTo(etcDir, spec string) error {
	if spec == "" {
		return nil
	}
	content, err := keyboardFileContent(spec)
	if err != nil {
		return err
	}
	dir := filepath.Join(etcDir, "default")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return writeFileAtomic(filepath.Join(dir, "keyboard"), content, 0o644)
}

// Deployment-writer methods (bootc path): same content, written into
// the deployment's writable /etc. Closes the phase-5 writer gap —
// fisherman never supported these (docs/plans/roadmap.md Phase 5).

// WriteLocale writes the deployment's etc/locale.conf.
func (w *DeploymentWriter) WriteLocale(ctx context.Context, locale string) error {
	if locale == "" {
		return nil
	}
	lay, err := w.resolve(ctx)
	if err != nil {
		return err
	}
	return writeLocaleTo(lay.etcDir, locale)
}

// WriteTimezone writes the deployment's etc/timezone and localtime
// symlink.
func (w *DeploymentWriter) WriteTimezone(ctx context.Context, tz string) error {
	if tz == "" {
		return nil
	}
	lay, err := w.resolve(ctx)
	if err != nil {
		return err
	}
	return writeTimezoneTo(lay.etcDir, tz)
}

// WriteKeyboard writes the deployment's etc/default/keyboard (see the
// overlay writer's vconsole.conf-symlink incident note; the same
// keyboard-configuration ownership applies to Debian bootc images).
func (w *DeploymentWriter) WriteKeyboard(ctx context.Context, spec string) error {
	if spec == "" {
		return nil
	}
	lay, err := w.resolve(ctx)
	if err != nil {
		return err
	}
	return writeKeyboardTo(lay.etcDir, spec)
}
