# Firn architecture

Living document. Rationale:
[ADR-0003](../adr/0003-rewrite-fisherman-as-firn.md),
[ADR-0004](../adr/0004-single-installer-scope-and-support-matrix.md),
[ADR-0005](../adr/0005-toml-recipe-model.md),
[ADR-0006](../adr/0006-install-time-offline-first-flatpaks.md),
[ADR-0007](../adr/0007-tui-only-frontend-single-binary.md),
[ADR-0010](../adr/0010-single-installer-iso-in-snosi.md) (media
boundary: firn ships binary + kiosk unit + contracts; snosi ships the
single installer ISO).
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

Before rendering image choices, the wizard validates the loaded catalog with
the recipe package's canonical machine-independent image constraints. Family
choices come only from families represented in that validated catalog; a
one-family catalog skips the family page. The selected catalog entry is the
sole family state used by every later page and by recipe assembly, including
after a start-over.

Each interactive run owns a randomly named 0700 directory below `/run/firn`.
The reviewed recipe references only 0600 secret files in that directory, and
the wizard's canonical serializer returns the accepted review bytes directly
to the command layer. That layer writes them unchanged as `recipe.toml` beside
the secrets, reloads that file, and gives the loaded recipe to the engine.
Start-over, quit, abort, and pre-persistence errors remove abandoned plaintext;
once the recipe is persisted, the directory remains available for the printed
headless reproduction command until the installer environment reboots.
Encrypted A/B wizard choices set `recovery_key_out` to `recovery-key` in that
same session, giving the one-time on-screen disclosure a durable private copy.

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
For bootc recipes that set `image.cosign_pub_key`, preflight selects the
same embedded-or-remote source the install will consume, resolves it to an
immutable digest, verifies that digest with cosign, and carries the pinned
reference into both native-bootc and podman installation paths.
For encrypted A/B recipes with `security.recovery_key_out`, preflight refuses
an existing destination, exclusively reserves a 0600 placeholder, and fsyncs
the complete generated key to a private same-directory staging file. Dry-run
exercises and removes both files. A real install later commits the staged key
by atomic rename, so path, permission, allocation, and write failures occur
before `stream-write` can touch the target disk.

### The bootc path

Carried from fisherman substantially intact (copy-with-attribution,
ADR-0003): GPT partitioning profiles (grub2 and systemd-boot layouts),
optional LUKS root (passphrase and/or TPM2 modes), mkfs for
xfs/ext4/btrfs (including `@`/`@home`/`@snapshots` subvolumes),
mount orchestration, `bootc install to-filesystem` via podman or direct
with the same argument-building logic, install-time TPM2 enrollment
against the deployed UKI's signed PCR 11 policy, the secure-install
(schema-1) contract, and filesystem
finalization. Fisherman's incident comments come along with the code.
The port includes lower-level ZFS partitioning and formatting helpers, but
recipe schema v1 rejects ZFS because the end-to-end bootable install path is
not implemented.

**Installing from the all-in-RAM ISO** ([ADR-0012](../adr/0012-bootc-install-from-ram-installer.md)):
the single installer ISO ([ADR-0010](../adr/0010-single-installer-iso-in-snosi.md))
boots entirely into RAM, so `/var/lib/containers`, `/var/tmp`, and the
root are memory-backed and `pivot_root` cannot pivot off the initramfs.
When it detects that environment (`bootcimg.StorageSpaceConstrained`),
the bootc-install step redirects podman's image store and blob staging
onto the target disk (bind mounts), disables `pivot_root`
(`no_pivot_root` via a scoped `CONTAINERS_CONF_OVERRIDE`), and hides its
on-target scratch from bootc's empty-`/target` check by self-binding the
scratch base into a mount point — all while staying on the
`containers-storage` transport, deliberately **not** fisherman's
`skopeo`→`oci:` redirect (which trips hardened images' signature policy).
Disk-backed hosts (the loop-device E2E) skip all of this unchanged.

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
Shared recipe validation owns cross-writer input semantics: for example,
user full names accept empty and Unicode GECOS text but reject passwd field
and record delimiters before either writer runs.
Flatpaks follow ADR-0006 on both paths: copy from the medium's seeded
repo, download the remainder into the mounted target, report (never
silently drop) what was unreachable.

### Trust

Bootc cosign trust is implemented in `internal/bootcimg`, carried from
fisherman with digest pinning: `image.cosign_pub_key` makes Firn resolve and
verify the immutable source digest before disk writes, while retaining the
recipe tag only as the installed system's day-two update reference. The
built-in TUI bootc catalog supplies the installer medium's
`/usr/lib/snosi/cosign.pub`; override catalogs may supply their own key path.
Headless recipes that omit the field rely on the host container policy and
cached-image provenance; Firn does not claim independent cosign verification
for that case.

`internal/trust` performs OpenPGP verification of the A/B artifact index via
`gpgv` against the shipped pubring. Verification failures abort before
destructive steps where possible (see Operational notes for the A/B stream
exception).

### Progress and frontends

`internal/progress` defines one event model (step begin/progress/end,
info, warning, summary items such as unreachable flatpaks, recovery-key
disclosure, completion with boot-entry info). The TUI consumes it over a
channel in-process; `--json-progress` serializes the same events as
versioned NDJSON for automation — the only supported external interface
(ADR-0007), replacing fisherman's stream and snosi's proto-1. No ordinary
event may contain secrets; recovery-key disclosure is the sole explicit
exception. The interactive channel renders it once behind a blocking
confirmation and never repeats it into logs after the TUI exits. Headless
renderers deliberately expose it on their selected progress stream, whose
caller must protect it; the exact boundaries are pinned by the
[progress protocol](../specs/progress-protocol.md#recovery-key-disclosure).
Catalog-selected bootc images carry their cosign public-key path into the
same generated recipe reviewed by the user, so the interactive trust path
uses the same engine preflight as headless installation.

## Operational notes

- Firn runs as root from live media; every destructive action sits behind
  preflight plus (interactively) typed disk confirmation matching the
  disk path or serial — `snosi-install`'s rule, adopted everywhere.
- **A/B installs need an isolated partition namespace:** the A/B image
  carries the same discoverable-partition types/labels as any snosi A/B
  host, so its partition surgery must not run against a device the host
  kernel scans — installers run from media against a bare disk, and the
  A/B E2E installs inside a VM
  ([ADR-0009](../adr/0009-ab-installs-require-partition-isolation.md)).
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
- TPM enrollment for both paths happens at install time against the deployed
  UKI's signed PCR 11 policy (firmware-independent). Firn deliberately does
  not use fisherman's PCR 7 first-boot staging: encrypted bootc must unlock
  before a staged first-boot unit could run.

## References

- Rationale: [ADR-0003](../adr/0003-rewrite-fisherman-as-firn.md),
  [ADR-0004](../adr/0004-single-installer-scope-and-support-matrix.md),
  [ADR-0005](../adr/0005-toml-recipe-model.md),
  [ADR-0006](../adr/0006-install-time-offline-first-flatpaks.md),
  [ADR-0007](../adr/0007-tui-only-frontend-single-binary.md),
  [ADR-0012](../adr/0012-bootc-install-from-ram-installer.md)
- Contracts: [specs/recipe-schema.md](../specs/recipe-schema.md),
  [specs/progress-protocol.md](../specs/progress-protocol.md)
- Built in: [roadmap — Phases 1–7](../plans/roadmap.md)
