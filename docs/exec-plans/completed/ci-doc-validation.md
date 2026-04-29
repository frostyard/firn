# CI Documentation Ceremony Validation

## Purpose
Add a GitHub Actions workflow that enforces firn's documentation ceremony on every PR: large PRs
must carry an exec plan, changes to docs/design/ should follow ADR conventions, and STATE.md must
remain YAML-valid so the automated pipeline can always parse task state.

## Baseline
`just test` passes clean before any changes (builds + tests for both Go modules).

## Milestones
- [x] Milestone 1 — Write exec plan at `docs/exec-plans/active/ci-doc-validation.md`; verify: file exists
- [x] Milestone 2 — Create `.github/workflows/doc-validation.yml` with the three validation steps; verify: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/doc-validation.yml'))"`
- [x] Milestone 3 — Move exec plan from `active/` to `completed/`; verify: file present in completed/
- [x] Milestone 4 — Update `STATE.md` to mark `ci-doc-validation` done; verify: `grep "status: done" STATE.md | grep ci-doc`

## Surprises & Discoveries
- The main branch was checked out in a separate worktree (`firn-philosophy-wt`); branching was done there.
- No `workflows/` sub-directory existed — it was created alongside `release.yml` and `snapshot.yml` in an earlier wave; those files were already present.

## Decision Log
- Exec plan check is a **hard failure** (exit 1) — a PR with 3+ changed files and no exec plan must not merge.
- ADR check is a **warning only** — `docs/design/` changes don't always warrant a new ADR (e.g., template updates).
- STATE.md validation is a **hard failure** — broken YAML would silently corrupt pipeline task state.
- `python3 -c "import yaml …"` used directly in the workflow; `pyyaml` is available on `ubuntu-latest` runners without extra install steps.
- `fetch-depth: 0` on checkout is required so `git diff origin/main...HEAD` has full history.

## Outcomes & Retrospective
All three validation steps implemented. Workflow YAML validated locally via `python3 yaml.safe_load`.
Hard failures prevent merging broken state; soft warning on ADR keeps the check non-blocking for
legitimate non-ADR design-folder edits. Total workflow runtime well under 30 seconds (pure shell +
one Python snippet, no compilation or network fetches).
