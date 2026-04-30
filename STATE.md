# Firn — Project State

## Current Phase
Wave 1 — Complete ✅
Wave 2 — Pending (LLM backends, integration testing, first real run)

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
    status: done
    depends_on: [mentat-go-module]
    parallel: false

  - id: mentat-llm-domain-classifier
    description: LLM call to classify scanner candidates into domains (single cheap call, not generation)
    status: done
    depends_on: [mentat-scanner]
    parallel: false

  - id: mentat-skill-generator
    description: Per-domain LLM generation of SKILL.md content, write to .agents/skills/{domain}/SKILL.md
    status: done
    depends_on: [mentat-llm-domain-classifier]
    parallel: false

  - id: mentat-change-detection
    description: Git diff / mtime tracking — only regenerate domains with changed files
    status: done
    depends_on: [mentat-skill-generator]
    parallel: false

  - id: spec-templates
    description: Write product.md and tech.md templates with format docs and examples
    status: done
    depends_on: []
    parallel: true

  - id: pipeline-go-module
    description: Initialize Go module for pipeline, basic CLI scaffold
    status: done
    depends_on: []
    parallel: true

  - id: pipeline-issue-watcher
    description: GitHub issue poller — labeled issues trigger spec generation
    status: done
    depends_on: [pipeline-go-module, spec-templates]
    parallel: false

  - id: pipeline-spec-generator
    description: issue-refiner equivalent — generates product.md+tech.md spec PR from labeled issue
    status: done
    depends_on: [pipeline-issue-watcher]
    parallel: false

  - id: pipeline-issue-worker
    description: Reads merged spec, implements PR, draft-first, invariant verification
    status: done
    depends_on: [pipeline-spec-generator]
    parallel: false

  - id: pipeline-config
    description: Config file support — pr_throttle (default 3), ci_fixer_max_attempts (default 3), draft_first (default true)
    status: done
    depends_on: [pipeline-go-module]
    parallel: true

  - id: goreleaser-setup
    description: GoReleaser Pro config + GitHub Actions release workflow + Justfile snapshot/release targets for both mentat and pipeline binaries
    status: done
    depends_on: [mentat-go-module, pipeline-go-module]
    parallel: true

  - id: ci-doc-validation
    description: GitHub Actions workflow validating ceremony — exec plan required for 3+ file PRs, ADR required for architectural decisions, STATE.md task status must be consistent with merged work
    status: done
    depends_on: [goreleaser-setup]
    parallel: true

  - id: llm-backend-codex
    description: Add OpenAI Codex CLI backend to mentat/classifier and pipeline/specgen; detect via which codex or CODEX_MODEL env var
    status: done
    depends_on: [mentat-llm-domain-classifier, pipeline-spec-generator]
    parallel: true

  - id: llm-backend-copilot
    description: Add GitHub Copilot CLI (pi) backend to mentat/classifier and pipeline/specgen; detect via which pi or GH_COPILOT_TOKEN env var
    status: done
    depends_on: [mentat-llm-domain-classifier, pipeline-spec-generator]
    parallel: true

  - id: mentat-skill-distribution
    description: After generating master SKILL.md files, distribute them to agent-specific paths (.agents/skills/, .claude/commands/, .codex/, .cursor/rules/, AGENTS.md). Config-driven target list.
    status: done
    depends_on: [mentat-skill-generator]
    parallel: true

  - id: mentat-skill-validation
    description: Verify skills are actually loaded by real agents. Embed a canary identifier in generated SKILL.md, invoke claude/codex/copilot CLI with a domain question, assert canary appears in response.
    status: pending
    depends_on: [mentat-skill-distribution]
    parallel: false

  - id: mentat-integration-test-green
    description: Run mentat sync against frostyard/mentat-fixture-green, compare output against DOMAINS.md ground truth, score result
    status: pending
    depends_on: [mentat-skill-generator]
    parallel: true

  - id: mentat-integration-test-brown
    description: Run mentat sync against frostyard/mentat-fixture-brown, compare output against DOMAINS.md ground truth (structural containers, sparse dirs, multi-language)
    status: pending
    depends_on: [mentat-skill-generator]
    parallel: true

  - id: docs-agents-md
    description: Write AGENTS.md and CLAUDE.md with harness rules for this repo
    status: done
    depends_on: []
    parallel: true
  - id: mentat-agents-md-richer
    description: Upgrade AGENTS.md generation — repo-level overview section + domain index (one-liner + link per domain) + critical patterns. Hard cap 500 lines. Compress domain sections to index entries if cap would be exceeded.
    status: pending
    depends_on: [mentat-skill-distribution]
    parallel: false

  - id: mentat-distribute-agents-md
    description: Distribute AGENTS.md to CLAUDE.md, .github/copilot-instructions.md, .codex/AGENTS.md in addition to existing targets
    status: pending
    depends_on: [mentat-agents-md-richer]
    parallel: false

  - id: pipeline-ci-fixer
    description: After issue-worker opens a PR, push CI fixes up to ci_fixer_max_attempts, then add needs-human label and stop
    status: pending
    depends_on: [pipeline-issue-worker]
    parallel: false

  - id: pipeline-review-addresser
    description: Watch for PR review comments on agent-opened PRs, push fixes addressing reviewer feedback
    status: pending
    depends_on: [pipeline-ci-fixer]
    parallel: false

  - id: pipeline-auto-merger
    description: Auto-merge approved PRs (agent-pr label, all checks green, at least one approval)
    status: pending
    depends_on: [pipeline-issue-worker]
    parallel: true

  - id: pipeline-task-state-folders
    description: Research botfiles task-status-tracker-contract.md pattern for durable multi-run task state. Apply to pipeline issue-worker resume logic — when an implementation job fails partway, where does state live?
    status: pending
    depends_on: [pipeline-issue-worker]
    parallel: false
  - id: mentat-writing-skills-prompt
    description: Replace hand-rolled generator prompt with writing-skills/SKILL.md as context. Bundle obra/superpowers writing-skills skill as a default; override with repo-local version if present. LLM follows skill methodology rather than bespoke section list.
    status: pending
    depends_on: [mentat-skill-generator]
    parallel: true
