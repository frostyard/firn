<!-- readme-kind: tool -->
# Firn

[![CI](https://github.com/frostyard/firn/actions/workflows/ci.yml/badge.svg)](https://github.com/frostyard/firn/actions/workflows/ci.yml)

Firn installs snosi bootc OCI images from a declarative TOML recipe, with a
terminal wizard and a graphical installer. It is for anyone who puts a snosi
image on a disk: the wizards write the recipe, and the same recipe installs
headlessly.

Firn is a GPL-3.0-only rewrite of fisherman, and it keeps the attribution of
the code it draws from ([`NOTICE`](NOTICE),
[ADR-0003](docs/adr/0003-rewrite-fisherman-as-firn.md)).

**Status:** pre-release. It ships on the snosi installer ISO.

**Documentation:** [docs/README.md](docs/README.md)

## What it does

- **Bootc-only installer.** `family = "bootc"` installs an OCI image straight
  from a container registry, deployed from the RAM installer over
  containers-storage
  ([ADR-0012](docs/adr/0012-bootc-install-from-ram-installer.md)).
- **Recipe-driven.** One versioned TOML file describes the target disk, image,
  security options and system configuration
  ([schema](docs/specs/recipe-schema.md),
  [ADR-0005](docs/adr/0005-toml-recipe-model.md)). Validation is fail-closed:
  unknown fields, unknown enum values and wrong-family fields are errors.
  Version 2 is current; bootc version 1 is accepted with a deprecation warning
  until 2026-12-30 and rejected from that date.
- **Security options.** LUKS root with a passphrase or TPM2 auto-unlock, and
  UEFI Secure Boot with MOK enrollment via secure-install schema-1
  ([ADR-0014](docs/adr/0014-port-secure-install-schema-1-for-bootc.md)).
- **Terminal wizard.** The bare `firn` command walks the install in one binary
  on the Charm stack
  ([ADR-0007](docs/adr/0007-tui-only-frontend-single-binary.md)) and emits a
  recipe you can reproduce headlessly.
- **Graphical installer.** `firn-gui` is a windowed wizard with a keyboard
  shortcut for every action, from the nested module [`gui/`](gui/)
  ([ADR-0021](docs/adr/0021-graphical-installer-separate-executable.md)).
- **Install-time flatpaks.** Flatpaks are installed into the target at
  install time, from the installer medium first and the network second; each
  image publishes its core set in an OCI label
  ([ADR-0006](docs/adr/0006-install-time-offline-first-flatpaks.md),
  [label spec](docs/specs/core-flatpaks-label.md)).
- **One progress protocol.** Every user-visible signal is a typed progress
  event, and `--json-progress` streams it as NDJSON for automation
  ([spec](docs/specs/progress-protocol.md)).

## Install

The snosi installer ISO runs firn: its console boot entry opens the terminal
wizard, and its graphical boot entry opens `firn-gui`
([frostyard/snosi](https://github.com/frostyard/snosi)).

On Debian trixie with the Frostyard APT repository
(`https://repository.frostyard.org/debian/`):

```sh
sudo apt install frostyard-firn
```

`frostyard-firn-gui` is for installer media, not installed systems.

[Release and
distribution](docs/design/architecture.md#operational-notes) lists the
packages and where they go.

## Use

```sh
firn                                                  # the terminal wizard
firn validate recipe.toml                             # check a recipe against this machine
firn install --confirm /dev/nvme0n1 recipe.toml       # install; --confirm must equal [target].disk
firn install --dry-run recipe.toml                    # preflight only, no disks touched
firn install --json-progress --confirm /dev/nvme0n1 recipe.toml   # NDJSON progress
```

The [command-line surface](docs/design/architecture.md#command-line-surface)
lists every flag, and the
[recipe schema](docs/specs/recipe-schema.md) describes the file.

## Develop

Firn is one Go binary with no CGO, plus the nested `gui/` module. Everything
below runs on the pins in [`mise.toml`](mise.toml):

```sh
mise install   # Go, golangci-lint and Node, pinned and verified
make ci        # the credential-free gate that CI runs
make gui-check # the gui/ module's own gate
```

`make gui-check` needs Pango and Cairo installed: without them the text
engine falls back to a stub that draws nothing and the tests pass without real
layout. [AGENTS.md](AGENTS.md) holds the rules, and
[the architecture](docs/design/architecture.md) shows how the parts fit.

## Where to go next

- [AGENTS.md](AGENTS.md): the rules for agents and contributors.
- [docs/README.md](docs/README.md): the decisions, designs, specs and plans.
- [SECURITY.md](SECURITY.md): the support policy, and how to report a
  vulnerability privately.
- [LICENSE](LICENSE): GPL-3.0-only.
- [NOTICE](NOTICE): the projects firn derives from.
