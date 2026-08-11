// Ported from frostyard/fisherman (GPL-3.0-only), fisherman/internal/post/post.go.

// Package sysconfig owns the semantics of every [system] recipe feature
// (docs/design/architecture.md, "System configuration"). This file holds
// the bootc-path deployment writer's layout resolution: composefs-native
// deployments keep their writable /etc under state/deploy/<id>/etc, while
// ostree deployments keep it inside the deployment directory's etc.
package sysconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/frostyard/firn/internal/runner"
)

// DeploymentWriter writes system configuration into a mounted bootc
// target. TargetDir is the mounted target root (the sysroot); Runner is
// the seam for all host command execution.
type DeploymentWriter struct {
	TargetDir string
	Runner    *runner.Runner
}

// DefaultComposeFsDeployEtcDir finds the writable /etc directory for the
// composefs-native deployment installed at target.  The bootc composefs-native
// layout stores the writable /etc at state/deploy/<COMPOSEFS_HASH>/etc/ — the
// same hash that appears in the kernel command line as composefs=<HASH> at boot.
//
// Discovery priority:
//  1. Read composefs=<HASH> from any BLS loader entry under
//     $TARGET/boot/loader/entries/ or $TARGET/boot/efi/loader/entries/.
//  2. Fall back to the newest directory under $TARGET/state/deploy/ (safe for
//     fresh installs where only one deployment exists).
//
// This path is bind-mounted at /etc on the booted system and must be used
// instead of $TARGET/etc/ for any post-install write that should be visible
// after first boot.
func DefaultComposeFsDeployEtcDir(target string) (string, error) {
	// Preferred: parse composefs=<HASH> from BLS loader entries.
	for _, loaderDir := range []string{
		filepath.Join(target, "boot", "loader", "entries"),
		filepath.Join(target, "boot", "efi", "loader", "entries"),
	} {
		entries, err := filepath.Glob(filepath.Join(loaderDir, "*.conf"))
		if err != nil || len(entries) == 0 {
			continue
		}
		for _, entry := range entries {
			data, err := os.ReadFile(entry)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				if !strings.HasPrefix(strings.TrimSpace(line), "options") {
					continue
				}
				for _, field := range strings.Fields(line) {
					if !strings.HasPrefix(field, "composefs=") {
						continue
					}
					hash := strings.TrimPrefix(field, "composefs=")
					if hash == "" {
						continue
					}
					deployEtc := filepath.Join(target, "state", "deploy", hash, "etc")
					if _, statErr := os.Stat(deployEtc); statErr == nil {
						return deployEtc, nil
					}
				}
			}
		}
	}

	// Fallback: pick the newest directory under state/deploy/.
	deployBase := filepath.Join(target, "state", "deploy")
	entries, err := os.ReadDir(deployBase)
	if err != nil {
		return "", fmt.Errorf("reading composefs deploy base %s: %w", deployBase, err)
	}
	var newestName string
	var newestMtime time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if newestName == "" || info.ModTime().After(newestMtime) {
			newestName = e.Name()
			newestMtime = info.ModTime()
		}
	}
	if newestName == "" {
		return "", fmt.Errorf("no composefs deploy directory found under %s", deployBase)
	}
	return filepath.Join(deployBase, newestName, "etc"), nil
}

// ComposeFsDeployEtcDirFn is called by composefs-native post-install functions
// to locate the writable /etc for the active deployment. Tests replace this with
// a stub; restore with DefaultComposeFsDeployEtcDir.
var ComposeFsDeployEtcDirFn = DefaultComposeFsDeployEtcDir

// ComposeFsVarDir returns the writable /var directory for composefs-native
// installs. The bootc composefs-native layout bind-mounts this path at /var
// at boot.
func ComposeFsVarDir(target string) string {
	return filepath.Join(target, "state", "os", "default", "var")
}

// DefaultDeploymentDir returns the ostree deployment directory inside sysroot.
// It first tries `ostree admin --sysroot=<sysroot> --print-current-dir`, which
// works on a booted system. On a freshly-installed target (never booted),
// --print-current-dir exits 1 because there is no booted-deployment state;
// we fall back to a filesystem glob over ostree/deploy/**/deploy/* to find
// the single deployment that bootc install to-filesystem just created.
func DefaultDeploymentDir(ctx context.Context, r *runner.Runner, sysroot string) (string, error) {
	out, err := r.Run(ctx, "ostree", "admin", "--sysroot="+sysroot, "--print-current-dir")
	if err == nil {
		path := strings.TrimSpace(string(out))
		if path != "" {
			return path, nil
		}
	}
	// Fallback: freshly-installed target has no booted-deployment state.
	// bootc install to-filesystem always produces exactly one deployment at
	// <sysroot>/ostree/deploy/<osname>/deploy/<hash>.<n>.
	matches, globErr := filepath.Glob(filepath.Join(sysroot, "ostree", "deploy", "*", "deploy", "*"))
	if globErr != nil || len(matches) == 0 {
		if err != nil {
			return "", fmt.Errorf("ostree admin --print-current-dir: %w", err)
		}
		return "", fmt.Errorf("no ostree deployment found under %s", sysroot)
	}
	return matches[0], nil
}

