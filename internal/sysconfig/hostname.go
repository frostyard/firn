// Ported from frostyard/fisherman (GPL-3.0-only), fisherman/internal/post/post.go.

package sysconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// WriteHostname writes etc/hostname into the installed system at TargetDir.
// For composefs-native deployments, the hostname goes into the active deploy
// etc dir (found via ComposeFsDeployEtcDirFn). For ostree-based deployments
// it goes into the ostree deployment subtree (found via DeploymentDirFn).
func (w *DeploymentWriter) WriteHostname(ctx context.Context, hostname string) error {
	lay, err := w.resolve(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(lay.etcDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", lay.etcDir, err)
	}
	hostnameFile := filepath.Join(lay.etcDir, "hostname")
	if err := os.WriteFile(hostnameFile, []byte(hostname+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", hostnameFile, err)
	}
	return nil
}
