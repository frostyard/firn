# Firn — Philosophy and Vision

> *Firn is the compacted granular snow at the head of a glacier — the intermediate stage between a loose snowflake and solid ice. It is the layer where raw material becomes something permanent.*

---

## The Problem

Agentic code generation works. The demos are real. But production use reveals a consistent failure mode: **the agent doesn't know enough about the codebase to produce reliable output, and nothing corrects that over time.**

Without accurate, fresh documentation, an agent working on a large or unfamiliar codebase makes educated guesses. Those guesses are often good. Sometimes they are catastrophically wrong. And because the agent has no memory of prior sessions, every new conversation starts from the same baseline of ignorance.

The second failure mode is workflow: even when an agent produces correct code, getting it from idea to merged PR reliably requires a level of discipline that most development workflows don't enforce. Specs are vague. Definitions of done are implicit. Review gates are optional. The result is a pile of marginally useful PRs that create more work than they save.

Firn is an attempt to solve both problems systematically.

---

## The Research Foundation

This system did not emerge from first principles. It is a synthesis of several years of prior art, personal experiments, and hard-won failures.

### What Yeti taught us

Yeti was a production GitHub automation daemon that polled repositories, used the Claude CLI to analyze issues and implement changes, and autonomously merged PRs. It ran for weeks across multiple frostyard projects and did real work.

Two things broke it:

1. **Maintainer burden.** Yeti was productive but created work faster than humans could review it. The automation queue became its own job. Too many PRs, too much noise, and no throttle.

2. **Issue → PR failure.** The pipeline from a labeled GitHub issue to a correct, focused PR never worked reliably. Root cause: `issue-refiner` produced freeform Markdown plans with no structured definition of done. `issue-worker` had no anchor. "CI passes" was necessary but not sufficient.

### What Rime tried to fix

Rime was a more principled orchestrator built on Yeti's lessons. It introduced structured exec plans with mandatory verification commands, a 9-dimension plan validation step before any code was touched, and an 8-pillar Agent Readiness Score for target repositories.

Rime got the execution pipeline right but never finished the trigger layer — it had no GitHub issue watcher and no CI-fix loop. It was a better engine without the infrastructure to drive it.

### What Warp showed us

When Warp open-sourced and published their agentic development workflow, the spec format was the key insight. Their `product.md` + `tech.md` approach — numbered testable behavioral invariants paired with line-referenced implementation steps — gives an agent a mechanical definition of done before it touches code.

The workflow: issue → spec PR (reviewed by humans) → implementation PR (executed by agent, verified against invariants) → merge. The spec-as-PR insight is particularly important for open source: a spec PR forces community review of intent before anyone codes. The spec becomes permanent documentation, not a throwaway planning artifact.

### What the broader research confirmed

GSD (Get Shit Done), CCPM, Symphony, and related frameworks all converge on the same architecture:

- **Context is scarce.** Every agent starts with a fresh window. Loading everything "just in case" degrades every subsequent reasoning step. Scope tool access tightly. Write documents that are complete in themselves.
- **Harness first.** The environment enables the agents. A scaffolded repo with enforced conventions, working CI, and clear documentation produces better agent output than a bare repo with sophisticated logic.
- **Mechanical gates over human memory.** If a rule can be a lint error, it is a lint error. Human reviewers forget; CI does not.
- **Issues are execution artifacts, not planning artifacts.** The hard thinking — scope, constraints, success criteria, task decomposition — should happen before any GitHub issue exists.

---

## The Architecture

Firn implements a layered approach. Each layer is a prerequisite for the one above it.

### Prerequisites: State and Ceremony

Before any implementation work starts, two things must be true:

**State management**: a `STATE.md` file committed to the repository owns the project task graph. Every task has a status, explicit dependencies, and a parallel flag. At session start, the orchestrating agent reads STATE.md, dispatches all unblocked tasks in parallel, and updates state as work completes. No agent or human needs to remember where things stand between sessions. Git history is the audit trail.

