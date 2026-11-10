---
description: Boot the snosi installer ISO or install the firn command on Debian trixie.
---

# Install firn

The snosi installer ISO includes firn and the host tools its installation pipeline needs. Boot it on the machine you intend to install. firn requires UEFI and runs as root from live media; it does not support legacy BIOS boot.

## Use the installer ISO

Get the installer medium through [frostyard/snosi](https://github.com/frostyard/snosi), which builds and publishes the ISO. firn supplies the installer binaries, not the media.

The ISO offers 2 boot entries:

| Entry | Installer |
| --- | --- |
| Console | Opens the [terminal wizard](../guides/terminal-wizard.md), including on serial consoles. |
| Graphical | Opens [firn-gui](../guides/graphical-installer.md) full screen under cage. It falls back to the terminal kiosk when no usable display is available. |

Both frontends create a recipe and run the same pipeline. The graphical entry does not select a different image family or a different installation method.

!!! warning "Prepare the target"

    Save all data you need from the target disk elsewhere. Check its model, serial number and size before you confirm an install. A real install replaces the disk's partition table and filesystems.

Continue with [your first install](first-install.md).

## Install the CLI package

On Debian trixie with the Frostyard APT repository already configured at `https://repository.frostyard.org/debian/`:

```sh
sudo apt install frostyard-firn
```

This installs the `firn` command. Installing the package alone does not supply an installer environment or make it safe to overwrite a running system. Use live media and let [preflight](../guides/preflight.md) check the tools required by your recipe.

`frostyard-firn-gui` is for installer media, not installed systems. Do not add it to an installed desktop image. See the [graphical installer design](https://github.com/frostyard/firn/blob/main/docs/design/architecture.md#graphical-installer-gui) for the package boundary.

For a local build, use [build from source](../contributing/build.md).

## Check the environment

From a live-media root shell, `firn` opens the terminal wizard. Its welcome page shows UEFI, Secure Boot and TPM detection. Without a TPM, passphrase-only encryption and unencrypted installs remain available.

For automation, prepare a recipe and run:

```sh
firn validate recipe.toml
firn install --dry-run recipe.toml
```

Validation checks the recipe against this machine. Dry-run also runs the pipeline's preflight checks without changing a disk. Neither command is a trial install that proves the resulting disk will boot. See [headless installation](../guides/headless-install.md).
