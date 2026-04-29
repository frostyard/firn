# Mentat CLI Scaffold

## Purpose
Establish the clix-based cobra CLI foundation for mentat with sync/status/init
command stubs, ldflags-injectable version info, and tests verifying all
command/flag registrations. No real logic implemented — establishes the CLI
contract that scanner, classifier, and generator will implement.

Note: this exec plan was written retroactively after merge (PR #1). The rule
requiring exec plans for 3+ file changes was not enforced at review time.
See PR template update in the same commit.

## Baseline
Run `just test` to confirm baseline passes before any changes.

## Milestones
- [x] Milestone 1 — Create `mentat/internal/version/version.go` with ldflags-injectable vars; verify: `cd mentat && go build ./...`
- [x] Milestone 2 — Rewrite `mentat/cmd/mentat/main.go` with `clix.App`, `sync`/`status`/`init` subcommands; verify: `just build-mentat`
- [x] Milestone 3 — Extract subcommands to `mentat/cmd/mentat/commands.go`; verify: `just build-mentat`
- [x] Milestone 4 — Write `mentat/cmd/mentat/main_test.go` — 7 tests covering Use, subcommand registration, flags; verify: `just test-mentat`
- [x] Milestone 5 — Update `Justfile` `build-mentat` target with ldflags; verify: `just build-mentat`
- [x] Milestone 6 — Update `STATE.md` to mark `mentat-go-module` done; verify: file review

## Surprises & Discoveries
- `clix.NewReporter()` exposes `.Message()` not `.Info()` — the task spec used `.Info()` which does not exist on the reporter interface
- Tests must use a `buildRoot()` helper that registers clix flags manually to avoid `fang.Execute` side effects in test runs

## Decision Log
- Subcommands split into `commands.go` alongside `main.go` for clarity; `main.go` stays as a clean entry point only
- Tests verify flag presence via `cmd.PersistentFlags().Lookup()` rather than execution to avoid fang's terminal detection

## Outcomes & Retrospective
All milestones completed. Build, vet, and 7 tests pass cleanly. The mentat binary
is produced at `mentat/bin/mentat` with version info injected at build time.

**Process gap identified:** exec plan was not written before implementation.
Added exec plan checklist item to PR template to catch this at review time.
