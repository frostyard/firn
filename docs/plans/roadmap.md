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

## Phase 6 — TUI (medium)

- Wizard flows per [ADR-0007](../adr/0007-tui-only-frontend-single-binary.md):
  recipe generation, in-process pipeline run, progress view,
  recovery-key and summary presentation, 80×24 legibility.
- Kiosk systemd unit for installer media (console + serial).
- **Done when:** a TUI-driven install completes on both families in the
  E2E VM, and the recipe it wrote reproduces the same install headless.

## Phase 7 — Becoming the only installer (medium, cross-repo)

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
  snowfield). Guard: the package's `core.json` is firn's install-time
  core-flatpak contract (ADR-0006 / snosi-firstboot line 36) — it must
  keep shipping in the slimmed package or move to an owner both consult.
- Retirement ADRs for frostyard/fisherman and `snosi-install` once
  parity is demonstrated.
- **Done when:** the single snosi installer ISO ships firn as the only
  installer, and installs of both image families from that published
  ISO succeed on real hardware.

## Later / ideas

- Fisherman extras not yet scoped: Windows data slurp, OEM vendor
  detection + brew first-login installs, audio/Plymouth polish, cache
  pre-warming, secure-install schema-1 (needed for snow secure path —
  likely promoted into Phase 3/4 when scoped).
- arm64 targets (fisherman releases arm64; A/B index is x86-64 today).
- A/B write-path hardening beyond GPT/ESP discard (would supersede the
  accepted stream-then-verify risk via a new ADR).

## Open questions

- **Which fisherman extras (slurp, OEM, brew, audio) are in firn's v1
  scope?** Decide by end of Phase 3; resolution that changes
  architecture becomes an ADR.
- **Is secure-install (schema-1) required for firn to replace fisherman
  on the snow secure path?** Decide by Phase 4 planning; if yes it is a
  Phase 4/5 work item, not "Later".
- **Do A/B artifacts need arm64 before Phase 7?** snosi-side; decide by
  Phase 6.

## References

- Implements: [design/architecture.md](../design/architecture.md),
  [specs/recipe-schema.md](../specs/recipe-schema.md),
  [specs/progress-protocol.md](../specs/progress-protocol.md)
