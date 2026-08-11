# Firn architecture

Living document. Rationale:
[ADR-0003](../adr/0003-rewrite-fisherman-as-firn.md),
[ADR-0004](../adr/0004-single-installer-scope-and-support-matrix.md),
[ADR-0005](../adr/0005-toml-recipe-model.md),
[ADR-0006](../adr/0006-install-time-offline-first-flatpaks.md),
[ADR-0007](../adr/0007-tui-only-frontend-single-binary.md).
Contracts: [specs/recipe-schema.md](../specs/recipe-schema.md),
[specs/progress-protocol.md](../specs/progress-protocol.md).

## Overview

Firn is a single Go binary that installs every snosi image family — bootc
OCI images and native A/B disk images — from one TOML recipe, either
headless (`firn install recipe.toml`) or through a built-in TUI wizard
that generates the same recipe and runs the same pipeline in-process.

```
 interactive ──► TUI wizard (bubbletea/huh)
                     │  serializes
                     ▼
                recipe (TOML) ◄──── headless: firn install recipe.toml
                     │  load + validate (fail-closed, per family)
                     ▼
                 preflight ──► pipeline (assembled step list)
                                    │            │
                              progress events    ├── bootc path steps
                              (Go channel)       └── A/B path steps
                                    │                     │
                     TUI view ◄─────┴────► --json-progress emitter
                                                (versioned NDJSON, spec)
```

## Design

### Layers and packages

| Layer | Packages (indicative) | Dependency rule |
|---|---|---|
| Frontend | `internal/tui` | Charm stack allowed (ADR-0007) |
| Pipeline | `internal/pipeline`, `internal/steps/*` | stdlib + host tools only |
| Domain | `internal/recipe`, `internal/disk`, `internal/luks`, `internal/trust`, `internal/sysconfig`, `internal/flatpak`, `internal/progress`, `internal/runner` | stdlib + host tools only (TOML decoder excepted, ADR-0005) |

Like fisherman, privileged work shells out to host tools (`sfdisk`,
`cryptsetup`, `bootc`/`podman`, `flatpak`, `systemd-cryptenroll`,
`mokutil`, …) through a `runner` package with an injectable executor for
tests.

### The step engine

The pipeline is an assembled, ordered list of **steps** — not a
procedural main. Each step declares a name, a progress weight, whether it
is destructive, and `Run(ctx, *Env) error`; `Env` carries the validated
recipe, resolved image metadata, mount/mapper state, and the progress
emitter. Assembly happens once, up front, from the recipe: family selects
the backbone (bootc vs A/B), options (encryption, TPM, Secure Boot,
flatpaks, slurp-style extras later) splice steps in or out. The assembled
list is inspectable, which gives dry-run, accurate progress totals, and
per-step tests for free. Adding a step means writing one and adding it to
assembly — one place, replacing the four-places-per-step bookkeeping of
fisherman's 1,200-line `main()`.

Teardown mirrors assembly: every step that mounts, opens, or maps
something registers an undo on a cleanup stack that runs on any exit
path — closing the mount/mapper-leak class of bug documented in
`snosi-install`.

### Preflight

Before anything destructive: UEFI check (refuse BIOS machines with a
clear diagnostic, ADR-0004), required-tool checks derived from the
assembled step list (each step declares the binaries it needs, so the
check list cannot drift from the code), disk refusal rules — the union of
both installers' rules: mounted anywhere, RAID/LVM member, the installer's
own boot medium, undersized. For the A/B path, minimum sizes are computed
from the image's published manifest/repart artifacts, not from a
hand-copied constant table (fixing a documented `snosi-install` hazard).

### The bootc path

Carried from fisherman substantially intact (copy-with-attribution,
ADR-0003): GPT partitioning profiles (grub2 and systemd-boot layouts),
optional LUKS root (passphrase and/or TPM2 modes), mkfs for
xfs/ext4/btrfs (including `@`/`@home`/`@snapshots` subvolumes) and ZFS,
mount orchestration, `bootc install to-filesystem` via podman or direct
with the same argument-building logic, TPM2 first-boot enrollment
staging, the secure-install (schema-1) contract, and filesystem
finalization. Fisherman's incident comments come along with the code.

### The A/B path

