# ADR-005 — Domain detection: directory heuristics + LLM classification pass

**Status:** Accepted  
**Date:** 2026-04-29

## Context

mentat must identify "logical domains" in a repository — each domain gets one SKILL.md documentation file. Directory structure alone is unreliable: `src/utils/` is not a domain, `src/auth/` probably is. The prior Go port (mentat-go) had a critical bug: it used `parts[0]` as the domain key, collapsing `internal/auth` and `internal/billing` into `internal`.

Approaches considered:
- **Tree-sitter AST analysis** — can extract package names and import paths, but requires CGO bindings and adds no accuracy benefit over simpler approaches for this specific problem
- **Import graph clustering** — accurate for Go, not portable to shell/infra repos (like snosi)
- **Directory heuristics** — fast, language-agnostic, but misses semantic boundaries
- **LLM-only** — send directory tree to LLM and ask it to identify domains; trivially portable, but non-deterministic; good for classification not scanning
- **Hybrid: heuristics + LLM classification** — heuristics produce candidates cheaply; one LLM call classifies them semantically

## Decision

Two-phase approach:

1. **Heuristic scan** — walk the repo, skip known structural containers (`src/`, `internal/`, `cmd/`, `pkg/`, `lib/` — configurable via `SKIP_DIR_NAMES`); apply file count threshold; collect candidate directories
2. **LLM classification** — one cheap call: "here is the directory tree with file counts, which are logical domains?" — handles semantic judgment heuristics can't

The aspens JS scanner's `SKIP_DIR_NAMES` approach is the reference: look at children of structural containers, not the containers themselves.

## Consequences

- Works across Go, TypeScript, shell/infra repos (snosi) — LLM handles language-agnostic classification
- `SKIP_DIR_NAMES` is the primary tuning knob — must be configurable, not hardcoded
- One LLM call per `mentat sync` for classification before the more expensive per-domain generation calls
- Results are non-deterministic across LLM versions — domain list may shift slightly; change detection must account for domains appearing/disappearing
