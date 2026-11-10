---
description: The boundaries between image verification, digest pinning, Secure Boot, encryption and secret handling.
---

# Trust model

firn runs privileged host commands to replace a disk. Its preflight and confirmation checks reduce accidental writes, but they do not make an untrusted installer medium or recipe safe. Check the medium, image, disk and security choices before installation.

## Image verification and digest pinning

`[image].cosign_pub_key` enables independent cosign verification for a registry reference. Before destructive work, firn selects the source, resolves its immutable digest, verifies that digest, and installs the same source digest. Verification failure stops preflight.

A valid embedded containers-storage image is preferred over the registry. The built-in interactive catalog supplies the medium's `/usr/lib/snosi/cosign.pub`. Custom catalogs can supply their own key paths. The catalog keeps image identity and trust metadata together; it does not ask you to enter an arbitrary reference independently of its key.

Without `cosign_pub_key`, firn relies on host container policy and cached-image provenance. Registry installs still pin a valid reported digest from preflight, but **pinning is not verification**. It identifies what was installed, not who built it.

If an unsigned source reports no valid digest, the tag remains the source and can resolve again during the pull. With `cosign_pub_key`, a non-registry reference fails preflight: cosign verification needs a registry reference. See the [recipe contract](https://github.com/frostyard/firn/blob/main/docs/specs/recipe-schema.md#image).

The pinned installation digest does not fix future updates to that build. Updates use `target_ref`, or `ref` when `target_ref` is empty.

## Separate security properties

| Mechanism | What it covers |
| --- | --- |
| Cosign verification | Verification of the selected OCI source digest against the configured public key before disk writes. |
| Secure Boot and MOK | Firmware acceptance of the staged boot chain; MOK enrollment can require a person at first boot. |
| LUKS | Encryption of the root partition at rest. The EFI System Partition remains unencrypted. |
| TPM2 enrollment | Automatic LUKS unlock against the deployed UKI's signed PCR 11 policy. |
| Typed disk confirmation | The exact target path you intend to erase; it is not an image-trust check. |

Read [Secure Boot](../guides/secure-boot.md) and [disks and encryption](../guides/disks-and-encryption.md) for the corresponding choices. A detection override changes only firn's probes, not the firmware or hardware.

## Recipes, files and recovery keys

Interactive runs own private 0700 directories below `/run/firn`. Secrets use 0600 files. The reviewed recipe references those files instead of placing passphrases inline. Persisted sessions remain until reboot so the printed headless command can reproduce the install. Protect any copy you keep beyond that live session.

Ordinary progress events must not contain secrets. The deliberate exception is a generated recovery key. Interactive frontends show it once behind an acknowledgement and clear it afterwards. Headless human output prints it to stderr; JSON mode emits it on stdout. Those streams can therefore contain secrets and must not enter ordinary logs.

The GUI does not copy recovery keys to the clipboard. Its package installs the executable alone in `/usr/libexec/firn-gui/`, behind a `/usr/bin/firn-gui` symlink, because the UI toolkit cleans update leftovers beside its executable. Do not move the real binary into a shared executable directory.

## Scope and qualification

The implemented recipe contract supports bootc, not A/B. That code scope is not a claim that all published ISO hardware and security combinations have completed qualification. The [architecture scope note](https://github.com/frostyard/firn/blob/main/docs/design/architecture.md#prospective-post-cutoff-scope) tracks that distinction.
