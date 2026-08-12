// Ported from frostyard/fisherman (GPL-3.0-only),
// fisherman/internal/install/storage_driver.go (filesystemType,
// defaultStorageSpaceConstrained).

package bootcimg

import (
	"fmt"
	"syscall"
)

// StorageSpaceConstrained reports whether podman's default image store
// lives on a memory-backed filesystem (tmpfs/ramfs/overlayfs) where a
// multi-gigabyte bootc image pull would exhaust RAM before it finishes
// unpacking. This is the single-installer ISO's situation: the whole
// rootfs is unpacked into tmpfs, so /var/lib/containers is RAM-backed
// and the image unpack dies with ENOSPC partway through the last layers
// (observed live: cayo pull failed at "write .../solidity.vim: no space
// left on device", 2026-08-12).
//
// When true, the bootc step redirects the image store (and bootc's
// deployment scratch) onto the target disk instead. When false — a host
// whose /var/lib/containers is already disk-backed, e.g. the loop-device
// E2E — nothing is redirected and any pre-pulled image stays visible.
func StorageSpaceConstrained() bool {
	for _, p := range []string{"/var/lib/containers", "/var"} {
		if fsType, err := filesystemType(p); err == nil {
			return fsType == "tmpfs" || fsType == "ramfs" || fsType == "overlayfs"
		}
	}
	return false
}

// filesystemType returns the filesystem type of the given path using
// statfs, mapping the fstype magic number to a name. Unknown magics are
// returned as "unknown(0x...)". See include/uapi/linux/magic.h.
func filesystemType(path string) (string, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return "", err
	}
	fsTypes := map[int64]string{
		0x9123683e: "btrfs",
		0xef53:     "ext4", // ext2/ext3/ext4 share this magic
		0x58465342: "xfs",
		0x794c7630: "overlayfs",
		0x01021994: "tmpfs",
		0x858458f6: "ramfs",
		0x6969:     "nfs",
		0x9660:     "isofs",
	}
	if name, ok := fsTypes[int64(st.Type)]; ok {
		return name, nil
	}
	return fmt.Sprintf("unknown(0x%x)", st.Type), nil
}
