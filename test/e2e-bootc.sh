#!/usr/bin/env bash
# E2E: recipe-driven bootc install onto a loop device, then boot the
# result under QEMU/OVMF and wait for a login prompt.
#
# Modeled on fisherman's "bootcrew" harness (frostyard/fisherman,
# justfile, GPL-3.0-only), reduced to firn's phase-3 scope.
#
# Requirements: root, qemu-system-x86_64, qemu-img, OVMF firmware,
# podman, and the usual disk tools (sfdisk, cryptsetup, mkfs.*).
# Roughly 20 GiB of scratch space.
#
# Usage: sudo test/e2e-bootc.sh [recipe.toml]
#   FIRN_E2E_IMAGE   image to install (default ghcr.io/frostyard/cayo:latest)
#   FIRN_E2E_DIR     scratch dir (default: mktemp -d)
#   FIRN_E2E_TIMEOUT seconds to wait for the login prompt (default 300)
set -euo pipefail

[[ $EUID -eq 0 ]] || { echo "e2e: must run as root" >&2; exit 1; }

here=$(cd "$(dirname "$0")/.." && pwd)
image=${FIRN_E2E_IMAGE:-ghcr.io/frostyard/cayo:latest}
work=${FIRN_E2E_DIR:-$(mktemp -d /var/tmp/firn-e2e.XXXXXX)}
timeout=${FIRN_E2E_TIMEOUT:-300}
hostname=frn-e2e

ovmf_code=""
for c in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd /usr/share/edk2/x64/OVMF_CODE.4m.fd; do
  [[ -f $c ]] && ovmf_code=$c && break
done
[[ -n $ovmf_code ]] || { echo "e2e: OVMF firmware not found" >&2; exit 1; }

cleanup() {
  [[ -n ${loop:-} ]] && losetup -d "$loop" 2>/dev/null || true
}
trap cleanup EXIT

echo "e2e: building firn"
if command -v go >/dev/null 2>&1; then
  (cd "$here" && go build -o "$work/firn" ./cmd/firn-cli)
elif [[ -x $here/build/firn || -x $here/firn ]]; then
  # Root often lacks the user's go toolchain (e.g. linuxbrew); a
  # pre-built ./firn from `just build` works fine.
  cp "$(ls "$here/build/firn" "$here/firn" 2>/dev/null | head -1)" "$work/firn"
else
  echo "e2e: go not on PATH and no prebuilt ./firn — run 'make build' first" >&2
  exit 1
fi

echo "e2e: creating 20G disk image + loop device"
truncate -s 20G "$work/disk.raw"
loop=$(losetup --find --show --partscan "$work/disk.raw")
ssh-keygen -t ed25519 -N "" -f "$work/id_e2e" -C firn-e2e >/dev/null

recipe=${1:-}
if [[ -z $recipe ]]; then
  recipe=$work/recipe.toml
  cat >"$recipe" <<EOF
version = 1

[image]
family = "bootc"
ref = "$image"

[target]
disk = "$loop"
filesystem = "btrfs"
btrfs_subvolumes = true

[security]
encryption = "none"

[system]
hostname = "$hostname"
locale = "en_US.UTF-8"
timezone = "America/Chicago"
keyboard = "us"
flatpaks = []
root_ssh_authorized_key_file = "$work/id_e2e.pub"

[system.user]
name = "e2e"
password_hash = "\$6\$firn.e2e\$XjSAJP9d3TXbJ4wIcZarBOUpAo6yLh4uYUniEcpKPGqAe7EfWbrKZOfjfHiZ0KOhSjrqAGdRhrGxU0aTsTfW/1"
groups = ["sudo"]
ssh_authorized_key_file = "$work/id_e2e.pub"
EOF
fi

echo "e2e: installing $image to $loop"
"$work/firn" install --uefi on --confirm "$loop" --json-progress "$recipe" | tee "$work/progress.ndjson"

grep -q '"event":"done","ok":true' "$work/progress.ndjson" \
  || { echo "e2e: install did not complete" >&2; exit 1; }

# Make the installed system speak on the serial console: without a
# console=ttyS0 karg the kernel and getty are silent on -nographic
# QEMU even when the boot succeeds (observed: firmware handoff then
# 240 bytes of serial silence over a perfectly healthy disk).
echo "e2e: enabling serial console in the boot entry"
esp=$(mktemp -d)
mount "${loop}p1" "$esp"
sed -i 's/^options /options console=ttyS0,115200 /' "$esp"/loader/entries/*.conf
umount "$esp" && rmdir "$esp"

losetup -d "$loop"; loop=""

echo "e2e: booting under QEMU (SSH probe on port 2225, up to ${timeout}s)"
cp "$ovmf_code" "$work/code.fd"
vars=${ovmf_code/CODE/VARS}
cp "$vars" "$work/vars.fd" 2>/dev/null || truncate -s "$(stat -c%s "$work/code.fd")" "$work/vars.fd"

sshport=2225
qemu_pid=""
trap '[[ -n ${loop:-} ]] && losetup -d "$loop" 2>/dev/null; [[ -n $qemu_pid ]] && kill $qemu_pid 2>/dev/null' EXIT
qemu-system-x86_64 \
  -m 4096 -smp 2 -enable-kvm -cpu host \
  -drive if=pflash,format=raw,readonly=on,file="$work/code.fd" \
  -drive if=pflash,format=raw,file="$work/vars.fd" \
  -drive file="$work/disk.raw",format=raw,if=virtio \
  -nic "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:$sshport-:22" \
  -display none -serial file:"$work/console.log" &
qemu_pid=$!

sshopts=(-i "$work/id_e2e" -p "$sshport" -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o BatchMode=yes)
deadline=$((SECONDS + timeout)); up=0
while ((SECONDS < deadline)); do
  ssh "${sshopts[@]}" root@127.0.0.1 true 2>/dev/null && { up=1; break; }
  kill -0 "$qemu_pid" 2>/dev/null || { echo "e2e: VM exited early" >&2; break; }
  sleep 5
done
((up)) || { echo "e2e: FAIL — no SSH by ${timeout}s (console: $work/console.log)" >&2; exit 1; }

fail=0
check() { if [[ $3 == *"$2"* ]]; then echo "e2e: ok   $1 = $3"; else echo "e2e: FAIL $1 = $3 (want $2)" >&2; fail=1; fi; }
check hostname "$hostname" "$(ssh "${sshopts[@]}" root@127.0.0.1 hostname)"
check user "uid=1000(e2e)" "$(ssh "${sshopts[@]}" root@127.0.0.1 id e2e)"
check groups sudo "$(ssh "${sshopts[@]}" root@127.0.0.1 id -Gn e2e)"
check locale "LANG=en_US.UTF-8" "$(ssh "${sshopts[@]}" root@127.0.0.1 cat /etc/locale.conf)"
check timezone America/Chicago "$(ssh "${sshopts[@]}" root@127.0.0.1 readlink /etc/localtime)"
check keyboard 'XKBLAYOUT="us"' "$(ssh "${sshopts[@]}" root@127.0.0.1 cat /etc/default/keyboard)"
check user-ssh-key firn-e2e "$(ssh "${sshopts[@]}" root@127.0.0.1 cat /var/home/e2e/.ssh/authorized_keys)"

ssh "${sshopts[@]}" root@127.0.0.1 systemctl poweroff 2>/dev/null || true
wait "$qemu_pid" 2>/dev/null || true; qemu_pid=""

((fail == 0)) || { echo "e2e: FAIL (details above; $work)" >&2; exit 1; }
echo "e2e: PASS — $hostname booted; full config matrix verified over SSH ($work)"
