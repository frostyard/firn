#!/usr/bin/env bash
# E2E: drive the REAL firn TUI wizard inside a throwaway QEMU guest via
# tmux, install cayo-ab to an NVMe target disk, then boot and verify it.
#
# WHY NESTED (ADR-0009, docs/adr/0009-ab-installs-require-partition-
# isolation.md): this test installs an A/B image, so it MUST run nested,
# exactly like test/e2e-ab.sh. The streamed snosi whole-disk image
# carries the same Discoverable-Partitions type GUIDs and labels
# (esp/var/root) as a snosi A/B host's own disk; on a host-visible loop
# device the HOST's udev acts on the cloned partitions (it tore down a
# live GNOME session on a snow-ab dev box, 2026-08-11). Inside a VM the
# target is a virtual NVMe disk the host kernel never scans — safe by
# construction.
#
# Flow: boot a Debian cloud guest with the blank NVMe target as a second
# disk; install tmux; run `sudo /tmp/firn` (bare — the TUI) in an
# 80x24 tmux pane and script the wizard with a driver (send-keys +
# capture-pane polling, never blind sleeps). After the install view
# finishes: save the generated /run/firn/session-*/recipe.toml, assert `firn
# validate` accepts it in the guest (validation-level headless reuse;
# the family engine E2Es separately cover execution; in-guest because its
# *_file secrets live on the
# guest's tmpfs), scp it out for the record, then boot the target disk
# and verify hostname + user over SSH with the root key the driver
# pasted into the wizard.
#
# Requirements: qemu-system-x86_64 (KVM), OVMF, genisoimage, curl, ssh.
# Network (host + guest NAT). ~30 GiB scratch. Usage: sudo test/e2e-tui.sh
#   FIRN_E2E_DIR   FIRN_E2E_TIMEOUT (default 600)
set -euo pipefail

[[ $EUID -eq 0 ]] || { echo "e2e-tui: must run as root (qemu KVM + disk images)" >&2; exit 1; }

here=$(cd "$(dirname "$0")/.." && pwd)
work=${FIRN_E2E_DIR:-$(mktemp -d /var/tmp/firn-e2e-tui.XXXXXX)}
timeout=${FIRN_E2E_TIMEOUT:-600}
# Default under /var: on a snosi A/B host root (and /root) is read-only
# erofs, so $HOME/.cache is unwritable under sudo.
cache=${FIRN_E2E_CACHE:-/var/tmp/firn-e2e-cache}
hostname=frn-tui-e2e
# Which family the wizard installs: ab (default) or bootc. Run both to
# satisfy the phase Done-when.
family=${FIRN_E2E_TUI_FAMILY:-ab}
case $family in
  ab|bootc) ;;
  *) echo "e2e-tui: FIRN_E2E_TUI_FAMILY must be ab or bootc" >&2; exit 2 ;;
esac
# A mixed fixture exercises the family picker; a single-family fixture proves
# the wizard skips that page rather than offering an unavailable family.
catalog_mode=${FIRN_E2E_TUI_CATALOG:-mixed}
case $catalog_mode in
  mixed|single) ;;
  *) echo "e2e-tui: FIRN_E2E_TUI_CATALOG must be mixed or single" >&2; exit 2 ;;
esac
sshport=2225
inst_port=2226
base_url=https://cloud.debian.org/images/cloud/trixie/latest
base_img=debian-13-genericcloud-amd64.qcow2

ovmf_code=""
for c in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd /usr/share/edk2/x64/OVMF_CODE.4m.fd; do
  [[ -f $c ]] && ovmf_code=$c && break
done
ovmf_vars=${ovmf_code/CODE/VARS}
[[ -n $ovmf_code && -f $ovmf_vars ]] || { echo "e2e-tui: OVMF firmware not found" >&2; exit 1; }

qemu_pid=""
cleanup() { [[ -n $qemu_pid ]] && kill "$qemu_pid" 2>/dev/null || true; }
trap cleanup EXIT

mkdir -p "$cache" "$work"

echo "e2e-tui: providing a static firn for the guest"
if command -v go >/dev/null 2>&1; then
  (cd "$here" && CGO_ENABLED=0 go build -o "$work/firn" ./cmd/firn-cli)
elif [[ -x $here/build/firn || -x $here/firn ]]; then
  # Root often lacks the user's go toolchain; a prebuilt ./firn from
  # `just build` works. The guest runs it, not the host (host only
  # needs it for the final `firn validate`).
  cp "$(ls "$here/build/firn" "$here/firn" 2>/dev/null | head -1)" "$work/firn"
