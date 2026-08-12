# Plan: Firn roadmap

Delivers firn from empty repo to the sole installer for all snosi image
families ([ADR-0004](../adr/0004-single-installer-scope-and-support-matrix.md)),
implementing [design/architecture.md](../design/architecture.md) and the
[recipe schema](../specs/recipe-schema.md) /
[progress protocol](../specs/progress-protocol.md) specs. Phases are
ordered so every phase ends with something demonstrable in a VM.

## Phase 1 — Skeleton and contracts (small) — ✅ shipped 2026-08-11 (`545d831`)

- Repo scaffold: Go module, `LICENSE` (GPL-3.0 text) + `NOTICE`
  (attribution lineage) — required before any code is copied in
  ([ADR-0003](../adr/0003-rewrite-fisherman-as-firn.md)) — `just check`
  recipe, CI running it.
- `internal/recipe`: TOML load + fail-closed validation per the
  [recipe schema spec](../specs/recipe-schema.md), with fixture recipes
  for both families.
- `internal/progress`: event model + NDJSON emitter per the
  [progress protocol spec](../specs/progress-protocol.md).
- **Done when:** `firn validate <recipe>` accepts the spec's examples,
  rejects each rule violation with a distinct error, and `just check`
  is green in CI.

## Phase 2 — Step engine and preflight (small) — ✅ shipped 2026-08-11 (`f702b37`)

