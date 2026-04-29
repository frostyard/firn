# GoReleaser Pro Setup

## Purpose
Add a GoReleaser Pro configuration and GitHub Actions release/snapshot workflows so that
both `mentat` and `pipeline` binaries are cross-compiled and published as GitHub Release
assets whenever a `v*` tag is pushed, and snapshot builds are verified on every push to main.

## Baseline
`just test` passes clean on `main` before any changes (confirmed).

## Milestones
- [x] Milestone 1 — Write `docs/exec-plans/active/goreleaser-setup.md` (this file)
- [x] Milestone 2 — Write `.goreleaser.yml` at repo root with two build entries (mentat + pipeline), archives, checksum, changelog, release; verify: `goreleaser check`
- [x] Milestone 3 — Write `.github/workflows/release.yml` (tag-triggered GoReleaser Pro release)
- [x] Milestone 4 — Write `.github/workflows/snapshot.yml` (push-to-main snapshot build)
- [x] Milestone 5 — Update `Justfile` with `snapshot` and `release` targets; verify: `just --list`
- [x] Milestone 6 — Update `.gitignore` to include `dist/`; verify: `just build` still passes
- [x] Milestone 7 — Update `STATE.md` marking `goreleaser-setup` done; move exec plan to `completed/`
- [x] Milestone 8 — Commit and open PR on branch `build/goreleaser`

## Surprises & Discoveries
- GoReleaser Pro v2 has deprecated `archives[].builds` in favour of `archives[].ids`. Caught by `goreleaser check` on first run; fixed immediately.

## Decision Log
- Using GoReleaser Pro `version: 2` schema (current as of 2.x releases)
- `goreleaser check` used for config validation since we cannot do a full release without `GORELEASER_KEY` set in CI
- `.gitignore` already has `*/dist/` for sub-module dist dirs; added root-level `dist/` because goreleaser writes to `./dist/` at repo root
- Used `archives[].ids` (not deprecated `builds`) per GoReleaser v2 schema

## Outcomes & Retrospective
All 8 milestones completed cleanly. `goreleaser check` passes with no warnings. `just build` passes with exit 0. `just --list` shows `snapshot` and `release` targets. STATE.md updated, exec plan moved to completed/.