else
  echo "e2e-tui: go not on PATH and no prebuilt ./firn — run 'make build' first" >&2
  exit 1
fi

if [[ ! -f $cache/$base_img ]]; then
  echo "e2e-tui: fetching Debian cloud image (once)"
  curl -fSL --retry 3 -o "$cache/$base_img.tmp" "$base_url/$base_img"
  mv "$cache/$base_img.tmp" "$cache/$base_img"
fi

echo "e2e-tui: preparing installer-env overlay + blank target disk"
qemu-img create -f qcow2 -F qcow2 -b "$cache/$base_img" "$work/installer.qcow2" 20G >/dev/null
truncate -s 30G "$work/target.raw"
# One keypair serves both roles: the host drives the installer guest
# with it (cloud-init), and the driver pastes its pubkey into the
# wizard's root-key page so the verify phase can SSH into the target.
ssh-keygen -t ed25519 -N "" -f "$work/id_e2e" -C firn-e2e >/dev/null
cp /usr/lib/systemd/import-pubring.gpg "$work/pubring.gpg" 2>/dev/null \
  || cp /usr/lib/snosi/os-update-pubring.gpg "$work/pubring.gpg"

# The production ISO ships cosign plus /usr/lib/snosi/cosign.pub. This Debian
# guest is only a TUI/pipeline driver, so provide a strict test double that
# accepts exactly Firn's digest-pinned argv. Unit tests exercise real success,
# bad-signature and wrong-key outcomes through the runner seam.
cat >"$work/cosign" <<'COSIGN'
#!/usr/bin/env bash
set -euo pipefail
[[ $# == 4 && $1 == verify && $2 == --key && -s $3 && $4 =~ @sha256:[0-9a-f]{64}$ ]] || {
  echo "e2e-tui cosign double: expected verify --key KEY IMAGE@sha256:DIGEST, got: $*" >&2
  exit 2
}
COSIGN
chmod 0755 "$work/cosign"
printf '%s\n' 'e2e-tui public-key placeholder' >"$work/cosign.pub"
if [[ $catalog_mode == single && $family == bootc ]]; then
  printf '%s\n' '[{"family":"bootc","name":"cayo","description":"E2E bootc image","ref":"ghcr.io/frostyard/cayo:latest","cosign_pub_key":"/usr/lib/snosi/cosign.pub"}]' >"$work/catalog.json"
elif [[ $catalog_mode == single ]]; then
  printf '%s\n' '[{"family":"ab","name":"cayo-ab","description":"E2E A/B image","product":"cayo-ab"}]' >"$work/catalog.json"
else
  printf '%s\n' '[{"family":"bootc","name":"cayo","description":"E2E bootc image","ref":"ghcr.io/frostyard/cayo:latest","cosign_pub_key":"/usr/lib/snosi/cosign.pub"},{"family":"ab","name":"cayo-ab","description":"E2E A/B image","product":"cayo-ab"}]' >"$work/catalog.json"
fi

# The tmux driver script, run INSIDE the guest. Quoted heredoc: nothing
# here is host-expanded; the pubkey is read in-guest from /tmp/id_e2e.pub.
cat >"$work/driver.sh" <<'DRIVER'
#!/usr/bin/env bash
# Drives the REAL firn TUI in an 80x24 tmux pane. Runs inside the guest.
#
# PAGE SCRIPT CONTRACT: the expect/send pairs under "wizard page script"
# are the integration contract with internal/tui's wizard pages — when a
# page's title text or field order changes, update this script in the
# same change. Every send-keys is gated on expect_screen; there are no
# blind sleeps.
set -uo pipefail

S=firn
INSTALL_TIMEOUT=${INSTALL_TIMEOUT:-540}
FAMILY=${FAMILY:-ab}
CATALOG_MODE=${CATALOG_MODE:-mixed}

cap() { tmux capture-pane -pt "$S" 2>/dev/null || true; }

fail() {
  echo "driver: FAIL — $*" >&2
  echo "driver: ---- last screen ----" >&2
  cap >&2
  echo "driver: ---------------------" >&2
  exit 1
}

# expect_screen REGEX [TRIES]: poll the pane once per second until the
# (case-insensitive, extended) regex matches, or fail with a screen dump.
expect_screen() {
  local want=$1 tries=${2:-40} i
  for ((i = 0; i < tries; i++)); do
    cap | grep -Eiq "$want" && return 0
    tmux has-session -t "$S" 2>/dev/null || fail "TUI exited while waiting for '$want'"
    sleep 1
  done
  fail "never saw '$want'"
}

# choose REGEX: walk a select list Down until the cursor line ('>' or
# '❯' marker, huh/bubbles convention) matches, then Enter. Robust
# against option order and prefilled defaults.
choose() {
  local want=$1 i
  for ((i = 0; i < 30; i++)); do
    # huh renders a group border (┃/│) before the cursor marker.
    if cap | grep -Eiq "^[┃│|[:space:]]*[>❯›].*${want}"; then
      tmux send-keys -t "$S" Enter
      sleep 0.5
      return 0
    fi
    tmux send-keys -t "$S" Down
    sleep 0.3
  done
  fail "never highlighted '$want' in a select list"
}

# choose_disk REGEX PATH: select the target and distinguish a wizard refusal
# from an ordinary page-gate desync. A refused huh Select stays on this page
# and renders diskChoiceError below the picker.
choose_disk() {
  local want=$1 path=$2 i
  for ((i = 0; i < 30; i++)); do
    if cap | grep -Eiq "^[┃│|[:space:]]*[>❯›].*${want}"; then
      tmux send-keys -t "$S" Enter
      for ((i = 0; i < 20; i++)); do
        sleep 0.25
        if cap | grep -Eiq "cannot install to[[:space:]]+${path}(:|[[:space:]]|$)"; then
          fail "wizard refused target disk $path; verify the nested VM disk layout and refusal reason above"
        fi
        cap | grep -Eiq 'Target disk' || return 0
      done
      fail "target disk $path was selected but the wizard did not leave the disk page"
    fi
    tmux send-keys -t "$S" Down
    sleep 0.3
  done
  fail "target disk $path was not selectable; verify the nested VM exposes the expected blank virtio disk"
}

# type_input TITLE TEXT: gate on a huh.NewInput title, clear the focused
# single-line input (ctrl-u), type TEXT literally, then Enter.
type_input() {
  expect_screen "$1"
  tmux send-keys -t "$S" C-u
  tmux send-keys -t "$S" -l "$2"
  tmux send-keys -t "$S" Enter
  sleep 0.5
}

root_pubkey=$(cat /tmp/id_e2e.pub) || fail "missing /tmp/id_e2e.pub"

# type_textarea TITLE TEXT: gate on a huh.NewText title and type into its
# initially empty textarea. In the pinned huh v1.0.0 keymap Enter advances;
# Alt+Enter or Ctrl+J inserts a newline. Do not treat it as NewInput: ctrl-u
# only clears before the cursor on the current textarea line.
type_textarea() {
  expect_screen "$1"
  tmux send-keys -t "$S" -l "$2"
  tmux send-keys -t "$S" Enter
  sleep 0.5
}

# accept_field TITLE: pin every default/empty-field acceptance by its visible
# title. This makes field insertion or reordering fail at a named contract
# boundary instead of silently shifting a skip count.
accept_field() {
  expect_screen "$1"
  tmux send-keys -t "$S" Enter
  sleep 0.4
}

tmux kill-server 2>/dev/null || true
# The wrapper keeps the pane alive after firn exits so the driver can
# read the exit status from the screen.
tmux new-session -d -s "$S" -x 80 -y 24 'sudo /tmp/firn; echo "FIRN-EXIT:$?"; sleep 600' \
  || fail "could not start tmux session"

# ---- wizard page script (contract with internal/tui/wizard_pages.go;
# page titles and field order must stay in sync) ----
expect_screen 'snosi installer'        # welcome note
# This harness intentionally uses non-Secure-Boot OVMF and therefore does not
# exercise the conditional MOK page. Assert the engine's probe result before
# sending the first key so a firmware/configuration change fails actionably.
cap | grep -Eiq 'Secure Boot inactive' \
  || fail 'e2e-tui requires Secure Boot inactive; the welcome page reported a different state (MOK flow is not scripted)'
tmux send-keys -t "$S" Enter
if [[ $CATALOG_MODE == mixed ]]; then
  expect_screen 'Update mechanism'     # family guidance: represented families only
  if [[ $FAMILY == bootc ]]; then
    choose 'long-term path'
  else
    choose 'proven path'
  fi
fi
expect_screen '^[┃│|[:space:]]*Image[[:space:]]*$' # stable image-page title
if [[ $FAMILY == bootc ]]; then
  choose 'cayo[[:space:]]+\(bootc image\)'
else
  choose 'cayo-ab[[:space:]]+\(A/B image\)'
fi
expect_screen 'Advanced image options'
# Shift-Tab is wizard-level back navigation, not merely field navigation.
tmux send-keys -t "$S" BTab
expect_screen '^[┃│|[:space:]]*Image[[:space:]]*$'
if [[ $FAMILY == bootc ]]; then
  choose 'cayo[[:space:]]+\(bootc image\)'
else
  choose 'cayo-ab[[:space:]]+\(A/B image\)'
fi
expect_screen 'Advanced image options'
accept_field 'Advanced image options' # keep catalog/default image policy
expect_screen 'Target disk'            # vda=installer, nvme0n1=blank target, vdb=seed
expect_screen 'FIRN_E2E_TARGET'         # hardware serial must be visible in the picker
choose_disk 'nvme0n1' '/dev/nvme0n1'
if [[ $FAMILY == bootc ]]; then
  expect_screen 'Root filesystem'
  choose 'btrfs'
  accept_field 'Create btrfs subvolumes' # initially Yes
else
  expect_screen '/var filesystem'      # A/B: only /var is variable
  choose 'ext4'
fi
expect_screen 'Disk encryption'        # SB inactive in this VM: no MOK group
choose 'none'
# System form: Hostname, Locale, Timezone, Keyboard, Root SSH key — one
# form; named gates pin its declaration order.
type_input 'Hostname' 'frn-tui-e2e'
accept_field 'Locale'                  # empty: keep image default
accept_field 'Timezone'                # empty: keep image default
accept_field 'Keyboard layout'         # empty: keep image default
type_textarea 'Root SSH authorized key' "$root_pubkey"
# User form: create? (default Yes) → username, full name, password x2,
# groups multi-select (sudo preselected), extra groups, user SSH key.
accept_field 'Create a user account'  # initially Yes
type_input 'Username' 'e2e'
accept_field 'Full name'               # optional
type_input '^.*Password' 'firn-e2e-pw'
type_input 'Confirm password' 'firn-e2e-pw'
accept_field '^.*Groups'               # sudo preselected
accept_field 'Additional groups'
accept_field 'User SSH authorized key' # empty huh.NewText
# Flatpaks: core-set confirm + IDs. The schema and TUI both default this
# optional feature to false, so Enter preserves the focused No answer.
accept_field 'core app set'            # No (cayo has no runtime)
accept_field 'Extra Flatpak apps'      # empty huh.NewText
# Review: with a long recipe the page TITLE scrolls off the 24-row pane;
# gate on the action list, which is always visible at the bottom.
expect_screen 'Quit without installing'
tmux send-keys -t "$S" Enter           # Install is the focused first action
sleep 0.5
expect_screen 'Point of no return'     # typed disk confirmation
type_input 'Point of no return' '/dev/nvme0n1'

# The install view deliberately holds secret disclosure and terminal
# screens. A recovery-key install must acknowledge the key first; only
# then can the success/failure screen be dismissed. Every keypress is
# gated on the screen it acts on so a slow pipeline cannot be skipped.
expect_screen 'RECOVERY KEY|install (complete|failed)' "$INSTALL_TIMEOUT"
if cap | grep -Eiq 'RECOVERY KEY'; then
  tmux send-keys -t "$S" Enter         # confirm the key was saved
  sleep 0.5
fi
expect_screen 'install (complete|failed)' "$INSTALL_TIMEOUT"
cap >/tmp/tui-final-screen.txt
tmux send-keys -t "$S" Enter           # dismiss the held terminal screen

# The wrapper can print the exit status only after the final screen exits.
expect_screen 'FIRN-EXIT:' 40
cap | grep -q 'FIRN-EXIT:0' || fail "firn exited nonzero"

# Save the generated recipe BEFORE the guest goes away — the host asserts
# `firn validate` accepts this exact artifact. The user password is guaranteed
# by this flow, so also pin the per-session containment and permission contract.
recipe=$(sudo find /run/firn -mindepth 2 -maxdepth 2 -type f -name recipe.toml -print -quit)
[[ -n $recipe ]] || fail "no generated recipe under /run/firn/session-*"
session=${recipe%/*}
secret=$(sudo sed -n 's/^[[:space:]]*password_file = "\(.*\)"$/\1/p' "$recipe" | head -1)
[[ $secret == "$session/"* ]] || fail "recipe secret $secret escaped session $session"
[[ $(sudo stat -c %a "$session") == 700 ]] || fail "session directory is not mode 0700"
[[ $(sudo stat -c %a "$recipe") == 600 ]] || fail "generated recipe is not mode 0600"
[[ $(sudo stat -c %a "$secret") == 600 ]] || fail "generated secret is not mode 0600"
sudo cp "$recipe" /tmp/recipe-out.toml && sudo chmod 644 /tmp/recipe-out.toml \
  || fail "could not save generated recipe"

tmux kill-server 2>/dev/null || true
echo "driver: OK"
DRIVER

# cloud-init NoCloud seed: inject the driving key so the host can run
# the driver over SSH (far more debuggable than fire-and-forget runcmd).
cat >"$work/meta-data" <<EOF
instance-id: firn-tui-e2e
local-hostname: firn-installer
EOF
cat >"$work/user-data" <<EOF
#cloud-config
ssh_authorized_keys:
  - $(cat "$work/id_e2e.pub")
EOF
genisoimage -quiet -output "$work/seed.iso" -volid cidata -joliet -rock \
  "$work/user-data" "$work/meta-data"

# --- Install phase: script the TUI over SSH inside a throwaway VM. ---
sshopts=(-i "$work/id_e2e" -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o BatchMode=yes)
gssh() { ssh "${sshopts[@]}" -p "$inst_port" debian@127.0.0.1 "$@"; }
gscp() { scp "${sshopts[@]}" -P "$inst_port" "$@"; }

cp "$ovmf_vars" "$work/vars.fd"
echo "e2e-tui: booting installer VM (the TUI installs cayo for family $family to the guest's /dev/nvme0n1)"
qemu-system-x86_64 \
  -m 4096 -smp 2 -enable-kvm -cpu host \
  -drive if=pflash,format=raw,readonly=on,file="$ovmf_code" \
  -drive if=pflash,format=raw,file="$work/vars.fd" \
  -drive if=none,id=firn-installer,file="$work/installer.qcow2",format=qcow2 \
  -device virtio-blk-pci,drive=firn-installer,bootindex=1 \
  -drive if=none,id=firn-target,file="$work/target.raw",format=raw \
  -device nvme,drive=firn-target,serial=FIRN_E2E_TARGET \
  -drive if=none,id=firn-seed,file="$work/seed.iso",format=raw,readonly=on \
  -device virtio-blk-pci,drive=firn-seed \
  -nic "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:$inst_port-:22" \
  -display none -serial file:"$work/installer-console.log" &
qemu_pid=$!

echo "e2e-tui: waiting for installer VM SSH"
deadline=$((SECONDS + timeout)); up=0
while ((SECONDS < deadline)); do
  gssh true 2>/dev/null && { up=1; break; }
  kill -0 "$qemu_pid" 2>/dev/null || { echo "e2e-tui: installer VM exited early" >&2; break; }
  sleep 5
done
((up)) || { echo "e2e-tui: FAIL — installer VM never reachable over SSH (console: $work/installer-console.log)" >&2; exit 1; }

echo "e2e-tui: staging firn + driver into the installer VM"
gscp "$work/firn" "$work/pubring.gpg" "$work/cosign" "$work/cosign.pub" "$work/catalog.json" "$work/id_e2e.pub" "$work/driver.sh" debian@127.0.0.1:/tmp/ >/dev/null
# tmux drives the TUI; xz/gpgv are the A/B pipeline's tools. The
extra_pkgs=""
[[ $family == bootc ]] && extra_pkgs="podman skopeo btrfs-progs dosfstools parted"
# pubring goes to firn's first default search location so the bare
# `firn` invocation needs no flags at all (as on real installer media).
gssh "sudo DEBIAN_FRONTEND=noninteractive sh -c 'apt-get update -q && apt-get install -y -q tmux xz-utils gpgv $extra_pkgs' \
  && sudo install -D -m 0644 /tmp/pubring.gpg /usr/lib/snosi/os-update-pubring.gpg \
  && sudo install -D -m 0644 /tmp/cosign.pub /usr/lib/snosi/cosign.pub \
  && sudo install -D -m 0644 /tmp/catalog.json /etc/firn/catalog.json \
  && sudo install -m 0755 /tmp/cosign /usr/local/bin/cosign" >/dev/null 2>&1 || {
  echo "e2e-tui: FAIL — could not prepare the guest (tmux/tools/trust roots)" >&2; exit 1; }

echo "e2e-tui: driving the TUI wizard inside the VM (tmux, 80x24, family $family, catalog $catalog_mode)"
set +e
gssh "INSTALL_TIMEOUT=$timeout FAMILY=$family CATALOG_MODE=$catalog_mode bash /tmp/driver.sh" >"$work/driver.log" 2>&1
drc=$?
set -e
# Pull debug artifacts regardless of outcome.
gscp debian@127.0.0.1:/tmp/tui-final-screen.txt "$work/tui-final-screen.txt" >/dev/null 2>&1 || true
if ((drc != 0)); then
  echo "e2e-tui: FAIL — TUI driver exited $drc inside the guest" >&2
  echo "---- driver.log (tail, includes last screen dump) ----" >&2
  tail -40 "$work/driver.log" >&2
  exit 1
fi
echo "e2e-tui: TUI install OK — final screen in $work/tui-final-screen.txt"

echo "e2e-tui: retrieving the generated recipe (reproduce-headless artifact)"
gscp debian@127.0.0.1:/tmp/recipe-out.toml "$work/recipe-out.toml" >/dev/null

# The wizard's written recipe must be reusable headless: `firn install
# --confirm /dev/nvme0n1 <recipe>` in this same environment. Assert that to
# the validation level (a full second headless install is e2e-ab.sh's
# job) — IN THE GUEST, because the wizard stores interactive secrets as
# *_file paths under /run/firn (spec rule 6) and validation fail-closed
# requires those files to exist. Must run before poweroff: /run is tmpfs.
echo "e2e-tui: validating the generated recipe inside the guest"
gssh "sudo /tmp/firn validate --secure-boot off --tpm off /tmp/recipe-out.toml" || {
  echo "e2e-tui: FAIL — wizard-generated recipe does not validate" >&2
  cat "$work/recipe-out.toml" >&2
  exit 1
}
grep -q "family = \"$family\"" "$work/recipe-out.toml" \
  || { echo "e2e-tui: FAIL — generated recipe is not family $family" >&2; exit 1; }
if grep -Eq '^[[:space:]]*(locale|timezone|keyboard)[[:space:]]*=' "$work/recipe-out.toml"; then
  echo "e2e-tui: FAIL — skipped system fields must preserve image defaults" >&2
  cat "$work/recipe-out.toml" >&2
  exit 1
fi

gssh sudo poweroff 2>/dev/null || true
wait "$qemu_pid" 2>/dev/null || true; qemu_pid=""

# --- Verify phase: boot the installed disk, check over SSH. ---
echo "e2e-tui: booting the INSTALLED disk (SSH probe on $sshport, up to ${timeout}s)"
cp "$ovmf_vars" "$work/vars2.fd"
qemu-system-x86_64 \
  -m 4096 -smp 2 -enable-kvm -cpu host \
  -drive if=pflash,format=raw,readonly=on,file="$ovmf_code" \
  -drive if=pflash,format=raw,file="$work/vars2.fd" \
  -drive file="$work/target.raw",format=raw,if=virtio \
  -nic "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:$sshport-:22" \
  -display none -serial file:"$work/installed-console.log" &
qemu_pid=$!

sshopts=(-i "$work/id_e2e" -p "$sshport" -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o BatchMode=yes)
deadline=$((SECONDS + timeout)); up=0
while ((SECONDS < deadline)); do
  ssh "${sshopts[@]}" root@127.0.0.1 true 2>/dev/null && { up=1; break; }
  kill -0 "$qemu_pid" 2>/dev/null || { echo "e2e-tui: installed VM exited early" >&2; break; }
  sleep 5
done
if ((!up)); then
  echo "e2e-tui: FAIL — installed disk never reachable over SSH (console: $work/installed-console.log)" >&2
  echo "     (root login works only if the wizard's root-key page took the pasted key)" >&2
  exit 1
fi

fail=0
check() { if [[ $3 == *"$2"* ]]; then echo "e2e-tui: ok   $1 = $3"; else echo "e2e-tui: FAIL $1 = $3 (want $2)" >&2; fail=1; fi; }
check hostname "$hostname" "$(ssh "${sshopts[@]}" root@127.0.0.1 hostname)"
check user "uid=1000(e2e)" "$(ssh "${sshopts[@]}" root@127.0.0.1 id e2e)"

ssh "${sshopts[@]}" root@127.0.0.1 poweroff 2>/dev/null || true
wait "$qemu_pid" 2>/dev/null || true; qemu_pid=""

((fail == 0)) || { echo "e2e-tui: FAIL (details above; $work)" >&2; exit 1; }
echo "e2e-tui: PASS — the wizard installed $family (cayo) inside a VM, the disk boots, and the generated recipe validates ($work)"