// DeploymentDirFn is called by the deployment writer to locate the ostree
// deployment directory. Tests replace this with a stub; restore with
// DefaultDeploymentDir.
var DeploymentDirFn = DefaultDeploymentDir

// isComposeFsNative reports whether the installed system at TargetDir uses the
// composefs-native backend (bootc install to-filesystem --composefs-backend).
//
// Detection is POSITIVE, via the composefs-native deploy base
// <sysroot>/state/deploy/<hash>/ (the same path DefaultComposeFsDeployEtcDir
// resolves against). The earlier heuristic keyed off "/ostree absent", but the
// composefs-native backend ALSO creates an /ostree directory — so that check
// mis-classified composefs-native installs as ostree. The concrete failure:
// WriteHostname (and every other isComposeFsNative caller) then took the ostree
// path, calling `ostree admin --print-current-dir`, which exits 1 on a
// freshly-installed --skip-finalize target, and whose glob fallback over
// /ostree/deploy/*/deploy/* finds nothing because the deployment is under
// /state/ — a fatal `finding deployment dir` crash on composefs images.
func (w *DeploymentWriter) isComposeFsNative(ctx context.Context) bool {
	// Use ls via the runner (not os.Stat), which runs in the host mount
	// namespace rather than any sandbox. composefs-native ⟺ state/deploy exists.
	if _, err := w.Runner.Run(ctx, "ls", filepath.Join(w.TargetDir, "state", "deploy")); err == nil {
		return true
	}
	// Legacy signal retained: a deployment with no /ostree at all is composefs.
	_, err := w.Runner.Run(ctx, "ls", filepath.Join(w.TargetDir, "ostree"))
	return err != nil
}

// layout is the resolved deployment layout for one write operation.
type layout struct {
	composefs bool
	// root is the deployment root: the ostree deployment directory (a full
	// rootfs), or the parent of the composefs state/deploy/<id>/etc dir.
	root string
	// etcDir is the deployment's writable /etc.
	etcDir string
}

// resolve locates the deployment root and its writable /etc. For
// composefs-native deployments the writable /etc is found via
// ComposeFsDeployEtcDirFn; for ostree-based deployments it is the etc
// subtree of the deployment directory (found via DeploymentDirFn).
func (w *DeploymentWriter) resolve(ctx context.Context) (layout, error) {
	if w.isComposeFsNative(ctx) {
		etcDir, err := ComposeFsDeployEtcDirFn(w.TargetDir)
		if err != nil {
			return layout{}, fmt.Errorf("finding composefs deploy etc: %w", err)
		}
		return layout{composefs: true, root: filepath.Dir(etcDir), etcDir: etcDir}, nil
	}
	deployDir, err := DeploymentDirFn(ctx, w.Runner, w.TargetDir)
	if err != nil {
		return layout{}, fmt.Errorf("finding deployment dir: %w", err)
	}
	return layout{root: deployDir, etcDir: filepath.Join(deployDir, "etc")}, nil
}

// EtcDir resolves the deployment's writable /etc for a mounted target
// — the directory other packages (e.g. TPM first-boot staging) write
// configuration into.
func EtcDir(ctx context.Context, r *runner.Runner, targetDir string) (string, error) {
	w := &DeploymentWriter{TargetDir: targetDir, Runner: r}
	lay, err := w.resolve(ctx)
	if err != nil {
		return "", err
	}
	return lay.etcDir, nil
}

// staterootVarDir returns the stateroot /var for the resolved layout: the
// directory mounted over /var on the booted system. composefs-native →
// state/os/default/var; ostree/bootc → ostree/deploy/default/var.
func (w *DeploymentWriter) staterootVarDir(lay layout) string {
	if lay.composefs {
		return ComposeFsVarDir(w.TargetDir)
	}
	return filepath.Join(w.TargetDir, "ostree", "deploy", "default", "var")
}