Ported from `snosi-install` (bash → Go, behavior preserved, structure
fixed): fetch and gpgv-verify the signed artifact index, resolve the
channel version, stream the compressed image to the target disk while
hashing the compressed stream — **stream-then-verify is retained as an
accepted, documented risk** (no 2× scratch space on live media; decided
with ADR review, see Operational notes) — then validate the written GPT
layout, relocate the backup GPT, grow `/var` (filesystem-aware:
`resize2fs` or btrfs resize), format `/var` (LUKS2 by default, plain
only on explicit opt-out; ext4 by default or btrfs with optional nested
`home`/`snapshots` subvolumes per
[ADR-0008](../adr/0008-ab-var-filesystem-choice.md)), enroll TPM
against the
UKI's embedded `.pcrpkey` (signed PCR 11), seed `/var` state and the
`/etc`-overlay upper, and stage MOK enrollment when Secure Boot is in
play. The hand-rolled awk account editing is reimplemented in Go with
unit tests against fixture passwd/group/shadow files.

### System configuration (`internal/sysconfig`)

One package owns the semantics of every `[system]` feature — hostname,
user + groups + password, locale, timezone, keyboard, root/user SSH
authorized keys, flatpak set. Each feature is written through one of two
**target writers** behind a common interface:

- **deployment writer** (bootc): writes into the deployment's `/etc`
  (composefs- and ostree-aware, carried from fisherman's `post` package),
  users via `useradd --root`/chroot, homes in the stateroot.
- **overlay writer** (A/B): seeds `var/lib/snosi/etc-overlay/upper/` and
  `/var/home`, reading the pristine baseline from the read-only erofs
  root.

A feature is complete only when both writers implement it (ADR-0004).
Flatpaks follow ADR-0006 on both paths: copy from the medium's seeded
repo, download the remainder into the mounted target, report (never
silently drop) what was unreachable.

### Trust

`internal/trust` speaks both systems: cosign verification of OCI image
references (bootc, carried from fisherman including digest pinning for
secure installs) and OpenPGP verification of the A/B artifact index via
`gpgv` against the shipped pubring. Nothing is installed from an
unverified source; verification failures abort before destructive steps
where possible (see Operational notes for the A/B stream exception).

### Progress and frontends

`internal/progress` defines one event model (step begin/progress/end,
info, warning, summary items such as unreachable flatpaks, recovery-key
disclosure, completion with boot-entry info). The TUI consumes it over a
channel in-process; `--json-progress` serializes the same events as
versioned NDJSON for automation — the only supported external interface
(ADR-0007), replacing fisherman's stream and snosi's proto-1. Secrets
never appear in events; recovery-key disclosure is an explicit,
deliberate event type the TUI renders with confirmation.

## Operational notes

- Firn runs as root from live media; every destructive action sits behind
  preflight plus (interactively) typed disk confirmation matching the
  disk path or serial — `snosi-install`'s rule, adopted everywhere.
- **A/B stream-then-verify residue:** on any write/verify failure the
  failure path discards the GPT regions and ESP signatures so nothing on
  the disk is bootable or auto-discoverable, then reports loudly — but
  unauthenticated bytes may remain on the platter. This is an accepted
  risk, chosen over 2× scratch space; a future ADR may harden it.
- Cleanup stack unwinding must be idempotent: a failed install should
  leave no mounts, no open mappers, and a re-runnable installer without a
  reboot.
- Space-constrained live environments (tmpfs/overlay roots) redirect
  scratch onto the target disk, carried from fisherman's
  `isSpaceConstrained` handling.
- TPM enrollment happens where each path's constraints demand: A/B
  enrolls at install time against signed PCR 11 (firmware-independent);
  bootc stages first-boot enrollment because PCR 7 differs in the live
  environment (fisherman's documented incident).

## References

- Rationale: [ADR-0003](../adr/0003-rewrite-fisherman-as-firn.md),
  [ADR-0004](../adr/0004-single-installer-scope-and-support-matrix.md),
  [ADR-0005](../adr/0005-toml-recipe-model.md),
  [ADR-0006](../adr/0006-install-time-offline-first-flatpaks.md),
  [ADR-0007](../adr/0007-tui-only-frontend-single-binary.md)
- Contracts: [specs/recipe-schema.md](../specs/recipe-schema.md),
  [specs/progress-protocol.md](../specs/progress-protocol.md)
- Built in: [roadmap — Phases 1–7](../plans/roadmap.md)
