---
description: Build the CLI, graphical installer and documentation site, and run the repository gates.
---

# Build from source

firn's root Go module builds the CLI and terminal wizard without CGO. The graphical installer is a separate nested module in `gui/`, with its own Go requirement. Repository tools are pinned in `mise.toml` and `mise.lock`; do not choose substitute versions to bypass a failed gate.

## Build the CLI

```sh
git clone https://github.com/frostyard/firn.git
cd firn
mise trust
mise install
make build
```

`make build` writes `build/firn`. Building does not run an installation. Do not launch a real install on a development host to test a documentation or UI change.

## Run checks

| Target | Scope |
| --- | --- |
| `make verify` | Non-mutating, credential-free gate: template checks, module tidy diff, formatting check, pinned lint, vet and tests. |
| `make check` | Formatting, lint, tests and the template gate; formatting can update source files. |
| `make ci` | Verify plus coverage, the race detector and cross-architecture CLI builds. With `BASE=<ref>`, also checks the corrections records against the base (no record removed or renamed). |

```sh
make verify
make ci
```

The root targets do not enter the nested GUI module. Root/KVM end-to-end harnesses are separate from these checks and require their documented isolated environments. Read the repository's [AGENTS.md](https://github.com/frostyard/firn/blob/main/AGENTS.md) before using them.

## Build the graphical installer

The GUI needs Go 1.27.4 (the pin in `gui/go.mod`); mygo needs Go 1.27.3 or later. With `GOTOOLCHAIN=auto`, an older Go can fetch the required toolchain. Install GTK 3, Pango and Cairo for the GUI runtime; tests need Pango and Cairo for real text layout. Without them, a fallback stub can let tests pass without drawing text.

```sh
make gui-build
make gui-check
make gui-demo
```

`gui-build` writes `build/firn-gui`. `gui-check` runs tidy diff, formatting checks, vet and tests within `gui/`. `gui-demo` runs simulated disks and progress without root and writes nothing to disk. The root linter does not lint the nested module.

For a GUI page change, render snapshots from `gui/` and inspect both appearances:

```sh
cd gui
FIRN_GUI_SNAPSHOTS=../build/gui-snapshots go test ./internal/installer -run TestSnapshots
```

Keep the real GUI binary in its own directory if you install it manually. The package uses `/usr/libexec/firn-gui/` because the UI toolkit deletes update leftovers beside the executable.

## Build and preview the documentation

The site uses ProperDocs with MaterialX. It needs Python 3 with `venv` support and the hashed dependency lock already in the repository.

```sh
make site
make site-serve
```

`make site` builds strict into `site/dist/`. `make site-serve` previews at `http://127.0.0.1:8000/firn/`. The virtual environment lives in `build/site-venv`. `make ci` does not build the site; the separate site workflow does.

User pages live in `site/content/`; every page must be in `site/properdocs.yml` navigation. Use relative `.md` links between site pages and absolute GitHub URLs for repository contracts. Keep user-visible behavior and its affected site page in the same pull request. Maintainer ADRs, designs, specifications and plans stay in [docs/](https://github.com/frostyard/firn/blob/main/docs/README.md).

Use sentence case headings, lowercase product names and a calm declarative tone. Keep `font: false` and use only the palette in `frostyard.css`.

Regenerate the requirements lock only when updating dependencies; never hand-edit it:

```sh
uv pip compile --universal --generate-hashes site/requirements.in -o site/requirements.txt
```

## Contribute

Read [AGENTS.md](https://github.com/frostyard/firn/blob/main/AGENTS.md) for source, test, provenance and review rules. Keep public recipe or progress changes with their specifications and user pages. Report vulnerabilities through [SECURITY.md](https://github.com/frostyard/firn/blob/main/SECURITY.md), not a public issue.
