# ADR-006 — Pipeline configuration: file-based with conservative defaults

**Status:** Accepted  
**Date:** 2026-04-29

## Context

The pipeline daemon needs configuration for behaviour that varies per deployment: PR throttle (how many concurrent open agent PRs per repo), CI fixer retry limit, draft-first policy. These must be tunable without recompiling. Prior experience with Yeti showed that hardcoded limits (or no limits) led to maintainer burden.

## Options considered

1. **Flags only** — simple but requires flags on every invocation; can't express per-repo overrides
2. **Environment variables** — workable but hard to audit; no schema
3. **Config file (TOML/YAML/JSON)** — explicit, version-controllable, supports per-repo overrides, discoverable schema

## Decision

Config file at `.firn/config.toml` in the target repo (or `~/.config/firn/config.toml` for global defaults). Conservative defaults:

```toml
[pipeline]
pr_throttle = 3           # max concurrent open agent PRs per repo
ci_fixer_max_attempts = 3 # stop after N failed CI fix attempts, add needs-human label  
draft_first = true        # all agent PRs open as drafts until invariants pass
```

`pr_throttle` is a knob, not a hardcoded value. Default of 3 is intentionally conservative.

## Consequences

- Operators can tune per-repo without code changes
- `pipeline-config` task implements config loading using `github.com/spf13/viper` (already a clix transitive dependency)
- Agents must check `clix.DryRun` before writing config changes
- The 3-PR default means wave 1 of a busy repo may feel slow — this is intentional; correctness over throughput until the pipeline is proven
