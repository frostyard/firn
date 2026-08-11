# 0008 — /var filesystem choice for A/B installs

- **Status:** Accepted
- **Date:** 2026-08-11

## Context

- On the A/B path the root filesystem is structurally fixed: erofs
  sealed with dm-verity at image build time, its roothash carried in
  the signed UKI. The installer streams it and must not (and cannot)
  vary it.
- `/var` is different: it is the one partition the installer itself
  formats (LUKS2 by default, then mkfs) and grows. `snosi-install`
  hardcodes ext4 and asserts ext4 during its grow step.
- The bootc path offers filesystem choice including btrfs with
  subvolumes ([ADR-0005](0005-toml-recipe-model.md)); users of the A/B
  path get no equivalent for the writable half of their system, where
  `/var/home` lives.
- ADR-0005 described the A/B `[target]` section as accepting "only what
  is genuinely variable"; the `/var` filesystem is genuinely variable —
  the installer creates it either way.

## Decision

The recipe's `[target]` section gains two A/B-only fields:

- `var_filesystem` — `"ext4"` (default) or `"btrfs"`. Not
  security-relevant, so a default is permitted (ADR-0004's
  explicitness rule covers security choices only).
- `var_subvolumes` — with btrfs, create **nested** subvolumes `home`
  and `snapshots` inside `/var`. Nested subvolumes appear as ordinary
  directories to the booted system, so the image's mount logic
  (gpt-auto discovery of the `var` partition) needs no changes and no
  mount units are seeded.

Grow logic becomes filesystem-aware (`resize2fs` for ext4,
`btrfs filesystem resize max` after a scratch mount for btrfs),
replacing `snosi-install`'s ext4 assertion.

## Consequences

- A/B users get snapshot-capable `/var` and `/var/home` (snapper et
  al. work against the nested subvolumes) without any image-side or
  update-path change.
- The subvolume set intentionally differs from the bootc path's
  `@`/`@home`/`@snapshots` top-level convention: on A/B the mounted
  `/var` *is* the default subvolume, so nesting is the layout that
  avoids new mount machinery. The recipe spec documents both shapes.
- The `/var` grow and format steps carry per-filesystem branches and
  their E2E matrix doubles for the A/B path.
- ADR-0005's "(target disk)" parenthetical is refined, not reversed:
  `[target]` on A/B now admits disk plus the `/var` filesystem —
  still nothing the sealed image fixes.

## Alternatives considered

- **Top-level `@`-style layout with mount units seeded via the
  `/etc`-overlay:** matches bootc naming but couples the installer to
  the image's mount configuration; rejected for the zero-image-change
  nested layout.
- **Also offering xfs/zfs for `/var`:** no current demand; the field is
  an enum and can grow by spec change later.
- **Btrfs for the A/B root:** structurally excluded — the erofs +
  dm-verity root is the design (integrity, signed roothash,
  image-identical A/B updates), not a filesystem preference.

## References

- Shapes: [specs/recipe-schema.md](../specs/recipe-schema.md),
  [design/architecture.md](../design/architecture.md)
- Builds on: [ADR-0004](0004-single-installer-scope-and-support-matrix.md),
  [ADR-0005](0005-toml-recipe-model.md)
