# 0005 — Versioned, sectioned TOML recipe as the single configuration surface

- **Status:** Accepted
- **Date:** 2026-08-11

## Context

- Fisherman's configuration is one flat JSON object of ~30 peer fields
  (`disk` beside `hostname` beside `cosignPubKey`), loaded and validated
  fail-closed; its TUI serializes wizard state into that file.
  `snosi-install` has no file at all: ~30 CLI flags, with the GUI
  assembling an argv and passing secrets via 0600 tmpfiles.
- [ADR-0004](0004-single-installer-scope-and-support-matrix.md) commits
  firn to one configuration surface across both image families, with
  security-relevant settings always explicit.
- Recipes are hand-authored (lab/automation users) as well as
  machine-generated (the TUI, provisioning scripts), so comments in the
  file format have real value.
- Fisherman recently gained btrfs subvolume support (`@`, `@home`,
  `@snapshots`); the recipe surface must not regress it.
- Frostyard tooling convention already favors TOML (e.g. `.mill.toml`).

## Decision

Firn's sole configuration input is a **TOML recipe** with a top-level
schema `version` and four sections:

- `[image]` — which image to install, with an **explicit family
  discriminator** (`family = "bootc" | "ab"`), reference/channel, and
  trust material. The family is never inferred from the reference shape.
- `[target]` — disk selection and layout: filesystem choice and btrfs
  subvolume support (`@`, `@home`, `@snapshots`) carry over from
  fisherman on the bootc path; the A/B path's layout is fixed by the
  image and accepts only what is genuinely variable (target disk).
- `[security]` — encryption mode, Secure Boot/MOK, TPM policy. Explicit
  per ADR-0004: omission is a validation error, not a default.
- `[system]` — hostname, locale, timezone, keyboard, flatpaks,
  `[system.user]` (name, password, groups, SSH authorized key), and the
  optional root SSH authorized key.

The TUI is a recipe *generator*: it serializes wizard state to this same
schema and invokes the same pipeline non-interactive mode uses. There is
no configuration reachable from the TUI that a recipe cannot express.

Secret-valued fields (passwords, passphrases) accept `*_file` variants
pointing at 0600 files, so automation and the TUI can keep secrets out of
the recipe body and off argv — carrying over `snosi-install`'s posture.

**Clean break from fisherman recipes.** Firn does not read or convert the
legacy flat JSON format; fisherman keeps serving its own recipes until
its retirement (a future decision per ADR-0003).

## Consequences

- One small pure-Go TOML dependency enters the backend, ending fisherman's
  zero-dependency rule for the config layer only; shelling out to host
  tools remains the rule elsewhere.
- The schema is a versioned public contract: it gets a spec under
  `specs/`, and changes to it happen only alongside implementing code.
- Everything is testable headless: any TUI flow is reproducible by
  feeding its generated recipe to non-interactive mode.
- Existing fisherman users must write a new recipe when they migrate;
  there is no automated path. Acceptable while fisherman remains alive.
- The `family` discriminator means validation can be per-family and
  fail-closed: bootc-only fields in an `ab` recipe (and vice versa) are
  errors, not ignored noise.

## Alternatives considered

- **JSON (fisherman lineage):** stdlib-only and trivial to generate, but
  no comments for hand-authored recipes; rejected.
- **Accept both TOML and JSON:** doubles the parse/validation test
  surface for marginal benefit; machine generators can emit TOML.
- **Fisherman-compatible flat schema:** eases migration but freezes a
  30-field flat namespace that was never designed for the A/B path;
  rejected with the clean-break decision.
- **Native legacy-recipe reading or a converter:** makes the old schema a
  permanent (or semi-permanent) contract of the new codebase; rejected —
  fisherman itself covers legacy users until retirement.

## References

- Shapes: [specs/recipe-schema.md](../specs/recipe-schema.md),
  [design/architecture.md](../design/architecture.md)
- Builds on: [ADR-0003](0003-rewrite-fisherman-as-firn.md),
  [ADR-0004](0004-single-installer-scope-and-support-matrix.md)
