# 0006 — Install-time, offline-first flatpak provisioning on both paths

- **Status:** Accepted
- **Date:** 2026-08-11

## Context

- [ADR-0004](0004-single-installer-scope-and-support-matrix.md) puts
  system flatpaks in firn's configuration surface for both image
  families.
- The two existing installers take opposite approaches. Fisherman
  (bootc): install-time hybrid — `post.CopyFlatpaks` copies system
  flatpaks from the live medium's own flatpak repo into the target's
  `var/lib/flatpak` (promoting user-only refs), then downloads anything
  missing over the network; the machine is fully provisioned at first
  login. `snosi-install` (A/B): defers entirely — it records the desired
  set in `first-boot.json`, and a `snosi-firstboot` unit on the
  *installed system* adds Flathub and installs a core set from the
  image's catalog on first boot.
- The first-boot model means the installer ships and maintains a
  component that lives on every installed system, and the user's first
  login happens on a machine still downloading its apps.
- Both target layouts expose a writable flatpak location at install
  time: the bootc deployment's `var/lib/flatpak`, and the A/B path's
  (possibly LUKS-opened) `/var` partition.

## Decision

Firn installs system flatpaks **at install time, offline-first, on both
paths**:

1. Flatpaks present in the installer medium's seeded repo are copied
   into the mounted target's flatpak installation (carrying over
   fisherman's `CopyFlatpaks` mechanics, including user-ref promotion).
2. Requested flatpaks not on the medium are downloaded during install,
   directly into the mounted target.
3. With no network, the install still succeeds with whatever the medium
   carries; missing flatpaks are reported in the install summary, not
   silently dropped.

The recipe expresses the set two ways, combinable: an explicit
`flatpaks = [...]` app-ID list, and `core_flatpaks = true` selecting the
image-defined core set where the image family publishes one (the A/B
catalog today). Firn installs no first-boot units and owns no components
on the installed system.

## Consequences

- The machine is fully provisioned at first login on both paths — a
  behavior change for A/B installs (today they finish provisioning after
  first boot).
- Firn has zero footprint on installed systems; there is no
  firn-firstboot unit to version, ship in images, or debug in the field.
  When firn replaces `snosi-install`, the flatpak role of
  `snosi-firstboot` retires with it (snosi-side change, tracked in the
  plan).
- Installs take longer when flatpaks must be downloaded, and the
  network-availability question moves to install time. The TUI's network
  step must actually matter (unlike `snosi-install`'s check-only page).
- Obligation on media builds: installer ISOs should seed a flatpak repo
  with the core set so the offline path is the common path, not the
  degraded one.
- "Report, don't fail" for unreachable flatpaks needs a visible summary
  surface in both the TUI and the machine-readable progress protocol.

## Alternatives considered

- **Install-time plus a first-boot fallback unit:** most robust against
  offline installs, but reintroduces an installer-owned component on
  every installed system for a corner case the summary report already
  covers; rejected.
- **First-boot only (`snosi-install` model everywhere):** fast installs,
  but regresses the bootc path, leaves first login incomplete, and
  requires shipping firn components inside every image; rejected.

## References

- Shapes: [specs/recipe-schema.md](../specs/recipe-schema.md),
  [design/architecture.md](../design/architecture.md)
- Builds on: [ADR-0003](0003-rewrite-fisherman-as-firn.md),
  [ADR-0004](0004-single-installer-scope-and-support-matrix.md),
  [ADR-0005](0005-toml-recipe-model.md)
