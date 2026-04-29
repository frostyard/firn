# pipeline-issue-worker

## Purpose
Implement the worker package that reads a merged spec PR, checks the PR throttle, calls an LLM to generate an implementation summary, and opens a draft "agent-pr" PR against the target repo. Wire the worker into `runCmd` with a 30s-poll / 10-minute timeout waiting for the spec PR to merge.

## Baseline
`just test-pipeline` passes (confirmed: all 4 test packages green).

## Milestones
- [x] M0 — Write this exec plan at `docs/exec-plans/active/pipeline-issue-worker.md`
- [x] M1 — Export `NewLLMCaller` from `pipeline/internal/specgen/specgen.go` so worker can reuse backends; verify: `cd pipeline && go build ./...`
- [x] M2 — Create `pipeline/internal/worker/worker.go` with `Config`, `WorkResult`, `GHRunner`, `ExecGHRunner`, `CountOpenAgentPRs`, and `Process`; verify: `cd pipeline && go build ./...`
- [x] M3 — Create `pipeline/internal/worker/worker_test.go` covering all 5 required cases; verify: `cd pipeline && go test ./internal/worker/...`
- [x] M4 — Update `pipeline/cmd/pipeline/main.go` to call `worker.Process()` after spec PR merges (30s poll, 10-min timeout); verify: `just test-pipeline && just build-pipeline`

## Surprises & Discoveries
- `pollSpecPRMerge` needs `encoding/json` in main.go — added to import block.
- `specgen.ExecGHRunner` is already exported so it can be reused directly in the poller.
- Worker title extraction (`extractTitle`) parses the first `# ` heading from product.md, giving a clean `impl: GH{N} Feature Name` title.

## Decision Log
- Worker imports `specgen.LLMCaller` interface directly (both in same module) rather than redefining it — avoids duplication, clean unidirectional dependency (worker → specgen).
- `NewLLMCaller` added to specgen as thin export wrapping private `newLLMCaller` — keeps backends in one place.
- `DryRun` is a field on `worker.Config` (same pattern as `specgen.Config`) — no global state.
- Polling logic lives in `main.go` as a private helper `pollSpecPRMerge` — keeps worker package pure (no I/O loop).

## Outcomes & Retrospective
All 5 worker tests pass. Full pipeline test suite (5 packages) green. `go vet` clean. Build produces binary at `pipeline/bin/pipeline`. Worker follows all conventions: no global state, errors wrapped with context, `slog` for logging, interface injection for GHRunner and LLMCaller.
