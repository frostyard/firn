# Mentat: Improve Skill Generation Prompt

## Purpose
Rewrite `buildPrompt()` in `mentat/internal/generator/generator.go` to follow Anthropic's
skill authoring best practices — concise, example-driven, and minimal in structure. Also
simplify `normaliseContent()` by removing the fragile `---\n` heuristic. Add targeted unit
tests for the revised prompt and normaliser.

## Baseline
Run `just test` to confirm baseline passes before any changes. ✅ (all packages pass)

## Milestones
- [x] Milestone 1 — Write exec plan at `docs/exec-plans/active/mentat-writing-skills-prompt.md`; verify: manual
- [x] Milestone 2 — Rewrite `buildPrompt()` in `mentat/internal/generator/generator.go`; verify: `cd mentat && go vet ./...`
- [x] Milestone 3 — Simplify `normaliseContent()` in the same file; verify: `cd mentat && go vet ./...`
- [x] Milestone 4 — Add/update unit tests for `buildPrompt` and `normaliseContent` in `mentat/internal/generator/generator_internal_test.go`; verify: `just test-mentat`
- [x] Milestone 5 — Final verify: `just build-mentat` and `just test`

## Surprises & Discoveries
- Go does not allow backtick characters inside raw string literals. The `promptExample`
  constant had to be split into a package-level `const` with string concatenation for
  inline code segments rather than being inlined directly in `buildPrompt`.
- The existing test file uses `package generator_test` (black-box), so internal function
  tests needed a new `generator_internal_test.go` file with `package generator`.

## Decision Log
- Keeping the concrete example inside a raw string literal to avoid indentation drift.
- Not enumerating sections by name — trusting the model to pick the right structure.
- Removing the `\n---\n` heuristic from `normaliseContent` because it could silently
  truncate valid content; the fallback frontmatter injection already handles missing frontmatter.

## Outcomes & Retrospective
`buildPrompt` shrank from ~40 lines of prescriptive instructions to ~30 lines of
concise, example-driven text. `normaliseContent` dropped the fragile `\n---\n`
heuristic entirely (regression-tested). 14 new tests cover all prompt properties
and normaliser edge cases.
