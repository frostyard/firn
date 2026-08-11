# 0010 — One installer ISO for all image families, built in the snosi repo

- **Status:** Accepted
- **Date:** 2026-08-11

## Context

- [ADR-0004](0004-single-installer-scope-and-support-matrix.md) made
  firn the single installer *binary* for both snosi image families, but
  the roadmap still planned two installer media: the native-installer
  ISO (A/B path) and the snow live ISO's installer (bootc path) — one
  firn, two boats.
- A separate validation session (2026-08-11) confirmed that a single
  installer ISO can carry everything both families need — the bootc
  path's podman/skopeo tooling and the A/B path's stream/enroll tooling
  — and drive both install flows from one firn.
- snosi already owns all image and media build infrastructure (mkosi,
  repart definitions, publication contracts); the current A/B installer
  lives at `snosi/shared/native-installer`.

## Decision

Snosi builds **one installer ISO** for all snosi image families, in the
snosi repository, as the successor to `shared/native-installer`. It
ships firn as the only installer, with firn's TUI as the kiosk
(ADR-0007) and the recipe's `image.family` selecting the install flow.

The repo boundary is: **firn ships a binary, a kiosk systemd unit, and
contracts** (recipe schema, progress protocol); **snosi ships media**
— ISO assembly, the tool payload both paths need, the seeded flatpak
repo (ADR-0006's media obligation), the MOK certificate, and the
pubring. Firn stays media-agnostic.

## Consequences

- One ISO to build, publish, sign, and test instead of two; the
  native-installer ISO and the live ISO's installer role both converge
  into it. (Whether a live *demo* ISO continues to exist is snosi's
  call — it is no longer an installer.)
- The single medium must carry both families' tool payloads
  (podman/skopeo/bootc-adjacent for bootc; xz/gpgv/cryptsetup/
  systemd-cryptenroll/mokutil for A/B) — firn's step-declared tool
  preflight is the contract for what must be present.
- The TUI wizard offers the full catalog: family/image selection is an
  in-wizard choice, not a which-ISO-did-you-boot choice.
- ISO build/E2E automation lives in snosi; firn's repo contributes the
  binary artifact and its own E2Es (loop-device and nested-VM), plus
  the contracts snosi's ISO tests consume.
- Phase 7's Done-when narrows to a single medium (roadmap updated in
  this change).

## Alternatives considered

- **Two ISOs (the prior plan):** double the media builds, signing, and
  boot-test matrix for zero user benefit once one installer drives both
  flows; rejected by validation.
- **Building the ISO in the firn repo:** media assembly needs snosi's
  mkosi/publication machinery and trust material; duplicating that
  boundary in firn couples the installer to infrastructure it
  otherwise never touches; rejected.

## References

- Shapes: [plans/roadmap.md — Phase 7](../plans/roadmap.md),
  [design/architecture.md](../design/architecture.md)
- Builds on: [ADR-0004](0004-single-installer-scope-and-support-matrix.md),
  [ADR-0006](0006-install-time-offline-first-flatpaks.md),
  [ADR-0007](0007-tui-only-frontend-single-binary.md)
