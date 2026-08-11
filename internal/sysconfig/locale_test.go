package sysconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// The deployment writer's locale/timezone/keyboard must produce the
// exact same artifacts as the overlay writer (shared builders in
// locale.go), landing in the deployment's writable /etc.
func TestDeploymentWriterLocaleTimezoneKeyboard(t *testing.T) {
	target := composefsTarget(t, "aaa")
	etcDir := filepath.Join(target, "state", "deploy", "aaa", "etc")

	var calls []call
	w := &DeploymentWriter{TargetDir: target, Runner: fakeRunner(&calls)}
	ctx := t.Context()

	if err := w.WriteLocale(ctx, "en_US.UTF-8"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(etcDir, "locale.conf")); err != nil || string(data) != "LANG=en_US.UTF-8\n" {
		t.Errorf("locale.conf = %q, %v", data, err)
	}

	if err := w.WriteTimezone(ctx, "America/Chicago"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(etcDir, "timezone")); err != nil || string(data) != "America/Chicago\n" {
		t.Errorf("timezone = %q, %v", data, err)
	}
	if link, err := os.Readlink(filepath.Join(etcDir, "localtime")); err != nil || link != "../usr/share/zoneinfo/America/Chicago" {
		t.Errorf("localtime -> %q, %v (must be relative)", link, err)
	}
	if err := w.WriteTimezone(ctx, "Etc/UTC"); err != nil {
		t.Fatalf("replacing existing symlink (ln -sfn semantics): %v", err)
	}
	if err := w.WriteTimezone(ctx, "America/../etc"); err == nil {
		t.Error("path-traversal timezone must be rejected")
	}

	if err := w.WriteKeyboard(ctx, "us:intl"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(etcDir, "default", "keyboard"))
	if err != nil {
		t.Fatal(err)
	}
	want := "XKBMODEL=\"pc105\"\nXKBLAYOUT=\"us\"\nXKBVARIANT=\"intl\"\nXKBOPTIONS=\"\"\nBACKSPACE=\"guess\"\n"
	if string(data) != want {
		t.Errorf("keyboard = %q, want %q", data, want)
	}

	// Empty values are no-ops on every method.
	for _, err := range []error{w.WriteLocale(ctx, ""), w.WriteTimezone(ctx, ""), w.WriteKeyboard(ctx, "")} {
		if err != nil {
			t.Errorf("empty value must be a no-op: %v", err)
		}
	}
}
