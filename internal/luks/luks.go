// Ported from frostyard/fisherman (GPL-3.0-only), fisherman/internal/luks/luks.go.

// Package luks wraps cryptsetup for LUKS2 root-partition encryption.
package luks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/frostyard/firn/internal/runner"
)

const mapperPrefix = "/dev/mapper/"

// stat is a test seam for probing /dev/mapper nodes.
var stat = os.Stat

// MapperPath returns the /dev/mapper/ device path for a mapper name.
func MapperPath(name string) string {
	return mapperPrefix + name
}

// GenerateRecoveryKey generates a cryptographically random
// 64-character hex passphrase.
func GenerateRecoveryKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("luks: crypto/rand unavailable: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Format creates a LUKS2 container on dev, reading the passphrase from
// stdin to avoid it appearing in the process table.
func Format(ctx context.Context, r *runner.Runner, dev, passphrase string) error {
	_, err := r.RunInput(ctx, passphrase,
		"cryptsetup", "luksFormat",
		"--batch-mode",
		"--type=luks2",
		"--key-file=-",
		dev,
	)
	if err != nil {
		return fmt.Errorf("luks: format %s: %w", dev, err)
	}
	return nil
}

// Open opens the LUKS container on dev, making it available at
// /dev/mapper/<mapper>, and returns that path. The passphrase is
// passed via stdin so it never appears in the process table.
//
// A previous interrupted run may have left the mapper open. Close it
// before opening so luksOpen succeeds cleanly (fisherman performs this
// stale-mapper close before luksFormat/luksOpen for the same reason).
func Open(ctx context.Context, r *runner.Runner, dev, mapper, passphrase string) (string, error) {
	if _, err := stat(MapperPath(mapper)); err == nil {
		_ = Close(ctx, r, mapper)
	}
	_, err := r.RunInput(ctx, passphrase,
		"cryptsetup", "luksOpen",
		"--key-file=-",
		dev,
		mapper,
	)
	if err != nil {
		return "", fmt.Errorf("luks: open %s: %w", dev, err)
	}
	return MapperPath(mapper), nil
}

// Close closes the LUKS device identified by mapper.
func Close(ctx context.Context, r *runner.Runner, mapper string) error {
	if _, err := r.Run(ctx, "cryptsetup", "luksClose", mapper); err != nil {
		return fmt.Errorf("luks: close %s: %w", mapper, err)
	}
	return nil
}
