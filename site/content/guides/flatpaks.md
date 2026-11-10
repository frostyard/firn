---
description: Select the image's core Flatpak set and extra apps, and understand offline and metadata failures.
---

# Flatpaks

firn provisions Flatpaks into the target during installation. It first uses the installer's seeded Flatpak repository when present, then downloads missing requested apps. It does not defer them to a firn first-boot service.

!!! note "Published ISOs need downloads"

    Published installer ISOs carry no Flatpak seed. Their core apps and runtimes therefore need network downloads during installation. Offline-first support does not mean every medium contains the apps you choose.

## Select the image's core set

Each image can publish its own app list in the OCI label `org.frostyard.core-flatpaks`. The list belongs to the chosen image, not to firn or another desktop's catalog.

Both wizards preview that list on the apps page:

- A valid set shows app names and an initially enabled toggle.
- No set shows an explanation and no toggle.
- A malformed set shows an explanation and no toggle.
- Failed inspection keeps the toggle, initially enabled, with a note that metadata will be read at install time.

For the same image, revisiting the page keeps your answer. Choosing another image applies its initial selection. Previewing is not signature verification; preflight selects and checks the source again before installation.

## Write a headless app selection

These fields belong inside the recipe's existing `[system]` table:

```toml
[system]
hostname = "frost01"
core_flatpaks = true
flatpaks = ["org.mozilla.firefox"]
```

`core_flatpaks` defaults to false in hand-written recipes. The wizard's initially enabled toggle does not change that schema default. Explicit app IDs are installed first, followed by the published core set. Duplicate IDs are installed once, at their first occurrence.

Extra apps in the terminal wizard accept comma, space or newline-separated application IDs. Use app IDs, not display names.

## Understand failures and omissions

With `core_flatpaks = true`, malformed labels fail preflight with `core_flatpaks_label_invalid`, and unreadable metadata fails with `core_flatpaks_label_unreadable`. No destructive step has run at that point. An image with no core set instead produces `no_core_set` and continues.

With `core_flatpaks = false`, firn does not parse the label, so a malformed core list cannot affect the install. This is an explicit choice to omit that set, not a repair of the image metadata.

If a requested app cannot be reached, `flatpak_unreachable` appears in warnings and the final summary. Read the summary even when the installation succeeds. firn reports the missing apps rather than silently dropping them.

A locally built seeded medium can copy its whole Flatpak seed into the target, not only the requested IDs. This existing behavior means a seeded desktop medium can also supply apps to a different selected image.

The [core Flatpaks label specification](https://github.com/frostyard/firn/blob/main/docs/specs/core-flatpaks-label.md) defines metadata validation. [ADR-0018](https://github.com/frostyard/firn/blob/main/docs/adr/0018-image-published-core-flatpaks-label.md) records the published-media and seeding limits.
