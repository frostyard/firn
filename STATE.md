# Firn — Project State

## Current Phase
Wave 1 — Foundation

## Task Graph

```yaml
tasks:
  - id: mentat-go-module
    description: Initialize Go module for mentat, basic CLI scaffold (cobra), build+test passing
    status: done
    depends_on: []
    parallel: true

  - id: mentat-scanner
    description: Repo scanner — directory walk, SKIP_DIR_NAMES, file count threshold, candidate list output
    status: pending
    depends_on: [mentat-go-module]
    parallel: false

  - id: mentat-llm-domain-classifier
    description: LLM call to classify scanner candidates into domains (single cheap call, not generation)
    status: pending
    depends_on: [mentat-scanner]
    parallel: false

  - id: mentat-skill-generator
    description: Per-domain LLM generation of SKILL.md content, write to .agents/skills/{domain}/SKILL.md
    status: pending
    depends_on: [mentat-llm-domain-classifier]
    parallel: false

  - id: mentat-change-detection
    description: Git diff / mtime tracking — only regenerate domains with changed files
    status: pending
    depends_on: [mentat-skill-generator]
    parallel: false

  - id: spec-templates
    description: Write product.md and tech.md templates with format docs and examples
    status: done
    depends_on: []
    parallel: true

  - id: pipeline-go-module
    description: Initialize Go module for pipeline, basic CLI scaffold
    status: pending
    depends_on: []
    parallel: true

  - id: pipeline-issue-watcher
    description: GitHub issue poller — labeled issues trigger spec generation
    status: pending
    depends_on: [pipeline-go-module, spec-templates]
    parallel: false

  - id: pipeline-spec-generator
    description: issue-refiner equivalent — generates product.md+tech.md spec PR from labeled issue
    status: pending
    depends_on: [pipeline-issue-watcher]
    parallel: false

  - id: pipeline-issue-worker
    description: Reads merged spec, implements PR, draft-first, invariant verification
    status: pending
    depends_on: [pipeline-spec-generator]
    parallel: false

  - id: pipeline-config
    description: Config file support — pr_throttle (default 3), ci_fixer_max_attempts (default 3), draft_first (default true)
    status: pending
    depends_on: [pipeline-go-module]
    parallel: true

  - id: docs-agents-md
    description: Write AGENTS.md and CLAUDE.md with harness rules for this repo
    status: done
    depends_on: []
    parallel: true
```

## Completed

- `docs-agents-md` — AGENTS.md + CLAUDE.md written in initial scaffold
- `spec-templates` — specs/template/product.md + tech.md written in initial scaffold
- `pipeline-go-module` — clix-based cobra CLI scaffold with run/status/trigger stubs, internal/version package, tests, ldflags build target
- `mentat-go-module` — clix+cobra CLI scaffold (sync/status/init stubs), version ldflags, tests passing

## Decisions

| Decision | Choice |
|---|---|
| Monorepo structure | mentat/ + pipeline/ as separate Go modules |
| PR throttle default | 3 |
| Spec location | repo file (specs/GH{N}/) |
| Domain detection | directory heuristics + LLM classification pass |
| Agent state management | this file, git-committed, updated per wave |
