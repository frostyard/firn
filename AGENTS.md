# Firn

Firn is the single installer for all snosi image families — bootc OCI
images and native A/B disk images — recipe-driven, with a built-in TUI.
It is a deeply-inspired GPL-3.0-only rewrite of fisherman (attribution in
`NOTICE`, rationale in ADR-0003). Start at
[docs/README.md](docs/README.md).

This file (`AGENTS.md`) is the CANONICAL agent instructions — `CLAUDE.md`,
`GEMINI.md`, and `.github/copilot-instructions.md` are symlinks to it, and
`.claude/skills` symlinks to `.agents/skills/`
([ADR-0002](docs/adr/0002-agent-portable-instruction-surface.md)). Edit only
the canonical paths; keep content tool-agnostic.

## Skills (follow these for common tasks)

Step-by-step procedures live in [.agents/skills/](.agents/skills/); follow
them rather than improvising, whichever agent you are:

<!-- One bullet per skill: **When to use it** → [.agents/skills/<name>/SKILL.md].
Add a skill whenever you find yourself re-explaining a multi-step procedure.
Start from .agents/skills/TEMPLATE/SKILL.md. -->

- **Changing a wizard page or debugging `just e2e-tui`** →
  [.agents/skills/drive-tui-e2e/SKILL.md](.agents/skills/drive-tui-e2e/SKILL.md)
  — the tmux expect-driver is a contract with `internal/tui/wizard_pages.go`.
- **Extending or debugging any nested-VM E2E** (`e2e-ab`, `e2e-tui`) →
  [.agents/skills/nested-vm-e2e/SKILL.md](.agents/skills/nested-vm-e2e/SKILL.md)
  — includes the ADR-0009 isolation rules and artifact locations.
- **Implementing anything fisherman or snosi-install already does** →
  [.agents/skills/port-from-parents/SKILL.md](.agents/skills/port-from-parents/SKILL.md)
  — provenance, incident comments, runner seam, fake-runner tests.

## Code conventions (live — the code exists)

<!-- The most important section. Rules here must describe the code AS IT IS,
not aspirations — an agent that follows a stale rule produces broken work.
Graduate a rule into this list only when the code enforcing or exemplifying
it has landed; until then it lives in a design doc as intent.

Write rules imperatively and concretely, each with enough mechanism to be
followed without asking ("Storage only via db.Open(slug, migrations)" — not
"use the database layer"). Point at one canonical example in the code for
every structural rule. Rules that remove a degree of freedom are the
valuable ones: every choice an agent doesn't have to make is a failure mode
removed. -->

- Run `make check` (fmt + lint + test) before calling any change done —
  the frostyard house gate (ADR-0011; exemplar: frostyard/updex). CI
  runs the same steps. `make lint` fails on linter findings when
  golangci-lint is installed and skips with a message when it is missing; CI
  is the backstop for environments without the tool.
- Layout follows the frostyard Go conventions with documented
  deviations (ADR-0011): `cmd/firn-cli/main.go` is the clix entry,
  `cmd/firn/` holds cobra handlers, and — deviating from SDK-first —
  the pipeline stays under `internal/` (firn's public contracts are
  the recipe schema and progress protocol, not Go APIs).
- Releases: conventional commits; `make bump` tags via svu; GoReleaser
  Pro publishes `frostyard-firn` packages plus a nightly `dev`
  snapshot.
- Pipeline and domain packages (`internal/*` except the TUI) use only the
  Go stdlib plus shelling out to host tools; the sanctioned exceptions are
  the TOML decoder (ADR-0005) and, in the TUI layer only, the Charm stack
  (ADR-0007). Do not add other dependencies without an ADR.
- Recipe validation is fail-closed: unknown fields, unknown enum values,
  and wrong-family fields are errors (`internal/recipe/validate.go` is the
  canonical example). New recipe fields change
  `docs/specs/recipe-schema.md` in the same commit.
- Progress events are the only user-visible output of pipeline code; new
  event types and stable codes change `docs/specs/progress-protocol.md`
  in the same commit (`internal/progress`).
- Files copied or substantially derived from fisherman keep a provenance
  comment at the top identifying their origin (see `NOTICE`).

## Repository boundary

<!-- What does NOT belong in this repo (secrets, personal data, generated
files that are actually build outputs, apps that belong elsewhere)? How are
releases cut? Delete the section if genuinely not applicable. -->

## Documentation rules (enforced)

Docs live in `docs/` in four categories. **Every new doc starts from its
category's `TEMPLATE.md`** and follows its structure:

- `docs/adr/` — why we decided. Immutable once Accepted; reversals are new
  ADRs that mark the old one Superseded.
- `docs/design/` — how it fits together. Living; updated in place to match
  reality.
- `docs/specs/` — exact contracts. Change only alongside implementing code.
- `docs/plans/` — order of work. Phases with "Done when" outcomes.

### Cross-linking is mandatory

A doc without its required links is incomplete — do not finish a docs change
until they exist, in both directions:

- **ADR** → links every design doc/spec it shapes, and prior ADRs it builds on.
- **Design doc** → links the ADR(s) providing its rationale, the spec(s)
  pinning its contracts, and the roadmap phase that builds it.
- **Spec** → links its motivating ADR(s) and the design doc showing where it
  fits.
- **Plan** → every phase links the design docs/specs it implements; resolved
  open questions become ADRs.

When you touch a doc, verify its links still hold (targets exist, section
anchors valid) and add the back-links on the targets. Use relative paths.

### Housekeeping

- New doc ⇒ add a line to the index in [docs/README.md](docs/README.md).
- New significant decision ⇒ new ADR *first*, then update the affected design
  docs/specs in the same change.
- Convert relative dates ("next weekend") to absolute dates in all docs.
