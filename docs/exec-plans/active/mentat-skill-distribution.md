# Mentat Skill Distribution

## Purpose
Extend mentat's sync pipeline to copy generated SKILL.md content to the
agent-specific directory layouts expected by pi/Miles, Claude Code, Codex,
Cursor, and a combined AGENTS.md — so a single `mentat sync` run keeps all
agent toolchains up to date simultaneously.

## Baseline
`just test-mentat` passes clean before any changes (confirmed 2026-04-29).

## Milestones
- [x] Milestone 1 — Create `mentat/internal/distributor/distributor.go` with
  `Target`, `Config`, `SkillContent`, `DefaultConfig`, `Distribute`, and
  `DistributeAll`; verify: `cd mentat && go build ./...`
- [x] Milestone 2 — Create `mentat/internal/distributor/distributor_test.go`
  with table-driven tests for all five targets; verify: `cd mentat && go test ./...`
- [x] Milestone 3 — Update `mentat/cmd/mentat/commands.go`: add `--no-distribute`
  flag and wire `distributor.DistributeAll` after `generator.GenerateAll`;
  verify: `just build-mentat && cd mentat && go test ./...`

## Surprises & Discoveries
(fill in as you work)

## Decision Log
- `Distribute` writes the pi target too (not just the "others") — keeps the
  distributor as the single place that knows about target paths; the generator
  output dir stays as the authoritative pi location.
- For dry-run: skip all distributor writes (mirrors generator behaviour); log
  what would be written instead.
- `DistributeAll` sorts skills by domain name before writing AGENTS.md so the
  output is deterministic across runs.
- YAML frontmatter extraction uses a simple string parser to avoid adding a
  new dependency (yaml.v3 is already present in go.mod).

## Outcomes & Retrospective
All three milestones completed in a single session. `just build-mentat` exits 0;
`go test ./...` and `go vet ./...` both pass clean. 23 new tests cover all five
target paths, frontmatter stripping/rewriting, dry-run, disabled targets, and
alpha-sorted AGENTS.md output.
