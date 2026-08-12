# 0012 — Run bootc installs from the RAM installer over containers-storage

- **Status:** Proposed
- **Date:** 2026-08-11

## Context

- The single installer ISO ([ADR-0010](0010-single-installer-iso-in-snosi.md))
  boots **all-in-RAM**: the whole rootfs is unpacked into the initramfs
  (a `rootfs`/ramfs), and `/var` is tmpfs. There is no disk-backed
  scratch anywhere except the target disk being installed to.
- A bootc install runs `bootc install to-filesystem` via `podman run`,
  which must pull and unpack the OCI image before deploying it. Live
  testing in a nested VM (2026-08-11) found the RAM environment breaks
  this three ways, each hidden behind the last:
  1. **ENOSPC.** podman unpacks image layers into `/var/lib/containers`
     and stages downloaded blobs in `/var/tmp` — both tmpfs — so the
     pull dies partway ("write .../solidity.vim: no space left on
     device"), leaving the disk partitioned but with no OS.
  2. **`crun: pivot_root: Invalid argument`.** `pivot_root(2)` cannot
     pivot off the initramfs `rootfs`, so the bootc container cannot
     start at all — surfacing only once the ENOSPC was cleared.
  3. **bootc requires `/target` to contain only mount points.** Any
     disk-backed scratch has to live on the target root (the only
     disk-backed space), and a stray directory there fails bootc's
     empty-rootfs check ("Found empty directory: scratch").
- Fisherman solved the same live-media ENOSPC for its composefs path by
  redirecting podman's store with `--root` and **exporting the image to
  an OCI layout**, then running bootc with `--source-imgref oci:<path>`.
  That opens the image over the `oci:` transport, which a hardened snosi
  image's own `/etc/containers/policy.json` (reject-by-default, exception
  only for `containers-storage`) rejects — a secure-image
  signature-policy failure that cost a full day to unwind.

## Decision

Firn makes bootc installs work from the RAM installer with three
targeted fixes, applied together and **gated on detecting the RAM
environment** (`bootcimg.StorageSpaceConstrained`: `/var` or
`/var/lib/containers` is tmpfs/ramfs/overlayfs), so disk-backed hosts —
including the loop-device E2E — keep the unchanged path:

1. **Redirect storage to the target disk.** Bind-mount target-disk
   directories over `/var/lib/containers` and `/var/tmp` for the pull, so
   layers unpack and blobs stage on disk.
2. **Disable `pivot_root`.** Set `no_pivot_root` (MS_MOVE + chroot
   fallback) via a process-scoped `CONTAINERS_CONF_OVERRIDE` that merges
   over podman's defaults — no global config is touched.
3. **Hide the scratch from bootc.** The scratch tree lives under the
   mounted target root; bind its base onto itself so it presents as a
   mount point, which bootc treats as foreign and does not recurse into.
   Teardown unmounts recursively (`umount -R`) because podman leaves an
   overlay submount under the store, then removes the tree before the
   deployment is finalized.

Firn stays on the **`containers-storage` transport** throughout — the
same path the bootc loop-device E2E already validated against a hardened
image — and does **not** adopt fisherman's `skopeo`→OCI-layout redirect.

Verified end to end: cayo (unencrypted) and snow
(`tpm2-luks-passphrase` + `core_flatpaks` + user + groups) install from
the RAM ISO and produce bootable, encrypted disks.

## Consequences

- bootc installs work from the single installer ISO, closing the gap
  that made the ISO's bootc path effectively untested.
- Firn never enters the `oci:`-transport signature-policy trap, so
  hardened/secure images install without a `policy.json` override.
- The unpacked store and the deployment **coexist on the target during
  the install** (both freed of the store afterward). This costs transient
  space fisherman's store-drop reclaimed, so very small targets that
  fisherman would have fit could ENOSPC here; snosi's disk floor makes
  that acceptable. Revisit if a tighter target appears.
- Teardown must unmount recursively and before finalize; a missed
  unmount leaks the pulled image onto the installed disk (guarded, and
  reported as a non-fatal warning).
- `core_flatpaks` on composefs images can no longer read the core set
  from `/usr` at install time; firn falls back to an installer-embedded
  list ([ADR-0006](0006-install-time-offline-first-flatpaks.md)).

## Alternatives considered

- **Fisherman's `skopeo`→OCI-layout redirect:** rejected — it opens the
  image over the `oci:` transport, which hardened snosi images' policy
  rejects, reintroducing the secure-image debugging it cost fisherman.
- **`switch_root` the ISO into tmpfs so `pivot_root` works:** rejected
  for now — it is an ISO-build change in snosi, whereas `no_pivot_root`
  is self-contained in firn and works regardless of how the medium boots.
- **btrfs-subvolume scratch isolation** (a sibling subvolume outside the
  target's view): rejected — it only covers btrfs targets, while the
  self-bind mount-point trick is filesystem-agnostic.

## References

- Shapes: [design/architecture.md](../design/architecture.md) ("The
  bootc path"), [plans/roadmap.md](../plans/roadmap.md)
- Builds on: [ADR-0010](0010-single-installer-iso-in-snosi.md) (the
  all-in-RAM single ISO), [ADR-0003](0003-rewrite-fisherman-as-firn.md)
  (copy-with-attribution from fisherman),
  [ADR-0006](0006-install-time-offline-first-flatpaks.md) (the
  installer-embedded core flatpak set)
