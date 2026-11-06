# Org-wide decisions (frostyard/core ADRs)

Some conventions are decided at org level. They are ADRs in
[frostyard/core](https://github.com/frostyard/core/tree/main/docs/adr).

These ADRs bind firn. The list below says what each one binds here, and where
it lives in this repository.

## Baseline decisions

- [ADR-0018 — Org-wide agent instruction and knowledge surfaces](https://github.com/frostyard/core/blob/main/docs/adr/0018-org-wide-agent-instruction-and-knowledge-surfaces.md) — `AGENTS.md` is the canonical instruction file; `CLAUDE.md`, `GEMINI.md`, `.cursorrules` and `.github/copilot-instructions.md` are symlinks to it. Durable corrections go in the append-only inbox ([`.memory/README.md`](../.memory/README.md)).
- [ADR-0019 — Repository governance as machine-readable policy with risk tiers](https://github.com/frostyard/core/blob/main/docs/adr/0019-governance-as-code-and-risk-tiers.md) — the risk tiers are machine-readable in [`policies/agent-governance.json`](../policies/agent-governance.json), and every pull request declares one in the risk tier section of [`.github/pull_request_template.md`](../.github/pull_request_template.md).
- [ADR-0021 — SHA-pinned actions and least-privilege CI workflows](https://github.com/frostyard/core/blob/main/docs/adr/0021-sha-pinned-actions-and-least-privilege-ci.md) — the workflows in [`.github/workflows/`](../.github/workflows/) pin every action to a full SHA with the version in a comment, set `permissions: {}`, and set `persist-credentials: false`.
- [ADR-0025 — One docs/ tree per repository, in core's four-category shape](https://github.com/frostyard/core/blob/main/docs/adr/0025-consolidate-repository-docs-into-docs.md) — all documentation lives in this [`docs/`](README.md) tree, in 4 categories, each with a `TEMPLATE.md`.
- [ADR-0029 — ACMM conformance via canonical aliases](https://github.com/frostyard/core/blob/main/docs/adr/0029-acmm-conformance-via-canonical-aliases.md) — the tool paths are alias symlinks, and `.claude/skills` is a symlink to `.agents/skills/`.
- [ADR-0040 — Publish the repository settings contract](https://github.com/frostyard/core/blob/main/docs/adr/0040-publish-the-repository-settings-contract.md) — [`SECURITY.md`](../SECURITY.md) states the support policy, and private vulnerability reporting is on.
- [ADR-0043 — Pin repository tools in mise and name the verify gate](https://github.com/frostyard/core/blob/main/docs/adr/0043-pin-repository-tools-in-mise-and-name-the-verify-gate.md) — [`mise.toml`](../mise.toml) and [`mise.lock`](../mise.lock) pin every tool the gate runs, and CI installs that same set.
- [ADR-0044 — Expose the make gate triad in every repository](https://github.com/frostyard/core/blob/main/docs/adr/0044-expose-the-make-gate-triad-in-every-repository.md) — the [`Makefile`](../Makefile) has the `verify`, `check` and `ci` targets.
- [ADR-0051 — ADRs state facts only, not the process that produced them](https://github.com/frostyard/core/blob/main/docs/adr/0051-adrs-state-facts-not-process.md) — the ADRs in [`adr/`](adr/) state facts only.
- [ADR-0068 — Label pull requests that touch high-risk paths](https://github.com/frostyard/core/blob/main/docs/adr/0068-label-pull-requests-that-touch-high-risk-paths.md) — [`risk-paths.json`](../.github/risk-paths.json) lists the high-risk paths, and [`risk-paths.yml`](../.github/workflows/risk-paths.yml) adds the `risk:high` label.
- [ADR-0071 — Hold every code repository to one baseline](https://github.com/frostyard/core/blob/main/docs/adr/0071-hold-every-code-repository-to-one-baseline.md) — every code repository carries the baseline checklist ([spec](https://github.com/frostyard/core/blob/main/docs/specs/repository-baseline.md)). It has a [`LICENSE`](../LICENSE), a [Dependabot config](../.github/dependabot.yml), and a [`ci.yml`](../.github/workflows/ci.yml) that runs `make ci`. It has no `CODEOWNERS`.
- [ADR-0072 — Write every README to one standard](https://github.com/frostyard/core/blob/main/docs/adr/0072-write-every-readme-to-one-standard.md) — the [README](../README.md) declares its kind on line 1 and follows core's `tool` template.
- [ADR-0073 — Let CI run the `make ci` targets as separate jobs](https://github.com/frostyard/core/blob/main/docs/adr/0073-let-ci-run-the-make-ci-targets-as-separate-jobs.md) — [`ci.yml`](../.github/workflows/ci.yml) may spread the `make ci` targets over more than one job, so a failure names the target that failed.

## Release and publication

- [ADR-0012 — svu-derived versions, make bump, and the rolling dev prerelease](https://github.com/frostyard/core/blob/main/docs/adr/0012-svu-versioning-and-rolling-dev-prerelease.md) and [ADR-0053 — Name the svu config file `.svu.yml`](https://github.com/frostyard/core/blob/main/docs/adr/0053-name-the-svu-config-svu-yml.md) — [`.svu.yml`](../.svu.yml) sets the config, and the [`Makefile`](../Makefile) `bump` target runs `svu next` and pushes the tag. A nightly workflow publishes the rolling `dev` prerelease.
- [ADR-0055 — Publish Debian packages through the apt-publisher single writer](https://github.com/frostyard/core/blob/main/docs/adr/0055-publish-debian-packages-through-the-apt-publisher.md) and [ADR-0056 — The APT publisher triggers image rebuilds after packages are live](https://github.com/frostyard/core/blob/main/docs/adr/0056-rebuild-images-after-apt-publication.md) — the tag workflow sends one `publish-deb` request to frostyard/apt-publisher, which publishes the packages and dispatches snosi's rebuild. [ADR-0017](adr/0017-publish-debian-packages-through-apt-publisher.md) records the local decision.

## When they apply

- [ADR-0070 — Build tool docs sites with ProperDocs and MaterialX on GitHub Pages](https://github.com/frostyard/core/blob/main/docs/adr/0070-build-tool-docs-sites-with-materialx-on-github-pages.md) — firn is a user-facing tool, so it owes a `site/` docs site. It has none yet: the follow-up is [frostyard/firn#127](https://github.com/frostyard/firn/issues/127).

A Go repository uses the Go minor and the golangci-lint version in core's
`frostyard-go-repo` skill, section
[Fleet toolchain](https://github.com/frostyard/core/blob/main/.agents/skills/frostyard-go-repo/SKILL.md#fleet-toolchain).
firn's root module is not on that Go minor yet: the follow-up is
[frostyard/firn#128](https://github.com/frostyard/firn/issues/128).

When you change behavior that one of these ADRs covers, update or supersede the
ADR in frostyard/core first.
