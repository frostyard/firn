# ADR-007 — Release tooling: GoReleaser Pro

**Status:** Accepted  
**Date:** 2026-04-29

## Context

Both `mentat` and `pipeline` are Go binaries intended for distribution. Users need to install them via `go install`, download pre-built binaries, or use package managers. Manual release processes don't scale and are error-prone.

## Options considered

1. **`go install` only** — zero config, but no pre-built binaries, no checksums, no homebrew tap, no GitHub release assets
2. **GoReleaser (OSS)** — standard Go release tool; handles cross-compilation, GitHub releases, checksums, archives; free but lacks some distribution features
3. **GoReleaser Pro (licensed)** — adds: monorepo support (build only changed modules), nightly builds, split archives per module, enhanced homebrew tap generation, docker manifests, AUR, Chocolatey; Brian has a Pro license

## Decision

Use GoReleaser Pro. The monorepo support is directly relevant — mentat and pipeline can release independently without coupling their versions. A `.goreleaser.yml` at the repo root configures both binaries with their ldflags.

## Consequences

- `GORELEASER_KEY` must be set in GitHub Actions secrets
- Releases triggered by pushing a tag (`v*`) — both binaries release together from one tag for now; can split to per-module tags later with GoReleaser Pro's monorepo config
- Pre-built binaries available for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64
- `just release` target in Justfile wraps `goreleaser release`
- `just snapshot` wraps `goreleaser release --snapshot --clean` for local testing without publishing
