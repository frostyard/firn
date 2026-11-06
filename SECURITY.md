# Security Policy

## Supported versions

Firn is pre-release. Only the latest tagged release (`vX.Y.Z`) gets fixes.

A fix ships as a new release. The release workflow publishes the `.deb`
packages through apt-publisher to
`https://repository.frostyard.org/debian/`, and the next snosi installer ISO
build installs that release.

The nightly `dev` prerelease is a snapshot for testing. It gets no fixes: if
you find a problem in it, report it, and check whether the latest tagged
release has it too.

## Reporting a vulnerability

Use GitHub private vulnerability reporting:
https://github.com/frostyard/firn/security

Open the Security tab and click **Report a vulnerability**.

Do not open a public issue with the details of a vulnerability. Do not post
credentials in an issue, a pull request or a discussion.

## What to include

- The affected commit or release.
- The impact: what an attacker can do.
- The steps to reproduce it.
