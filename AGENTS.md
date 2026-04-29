# Firn — Agent Navigation Map

Firn is an agentic toolchain for the frostyard ecosystem with two components:

- **mentat** — scans a repository, classifies its code into domains, and generates per-domain `SKILL.md` files under `.agents/skills/{domain}/`. Keeps documentation fresh by detecting changed files between runs.
- **pipeline** — issue-to-PR execution engine: watches GitHub issues, generates spec PRs (product.md + tech.md), implements them in isolated worktrees, and verifies results against behavioral invariants.

**Always read `STATE.md` before starting any task.** It is the authoritative task graph. Update it when tasks complete.

---

## Directory Map

```
firn/
├── mentat/                      # Go module: github.com/frostyard/firn/mentat
│   ├── cmd/mentat/main.go       # CLI entry point (cobra)
│   └── ...                      # scanner/, classifier/, generator/ packages (to be added)
├── pipeline/                    # Go module: github.com/frostyard/firn/pipeline
│   ├── cmd/pipeline/main.go     # CLI entry point (cobra)
│   └── ...                      # watcher/, spec/, worker/, config/ packages (to be added)
├── specs/
│   └── template/
│       ├── product.md           # Behavioral invariants template — copy for each issue
│       └── tech.md              # Line-referenced implementation plan template
├── docs/
│   ├── design/                  # ADRs — ADR-NNN-slug.md format
│   └── exec-plans/
│       ├── active/              # In-progress execution plans (required for 3+ file changes)
│       └── completed/           # Archived plans — move here when done
├── .github/
│   ├── ISSUE_TEMPLATE/
│   │   └── feature.md           # Feature request → triggers needs-spec label
│   └── workflows/               # CI workflows (placeholder — add as needed)
├── STATE.md                     # Task graph — READ THIS FIRST
├── AGENTS.md                    # This file
├── CLAUDE.md                    # Claude Code harness rules (mirrors AGENTS.md)
├── README.md                    # Project overview
├── Justfile                     # Build targets — use `just` to build/test
└── .gitignore
```

---

## Build & Test

Both components are independent Go modules. Use `just` for all build/test operations:

```bash
just build          # build mentat + pipeline
just test           # test mentat + pipeline
just lint           # go vet both modules
just build-mentat   # build mentat only
just build-pipeline # build pipeline only
just test-mentat    # test mentat only
just test-pipeline  # test pipeline only
```

**Manual module commands** (if not using just):
```bash
cd mentat  && go build ./... && go test ./...
cd pipeline && go build ./... && go test ./...
```

All builds must pass clean. All tests must pass. `go vet` must be clean. No exceptions before commit.

---

## Code Conventions

### Module paths
- mentat: `github.com/frostyard/firn/mentat`
- pipeline: `github.com/frostyard/firn/pipeline`
- Internal packages use short, lowercase names: `scanner`, `classifier`, `generator`, `watcher`, `worker`

### Error handling
- **Always wrap errors with context**: `fmt.Errorf("scanning %s: %w", path, err)`
- Return errors; do not log and swallow
- Fatal only at `main()` — all other packages return `error`
- Use `errors.Is` / `errors.As` for error inspection, never string matching

### No global state
- No package-level `var` that holds mutable runtime state
- Pass config and dependencies explicitly via structs or function parameters
- Use `context.Context` for cancellation and deadline propagation — thread it through all I/O calls

### Logging
- Use `log/slog` (stdlib structured logger) — no `fmt.Println` in library code
- Logger instance passed via struct field or function parameter, never global
- Log levels: `Debug` for scan details, `Info` for phase transitions, `Warn` for skipped items, `Error` for failures

### File size
- Split files over 300 lines into sub-packages
- One exported type per file is a good default; group closely related small types

### Tests
- New packages and pure functions always get `_test.go` files
- Table-driven tests for anything with multiple cases
- No network/filesystem calls in unit tests — use interfaces and fakes
- Integration tests (require real FS or network) go in `_integration_test.go` with `//go:build integration`

### Git / Commits
- Conventional commits: `feat|fix|refactor|build|ci|chore|docs|style|perf|test`
- One branch per task/exec plan: `mentat/scanner`, `pipeline/issue-watcher`, etc.
- Never push directly to `main` — all changes via branch + PR
- Never `--no-verify`; never force-push to main

---

## Anti-Patterns

| ❌ Don't | ✅ Do instead |
|---|---|
| `log.Fatal(err)` in a library function | Return the error to the caller |
| `var globalConfig Config` at package level | Pass `Config` as a parameter or struct field |
| `fmt.Println("debug: ...")` | `slog.Debug("...", "key", val)` |
| `err != nil { return }` (silent drop) | `return fmt.Errorf("context: %w", err)` |
| String-match on error messages | `errors.Is(err, ErrNotFound)` |
| `http.DefaultClient` in library code | Accept `*http.Client` as parameter |
| Hardcoding `github.com/frostyard/someother` imports | Each module is self-contained; use interfaces for cross-module contracts |
| Touching 3+ files without an exec plan | Write `docs/exec-plans/active/FEATURE.md` first |
| Marking a task done without updating `STATE.md` | Update `STATE.md` in the same commit |
| `go get` adding an indirect dep without a comment | Add a comment in go.mod explaining why |

