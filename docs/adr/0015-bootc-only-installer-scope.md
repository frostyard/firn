# 0015 — Propose a post-cutoff bootc-only installer scope

- **Status:** Proposed
- **Date:** 2026-09-24

## Context

Firn currently installs bootc OCI and native A/B disk images through one
binary and one version-1 TOML recipe contract ([ADR-0004](0004-single-installer-scope-and-support-matrix.md),
[ADR-0005](0005-toml-recipe-model.md)). `steps.Assemble` still selects either
backbone; the wizard's valid `/etc/firn/catalog.json` override replaces its
built-ins wholesale, while an unreadable or invalid override warns and falls
back to built-ins that include A/B. The built-ins do not include Sundog.
The [recipe schema](../specs/recipe-schema.md#rules) accepts both families in
version 1 and requires a version increment for breaking changes (rule 7).

The proposed post-2026-09-30 product scope is bootc-only, but a date alone
does not change Firn's contract or retire A/B. [Phase 7](../plans/roadmap.md#phase-7-becoming-the-only-installer-medium-cross-repo-in-progress)
still requires installs of **both** families from the published snosi ISO on
real hardware. Nested-VM, fixture and lab results do not complete that gate.
Secure Boot lab proofs with host-side `virt-fw-vars` injection do not prove a
person can complete first-boot MokManager enrollment.

## Decision

**Proposed for Brian and Ben's architectural review, not approved or in
force:** after 2026-09-30, target a bootc-only Firn installer. Retain the
UEFI-only floor (not BIOS), the direct bootc path (not NBC), shared MOK/TPM
helpers needed by bootc, disk partitioning and LUKS, cosign verification,
the kiosk, the progress protocol and existing security interfaces. Do not
broaden the contract to ZFS root or imply arm64 qualification. Preserve
explicit encryption and Secure Boot choices, TPM-absent/passphrase modes,
preflight disk refusal, and install-time flatpak behavior.

**Proposed recipe migration choice for Brian and Ben to decide:** define a
new recipe version for the bootc-only contract; explicitly migrate existing
bootc-v1 automation and wizard output to that version under a documented
compatibility/window policy to be approved before coding. After that
policy and implementation land, reject A/B-v1 with a specific diagnostic
rather than silently converting it into bootc or continuing to advertise
it. Decide the migration mechanism and compatibility window during the
approval/inventory gate; this proposal does not make bootc-v1 invalid now.
Accepting bootc-v1 indefinitely while rejecting A/B-v1 *without* a version
increment would require an explicit exception to schema rule 7; it is not
an implicit interpretation of v1.

No A/B support is removed automatically on 2026-09-30. Remove A/B-only
paths and dependencies only in separately reviewed implementation changes
after the new contract, diagnostics, wizard and catalog behavior agree.
Odrade coordinates the cross-repo transition; Murbella owns snosi's
published installer ISO and shipped catalog, including product refs and
trust keys. Firn retains the binary, kiosk and interface contracts.
Production installation remains subject to Brian's separate authorization.

## Consequences

- Existing bootc-v1 generators need an explicit migration path; A/B-v1
  callers need an actionable rejection rather than silent reinterpretation.
  Until approval and code/spec changes, v1 remains valid for both families.
- Retiring A/B requires coordinated fail-closed recipe validation, assembly,
  wizard/catalog override and fallback changes, A/B-only tool removal and
  snosi media/catalog updates. The override must not reintroduce an
  unsupported family, including on invalid/unreadable fallback.
- The sole-path decision is blocked on published-ISO, real UEFI x86-64
  hardware evidence, actual MokManager enrollment where applicable and
  separate production authorization. VM/fixture/lab evidence supplements,
  but does not replace, that qualification or retrospectively finish Phase 7.

## Alternatives considered

- **Keep dual-family support:** avoids migration but extends the A/B
  validation, tool and publication surface beyond the proposed scope.
- **Accept bootc-v1 but reject A/B-v1 in place:** reduces immediate bootc
  migration but breaks rule 7's whole-schema version guarantee unless
  explicitly excepted; not the recommended contract.
- **Convert A/B-v1 to bootc implicitly:** image identity, layout and trust
  differ; silent conversion could target the wrong disk or security policy.
- **Treat the cutoff or VM results as retirement approval:** neither
  changes the implemented contract nor proves the published ISO on hardware.

## References

- Prospective design and plan: [Firn architecture](../design/architecture.md#prospective-post-cutoff-scope),
  [roadmap Phase 9](../plans/roadmap.md#phase-9-proposed-post-cutoff-bootc-only-transition-bounded-cross-repo-not-started).
- Current contracts (unchanged while Proposed): [recipe schema v1](../specs/recipe-schema.md),
  [progress protocol v1](../specs/progress-protocol.md).
- Builds on: [ADR-0004](0004-single-installer-scope-and-support-matrix.md),
  [ADR-0005](0005-toml-recipe-model.md),
  [ADR-0010](0010-single-installer-iso-in-snosi.md),
  [ADR-0012](0012-bootc-install-from-ram-installer.md),
  [ADR-0014](0014-port-secure-install-schema-1-for-bootc.md).
