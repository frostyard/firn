# ADR-002 — CLI framework: clix

**Status:** Accepted  
**Date:** 2026-04-29

## Context

Both mentat and pipeline are CLI tools that need consistent flag conventions (`--dry-run`, `--json`, `--verbose`, `--silent`), structured output, progress reporting, and version info injection. These are cross-cutting concerns that would need to be reimplemented or awkwardly shared between tools without a shared library.

## Options considered

1. **Raw cobra** — maximum flexibility, but each tool re-implements the common flags and reporter pattern independently; inconsistent UX across tools
2. **clix (github.com/frostyard/clix)** — frostyard's own library wrapping cobra + charmbracelet/fang; provides standardized flags, `OutputJSON`, `NewReporter`, and version injection out of the box; already used by other frostyard tools
3. **urfave/cli** — popular alternative, but not used elsewhere in the frostyard ecosystem

## Decision

Use `github.com/frostyard/clix` for both tools. All four standard flags (`--dry-run`, `--json`, `--verbose`, `--silent`) are registered automatically via `clix.App.Run()`.

## Consequences

- Consistent flag UX across all frostyard CLI tools
- `clix.DryRun` must be checked before any write operation in both tools — agents must know to check this before file writes, API calls, or git operations
- `clix.OutputJSON(v)` is the standard for structured output; agents must not use `fmt.Println` for data output in library code
- Dependency on frostyard/clix — if that library changes its API, both tools require updates
