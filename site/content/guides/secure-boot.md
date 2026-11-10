---
description: Prepare the secure boot chain, stage MOK enrollment and complete the human step at first boot.
---

# Secure Boot

UEFI is required for every install, but Secure Boot is optional. When Secure Boot is active, firn requires an explicit MOK choice in the recipe. When Secure Boot is inactive, `mok` and `mok_password_file` must not be set: validation rejects them. The wizards show the choice only when it applies.

Secure Boot controls trust in the boot chain. It is separate from [cosign image verification](../concepts/trust.md) and [LUKS disk encryption](disks-and-encryption.md).

## The boot chain

For a compatible secure bootc image, the enrollment path stages:

1. Debian shim, trusted by Microsoft firmware keys.
2. snosi's MOK-signed systemd-boot.
3. The unified kernel image (UKI).

firn reads the image's secure-install schema-1 contract and checks the required capability and boot-chain shape. It stages the EFI files from the deployed image and requests MOK enrollment using the image's own certificate. The installed image owns later bootloader reconciliation; firn is a one-time installer.

See [the secure-install decision](https://github.com/frostyard/firn/blob/main/docs/adr/0014-port-secure-install-schema-1-for-bootc.md) for the contract boundary.

## Choose enrollment

Under active Secure Boot, the wizards initially select **enroll**. Enter and confirm a one-time MokManager password. This is not your account password or disk passphrase.

For a headless recipe, use this security section with the rest of your recipe:

```toml
[security]
encryption = "none"
mok = "enroll"
mok_password_file = "/run/firn/my-mok-password"
```

Create the password file securely before validation. It must be an existing regular file that is not world-readable; use mode 0600. Use `bootloader = "systemd"` in `[target]`, or omit the field for that default.

`mok = "skip"` does not enroll a key. With `skip`, firn stages neither the shim → MOK-signed systemd-boot chain nor a MOK enrollment request. Use it only when you manage the boot trust yourself. Skipping enrollment does not guarantee that firmware will accept a chain it does not already trust.

## Complete the next boot

After a successful install with enrollment staged, boot the target disk. MokManager starts. Select **Enroll MOK**, then **Continue** and **Yes**, then type the one-time password and reboot.

This human step is required. A headless install can stage the request but cannot complete it unattended. Do not treat successful staging or a passed dry-run as proof of completed enrollment.

If firmware refuses the boot chain, check whether MOK enrollment completed and whether you used a compatible secure image. See [troubleshooting](../reference/troubleshooting.md).

## Do not use overrides to change firmware

Detection flags change firn's view of the machine only. `--secure-boot off` does not turn Secure Boot off in firmware, and `--uefi on` cannot make BIOS boot supported. Keep automatic detection unless you have a specific reason to correct it.
