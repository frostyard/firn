# CLAUDE.md

> See [AGENTS.md](AGENTS.md) for the full navigation map. This file mirrors it for Claude Code compatibility.

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
├── AGENTS.md                    # Full navigation map
├── CLAUDE.md                    # This file
├── README.md                    # Project overview
├── Justfile                     # Build targets — use `just` to build/test
└── .gitignore
```

---

## Build & Test

```bash
just build          # build mentat + pipeline
just test           # test mentat + pipeline
just lint           # go vet both modules
just build-mentat   # build mentat only
just build-pipeline # build pipeline only
just test-mentat    # test mentat only
just test-pipeline  # test pipeline only
```

All builds must pass clean. All tests must pass. `go vet` must be clean. No exceptions before commit.

---

## Code Conventions

### Module paths
- mentat: `github.com/frostyard/firn/mentat`
- pipeline: `github.com/frostyard/firn/pipeline`
- Internal packages: short, lowercase names (`scanner`, `classifier`, `generator`, `watcher`, `worker`)

### Error handling
- Always wrap: `fmt.Errorf("context: %w", err)`
- Return errors; never log and swallow
- Fatal only at `main()` — libraries return `error`
- `errors.Is` / `errors.As` for inspection

### No global state
- No mutable package-level `var`
- Pass config and dependencies explicitly
- Thread `context.Context` through all I/O

### Logging
- `log/slog` only — never `fmt.Println` in library code
- Logger passed via struct field or parameter, never global

### Tests
- New packages always get `_test.go` files
- Table-driven tests for multiple cases
- No network/FS in unit tests — use interfaces and fakes
- Integration tests: `_integration_test.go` with `//go:build integration`

### Git
- Conventional commits: `feat|fix|refactor|build|ci|chore|docs|style|perf|test`
- One branch per task: `mentat/scanner`, `pipeline/issue-watcher`, etc.
- Never push directly to `main`

---

## Anti-Patterns

| ❌ Don't | ✅ Do instead |
|---|---|
| `log.Fatal(err)` in a library function | Return the error to the caller |
| `var globalConfig Config` at package level | Pass `Config` as a parameter or struct field |
| `fmt.Println("debug: ...")` | `slog.Debug("...", "key", val)` |
| Silent error drop | `return fmt.Errorf("context: %w", err)` |
| String-match on error messages | `errors.Is(err, ErrNotFound)` |
| `http.DefaultClient` in library code | Accept `*http.Client` as parameter |
| Touching 3+ files without an exec plan | Write `docs/exec-plans/active/FEATURE.md` first |
| Marking a task done without updating `STATE.md` | Update `STATE.md` in the same commit |

---

## Agent Roles

| Role | Component | Responsibility | May write to |
|------|-----------|----------------|--------------|
| Scanner | mentat | Directory walk, file classification, domain candidate list | `mentat/` source |
| Classifier | mentat | Single LLM call → domain assignments | `mentat/` source |
| Generator | mentat | Per-domain SKILL.md generation | Target repo `.agents/skills/{domain}/` |
| Refiner | pipeline | Issue → spec PR (product.md + tech.md) | `specs/GH{N}/`, `pipeline/` source |
| Worker | pipeline | Merged spec → implementation draft PR | Files named in tech spec only |
| Verifier | pipeline | Invariant checks, PASS/FAIL per invariant | Nothing |

**Verifier output:** Plain text `PASS:` or `FAIL:` lines — no Markdown.

---

## Exec Plans

Any change touching 3+ files requires `docs/exec-plans/active/FEATURE.md` before writing code.

Required sections: Purpose (>20 words), Baseline (`just test`), Milestones (each with a verify command naming specific files), Surprises & Discoveries, Decision Log, Outcomes & Retrospective.

Plans are live — check off milestones immediately, record surprises as they happen.

---

## State Management

1. Read `STATE.md` at the start of every session
2. Update task `status` to `done` and add to Completed in the same commit when finishing a task
3. Do not start tasks whose `depends_on` are not `done`
4. `parallel: true` + no unfulfilled deps = can run concurrently
