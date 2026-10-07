# Spec: Firn recipe schema (version 2)

This contract governs the TOML recipe file — firn's sole configuration
input. Consumers: the recipe loader/validator (`internal/recipe`), the
TUI (which generates recipes), automation and provisioning scripts, and
the test suites. Per
[ADR-0005](../adr/0005-toml-recipe-model.md) and
[ADR-0016](../adr/0016-bootc-only-recipe-contract.md), this file changes only
alongside the code that implements it.

## Interface

Top level:

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `version` | integer | yes | `2` is the operative contract; bootc `1` is temporarily accepted with a deprecation warning (see compatibility below). All other versions are rejected. |
| `[image]` | table | yes | See below. |
| `[target]` | table | yes | See below. |
| `[security]` | table | yes | See below. |
| `[system]` | table | yes | See below. |

### `[image]`

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `family` | string | yes | MUST be `"bootc"`. Never inferred. |
| `ref` | string | yes | OCI image reference. |
| `target_ref` | string | no | Post-install upgrade ref; defaults to `ref`. |
| `cosign_pub_key` | string (path) | no | Enables independent cosign verification of a registry `ref`. Before any destructive step, Firn selects the source it would install (preferring a valid embedded containers-storage image), resolves it to an immutable `sha256` digest, runs `cosign verify --key` against that digest, and installs that same digest. Verification failure emits `image_verification_failed`. |

### `[target]`

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `disk` | string (path) | yes | Whole-disk block device (`/dev/…`, `by-id` paths allowed). Never a partition. |
| `filesystem` | string | yes | `"btrfs"`, `"xfs"`, or `"ext4"`. ZFS is not part of schema v2 because the installer does not yet have a complete bootable ZFS path. |
| `btrfs_subvolumes` | bool | no | With btrfs only: create top-level `@`, `@home`, `@snapshots`. Default `false`. |
| `bootloader` | string | no | `"systemd"` (default) or `"grub2"`. |

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
| `encryption` | string | yes | `"none"`, `"luks-passphrase"`, `"tpm2-luks"`, `"tpm2-luks-passphrase"`. |
| `passphrase` / `passphrase_file` | string / path | conditional | Exactly one MUST be set when `encryption` includes `passphrase`; MUST NOT be set otherwise. |
| `mok` | string | yes when Secure Boot is active | `"enroll"` or `"skip"`. With `"enroll"`, `mok_password_file` MUST be set. Used for the secure-install schema-1 path ([ADR-0014](../adr/0014-port-secure-install-schema-1-for-bootc.md)). |
| `mok_password_file` | string (path) | conditional | Existing regular file that is not world-readable (rule 2); content is the one-time MokManager password. |

### `[system]`

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `hostname` | string | yes | RFC 1123 host label(s), max 253 chars. |
| `locale` | string | no | `ll_CC[.ENC]` form, e.g. `en_US.UTF-8`. Empty or omitted preserves the image default. |
| `timezone` | string | no | IANA zone name, e.g. `America/Chicago`; MUST exist in the target's zoneinfo. Empty or omitted preserves the image default. |
| `keyboard` | string | no | `LAYOUT[:VARIANT[:MODEL]]` XKB triplet. Empty or omitted preserves the image default. |
| `flatpaks` | array of string | no | Flatpak application IDs. |
| `core_flatpaks` | bool | no | Install the core set the selected image publishes in its `org.frostyard.core-flatpaks` label ([core Flatpaks label](core-flatpaks-label.md)), after `flatpaks`. A malformed label fails preflight; an image with no set installs none and reports `no_core_set`. Default `false`. |
| `root_ssh_authorized_key` / `_file` | string / path | no | At most one. The inline value or referenced file contains one or more newline-separated OpenSSH public-key records; every line MUST be non-empty and match a supported key type, base64 key body, and optional comment. |

The TUI initially offers user creation with the `sudo` group. It offers
`core_flatpaks` only when the chosen image publishes a core set or could not
be inspected, initially enabled in both cases
([ADR-0019](../adr/0019-wizard-offers-core-flatpaks-by-default.md)), and
never serializes `core_flatpaks = true` for an image whose
toggle it hid ([core Flatpaks label](core-flatpaks-label.md), rule 7).
Rebuilding either form, or revisiting the flatpaks page for the same image,
preserves the user's accepted values rather than reapplying these initial
selections; choosing another image applies that image's initial selection.
The recipe default stays `false`.

