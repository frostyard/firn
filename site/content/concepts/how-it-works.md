---
description: How recipes, preflight, bootc deployment and shared progress connect the three installation interfaces.
---

# How firn works

firn is a one-time installer for snosi bootc OCI images. The terminal wizard, graphical installer and headless command all use the same TOML recipe contract and installation pipeline.

## Recipes are the input

A version-2 recipe names the image, whole target disk, filesystem, explicit security choices and system settings. The interactive frontends generate that recipe for you. The review page's accepted TOML is the exact persisted input, not a different approximation of the choices.

Validation is fail-closed. Unknown fields, unsupported enum values and wrong-family fields are errors. The current contract is bootc-only; A/B recipes are not converted into bootc recipes.

[The recipe reference](../reference/recipe.md) summarizes the fields and compatibility window.

## Preflight comes before disk writes

After validation, firn assembles the ordered steps for your choices. [Preflight](../guides/preflight.md) checks UEFI, required host tools, the image and the target disk. Encryption, TPM2, Secure Boot and Flatpaks add work to that pipeline as needed.

A dry-run runs only preflight. A real install requires the exact disk path as confirmation and takes an exclusive machine-wide lock. Only 1 install can write disks at a time.

## The bootc deployment

firn creates a GPT disk layout and the chosen root filesystem. If requested, it creates LUKS root encryption and opens the mapped device. It then runs `bootc install to-filesystem`, directly or through podman.

On an all-in-RAM installer, the image store and blob scratch need disk space. firn redirects that temporary storage onto the target disk during installation. It keeps the containers-storage transport rather than exporting to an OCI layout. A target needs temporary space for the store and deployment together, not only the final installed system.

System configuration is written into the bootc deployment: hostname, region settings, users, groups and SSH public keys. Requested Flatpaks are provisioned into the target during installation. TPM2 enrollment uses the deployed UKI's signed PCR 11 policy. The Secure Boot path stages the image's boot chain and MOK request.

## Progress and cleanup

Every pipeline signal uses the same [progress event model](../reference/progress.md). Both interactive frontends receive it in-process. `--json-progress` serializes it as NDJSON for external automation.

Steps that mount filesystems or open mappings register cleanup actions. Cleanup runs on success, failure and cancellation before the final summary and terminal event. It releases resources; it does not undo a partition table or filesystem that was already written.

firn does not install a firn first-boot provisioning service. The installed image owns runtime updates and bootloader reconciliation. Image updates follow `target_ref`, or the original image reference when that field is empty, rather than the immutable digest selected for installation.

For maintainer details, see the [architecture](https://github.com/frostyard/firn/blob/main/docs/design/architecture.md).
