---
name: nested-vm-e2e
description: Build, extend, or debug firn's nested-VM end-to-end harnesses (test/e2e-ab.sh, test/e2e-tui.sh) that install inside a throwaway QEMU guest and verify the booted disk over SSH. Use whenever an E2E needs a new assertion, a new install variant, or fails somewhere between guest boot and SSH verification.
---

# Nested-VM end-to-end harnesses

Goal: an install runs entirely inside a throwaway QEMU guest (the host
kernel never scans the target disk), the installed disk boots in a
second QEMU, and assertions run over SSH. Done = `PASS` with every
`ok` check line printed.

**Why nested is non-negotiable for A/B installs:**
[ADR-0009](../../../docs/adr/0009-ab-installs-require-partition-isolation.md)
— a snosi A/B image clones the host's own discoverable-partition
layout; a host-visible partscan once tore down a live GNOME session.
The bootc loop-device harness (`test/e2e-bootc.sh`) is exempt only
because it creates fresh generic partitions.

## The pattern (shared by e2e-ab.sh and e2e-tui.sh)

1. Debian cloud image (cached at `/var/tmp/firn-e2e-cache`) + qcow2
   overlay = installer guest; blank `target.raw` attached as the
   SECOND virtio disk (`/dev/vdb` in-guest); cloud-init NoCloud seed
   ISO injects one ed25519 key.
2. Drive everything over SSH (`hostfwd`), never fire-and-forget
   cloud-init `runcmd` — debuggability is the point. `gssh`/`gscp`
   helpers wrap the port and key.
3. Stage a static firn (`CGO_ENABLED=0`; root often lacks the user's
   go toolchain, so harnesses fall back to a prebuilt `./firn` — run
   `make build` first). apt-install exactly the tools firn's preflight
   will demand for the recipe's family.
4. Install, capture `--json-progress` output, assert the `done` event.
5. Boot `target.raw` in a fresh QEMU (OVMF, `hostfwd` to a different
   port), poll SSH with the seeded root key, run `check` assertions
   (hostname, `id`, locale/timezone/keyboard files, `flatpak list`,
   `install-info.json`).

## Debugging

- Work dirs are `/var/tmp/firn-e2e*.XXXXXX` (root-owned; `sudo` to
  read; clean with `sudo rm -rf`). Key artifacts: `progress.ndjson`,
  `firn.err`, `installer-console.log`, `installed-console.log`.
- No SSH from the installed disk ≠ boot failure: check the console
  log for the login prompt first. sshd may be up while the check is
  wrong (e.g. a key written where the booted system never looks).
- Inspecting an installed A/B disk directly on the host is FORBIDDEN
  (ADR-0009); boot it in a VM instead. bootc disks may be
  loop-mounted on the host.

## Pitfalls (each one was hit for real)

- Host `/root` and `/mnt` are read-only on snosi A/B dev machines —
  caches live under `/var/tmp`, firn's work dirs under `/run/firn`.
- The pubring lives at `/usr/lib/snosi/os-update-pubring.gpg` on
  installer media but `/usr/lib/systemd/import-pubring.gpg` on
  installed systems; harnesses copy whichever exists into the guest
  at firn's first search path.
- QEMU serial is silent without a `console=ttyS0` karg; the bootc
  harness injects it into the BLS entry post-install. A/B UKIs are
  signed — do not edit their cmdline; use SSH verification instead.
- Verify-phase ports must differ per harness (2222/2223/2225/2226…)
  so parallel runs never collide.
