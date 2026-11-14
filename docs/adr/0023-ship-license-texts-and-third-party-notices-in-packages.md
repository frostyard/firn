# 0023 — Ship license texts and third-party notices in the packages

- **Status:** Accepted
- **Date:** 2026-11-13

## Context

- firn is GPL-3.0-only. Sections 4 and 6 of the GPL ask that every
  recipient of the program, object code included, gets a copy of the
  License with it.
- `frostyard-firn-gui` embeds, through frostui
  ([ADR-0022](0022-draw-the-graphical-installer-with-frostui.md)), the
  Inter typeface under the SIL Open Font License 1.1, whose copyright notice
  and license must go with each copy, and Lucide icons under ISC and MIT.
- Both binaries compile in Go modules under MIT, BSD, ISC and Apache
  licenses (mygo, frostui, the Charm stack, BurntSushi/toml, golang.org/x
  modules and others), most of which ask that their copyright and permission
  notice go with copies, and the Go standard library under its BSD license.
- The archives carried `LICENSE` and `NOTICE` but no module notices. The
  `.deb`, `.rpm` and `.apk` packages, which most users install (apt from the
  Frostyard repository, snosi's installer ISO), carried none of these texts,
  only the `license: GPL-3.0-only` metadata field
  ([issue #151](https://github.com/frostyard/firn/issues/151)).
- The dependency rule in `AGENTS.md` allows no new dependency without an
  ADR.

## Decision

- `scripts/third-party-notices.mjs` (Node standard library only) generates
  1 committed notices file per binary: `THIRD_PARTY_NOTICES` for `firn`
  (`./cmd/firn-cli`) and `gui/THIRD_PARTY_NOTICES` for `firn-gui`
  (`gui/`, `./cmd/firn-gui`). It runs `go list -deps` for linux/amd64 and
  linux/arm64 with `CGO_ENABLED=0`, as GoReleaser builds, and skips the
  standard library, the main module and `github.com/frostyard/firn`. For
  each other package it walks from the package directory, and from the
  directory of each file the package embeds, up to the module root, and
  takes every `LICENSE`, `LICENCE`, `COPYING`, `COPYRIGHT`, `NOTICE` or
  `OFL` file there. A package with no license file on its walk is an error.
  The Go standard library's `LICENSE` is added. The output names each file
  by module path and path in the module, without versions, sorted.
- `make notices` writes both files. `make verify` runs
  `node scripts/third-party-notices.mjs --check firn` and `make gui-check`
  runs it for `firn-gui`: a stale or missing file fails the gate.
- `.goreleaser.yaml` ships the files in each format's conventional place:
  - archives: `LICENSE`, `NOTICE` and the binary's notices file;
  - deb: `/usr/share/doc/<package>/copyright` (the GPL text, Debian Policy
    12.5), `NOTICE` and `THIRD_PARTY_NOTICES` beside it;
  - rpm: `LICENSE`, `NOTICE` and `THIRD_PARTY_NOTICES` under
    `/usr/share/licenses/<package>/`, marked `%license` (nfpm's
    `type: license`);
  - apk: the same 3 files under `/usr/share/licenses/<package>/`.
- `cmd/firn-cli/release_packaging_contract_test.go` pins this: every
  archive and nfpm entry must be in its tables and carry the files above,
  and each source file must exist and not be empty.

## Consequences

- A Go dependency change that adds a module or changes a license text must
  run `make notices` in the same pull request. A Dependabot pull request
  that adds a module fails the gate until a maintainer regenerates the
  file; a version bump alone does not.
- The deb `copyright` file is the GPL text, not a machine-readable
  Debian copyright file: nfpm maps 1 source file to 1 destination, so
  `NOTICE` (copyright holders and attribution) and `THIRD_PARTY_NOTICES` go
  beside it.
- The notices files are reviewable in each pull request, and the packages
  ship exactly what the repository holds at the tag.
- No Go dependency is added; the generator needs Node, which the template
  gate already needs, and `go`. The `gui` CI job installs Node for it.

## Alternatives considered

- **Generate the notices at release time in a GoReleaser hook:** the output
  is not reviewable and stays untested until a tag runs.
- **List the modules in `NOTICE` by hand:** it drifts from the modules the
  binaries compile in, and nothing checks it.
- **A third-party license tool such as go-licenses:** a new dependency for
  what 1 small script and `go list` do.

## References

- Shapes: [design/architecture.md](../design/architecture.md#operational-notes)
  (release and distribution)
- Builds on: [ADR-0003](0003-rewrite-fisherman-as-firn.md) (`NOTICE` and
  attribution), [ADR-0017](0017-publish-debian-packages-through-apt-publisher.md)
  (the packages and their publication),
  [ADR-0021](0021-graphical-installer-separate-executable.md) (the `gui/`
  module and `frostyard-firn-gui`),
  [ADR-0022](0022-draw-the-graphical-installer-with-frostui.md) (frostui's
  font and icons)
- Issue: [frostyard/firn#151](https://github.com/frostyard/firn/issues/151)
