# 0014 — Port fisherman's secure-install (schema-1) into firn's bootc pipeline

- **Status:** Proposed
- **Date:** 2026-08-12

## Context

- firn installs bootc images with a plain `bootc install to-filesystem`
  and stops ([ADR-0012](0012-bootc-install-from-ram-installer.md)). Under
  UEFI Secure Boot that produces an unbootable system: the firmware
  rejects the boot chain (`BdsDxe: … Access Denied`). This is the Phase 8
  gap ([roadmap](../plans/roadmap.md)).
- snosi's secure bootc images (`io.snosi.bootc.secureboot-capable=true`,
  contract at `/usr/lib/snosi/bootc-secure.json`) boot **Debian shim
  (Microsoft-trusted) → snosi-MOK-signed systemd-boot → UKI**. Firmware
  trusts the second stage only once the snosi MOK is enrolled, which is a
  real human-in-the-loop step: the installer stages the MOK with `mokutil
  --import` (password-hashed) so **MokManager prompts the user to enroll
  it on first boot**. `bootc install` does none of this.
- fisherman already implements the whole path in `internal/secure`
  (`espstage.go` stages shim/second-stage/MokManager onto the ESP;
  `enroll.go` `StageMOK`; a runtime bootloader reconciler) plus a
  `contract.go` that exact-matches every field of `bootc-secure.json`.
- firn is retiring the dakota installer (its secure bootc installer +
  the lab's `run-secure-install-tests` lane), so firn must own the secure
  bootc path. firn already has half of `StageMOK`
  (`internal/abimg/mok.go`, wired only into the A/B `mok-stage` step).

## Decision

Port fisherman's secure-install "schema-1" into firn's bootc pipeline,
gated by `security.mok = "enroll"` on the bootc family (mirroring the
A/B `mok-stage` gate), adding exactly the two things firn omits:

1. **ESP secure chain** — after `bootc install`, stage Debian shim
   (`BOOTX64.EFI`), the MOK-signed systemd-boot (`grubx64.efi`), and
   MokManager (`mmx64.efi`) from the deployed image onto the ESP, keeping
   fisherman's `.signed`-suffix guard, shim-LAST ordering, `sbverify`
   checks, and sync-on-write (port `espstage.go` verbatim modulo the
   runner seam).
2. **MOK staging** — `abimg.StageMOK` against the target image's own
   `mok.crt`, as a bootc pipeline step, so first boot enrolls the MOK via
   MokManager.

Because the image `/usr` is composefs (not materialized under the target
mount), the shim/second-stage/MokManager/`mok.crt`/`bootc-secure.json`
are **extracted from the pulled image** (`podman … tar`, port
`install/secureroot.go`) **inside `runBootcInstall`, before its
container-store teardown** (the RAM-ISO store is ephemeral — ADR-0012).

**Do NOT touch TPM enrollment** — firn's signed-PCR-11 scheme
([ADR-0012], `EnrollTPMFromUKI`) is already complete and deliberately
diverges from fisherman's PCR-7 staging.

Contract verification is **minimal and drift-resistant**: read
`bootc-secure.json`, require `schema == 1`, the capability label
`io.snosi.bootc.secureboot-capable == "true"`, and the `secure_boot`
shape (`shim=debian`, `second_stage=mok-signed-systemd-boot`,
`mok_manager=MokManager`). Do **not** re-pin `bootc_version` /
`minimum_versions` — the live contract (1.16.7) already exceeds
fisherman's hard-pinned 1.16.3, proving that path is a maintenance trap.

Ship **no runtime reconciler**: it is image-owned
(`snosi-bootc-bootloader-reconcile.service`, self-gating on the
contract), so firn's obligation is only that the ESP it stages is
byte-consistent with the image's second stage.

Extend recipe `security.mok` / `mok_password_file` to the bootc family
(today `abOnlyKeys`), with the schema change landing in
`docs/specs/recipe-schema.md` in the same commit.

Once firn's bootc + Secure Boot lab lane is green on a real MokManager
enrollment, **retire dakota's secure installer and the
`run-secure-install-tests` lane**, replaced by firn's own coverage.

## Consequences

- firn becomes the single installer for the secure bootc path; the
  bootc + Secure Boot matrix cells held out today
  (`argo/firn-install-test.yaml` PENDING) can be re-enabled on the real
  enrollment path (no `virt-fw-vars` pre-seed).
- New host-tool dependencies on the installer medium: `sbverify`
  (`sbsigntool`) and (already present) `mokutil`, `openssl` — declared as
  step `Tools` so preflight fails closed if absent.
- The secure-artifact extraction is coupled to `runBootcInstall`'s store
  lifetime; a standalone step after it would find an empty RAM store.
  This is the highest-risk part and must be proven on the nested-VM E2E,
  not just fake-runner tests.
- MokManager first-boot enrollment is an unavoidable human step (type the
  one-time password). Unattended media cannot complete it; the lab must
  drive MokManager or assert genuine `mokutil` staging.
- `mokutil --generate-hash=<password>` puts the password on argv for one
  invocation (mokutil has no stdin path) — firn's `mok.go` already
  carries this honest note; it is inherent, not firn-introduced.
- `mok` becomes valid on bootc recipes; the fail-closed validator gains
  bootc + Secure-Boot rules parallel to the A/B ones.

## Alternatives considered

- **Port fisherman's full exact-match `contract.go`:** rejected — it
  hard-pins versions and would reject current images (1.16.7 vs 1.16.3).
- **Ship a firn bootloader reconciler:** rejected — the reconciler is a
  runtime component of the installed OS, image-owned; firn is a one-shot
  installer.
- **Keep dakota's secure installer:** rejected — firn is retiring dakota;
  two installers for the secure path is the state we are leaving.
- **Skopeo `oci:` extraction of secure artifacts:** rejected — firn
  deliberately avoids the cosign/oci-transport machinery ([ADR-0012]);
  `podman … tar` from the pinned containers-storage image suffices.

## References

- Builds on: [ADR-0012](0012-bootc-install-from-ram-installer.md),
  [ADR-0004](0004-single-installer-scope-and-support-matrix.md).
- Plan: [roadmap Phase 8](../plans/roadmap.md).
- Shapes: [specs/recipe-schema.md](../specs/recipe-schema.md),
  [progress-protocol.md](../specs/progress-protocol.md).
- Ports: fisherman `internal/secure/{espstage,enroll,contract}.go`,
  `internal/install/secureroot.go`; snosi `shared/bootc-secure`
  (`bootc-secure.json`, the reconciler).
