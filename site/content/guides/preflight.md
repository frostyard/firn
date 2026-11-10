---
description: Understand what firn checks before destructive work, and what validation alone does not check.
---

# Preflight checks

Preflight runs before partitioning or formatting. The same checks run in both wizards and in headless installs. To run them without installing:

```sh
firn install --dry-run recipe.toml
```

Use a root shell in the intended live environment. The recipe must first pass schema validation.

## What is checked

| Check | What firn requires |
| --- | --- |
| `preflight-uefi` | UEFI mode. Legacy BIOS is refused. A missing TPM produces a warning; TPM-dependent recipe choices already fail validation. |
| `preflight-tools` | The commands required by the assembled pipeline must be on `PATH`. The list follows the chosen options rather than a fixed generic package list. |
| `preflight-image` | A usable image source. With `cosign_pub_key`, resolve and verify an immutable digest before disk writes. Read core Flatpaks metadata when requested. |
| `preflight-disk` | A resolvable whole disk that passes disk refusal checks. By-id paths resolve to their canonical disk device. |

The disk checks refuse the disk backing the running system, mounted filesystems anywhere below the disk, RAID members, LVM physical volumes and media with `SNOSI_INSTALLER_` or `FIRN_INSTALLER_` labels. A partition is not a valid whole-disk target.

firn prefers a valid local containers-storage image over the registry. A cached source can allow offline installation. For registry sources, signed recipes install the verified digest. Unsigned recipes also pin a reported valid digest, but that is not signature verification. See the [trust model](../concepts/trust.md).

## Image and network failures

Image resolution or signature failures happen before disk writes. A supplemental HTTPS registry probe can identify a connectivity failure:

- `network_unreachable`: the image attempt and probe failed, and the machine has no routable network address.
- `registry_unreachable`: the attempt and probe indicate DNS, connection or timeout failure, or a proxy 502/503/504. The cause can be the network or registry.
- Other errors retain their original meaning. TLS trust failures, registry authorization, missing manifests, rate limits and failed cosign verdicts are not all classified as network failures.

Correct the actual cause before retrying. Do not remove a trust key simply to bypass a verification failure. See [troubleshooting](../reference/troubleshooting.md).

## What a pass means

Dry-run reports that the recipe passed validation and preflight on this machine at that time. It changes no disk. It does not prove that later downloads, formatting, TPM enrollment, MOK enrollment or the final boot will succeed.

A real install repeats preflight. Keep enough target space for both the image store and deployment during a RAM-media install. Do not treat a passed preflight as a storage-capacity guarantee.

The [architecture](https://github.com/frostyard/firn/blob/main/docs/design/architecture.md#preflight) describes the checks and their place in the pipeline.
