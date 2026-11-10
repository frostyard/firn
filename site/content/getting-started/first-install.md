---
description: Prepare a disk, choose an installer, review its recipe and complete the first boot.
---

# Your first install

Use this checklist for an interactive install from the [snosi installer ISO](install.md). For automation, follow [headless install with a recipe](../guides/headless-install.md).

## Before you boot

- Back up the target disk. firn erases the whole disk; it does not offer a dual-boot partition editor.
- Identify the intended disk by model, serial number and size, not just its `/dev/` name.
- Decide whether you need a [LUKS passphrase or TPM2 auto-unlock](../guides/disks-and-encryption.md).
- If Secure Boot is active, prepare to complete [MOK enrollment](../guides/secure-boot.md) at the next boot.
- Arrange registry access if the chosen image is not cached on the medium. Published installer ISOs carry no Flatpak seed, so requested apps need downloads.

## Choose an installer

Boot the console entry for the terminal wizard or the graphical entry for `firn-gui`. On a live-media root shell, `firn` starts the terminal wizard.

Follow the [terminal walkthrough](../guides/terminal-wizard.md) or the [graphical walkthrough](../guides/graphical-installer.md). Both collect:

1. The bootc image and optional advanced image settings.
2. The target disk and root filesystem.
3. Encryption and, when Secure Boot is active, MOK enrollment choices.
4. Hostname, optional region settings and SSH public keys.
5. A user account and supplementary groups, if wanted.
6. The image's core Flatpaks and any extra app IDs.

The terminal wizard starts at btrfs without subvolumes and no encryption. These are initial selections, not recommendations for your data. Check every security choice explicitly. The graphical installer starts with the same choices.

## Review and confirm

The review page shows the TOML recipe that will drive the install. Check the disk identity, image, filesystem and security fields. User and encryption passwords are represented by private file paths, not plaintext in the recipe.

To start, type the exact target disk path requested by the confirmation screen. The pipeline then runs preflight before any destructive step. A failed preflight stops the install; use the [failure message and stable code](../reference/troubleshooting.md) to correct the cause.

!!! warning "Cancellation does not restore data"

    Once destructive steps start, stopping the pipeline only stops further work and runs cleanup. It does not restore the previous partition table, filesystems or operating system.

## Finish and boot

Read the final summary before leaving the installer. Warnings can report apps that were not reachable or groups absent from the image.

If a recovery key appears, store it securely before acknowledging it. The interactive installer shows it only once and does not repeat it into ordinary logs.

After a successful install, boot from the target disk. If MOK enrollment was staged, complete MokManager with the one-time password you entered. With passphrase-only LUKS, enter your encryption passphrase when asked at boot.

The installer prints a headless reproduction command. Its recipe and secret files remain in a private session directory below `/run/firn` until the live environment reboots. They are not a permanent backup. Protect them if you retain a copy, and review them before any later destructive run.
