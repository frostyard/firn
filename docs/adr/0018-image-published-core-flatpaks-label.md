# 0018 — Read each image's core flatpak set from an OCI label

- **Status:** Accepted
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

1. **Source in snosi.** Snosi keeps one file per distinct core set:
   `flatpaks/snow.json` and `flatpaks/sundog.json`. Snowfield's build uses
   `flatpaks/snow.json` until it needs a list of its own. Floe has no file.
   Each entry carries an app ID and a display name. Snosi validates each file
   against the rules in item 3 before packaging.
2. **Label.** The image build stamps the file's compact JSON into the label
   `org.frostyard.core-flatpaks`, for example
   `--label "org.frostyard.core-flatpaks=$(jq -c . flatpaks/snow.json)"`.
   An image without a file carries no label key. Snosi CI checks the label
   on the final pushed image, comparing parsed JSON with the source file,
   and checks that the key is absent on images without a file.
3. **Value format.** The label value is versioned JSON:

   ```json
   {"version":1,"flatpaks":[{"id":"org.mozilla.firefox","name":"Firefox"}]}
   ```

   Firn parses it fail-closed, like the recipe:

   | Label content | Result |
   | --- | --- |
   | Key absent | No core set |
   | Empty string | Malformed |
   | Not a single JSON object (invalid JSON, trailing data) | Malformed |
   | `version` missing, null, or not `1` | Malformed |
   | `flatpaks` missing or null | Malformed |
   | Any field other than `version`, `flatpaks`, and per entry `id`, `name` | Malformed |
   | `id` missing, empty, or not a valid Flatpak application ID | Malformed |
   | `name` missing or empty | Malformed |
   | `flatpaks` is an empty array | No core set |
   | Duplicate `id` | Later duplicates dropped; first occurrence kept |

   A valid Flatpak application ID has at least three dot-separated
   elements, each of `[A-Za-z0-9_-]` and not starting with a digit, with
   `-` only in the last element, and is at most 255 characters. IDs are
   validated because they become `flatpak install` arguments. Snosi CI
   rejects an empty array: an image with no core set publishes no label.
4. **When firn reads it.** `preflight-image`, which already inspects the
   image before any disk write, reads the label from the image it selects:
   the local copy when one is selected, otherwise the registry image. It
   parses the label only when `core_flatpaks = true` and keeps the parsed set
   for the flatpak step. With `core_flatpaks = true`:
   - a malformed label fails preflight, before partitioning;
   - no core set keeps the existing `no_core_set` warning and summary item,
     and the install continues.

   With `core_flatpaks = false`, firn ignores the label, including a
   malformed one. Network reachability is a separate pre-install check
   ([firn#105](https://github.com/frostyard/firn/issues/105)).
5. **Which image the set describes.** Signed recipes deploy the digest
   preflight verified, so the set always matches the installed image.
   Unsigned recipes deploy the tag, which bootc resolves again when it
   pulls; if the tag moves during the install, the deployed image can be a
   newer build than the label firn read. Every catalog entry is signed, so
   this affects only headless recipes without `cosign_pub_key`. Whether
   unsigned installs should pin is an open question
   ([firn#109](https://github.com/frostyard/firn/issues/109)).
6. **Install.** The flatpak step merges the preflight set with the recipe's
   explicit `flatpaks`, keeping the first occurrence of each ID, and
   provisions them through ADR-0006's offline-first mechanism, unchanged.
7. **Retired sources.** Firn stops reading first-setup's `core.json` path
   and `/usr/share/firn/core-flatpaks.json`. There is no fallback to another
   image's list.
8. **TUI.** After an image is chosen, the wizard inspects it, with a
   timeout, and inspects again when the user picks a different image. It
   distinguishes four states:
   - inspect failed (for example, offline with no local copy): keep the
     toggle and say the list will be read at install time;
   - no core set: do not offer the toggle;
   - malformed label: do not offer the toggle, and say why;
   - valid set: list the apps by name beside the toggle.

   Hiding the toggle clears its value, so a recipe never carries
   `core_flatpaks = true` for an image whose toggle was hidden. The explicit
   flatpaks field stays available. The preview is never written into the
   recipe; preflight reads the label again at install.
9. **Recipe.** The recipe key stays `core_flatpaks`; its meaning narrows to
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
- The label is part of the image config, so for signed recipes the
  cosign-verified manifest digest covers it.
- **Label inheritance.** An image built `FROM` another image inherits its
  labels. Snosi product images are built `FROM scratch`, so nothing is
  inherited today; the CI check that the key is absent on images without a
  file guards against a future derived image. Snosi does not clear the
  label: its secure packaging commits the final image from the first
  candidate, and clearing would remove the label it must keep.
- **The booted system cannot easily read the label.** Anything on the
  installed system that needs the list, such as first-setup's core menu on
  Snow, needs its own copy. Snosi generates first-setup's `core.json` from
  `flatpaks/snow.json`, transforming it into the `{"core":[{"id":…}]}` shape
  first-setup expects, and checks it for drift. Otherwise the next
  re-vendor of first-setup's list would overwrite the new source.
- **Offline seeding is out of scope.** Published installer ISOs carry no
  flatpak seed today, so installs download the set. ADR-0006's provisioning
  copies the medium's whole `/var/lib/flatpak`, so a locally built seeded
  ISO still puts its GNOME seed on any image it installs. That is existing
  behavior, unchanged here. Installing only the requested apps from a
  medium is a separate problem with its own decision.
- **Fail before writing.** A malformed label with `core_flatpaks = true`
  stops the install before any disk write. The TUI never offers the toggle
  for such an image, so only headless recipes can reach that error.
- **Saved recipes change meaning.** A saved Sundog or Floe recipe with
  `core_flatpaks = true` installs that image's set, or none, instead of the
  GNOME list. The release notes say so.
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
  (`no_core_set`, `core_flatpaks_label_invalid`),
  [specs/core-flatpaks-label.md](../specs/core-flatpaks-label.md) (the label
  contract), [roadmap Phase 10](../plans/roadmap.md#phase-10)
- Builds on: [ADR-0006](0006-install-time-offline-first-flatpaks.md)
  (offline-first provisioning, whose core-set source this replaces),
  [ADR-0010](0010-single-installer-iso-in-snosi.md) (snosi owns the ISO and
  its seeded flatpaks), [ADR-0012](0012-bootc-install-from-ram-installer.md)
  (the composefs fallback this retires),
  [firn#105](https://github.com/frostyard/firn/issues/105) (pre-install
  network check), [firn#109](https://github.com/frostyard/firn/issues/109)
  (unsigned digest pinning),
  [ADR-0016](0016-bootc-only-recipe-contract.md) (the version-2 recipe
  carrying `core_flatpaks`)
