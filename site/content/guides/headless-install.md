---
description: Validate a TOML recipe, run non-destructive preflight, confirm the target disk and consume progress.
---

# Headless install with a recipe

Use a version-2 TOML recipe to install without a wizard. Run the install from a root shell on live media. Validation and dry-run are separate checks; neither is a disk installation.

## Write a recipe

This example selects a bootc image, btrfs and no encryption. It assumes Secure Boot is inactive. Replace the disk and hostname for your machine. The cosign public-key path is supplied by the snosi installer medium and must match the selected image.

```toml
version = 2

[image]
family = "bootc"
ref = "ghcr.io/frostyard/snow:latest"
cosign_pub_key = "/usr/lib/snosi/cosign.pub"

[target]
disk = "/dev/nvme0n1"
filesystem = "btrfs"

[security]
encryption = "none"

[system]
hostname = "frost01"
```

Save it as `recipe.toml`. This minimal recipe creates no user account and installs no core Flatpaks. Add the [account fields](../reference/recipe.md#system-and-user) and a login method before using it for a machine you need to access. See the [recipe reference](../reference/recipe.md) for the optional fields.

For an encrypted install, choose a passphrase mode and point `passphrase_file` at an existing private file. For Secure Boot, configure `mok` and, for enrollment, `mok_password_file`. See [encryption](disks-and-encryption.md) and [Secure Boot](secure-boot.md).

!!! warning "Keep secrets private"

    Use 0600 regular files for passwords and passphrases, outside shared directories. Do not put passwords in shell command arguments or shell history. Secret files must exist when validation runs. A recipe can contain inline secrets or password hashes, so protect the recipe too.

## Validate the fields

```sh
firn validate recipe.toml
```

A valid recipe prints `firn: recipe is valid (family bootc)` and exits 0. Invalid recipes print field issues and return non-zero. Unknown fields and unsupported values are errors, not ignored settings.

Validation checks schema constraints, referenced files and machine-dependent TPM and Secure Boot choices. It does not perform the pipeline's tool, image and disk preflight checks.

## Run preflight without changing a disk

```sh
firn install --dry-run recipe.toml
```

This assembles the pipeline and runs only preflight. It does not partition, format or install. No `--confirm` is required. A successful run ends with a diagnostic containing `preflight passed` and `no disks touched`.

`--dry-run` (also `-n`) requires a recipe. It is rejected for bare `firn`, for `firn install` without a recipe, and for `firn validate`.

## Confirm and install

!!! danger "This command erases the target disk"

    Check backups and the disk's identity before running it. The value of `--confirm` must exactly match `[target].disk` as written in the recipe, including a by-id path if you used one.

```sh
firn install --confirm /dev/nvme0n1 recipe.toml
```

Preflight runs again before destructive work. Progress and human diagnostics go to stderr. A successful install exits 0; failures return non-zero. Cancellation stops further work and unwinds cleanup, but cannot restore overwritten data.

Only 1 real install can run at a time on a machine. A second fails with `install_in_progress` before any step. Dry-run does not take that lock.

## Read machine progress

```sh
firn install --json-progress --confirm /dev/nvme0n1 recipe.toml
```

`--json-progress` emits versioned NDJSON to stdout, one event per line. Other output goes to stderr. Consumers must wait for a terminal `done` or `error`; a closed stream without either is failure. See [progress events](../reference/progress.md).

!!! warning "Progress can disclose a recovery key"

    TPM-only LUKS emits the literal recovery key on stdout in JSON mode and on stderr in human mode. Protect the selected stream, terminal scrollback and any captured files. Do not send it to ordinary logs.

## Reuse a wizard recipe

Both wizards print a reproduction command for their saved recipe. Each run has a private 0700 session directory below `/run/firn`; referenced secret files are 0600. Persisted sessions remain until the installer environment reboots. Abandoned sessions are removed.

Use the printed path, not a guessed session name. If you keep a recipe beyond reboot, also retain its referenced files securely and update their paths. Validate it again on the intended installer machine before confirming another destructive install.
