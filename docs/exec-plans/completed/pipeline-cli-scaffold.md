# Pipeline CLI Scaffold

## Purpose
Establishes the clix-based cobra CLI foundation for the pipeline engine with
`run`, `status`, and `trigger` subcommand stubs, an ldflags-injectable version
package, and tests verifying all command/flag registrations are correct.

## Baseline
Run `just test` to confirm baseline passes before any changes.

## Milestones
- [x] Milestone 1 — Create `pipeline/internal/version/version.go` with ldflags-injectable vars; verify: `cd pipeline && go build ./...`
- [x] Milestone 2 — Rewrite `pipeline/cmd/pipeline/main.go` with `clix.App`, `run`/`status`/`trigger` subcommands; verify: `just build-pipeline`
- [x] Milestone 3 — Write `pipeline/cmd/pipeline/main_test.go` covering Use, subcommand registration, flags; verify: `just test-pipeline`
- [x] Milestone 4 — Update `Justfile` `build-pipeline` target with ldflags; verify: `just build-pipeline`
- [x] Milestone 5 — Update `STATE.md` to mark `pipeline-go-module` done; verify: file review

## Surprises & Discoveries
- mentat module has pre-existing build failures (undefined syncCmd/statusCmd/initCmd) — unrelated to this task, not introduced here.
- Just backtick syntax for shell substitution in ldflags works cleanly with `just`.

## Decision Log
- Followed mentat pattern for clix.App usage; mentat cmd stub is also bare so we define the pattern here first.
- `run` subcommand uses `--interval` of type `time.Duration` (stored as string flag then parsed) to keep cobra flag simple.

## Outcomes & Retrospective
All 5 milestones completed. Build, vet, and tests pass cleanly. The pipeline binary
is produced at `pipeline/bin/pipeline` with version info injected. The scaffold
provides a solid foundation for the watcher/spec/worker subpackages.
