# 0003 — Rewrite fisherman as firn rather than hard-forking

- **Status:** Accepted
- **Date:** 2026-08-11

## Context

Snosi ships two image families, each with its own installer today:

- **bootc OCI images** (snow, snowfield, cayo), installed by
  [frostyard/fisherman](https://github.com/frostyard/fisherman) — a
  recipe-driven Go installer forked from tuna-os/fisherman, now 374 commits
  diverged (its own `internal/secure` snosi path, release infrastructure,
  governance tooling). Upstream itself has moved to projectbluefin/fisherman.
- **native A/B disk images** (snow-ab, snowfield-ab, cayo-ab) — whole-disk
  GPT images using Discoverable Partitions Spec types (DDIs in the systemd
  sense), erofs root sealed with dm-verity — installed by `snosi-install`,
  an 1,863-line bash script with a GTK4 kiosk GUI living at
  `snosi/shared/native-installer`.

The goal is one installer for all snosi images. Relevant facts about the
two candidates for a starting point:

- Fisherman's internals are high quality (259 test functions, fail-closed
  validation, comments citing the concrete incident behind each workaround),
  but its structure resists extension: the entire install pipeline is a
  ~1,200-line procedural `main()` where adding a step means touching four
  places; the TUI module is unintegrated and partly broken (it invokes an
  `install` subcommand that does not exist, and collects SSH keys it then
  discards); a dead Python/GTK4 frontend and stale meson/flatpak packaging
  remain in-tree; the module path still says `github.com/tuna-os/fisherman`.
- Fisherman declares GPL-3.0-only but ships no LICENSE file.
- `snosi-install` supports configuration fisherman lacks (locale, timezone,
  keyboard, root SSH authorized key, `/etc`-overlay seeding, TPM-enrolled
  encrypted `/var`) and vice versa (recipes, filesystem choice, LUKS root,
  btrfs/ZFS). The two share no code and have independently invented the
  same concepts: tool preflight checks, disk enumeration/refusal rules,
  and two incompatible line-delimited JSON progress protocols.
- A hard git fork would carry fisherman's history and structure forward and
  leave firn's actual vision expressed as diffs against someone else's
  decisions. This repository's docs framework (ADR/design/spec/plan) exists
  precisely to record decisions from the beginning.

## Decision

Firn is a new Go codebase, deeply inspired by fisherman rather than a git
fork of it. Where fisherman (or `snosi-install`) has already solved a
problem well, firn copies the code rather than reinventing it, preserving
upstream attribution per copied file and in a repository-level NOTICE.

Firn is licensed **GPL-3.0-only**, matching fisherman's declared license,
and ships an actual `LICENSE` file containing the GPL-3.0 text — closing
the gap upstream never did. The NOTICE acknowledges the lineage:
tuna-os/fisherman → projectbluefin/fisherman → frostyard/fisherman.

Go is the implementation language: it is what fisherman is written in (so
copied code stays copyable verbatim), it produces static binaries suited to
live-ISO environments, and the backend can follow fisherman's zero-external-
dependency discipline.

## Consequences

- Firn gets a clean structure designed for the union of both install paths,
  with decisions recorded as ADRs from day one instead of inherited as
  history. Dead weight (Python frontend, broken TUI seam, stale packaging)
  is never imported.
- All of fisherman is legally copyable into firn without license friction;
  copied files keep their provenance comments and headers.
- **We re-earn fisherman's battle scars.** Every dated workaround comment in
  fisherman encodes a real incident (sfdisk reread races, live-ISO space
  constraints, PCR 7 drift in installers). Obligation: when implementing an
  area fisherman already covers, consult the corresponding fisherman code
  first, and when copying, keep its incident comments intact.
- Two installers exist during the transition. frostyard/fisherman continues
  to serve the bootc path and `snosi-install` the A/B path until firn
  reaches parity with each; their retirement is a future decision, not made
  here.
- Obligation: `LICENSE` (GPL-3.0 text) and `NOTICE` (attribution) must exist
  in the repository root before any code is copied in.

## Alternatives considered

- **Hard fork of frostyard/fisherman:** inherits ~20k LOC including dead
  code, the monolithic pipeline, a wrong module path, and an unintegrated
  TUI; the A/B path would be bolted onto a structure never designed for
  it, and firn's vision would live as deltas rather than decisions.
- **Extend `snosi-install`:** 1,863 lines of bash with documented structural
  fragility (dynamic scoping across function boundaries, device paths
  returned via stdout, a hand-rolled `useradd` in awk); the wrong base for
  a multi-target installer with a TUI.
- **Keep two installers (status quo):** duplicated preflight, disk-refusal,
  and progress-protocol logic, two UIs and two test harnesses to maintain;
  this is the situation firn exists to end.

## References

- Shapes: [design/architecture.md](../design/architecture.md)
- Builds on: [ADR-0001](0001-record-architecture-decisions.md),
  [ADR-0002](0002-agent-portable-instruction-surface.md)
