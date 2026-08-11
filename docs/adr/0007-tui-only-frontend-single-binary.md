# 0007 — TUI-only frontend in a single binary, one progress protocol

- **Status:** Accepted
- **Date:** 2026-08-11

## Context

- Fisherman's frontend is a bubbletea TUI in a separate, unintegrated Go
  module: it spawns the backend binary with a subcommand that does not
  exist and parses its line-delimited JSON progress events. The seam
  between the two processes is where its known bugs live.
- The A/B installer's frontend is a GTK4/libadwaita kiosk (Python) run
  under the cage Wayland compositor. It is why the installer ISO carries
  GTK4, Mesa/llvmpipe, and cage — all unpacked into RAM at boot, since
  the ISO runs entirely from an initramfs. It performs no privileged
  work itself: it spawns `snosi-install --non-interactive` and parses a
  second, incompatible line-delimited JSON protocol ("proto-1").
- Machines without displays (servers, serial consoles) already fall back
  to text mode on the A/B ISO; a TUI serves display and serial consoles
  with the same code.
- [ADR-0005](0005-toml-recipe-model.md) already requires the interactive
  frontend to be a recipe generator driving the same pipeline as
  non-interactive mode.

## Decision

Firn ships **one frontend: a bubbletea TUI**, in the **same binary** as
the install pipeline.

- `firn install <recipe.toml>` runs headless; `firn` (or `firn install`
  with no recipe) launches the TUI wizard.
- The TUI builds a recipe (ADR-0005) and invokes the pipeline
  **in-process** — progress flows over a Go channel, not a parsed
  byte stream. The subprocess seam that broke fisherman's TUI does not
  exist.
- For every *external* consumer (automation, tests, any future GUI), a
  `--json-progress` mode emits **one versioned line-delimited JSON
  progress protocol**, replacing both fisherman's event stream and
  `snosi-install`'s proto-1. It is a spec-governed public contract.
- On the installer ISO, the kiosk role is a systemd unit running the
  firn TUI on the console (and serial console) — no compositor, no
  graphics stack.

The GTK kiosk GUI and fisherman's TUI module are both retired, not
ported. A future GUI, if ever wanted, is a separate project consuming
the recipe schema and progress protocol; firn's contracts are designed
so that requires no firn changes.

## Consequences

- One wizard codebase serves laptops, servers, and serial consoles
  identically; no-display machines are first-class instead of a
  fallback.
- The installer ISO sheds GTK4/Mesa/cage, substantially shrinking the
  in-RAM rootfs on the A/B path (an snosi-media change tracked in the
  plan).
- Mouse-first end users lose the graphical wizard; the TUI's usability
  bar is therefore product-critical, not a developer convenience
  (bubbles/huh form quality, sensible defaults, readable at 80×24).
- The Charm dependencies (bubbletea, bubbles, huh, lipgloss) enter the
  binary. The dependency posture from ADR-0005 refines to: the frontend
  layer may take UI dependencies; the pipeline stays stdlib + host
  tools.
- Two protocol consumers exist from day one (in-process channel, JSON
  emitter), so the progress event model must be defined once and shared,
  and the JSON form needs its own spec and version field.
- Secrets never cross a process boundary in interactive use; the
  `*_file` mechanics of ADR-0005 remain for external automation only.

## Alternatives considered

- **Keep a GUI kiosk (rewrite or adapt the GTK wizard):** friendlier for
  mouse-first users, but it is a second frontend to maintain, keeps the
  heavy graphics stack in the ISO's RAM footprint, and history shows the
  out-of-process seam is where integration rots; rejected.
- **TUI as a separate binary/module (fisherman's layout):** the current
  broken state is direct evidence against it — the seam drifted because
  nothing forced the two modules to agree; rejected.
- **GUI later as a firn deliverable:** kept out of scope instead; the
  recipe schema and progress protocol are deliberately sufficient for an
  external project to build one without firn changes.

## References

- Shapes: [design/architecture.md](../design/architecture.md),
  [specs/progress-protocol.md](../specs/progress-protocol.md)
- Builds on: [ADR-0003](0003-rewrite-fisherman-as-firn.md),
  [ADR-0004](0004-single-installer-scope-and-support-matrix.md),
  [ADR-0005](0005-toml-recipe-model.md)
