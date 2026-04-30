---
name: firn-workflow
description: "The contribution workflow for firn-patterned repositories: exec plans before code, ADRs for architecture, Warp spec format for issues, PR ceremony. Use when contributing to a repo that uses firn conventions (has AGENTS.md, docs/design/, docs/exec-plans/, specs/)."
---

# firn-workflow — Contribution Workflow

Trigger: contributing to a repo with `AGENTS.md`, `docs/design/`, `docs/exec-plans/`, or `specs/` directories — or when the user mentions "exec plan", "ADR", "spec PR", or "firn workflow".

**The short version:**
- 3+ file changes → write exec plan first
- Architecture decision → write ADR first
- New feature issue → spec PR (product.md + tech.md) before implementation PR
- All changes via branch + PR, never direct to main

---

## Exec Plans

**Required for any change touching 3 or more files.**

1. Create `docs/exec-plans/active/FEATURE.md` before writing any code
2. Use this format:

```markdown
# Feature Name

## Purpose
[>20 words explaining what this does and why]

## Baseline
Run `just test` (or equivalent) to confirm baseline passes before starting.

## Milestones
- [ ] Milestone 1 — [what to do]; verify: `[command that confirms it worked]`
- [ ] Milestone 2 — ...

## Decision Log
[Key choices made and why]

## Outcomes & Retrospective
[Fill in after completion]
```

**Rules:**
- Every milestone must have a verification command — "it should work" is not a verification
- File paths must be specific — name the actual file, not "update the config"
- Move to `docs/exec-plans/completed/` when the PR merges

3. The PR template checklist requires an exec plan for 3+ file changes. CI will fail without one.

---

## ADRs (Architectural Decision Records)

**Required when making a significant architectural decision.**

1. Check `docs/design/` — don't re-litigate settled decisions
2. Create `docs/design/ADR-NNN-slug.md` using `docs/design/ADR-TEMPLATE.md`
3. Format:

```markdown
# ADR-NNN — Short decision title

**Status:** Accepted
**Date:** YYYY-MM-DD

## Context
[What situation prompted this? What constraints exist?]

## Options considered
1. **Option A** — tradeoffs
2. **Option B** — tradeoffs

## Decision
[What was decided, stated plainly.]

## Consequences
[What becomes true after this decision — positive and negative.]
```

4. The CI doc-validation workflow warns (not fails) when `docs/design/` is modified without a new `ADR-NNN` file.

---

## Spec Format (for pipeline issues)

When a GitHub issue is labeled `needs-spec`, a spec PR is opened before any implementation. The spec lives at `specs/GH{N}/`:

**`specs/GH{N}/product.md`** — numbered behavioral invariants, no implementation detail:
```markdown
# Feature Name — Product Spec

**Issue:** GH#N
**Status:** Draft

## Behavioral Invariants
1. [Observable outcome in scenario X — no "how", only "what"]
2. [Observable outcome in scenario Y]

## Out of Scope
- [Explicit non-goal]
```

**`specs/GH{N}/tech.md`** — line-referenced implementation plan:
```markdown
# Feature Name — Technical Spec

## Implementation Plan
- `path/to/file.go:42-87` — what to change and why
- `path/to/new-file.go` — new file: what it does

## Verification
1. Invariant 1: `[test command or assertion]`
2. Invariant 2: `[test command or assertion]`
```

**The spec is a PR itself.** It merges before implementation begins. This forces review of intent before anyone writes code.

---

## PR Checklist

Every PR must satisfy (enforced by `.github/pull_request_template.md` and CI):

- [ ] `just build` passes
- [ ] `just test` passes
- [ ] `just lint` passes
- [ ] If 3+ files changed: exec plan in `docs/exec-plans/`
- [ ] `STATE.md` updated if a wave task completed
- [ ] No direct push to main

---

## Repo Layout

```
docs/
├── design/          ADRs — one per architectural decision
│   └── ADR-TEMPLATE.md
├── exec-plans/
│   ├── active/      In-progress exec plans
│   └── completed/   Archived after merge
└── PHILOSOPHY.md    Why the project is designed this way

skills/              Reusable agent skills (copy to .agents/skills/ in your repo)
specs/               Spec PRs — one directory per issue (GH{N}/product.md + tech.md)
STATE.md             Task graph — the project manager (see [[state-md]] skill)
AGENTS.md            Agent navigation map for this repo
```

---

## Agent Roles in firn

| Role | Responsibility | Writes to |
|------|---------------|-----------|
| **Scanner** | Domain detection, repo analysis | `mentat/` source |
| **Classifier** | LLM domain classification | `mentat/internal/classifier/` |
| **Generator** | SKILL.md generation | `mentat/internal/generator/` |
| **Refiner** | Spec generation from issues | `pipeline/internal/specgen/` |
| **Worker** | Implementation from merged spec | `pipeline/internal/worker/` |
| **Verifier** | Post-PR invariant checks | read-only |

---

## LLM Output Validation

When invoking LLM CLIs (claude, copilot, codex) to generate file content:

1. **Disable agent tools.** Tools-enabled mode produces scaffolding (failed tool calls, status lines) instead of content. Use `--available-tools=` (copilot) or equivalent.
2. **Validate before writing.** Check that the output starts with the expected structure (e.g. YAML frontmatter for SKILL.md). If missing, inject it rather than writing garbage.
3. **Strip trailing stats.** Some CLIs append `Changes +0 -0 / Requests N` lines. Trim before writing.

---

## References

- [[state-md]] — how to work with the STATE.md task graph
- `docs/PHILOSOPHY.md` — why firn is designed the way it is
- `AGENTS.md` — full agent navigation map for this repo
- `docs/design/` — all architectural decisions
