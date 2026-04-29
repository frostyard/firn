# mentat LLM Domain Classifier

## Purpose
Implement a single-call LLM classifier that takes scanner `Candidate` objects and returns
`DomainResult` values describing each logical domain's name, path, description, file count,
and languages. Wire the classifier into `syncCmd` so the full scan → classify pipeline runs
end-to-end, with `--dry-run` stopping after printing classified domains.

## Baseline
`just test-mentat` passes before any changes (confirmed: all tests pass on branch
`mentat/llm-classifier` branched from `mentat/scanner`).

## Milestones
- [x] Milestone 1 — Write `docs/exec-plans/active/mentat-llm-classifier.md` (this file)
- [x] Milestone 2 — Create `mentat/internal/classifier/classifier.go` with `DomainResult`,
  `Config`, `LLMCaller` interface, `DefaultConfig()`, `Classify()`, `ErrNoBackend`, and
  the three concrete backends (`claudeBackend`, `openaiBackend`, `ollamaBackend`);
  verify: `cd mentat && go build ./...`
- [x] Milestone 3 — Create `mentat/internal/classifier/classifier_test.go` with mock-based
  tests for `Classify()`, backend selection, and error handling;
  verify: `cd mentat && go test ./internal/classifier/...`
- [x] Milestone 4 — Wire classifier into `syncCmd` in
  `mentat/cmd/mentat/commands.go` (call `Classify`, print results, respect `--dry-run`);
  verify: `cd mentat && go build ./... && go vet ./...`
- [x] Milestone 5 — Full suite passes: `just test-mentat` and `just build-mentat`
- [x] Milestone 6 — Update `STATE.md`: mark `mentat-llm-domain-classifier` done

## Surprises & Discoveries
- `ClassifyWith` (exported, takes explicit LLMCaller) needed the same empty-candidates guard
  as `Classify`; the guard was initially only in `Classify`, causing a test failure when the
  mock returned `""` and `parseResponse` tried to unmarshal empty input.
- `noopLogger()` in tests required a write-to-nil `slog.TextHandler` — used a very high
  log level instead to suppress all output cleanly.

## Decision Log
- `LLMCaller` interface isolates all network/exec I/O so unit tests never touch the network.
- `claudeBackend` uses `exec.Command("claude", ...)` as specified; stdout is the response.
- `openaiBackend` uses the OpenAI chat completions REST endpoint directly (no SDK) to avoid
  adding a heavy dependency.
- `ollamaBackend` uses the Ollama `/api/generate` REST endpoint directly.
- Prompt asks for JSON array `[{"name":…,"path":…,"description":…}]`; response is parsed
  by stripping any markdown fences before `json.Unmarshal`.
- `FileCount` and `Languages` on `DomainResult` are filled from the matching `Candidate`
  (keyed by path) after the LLM response is parsed, so the LLM doesn't need to reproduce
  data we already have.

## Outcomes & Retrospective
All 28 tests pass (14 new classifier tests + 14 pre-existing). Build and vet are clean.
The `LLMCaller` interface provides clean seam for unit tests — zero network calls in the
test suite. Backend detection via env vars is table-tested exhaustively. The exec plan
accurately forecast the work; no milestone required back-tracking.
