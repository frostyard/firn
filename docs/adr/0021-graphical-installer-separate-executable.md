# 0021 — Ship a graphical installer as a separate executable in its own Go module

- **Status:** Accepted (2026-10-26); amended by snosi
  [ADR-0022](https://github.com/frostyard/snosi/blob/main/docs/adr/0022-installer-graphical-and-console-boot-entries.md)
  ([frostyard/snosi#1155](https://github.com/frostyard/snosi/pull/1155)): the installer ISO is no longer
  unaffected, since its boot menu adds a graphical entry that runs `firn-gui`
  full screen beside the console entry that keeps the TUI kiosk.
- **Date:** 2026-10-25

## Context

- [ADR-0007](0007-tui-only-frontend-single-binary.md) made the bubbletea
  TUI firn's only frontend and kept a GUI out of scope: "A future GUI, if
  ever wanted, is a separate project consuming the recipe schema and
  progress protocol". It also recorded the cost: "Mouse-first end users
  lose the graphical wizard".
- Live desktop media (a GNOME session booted from USB) already run a
  compositor and GTK 3. Users there expect a windowed, mouse-first
  installer, not a terminal.
- ADR-0007's main lesson still holds: the out-of-process seam between a
  frontend and the install backend is where fisherman's bugs lived. The
  TUI avoids it by assembling a recipe and running the pipeline
  in-process with progress over a Go channel.
- Running `firn install --json-progress` under `pkexec` from an
  unprivileged GUI has two further problems. The unprivileged parent
  cannot signal its root child, so it cannot cancel an install and let the
  pipeline unwind its mounts and mappers. And the `tpm2-luks` recovery key
  would have to be read off a pipe instead of staying in-process
  ([progress protocol](../specs/progress-protocol.md#recovery-key-disclosure)).
- [mygo](https://github.com/egoist/mygo) (MIT, v0.6.4) draws native UI in
  Go with OpenGL or the CPU, without cgo: it loads GTK 3, Pango and
  Cairo with `dlopen` (purego). A native-UI window needs no WebKitGTK and
  no JavaScript toolchain. Its `ui.NewTester` runs a view without a window
  and renders frames to images, so pages are unit-testable. mygo v0.6.4
  requires Go 1.27.3.
- The root module pins go1.26.10. `make verify` requires golangci-lint
  built with a Go at least as new as that toolchain
  ([ADR-0011](0011-adopt-frostyard-go-conventions.md), core ADR-0043).
  The pinned golangci-lint 2.13.1 and the newest release, 2.15.0, are both
  built with go1.27.0, and go1.27.0 cannot read the go1.27.3 standard
  library's export data. Moving the root module to Go 1.27.3 would stop
  golangci-lint from typechecking anything in the repository.
- The dependency rule stays: pipeline and domain packages use the standard
  library plus host tools; only the frontend layer takes UI dependencies
  (ADR-0007, Consequences).

## Decision

**Firn ships a second frontend, the graphical wizard `firn-gui`, as a
separate executable in a nested Go module.** The `firn` binary, its TUI,
the kiosk units and the installer ISO's TUI kiosk role are unchanged. This
supersedes ADR-0007 only where it limits firn to a TUI and places any GUI
in a separate project.

1. **Separate module.** The GUI lives in `gui/`, module
   `github.com/frostyard/firn/gui`, with its own `go.mod` (Go 1.27.3) and
   a `replace` of `github.com/frostyard/firn` to the repository root. It
   imports firn's `internal/` packages directly: Go allows this in module
   mode because the import path `github.com/frostyard/firn/gui/...` sits
   under the root of `internal/`. The root module's Go pin, dependencies
   and `make check` scope stay as they are; `gui/` has its own make
   targets.
2. **In-process, like the TUI.** The GUI is a recipe generator. It
   assembles a recipe from its pages, writes secrets only as 0600 files
   in a private 0700 session directory under `/run/firn`, shows the exact
   TOML on its review page, validates those bytes with `recipe.Parse` and
   `recipe.Validate`, writes and reloads `recipe.toml`, and runs
   `steps.Assemble` with progress over a `progress.Channel`. There is no
   subprocess and no NDJSON parsing. The recovery key stays in-process: it
   is shown on a blocking acknowledgement, never copied to the clipboard,
   and the window cannot close until the user acknowledges it. The window
   also stays while an install runs; a quit or SIGINT/SIGTERM cancels the
   install and quits only after its cleanups have closed what it opened.
3. **Typed confirmation.** Before installing, the user types the exact
   target disk path: the same rule as headless `--confirm` and the TUI's
   confirmation page.
4. **Privilege.** The GUI runs as root, as the TUI does. Started
   unprivileged, it re-executes itself through `pkexec`, passing on only
   the display variables a GTK window needs. `--demo` runs a simulated
   machine and a simulated install without privilege and never touches a
   disk.
5. **Framework.** mygo native UI, pinned to an exact version, sanctioned
   for the `gui/` module only.
6. **One catalog.** The image catalog and the offered user groups move
   from `internal/tui` to `internal/catalog`, so both frontends offer the
   same images, trust tuples and groups.
7. **The schema is the field validator.** The GUI checks fields by
   assembling a recipe from the current answers, with placeholder secret
   files, and running `recipe.Validate` on it. It keeps no copies of the
   schema's patterns.
8. **Initial selections match the TUI's documented ones**: encryption
   `none`, MOK `enroll`, a user with the catalog's default groups, core
   Flatpaks on when offered, btrfs without subvolumes, systemd-boot. Where
   the TUI leaves an optional field empty, the GUI may prefill it from
   the running session (locale, timezone, keyboard) or from the image name
   (hostname). Every prefill can be changed or cleared.

## Consequences

- Mouse-first users on live desktop media get a graphical installer that
  produces the same recipes and runs the same pipeline as the TUI.
- Two frontends must stay in parity. Recipe-schema rule 5 and the
  interactive parity table now cover both, and the progress protocol
  lists the GUI's progress view as a derived artifact.
- Building `gui/` needs Go 1.27.3. With the default `GOTOOLCHAIN=auto`, an
  older `go` downloads it.
- The pinned golangci-lint cannot lint `gui/` until a release is built
  with Go 1.27.3 or newer, for the export-data reason above. Until then
  `make gui-check` runs gofmt, `go vet` and the tests. Bump golangci-lint
  and add it to that target when a compatible build ships.
- `firn-gui` needs GTK 3, Pango and Cairo at run time, and OpenGL (or it
  falls back to drawing on the CPU). They are loaded with `dlopen`, so a
  machine missing them fails at startup with a clear error rather than at
  link time. The installer ISO, which has no graphics stack, is
  unaffected and keeps the TUI kiosk.
- A root GTK client joins the user's compositor. `pkexec` strips the
  environment, so the GUI forwards an allowlist of display variables to
  itself. Desktops that refuse root clients need the TUI instead.
- mygo is pre-1.0. An exact version pin and the headless page tests
  contain API churn; upgrades are deliberate.
- The `gui/` tests load Pango through mygo's text engine, so the machine
  running them needs Pango and Cairo installed.
- Releases ship `firn-gui` as the `frostyard-firn-gui` package: the binary
  alone in `/usr/libexec/firn-gui/` behind a `/usr/bin/firn-gui` symlink,
  because mygo deletes `.*.old-*` entries beside its executable at
  startup (firn-gui also refuses to start where any exist), a desktop
  entry, and a polkit action that requires an administrator's password
  every time and only in an active local session. The package is for live
  desktop media; installed images must not ship it, or every desktop
  would get a launcher that can erase a disk. apt-publisher must list the
  package before a release ships it
  ([ADR-0017](0017-publish-debian-packages-through-apt-publisher.md)).
- ADR-0007 stays Accepted, amended by this ADR where it limits firn to a
  TUI.

## Alternatives considered

- **Keep the TUI as the only frontend (ADR-0007):** leaves desktop users
  with a terminal installer on a graphical session; rejected.
- **A GUI that drives `firn install --json-progress` under `pkexec`
  (ADR-0007's external GUI):** brings back the interactive subprocess
  seam, cannot cancel a root child from an unprivileged parent, and moves
  the recovery key across a pipe. It remains possible for a GUI outside
  this repository; rejected for one that can link the pipeline.
- **A webview frontend (mygo web pages, Wails, Electron):** needs
  WebKitGTK or a bundled browser engine and a JavaScript toolchain in the
  build; rejected for a heavier runtime and build than native UI.
- **GTK 4/libadwaita through cgo bindings (gotk4), Fyne or Gio on
  Linux:** need cgo, which breaks firn's `CGO_ENABLED=0` cross-builds;
  rejected.
- **The GUI in the root module with Go bumped to 1.27.3:** golangci-lint
  could no longer typecheck the repository, and firn's own Go floor would
  rise for a dependency only the GUI uses; rejected until a compatible
  golangci-lint ships, when merging the modules can be reconsidered.

## References

- Shapes: [design/architecture.md](../design/architecture.md#graphical-installer-gui),
  [specs/recipe-schema.md](../specs/recipe-schema.md#interactive-wizard-parity),
  [specs/progress-protocol.md](../specs/progress-protocol.md#derived-artifacts),
  [plans/roadmap.md](../plans/roadmap.md#phase-11-graphical-installer-medium-in-progress)
- Amends: [ADR-0007](0007-tui-only-frontend-single-binary.md)
- Builds on: [ADR-0005](0005-toml-recipe-model.md),
  [ADR-0007](0007-tui-only-frontend-single-binary.md),
  [ADR-0011](0011-adopt-frostyard-go-conventions.md),
  [ADR-0019](0019-wizard-offers-core-flatpaks-by-default.md)
