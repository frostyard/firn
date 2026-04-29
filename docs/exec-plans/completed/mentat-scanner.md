# mentat-scanner — Repo Scanner with Domain Candidate Detection

## Purpose
Implement the `mentat/internal/scanner` package that walks a repository tree, skips
structural containers and noise directories, counts source files per directory, and returns
a list of domain `Candidate` structs for downstream classification. Wire the scanner into
the existing `syncCmd` stub so that `mentat sync` produces real output.

## Baseline
Confirmed: `just test` passes clean before any changes.

## Milestones
- [x] Milestone 1 — Write `docs/exec-plans/active/mentat-scanner.md` (this file); verify: file exists
- [x] Milestone 2 — Implement `mentat/internal/scanner/scanner.go` with `Config`, `Candidate`, `Scan`, `DefaultConfig`; verify: `cd mentat && go build ./...`
- [x] Milestone 3 — Write `mentat/internal/scanner/scanner_test.go` with table-driven tests for skip, container descent, and MinFiles threshold; verify: `cd mentat && go test ./internal/scanner/...`
- [x] Milestone 4 — Wire scanner into `mentat/cmd/mentat/commands.go` `syncCmd`; verify: `cd mentat && go build ./...`
- [x] Milestone 5 — Full suite clean: `cd mentat && go test ./...` and `cd mentat && go vet ./...`; verify: exit code 0
- [x] Milestone 6 — `just build-mentat` clean; verify: exit code 0
- [x] Milestone 7 — Commit, push branch, open PR; verify: PR visible on GitHub
- [x] Milestone 8 — Update `STATE.md` task `mentat-scanner` to `done`, move exec plan to `completed/`

## Surprises & Discoveries
- git branch switching between shell sessions caused file loss; had to recreate files on the correct branch.

## Decision Log
- `Scan` signature takes `context.Context` as first arg to honor cancellation on large repos, per AGENTS.md convention
- Language detection is extension-based (fast, no AST needed at scan phase); LLM classification handles semantics
- `ContainerDirs` descend into children: `src/auth/` → candidate `src/auth`, not `src`
- Depth cap at 3 levels via `Config.MaxDepth` (default 3) — avoids pathologically deep vendored trees
- Unit tests use `os.MkdirTemp` + real FS (scanner is pure filesystem I/O); this is idiomatic Go for scanner testing

## Outcomes & Retrospective
- All 8 milestones completed.
- 12 tests added covering: basic candidates, skip dirs, container descent, MinFiles threshold (default and custom), language detection, file count accuracy, MaxDepth limit, context cancellation, DefaultConfig values, and 4 table-driven scenarios.
- `syncCmd` now calls `scanner.Scan`, prints text output (one candidate per line) or JSON array, and respects `--dry-run` and `--json` flags.
- `just build-mentat`, `go test ./...`, and `go vet ./...` all exit 0.
