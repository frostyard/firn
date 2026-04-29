# LLM Backend — GitHub Copilot CLI (pi)

## Purpose
Add GitHub Copilot CLI (`pi`) as an LLM backend to both `mentat/internal/classifier` and `pipeline/internal/specgen`, enabling Firn to use the locally-installed `pi` tool for classification and spec generation when Claude/OpenAI/Ollama are unavailable.

## Baseline
`just test` passes clean before any changes. ✅ (verified)

## Milestones
- [x] Write exec plan at `docs/exec-plans/active/llm-backend-copilot.md`
- [x] M1: `mentat/internal/classifier/classifier.go` — add copilot to `DefaultConfig()` (priority: claude → copilot → codex → openai → ollama) and update `ErrNoBackend` message; update `newCaller` switch; verify: `just test-mentat`
- [x] M2: Add `mentat/internal/classifier/copilot.go` — `copilotBackend` struct implementing `LLMCaller`, invoking `pi --print --no-session --no-context-files --no-tools` with prompt via stdin; verify: `just build-mentat`
- [x] M3: Add tests for copilot in `mentat/internal/classifier/classifier_test.go` — `TestDefaultConfig_Copilot` (GH_COPILOT_TOKEN set) and `TestDefaultConfig_CopilotWhich` (which pi succeeds, token absent) cases; verify: `just test-mentat`
- [x] M4: `pipeline/internal/specgen/specgen.go` — add copilot to `DefaultConfig()`, update `ErrNoBackend`; `pipeline/internal/specgen/backends.go` — update `newLLMCaller` switch + add copilot backend; verify: `just test-pipeline`
- [x] M5: Add tests for copilot in `pipeline/internal/specgen/specgen_test.go`; verify: `just test-pipeline`
- [x] M6: Final `just build` + `just test` clean pass

## pi CLI flags chosen
`pi --print --no-session --no-context-files --no-tools`
- `--print` / `-p`: non-interactive mode, exits after processing
- `--no-session`: ephemeral, no saved session state
- `--no-context-files`: skip AGENTS.md/CLAUDE.md discovery
- `--no-tools`: no filesystem/bash tools — pure text generation
- Prompt passed via stdin (consistent with how `pi [messages...]` works in non-interactive mode)
- Default model: empty (uses `pi`'s configured default); override via `Config.Model` using `--model`

## Surprises & Discoveries
- `pi suggest` subcommand from the task spec doesn't exist; `pi --help` shows `--print` is the non-interactive flag
- The yeti reference (`claude.ts`) uses a separate `copilot` binary with different flags — `pi` is a different tool

## Decision Log
- Priority order claude → copilot → codex → openai → ollama: `codex` not yet a backend, so effective order is claude → copilot → openai → ollama
- Detection: `GH_COPILOT_TOKEN` env var first; fall back to `exec.LookPath("pi")` succeeding
- Prompt via stdin (not positional arg) to handle long prompts safely
- `--no-tools` prevents `pi` from making filesystem edits during classification

## Outcomes & Retrospective
- All milestones completed successfully.
- `pi --print --no-session --no-context-files --no-tools` chosen as the correct non-interactive invocation (the `suggest` subcommand in the task spec doesn't exist in the real `pi` CLI).
- Existing tests needed PATH isolation (`t.Setenv("PATH", "/usr/bin:/bin")`) to prevent the installed `pi` binary from hijacking backend detection in non-copilot tests.
- Both modules build clean, all tests pass, `go vet` is clean.
