# ADR-004 — Spec format: Warp product.md + tech.md

**Status:** Accepted  
**Date:** 2026-04-29

## Context

The primary failure mode of prior agentic issue→PR pipelines (Yeti's `issue-refiner` + `issue-worker`) was freeform specs with no definition of done. `issue-worker` had no anchor — "CI passes" was necessary but not sufficient, and implementations regularly drifted from intent.

## Options considered

1. **Freeform Markdown in issue comment (Yeti model)** — easy to produce, hard to parse, no enforced structure; implementations drift
2. **Warp spec format: product.md + tech.md as repo files** — `product.md` contains numbered testable behavioral invariants (no implementation detail); `tech.md` contains line-referenced implementation steps; both committed to the repo as a PR before any implementation begins
3. **GitHub issue structured fields** — limited formatting, buried in issue history, not version-controlled

## Decision

Adopt the Warp spec format. Specs live at `specs/GH{N}/product.md` + `specs/GH{N}/tech.md`. The spec is opened as a PR first — reviewed and merged before any implementation PR. The implementation PR references the spec by path.

## Consequences

- **Good friction:** the spec PR is a mandatory review checkpoint. Community can correct intent before anyone codes. The spec becomes permanent project documentation.
- `pipeline-spec-generator` must produce valid `product.md` (numbered invariants, no implementation detail) and `tech.md` (file:line references) — not freeform text
- `pipeline-issue-worker` reads `tech.md` as its task list and asserts each `product.md` invariant before marking the PR ready for review
- Spec files accumulate in `specs/` — this is intentional; they are the decision history of the project
