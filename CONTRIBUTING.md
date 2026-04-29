# Contributing to Firn

Thanks for your interest. Firn is early-stage and the workflow is intentionally structured — read this before opening a PR.

## The short version

1. **Discuss first** — open an issue before implementing anything non-trivial
2. **Exec plan for large changes** — any PR touching 3+ files needs an exec plan in `docs/exec-plans/active/` before you write code (CI will fail without one)
3. **ADR for architecture** — decisions about how the system works go in `docs/design/ADR-NNN-slug.md` using the template
4. **Branch + PR** — no direct commits to main

## Philosophy

Read `docs/PHILOSOPHY.md`. It explains what we're building, why the workflow is structured the way it is, and what the research foundation is. It will save you time.

## Development

```bash
# Build everything
just build

# Test everything
just test

# Lint
just lint
```

Both `mentat/` and `pipeline/` are separate Go modules. See `AGENTS.md` for the full development guide including conventions, anti-patterns, and agent roles.

## Exec plans

For any change touching 3+ files, write a plan at `docs/exec-plans/active/FEATURE.md` before writing code. The format is in `AGENTS.md`. Move it to `docs/exec-plans/completed/` when the PR merges.

CI validates this. PRs without exec plans fail the doc-validation check.

## ADRs

Architectural decisions live in `docs/design/ADR-NNN-slug.md`. Use `docs/design/ADR-TEMPLATE.md`. Check existing ADRs before proposing something that may already be settled.

## Spec format

If you're contributing via the pipeline workflow (issue → spec → PR), the spec format is in `specs/template/`. Product specs use numbered behavioral invariants. Tech specs use file:line references.
