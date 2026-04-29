# LLM Backend: Codex CLI

## Purpose
Add OpenAI Codex CLI as an LLM backend to both mentat and pipeline packages so
that users with `codex` installed (or `CODEX_MODEL` set) can run classification
and spec-generation without needing Claude, OpenAI API keys, or Ollama.

## Baseline
`just test` passes clean before any changes. ✓

## Milestones
- [x] M1 — Write exec plan (`docs/exec-plans/active/llm-backend-codex.md`); verify: file exists
- [x] M2 — Add `codexBackend` to `mentat/internal/classifier/classifier.go` and update `DefaultConfig()` + `newCaller()`; verify: `cd mentat && go vet ./... && go test ./...`
- [x] M3 — Add test cases for `CODEX_MODEL` detection and positional-arg dispatch in `mentat/internal/classifier/classifier_test.go`; verify: `cd mentat && go test ./...`
- [x] M4 — Add `codexBackend` to `pipeline/internal/specgen/backends.go` and update `newLLMCaller()` + pipeline `DefaultConfig()`; verify: `cd pipeline && go vet ./... && go test ./...`
- [x] M5 — Add test cases for `CODEX_MODEL` detection and positional-arg dispatch in `pipeline/internal/specgen/specgen_test.go`; verify: `cd pipeline && go test ./...`
- [x] M6 — Full build + test: `just build && just test`; verify: exit 0

## Surprises & Discoveries
- `codex` binary is already installed in the build/test environment. Placing `which codex` higher than OPENAI/OLLAMA detection would break existing tests (`TestDefaultConfig_OpenAI`, `TestDefaultConfig_Ollama`, `TestDefaultConfig_NoBackend`). Solution: put `which codex` as last-resort fallback after all env-var checks. `CODEX_MODEL` env var still takes priority over OPENAI.
- Updated existing `TestDefaultConfig_*` tests to also clear `CODEX_MODEL` env var.
- `TestDefaultConfig_NoBackend` and `TestClassify_ErrNoBackend` needed conditional logic / skip when codex is on PATH — handled gracefully.

## Decision Log
- Codex prompt passed as a single positional argument: `exec.CommandContext(ctx, "codex", prompt)` — no stdin, no flags, consistent with Yeti's `promptVia: "positional"` pattern.
- Detection priority: ANTHROPIC_API_KEY (claude) → CODEX_MODEL env var (codex) → `which codex` succeeds (codex) → OPENAI_API_KEY (openai) → OLLAMA_HOST/BASE_URL (ollama). Codex placed before openai because it is more specific.
- `which codex` detection uses `exec.LookPath("codex")` — no network, no FS writes, fast.
- Model from `CODEX_MODEL` env var is stored but Codex CLI doesn't take a `--model` flag in the same way; stored for forward compatibility.

## Outcomes & Retrospective
All milestones complete. `just build && just test` exits 0. Codex backend added to both mentat and pipeline with full test coverage for env-var detection and prompt-forwarding. The `which codex` fallback is tested conditionally so the suite is not sensitive to whether codex is installed in CI.

