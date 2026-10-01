---
name: nested-vm-e2e
description: Build, extend, or debug firn's nested-VM bootc end-to-end harnesses (test/e2e-tui.sh, test/e2e-bootc-secure.sh) that install inside a throwaway QEMU guest and verify the booted disk over SSH. Use whenever an E2E needs a new assertion, a new install variant, or fails somewhere between guest boot and SSH verification.
---

# Nested-VM end-to-end harnesses

Goal: install entirely inside a throwaway QEMU guest, boot the target disk
in a second QEMU, and verify it over SSH. Done = `PASS` with every `ok`
check line printed. The host never scans the guest's target disk. The
separate bootc loop-device harness (`test/e2e-bootc.sh`) creates fresh
generic partitions on the host instead.

## The pattern

1. Debian cloud image (cached at `/var/tmp/firn-e2e-cache`) + qcow2
   overlay = installer guest; blank target attached as a second disk;
   cloud-init NoCloud seed ISO injects an ed25519 key.
2. Drive everything over SSH (`hostfwd`), never fire-and-forget cloud-init
   `runcmd`. `gssh`/`gscp` wrap the port and key.
3. Stage a static firn (`CGO_ENABLED=0`; root may lack the user's Go toolchain,
   so harnesses can use a prebuilt `./firn` — run `make build` first).
   Install the tools bootc preflight requires in the guest.
4. Install, capture progress, assert the `done` event.
5. Boot the target in a fresh QEMU (OVMF, separate hostfwd port), poll SSH
   with the seeded root key and run the harness's system assertions.

## Debugging

- Work dirs are `/var/tmp/firn-e2e*.XXXXXX` (root-owned). Key artifacts:
  `progress.ndjson`, `firn.err`, `installer-console.log`,
  `installed-console.log` (names vary by harness).
- No SSH from the installed disk ≠ boot failure: check the console log
  for the login prompt first. sshd may be up while the check is wrong
  (e.g. a key written where the booted system never looks).
- Keep the nested target disk guest-only during installation.

## Pitfalls

- Caches live under `/var/tmp`, firn's work dirs under `/run/firn`.
- QEMU serial is silent without a `console=ttyS0` karg; the bootc
  loop-device harness injects it into the BLS entry post-install.
- Verify-phase ports must differ per harness (2222/2223/2225/2226…)
  so parallel runs never collide.
