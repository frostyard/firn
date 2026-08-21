---
name: port-from-parents
description: Port code from fisherman (Go) or snosi-install (bash) into firn with provenance, incident comments, and fake-runner tests intact. Use whenever implementing an area either parent installer already covers — consult-before-reimplement is an ADR-0003 obligation.
---

# Port code from fisherman / snosi-install

Goal: firn gains a behavior one of its parents already proved, keeping
the parents' battle scars. Done = ported code with provenance header,
incident comments carried over, fake-runner tests green, and any
deliberate divergence documented in a comment.

Sources:
- fisherman (Go): `/home/bjk/projects/frostyard/fisherman/fisherman/`
- snosi-install (bash): `/home/bjk/projects/frostyard/snosi/shared/`
  `native-installer/tree/usr/libexec/snosi-install` (tests in
  `snosi/test/snosi-install-test.sh`)

## Steps

1. Read the parent implementation FIRST (ADR-0003 obligation). Note
   every comment citing a dated incident or CI run — those encode real
   failures and MUST travel with the logic they guard.
2. Start the firn file with a provenance header:
   `// Ported from frostyard/fisherman (GPL-3.0-only), fisherman/internal/<pkg>/<file>.go.`
   (or `frostyard/snosi ... snosi-install (<function names>)`).
3. All host commands go through `*runner.Runner`
   (`Run`/`RunInput`/`RunStream`/`LookPath`) — never `os/exec`
   directly. Pure file manipulation on mounted trees uses plain
   `os`/`filepath` (see `internal/sysconfig` for the split).
4. Bash → Go structural fixes are ENCOURAGED and documented in place:
   typed return values replace stdout-capture contracts, parsed
   structs replace awk edits, atomic write+rename replaces in-place
   edits. Note the divergence and why it is behavior-equivalent.
5. Tests: table-driven, `runner.NewFake` asserting exact argv
   sequences (byte-exact for security-relevant argv like
   systemd-cryptenroll), fixture trees under `t.TempDir()`. Never run
   real commands.
6. Verify with `make check`, then prove it on hardware with the
   relevant E2E — unit tests repeatedly missed what only a real boot
   catches (wrong baseline path, runtime-invisible writes).

## Pitfalls (each one was hit for real)

- **Deployment-vs-runtime paths**: what you write under a bootc
  deployment root is not necessarily what the booted system sees —
  `/root` is stateroot `var/roothome`, homes live in the stateroot
  var, composefs has no materialized `/usr`. Check where the BOOTED
  system reads before choosing a write location.
- **A/B baseline /etc** is at the erofs root's `/.etc.lower`, not
  `/etc` (the runtime /etc is an overlay; on-disk it is empty).
- Parent behavior can be dead weight: snosi resized a filesystem it
  immediately reformatted; fisherman "downloaded" flatpaks in a doc
  comment only. Port what the code DOES, verify claims against the
  code, and drop dead work with a comment.
- Recipe-driven divergences (ADR-0008 var filesystems, join-where-
  exists groups) beat parent-exact ports when the parent's assumption
  conflicts with firn's recipe contract — record those in the spec.
