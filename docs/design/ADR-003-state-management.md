# ADR-003 — State management: git-committed STATE.md with dependency task graph

**Status:** Accepted  
**Date:** 2026-04-29

## Context

Firn is a multi-phase project executed across multiple agent sessions. The orchestrating agent (Miles/Pie) resets context on every session. Without external state, the project manager role falls to human memory or gets reconstructed expensively from git history each time.

Previous attempts (Yeti, Rime) did not have a single authoritative state file. This contributed to work being duplicated, phases being unclear, and parallel dispatch being ad-hoc.

## Options considered

1. **GitHub Projects / Issues as state** — requires network access, not readable cold without API calls, doesn't express dependency metadata cleanly
2. **PLAN.md (flat checklist)** — simple but no machine-readable dependency graph; can't drive parallel dispatch without parsing
3. **STATE.md with YAML task graph** — structured, git-committed, readable cold by any agent; supports `depends_on`, `parallel`, and `status` fields; enables deterministic parallel dispatch

## Decision

`STATE.md` at the repo root contains a YAML task graph. Every task has: `id`, `description`, `status` (pending/in-flight/done), `depends_on` (list of task ids), `parallel` (bool). At session start, Miles reads STATE.md, dispatches all tasks with `status: pending` and satisfied dependencies. Updates are committed as part of the work.

## Consequences

- No agent or human needs to remember project state between sessions — STATE.md is always the source of truth
- Tasks must not be marked `done` until the corresponding PR is merged and verified; pre-emptive status updates are a process error
- STATE.md conflicts can occur when parallel subagents both modify it — this is a known limitation until worktree isolation is implemented (see ADR-005)
- Git history of STATE.md is the project audit trail
