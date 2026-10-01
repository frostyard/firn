// Ported from frostyard/snosi (GPL-3.0-only), shared/native-installer/tree/usr/libexec/snosi-install (seed_var, seed_first_user).

// File helpers shared by the deployment writer.
package sysconfig

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path with the given mode via a temp file
// in the same directory plus an atomic rename, so a crash mid-write never
// leaves a truncated account database (or any half-written config) behind.
//
// os.CreateTemp creates the temp 0600 (like mktemp). Renaming a 0600 temp
// straight over /etc/group is exactly how snosi's append_group_member once
// shipped a broken system (2026-07-17): /etc/group must stay 0644 or
// NON-root group-name resolution breaks entirely (getent group empty,
// `id -nG` fails) even though numeric memberships survive. The temp is
// therefore chmodded to the target mode BEFORE the rename, so no window
// exists in which the final path carries the wrong mode.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp for %s: %w", path, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename has succeeded
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmp, path, err)
	}
	return nil
}

// copySkel recursively copies the contents of src into dst, preserving each
// entry's own mode bits and symlink targets (the useful subset of `cp -a`;
// skel trees hold only files, directories, and symlinks — anything else is
// skipped). Ownership is fixed afterwards by the caller: DeploymentWriter
// runs `chown -R` on the home through its Runner.
func copySkel(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		info, err := e.Info() // lstat semantics: symlinks are not followed
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(s)
			if err != nil {
				return err
			}
			if err := os.Symlink(target, d); err != nil {
				return err
			}
		case info.IsDir():
			if err := os.Mkdir(d, 0o700); err != nil {
				return err
			}
			if err := copySkel(s, d); err != nil {
				return err
			}
			// Chmod explicitly: Mkdir's mode argument is masked by umask.
			if err := os.Chmod(d, info.Mode().Perm()); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			data, err := os.ReadFile(s)
			if err != nil {
				return err
			}
			if err := os.WriteFile(d, data, 0o600); err != nil {
				return err
			}
			if err := os.Chmod(d, info.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}