---

## Agent Roles

### Scanner (mentat)
**Responsibility:** Repository analysis — directory walk, file classification, domain detection.  
**May write to:** `mentat/` source files, `docs/exec-plans/active/` (for multi-file changes)  
**Tools:** Read, Glob, Grep, Bash (read-only for filesystem analysis)  
**Constraint:** Does not write SKILL.md files directly — produces candidate list for Classifier

### Classifier (mentat)
**Responsibility:** Single LLM call to assign scanner candidates to named domains (e.g., `auth`, `storage`, `api`).  
**May write to:** `mentat/` source files  
**Constraint:** Classification is cheap and non-generative — one structured call, not one-per-file

### Generator (mentat)
**Responsibility:** Per-domain LLM generation of SKILL.md content; writes to `.agents/skills/{domain}/SKILL.md` in the target repo.  
**May write to:** Target repo skill files only (never to firn's own source)  
**Constraint:** Only regenerates domains where files have changed since last run (mtime / git diff)

### Refiner (pipeline)
**Responsibility:** Reads a GitHub issue labeled `needs-spec`, generates `specs/GH{N}/product.md` + `specs/GH{N}/tech.md`, opens a spec PR.  
**May write to:** `specs/GH{N}/`, `pipeline/` source files  
**Constraint:** Spec PR must be reviewed/merged by a human before Worker activates

### Worker (pipeline)
**Responsibility:** Reads merged spec, creates implementation branch, implements the feature, opens a draft PR.  
**May write to:** Files named in the tech spec's implementation plan only  
**Constraint:** Draft PR first. Promote to ready only after invariant verification passes. Max 3 CI-fixer attempts.

### Verifier (pipeline)
**Responsibility:** Post-PR checks — runs verification commands from tech spec, reports PASS/FAIL per invariant.  
**May write to:** Nothing  
**Tools:** Read, Bash (test commands only)  
**Output format:** Plain text lines starting with `PASS:` or `FAIL:` — no Markdown formatting

---

## Exec Plans

**Any change touching 3 or more files requires an exec plan before writing code.**

Create `docs/exec-plans/active/FEATURE.md` with these sections:

```markdown
# [Feature Name]

## Purpose
[>20 words: what this plan achieves and why]

## Baseline
Run `just test` to confirm baseline passes before any changes.

## Milestones
- [ ] Milestone 1 — [description]; verify: `just test-mentat`
- [ ] Milestone 2 — [description]; verify: `just build`
- [ ] Milestone 3 — [description]; verify: `just test`

Name specific files in each milestone — not "update config" but "update `mentat/config.go`".

## Surprises & Discoveries
(fill in as you work)

## Decision Log
(record non-obvious choices)

## Outcomes & Retrospective
(fill in before moving to completed/)
```

**Plans are live documents:**
- Check off milestones immediately when done — do not batch
- Record surprises as they happen
- Fill in Outcomes before moving to `docs/exec-plans/completed/`
- When resuming, verify checkboxes match reality before writing code

---

## State Management

`STATE.md` is the project manager. It contains:
- Current phase
- Full task graph with `status`, `depends_on`, and `parallel` fields
- Completed task list
- Architectural decisions

**Rules:**
1. Read `STATE.md` at the start of every session
2. When a task completes, update its `status` to `done` in the task graph AND add it to the Completed section in the same commit
3. Do not start a task whose `depends_on` tasks are not `done`
4. Parallel tasks (marked `parallel: true`) with no unfulfilled `depends_on` can run concurrently
5. After each wave, commit `STATE.md` with a message like `chore: wave 1 complete — update task graph`

---

## Spec Workflow

Issues flow through this lifecycle:
```
GitHub Issue (labeled needs-spec)
  → Refiner generates specs/GH{N}/product.md + tech.md
  → Spec PR opened for human review
  → Human approves and merges spec PR
  → Worker reads merged spec, creates implementation branch
  → Worker opens draft PR
  → Verifier runs invariant checks → PASS/FAIL per invariant
  → Draft → Ready when all PASS
  → Human reviews + merges
```

Spec files live at `specs/GH{N}/product.md` and `specs/GH{N}/tech.md`. Never implement from an unmerged spec.

---

## Architectural Decisions

ADRs live in `docs/design/ADR-NNN-slug.md`. Check existing ADRs before re-litigating a settled decision.

Current decisions (see also STATE.md Decisions table):
- Monorepo with two independent Go modules (`mentat/` + `pipeline/`)
- PR throttle default: 3 concurrent open PRs
- Spec location: repo file at `specs/GH{N}/`
- Domain detection: directory heuristics + single LLM classification pass
- State: `STATE.md`, git-committed, updated per wave
