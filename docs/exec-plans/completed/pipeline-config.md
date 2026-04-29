# Pipeline Config File Support

## Purpose
Add TOML-based configuration loading to the pipeline CLI so operators can tune
`pr_throttle`, `ci_fixer_max_attempts`, and `draft_first` per-repo or globally
without recompiling, in line with ADR-006 conservative defaults.

## Baseline
Run `just test` to confirm baseline passes before any changes.

```
ok  github.com/frostyard/firn/pipeline/cmd/pipeline
?   github.com/frostyard/firn/pipeline/internal/version  [no test files]
```

## Milestones

- [x] M1 — Create `pipeline/internal/config/config.go` with `Config`, `PipelineConfig`, `Default()`, `Load()`, `ConfigPath()`;
        verify: `cd pipeline && go build ./...`
- [x] M2 — Create `pipeline/internal/config/config_test.go` with table-driven tests for `Default()`, `Load()`, `ConfigPath()`;
        verify: `cd pipeline && go test ./...`
- [x] M3 — Update `pipeline/cmd/pipeline/main.go` `runCmd()` with `--config` flag, config loading, verbose log of effective config;
        verify: `cd pipeline && go vet ./... && go test ./...`
- [x] M4 — Final: `just build-pipeline` clean, `just test-pipeline` clean, `just lint` clean

## Surprises & Discoveries
- `t.Setenv` cannot be used with `t.Parallel()` in Go 1.26 — removed `t.Parallel()` from the three
  tests that redirect HOME.
- `clix.Verbose` is a package-level `bool` in clix v0.1+, confirmed in source.
- Files written to disk before `git checkout` were lost in the stash/pop across branches; had to
  re-create them on the correct branch.

## Decision Log
- Using `github.com/spf13/viper` (already a transitive dep via clix) rather than adding `BurntSushi/toml`
  to keep the module graph tidy — as stated in ADR-006.
- `pelletier/go-toml/v2` is already in go.mod (indirect), so TOML parsing is available through viper at zero
  additional module cost.

## Outcomes & Retrospective
All four milestones completed. Three new/modified files:
- `pipeline/internal/config/config.go` — Config/PipelineConfig structs, Default(), Load(), ConfigPath()
- `pipeline/internal/config/config_test.go` — 8 tests (table-driven), all green
- `pipeline/cmd/pipeline/main.go` — runCmd wired with --config flag and config loading

`just build-pipeline`, `just test-pipeline`, `just lint` all exit 0.
