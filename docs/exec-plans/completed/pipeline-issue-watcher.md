# pipeline-issue-watcher

## Purpose
Implement `pipeline/internal/watcher` — a GitHub issue poller that emits `Issue` values on
a channel whenever a repo has issues labeled `needs-spec`. Wire it into the existing `runCmd`
so the pipeline daemon actually polls GitHub, respects `--dry-run`, and logs discovered issues.

## Baseline
Run `just test` to confirm baseline passes before any changes.
Confirmed: all tests pass on `main` at HEAD `1d0639b`.

## Milestones

- [x] Milestone 1 — Write `docs/exec-plans/active/pipeline-issue-watcher.md` (this file);
  verify: file exists on disk
- [x] Milestone 2 — Create `pipeline/internal/watcher/watcher.go`: `GHRunner` interface,
  `ExecRunner`, `Config`, `Issue`, `Watch`, `fetchIssues`;
  verify: `cd pipeline && go build ./internal/watcher/`
- [x] Milestone 3 — Create `pipeline/internal/watcher/watcher_test.go`: mock runner, parse
  JSON test, context cancellation test, dedup test, empty-repo error test;
  verify: `cd pipeline && go test ./internal/watcher/`
- [x] Milestone 4 — Update `pipeline/cmd/pipeline/main.go` `runCmd` to call `watcher.Watch`,
  receive issues in a loop, log them, and handle `--dry-run`;
  verify: `cd pipeline && go build ./... && go test ./...`
- [x] Milestone 5 — Update `STATE.md`: mark `pipeline-issue-watcher` done, add to Completed;
  verify: diff shows both task graph and Completed updated
- [x] Milestone 6 — Full suite + lint clean;
  verify: `just test && just lint`

## Surprises & Discoveries
- `pipeline-config` task was already done on `main` (commit `641ac2c`) so `config.go` existed
  before this branch; no need to create it.
- 7 tests written vs. 4 described in spec — added `TestWatchPollErrorDoesNotStop`,
  `TestWatchDefaultLabel`, and `TestWatchInvalidJSON` for better coverage of error paths.
- git stash/worktree complications required extra care; files recreated from session content.

## Decision Log

- Added `Runner GHRunner` and `Log *slog.Logger` fields to `Config` so tests can inject mocks
  without a separate constructor; this follows the AGENTS.md "pass dependencies explicitly"
  principle while keeping `Watch` signature close to spec.
- `Watch` sends each issue at most once per invocation using an in-memory `seen` map;
  this prevents channel flooding on every poll tick.
- First poll fires immediately before the ticker starts so the daemon reacts without waiting
  one full `Interval`.
- Dry-run is handled in `runCmd` (CLI layer), not in the watcher — the watcher is agnostic
  to clix flags.

## Outcomes & Retrospective
`pipeline/internal/watcher` fully implemented: 152-line production file + 7-test file.
`runCmd` upgraded from stub to live daemon loop. All milestones complete, build and lint clean.
