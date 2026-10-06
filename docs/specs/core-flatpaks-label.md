# Spec: Core Flatpaks image label (version 1)

This contract governs the `org.frostyard.core-flatpaks` OCI image label: the
core Flatpak set an image publishes for firn to install when a recipe sets
`core_flatpaks = true`. Snosi produces it from `flatpaks/` in every image
build; firn's `preflight-image` step consumes it (`internal/flatpak`
`ParseCoreLabel`, `internal/steps/preflight.go`). It replaces first-setup's
`core.json` and the installer ISO's `/usr/share/firn/core-flatpaks.json` as
the source of the core set ([ADR-0018](../adr/0018-image-published-core-flatpaks-label.md)).

## Interface

The label value is a single JSON object:

```json
{"version":1,"flatpaks":[{"id":"org.mozilla.firefox","name":"Firefox"}]}
```

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `version` | integer | yes | Exactly `1`. |
| `flatpaks` | array of objects | yes | May be empty (no core set). |
| `flatpaks[].id` | string | yes | Flatpak application ID: at least three dot-separated elements of `[A-Za-z0-9_-]`, none starting with a digit, `-` only in the last element, at most 255 characters. |
| `flatpaks[].name` | string | yes | Display name; not empty or whitespace only. |

Field names are exact and case-sensitive. An image with no core set
publishes no label key.

## Rules

1. Firn parses the label fail-closed. Every row below other than "No core
   set" and "Accepted" is **malformed**:

   | Label content | Result |
   | --- | --- |
   | Key absent | No core set |
   | Empty string | Malformed |
   | `null`, or not a single JSON object (invalid JSON, trailing data) | Malformed |
   | `version` missing, null, not an integer, or not `1` (`1.0` and `true` are not `1`) | Malformed |
   | `flatpaks` missing or null | Malformed |
   | Any field other than `version`, `flatpaks`, and per entry `id`, `name`, including case variants such as `VERSION` or `Id` | Malformed |
   | An entry that is null, or whose `id` is missing, empty, or not a valid application ID | Malformed |
   | `name` missing, empty, or whitespace only | Malformed |
   | `flatpaks` is an empty array | No core set |
   | Duplicate `id` | Accepted; later duplicates dropped, first occurrence kept |

2. Firn reads the label in `preflight-image`, before any disk write, from
   the image that step selects: the local containers-storage copy when it is
   selected, otherwise the registry image. For a signed recipe that is the
   cosign-verified digest firn deploys. For an unsigned recipe bootc resolves
   the tag again when it pulls, so a tag that moves during the install can
   deploy a newer build than the label described
   ([firn#109](https://github.com/frostyard/firn/issues/109)).
3. With `core_flatpaks = true`:
   - a malformed label fails `preflight-image` with error code
     `core_flatpaks_label_invalid`, in dry-run and real runs alike;
   - labels that could not be read fail `preflight-image` too. A signed
     `@sha256:` reference can verify while its registry inspection failed
     and no matching local copy exists; its labels are then unknown, not
     absent, and the install stops rather than report `no_core_set`;
   - no core set emits the `no_core_set` warning and summary item, and the
     install continues;
   - otherwise the set's IDs are installed after the recipe's explicit
     `flatpaks`, each ID once at its first occurrence, through ADR-0006's
     offline-first provisioning.
4. With `core_flatpaks = false`, firn does not parse the label; a malformed
   label cannot affect the install.
5. A new format increments `version`. Firn releases that do not know a
   version treat its label as malformed.
6. Producers must not publish an empty `flatpaks` array or an empty-string
   value; an image without a core set carries no label key.
7. The TUI wizard previews the label on its flatpaks page, inspecting the
   chosen image with rule 2's local-first selection (no signature
   verification) under a 30-second timeout, and again when the chosen image
   changes. It:
   - lists a valid set's names beside the `core_flatpaks` toggle;
   - shows no toggle, and an explanation, for no core set or a malformed
     label, and sets `core_flatpaks` to false;
   - keeps the toggle, noting the list is read at install time, when
     inspection fails or returns no readable metadata, and inspects again on
     the next visit.

   The preview is display-only and never written into the recipe.

## Derived artifacts

| Artifact | Derivation |
| --- | --- |
| Snosi `flatpaks/*.json` and `flatpaks/core-flatpaks.py` | Produce the label per product and check it on every packaged and pushed image; snosi's `test/core-flatpaks-test.sh` mirrors rule 1's rejections. |
| Progress codes `no_core_set`, `core_flatpaks_label_invalid` | [Progress protocol](progress-protocol.md#stable-codes). |
| `core_flatpaks` recipe field | [Recipe schema](recipe-schema.md). |

## References

- Rationale: [ADR-0018](../adr/0018-image-published-core-flatpaks-label.md),
  [ADR-0006](../adr/0006-install-time-offline-first-flatpaks.md)
- Context: [design/architecture.md](../design/architecture.md) (system
  configuration, flatpaks)
