---
description: Choose a whole-disk layout, a root filesystem and a LUKS or TPM2 encryption mode.
---

# Disks and encryption

firn installs onto a whole disk. It replaces the partition table and creates new filesystems; it does not resize an existing system or preserve another operating system beside it.

## Identify the target

Read the picker or review page's disk identity, including serial, WWN, model, size and labels where available. Device names such as `/dev/sda` can change between boots.

A headless recipe can use `/dev/disk/by-id/…`. It must resolve to a whole disk, not a partition. firn resolves the canonical device before partitioning. Typed confirmation still matches the recipe path exactly, not the resolved path.

[Preflight](preflight.md) refuses mounted disks, RAID/LVM members, the running system's disk and recognized installer media. Do not remove a refusal condition until you have independently confirmed which disk you intend to erase.

## Layouts and filesystems

Both bootloader layouts use GPT:

| Bootloader | Partitions |
| --- | --- |
| `systemd` (default) | A 2 GiB EFI System Partition, then root using the remaining space. |
| `grub2` | A 2 GiB EFI System Partition, a separate 2 GiB ext4 `/boot`, then root using the remaining space. |

The EFI System Partition is FAT32. Encryption wraps the root partition, not the EFI System Partition or GRUB's separate `/boot`. The interactive installers offer GRUB 2 only when Secure Boot is inactive; the enrollment path uses signed systemd-boot.

Root can use `btrfs`, `xfs` or `ext4`. With btrfs, `btrfs_subvolumes = true` creates top-level `@`, `@home` and `@snapshots`. It defaults to false. ZFS root is rejected because there is no complete bootable installation path.

The [partitioning implementation](https://github.com/frostyard/firn/blob/main/internal/disk/partition.go) defines the layouts; the [recipe contract](https://github.com/frostyard/firn/blob/main/docs/specs/recipe-schema.md#target) defines the supported choices.

## Choose an encryption mode

`[security].encryption` is always explicit in a recipe:

| Mode | Unlock behavior |
| --- | --- |
| `none` | No root encryption. |
| `luks-passphrase` | LUKS2 root, unlocked with your passphrase at boot. No TPM required. |
| `tpm2-luks` | LUKS2 root with TPM2 auto-unlock and a generated recovery key. |
| `tpm2-luks-passphrase` | LUKS2 root with TPM2 auto-unlock plus your fallback passphrase. |

TPM modes fail validation when no TPM is detected; there is no silent fallback. Enrollment happens during installation against the deployed UKI's signed PCR 11 policy. It is not deferred to a first-boot unit.

## Supply a passphrase

Passphrase modes require exactly 1 of `passphrase` or `passphrase_file`. Prefer a 0600 regular file for a headless recipe:

```toml
[security]
encryption = "tpm2-luks-passphrase"
passphrase_file = "/run/firn/my-passphrase"
```

This is a section fragment, not a complete recipe. Create the file securely before validation; do not put the secret in an example command or shell history. The wizards ask for confirmation and write a session-owned 0600 file rather than placing the passphrase inline in TOML.

## Keep the recovery material

With `tpm2-luks`, firn generates and discloses a recovery key. Store it outside the encrypted machine before acknowledging it. The interactive installers show it once behind a blocking acknowledgement and clear it afterwards.

Headless output deliberately exposes the key: stderr in human mode, stdout in `--json-progress` mode. Treat that stream as secret-bearing. See [progress events](../reference/progress.md#recovery-key-handling).

With `tpm2-luks-passphrase`, keep your fallback passphrase securely. TPM auto-unlock is not a substitute for recovery material you can use if automatic unlock fails.

Secure Boot is a separate choice. Read [Secure Boot and MOK enrollment](secure-boot.md) when the firmware enforces it.
