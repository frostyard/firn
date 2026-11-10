---
description: Installation and validation commands, their firn-owned flags, GUI flags and exit status.
---

# Commands

This page covers firn's installation and validation commands, all flags registered by those handlers, `--dry-run`, and the flags registered by `firn-gui`. The clix command framework also supplies common options and generated commands; use `firn --help` and subcommand `--help` for that complete surface in your installed version.

## `firn`

With no arguments, open the [terminal wizard](../guides/terminal-wizard.md). Run from live media as root. Bare `firn` always detects capabilities automatically and rejects `--dry-run`.

## `firn validate <recipe.toml>`

Load and validate exactly 1 recipe against the schema and this machine. It checks referenced files and TPM/Secure Boot requirements but does not run the installation pipeline's image, tools or disk preflight.

```sh
firn validate recipe.toml
```

| Option | Meaning |
| --- | --- |
| `--secure-boot auto\|on\|off` | Override Secure Boot detection; default `auto`. |
| `--tpm auto\|on\|off` | Override TPM detection; default `auto`. |

`--uefi` is not a validation flag. `--dry-run` is rejected; use recipe-backed install dry-run instead.

## `firn install [recipe.toml]`

With a recipe, validate and run the headless pipeline. Without one, open the terminal wizard with the selected capability overrides.

```sh
firn install --dry-run recipe.toml
```

A real run is destructive:

```sh
firn install --confirm /dev/nvme0n1 recipe.toml
```

| Option | Meaning |
| --- | --- |
| `--confirm PATH` | Required for a real headless install; must equal `[target].disk` exactly as written. |
| `--json-progress` | Emit NDJSON on stdout instead of human progress on stderr; requires a recipe. The stream can contain a recovery key. |
| `--dry-run`, `-n` | Inherited common flag: run preflight only and touch no disks. Requires a recipe and no confirmation. |
| `--secure-boot auto\|on\|off` | Override Secure Boot detection; default `auto`. |
| `--tpm auto\|on\|off` | Override TPM detection; default `auto`. |
| `--uefi auto\|on\|off` | Override UEFI detection; default `auto`. |

`--confirm`, `--json-progress` and `--dry-run` are rejected without a recipe. The wizard asks for its own typed confirmation. Capability overrides are available on `firn install` without a recipe, unlike bare `firn`.

!!! warning "Overrides do not change the machine"

    `on` and `off` force firn's capability probes only. They do not create a TPM, disable firmware Secure Boot or add BIOS support.

## `firn-gui`

Open the [graphical installer](../guides/graphical-installer.md). It accepts flags only, not a recipe or another positional argument. Without root, it re-runs through `pkexec` unless demo mode or `--no-elevate` applies.

| Option | Meaning |
| --- | --- |
| `--demo` | Simulated machine and install, unprivileged; nothing written to disk. |
| `--secure-boot auto\|on\|off` | Override Secure Boot detection; default `auto`. |
| `--tpm auto\|on\|off` | Override TPM detection; default `auto`. |
| `--uefi auto\|on\|off` | Override UEFI detection; default `auto`. |
| `--appearance light\|dark` | Force appearance; empty follows the desktop. |
| `--no-elevate` | Fail instead of re-running through `pkexec` when not root. |
| `--version` | Print version information and exit. |
| `--session-env KEY=VALUE` | Repeatable display/locale forwarding flag set by `firn-gui` itself during elevation, not a general environment override. |

`--help` shows the GUI flag usage without opening the window.

## Exit status and streams

For the firn CLI, success exits 0 and failure returns non-zero. Do not depend on distinct non-zero values for recipe and install failures. A valid bootc-v1 recipe before its cutoff can exit 0 with a deprecation warning on stderr.

Headless human progress goes to stderr. With `--json-progress`, stdout contains only NDJSON events while the progress stream is active; human diagnostics still go to stderr. Early argument or validation errors can occur before a progress stream starts. A started stream must end with `done` or `error`; absence of either is failure.

See [headless installation](../guides/headless-install.md) for the full sequence and [progress events](progress.md) for consumer rules.
