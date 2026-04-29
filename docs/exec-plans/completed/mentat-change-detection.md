# mentat-change-detection

## Purpose
Add a Tracker package to mentat that computes sha256 content hashes of domain source files, persists state to `.agents/mentat-state.json`, and gates `GenerateAll` on staleness so only changed domains are regenerated. Adds `--force` flag to `syncCmd` to bypass the check.

## Baseline
Confirmed `just test-mentat` passes before any changes.

## Milestones
- [x] Write exec plan at `docs/exec-plans/active/mentat-change-detection.md`
- [x] Milestone 1 — Create `mentat/internal/tracker/tracker.go` with `DomainState`, `Tracker`, `Load`, `Save`, `IsStale`, `RecordGeneration`; verify: `cd mentat && go build ./...`
- [x] Milestone 2 — Create `mentat/internal/tracker/tracker_test.go` with all required test cases; verify: `cd mentat && go test ./internal/tracker/...`
- [x] Milestone 3 — Wire tracker into `syncCmd` in `mentat/cmd/mentat/commands.go` (load, filter, record, save) and add `--force` flag; verify: `cd mentat && go build ./... && go test ./...`
- [x] Milestone 4 — Full verify: `just test-mentat && just build-mentat`

## Surprises & Discoveries
(fill in as you work)

## Decision Log
- Hash is sha256 of all source file contents under the domain path (non-recursive to match scanner behaviour). The hash is computed over sorted filenames so order is deterministic.
- `IsStale` returns true when SKILL.md is missing (no state entry), ensuring first-run always generates.
- State file path defaults to `{repoPath}/.agents/mentat-state.json`; Tracker.StateFile can override the full path.
- Tests use os.MkdirTemp for filesystem isolation — no mocks needed since Tracker only does file I/O.

## Outcomes & Retrospective
All milestones completed in a single pass. 9 tracker tests pass; full mentat test suite clean; `just build-mentat` exits 0. The `syncCmd` now filters to stale domains before calling `GenerateAll`, records each written domain into `.agents/mentat-state.json`, and exposes `--force` to bypass staleness. Generator `Overwrite` set to `true` for domains that reach it — the tracker is the gate.
