<!-- The org squash-merges: branch off main, never stack on another PR's
branch. Title and commits use Conventional Commits (`type(scope): summary`)
— `make bump` derives the next tag from the squashed `main` commit.

Keep every `##` heading below, even when a section is short: automated
completion checks read these headings and reject a body that drops one.
Delete the guidance comments, not the headings. -->

## Summary

<!-- What changes and why, in a few sentences. Link the issue(s) this closes.
Name the failure the change removes, not just the code it touches. -->

## Changes

<!-- One bullet per file or package, in the order a reviewer should read
them. Say explicitly what you deliberately left alone. -->

-

## Docs housekeeping

<!-- Delete rows that don't apply; keep the heading. See "Documentation
rules (enforced)" in AGENTS.md. -->

- [ ] Recipe-schema changes update `docs/specs/recipe-schema.md` in the same
      commit; progress-event changes update `docs/specs/progress-protocol.md`
- [ ] `docs/design/*` and `docs/plans/roadmap.md` updated for behavior changes;
      `AGENTS.md` for convention/workflow changes
- [ ] New docs started from their category's `TEMPLATE.md` and indexed in
      [`docs/README.md`](../docs/README.md), cross-linked both ways
      (ADR ↔ design ↔ spec ↔ plan)
- [ ] New significant decision recorded as an ADR *first*, in this PR
- [ ] Conformance aliases (ADR-0002: `CLAUDE.md`, `GEMINI.md`,
      `.github/copilot-instructions.md`, `.claude/skills`) untouched —
      canonical `AGENTS.md` / `.agents/skills/` edited instead
- [ ] Code derived from fisherman or snosi-install keeps its provenance
      comment (`NOTICE`, ADR-0003)

## Verification

<!-- Paste the evidence, not just the claim: the gate's tail, the focused
test names, and — for a fix — the failure the test shows before the fix.
`mise install` first if golangci-lint is missing. Keep at least one ticked
box or plain line outside the code fence — a section holding only a fenced
log reads as empty to the completion check. -->

- [ ] `make check` (fmt, lint, test) green
- [ ] `make verify` green — the credential-free, non-mutating gate: tidy
      diff, `gofmt -l`, pinned golangci-lint, `go vet`, `go test`
- [ ] New or changed behavior has focused tests, including failure paths;
      pipeline steps are covered through the `runner` fake, not real devices
- [ ] Wizard-page or nested-VM changes: `make e2e-tui` / `make e2e-ab`
      considered, and the result stated below

```
<!-- gate output -->
```

## Risk tier

<!-- Tick exactly ONE box and leave its label text exactly as written —
put the justification under Rationale, not on the checkbox line. Tiers come
from `policies/agent-governance.json`: classify highest-applicable, and when
uncertain take the higher plausible tier. Touching a protected boundary
(`.github/workflows/**`, `.goreleaser.yaml`, `internal/install/**`,
`cmd/**`) is never below high. -->

- [ ] low
- [ ] moderate
- [ ] high
- [ ] critical

**Rationale:**

-