- `internal/pipeline`: step interface, assembly from a validated recipe,
  cleanup stack, dry-run
  ([design](../design/architecture.md#the-step-engine)).
- Preflight steps: UEFI check, step-declared tool checks, disk
  enumeration and refusal rules
  ([design](../design/architecture.md#preflight)).
- **Done when:** `firn install --dry-run` on recipes of both families
  prints the assembled step list and correct preflight verdicts (BIOS VM
  refused, busy disk refused) without touching any disk.

## Phase 3 — bootc path to fisherman parity (large) — ✅ shipped 2026-08-11 (`a67366d`; E2E: cayo boots to login with hostname/user/groups. Flatpak mechanics are covered by fake-runner tests; the E2E exercises an empty set since cayo ships no flatpak runtime — the full-matrix E2E lands in Phase 5)

- Copy/port fisherman's `disk`, `luks`, `install` (bootc), and `post`
  packages into steps, provenance headers intact: partitioning profiles,
  btrfs subvolumes, LUKS modes, `bootc install to-filesystem`,
  TPM first-boot staging, finalization
  ([design](../design/architecture.md#the-bootc-path)).
- `internal/sysconfig` deployment writer: hostname, user + groups,
  flatpak copy (fisherman parity set).
- E2E VM harness (adapt fisherman's bootcrew loop-device + QEMU
  approach).
- **Done when:** a recipe-driven install of a snow bootc image in the
  E2E VM boots to login with hostname, user, and flatpaks applied.

## Phase 4 — A/B path (large) — ✅ shipped 2026-08-11 (E2E: cayo-ab installed inside a nested VM boots + verifies over SSH. Install runs in a throwaway QEMU guest because the A/B image carries the host's own discoverable-partition layout — see the E2E script header and ADR-0009. Encrypted-var/TPM boot is unit-tested at the argv level; full encrypted boot arrives with the ISO in Phase 7)

- `internal/trust`: gpgv-verified index fetch, version resolution
  (incl. `release` pinning); manifest-derived minimum-size computation.
- Stream-write step (stream-then-verify with hardened failure path),
  layout validation, GPT relocate, `/var` grow/format/LUKS with
  filesystem choice and optional subvolumes
  ([ADR-0008](../adr/0008-ab-var-filesystem-choice.md)), TPM
  enrollment against the UKI `.pcrpkey`, MOK staging
  ([design](../design/architecture.md#the-ab-path)).
- `internal/sysconfig` overlay writer: hostname, user (Go
  reimplementation of account editing, fixture-tested), locale,
  timezone, keyboard, root SSH key — `snosi-install` parity.
- **Done when:** a recipe-driven A/B install of snow-ab in the E2E VM
  boots with encrypted `/var`, TPM auto-unlock, and the seeded user.

## Phase 5 — Full configuration matrix (medium) — ✅ shipped 2026-08-11 (both E2Es apply and SSH-verify hostname, user+groups+password, locale, timezone, keyboard, root+user SSH keys on booted systems; the snow-ab E2E additionally proves install-time flatpak download — org.gnome.Calculator lands via the firn-added flathub remote and appears in `flatpak list` on the booted GNOME system. The medium-copy path is fake-runner-tested; its on-ISO E2E is a Phase 7 item)

- Close the writer gaps so both writers implement every `[system]`
  feature: locale/timezone/keyboard/SSH keys on the deployment writer;
  user SSH key and groups on the overlay writer.
- Unified offline-first flatpak step on both paths, `core_flatpaks`,
  unreachable-set reporting
  ([ADR-0006](../adr/0006-install-time-offline-first-flatpaks.md)).
- **Done when:** an E2E matrix run applies every `[system]` field on
  both families and asserts each on the booted system.

## Phase 6 — TUI (medium) — ✅ shipped 2026-08-11 (tmux-driven E2E walks the real wizard and installs both families in nested VMs; booted disks verify over SSH; wizard-generated recipes pass `firn validate` as the reproduce-headless artifact. Wizard opens with the bootc-vs-A/B guidance page; kiosk units in dist/)

- Wizard flows per [ADR-0007](../adr/0007-tui-only-frontend-single-binary.md):
  recipe generation, in-process pipeline run, progress view,
  recovery-key and summary presentation, 80×24 legibility.
- Kiosk systemd unit for installer media (console + serial).
- **Done when:** a TUI-driven install completes on both families in the
  E2E VM, and the recipe it wrote reproduces the same install headless.

## Phase 7 — Becoming the only installer (medium, cross-repo) — ⏳ in progress

Proven so far (2026-08-11), **all merged 2026-08-12** (snosi #693,
first-setup #29):
- The single installer ISO **builds** from a new snosi profile
  (`snosi` branch `firn-installer`: `mkosi.profiles/firn-installer` +
  `shared/firn-installer/`) — 658M, all 33 preflight-contract binaries
  present, GTK/cage dropped, firn kiosk units wired.
- The **full-fidelity encrypted-boot E2E passes** (`snosi`
  `test/firn-installer-iso-test.sh`): the ISO boots to the firn kiosk,
  a `tpm2-luks` cayo-ab install runs from the medium, and rebooting the
  same VM (persistent swtpm) auto-unlocks `/var` via the TPM — the
  proof this phase was created to get. It caught a real bug (TPM
  enrollment targeted the mapper, not the LUKS partition; fixed).
- **first-setup slimmed to first-login only** (`first-setup` branch
  `first-login-only`): modes 1–2 removed, `core.json` contract kept.

Also done: snosi's dead first-boot-setup wiring removed
(`_snow-linux-live-setup` + service + the `Session=firstsetup`
AccountsService override) in the same `firn-installer` branch.
Correction: snowfield already ships `snow-first-setup` transitively via
`shared/composition/snow` → `shared/packages/snow` — no restore needed
(the earlier "snowfield lost it" note was a shallow-grep error).

Still to do:
- ✅ **ISO flatpak seeding (Option A) — done and proven.** The 23 apps
  of first-setup's `core.json` + GNOME runtime (1.9 GB) are staged into
  a flatpak installation, embedded as a 624 MB squashfs data area on
  the ISO (outside the RAM-unpacked initramfs), and mounted read-only
  at `/var/lib/flatpak` by a oneshot before the kiosk. Initramfs size
  unchanged.
- ✅ **on-ISO offline medium-copy E2E — passes.** `FIRN_ISO_FLATPAK=1`
  installs snow-ab with `core_flatpaks` and dl.flathub.org black-holed;
  all 23 apps land on the booted system from the seed alone, with the
  encrypted `/var` TPM-auto-unlocked.
- ✅ **snosi-firstboot's flatpak role retired** — merged in #693; firn
  owns install-time flatpaks, first-boot no longer installs them.
- ✅ **bootc installs from the all-in-RAM ISO — done and proven**
  ([ADR-0012](../adr/0012-bootc-install-from-ram-installer.md)). The RAM
  environment broke bootc three ways (tmpfs ENOSPC on the image
  unpack/blob-staging, `pivot_root` off the initramfs, and bootc's
  empty-`/target` check); firn redirects the store to disk, sets
  `no_pivot_root`, and self-binds its scratch into a mount point, staying
  on the `containers-storage` transport. Verified live in a nested VM:
  cayo and snow (`tpm2-luks-passphrase` + `core_flatpaks` + user +
  groups) install and produce bootable, encrypted disks. `core_flatpaks`
  on composefs now reads an installer-embedded core list
  (`/usr/share/firn/core-flatpaks.json`) since `/usr` is unreadable at
  install time.
- 🚧 **frostyard/lab suites** (final step, in progress): add a firn
  install-test matrix to lab's Argo-Workflows homelab harness — new
  `lab/argo/*.yaml` workflows modeled on `snosi-bootc-install-test.yaml`
  / `snosi-install-test.yaml`, booting the firn ISO on incus VMs and
  driving a matrix of secure-boot × encryption × image × family from
  nothing to installed-and-booted.
- 🚧 **retirement ADRs for fisherman and snosi-install** (in progress):
  written in frostyard/core (org-wide decision record), recording their
  supersession by firn.
- ✅ **review branches merged**: first-setup `first-login-only`
  (#29, released v0.4.0) and snosi `firn-installer` (#693) — the single
  installer ISO, embedded flatpak list, VGA-console visibility, and the
  man-db var-audit fix all landed on their mains.

## Phase 7 plan — Becoming the only installer (medium, cross-repo)

- **One installer ISO** for all image families, built in the snosi repo
  as the successor to `shared/native-installer`
  ([ADR-0010](../adr/0010-single-installer-iso-in-snosi.md)): ships
  firn + its kiosk unit, drops GTK4/Mesa/cage, carries both families'
  tool payloads (firn's step-declared preflight is the contract), seeds
  a flatpak repo, the MOK cert, and the pubring
  ([ADR-0006](../adr/0006-install-time-offline-first-flatpaks.md),
  [ADR-0007](../adr/0007-tui-only-frontend-single-binary.md)). The
  native-installer ISO and the live ISO's installer role both converge
  into it.
- **On-ISO staged-flatpak validation**: an E2E that installs from the
  ISO with the network cut (or restricted) and asserts the medium's
  seeded flatpaks land on the target via the tar-copy path — proving
  ADR-0006's offline-first promise, not just the download path
  (Phase 5 proved download; the medium-copy path currently has only
  fake-runner coverage).
- Full-fidelity encrypted-boot E2E: with the ISO in hand, the A/B E2E
  installs from it **inside one VM with a persistent swtpm**, so
  encrypted `/var` + signed-PCR-11 TPM auto-unlock is exercised through
  a real boot (today it is argv-level unit-tested only —
  [ADR-0009](../adr/0009-ab-installs-require-partition-isolation.md)
  consequences); same-VM install also covers the bootc path's staged
  first-boot enrollment.
- Retire `snosi-firstboot`'s flatpak role (snosi-side).
- **Slim frostyard/first-setup to first-login only** (cross-repo):
  first-setup is a three-mode tool — an old installer mode, a
  first-boot setup wizard (keyboard, locale, user creation), and a
  first-login mode (light/dark preference, user flatpak offers, other
  per-user niceties). Firn now owns everything the first two modes did
  at install time; rip them out, keep only first-login, and restore the
  package to BOTH desktop images — today snow ships `snow-first-setup`
  but snowfield lost it somewhere along the way
  (`snosi/shared/packages/snow/mkosi.conf:7` vs no reference under
  snowfield). Contract: the system flatpaks firn installs
  (`core_flatpaks`) are defined by a JSON file owned by the
  frostyard/first-setup repo and shipped in its package
  (`/usr/share/org.frostyard.FirstSetup/snow_first_setup/core.json`) —
  the slimmed package keeps shipping it, and firn keeps reading it from
  the mounted image at install time (ADR-0006).
- Retirement ADRs for frostyard/fisherman and `snosi-install` once
  parity is demonstrated.
- **Done when:** the single snosi installer ISO ships firn as the only
  installer, and installs of both image families from that published
  ISO succeed on real hardware.

## Phase 8 — bootc under Secure Boot: secure-install schema-1 (large, cross-repo) — ⏳ in progress (code + E2E DONE 2026-08-12; lab re-enable + dakota retirement remain)

Firn cannot yet install a bootc image that boots under UEFI Secure Boot.
snosi's secure bootc images use the **Debian shim** (Microsoft-trusted)
chainloading a **snosi-MOK-signed systemd-boot** second stage
(`grubx64.efi`) plus MokManager (`mmx64.efi`) — a chain the firmware
trusts only once the snosi MOK is enrolled. Enrollment is a real,
human-in-the-loop step: the installer stages the MOK with `mokutil
--import` (password-hashed) so **MokManager prompts the user to enroll it
on first boot**. A plain `bootc install` does none of this, so firn's
bootc + SB installs land in MokManager with nothing staged. The contract
is snosi's `/usr/lib/snosi/bootc-secure.json` (images labelled
`io.snosi.bootc.secureboot-capable=true`).

Fisherman already implements the whole path — the port target is
`fisherman/internal/secure` (`espstage.go` stages shim / second-stage /
MokManager onto the ESP; `enroll.go` `StageMOK`; a runtime bootloader
reconciler). Firn already has half of `StageMOK`
(`internal/abimg/mok.go`, wired into the A/B `mok-stage` step) —
schema-1 extends it to the bootc pipeline plus the ESP secure-chain
assembly and the reconciler unit
([port-from-parents](../../.agents/skills/port-from-parents/SKILL.md)).

- Port secure-install schema-1 into firn's bootc pipeline: read
  `bootc-secure.json`, verify the image is `secureboot-capable`, stage
  the ESP secure chain (shim → MOK-signed systemd-boot → MokManager)
  after `bootc install`, and stage the MOK via mokutil for first-boot
  MokManager enrollment. Recipe `mok` becomes valid for the bootc family
  (today an `abOnlyKeys` field, `internal/recipe/validate.go`); the
  schema change lands in
  [recipe-schema.md](../specs/recipe-schema.md) in the same commit.
- Ship the runtime bootloader-reconciler equivalent (or rely on the
  image's `snosi-bootc-bootloader-reconcile.service`) so the MOK-signed
  second stage survives bootc updates.
- **Retire dakota's installer and its secure tests** (cross-repo): firn
  becomes the single installer for the secure bootc path too, superseding
  dakota-iso's `bootc-secure-installer-runner.sh`. The lab's
  `run-secure-install-tests` (dakota) lane retires and is replaced by
  firn's own bootc + SB lane — the three cells held out of the matrix in
  Phase 7 (`argo/firn-install-test.yaml` PENDING block), re-enabled once
  firn enrolls the MOK for real (drop the lab `virt-fw-vars --add-mok`
  pre-seed, which fakes the enrolled end-state; drive MokManager or
  assert genuine mokutil staging).
- Kick-off is an ADR: committing firn to schema-1 and retiring dakota's
  installer/tests is a significant decision, mirroring the
  fisherman/snosi-install retirement ADRs (frostyard/core 0027–0028).
  Recorded as [ADR-0014](../adr/0014-port-secure-install-schema-1-for-bootc.md)
  (Accepted).
- ✅ **Code + local proof DONE (2026-08-12).** `internal/secureboot`
  (espchain/imageroot/contract), the bootc `esp-stage` + `mok-stage`
  steps, and recipe `mok` for bootc are implemented and unit-tested.
  `test/e2e-bootc-secure.sh` installs cayo with `mok = "enroll"` in a
  secboot QEMU guest and **boots the disk under enforced Secure Boot**
  (guest reports `SecureBoot enabled`), with the MOK enrolled host-side
  via `virt-fw-vars` (the MokManager stand-in, dakota-style).
- **Remaining (cross-repo):** re-enable the three PENDING bootc+SB lab
  cells (`argo/firn-install-test.yaml`) on the real enrollment path, and
  retire dakota's secure installer + `run-secure-install-tests`.
- **Done when:** the three PENDING lab cells are green on the real path
  and dakota's secure installer + `run-secure-install-tests` are retired.

## Later / ideas

- ✅ **Encrypted bootc installs of UKI-entry images — boot-time unlock
  (DONE, proven on hardware 2026-08-12).** UKI-directive BLS entries bake
  the cmdline into the UKI with no `options` line, so there is nowhere to
  inject `rd.luks` kargs. Rather than entry-`options` merging, firn leans
  on the same gpt-auto path the unencrypted case uses: retag-root runs for
  encrypted too (the LUKS partition gets the DPS root GUID, so gpt-auto
  discovers it and sets up cryptsetup), and TPM2 is enrolled at install
  against the deployed UKI's **signed PCR 11** (the A/B path's
  firmware-independent scheme, `EnrollTPMFromUKI`), so first boot
  auto-unlocks without the chicken-and-egg of PCR-7 first-boot staging.
  The bootc UKI lives at `EFI/Linux/<vendor>/…efi`. Verified: a
  `tpm2-luks` install auto-unlocks and boots to a login prompt on a vTPM
  VM, and the lab matrix's `tpm2-luks` cells (cayo + snow) pass.
  `luks-passphrase` still prompts interactively at boot by design.

- ✅ **bootc installs under UEFI Secure Boot don't boot — scoped as
  Phase 8 (2026-08-12).** The lab matrix found any `bootc` install with
  Secure Boot ON — with or without encryption — is rejected by firmware
  at boot (`BdsDxe: … Access Denied -- rejected probably by Secure
  Boot`): the snosi-MOK-signed second stage is untrusted until the MOK is
  enrolled, and firn does not stage the ESP secure chain or run mokutil
  for bootc. Unencrypted and encrypted-with-SB-off bootc both boot fine;
  only SB-on is affected. This is the **secure-install schema-1** gap,
  now the Phase 8 work item above. The lab's three bootc+SB cells are held
  out of the matrix as PENDING rather than faked green with a
  `virt-fw-vars --add-mok` pre-seed (`argo/firn-install-test.yaml`).

- Fisherman extras not yet scoped: Windows data slurp, OEM vendor
  detection + brew first-login installs, audio/Plymouth polish, cache
  pre-warming. (secure-install schema-1 is now scoped as Phase 8.)
- arm64 targets (fisherman releases arm64; A/B index is x86-64 today).
- A/B write-path hardening beyond GPT/ESP discard (would supersede the
  accepted stream-then-verify risk via a new ADR).

## Open questions

- **Which fisherman extras (slurp, OEM, brew, audio) are in firn's v1
  scope?** Decide by end of Phase 3; resolution that changes
  architecture becomes an ADR.
- ✅ **Is secure-install (schema-1) required for firn to replace fisherman
  on the snow secure path?** RESOLVED yes (2026-08-12): firn is retiring
  the dakota installer, so it must own the secure bootc path. Scoped as
  Phase 8 above; its kick-off ADR records the decision.
- **Do A/B artifacts need arm64 before Phase 7?** snosi-side; decide by
  Phase 6.

## References

- Implements: [design/architecture.md](../design/architecture.md),
  [specs/recipe-schema.md](../specs/recipe-schema.md),
  [specs/progress-protocol.md](../specs/progress-protocol.md)
