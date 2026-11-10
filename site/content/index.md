---
title: Install frostyard images with firn
description: Install snosi bootc images with a terminal wizard, a graphical installer or a TOML recipe.
hide:
  - navigation
  - toc
---

<div class="fy-hero" markdown>
<div class="fy-hero__text" markdown>
<p class="fy-eyebrow">firn</p>

# Install frostyard images with firn

<p class="fy-hero__lede">firn installs snosi bootc OCI images onto a disk. Use the terminal wizard or graphical installer on the snosi installer ISO, or supply a TOML recipe for a headless install. Each method runs the same checks and installation pipeline.</p>

[Get started →](getting-started/install.md){ .md-button .md-button--primary }
[Your first install](getting-started/first-install.md){ .md-button }

</div>
<img class="fy-hero__icon" src="assets/logo.svg" alt="">
</div>

!!! warning "Installation erases the selected disk"

    Back up its data before you start. firn installs to a whole disk, not into free space beside an existing system. Every real install requires the exact target disk path as confirmation.

## Choose how to install

<div class="grid cards" markdown>

-   **[Terminal wizard](guides/terminal-wizard.md)**

    ---

    Run `firn` to choose an image, disk, security options and system settings. Review the generated recipe before you confirm.

-   **[Graphical installer](guides/graphical-installer.md)**

    ---

    Use `firn-gui` from the ISO's graphical boot entry. Its pages collect the same recipe fields, with keyboard shortcuts for every action.

-   **[Headless install](guides/headless-install.md)**

    ---

    Write a version-2 TOML recipe. Validate it, run preflight without disk changes, then install with typed disk confirmation.

-   **[Security choices](guides/disks-and-encryption.md)**

    ---

    Select unencrypted storage, a LUKS passphrase, or TPM2 auto-unlock. Secure Boot uses a separate MOK enrollment step.

</div>

## Start here

<div class="grid cards" markdown>

- <span class="fy-index">01</span> **[Install](getting-started/install.md)**

    Boot the snosi installer ISO or install the CLI package.

- <span class="fy-index">02</span> **[How firn works](concepts/how-it-works.md)**

    Understand recipes, preflight and the installation pipeline.

- <span class="fy-index">03</span> **[Commands](reference/cli.md)**

    Look up installation, validation and detection options.

- <span class="fy-index">04</span> **[Troubleshooting](reference/troubleshooting.md)**

    Find failure codes, diagnostics and known limits.

</div>

!!! note "Pre-release"

    firn is pre-release software. The current recipe contract is bootc-only version 2. A/B images, legacy BIOS boot and ZFS root are not supported. This site describes the implemented installer, not a claim of completed hardware qualification.

firn is GPL-3.0-only software, derived from fisherman. See the repository's [NOTICE](https://github.com/frostyard/firn/blob/main/NOTICE) for attribution.