**Ceremony enforcement**: architectural decisions require ADRs. Implementation tasks touching 3+ files require exec plans written before code. PRs cannot merge without CI validation of these artifacts. These are not suggestions — they are mechanical gates.

### Layer 0: Documentation (mentat)

`mentat` is a Go tool that scans a repository, identifies logical domains, and generates one SKILL.md documentation file per domain using an LLM. It runs on commit or schedule and regenerates only the domains whose files changed.

The output of mentat is what makes the layers above it work. An agent given a SKILL.md for `auth/` before touching authentication code makes substantially fewer wrong decisions than one reading raw source cold.

Fresh documentation is not a one-time setup task. It is an ongoing practice. Mentat enforces that practice mechanically.

### Layer 1: Pre-GitHub Planning

Issues are execution artifacts. They should be created only after the scope, constraints, success criteria, and task dependencies are understood.

The planning layer (not yet implemented) provides two modes:

- **Dream extraction**: agent-led conversation surfacing what success looks like, what is explicitly out of scope, and what the constraints are. Produces a PRD.
- **Assumptions mode**: for established repos, the agent reads the codebase, surfaces evidence-based assumptions with confidence levels, and the human confirms or corrects. 2–4 interactions instead of 15–20.

Only after planning does the pipeline create GitHub issues — as execution artifacts with explicit dependency metadata for parallel dispatch.

### Layer 2: The Spec (pipeline)

A labeled GitHub issue triggers the pipeline's spec generator. It produces:

- `specs/GH{N}/product.md` — numbered testable behavioral invariants. No implementation detail. Every invariant is observable and pass/fail.
- `specs/GH{N}/tech.md` — line-referenced implementation steps. Each step names the exact file and line range to modify.

These are committed as a PR. The community reviews intent. The spec is merged. Only then does implementation begin.

### Layer 3: The Implementation (pipeline)

A merged spec triggers the pipeline's issue worker. It reads `tech.md` as its task list, implements each step, and asserts each invariant from `product.md` before marking the PR ready for review.

Draft-first by default. Maximum 3 concurrent open agent PRs per repo (configurable). CI-fixer capped at 3 attempts before adding a "needs-human" label.

---

## Design Principles

**State lives in git.** `STATE.md`, exec plans, ADRs, specs — everything is committed. Nothing depends on agent memory or human memory. A cold agent reading the repository should be able to understand where things stand and what to do next.

**Ceremony is enforced, not requested.** An ADR requirement in a README is advisory. An ADR requirement in CI is structural. We choose structural.

**Fresh docs are the substrate.** Every layer above Layer 0 depends on agents having accurate knowledge of the codebase they are working in. Mentat is not a nice-to-have. It is the foundation.

**Parallel where possible, sequential where necessary.** The task graph has explicit `depends_on` metadata. Anything without a dependency runs in parallel. Wave completion is deterministic.

**Conservative by default.** A 3-PR throttle feels slow. That is intentional. Correctness and maintainability before throughput. Defaults can be tuned once the pipeline is proven.

**The friction is the feature.** A spec PR that requires human review before implementation is not bureaucracy — it is the mechanism by which intent is verified. The spec becomes permanent documentation. The friction is appropriate.

---

## What This Is Not

Firn is not a general-purpose agentic framework. It is a specific set of tools for a specific workflow: automated documentation generation and a disciplined issue-to-PR pipeline for software repositories.

It is not a replacement for human engineering judgment. The spec review step, the ADR process, and the exec plan requirement all exist to keep humans in the loop at the right moments.

It is not finished. Wave 1 is scaffolding. The pipeline is stubs. Mentat is a scanner without a generator. The philosophy precedes the implementation.

---

## Why "Firn"

Firn is the compacted granular snow at the head of a glacier. It is the intermediate stage between a loose snowflake and glacier ice — the layer where raw material consolidates into something durable.

This project takes loose ideas (agentic development, documentation generation, structured specs) and tries to compact them into something that works consistently. The name is a reminder that this is not the end state. It is the stage where things solidify.
