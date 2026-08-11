# 0004 — Single installer for all snosi images, UEFI floor, optional security features

- **Status:** Accepted
- **Date:** 2026-08-11

## Context

- Snosi ships two image families: bootc OCI images (snow, snowfield, cayo)
  and native A/B disk images (snow-ab, snowfield-ab, cayo-ab; whole-disk
  DDIs with an erofs + dm-verity root). Each family has its own installer
  today, and neither installer covers the other family
  ([ADR-0003](0003-rewrite-fisherman-as-firn.md)).
- The system-configuration surfaces of the two installers overlap but
  neither is complete. Fisherman (bootc): hostname, first user + groups,
  flatpak copying — but no locale, timezone, keyboard, or SSH keys.
  `snosi-install` (A/B): hostname, first user, locale, timezone, keyboard,
  root SSH authorized key, first-boot flatpaks — but no recipes, no user
  SSH key, no group selection.
- The snosi user base includes machines without a TPM and machines with
  Secure Boot disabled. The native A/B images are UEFI-only by
  construction: systemd-boot, UKIs, and Discoverable-Partitions root
  selection have no legacy-BIOS boot path, and adding one would be a
  snosi image-side change, not an installer change.
- `snosi-install` already refuses to default any security-relevant setting
  in non-interactive mode; fisherman validates fail-closed (it rejects
  configurations that would silently downgrade security rather than
  proceeding).

## Decision

Firn is the sole installer for **all** snosi image families: one binary
installs both bootc OCI images and native A/B disk images, behind one
configuration surface and one frontend.

**Hardware floor: UEFI is required.** Firn does not support legacy-BIOS
boot for any image family and refuses non-UEFI machines with a clear
diagnostic. Above that floor, the security features are all optional and
independently degradable:

- **Secure Boot optional.** When enabled, firn stages what the target
  needs (MOK enrollment on the A/B path); when disabled, it skips those
  steps and the installed system still boots.
- **TPM optional.** With a TPM: automatic unlock enrolled against the
  signed-PCR policy. Without: passphrase / recovery-key unlock only.
- **Encryption optional.** Both paths support unencrypted installs.
  Security-relevant settings are always **explicit**: interactive flows
  prompt for them, and non-interactive configurations that omit them are
  rejected rather than silently defaulted (either direction).

**Configuration surface: the union of both installers, on both paths.**
Every install, regardless of image family, can set: hostname, a first
user with password and group membership, system flatpaks, system locale,
system timezone, keyboard layout, an optional root SSH authorized key,
and an optional user SSH authorized key.

## Consequences

- Every configuration feature needs two mechanisms: writing into a bootc
  deployment's `/etc` versus seeding the A/B `/etc`-overlay upper in
  `/var`. The configuration spec must define each feature's semantics on
  both paths; a feature is not done until both are implemented.
- Firn must speak both trust systems: cosign-signed OCI references (bootc)
  and the OpenPGP-signed artifact index (A/B).
- BIOS-only machines are explicitly unsupported; this is a product
  boundary firn can state in its docs and preflight error, not a gap.
- The user SSH authorized key and per-path locale/timezone/keyboard/SSH
  support are new code on at least one path each — the union is a real
  feature matrix, not just glue.
- The explicit-security rule carries `snosi-install`'s posture to the
  bootc path too: recipes that say nothing about encryption become
  invalid, which is a behavior change from fisherman (where omitting
  `encryption` meant "none").

## Alternatives considered

- **BIOS support via the bootc path only:** steers a hardware class to one
  image family, splitting the support matrix and requiring grub2 BIOS
  work; rejected — UEFI-only keeps the matrix uniform.
- **BIOS support for everything:** requires snosi to grow a BIOS-bootable
  A/B image variant, a cross-repo commitment out of firn's control;
  rejected.
- **Mandatory encryption:** simpler matrix, but excludes real users
  (TPM-less machines where passphrase entry is impractical, throwaway or
  lab installs); rejected in favor of explicit choice.
- **Silent security defaults** (encrypt unless told otherwise, or the
  reverse): friendlier for quick starts, but both installers' histories
  show silent security decisions become incidents; rejected for
  explicitness.

## References

- Shapes: [design/architecture.md](../design/architecture.md),
  [specs/recipe-schema.md](../specs/recipe-schema.md)
- Builds on: [ADR-0003](0003-rewrite-fisherman-as-firn.md)
