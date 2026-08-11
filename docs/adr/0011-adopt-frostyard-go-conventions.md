# 0011 — Adopt the frostyard Go repository conventions

- **Status:** Accepted
- **Date:** 2026-08-11

## Context

- Frostyard has a house pattern for Go repositories, captured in
  `core/.agents/skills/frostyard-go-repo/SKILL.md` with
  [frostyard/updex](https://github.com/frostyard/updex) as the
  canonical exemplar: clix/cobra CLI entry, Makefile `check` gate,
  golangci-lint v2, GoReleaser Pro with svu-tagged releases, a nightly
  `dev` snapshot, and shared org workflows.
- Firn grew up during phases 1–6 with its own minimal scaffolding
  (stdlib `flag` CLI, justfile, a single hand-written CI workflow).
  Keeping frostyard repos similar is worth more than firn's local
  variants.
- Not all of the pattern fits: the SDK-first layout ("a public
  `<name>/` package with a Client struct; the CLI is a thin shell")
  models service/tool SDKs. Firn's public contracts are its recipe
  schema and progress protocol (ADR-0005/0007), consumed over files
  and streams, not Go imports; nobody imports firn as a library.

## Decision

Firn adopts the frostyard Go conventions wherever they fit, copying
from updex (which wins over the skill text on disagreement):

- **CLI**: `github.com/frostyard/clix` wrapping a cobra tree.
  `cmd/firn-cli/main.go` is the ldflags-stamped entry;
  `cmd/firn/` holds the command handlers (`root`, `validate`,
  `install`); bare `firn` still launches the TUI (ADR-0007).
  clix/fang provide version, completions, and man pages.
- **Build gate**: updex's Makefile (`make check` = fmt + lint + test),
  golangci-lint v2 config, `.svu.yaml`, GoReleaser Pro config
  (binary `firn`, nfpm package `frostyard-firn`, LICENSE + NOTICE in
  artifacts), completions/manpages scripts, and the three org
  workflows (test / release / snapshot). The justfile and the
  hand-written CI workflow are retired.
- **Idioms**: conventional commits drive svu/changelogs; `make bump`
  releases; error strings lowercase without trailing punctuation.

Documented deviations (deliberate, not drift):

- **No public SDK package.** The pipeline stays under `internal/`;
  firn's outward contracts remain the recipe schema and progress
  protocol specs. Revisit only if a real Go consumer appears.
- **Root-requiring E2E harnesses stay** (`test/e2e-*.sh`, KVM+root,
  outside CI) alongside the house preference for read-only black-box
  tests — they are firn's core evidence (phases 3–6) and CI cannot
  host them.
- **Dependency posture is unchanged** (ADR-0005/0007): pipeline and
  domain packages remain stdlib + host tools; clix/cobra join the
  Charm stack as sanctioned CLI/TUI-layer dependencies.

## Consequences

- Firn builds, lints, releases, and reads like every other frostyard
  Go repo; org tooling (repogen packaging, nightly `dev` snapshots)
  applies without special cases.
- Exit codes collapse to 0/non-zero via clix (the old CLI
  distinguished usage errors as 2); nothing scripted against firn
  relied on the distinction.
- CI needs the org secrets (`GORELEASER_KEY`, `R2_*`) for release and
  snapshot workflows; the test workflow runs without them.
- The `check` gate gains a real linter; golangci findings in ported
  code are fixed or explicitly ignored at adoption time, not left as
  warnings.

## Alternatives considered

- **Full SDK-first restructure**: churn across ~15 internal packages
  to manufacture an API nobody imports; rejected until a consumer
  exists.
- **Keeping the justfile alongside the Makefile**: two build entry
  points drift; rejected — one gate, the org's.

## References

- Shapes: [design/architecture.md](../design/architecture.md),
  the repo's `Makefile`/`.goreleaser.yaml`/workflows
- Builds on: [ADR-0003](0003-rewrite-fisherman-as-firn.md),
  [ADR-0005](0005-toml-recipe-model.md),
  [ADR-0007](0007-tui-only-frontend-single-binary.md)
