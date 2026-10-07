# Plan: Firn roadmap

Tracks firn from its original dual-family scope
([ADR-0004](../adr/0004-single-installer-scope-and-support-matrix.md))
through the bootc-only implementation ([ADR-0016](../adr/0016-bootc-only-recipe-contract.md)),
implementing [design/architecture.md](../design/architecture.md) and the
[recipe schema](../specs/recipe-schema.md) /
[progress protocol](../specs/progress-protocol.md) specs. Phases are
ordered so every phase ends with something demonstrable in a VM.

## Phase 1 — Skeleton and contracts (small) — ✅ shipped 2026-08-11 (`545d831`)

- Repo scaffold: Go module, `LICENSE` (GPL-3.0 text) + `NOTICE`
  (attribution lineage) — required before any code is copied in
  ([ADR-0003](../adr/0003-rewrite-fisherman-as-firn.md)) — `make check`
  recipe, CI running it.
- `internal/recipe`: TOML load + fail-closed validation per the
  [recipe schema spec](../specs/recipe-schema.md), with fixture recipes
  for both families.
- `internal/progress`: event model + NDJSON emitter per the
  [progress protocol spec](../specs/progress-protocol.md).
- **Done when:** `firn validate <recipe>` accepts the spec's examples,
  rejects each rule violation with a distinct error, and `make check`
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
- **Deferred:** ZFS root support is outside recipe schema v1. Lower-level
  partitioning and pool helpers exist, but the schema must not accept ZFS
  until a complete bootable path and nested-VM E2E land together
  ([design](../design/architecture.md#the-bootc-path),
  [schema](../specs/recipe-schema.md#target)).

## Phase 4 — A/B path (large) — ✅ shipped 2026-08-11 (E2E: cayo-ab installed inside a nested VM boots + verifies over SSH. Install ran in a throwaway QEMU guest because the A/B image carried the host's own discoverable-partition layout — see ADR-0009. Encrypted-var/TPM boot was unit-tested at the argv level; full encrypted boot arrived with the ISO in Phase 7. This path was removed in Phase 9.)

- `internal/trust`: gpgv-verified index fetch, version resolution
  (incl. `release` pinning); manifest-derived minimum-size computation.
- Stream-write step (stream-then-verify with hardened failure path),
  layout validation, GPT relocate, `/var` grow/format/LUKS with
  filesystem choice and optional subvolumes
  ([ADR-0008](../adr/0008-ab-var-filesystem-choice.md)), TPM
  enrollment against the UKI `.pcrpkey`, MOK staging
   ([historical isolation decision](../adr/0009-ab-installs-require-partition-isolation.md)).
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

## Phase 6 — TUI (medium) — ✅ shipped 2026-08-11 (tmux-driven E2E walks the real wizard and installs both families in nested VMs; booted disks verify over SSH; wizard-generated recipes pass `firn validate` as validation-level headless reuse. The family engine E2Es cover execution; the TUI harness does not perform a redundant second install. Wizard opens with the bootc-vs-A/B guidance page; kiosk units in dist/)

- Wizard flows per [ADR-0007](../adr/0007-tui-only-frontend-single-binary.md):
  recipe generation, in-process pipeline run, progress view,
  recovery-key and summary presentation, 80×24 legibility.
- Kiosk systemd unit for installer media (console + serial).
- **Done when:** a TUI-driven install completes on both families in the
  E2E VM, and the recipe it wrote validates through the canonical headless
  loader. The family engine E2Es separately prove those recipes' pipeline
  semantics; a second destructive install is not part of the TUI harness.

<a id="phase-7-becoming-the-only-installer-medium-cross-repo-in-progress"></a>
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
- 🚧 **frostyard/lab suites** (in progress): the Phase 7 plan for a
  secure-boot × encryption × image × family matrix is superseded in
  implementation scope by [ADR-0016](../adr/0016-bootc-only-recipe-contract.md)
  and [Phase 9](#phase-9-bootc-only-transition-bounded-cross-repo-implementation-in-progress).
  Remaining lab work runs the firn ISO on incus VMs across supported
  bootc images and security modes, checking install-to-boot results.
  Lab results supplement, but do not satisfy, Phase 7's outstanding
  published-ISO real-hardware both-family criterion.
- 🚧 **retirement ADRs for fisherman and snosi-install** (in progress):
  written in frostyard/core (org-wide decision record), recording their
  supersession by firn.
- ✅ **review branches merged**: first-setup `first-login-only`
  (#29, released v0.4.0) and snosi `firn-installer` (#693) — the single
  installer ISO, embedded flatpak list, VGA-console visibility, and the
  man-db var-audit fix all landed on their mains.

## Phase 7 plan — Becoming the only installer (medium, cross-repo)

Historical dual-family plan; Phase 9 supersedes its proposed implementation
scope but does not satisfy its outstanding published-ISO hardware criterion.

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

## Phase 8 — bootc under Secure Boot: secure-install schema-1 (large, cross-repo) — ⏳ in progress (code + E2E + lab all DONE 2026-08-12; only dakota retirement remains)

Firn installs secureboot-capable bootc images under UEFI Secure Boot when
the recipe opts in with `security.mok = "enroll"` (the bootc contract in
[recipe-schema.md](../specs/recipe-schema.md)). snosi's secure bootc images
use the **Debian shim** (Microsoft-trusted) to chainload a
**snosi-MOK-signed systemd-boot** second stage (`grubx64.efi`) plus
MokManager (`mmx64.efi`). `internal/steps/assemble.go` inserts `esp-stage`
after the bootc deployment to validate `/usr/lib/snosi/bootc-secure.json`
and stage that chain, then runs `mok-stage` last to import the image's MOK
with `mokutil`. MokManager prompts the user to complete enrollment on first
boot; the image-owned `snosi-bootc-bootloader-reconcile.service` preserves
the signed second stage across bootc updates.

The implementation ports fisherman's `internal/secure` behavior into
`internal/secureboot` and reuses the MOK helper (now `internal/enroll/mok.go`) through the bootc
`mok-stage`, with provenance and incident guidance preserved
([port-from-parents](../../.agents/skills/port-from-parents/SKILL.md)).
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
- ✅ **Lab re-enabled + proven in-fleet (2026-08-12).** firn v0.3.1 (the
  release that carries schema-1; needed the ISO to ship `sbverify` and the
  extraction to use `--network host`), and the lab lane completes
  enrollment host-side after install (`virt-fw-vars --add-mok` + clear
  `MokNew`). `bootc/cayo/none/sb=true` installed and **booted under
  enforced Secure Boot**; the three bootc+SB cells are re-enabled in
  `argo/firn-install-test.yaml` (10 active cells).
- **Remaining (cross-repo):** retire dakota's secure installer +
  `run-secure-install-tests`, now that firn's own lane covers bootc+SB.
- **Done when:** dakota's secure installer + `run-secure-install-tests`
  are retired.

<a id="phase-9-proposed-post-cutoff-bootc-only-transition-bounded-cross-repo-not-started"></a>
<a id="phase-9-bootc-only-transition-bounded-cross-repo-implementation-in-progress"></a>
## Phase 9 — bootc-only transition (bounded, cross-repo) — ⏳ implementation in progress

**Implementation in progress.** [ADR-0015 (Proposed)](../adr/0015-bootc-only-installer-scope.md)
describes scope; [ADR-0016 (Accepted)](../adr/0016-bootc-only-recipe-contract.md)
sets the implemented version-2 contract and bootc-v1 migration window. The
[architecture](../design/architecture.md#current-bootc-only-recipe-boundary),
[recipe schema v2](../specs/recipe-schema.md#bootc-version-1-compatibility) and
[progress protocol](../specs/progress-protocol.md#stable-codes) reflect the
bootc-only code. The 90-day v1 window begins with the v2 release; record
release and exact expiry dates at release. Expiry needs a later reviewed
validator change, not a time-triggered switch. Phase 7's
published-ISO, real-hardware **both-family** Done-when remains incomplete;
this phase does not rewrite its historical A/B evidence or mark it done.

1. **Firn implementation (in progress):** the recipe validator accepts v2
   bootc, warns for v1 bootc, rejects A/B-v1 before assembly and fails closed
   on unknown versions. `steps.Assemble` has only the bootc backbone;
   A/B-only dependencies are removed while MOK/TPM helpers remain in
   `internal/enroll`. The wizard emits v2 and the bootc-only built-ins are
   Snow, Snowfield and Floe. `tui.loadCatalogFrom` / `checkCatalog` reject
   unsupported overrides wholesale and fall back to bootc-only built-ins.
   Remaining rollout work includes inventory and migration of stored v1
   recipes, release-date/expiry recording, and coordination with snosi on
   its shipped catalog and ISO/tool payload. Snowfield's scope needs
   confirmation. Sundog is offered on the ISO through snosi's shipped
   `/etc/firn/catalog.json`, which carries its ref and cosign key; it is not
   in `builtinCatalog()`, which applies only without that file. Keep Firn's media boundary from
   [ADR-0010](../adr/0010-single-installer-iso-in-snosi.md) and UEFI floor
   from [ADR-0004](../adr/0004-single-installer-scope-and-support-matrix.md).
2. **Qualification and sole-path decision gate:** after the above changes,
   record a bounded published-ISO evidence matrix on actual UEFI x86-64
   hardware, not a claim that every product supports every security mode.
   Confirm supported cells and the Snowfield disposition before testing:

   | Product | Proposed hardware cells (subject to image support) | Evidence to record |
   | --- | --- | --- |
   | Snow | Secure Boot on + TPM present + `tpm2-luks-passphrase`; Secure Boot off + TPM absent + `none` | Published image ref and cosign key; published snosi ISO build; device identity; install-to-login and hostname/user/config checks. |
   | Floe | Secure Boot off + TPM absent + `luks-passphrase` | Same provenance, device, login and configuration checks; verify boot-time passphrase unlock. |
   | Sundog | Image, key and catalog entry ship in snosi's catalog; if secure-capable, Secure Boot on + TPM present + `tpm2-luks` | Same provenance, device, login and configuration checks; if not secure-capable, explicitly reallocate the Secure Boot cell to a supported image. |
   | Snowfield | Decide whether in the post-cutoff catalog/support scope before qualification | If included, assign a supported distinct hardware cell and record the same provenance, ISO, login and configuration evidence; otherwise record exclusion and rationale. |

   For each qualified cell, capture the exact published image digest/ref,
   verified cosign trust key, published snosi ISO identity, machine/firmware
   identity, and install-to-login/identity/config assertions. Across the
   supported cells cover Secure Boot on/off, TPM present/absent, unencrypted,
   passphrase LUKS and TPM unlock; expand the table if image capabilities
   demand it. Secure-capable images under Secure Boot additionally need
   **actual first-boot MokManager enrollment and a subsequent Secure Boot
   boot** (not host-side `virt-fw-vars` injection). Exercise cosign failure
   refusal before writes, disk preflight/refusal, and applicable offline
   flatpak/media-copy behavior. Nested-VM, fixtures and lab runs support but
   do not substitute for published-ISO real-hardware proof. Do not declare
   the ISO the sole path until this evidence, the outstanding Phase 7
   criterion, and separate production authorization are resolved.

- **Done when:** architectural approval and migration policy are recorded,
  separately reviewed contract/code and snosi media changes have landed,
  the bounded real-hardware evidence is recorded, Phase 7's outstanding
  criterion is addressed explicitly, and a separate sole-path/production
   decision has been made. **Not complete; implementation in progress.**

<a id="phase-10"></a>
## Phase 10 — Image-published core flatpaks (small, cross-repo) — ⏳ in progress

**In progress.** [ADR-0018](../adr/0018-image-published-core-flatpaks-label.md)
moves the `core_flatpaks` set from first-setup's `core.json` and the ISO's
`/usr/share/firn/core-flatpaks.json` to an `org.frostyard.core-flatpaks`
label each image publishes ([spec](../specs/core-flatpaks-label.md)).
Before it, every image, including Sundog and Floe, got first-setup's GNOME
list when the user opted in. Order matters: snosi labels ship before a firn
release stops reading the old paths.

Out of scope: offline flatpak seeding. Published ISOs carry no seed, and a
locally seeded ISO keeps today's whole-tree copy (ADR-0018 Consequences).
Migrating existing installs and removing Sundog's native apps belong to
snosi.

1. ✅ **Settle the label contract (firn docs).** ADR-0018 defines the parser
   cases, preflight-time validation, and which image the set describes
   (#110).
2. ✅ **Snosi: publish the label** (frostyard/snosi#1054).
   `flatpaks/snow.json` (Snow and Snowfield) and `flatpaks/sundog.json`;
   Floe has none. CI validates the files, every packaging lane passes the
   label and checks it, and the secure lane checks the pushed digest. The
   ISO fallback and seed read `flatpaks/legacy/firn-core-flatpaks.json`,
   generated from `snow.json`; first-setup never read its own `core.json`.
3. ✅ **Firn: consume the label.** ADR-0018 accepted; the
   [label spec](../specs/core-flatpaks-label.md) lands with the code.
   `preflight-image` returns the selected source's labels, parses them when
   `core_flatpaks = true` (malformed fails with
   `core_flatpaks_label_invalid`), and keeps the set for `runFlatpaks`,
   which installs explicit apps then the core set, each once.
   `internal/flatpak` no longer reads first-setup's `core.json` or the ISO
   fallback. Tag only after step 2's images are published.
4. ✅ **Firn TUI.** The flatpaks page inspects the chosen image with a
   timeout and again on image change, and distinguishes inspect failure, no
   core set, a malformed label and a valid set; hiding the toggle clears it.
   Unit tests cover image switching and every state. `test/e2e-tui.sh`
   asserts Floe's real outcome (no toggle, an explanation); the guest
   already pulls Floe from GHCR, so this adds no new registry dependency.
   ✅ The toggle starts on for each newly chosen image whenever it is
   offered ([ADR-0019](../adr/0019-wizard-offers-core-flatpaks-by-default.md));
   Sundog drops its native apps for its core set, so the default must
   offer them.
5. **Snosi: retire the fallback.** Once the ISO carries a step-3 firn
   release, remove the `/usr/share/firn/core-flatpaks.json` embedding from
   `shared/firn-installer/mkosi.conf` and the Justfile's `_firn-binary`,
   and point the seed at `flatpaks/snow.json` so
   `flatpaks/legacy/firn-core-flatpaks.json` can go.

- **Done when:** Snow and Sundog installs with `core_flatpaks = true` each
  land their own image's set, a malformed label fails preflight before any
  disk write, a Floe install wizard offers no core set, the TUI shows the
  set before install, and neither firn nor the ISO references first-setup's
  `core.json` or `core-flatpaks.json`.

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
- arm64 bootc targets (fisherman releases arm64; Firn hardware qualification
  remains x86-64).

## Open questions

- **Which fisherman extras (slurp, OEM, brew, audio) are in firn's v1
  scope?** Decide by end of Phase 3; resolution that changes
  architecture becomes an ADR.
- ✅ **Is secure-install (schema-1) required for firn to replace fisherman
  on the snow secure path?** RESOLVED yes (2026-08-12): firn is retiring
  the dakota installer, so it must own the secure bootc path. Scoped as
  Phase 8 above; its kick-off ADR records the decision.

## References

- Implements: [design/architecture.md](../design/architecture.md),
  [specs/recipe-schema.md](../specs/recipe-schema.md),
  [specs/progress-protocol.md](../specs/progress-protocol.md)
