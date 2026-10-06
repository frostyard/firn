# Firn architecture

Living document. Rationale:
[ADR-0003](../adr/0003-rewrite-fisherman-as-firn.md),
[ADR-0004](../adr/0004-single-installer-scope-and-support-matrix.md),
[ADR-0005](../adr/0005-toml-recipe-model.md),
[ADR-0006](../adr/0006-install-time-offline-first-flatpaks.md),
[ADR-0007](../adr/0007-tui-only-frontend-single-binary.md),
[ADR-0015 (Proposed)](../adr/0015-bootc-only-installer-scope.md),
[ADR-0016 (Accepted)](../adr/0016-bootc-only-recipe-contract.md),
[ADR-0010](../adr/0010-single-installer-iso-in-snosi.md) (media
boundary: firn ships binary + kiosk unit + contracts; snosi ships the
single installer ISO).
Contracts: [specs/recipe-schema.md](../specs/recipe-schema.md),
[specs/progress-protocol.md](../specs/progress-protocol.md).

<a id="prospective-post-cutoff-scope"></a>
## Current bootc-only recipe boundary

[ADR-0016](../adr/0016-bootc-only-recipe-contract.md) provides the accepted
v2 recipe boundary implemented below. [ADR-0015](../adr/0015-bootc-only-installer-scope.md)
remains Proposed; code scope is not a sole-path or production authorization.
[Roadmap Phase 9](../plans/roadmap.md#phase-9-proposed-post-cutoff-bootc-only-transition-bounded-cross-repo-not-started)
tracks the remaining published-ISO hardware qualification and separate
authorization.

## Overview

Firn is a single Go binary that installs bootc OCI images from a version-2
TOML recipe (temporarily accepting bootc-v1 with a warning), either
headless (`firn install recipe.toml`) or through a built-in TUI wizard
that generates the same recipe and runs the same pipeline in-process.

```
 interactive ──► TUI wizard (bubbletea/huh)
                     │  serializes
                     ▼
                recipe (TOML) ◄──── headless: firn install recipe.toml
                      │  load + validate (fail-closed)
                      ▼
                  preflight ──► bootc pipeline (assembled step list)
                                      │
                               progress events (Go channel)
                                │                   │
                         TUI view          --json-progress emitter
                                               (versioned NDJSON)
```

Before rendering image choices, the wizard validates the entire catalog with
the recipe package's canonical machine-independent image constraints. Built-in
entries are bootc Snow, Snowfield and Floe. An override at
`/etc/firn/catalog.json` containing an A/B entry or unknown field (such as
`product`) is rejected wholesale with a warning and bootc-only built-in
fallback; invalid, empty or unreadable overrides likewise fall back. The
wizard has no family page. The selected catalog entry remains the image state
for later pages and recipe assembly, including after a start-over.

After image selection, an opt-in advanced page exposes the engine's update
and bootloader controls without separating image identity from its catalog trust
policy: `target_ref` and (when Secure Boot is inactive) `bootloader`.
Custom `ref` and `cosign_pub_key` values travel together through the catalog override.
Installer-environment SSH key paths and precomputed user password hashes stay
headless-only; the wizard instead accepts pasted keys and creates its own
private password files. The exact parity/delta table lives in the
[recipe schema](../specs/recipe-schema.md#interactive-wizard-parity).

Each interactive run owns a randomly named 0700 directory below `/run/firn`.
The reviewed recipe references only 0600 secret files in that directory, and
the wizard's canonical serializer returns the accepted review bytes directly
to the command layer. That layer writes them unchanged as `recipe.toml` beside
the secrets, reloads that file, and gives the loaded recipe to the engine.
Start-over, quit, abort, and pre-persistence errors remove abandoned plaintext;
once the recipe is persisted, the directory remains available for the printed
headless reproduction command until the installer environment reboots.

## Design

### Layers and packages

| Layer | Packages (indicative) | Dependency rule |
|---|---|---|
| Frontend | `internal/tui` | Charm stack allowed (ADR-0007) |
| Pipeline | `internal/pipeline`, `internal/steps/*` | stdlib + host tools only |
| Domain | `internal/recipe`, `internal/disk`, `internal/luks`, `internal/enroll`, `internal/sysconfig`, `internal/flatpak`, `internal/progress`, `internal/runner` | stdlib + host tools only (TOML decoder excepted, ADR-0005) |

Like fisherman, privileged work shells out to host tools (`sfdisk`,
`cryptsetup`, `bootc`/`podman`, `flatpak`, `systemd-cryptenroll`,
`mokutil`, …) through a `runner` package with an injectable executor for
tests.

### The step engine

The pipeline is an assembled, ordered list of **steps** — not a
procedural main. Each step declares a name, a progress weight, whether it
is destructive, and `Run(ctx, *Env) error`; `Env` carries the validated
recipe, resolved image metadata, mount/mapper state, and the progress
emitter. Assembly happens once, up front, from the validated bootc recipe;
options (encryption, TPM, Secure Boot,
flatpaks, slurp-style extras later) splice steps in or out. The assembled
list is inspectable, which gives dry-run, accurate progress totals, and
per-step tests for free. Adding a step means writing one and adding it to
assembly — one place, replacing the four-places-per-step bookkeeping of
fisherman's 1,200-line `main()`.

Teardown mirrors assembly: every step that mounts, opens, or maps
something registers an undo on a cleanup stack that runs on any exit
path — closing the mount/mapper-leak class of bug documented in
`snosi-install`. Cleanup warnings are emitted during that unwind, followed by
any accumulated user-facing summary and then the single terminal event on
both success and failure.
Both in-process and NDJSON emitters reject events after the first terminal
event. The step engine retains the first emission failure, stops before
starting another step, unwinds every registered cleanup, and returns the
failure without trying to report it through the same failed emitter. If the
in-process producer disappears before a terminal event, the TUI
and command result synthesize the protocol's `stream_truncated` failure rather
than attributing the crash to user cancellation. The headless human renderer
also consumes fine-grained `step_progress`, so it and the TUI expose the same
pipeline movement at different presentation fidelity.

### Preflight

Before anything destructive: UEFI check (refuse BIOS machines with a
clear diagnostic, ADR-0004), required-tool checks derived from the
assembled step list (each step declares the binaries it needs, so the
check list cannot drift from the code), disk refusal rules — the union of
both installers' rules: mounted anywhere, RAID/LVM member, the installer's
own boot medium, undersized. Unsupported recipes fail validation before
assembly or disk writes.
For bootc recipes that set `image.cosign_pub_key`, preflight selects the
same embedded-or-remote source the install will consume, resolves it to an
immutable digest, verifies that digest with cosign, and carries the pinned
reference into both native-bootc and podman installation paths.

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
recipe schema v2 rejects ZFS because the end-to-end bootable install path is
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

Retired in the bootc-only implementation ([ADR-0016](../adr/0016-bootc-only-recipe-contract.md));
there is no A/B pipeline in Firn. See [ADR-0009](../adr/0009-ab-installs-require-partition-isolation.md)
for the historical partition-isolation requirement.

### System configuration (`internal/sysconfig`)

One package owns the semantics of every `[system]` feature — hostname,
user + groups + password, locale, timezone, keyboard, root/user SSH
authorized keys, flatpak set. Each feature is written through the
**deployment writer**:

- **deployment writer** (bootc): writes into the deployment's `/etc`
  (composefs- and ostree-aware, carried from fisherman's `post` package),
  users via `useradd --root`/chroot, homes in the stateroot.

Shared recipe validation owns input semantics: for example,
user full names accept empty and Unicode GECOS text but reject passwd field
and record delimiters before the writer runs.
Flatpaks follow ADR-0006: copy from the medium's seeded
repo, download the remainder into the mounted target, report (never
silently drop) what was unreachable. With `core_flatpaks`, the core set
comes from first-setup's `core.json` in the deployment, or, when composefs
makes that unreadable ([ADR-0012](../adr/0012-bootc-install-from-ram-installer.md)),
from the ISO's `/usr/share/firn/core-flatpaks.json`; neither depends on the
image being installed.
[ADR-0018 (Proposed)](../adr/0018-image-published-core-flatpaks-label.md)
replaces both with an `org.frostyard.core-flatpaks` label each image
publishes ([roadmap Phase 10](../plans/roadmap.md#phase-10)).

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

### Progress and frontends

`internal/progress` defines one event model (step begin/progress/end,
info, warning, summary items such as unreachable flatpaks, recovery-key
disclosure, and terminal completion or failure). The TUI consumes it over a
channel in-process; `--json-progress` serializes the same events as
versioned NDJSON for automation — the only supported external interface
(ADR-0007), replacing fisherman's stream and snosi's proto-1. The frontend
split preserves warning and error information: the TUI keeps and displays
each stable code alongside a human-readable message. No ordinary
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
  exact configured target disk path — `snosi-install`'s rule, adopted
  everywhere. The disk picker identifies each path with its available vendor,
  model, serial, WWN, transport, size, and filesystem labels before selection;
  long identity records wrap rather than truncate.
- Cleanup stack unwinding must be idempotent: a failed install should
  leave no mounts, no open mappers, and a re-runnable installer without a
  reboot.
- Space-constrained live environments (tmpfs/overlay roots) redirect
  scratch onto the target disk, carried from fisherman's
  `isSpaceConstrained` handling.
- TPM enrollment happens at install time against the deployed
  UKI's signed PCR 11 policy (firmware-independent). Firn deliberately does
  not use fisherman's PCR 7 first-boot staging: encrypted bootc must unlock
  before a staged first-boot unit could run.
- Release and distribution
  ([ADR-0017](../adr/0017-publish-debian-packages-through-apt-publisher.md)):
  a tag runs `.github/workflows/release.yml`, which creates the GitHub
  release, attests its assets' build provenance, and asks
  frostyard/apt-publisher to publish the `frostyard-firn` `.deb` files to
  `https://repository.frostyard.org/debian/` (`trixie` and `forky`).
  apt-publisher then dispatches snosi's rebuild; the installer ISO installs
  its pinned firn version from `trixie`
  ([ADR-0010](../adr/0010-single-installer-iso-in-snosi.md)).

## References

- Rationale: [ADR-0003](../adr/0003-rewrite-fisherman-as-firn.md),
  [ADR-0004](../adr/0004-single-installer-scope-and-support-matrix.md),
  [ADR-0005](../adr/0005-toml-recipe-model.md),
  [ADR-0006](../adr/0006-install-time-offline-first-flatpaks.md),
  [ADR-0007](../adr/0007-tui-only-frontend-single-binary.md),
  [ADR-0012](../adr/0012-bootc-install-from-ram-installer.md)
- Scope and contract: [ADR-0015 (Proposed)](../adr/0015-bootc-only-installer-scope.md),
  [ADR-0016 (Accepted)](../adr/0016-bootc-only-recipe-contract.md),
  [ADR-0018 (Proposed)](../adr/0018-image-published-core-flatpaks-label.md),
  [roadmap Phase 9](../plans/roadmap.md#phase-9-proposed-post-cutoff-bootc-only-transition-bounded-cross-repo-not-started)
- Contracts: [specs/recipe-schema.md](../specs/recipe-schema.md),
  [specs/progress-protocol.md](../specs/progress-protocol.md)
- Release and distribution: [ADR-0017](../adr/0017-publish-debian-packages-through-apt-publisher.md)
- Built in: [roadmap — Phases 1–7](../plans/roadmap.md)