### `[system.user]` (optional table; omit to create no user)

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `name` | string | yes | POSIX username, `[a-z_][a-z0-9_-]*`, ≤ 32 chars. |
| `fullname` | string | no | GECOS comment. Empty and Unicode values are valid; `:`, CR, and LF are rejected before writing the passwd field. |
| `password_file` | string (path) | one of these two | Existing regular file that is not world-readable (rule 2), containing the plaintext password (hashed by firn, SHA-512 crypt). |
| `password_hash` | string | one of these two | Pre-computed `$…` crypt hash, passed through verbatim. |
| `groups` | array of string | no | Supplementary groups; validated only for name syntax (`internal/recipe/validate.go`'s `usernameRe`), not existence in the image. At install time only groups present in the deployment's `etc/group` are joined (`internal/sysconfig/user.go`'s `filterGroups`); a group missing from the image is silently skipped, not rejected, and reported via a `progress.CodeGroupMissing` warning. |
| `ssh_authorized_key` / `_file` | string / path | no | At most one. Content follows the same per-line public-key rules as `root_ssh_authorized_key`; every configured line is installed in the user's `authorized_keys`. |

Supported SSH key types are `ssh-ed25519`, `ssh-rsa`, `ecdsa-sha2-*`, and
OpenSSH `sk-*` security-key types. A final newline is allowed; blank lines,
`authorized_keys` options, and other non-key content are rejected.

### Interactive wizard parity

The wizard exposes the common schema plus an opt-in **Advanced image options**
page for `target_ref` and `bootloader`. The wizard offers GRUB 2 only when Secure Boot is
inactive because Firn's MOK enrollment path stages a signed systemd-boot
chain. Image identity (`ref`) and
`cosign_pub_key` come from the selected catalog entry; operators customize
that tuple together through `/etc/firn/catalog.json` rather than entering an
untrusted reference independently of its trust policy.

The following equivalent or deliberately headless-only representations keep
the interactive secret/path surface smaller:

| Schema field | Wizard policy |
| --- | --- |
| `passphrase` / `passphrase_file` | Collect the passphrase and write a session-owned 0600 `passphrase_file`; never serialize it inline. |
| `mok_password_file` | Collect the one-time password and write a session-owned 0600 file. |
| SSH inline / `_file` variants | Accept validated inline pasted keys. Installer-environment file paths are headless-only. |
| User `password_file` / `password_hash` | Collect a password into a session-owned 0600 file. Precomputed hashes are headless-only. |

```toml
# minimal valid example (bootc install, Secure Boot off)
version = 2

[image]
family = "bootc"
ref = "ghcr.io/frostyard/snow:latest"

[target]
disk = "/dev/nvme0n1"
filesystem = "btrfs"

[security]
encryption = "none"

[system]
hostname = "frost01"

```

### Bootc version-1 compatibility

`version = 1` with `image.family = "bootc"` remains accepted under the
existing bootc validation rules, without rewriting the recipe. The
non-fatal `recipe.Deprecations` issue has field `version`, code
`deprecated-version`, and message: `bootc recipe version 1 is deprecated;
change version to 2 after validating the bootc fields before the published
compatibility end date`. `firn validate` prints this warning to stderr and
exits 0; `firn install` and the TUI emit the
[`recipe_v1_deprecated` warning](progress-protocol.md#stable-codes) before
the first step event. Migrate only after confirming the family and checking
all bootc fields and referenced files on the intended installer medium;
change only `version = 1` to `version = 2`. Old Firn releases cannot consume
v2 recipes. Never infer a family or convert an A/B recipe to bootc.

The bootc-v1 compatibility window is **90 days from the release that ships
v2**, not from a calendar cutoff. The actual release and exact end dates
must be recorded at release; neither is set here. The validator has no
automatic expiry: rejecting bootc-v1 after that window requires a later
reviewed code and release change.

`version = 1` with `image.family = "ab"` parses but fails validation before
pipeline assembly or disk writes with one issue: field `image.family`, code
`family-scope`, message `A/B version 1 is unsupported by this bootc-only Firn;
no conversion was performed; use a pre-transition installer only where
separately authorized`. Version 2 with `family = "ab"` is an enum error;
`image.product`, `image.origin`, `image.release`, `target.var_filesystem`,
`target.var_subvolumes`, and `security.recovery_key_out` are unknown fields.
Missing, zero, and all other unsupported versions (including future versions)
fail closed with `bad-version`; no downgrade or conversion is attempted.

## Rules

1. Validation is fail-closed: unknown fields, unknown enum values, and
   A/B-only fields are **errors**, never warnings or ignored noise.
2. Every `*_file` field MUST reference an existing regular file at
   validation time; the secret-valued ones (`passphrase_file`,
   `mok_password_file`, `password_file`) MUST NOT be world-readable.
   Inline and `_file` variants of the same value are mutually
   exclusive.
 3. `[security]` completeness is machine-aware: `mok` is
    required when Secure Boot
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
    increments it; only the explicit bootc-v1 compatibility exception above
    is accepted in addition to v2.

## References

- Rationale: [ADR-0005](../adr/0005-toml-recipe-model.md),
  [ADR-0004](../adr/0004-single-installer-scope-and-support-matrix.md),
   [ADR-0006](../adr/0006-install-time-offline-first-flatpaks.md),
   [ADR-0015](../adr/0015-bootc-only-installer-scope.md),
   [ADR-0016](../adr/0016-bootc-only-recipe-contract.md),
   [ADR-0018](../adr/0018-image-published-core-flatpaks-label.md)
   (`core_flatpaks`; see [core Flatpaks label](core-flatpaks-label.md))
- Context: [design/architecture.md](../design/architecture.md)
