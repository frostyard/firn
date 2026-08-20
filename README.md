# Firn

Firn is the single installer for every [snosi](https://github.com/frostyard)
image family — bootc OCI images and native A/B disk images — driven by one
declarative recipe, with a built-in terminal wizard. It replaces a fleet of
per-family installers with a single recipe schema and a single progress
protocol.

Firn is a GPL-3.0-only, deeply-inspired rewrite of
[fisherman](https://github.com/frostyard). Attribution to the code it draws
from lives in [`NOTICE`](NOTICE); the rationale for rewriting rather than
forking is [ADR-0003](docs/adr/0003-rewrite-fisherman-as-firn.md).

## What it does

- **Two image families, one installer.** `family = "bootc"` installs an OCI
  image straight from a container registry (deployed from the RAM installer
  over containers-storage, [ADR-0012](docs/adr/0012-bootc-install-from-ram-installer.md));
  `family = "ab"` streams a signed native A/B whole-disk image and grows it in
  place. The recipe picks the family; the pipeline splices the right steps in.
- **Recipe-driven.** A single versioned TOML file
  ([schema](docs/specs/recipe-schema.md),
  [ADR-0005](docs/adr/0005-toml-recipe-model.md)) describes the target disk,
  image, security options, and system configuration. Validation is
  fail-closed: unknown fields, unknown enum values, and wrong-family fields are
  errors, never warnings.
- **Security options.** LUKS `/var` (A/B) or root (bootc) with passphrase or
  TPM2 auto-unlock; UEFI Secure Boot with MOK enrollment on both families
  (native A/B, and bootc via secure-install schema-1,
  [ADR-0014](docs/adr/0014-port-secure-install-schema-1-for-bootc.md)).
- **Built-in TUI.** The bare `firn` command opens a terminal wizard
  (single binary, Charm stack, [ADR-0007](docs/adr/0007-tui-only-frontend-single-binary.md))
  that walks the install and emits a recipe you can reproduce headlessly.
- **One progress protocol.** Every user-visible signal from the pipeline is a
  typed progress event ([spec](docs/specs/progress-protocol.md)); `--json-progress`
  streams it as NDJSON for automation.

## Usage

```sh
firn                          # open the interactive wizard
firn validate recipe.toml     # check a recipe against the schema + this machine
firn install --confirm /dev/nvme0n1 recipe.toml   # install; confirmation must exactly match [target].disk
firn install --dry-run recipe.toml                 # preflight only; no confirmation required
firn install --json-progress --confirm /dev/nvme0n1 recipe.toml   # install with NDJSON progress
```

Non-dry-run headless installs are destructive and require
`--confirm <target-disk>`, where the value exactly matches the recipe's
`[target].disk`. A headless `--dry-run` does not modify disks and does not
require confirmation.

`firn install` takes detection overrides (`--secure-boot`, `--tpm`, and
`--uefi`, each `auto|on|off`) that force the machine-capability probes when you
know better than autodetection — e.g. in a VM. They also reach the wizard when
you run `firn install` with no recipe; the bare `firn` command always
autodetects. `--uefi` is `install`-only (`firn validate` accepts
`--secure-boot` and `--tpm` but not `--uefi`, which it does not consume).
See [`docs/specs/recipe-schema.md`](docs/specs/recipe-schema.md)
for a complete recipe.

## Building

Firn is a single Go binary with no CGO. The pipeline and domain packages use
only the Go standard library plus host tools it shells out to; the sanctioned
dependencies are a TOML decoder and, in the TUI layer only, the Charm stack
([ADR-0011](docs/adr/0011-adopt-frostyard-go-conventions.md)).

```sh
make build     # build ./build/firn
make check     # fmt + lint + test — the gate for "done"; CI runs the same
```

End-to-end harnesses under [`test/`](test/) install into throwaway QEMU guests
and verify the booted disk over SSH (`test/e2e-ab.sh`, `test/e2e-bootc.sh`,
`test/e2e-bootc-secure.sh`, `test/e2e-tui.sh`).

## Layout

Firn's own contracts are the recipe schema and the progress protocol, not a Go
API, so the pipeline lives under `internal/`:

```
cmd/firn-cli/         clix entry point (main)
cmd/firn/             cobra command handlers (install, validate, TUI)
internal/recipe/      TOML recipe model + fail-closed validation
internal/steps/       pipeline assembly (family backbone + option splices)
internal/pipeline/    the step engine, cleanup stack, runner seam
internal/bootcimg/    bootc install (podman/bootc)
internal/abimg/       native A/B image surgery (stream, grow, LUKS, TPM, MOK)
internal/secureboot/  UEFI Secure Boot ESP chain + contract (schema-1)
internal/tui/         the terminal wizard
internal/progress/    the progress protocol emitters
```

## Documentation

Start at [`docs/README.md`](docs/README.md). Docs are split by the question
they answer: [`adr/`](docs/adr/) (why — immutable decisions), [`design/`](docs/design/)
(how — living architecture), [`specs/`](docs/specs/) (what exactly — testable
contracts), [`plans/`](docs/plans/) (when — phased roadmap). Every doc starts
from its category's `TEMPLATE.md` and cross-links the docs it shapes.

Agent instructions are canonical in [`AGENTS.md`](AGENTS.md); `CLAUDE.md`,
`GEMINI.md`, and `.github/copilot-instructions.md` are symlinks to it, and
`.claude/skills` symlinks to `.agents/skills/`
([ADR-0002](docs/adr/0002-agent-portable-instruction-surface.md)) — every agent
reads the same law.

## License

GPL-3.0-only. See [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
