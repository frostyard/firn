# 0013 — btrfs /var on A/B requires a filesystem-agnostic image mount

- **Status:** Proposed
- **Date:** 2026-08-12

## Context

- [ADR-0008](0008-ab-var-filesystem-choice.md) added `var_filesystem =
  "ext4" | "btrfs"` for the A/B path and chose a **nested-subvolume**
  layout *specifically to need no image changes*: "the image's mount
  logic … needs no changes and no mount units are seeded" (0008 Decision),
  and it rejected the top-level layout "for the zero-image-change nested
  layout" (0008 Alternatives).
- That premise was false. The native A/B root image (`snosi
  shared/outformat/ab-root`) pins **ext4** for `/var` in three places,
  *independent of subvolume layout*:
  - the initrd `snosi-etc-overlay` module runs `mount -t ext4` and
    bundles only the ext4 kernel driver (`instmods overlay ext4`);
  - `/etc/fstab` carries `PARTLABEL=var /var ext4 … 0 2`.
- So every firn A/B install that honoured `var_filesystem = "btrfs"`
  produced a disk the image could not boot: the btrfs `/var` never
  mounted and the system dropped to emergency mode at
  `snosi-etc-overlay-initrd`. This was the real cause of the 2026-08-12
  firn AB matrix failures (misdiagnosed as a dm-verity stall; verity
  sets up cleanly in the serial log — the failure is the next step, the
  `/var` mount).
- ADR-0008's nested-subvolume choice did correctly avoid *new mount
  units*; what it missed is that the image hardcodes the `/var`
  *filesystem type* at the mount/fstab/driver level, which no subvolume
  layout changes.

## Decision

Deliver btrfs `/var` by making the A/B image's `/var` mount
**filesystem-agnostic**, not by retracting the feature: the initrd
autodetects the `/var` filesystem (libblkid) and carries the btrfs
driver, and `/etc/fstab` uses `auto` (fsck pass 2 retained — the base
image ships `btrfs-progs`, so `fsck.btrfs` is present as a no-op for a
btrfs `/var` while ext4 keeps its boot-time check). This is snosi PR #705.

btrfs `/var` on A/B therefore **requires an ab-root image at or after the
PR #705 build**. firn's validator continues to accept `var_filesystem =
"btrfs"`: correctness is delivered image-side, and firn does **not** gate
the feature in its recipe validator.

## Consequences

- btrfs `/var` (and `/var/home`) genuinely boots once the fixed image is
  published — the core feature ADR-0008 intended is actually delivered.
- **Compatibility floor.** firn with `var_filesystem = "btrfs"` against a
  *pre-#705* ab-root image still produces an unbootable disk. Because
  btrfs `/var` on A/B never booted, no existing install regresses; the
  exposure is new installs built on stale media, mitigated by publishing
  the fixed image before the feature is promoted, and by the lab's
  btrfs-var AB cell gating on the real boot.
- ext4 `/var` is unaffected: autodetection still mounts it, and fsck
  pass 2 is retained, so its boot-time check is preserved.
- ADR-0008's **"zero-image-change" rationale is superseded**; its
  *decision* (offer `var_filesystem` / `var_subvolumes`, nested
  subvolumes) stands and is orthogonal to this fix.
- **No firn validator gate.** A blanket reject of btrfs `/var` would
  block the very feature we are delivering and its own end-to-end
  verification, and firn cannot detect an image's `/var` mount capability
  at validate time. If interim protection is later judged necessary, the
  correct mechanism is an image-advertised capability the installer
  checks — deferred, not built here.

## Alternatives considered

- **Gate btrfs `/var` in firn's validator until images are fixed:**
  rejected. It blocks the feature and its verification, protects no
  regression (btrfs `/var` never booted), and becomes a footgun once
  images are fixed because firn cannot tell image versions apart. (This
  reverses the interim-gate half of the 2026-08-12 "both" plan, on the
  reasoning above — surfaced for gating.)
- **Abandon btrfs `/var`, keep ext4-only for A/B:** rejected — a
  snapshot-capable `/var`/`/var/home` is a core firn feature.
- **Image-advertised btrfs-var capability + firn preflight check:**
  deferred — the right long-term design, but more machinery than the
  imminent image fix warrants.

## References

- Supersedes the zero-image-change premise of
  [ADR-0008](0008-ab-var-filesystem-choice.md); builds on
  [ADR-0004](0004-single-installer-scope-and-support-matrix.md).
- Shapes: [specs/recipe-schema.md](../specs/recipe-schema.md),
  [design/architecture.md](../design/architecture.md)
- Implementation: snosi PR #705 (`shared/outformat/ab-root` /var mount).
