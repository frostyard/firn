# Spec: Firn recipe schema (version 1)

This contract governs the TOML recipe file — firn's sole configuration
input. Consumers: the recipe loader/validator (`internal/recipe`), the
TUI (which generates recipes), automation and provisioning scripts, and
the test suites. Per
[ADR-0005](../adr/0005-toml-recipe-model.md) this file changes only
alongside the code that implements it.

## Interface

Top level:

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `version` | integer | yes | MUST be `1`. Unknown versions are rejected. |
| `[image]` | table | yes | See below. |
| `[target]` | table | yes | See below. |
| `[security]` | table | yes | See below. |
| `[system]` | table | yes | See below. |

### `[image]`

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `family` | string | yes | `"bootc"` or `"ab"`. Never inferred. |
| `ref` | string | bootc: yes | OCI image reference. bootc-only field. |
| `target_ref` | string | no | Post-install upgrade ref; defaults to `ref`. bootc-only. |
| `cosign_pub_key` | string (path) | no | Enables independent cosign verification of a registry `ref`. Before any destructive step, Firn selects the source it would install (preferring a valid embedded containers-storage image), resolves it to an immutable `sha256` digest, runs `cosign verify --key` against that digest, and installs that same digest. Verification failure emits `image_verification_failed`. bootc-only. |
| `product` | string | ab: yes | A/B publication channel matching `^[a-z0-9][a-z0-9._-]*$`; bare names such as `"snow"` and channel names such as `"snow-ab"` are valid. ab-only field. |
| `origin` | string (URL) | no | Artifact origin; default `https://repository.frostyard.org`. ab-only. |
| `release` | string | no | Pin an A/B release (14-digit version); default: newest in the signed index. ab-only. |

### `[target]`

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `disk` | string (path) | yes | Whole-disk block device (`/dev/…`, `by-id` paths allowed). Never a partition. |
| `filesystem` | string | bootc: yes | `"btrfs"`, `"xfs"`, or `"ext4"`. bootc-only (the A/B *root* is image-fixed; see `var_filesystem`). ZFS is not part of schema v1 because the installer does not yet have a complete bootable ZFS path. |
| `btrfs_subvolumes` | bool | no | bootc + btrfs only: create top-level `@`, `@home`, `@snapshots`. Default `false`. |
| `bootloader` | string | no | bootc only: `"systemd"` (default) or `"grub2"`. |
| `var_filesystem` | string | no | ab only: `"ext4"` (default) or `"btrfs"` for the `/var` partition ([ADR-0008](../adr/0008-ab-var-filesystem-choice.md)). |
| `var_subvolumes` | bool | no | ab only, requires `var_filesystem = "btrfs"`: create nested subvolumes `home` and `snapshots` inside `/var`. Default `false`. |

### `[security]`

All security choices are explicit
([ADR-0004](../adr/0004-single-installer-scope-and-support-matrix.md)):
omitting a required field here is a validation error, never a default.
The interactive TUI still needs an initial cursor selection: it starts
`encryption` at `"none"` and, when Secure Boot is active, `mok` at `"enroll"`.
The screen identifies both initial selections and the user must explicitly
advance past each prompt; the recipe always serializes the accepted values.

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `encryption` | string | yes | bootc: `"none"`, `"luks-passphrase"`, `"tpm2-luks"`, `"tpm2-luks-passphrase"`. ab: `"none"`, `"luks"` (recovery key only), `"tpm2-luks"` (recovery key + TPM). |
| `passphrase` / `passphrase_file` | string / path | conditional | Exactly one MUST be set when `encryption` includes `passphrase`; MUST NOT be set otherwise. |
| `recovery_key_out` | string (path) | no | ab only with `encryption = "luks"` or `"tpm2-luks"`: also write the generated recovery key to this non-empty path. Preflight refuses an existing path and reserves a new 0600 file before any destructive step (including in dry-run); the final byte-exact key is committed by same-directory atomic rename. The key is always disclosed via the progress protocol. |
| `mok` | string | ab or bootc: yes when Secure Boot is active | `"enroll"` or `"skip"`. With `"enroll"`, `mok_password_file` MUST be set. bootc uses it for the secure-install schema-1 path ([ADR-0014](../adr/0014-port-secure-install-schema-1-for-bootc.md)). |
| `mok_password_file` | string (path) | conditional | 0600 file; content is the one-time MokManager password. |

### `[system]`

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `hostname` | string | yes | RFC 1123 host label(s), max 253 chars. |
| `locale` | string | no | `ll_CC[.ENC]` form, e.g. `en_US.UTF-8`. Empty or omitted preserves the image default. |
| `timezone` | string | no | IANA zone name, e.g. `America/Chicago`; MUST exist in the target's zoneinfo. Empty or omitted preserves the image default. |
| `keyboard` | string | no | `LAYOUT[:VARIANT[:MODEL]]` XKB triplet. Empty or omitted preserves the image default. |
| `flatpaks` | array of string | no | Flatpak application IDs. |
| `core_flatpaks` | bool | no | Install the image-defined core set where published. Default `false`. |
| `root_ssh_authorized_key` / `_file` | string / path | no | At most one. The inline value or referenced file contains one or more newline-separated OpenSSH public-key records; every line MUST be non-empty and match a supported key type, base64 key body, and optional comment. |

