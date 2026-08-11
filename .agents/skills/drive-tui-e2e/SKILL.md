---
name: drive-tui-e2e
description: Extend or debug the tmux-driven TUI end-to-end test (test/e2e-tui.sh), which scripts firn's real wizard screen by screen inside a nested VM. Use whenever a wizard page changes, a new TUI flow needs E2E coverage, or `make e2e-tui` fails and the expect script must be re-synced.
---

# Drive firn's TUI end-to-end with tmux

Goal: `make e2e-tui` (and `FIRN_E2E_TUI_FAMILY=bootc` for the other
family) walks the real wizard via `tmux send-keys`/`capture-pane`,
installs to a virtio disk inside a nested VM, and verifies the booted
system. Done = PASS lines for both families.

The driver script embedded in `test/e2e-tui.sh` is a **contract with
`internal/tui/wizard_pages.go`**: any change to page titles, field
order, or option labels must update the driver in the same change.

## Steps

1. Read the current page flow in `internal/tui/wizard.go` (`run`) and
   the page titles in `internal/tui/wizard_pages.go` — the driver's
   `expect_screen` strings must match text that is actually visible.
2. Edit the "wizard page script" section of the driver heredoc in
   `test/e2e-tui.sh` using only its helpers:
   - `expect_screen REGEX [tries]` — gate EVERY send on this; never a
     blind sleep alone.
   - `choose REGEX` — walks a select with Down until the cursor line
     matches, then Enter. Match on stable substrings of the option
     label.
   - `type_line TEXT` — clears the focused input (C-u), types, Enter.
   - `skip_field` — Enter to accept the focused field and advance.
3. Run one family, read the result, fix, then run the other:
   `sudo test/e2e-tui.sh` (ab) and
   `sudo FIRN_E2E_TUI_FAMILY=bootc FIRN_E2E_TIMEOUT=900 test/e2e-tui.sh`.
4. Verify: both runs end `e2e-tui: PASS`; the generated recipe passed
   `firn validate` in-guest; the booted disk answered over SSH.

## Debugging a failure

- The harness copies out `driver.log` (ends with a full screen dump at
  the moment of failure) and `tui-final-screen.txt` into the work dir
  (`/var/tmp/firn-e2e-tui.*`). Read the screen dump FIRST — it almost
  always shows exactly which page the driver desynced on.
- `installer-console.log` in the work dir covers guest-boot problems.

## Pitfalls (each one was hit for real)

- **huh's cursor line starts with a group border** (`┃`/`│`), so match
  cursor lines with `^[┃│|[:space:]]*[>❯›]`, never `^\s*>`.
- **Enter on a huh Confirm accepts the FOCUSED (affirmative) button**,
  not the field's default value — declining needs `Right` then Enter.
- **Long pages scroll their title off the 24-row pane** (e.g. the
  review page's TOML). Gate on text near the BOTTOM of the page (the
  action list), not the title.
- **The pane dies when firn exits** unless wrapped:
  `'sudo /tmp/firn; echo "FIRN-EXIT:$?"; sleep 600'` — then gate the
  install wait on `FIRN-EXIT:` and assert `FIRN-EXIT:0`.
- **huh note descriptions render as markdown**: paired `_`/`*` vanish
  as emphasis. The review page escapes them (`wizard_pages.go`,
  `reviewForm`); keep that when touching the review text.
- **Preflight tool demands must exist in the guest**: the bootc family
  needs `podman skopeo btrfs-progs dosfstools parted` apt-installed
  (`partprobe` lives in `parted` on Debian). A preflight failure shows
  up as a clean install-view error, not a driver desync.
- The wizard must run **nested** (it can install A/B images):
  [ADR-0009](../../../docs/adr/0009-ab-installs-require-partition-isolation.md).
