# ADR-001 — Monorepo structure: mentat and pipeline as separate Go modules

**Status:** Accepted  
**Date:** 2026-04-29

## Context

Firn needs to deliver two distinct tools: `mentat` (repo scanner + documentation generator) and `pipeline` (issue→spec→PR execution engine). These tools have different responsibilities, release cadences, and dependency trees. The question is whether they should live in one Go module, separate modules within a monorepo, or entirely separate repositories.

## Options considered

1. **Single repo, single Go module** — simplest build, but blurs the boundary between tools; a dependency needed by pipeline bleeds into mentat's binary
2. **Single repo, separate Go modules** — clear module boundaries, independent `go.mod`/`go.sum`, can be independently versioned and installed via `go install`; one repo keeps STATE.md and the build system unified
3. **Separate repositories** — maximum isolation but requires coordinating STATE.md across repos, which reintroduces the project management problem we explicitly solved with a single state file

## Decision

Separate Go modules within a single monorepo (`mentat/` and `pipeline/` each with their own `go.mod`). One `Justfile` at the root orchestrates both.

## Consequences

- `go install github.com/frostyard/firn/mentat/cmd/mentat@latest` and `go install github.com/frostyard/firn/pipeline/cmd/pipeline@latest` work independently
- Dependencies don't bleed between tools
- STATE.md, AGENTS.md, and the spec templates live at the repo root, shared by both
- CI must run `just build` and `just test` which exercise both modules