```

## Completed

- `docs-agents-md` — AGENTS.md + CLAUDE.md written in initial scaffold
- `spec-templates` — specs/template/product.md + tech.md written in initial scaffold
- `mentat-go-module` — clix+cobra CLI scaffold (sync/status/init stubs), version ldflags, tests passing
- `pipeline-go-module` — clix-based cobra CLI scaffold with run/status/trigger stubs, internal/version package, tests, ldflags build target
- `mentat-change-detection` — tracker.go with sha256 content hashing, IsStale/RecordGeneration; --force flag; 9 tests
- `mentat-skill-generator` — generator.go with GenerateAll(), NewCaller export, full scan→classify→generate pipeline in syncCmd; 10 tests
- `mentat-llm-domain-classifier` — LLMCaller interface, claude/openai/ollama backends, env-based detection; 14 tests
- `mentat-scanner` — scanner.Scan() with SkipDirs/ContainerDirs/MinFiles/MaxDepth; 12 tests; wired into syncCmd
- `pipeline-issue-worker` — throttle check, spec→LLM→draft PR, pollSpecPRMerge, NewLLMCaller export; 5 tests
- `pipeline-spec-generator` — LLMCaller+GHRunner interfaces, product.md+tech.md generation, draft PR via gh; 7 tests
- `pipeline-issue-watcher` — GHRunner interface, Watch() with dedup/error-recovery, 7 tests
- `pipeline-config` — TOML config via viper; `pipeline/internal/config` package; `--config` flag wired into runCmd; 8 tests
- `ci-doc-validation` — GitHub Actions doc ceremony validation (exec plan gate, ADR warning, STATE.md YAML check)
- `goreleaser-setup` — `.goreleaser.yml` (GoReleaser Pro, two binaries), `.github/workflows/release.yml` + `snapshot.yml`, Justfile `snapshot`/`release` targets, `dist/` in `.gitignore`

## Decisions

| Decision | Choice |
|---|---|
| Monorepo structure | mentat/ + pipeline/ as separate Go modules |
| PR throttle default | 3 |
| Spec location | repo file (specs/GH{N}/) |
| Domain detection | directory heuristics + LLM classification pass |
| Agent state management | this file, git-committed, updated per wave |
