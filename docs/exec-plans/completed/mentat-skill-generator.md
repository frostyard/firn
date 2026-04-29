# mentat-skill-generator

## Purpose
Implement the generator package that takes a classified DomainResult, calls an LLM to produce SKILL.md content with YAML frontmatter and markdown body, and writes it to `.agents/skills/{domain}/SKILL.md` in the target repo. Wire it into the syncCmd pipeline so `mentat sync` completes the full scan → classify → generate flow.

## Baseline
Confirmed: `just test` passes on `main` before any changes.

## Milestones
- [x] M1 — Write `docs/exec-plans/active/mentat-skill-generator.md` (this file); verify: file exists
- [x] M2 — Implement `mentat/internal/generator/generator.go` with `Config`, `Result`, `Generate`, `GenerateAll`; verify: `cd mentat && go build ./...`
- [x] M3 — Write `mentat/internal/generator/generator_test.go` with all required test cases; verify: `cd mentat && go test ./internal/generator/...`
- [x] M4 — Wire generator into `mentat/cmd/mentat/commands.go` syncCmd; verify: `cd mentat && go build ./...`
- [x] M5 — Full suite passes clean; verify: `just test-mentat && just build-mentat`
- [x] M6 — Update `STATE.md` marking `mentat-skill-generator` done; verify: STATE.md edited

## Files to touch
1. `docs/exec-plans/active/mentat-skill-generator.md` — this file
2. `mentat/internal/generator/generator.go` — new package
3. `mentat/internal/generator/generator_test.go` — tests
4. `mentat/cmd/mentat/commands.go` — wire generate step
5. `STATE.md` — mark task done

## Surprises & Discoveries
- `newCaller` in classifier was unexported; added thin `NewCaller` export wrapper so generator can reuse backends without duplicating the switch.
- `gopkg.in/yaml.v3` was already a transitive dep; `go mod tidy` promoted it to direct when test file imported it — no manual go.mod comment needed.
- `clix.OutputJSON(domains)` early-exit in the original syncCmd would have prevented generator from running in --json mode; restructured to single OutputJSON call at end.

## Decision Log
- Reused `classifier.LLMCaller` interface and added `classifier.NewCaller` export. Generator imports classifier for interface, Config, and DomainResult — avoids code duplication.
- `GenerateAll` checks `clix.DryRun` and short-circuits before building the LLM caller; this means a missing backend does not fail a dry-run, which is correct behaviour.
- File sampling reads first 5 lines of up to 10 source files from the domain directory (non-recursive). This keeps prompt size bounded while giving the LLM enough context.

## Outcomes & Retrospective
All 10 generator tests pass. Full mentat test suite (26 tests across 4 packages) passes clean. go vet clean. Build succeeds. syncCmd pipeline is now complete: scan → classify → generate → report.
