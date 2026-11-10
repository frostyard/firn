---
description: Walk through the terminal installer's image, storage, security, system and review pages.
---

# Terminal wizard

Run `firn` as root from live media, or select the installer ISO's console boot entry. The wizard builds a recipe, shows it for review, and runs the installation pipeline in the same process.

## Move between fields and pages

Follow the key hints in each form. Press Enter on the welcome page to begin. Shift+Tab moves backwards through fields; at a page's first field it returns to the previous page. Esc or Ctrl+C quits during recipe collection.

An invalid value blocks forward progress, but does not prevent backwards navigation to correct an earlier answer.

## Choose the image

The welcome page shows UEFI, Secure Boot and TPM capabilities, plus catalog notices. Choose a bootc image on the next page. The built-in catalog offers snow, snowfield and floe; the installer medium can supply a replacement catalog at `/etc/firn/catalog.json`.

Advanced image options initially stay off. Enable them to change the post-install update reference (`target_ref`) or the bootloader. systemd-boot is the default; GRUB 2 is offered only when Secure Boot is inactive. Image identity and its cosign key come together from the catalog, not from a separate free-text image field.

## Select storage

Choose a whole disk. The picker shows available vendor, model, serial, WWN, transport, size and filesystem labels. Refused disks remain visible with their reasons. Use **Rescan disks** after changing device availability or mount state.

Choose btrfs, xfs or ext4 for the root filesystem. btrfs starts selected; its optional subvolumes are `@`, `@home` and `@snapshots`. ZFS is not offered because the bootable installation path is incomplete.

See [disks and encryption](disks-and-encryption.md) for layouts and refusal rules.

## Set security

Choose encryption explicitly. The initial selection is `none`. Passphrase modes ask for the passphrase twice. TPM2 modes are unavailable when no TPM is detected.

When Secure Boot is active, choose MOK enrollment or skip. Enrollment starts selected and asks for a one-time MokManager password twice. Skip is for operators who manage the boot trust themselves, not a way to make an untrusted boot chain work. Read [Secure Boot](secure-boot.md) before choosing.

## Configure the system and account

Enter a hostname. Locale, timezone and keyboard are optional; leaving them empty keeps the image default. The keyboard field uses `LAYOUT[:VARIANT[:MODEL]]`, such as `us` or `de:nodeadkeys`.

You can paste root SSH public keys. Each non-empty line must be an OpenSSH public-key record; do not paste a private key or `authorized_keys` options.

User creation starts enabled. Enter the username, optional full name, password and confirmation, then select supplementary groups. The catalog can preselect groups; without catalog defaults, `sudo` is selected. Groups absent from the installed image are skipped and reported. Additional groups accept commas or spaces. User SSH public keys are optional.

Without a created user, arrange a supported way to log in, such as the configured root SSH key. Do not assume that omitting a user creates an account automatically.

## Choose apps

The [Flatpaks page](flatpaks.md) previews the chosen image's core set. When offered, the toggle starts enabled. No set or a malformed set gets an explanation instead. If inspection fails, the toggle stays available and preflight retries the metadata read at install time.

Extra Flatpak application IDs accept commas, spaces or newlines.

## Review, confirm and watch

The review page shows the exact recipe to install. Choose **Install**, **Start over**, or **Quit without installing**. Install asks you to type the disk path exactly before the pipeline starts.

The progress view shows steps, warnings with stable codes and the final summary. Ctrl+C during installation cancels the pipeline and keeps the view open while cleanup runs. It cannot undo disk changes. The final screen remains until you dismiss it.

For TPM-only encryption, save the displayed recovery key and press Enter to acknowledge it. The key is cleared after acknowledgement. The saved recipe and secret files stay in the run's private `/run/firn` session until reboot; see [headless reuse](headless-install.md#reuse-a-wizard-recipe).
