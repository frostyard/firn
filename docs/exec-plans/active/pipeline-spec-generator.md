# pipeline-spec-generator

## Purpose
Implement the `specgen` package for the pipeline module that takes a GitHub issue, calls an LLM twice (product.md + tech.md), writes both files to `specs/GH{N}/`, and opens a spec PR via the `gh` CLI — completing the first automated step of the issue-to-PR workflow.

## Baseline
`cd pipeline && go test ./... && go vet ./...` — all green before any changes.

## Milestones
- [x] Write exec plan at `docs/exec-plans/active/pipeline-spec-generator.md`
- [x] Create `pipeline/internal/specgen/specgen.go` — `Config`, `SpecResult`, `LLMCaller` interface, `GenerateSpec()`, `newCaller()`, LLM backend stubs, `GHRunner` interface for `gh pr create`; verify: `cd pipeline && go build ./...`
- [x] Create `pipeline/internal/specgen/backends.go` — claude/openai/ollama backends copied from classifier pattern; verify: `cd pipeline && go build ./...`
- [x] Create `pipeline/internal/specgen/specgen_test.go` — mock LLMCaller + GHRunner, table-driven tests for 5 cases; verify: `cd pipeline && go test ./...`
- [x] Wire `specgen.GenerateSpec()` into `runCmd` in `pipeline/cmd/pipeline/main.go`; verify: `just build-pipeline && just test-pipeline`

## Surprises & Discoveries
- `io.Discard` sentinel keeps the `io` import live in specgen.go; `jsonDecode` helper lives in backends.go to avoid double-importing `encoding/json`.
- `createPR` passes `--head spec/GH{N}` and trusts production callers to have pushed the branch. Tests mock the gh call entirely so this is safe for unit tests.
- File tree fetch failure is non-fatal (logged as Warn, continues with placeholder) — spec generation should not fail due to rate-limiting or private repos.

## Decision Log
- Reuse `GHRunner` interface name from `watcher` but define it locally in `specgen` — avoids cross-package dependency between sibling internal packages. The watcher and specgen runners are conceptually the same abstraction but serve different call sites.
- `repoPath` defaults to `os.Getwd()` when empty — convenient for production (daemon already in repo root), easy to override in tests via `t.TempDir()`.
- Templates embedded as `const` strings rather than `//go:embed` to keep the package self-contained without a data file.

## Outcomes & Retrospective
All 5 milestones completed. `go test ./...` and `go vet ./...` both clean. `just build-pipeline` exits 0. The specgen package follows classifier patterns faithfully: same LLMCaller interface, same env-based backend detection, same GHRunner abstraction for gh CLI calls.
