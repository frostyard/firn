# 0016 — Propose a version-2 bootc-only recipe contract

- **Status:** Accepted
- **Date:** 2026-09-24

## Context

Firn currently implements the dual-family [version-1 recipe schema](../specs/recipe-schema.md#rules):
`image.family` is explicit, and rule 7 increments `version` for a breaking
whole-schema change. `internal/recipe/recipe.go` sets `SchemaVersion = 1`;
`internal/recipe/validate.go` rejects version 2. `steps.Assemble` still selects
either bootc or A/B. The wizard emits `recipe.SchemaVersion` and persists the
exact reviewed bytes. A valid `/etc/firn/catalog.json` replaces the built-in
catalog wholesale; invalid, empty or unreadable overrides warn and fall back
to built-ins that include A/B. Neither the built-ins nor the present contract
provides a confirmed Sundog reference and trust key.

[ADR-0015](0015-bootc-only-installer-scope.md) proposes a post-2026-09-30
bootc-only scope, not a calendar-triggered change in v1 validity. Existing
bootc-v1 automation, older ISO binaries, and the shipped snosi catalog make
the version boundary a migration and media-compatibility decision, not just
a validator switch.

## Decision

**Proposed contract for architectural review, not operative:** use `version = 2`
for a bootc-only whole-schema contract. Require `image.family = "bootc"` even
though only one family is supported; never infer family from `ref` or version.
Retain the v1 bootc fields and their constraints, including explicit
`security.encryption`, machine-aware MOK and TPM requirements, file/path
checks, and wizard/headless parity. Reject unknown fields, enums and
A/B-only fields rather than ignoring them. Version 2 rejects `family = "ab"`
and A/B-only `product`, `origin`, `release`, `var_filesystem`,
`var_subvolumes`, and `recovery_key_out`. Neither the operative v1 spec nor
the runtime implements these examples yet:

```toml
# Proposed v2, UEFI with Secure Boot off; no TPM required.
version = 2

[image]
family = "bootc"
ref = "ghcr.io/frostyard/snow:latest"
cosign_pub_key = "/usr/lib/snosi/cosign.pub"

[target]
disk = "/dev/disk/by-id/EXAMPLE-DISK"
filesystem = "btrfs"

[security]
encryption = "none"

[system]
hostname = "snow-install"
```

```toml
# Proposed v2, UEFI with Secure Boot on and TPM present.
# The referenced key/password files must exist at validation time; secret
# files must be regular and not world-readable. A human must complete MOK
# enrollment on first boot.
version = 2

[image]
family = "bootc"
ref = "ghcr.io/frostyard/snow:latest"
cosign_pub_key = "/usr/lib/snosi/cosign.pub"

[target]
disk = "/dev/disk/by-id/EXAMPLE-DISK"
filesystem = "ext4"

[security]
encryption = "tpm2-luks-passphrase"
passphrase_file = "/run/firn/bootc-passphrase"
mok = "enroll"
mok_password_file = "/run/firn/mok-password"

[system]
hostname = "snow-secure"
```

References and paths above are illustrative, not a new product catalog or
authorization to install. The cosign key must exist and match the published
image; secret files must exist with appropriate permissions. A current Firn
rejects both examples with a version error (`SchemaVersion = 1`).

**Proposed rollout (requires a separate reviewed post-2026-09-30 release):**

1. First accept v2 bootc and, temporarily, unchanged v1 bootc with the same
   v1 bootc validation rules. For every accepted bootc-v1 install, surface a
   documented migration warning (proposed text: `bootc recipe version 1 is
   deprecated; change version to 2 after validating the bootc fields before
   the published compatibility end date`). Do not rewrite the submitted or
   reviewed recipe, and do not silently upgrade its version. Specify the
   stable warning code and protocol behavior alongside implementation.
2. Reject A/B-v1 during recipe validation, **before pipeline assembly or any
   destructive work**, with a diagnostic naming `image.family` (proposed text:
   `image.family: A/B version 1 is unsupported by this bootc-only Firn; no
   conversion was performed; use a pre-transition installer only where
   separately authorized`). Do not route it to the bootc backbone. Unsupported
   versions, including future versions, fail closed with a version diagnostic
   before assembly or writes: no auto-downgrade, guessing, or fallback to v1.
3. Recommend a **90-day bootc-v1 compatibility window measured from that
   release**, not from 2026-09-30. Record the actual release date and exact
   expiry date before rollout; neither is set by this proposal. After expiry,
   reject bootc-v1 before assembly with an instruction to validate its bootc
   fields and migrate to version 2. The expiry requires separately reviewed
   implementation and release work; a date alone cannot flip a validator.

Inventory bootc-v1 generators and stored recipes before rollout. Bulk or
manual migration first confirms `image.family = "bootc"`, checks every field
against the v2 bootc contract and environment, then changes **only**
`version = 1` to `version = 2`. Preserve image `ref`, `target_ref`, cosign
trust-key path, disk, filesystem, security choices, and secret-file paths;
ensure those files exist in the target installer environment. Never infer a
family or convert A/B image identity, layout, trust, or secrets to bootc.
Revalidate on the intended new medium before installation. In mixed-version
fleets old Firn rejects v2; retain authorized v1 copies for old media while
needed, and ensure automation chooses recipes for the installed Firn version.
Old media is not presumed to be an authorized or safe production workaround.

The future bootc-only wizard should emit v2 for new recipes, using the same
headless validator; its review bytes remain the exact persisted `recipe.toml`
and in-process install input (`assembleRecipe` currently selects
`recipe.SchemaVersion`; `marshalAssembled` preserves reviewed bytes). The
wizard must not show A/B. `checkCatalog` should reject an override containing
any A/B entry rather than filter it; `loadCatalogFrom` should warn explicitly
for invalid, empty or unreadable overrides and use a **bootc-only** built-in
fallback (a missing override also uses that fallback). Valid overrides still
replace the fallback wholesale but may only advertise supported bootc entries.
The snosi-shipped `/etc/firn/catalog.json` and ISO tool/trust payload must be
updated together with Firn's contract so neither override nor fallback can
reintroduce A/B. Odrade coordinates; Murbella owns snosi publication and
confirmation of image refs/keys. Do not invent a Sundog ref/key; resolve its
missing built-in entry and Snowfield's support/catalog scope before rollout.

## Consequences

- Version 2 honors rule 7's whole-schema guarantee, at the cost of migrating
  bootc generators and temporarily testing two supported versions. Existing
  A/B-v1 automation stops working on the *future* bootc-only binary; it
  remains valid on current v1 Firn. Warning, expiry and rejection behavior
  must be specified and tested in the implementing code/spec/protocol changes.
- Rollback to an older binary cannot consume v2 recipes. Keep versioned media,
  recipe copies and compatibility decisions aligned; a rollback may need
  separately authorized old media and must not be treated as production
  permission. Expiring v1 needs advance notice and a controlled upgrade plan.
- [Phase 7](../plans/roadmap.md#phase-7-becoming-the-only-installer-medium-cross-repo-in-progress)
  still has an unmet published-ISO, real-hardware **both-family** criterion;
  this proposal neither erases it nor claims it completed. [Phase 8](../plans/roadmap.md)
  lab Secure Boot proofs use host-side MOK injection, not actual first-boot
  MokManager enrollment. [Phase 9](../plans/roadmap.md#phase-9-proposed-post-cutoff-bootc-only-transition-bounded-cross-repo-not-started)
  still needs published-ISO hardware/security-cell qualification, including
  actual enrollment where applicable, and a separate sole-path decision.
- Brian and Ben must decide whether to approve v2 rather than a rule-7
  exception; the release trigger/date, exact 90-day expiry, warning and
  diagnostic policy, migration inventory, and retirement criteria remain
  open. Murbella must confirm catalog/image/key scope and ISO/tool changes.
  No production writes, post-cutoff code changes, changes to current v1
  validity, or claim of sole-production readiness are authorized here.

## Alternatives considered

- **Accept bootc-v1 indefinitely but reject A/B-v1:** minimizes immediate
  bootc automation migration and avoids mixed bootc versions, but changes
  what `version = 1` means. It requires an **expressly approved exception**
  to recipe-schema rule 7's breaking-change version guarantee, splits v1
  acceptance by binary release, and leaves tooling/older-media compatibility
  ambiguous: an old Firn still accepts A/B-v1 while the new one does not.
  Avoids a bootc-v1 expiry, but rollback still needs media and support-policy
  coordination. Not recommended without that deliberate exception.
- **Reject all v1 immediately:** simpler future validator, but breaks every
  bootc-v1 automation caller on upgrade with no bounded migration runway.
- **Silently map A/B-v1 or infer family from `ref`:** image identity, disk
  layout and trust are not interchangeable; this defeats fail-closed review.

## References

- Prospective design and plan: [Firn architecture](../design/architecture.md#prospective-post-cutoff-scope),
  [roadmap Phase 9](../plans/roadmap.md#phase-9-proposed-post-cutoff-bootc-only-transition-bounded-cross-repo-not-started).
- Current contract (unchanged while Proposed): [recipe schema v1](../specs/recipe-schema.md#rules).
- Builds on: [ADR-0015](0015-bootc-only-installer-scope.md),
  [ADR-0005](0005-toml-recipe-model.md),
  [ADR-0010](0010-single-installer-iso-in-snosi.md).
