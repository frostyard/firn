# 0018 — Read each image's core flatpak set from an OCI label

- **Status:** Proposed
- **Date:** 2026-10-06

## Context

- [ADR-0006](0006-install-time-offline-first-flatpaks.md) lets a recipe set
  `core_flatpaks = true` to install "the image-defined core set where the
  image family publishes one". It is off by default, and the TUI starts the
  toggle unchecked.
- Today the core set is not image-defined in practice. `flatpak.CoreSet`
  reads `usr/share/org.frostyard.FirstSetup/snow_first_setup/core.json`, a
  file the frostyard/first-setup package owns. That list is GNOME-oriented.
- Snosi desktop images are composefs-native, so that file is unreadable in
  the deployment at install time
  ([ADR-0012](0012-bootc-install-from-ram-installer.md)). Firn falls back to
  `flatpak.InstallerCoreSet`, which reads `/usr/share/firn/core-flatpaks.json`.
  The snosi ISO build copies first-setup's `core.json` there. The fallback is
  the normal path for every desktop image.
- The fallback does not depend on the image being installed. With
  `core_flatpaks = true`, Sundog gets Snow's GNOME list, and so does Floe,
  a headless server.
- The TUI offers the core-flatpaks toggle for every catalog image, including
  Floe, and cannot show which apps it would install.
- first-setup does not fit Sundog and has no place in Floe, so it cannot be
  the source of every image's list.
- Firn already runs `skopeo inspect docker://<ref>` to resolve the image
  digest it verifies (`internal/bootcimg/verify.go`). That output includes
  the image config's labels without fetching any layer. OCI sets no size
  limit on a label value. containers/image limits the config blob to about
  4 MiB, and a core list is a few KB.

## Decision

**Each image publishes its own core flatpak set as an OCI label, and firn
reads the set only from that label.**

1. **Source in snosi.** Snosi keeps one file per image that has a core set:
   `flatpaks/snow.json`, `flatpaks/snowfield.json` and `flatpaks/sundog.json`.
   Floe has no file. Each entry carries an app ID and a display name.
2. **Label.** The image build stamps the file's compact JSON into the label
   `org.frostyard.core-flatpaks`, for example
   `--label "org.frostyard.core-flatpaks=$(jq -c . flatpaks/snow.json)"`.
   An image without a file carries no label.
3. **Value format.** The label value is versioned JSON:

   ```json
   {"version":1,"flatpaks":[{"id":"org.mozilla.firefox","name":"Firefox"}]}
   ```

   Firn parses it fail-closed, like the recipe: an unknown `version`, an
   unknown field, a missing or empty `id`, or malformed JSON is an error.
   Duplicate IDs are dropped in order.
4. **Install.** When `core_flatpaks = true`, firn takes the set from the
   label in the config of the digest it verified, not from an earlier
   preview. The set joins the recipe's explicit `flatpaks` and follows
   ADR-0006's offline-first provisioning unchanged. An image without the
   label installs no core set and keeps the existing `no_core_set` warning
   and summary item.
5. **Retired sources.** Firn stops reading first-setup's `core.json` path
   and `/usr/share/firn/core-flatpaks.json`. There is no fallback to another
   image's list.
6. **TUI.** After an image is chosen, the wizard inspects it. With no label,
   it does not offer the core-flatpaks toggle. With a label, it lists the
   apps by name beside the toggle. When the inspect fails (for example,
   offline with no local copy of the image), the wizard keeps the toggle and
   says the list will be read at install time.
7. **Recipe.** The recipe key stays `core_flatpaks`; its meaning narrows to
   "the set the image's label publishes". The recipe version does not change.

This ADR replaces ADR-0006's source for the core set, not its provisioning
mechanism. When accepted, ADR-0006 gains a status note pointing here.

## Consequences

- Each image owns its list. Sundog and other non-GNOME images get a fitting
  set, and Floe gets none, without changes to first-setup or firn.
- The composefs problem disappears: firn never reads the image's `/usr` for
  the list.
- The ISO no longer needs `/usr/share/firn/core-flatpaks.json`.
- The TUI can show the set before install, and hides an option that would
  do nothing.
- The label is part of the image config, so the cosign-verified manifest
  digest covers it.
- **Label inheritance.** An image built `FROM` another image inherits its
  label. Snosi must clear the label on images without a file and check in CI
  that each built label matches its file. Without that check, a server image
  could silently inherit a desktop list.
- **The booted system cannot easily read the label.** Anything on the
  installed system that needs the list, such as first-setup's core menu on
  Snow, needs its own copy. Snosi may generate first-setup's `core.json`
  from the same file, transforming it into the `{"core":[{"id":…}]}` shape
  first-setup expects.
- **Offline seeding.** ADR-0006 obliges installer ISOs to seed the core set.
  The ISO build should seed each carried image's set, read from the same
  label, instead of one GNOME set.
- **Rollout order.** Snosi images must carry the label before firn stops
  reading the old paths. Until then, a new firn offers no core set for an
  unlabeled image. That is preferable to installing the wrong list.
- Firn gains a second contract with snosi. It needs its own spec and a
  `version` field so the format can change without guessing.

## Alternatives considered

- **Keep first-setup's `core.json` as the source:** it is GNOME-oriented,
  Sundog does not fit it, and Floe has no first-setup; rejected.
- **Key the ISO fallback by catalog image (`/usr/share/firn/core-flatpaks/<name>.json`):**
  fixes the wrong-image bug, but the ISO, not the image, owns the list, and
  the list can drift from the image it describes; rejected.
- **A known file path in the image, read from the pulled image before
  deploy:** works around composefs, but needs layers on disk, so the TUI
  cannot preview the set before the pull; rejected in favor of the label.
- **List the set in the catalog JSON:** the catalog and an override catalog
  would both have to track each image's list, and headless recipes do not
  use the catalog; rejected.
- **Rename the recipe key to `default_flatpaks`:** a breaking recipe change
  with no behavior gain; rejected.

## References

- Shapes: [design/architecture.md](../design/architecture.md) (system
  configuration, flatpaks), [specs/recipe-schema.md](../specs/recipe-schema.md)
  (`core_flatpaks`), [specs/progress-protocol.md](../specs/progress-protocol.md)
  (`no_core_set`), a new core-flatpaks label spec created with the
  implementation, [roadmap Phase 10](../plans/roadmap.md#phase-10)
- Builds on: [ADR-0006](0006-install-time-offline-first-flatpaks.md)
  (offline-first provisioning, whose core-set source this replaces),
  [ADR-0010](0010-single-installer-iso-in-snosi.md) (snosi owns the ISO and
  its seeded flatpaks), [ADR-0012](0012-bootc-install-from-ram-installer.md)
  (the composefs fallback this retires),
  [ADR-0016](0016-bootc-only-recipe-contract.md) (the version-2 recipe
  carrying `core_flatpaks`)
