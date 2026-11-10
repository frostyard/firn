---
description: Common validation and preflight failures, stable progress codes, log locations and reporting rules.
---

# Troubleshooting

Start with the failing step, stable code and message. A preflight failure occurs before destructive work. A later failure or cancellation can leave an incomplete installation; cleanup does not recover the previous system.

## Common preflight failures

| Symptom | What to check |
| --- | --- |
| UEFI required | Boot the live medium in UEFI mode. Legacy BIOS is unsupported. `--uefi on` is not a firmware conversion. |
| Required tools not found on `PATH` | Use the snosi installer ISO or supply the reported host tools in your live environment. Requirements depend on the recipe's assembled steps. |
| Disk not found or not a whole disk | Check the actual disk path and by-id link. A partition is not a valid target. |
| Disk refused as mounted | Inspect all mounted descendants, including mapped devices. Unmount only after confirming you do not need the data and are not using the running system's disk. |
| RAID or LVM refusal | The disk or a descendant is an array member or physical volume. Do not bypass this by erasing signatures without a separate data-preservation plan. |
| Installer medium refused | Choose the intended target, not the recognized boot medium. |
| TPM mode rejected | Use a machine with the required TPM, or explicitly choose passphrase-only or no encryption if that meets your requirements. No silent fallback occurs. |
| Missing MOK choice or password file | With Secure Boot active, set `mok`; enrollment also needs an existing private `mok_password_file`. |
| Secret file rejected | The file must exist, be regular and not world-readable. Use 0600 and check the path in the live environment. |

`firn validate` checks recipe and machine constraints, not the pipeline's tools, image or disk checks. Run `firn install --dry-run recipe.toml` for [preflight](../guides/preflight.md) without disk writes.

## Progress codes

| Code | Meaning and next step |
| --- | --- |
| `network_unreachable` | Image resolution/verification and a registry probe failed with no routable local address. Check interface and network configuration. |
| `registry_unreachable` | Connectivity evidence points to the network, proxy or registry. Read the named cause, check access and retry. |
| `image_verification_failed` | Digest resolution or cosign verification failed. Check the image reference, matching public key and full diagnostic; do not bypass trust to make it pass. |
| `image_verification_retried` | A bounded verification retry follows. Verification is not weakened; wait for the final result. |
| `core_flatpaks_label_invalid` | Requested core metadata is malformed. The image needs corrected metadata, or you must explicitly choose not to install its core set. |
| `core_flatpaks_label_unreadable` | Requested core metadata could not be read. Check registry access and retry; unreadable is not the same as absent. |
| `no_core_set` | The image publishes no core set. Installation continues with no core apps; explicit app IDs still apply. |
| `flatpak_unreachable` | A requested app was not installed. Read the final summary for which apps are missing. |
| `group_missing` | A requested supplementary group does not exist in the image and was skipped. |
| `no_tpm` | No TPM was detected. Only choices that do not require it are valid. |
| `install_in_progress` | Another real install holds the machine lock. No step ran in this attempt. Let the other install finish or cancel it through its own frontend. |
| `recipe_v1_deprecated` | Migrate an accepted bootc-v1 recipe before the cutoff in the [recipe reference](recipe.md#bootc-v1-compatibility). |
| `cleanup_failed` | Resource cleanup failed; the run is unsuccessful. Inspect the diagnostic before trying again. |
| `store_cleanup_failed`, `store_umount_failed` | Cleanup or unmount of the redirected container store failed. Inspect the diagnostic and remaining resources. |
| `stream_truncated` | The producer ended without a terminal event. Treat the install as failed, not as user cancellation. |
| `step_failed` | A step failed without a more specific stable code. Read its message and captured host-command error. |

TLS trust, registry authorization, missing manifests and cosign verdicts are not automatically network failures. Keep their original diagnostic when reporting a problem. The [full protocol table](https://github.com/frostyard/firn/blob/main/docs/specs/progress-protocol.md#stable-codes) defines the precise distinctions.

## Boot and GUI problems

Firmware rejects the installed boot chain
:   Check whether [MOK enrollment](../guides/secure-boot.md) completed in MokManager, and whether the image supports the secure-install chain. Staging enrollment is not the same as completing it.

TPM auto-unlock fails
:   Use the recovery material for the mode you selected: the generated recovery key for `tpm2-luks`, or your fallback passphrase for `tpm2-luks-passphrase`. Keep those outside the encrypted system.

The GUI does not open
:   It needs a usable graphical session and GTK 3, Pango and Cairo. Unprivileged installation also needs `pkexec`; the package permits elevation only in an active local session. A desktop can refuse root clients. Use the terminal wizard from live media when graphics are unavailable.

The GUI refuses update leftovers beside its executable
:   Use the packaged layout, with the real executable alone in `/usr/libexec/firn-gui/`. Do not place it directly in a shared directory such as `/usr/bin`; see the [trust model](../concepts/trust.md).

A small disk runs out of space during image deployment
:   A RAM-media install temporarily keeps the unpacked image store and deployment together on the target. A preflight pass is not a capacity guarantee. Use a target with sufficient space; changing disk selection requires reviewing and confirming the new target.

## Where diagnostics live

Headless human progress and command errors go to stderr. With `--json-progress`, pipeline events go to stdout and other diagnostics stay on stderr. firn does not provide a separate `firn logs` command or a dedicated persistent install-log file.

The ISO's systemd kiosk directs stderr to the journal. On the live environment, inspect the current boot's journal:

```sh
journalctl -b
```

The terminal and graphical progress screens show warnings and the outcome. The GUI keeps an in-memory activity log, not a durable log file. The TUI final screen stays until dismissed.

Saved interactive recipes and secret files are under the run's private `/run/firn` session directory, available through the printed headless command until reboot. They are installation inputs, not logs. Do not attach the whole directory to a report.

!!! warning "Redact before sharing"

    Headless recovery keys appear on the selected progress stream, including stderr in human mode. Captures and journal output can contain secrets. Remove recovery keys, inline recipe secrets, password hashes and secret-file contents before sharing diagnostics.

## Report a problem

Open an issue at [frostyard/firn](https://github.com/frostyard/firn/issues). Include the firn version, installer medium, command or frontend, failing step, code and redacted message. Say whether it was validation, dry-run or a real install, and whether destructive steps had begun.

Known limits: bootc only, UEFI only, whole-disk replacement, no ZFS root and no unattended completion of first-boot MOK enrollment. Do not assume an architecture is hardware-qualified merely because a binary is published for it.
