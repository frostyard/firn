# 0022 — Draw the graphical installer with frostui, in the Frostyard look

- **Status:** Accepted
- **Date:** 2026-11-11

## Context

- [ADR-0021](0021-graphical-installer-separate-executable.md) sanctioned
  mygo for the `gui/` module only, and the dependency rule in `AGENTS.md`
  allows no other dependency without an ADR.
- `firn-gui` drew a look of its own: `gui/internal/installer/style.go`
  held a palette per appearance (a deep-blue sidebar gradient, its own
  blues and greens, corners of 5 to 18 DIPs, drop shadows), the system UI
  font, and literal font sizes that ignored the desktop's text size. No
  test measured its contrast.
- frostyard's desktop apps (ChairLift, Frostmail, Frostnotes) draw the
  frostyard brand through [frostui](https://github.com/frostyard/frostui)
  (MIT), the org's UI kit on mygo. Its v0.5.0 implements frostui
  [ADR-0011](https://github.com/frostyard/frostui/blob/main/docs/adr/0011-draw-the-frostyard-brand-in-a-dark-and-a-light-theme.md):
  a dark and a light theme (Frostyard, Frostyard light) whose text pairs
  are measured at 4.5:1 and focus ring at 3:1, Inter embedded under the
  SIL Open Font License 1.1, text sizes that follow the desktop's text
  size, 1px hairlines in place of shadows, and components: cards, icon
  tiles, outlined pills, keys, ghost and gradient buttons, a hero, key and
  value lists, a shortcuts list.
- frostui v0.5.0 requires mygo v0.9.0; `firn-gui` pinned v0.7.4. On v0.9.0
  the installer builds and its tests pass unchanged. frostui needs no cgo,
  and adds no module that mygo did not already bring but itself.
- frostui finds icons by freedesktop name: an app's embedded icons first,
  then the desktop's icon theme, then the kit's Lucide set. The installer
  ISO's graphical entry runs `firn-gui` as root in cage, with no desktop
  session to name an icon theme; live desktops run it under GNOME's
  Adwaita or KDE's Breeze.

## Decision

1. **Dependency.** The `gui/` module may depend on
   `github.com/frostyard/frostui`, at the exact version in `gui/go.mod`.
   Like mygo it never enters the root module, and a frostui upgrade may
   move mygo with it.
2. **Theme.** `firn-gui` draws Frostyard or Frostyard light
   (`frostui.FrostyardTheme`) for the appearance `--appearance`,
   Ctrl+Shift+L or the desktop picks, with the desktop's high contrast and
   text size and without its accent, then calls `frostui.Adapt`, which
   keeps that theme and registers Inter. Text takes the kit's styles and
   metrics, not literal sizes and radii.
3. **Components.** Where the kit has a component, the installer uses it
   (`Card`, `IconTile`, `Pill`, `IconPill`, `Shortcut`, `LabeledButton`,
   `IconButton`, `Hero`, `KeyValues`, `Loading`, `Problem`,
   `ShortcutsBody`). What a wizard needs and the kit lacks stays in
   `gui/internal/installer/style.go`, built only from the kit's colors
   (`ColorsOf`, `ToneColor`, `ToneFill`, `ToneLine`) and metrics: choice
   cards, notes with a title, outlined tags that keep the case of a name,
   the forward and destructive buttons, the strength meter and the dialog
   panel.
4. **Icons.** The installer keeps the icons drawn for it, named with
   freedesktop names where one fits, and hands them to `frostui.NewIcons`
   as embedded icons, the names the kit's components draw
   (`window-close`, `dialog-warning`) included. They win over the
   desktop's theme and the kit's set, so the installer looks the same on
   every desktop and in the ISO's cage session.
5. **Layout.** The window stays a wizard: a rail of steps, a header, the
   page, and a footer of actions, drawn on the kit's chrome, panel and
   selected-row colors. It does not use frostui's `Shell` or `PaneShell`.

## Consequences

- `firn-gui` looks like the other frostyard desktop apps, the
  documentation site and the snosi images, in light and dark, and every
  color it draws is a role the kit measures.
- The desktop's text size and high contrast now reach the installer's
  text, so layouts must leave room at larger sizes. A user's desktop
  accent color does not reach it, as it did not before.
- The stripped `firn-gui` binary grows from 13.1 MB to 15.8 MB, mostly
  the embedded Inter files. `NOTICE` names Inter (SIL OFL 1.1) and the
  kit's Lucide icons (ISC and MIT), which the binary carries.
- Before frostui v1 a minor version may change the kit's API or look
  (its design doc lists each change). Each upgrade re-renders the
  snapshots in both appearances and reviews them, and may bring a mygo
  upgrade with it.
- `style.go` is a second place the look is drawn. When the kit gains a
  component that covers one of its helpers (a titled banner, a primary
  button with a trailing icon), the installer moves to the kit's.
- The TUI and the root module are unchanged. `AGENTS.md`'s dependency
  rule names frostui beside mygo.

## Alternatives considered

- **Keep the installer's own palette:** frostyard's desktop software
  would keep two looks, and the installer's contrast would stay
  unmeasured; rejected.
- **Copy frostui's values into the installer:** the copy drifts from the
  kit's spec, and the font, the measured contrast and the components stay
  behind; rejected.
- **frostui's `Shell` or `PaneShell`:** their top bar, command palette,
  console and status bar serve apps with commands and background jobs. A
  linear wizard needs a rail of steps whose later ones cannot be chosen
  yet, and a footer of actions; rejected.
- **Icons from the desktop's theme:** they would change with the desktop
  (Adwaita, Breeze, or the kit's fallback set as root in cage), and some
  of the installer's icons have no freedesktop name; rejected.

## References

- Shapes: [design/architecture.md](../design/architecture.md#graphical-installer-gui),
  [plans/roadmap.md](../plans/roadmap.md#phase-11-graphical-installer-medium-in-progress)
- Builds on: [ADR-0021](0021-graphical-installer-separate-executable.md),
  [ADR-0007](0007-tui-only-frontend-single-binary.md) (frontend layers take
  UI dependencies; pipeline and domain packages do not)
- External: frostui
  [ADR-0011](https://github.com/frostyard/frostui/blob/main/docs/adr/0011-draw-the-frostyard-brand-in-a-dark-and-a-light-theme.md)
  and [spec: the Frostyard themes](https://github.com/frostyard/frostui/blob/main/docs/specs/frostyard-theme.md)
