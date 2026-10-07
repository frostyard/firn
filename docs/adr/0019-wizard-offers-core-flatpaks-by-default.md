# 0019 — Start the wizard's core-flatpaks toggle on

- **Status:** Accepted
- **Date:** 2026-10-07

## Context

- [ADR-0006](0006-install-time-offline-first-flatpaks.md) made
  `core_flatpaks` opt-in, and the TUI started its toggle unchecked
  ([ADR-0018](0018-image-published-core-flatpaks-label.md), Context).
- Since ADR-0018, each image publishes the set that fits it. Snow's set
  holds the desktop's everyday apps, such as its image viewer, PDF viewer
  and calculator; Snow does not ship them natively, and first-setup no
  longer installs them at first boot. A default Snow install therefore has
  none of them.
- Sundog is replacing its native Gwenview, Okular, KCalc and Skanpage with
  the same apps from its published set
  ([snosi Sundog Flatpak plan](https://github.com/frostyard/snosi/blob/main/docs/plans/2026-10-05-sundog-flatpak-defaults-plan.md)).
  With the toggle unchecked, a default Sundog install would have no image
  viewer, PDF viewer, calculator or scanner app.
- The TUI hides the toggle, and clears it, for an image with no set or a
  malformed label. It keeps the toggle when the label could not be read,
  and preflight reads the label again at install.

## Decision

**The wizard starts the core-flatpaks toggle on whenever it offers it.**

1. When the chosen image's preview shows a valid set, or the label could
   not be read, the toggle starts enabled.
2. The initial selection applies once per chosen image. Rebuilding the form
   or revisiting the page keeps the user's answer; choosing another image,
   or Start over, applies that image's initial selection.
3. The recipe default for `core_flatpaks` stays `false`. Headless recipes
   and saved recipes are unchanged.

## Consequences

- Default wizard installs of Snow, Snowfield and Sundog get their image's
  core apps. Floe offers no toggle and is unchanged.
- Default desktop installs download their core set. Published ISOs carry no
  flatpak seed, so a default Snow install now downloads its 23 apps and
  their runtimes, several GiB, at install time.
- When the label could not be read in the wizard, an install that still
  cannot read it fails preflight with `core_flatpaks_label_unreadable`,
  before any disk write. The user can retry or decline the toggle. Starting
  it off instead would turn a transient registry timeout into an install
  silently missing its core apps.
- The wizard default and the recipe default now differ: a recipe written by
  hand without `core_flatpaks` installs no core set.

## Alternatives considered

- **Start on only when the preview read a valid set:** an unreadable label
  would silently produce an install without core apps, the case this
  decision exists to prevent; rejected.
- **Change the recipe default to `true`:** changes the meaning of every
  saved and headless recipe; rejected.
- **A per-image default published by the image or catalog:** a second
  contract for a choice every current desktop image makes the same way;
  rejected until an image needs it.

## References

- Shapes: [specs/recipe-schema.md](../specs/recipe-schema.md),
  [specs/core-flatpaks-label.md](../specs/core-flatpaks-label.md),
  [design/architecture.md](../design/architecture.md),
  [plans/roadmap.md — Phase 10](../plans/roadmap.md#phase-10)
- Builds on: [ADR-0006](0006-install-time-offline-first-flatpaks.md),
  [ADR-0018](0018-image-published-core-flatpaks-label.md)
