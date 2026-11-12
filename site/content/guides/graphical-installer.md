---
description: Use firn-gui, its keyboard shortcuts, capability overrides and demo mode.
---

# Graphical installer

`firn-gui` is a separate graphical wizard that generates the same recipe and runs the same pipeline as the terminal installer. The snosi installer ISO's graphical boot entry runs it full screen as root. With no usable display, that entry falls back to the terminal kiosk.

On live desktop media that include it, run:

```sh
firn-gui
```

Without root, it re-runs itself through `pkexec`. The packaged policy requires an administrator's password in an active local session. The package is for installer media only; it must not be included in installed images.

## Try the pages without installing

```sh
firn-gui --demo
```

Demo mode runs without root, uses simulated disks and progress, and writes nothing to disk. It does not test your actual storage or prove that an image will boot.

To choose an appearance explicitly:

```sh
firn-gui --demo --appearance dark
```

`--appearance` accepts `light` or `dark`; the default follows the desktop. Either way the window is drawn in frostyard's look, set in Inter. It follows the desktop's text size and high-contrast setting, but not its accent color.

## Walk through the pages

| Page | What to check |
| --- | --- |
| Welcome | Machine capabilities and catalog notices. |
| System | The image; optional advanced update reference and bootloader. GRUB 2 is offered only with Secure Boot inactive. |
| Storage | The disk's path, size, transport, labels, serial and WWN; refusal reasons; filesystem and optional btrfs subvolumes. |
| Security | Encryption, passphrase confirmation, and MOK enrollment when Secure Boot is active. TPM modes need a TPM. |
| Region | Hostname, locale, timezone, keyboard and optional root SSH public keys. |
| Account | User, password, groups and optional user SSH public keys. |
| Apps | The image's core Flatpaks preview and extra app IDs. |
| Review | The exact TOML recipe, disk identity and validation issues. |
| Install | Progress, warnings, recovery-key acknowledgement and outcome. |

Optional locale, timezone and keyboard fields can start with valid values from the running session. The hostname can start from the image name. Change or clear these values as needed; empty optional fields keep the image default.

Rescanning disks never switches the chosen disk automatically. If the chosen disk disappears or becomes refused, the selection clears and a notice explains why.

## Keyboard shortcuts

| Shortcut | Action |
| --- | --- |
| Alt+Right / Alt+Left | Continue / go back; mouse forward/back also work. |
| Ctrl+Enter | Continue, or open installation confirmation from Review. |
| Alt+1 through Alt+8 | Jump to a reached step whose predecessors are complete. |
| Tab / Shift+Tab | Next / previous control. |
| Arrow keys | Choose between cards and options. |
| Space | Select a card or toggle a switch or check box. |
| Ctrl+R or F5 | Rescan disks on Storage. |
| F1 or Ctrl+/ | Show shortcuts. |
| Ctrl+Shift+L | Switch light/dark appearance. |
| Esc | Close an ordinary dialog; it cannot bypass recovery-key acknowledgement. |
| Ctrl+Q | Quit; asks during recipe collection and is refused during an install. |

## Correct capability detection

`--secure-boot`, `--tpm` and `--uefi` each accept `auto`, `on` or `off`. Use overrides only when you know why detection is wrong, such as a controlled VM environment.

```sh
firn-gui --tpm off
```

These flags change firn's probes, not the hardware or firmware. `--uefi on` does not add BIOS support, and `--secure-boot off` does not disable firmware Secure Boot.

The GUI accepts flags only. A recipe path or any other positional argument is an error. Use [headless installation](headless-install.md) for an existing recipe.

## Confirm and finish

Review the recipe, then type the exact disk path in the confirmation dialog. This erases the selected disk once destructive steps begin. **Stop** cancels the running pipeline and lets it clean up; it does not restore earlier data. The window refuses to close during an install.

A recovery key opens a blocking acknowledgement dialog. Write it down securely before confirming. It is not selectable and has no Copy button, so it does not enter the desktop clipboard. The installer clears it after acknowledgement.

After completion, read warnings and the summary. The printed headless command refers to a private session below `/run/firn`, retained until reboot. See [troubleshooting](../reference/troubleshooting.md) for failures and diagnostics.
