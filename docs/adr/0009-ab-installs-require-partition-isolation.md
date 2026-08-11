# 0009 — A/B installs run only against an isolated partition namespace

- **Status:** Accepted
- **Date:** 2026-08-11

## Context

The A/B install path streams a complete snosi whole-disk image onto the
target and then performs partition surgery on it (`sfdisk --relocate`,
`blockdev --rereadpt`, `udevadm settle` — see
[abimg](../../internal/abimg/) and
[design/architecture.md](../design/architecture.md#the-ab-path)). That
image's partitions carry the **Discoverable Partitions Spec type GUIDs
and snosi labels** (`esp`, `var`, root/verity slots) that every snosi
A/B system uses — by construction, since it *is* a snosi A/B image.

On 2026-08-11 an A/B end-to-end test wrote such an image to a
**host-visible loop device** (`losetup --partscan`) on a development
machine that is itself a snow-ab system, then triggered the partition
rescan. The host's own udev/systemd reacted to the freshly-appearing
discoverable `var`/root partitions and tore down the live graphical
session. The machine stayed up; the session did not. The bootc path
never exhibits this — it creates fresh, generically-typed partitions,
not a clone of the host's discoverable layout.

## Decision

Firn's A/B install operations, in testing and in production, run only
where the target disk's partition namespace is **isolated from any
running snosi system**:

- The A/B end-to-end test (`test/e2e-ab.sh`) installs **inside a
  throwaway VM** that owns the target as a virtio disk. The host kernel
  never scans the image's partitions. The harness refuses to fall back
  to a host-visible loop device.
- In production the isolation is inherent: firn runs from installer
  media against a bare target disk, not as a guest process on a
  co-resident snosi host.
- Any tool or harness that must attach an A/B image to a running snosi
  host for inspection MUST NOT expose its partitions to the host kernel
  (no `--partscan`, no partition mounts); inspect inside a VM instead.

This is a property of the A/B image (self-describing discoverable
partitions), not a firn defect, so firn does not attempt to defuse it by
rewriting partition types mid-install — doing so would break the very
gpt-auto discovery the installed system boots by.

## Consequences

- The A/B E2E is heavier than the bootc E2E (it boots a nested
  installer VM plus a verify VM) but is safe to run on a snosi A/B
  developer machine, which is the common case here.
- Encrypted-`/var` + TPM enrollment is exercised at the argv level by
  unit tests rather than in the nested E2E: TPM sealing binds to the
  installing machine's vTPM/PCRs, which the separate verify-boot VM
  cannot reproduce. Full encrypted-boot fidelity arrives when installs
  run from firn's own live ISO inside one VM (roadmap Phase 7).
- Contributors need a working KVM stack for the A/B E2E; the harness
  documents the requirement and refuses unsafe host-visible runs.

## Alternatives considered

- **Loop device with `--partscan` on the host:** simplest, and how the
  first harness worked — it is exactly what killed the session.
  Rejected.
- **Rewrite the image's partition type GUIDs during install so the host
  ignores them:** would neutralize the collision but also break the
  installed system's own gpt-auto root/var discovery, which depends on
  those exact types. Rejected.
- **Mount the installed partitions on the host to verify:** same
  hazard as partscan (the mount forces the host to claim the
  discoverable partitions). Verification happens inside a VM instead.

## References

- Shapes: [design/architecture.md](../design/architecture.md)
- Builds on: [ADR-0004](0004-single-installer-scope-and-support-matrix.md),
  [ADR-0008](0008-ab-var-filesystem-choice.md)
