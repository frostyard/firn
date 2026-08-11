// Ported from frostyard/snosi (GPL-3.0-only), shared/native-installer/tree/usr/libexec/snosi-install (stream_download_verify, validate_disk_image_layout, relocate_and_grow_var, format_var, enroll_tpm_var, mok_import).

package abimg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/frostyard/firn/internal/runner"
)

// FormatVar ports format_var's mkfs step: create the /var filesystem,
// labeled "var" so the image's gpt-auto/label-based discovery finds it.
// fs is "ext4" (snosi's hardcoded choice) or "btrfs" (ADR-0008).
//
// With btrfs and subvolumes true, NESTED subvolumes `home` and
// `snapshots` are created inside the filesystem root via a scratch
// mount. Nested (not top-level `@`-style) is deliberate: the mounted
// /var IS the default subvolume, so the nested subvolumes appear as
// plain directories to the booted system and neither the image's mount
// logic nor any mount units need to change (ADR-0008).
//
// LUKS layering is the caller's job via internal/luks — when /var is
// encrypted, dev is the opened /dev/mapper node, not the raw
// partition. (snosi's format_var interleaved cryptsetup here; firn
// keeps that in one place instead of reimplementing it.)
func FormatVar(ctx context.Context, r *runner.Runner, dev, fs string, subvolumes bool) error {
	switch fs {
	case "ext4":
		if _, err := r.Run(ctx, "mkfs.ext4", "-F", "-L", "var", dev); err != nil {
			return fmt.Errorf("abimg: mkfs.ext4 %s: %w", dev, err)
		}
		return nil
	case "btrfs":
		if _, err := r.Run(ctx, "mkfs.btrfs", "-f", "-L", "var", dev); err != nil {
			return fmt.Errorf("abimg: mkfs.btrfs %s: %w", dev, err)
		}
		if !subvolumes {
			return nil
		}
		mnt, err := mkdirTemp("", "firn-abimg-var-")
		if err != nil {
			return fmt.Errorf("abimg: scratch mount dir: %w", err)
		}
		defer os.RemoveAll(mnt)
		if _, err := r.Run(ctx, "mount", "-t", "btrfs", dev, mnt); err != nil {
			return fmt.Errorf("abimg: mount %s: %w", dev, err)
		}
		var subvolErr error
		for _, sub := range []string{"home", "snapshots"} {
			if _, err := r.Run(ctx, "btrfs", "subvolume", "create", filepath.Join(mnt, sub)); err != nil {
				subvolErr = fmt.Errorf("abimg: create subvolume %s: %w", sub, err)
				break
			}
		}
		if _, err := r.Run(ctx, "umount", mnt); err != nil {
			if subvolErr != nil {
				return fmt.Errorf("%w (and umount failed: %v)", subvolErr, err)
			}
			return fmt.Errorf("abimg: umount %s: %w", mnt, err)
		}
		return subvolErr
	default:
		return fmt.Errorf("abimg: unsupported var filesystem %q", fs)
	}
}
