---
description: A user summary of the version-2 TOML recipe fields, file rules and bootc-v1 compatibility window.
---

# Recipe

A recipe is firn's TOML installation input. This page summarizes the current fields; the [full recipe specification](https://github.com/frostyard/firn/blob/main/docs/specs/recipe-schema.md) is the exact contract for types, validation patterns and compatibility.

Unknown fields and enum values are errors. firn never guesses the image family or silently converts a recipe. See [headless installation](../guides/headless-install.md) for a complete starting example.

## Version and sections

Set `version = 2` at top level. The recipe contains `[image]`, `[target]`, `[security]` and `[system]`. Add `[system.user]` only when creating a user.

## Image

| Field | Meaning |
| --- | --- |
| `family` | Required; exactly `"bootc"`. |
| `ref` | Required OCI image reference. Registry installs pin a reported valid source digest during preflight. |
| `target_ref` | Optional post-install update reference; defaults to `ref`. |
| `cosign_pub_key` | Optional public-key path enabling independent cosign verification of a registry source digest. |

Unsigned digest pinning does not verify the publisher. If an unsigned source reports no valid digest, the tag is kept. See the [trust model](../concepts/trust.md).

## Target

| Field | Meaning |
| --- | --- |
| `disk` | Required whole-disk `/dev/…` path. A by-id path must resolve to a whole disk. Confirmation matches the path as written. |
| `filesystem` | Required: `"btrfs"`, `"xfs"` or `"ext4"`. |
| `btrfs_subvolumes` | Optional bool, default false; with btrfs only, creates `@`, `@home`, `@snapshots`. |
| `bootloader` | Optional: `"systemd"` (default) or `"grub2"`. |

ZFS is unsupported. See [disk layouts](../guides/disks-and-encryption.md).

## Security

| Field | Meaning |
| --- | --- |
| `encryption` | Required: `"none"`, `"luks-passphrase"`, `"tpm2-luks"` or `"tpm2-luks-passphrase"`. |
| `passphrase` / `passphrase_file` | Exactly 1 when the mode includes `passphrase`; neither for other modes. Prefer a private file. |
| `mok` | Required when Secure Boot is active: `"enroll"` or `"skip"`. Must not be set when Secure Boot is inactive. |
| `mok_password_file` | Required for `mok = "enroll"`; contains the one-time MokManager password. Must not be set with `mok = "skip"` or when Secure Boot is inactive. |

TPM modes require a detected TPM. Enrollment needs a person to complete MokManager at first boot. See [encryption](../guides/disks-and-encryption.md) and [Secure Boot](../guides/secure-boot.md).

## System and user

| `[system]` field | Meaning |
| --- | --- |
| `hostname` | Required valid host name, up to 253 characters. |
| `locale` | Optional `ll_CC[.ENC]`, for example `en_US.UTF-8`. |
| `timezone` | Optional IANA zone name, for example `America/Chicago`; must exist in zoneinfo. |
| `keyboard` | Optional XKB `LAYOUT[:VARIANT[:MODEL]]`. |
| `flatpaks` | Optional array of Flatpak application IDs. |
| `core_flatpaks` | Optional bool, default false; install the selected image's published core set. |
| `root_ssh_authorized_key` / `root_ssh_authorized_key_file` | Optional inline or file-based root SSH public-key records; at most 1 variant. |

Empty or omitted locale, timezone and keyboard keep the image defaults. [Flatpaks](../guides/flatpaks.md) explains core-list validation and offline behavior.

Omit `[system.user]` to create no user. When present:

| Field | Meaning |
| --- | --- |
| `name` | Required POSIX username, up to 32 characters. |
| `fullname` | Optional full name; Unicode is allowed, but colon and line breaks are rejected. |
| `password_file` / `password_hash` | Exactly 1: a private plaintext-password file or a precomputed crypt hash. |
| `groups` | Optional supplementary groups. Names are validated; absent image groups are skipped with `group_missing`. |
| `ssh_authorized_key` / `ssh_authorized_key_file` | Optional inline or file-based user SSH public-key records; at most 1 variant. |

SSH values accept newline-separated OpenSSH public-key records. Supported types include `ssh-ed25519`, `ssh-rsa`, `ecdsa-sha2-*` and OpenSSH `sk-*` types. Blank lines, private keys and `authorized_keys` options are not accepted; a final newline is allowed.

## File and secret rules

Every `*_file` value must name an existing regular file at validation time. Secret-valued files (`passphrase_file`, `mok_password_file`, `password_file`) must not be world-readable; use 0600. Inline and file variants of the same value are mutually exclusive.

The wizards write session-owned secret files instead of inline passwords. Precomputed user password hashes and installer-environment SSH key file paths are headless-only representations. Protect recipes and referenced files; do not paste their secrets into logs or issue reports.

## Bootc-v1 compatibility

Bootc `version = 1` remains accepted with a deprecation warning until **2026-12-30T00:00:00Z**. On and after that instant, the validator rejects it with `expired-version`. No environment variable moves the compiled-in cutoff. Older releases that lack the cutoff change can behave differently.

To migrate, confirm `family = "bootc"`, validate every bootc field and referenced file on the intended new medium, then change only `version = 1` to `version = 2`. Old firn releases cannot consume v2 recipes. There is no automatic upgrade or downgrade.

A/B-v1 is rejected with `family-scope`, not converted. In v2, `family = "ab"` and A/B-only fields such as `image.product` or `target.var_filesystem` are errors. See the [compatibility contract](https://github.com/frostyard/firn/blob/main/docs/specs/recipe-schema.md#bootc-version-1-compatibility) for the exact diagnostics.