The TUI initially offers user creation with the `sudo` group, but initially
leaves `core_flatpaks` disabled. Rebuilding either form preserves the user's
accepted values rather than reapplying these initial selections.

### `[system.user]` (optional table; omit to create no user)

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `name` | string | yes | POSIX username, `[a-z_][a-z0-9_-]*`, ≤ 32 chars. |
| `fullname` | string | no | GECOS comment. Empty and Unicode values are valid; `:`, CR, and LF are rejected so both image-family writers produce the same passwd field. |
| `password_file` | string (path) | one of these two | 0600 file containing the plaintext password (hashed by firn, SHA-512 crypt). |
| `password_hash` | string | one of these two | Pre-computed `$…` crypt hash, passed through verbatim. |
| `groups` | array of string | no | Supplementary groups; validated only for name syntax (`internal/recipe/validate.go`'s `usernameRe`), not existence in the image. At install time only groups present in the deployment's `etc/group` are joined (`internal/sysconfig/user.go`'s `filterGroups`); a group missing from the image is silently skipped, not rejected, and reported via a `progress.CodeGroupMissing` warning. |
| `ssh_authorized_key` / `_file` | string / path | no | At most one. Content follows the same per-line public-key rules as `root_ssh_authorized_key`; every configured line is installed in the user's `authorized_keys`. |

Supported SSH key types are `ssh-ed25519`, `ssh-rsa`, `ecdsa-sha2-*`, and
OpenSSH `sk-*` security-key types. A final newline is allowed; blank lines,
`authorized_keys` options, and other non-key content are rejected.

### Interactive wizard parity

The wizard exposes the common schema plus an opt-in **Advanced image options**
page. For bootc that page sets `target_ref` and `bootloader`; for A/B it sets
`origin` and `release`. The wizard offers GRUB 2 only when Secure Boot is
inactive because Firn's MOK enrollment path stages a signed systemd-boot
chain. Image identity (`ref` or `product`) and bootc
`cosign_pub_key` come from the selected catalog entry; operators customize
that tuple together through `/etc/firn/catalog.json` rather than entering an
untrusted reference independently of its trust policy.

The following equivalent or deliberately headless-only representations keep
the interactive secret/path surface smaller:

| Schema field | Wizard policy |
| --- | --- |
| `passphrase` / `passphrase_file` | Collect the passphrase and write a session-owned 0600 `passphrase_file`; never serialize it inline. |
| `mok_password_file` | Collect the one-time password and write a session-owned 0600 file. |
| `recovery_key_out` | For encrypted A/B installs, reserve `recovery-key` in the private wizard session. |
| SSH inline / `_file` variants | Accept validated inline pasted keys. Installer-environment file paths are headless-only. |
| User `password_file` / `password_hash` | Collect a password into a session-owned 0600 file. Precomputed hashes are headless-only. |

```toml
# minimal valid example (A/B install)
version = 1

[image]
family = "ab"
product = "snow-ab"

[target]
disk = "/dev/nvme0n1"

[security]
encryption = "tpm2-luks"
mok = "enroll"
mok_password_file = "/run/firn/mok-pw"

[system]
hostname = "frost01"

[system.user]
name = "bjk"
password_file = "/run/firn/user-pw"
groups = ["wheel"]
```

## Rules

1. Validation is fail-closed: unknown fields, unknown enum values, and
   fields belonging to the other family are **errors**, never warnings
   or ignored noise.
2. Every `*_file` field MUST reference an existing regular file at
   validation time; the secret-valued ones (`passphrase_file`,
   `mok_password_file`, `password_file`) MUST NOT be world-readable.
   Inline and `_file` variants of the same value are mutually
   exclusive.
3. `[security]` completeness is family- and machine-aware: `mok` is
   required exactly when `family` is `"ab"` or `"bootc"` and Secure Boot
   is active on the install machine; `tpm2-*` modes are an error on
   machines with no TPM (no silent fallback).
4. Validation MUST succeed or fail entirely before any destructive step;
   a recipe that validates assembles a runnable pipeline.
5. The TUI MUST NOT be able to produce a recipe this spec rejects, and
   every recipe it produces MUST reproduce the same install headless. The
   accepted review page's TOML byte sequence MUST be the exact persisted
   `recipe.toml` artifact used by the in-process install and printed
   reproduce-headless command; the command layer MUST NOT reserialize it.
6. Secrets (`passphrase`, `password_hash`, file contents) MUST never be
   echoed in logs, progress events, or error messages.
7. `version` gates the whole schema: any breaking change to this spec
   increments it, and firn rejects versions it does not implement.

## References

- Rationale: [ADR-0005](../adr/0005-toml-recipe-model.md),
  [ADR-0004](../adr/0004-single-installer-scope-and-support-matrix.md),
  [ADR-0006](../adr/0006-install-time-offline-first-flatpaks.md)
- Context: [design/architecture.md](../design/architecture.md)
