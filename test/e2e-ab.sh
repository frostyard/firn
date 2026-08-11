#!/usr/bin/env bash
# E2E: recipe-driven A/B install, run entirely INSIDE a throwaway QEMU
# guest so the host never sees the image's partitions.
#
# WHY NESTED: firn installs an A/B image by streaming a snosi whole-disk
# image and then rescanning its partitions. That image carries the same
# Discoverable-Partitions type GUIDs and labels (esp/var/root) as a snosi
# A/B host's own disk, so doing it against a host-visible loop device
# makes the HOST's udev act on the cloned partitions (it tore down a live
# GNOME session on a snow-ab dev box, 2026-08-11). Inside a VM the target
# is a virtio disk the host kernel never scans — safe by construction.
#
# Flow: boot a Debian cloud image (installer env) with the blank target
# as a second virtio disk and firn shared in over 9p; cloud-init runs
# firn install against /dev/vdb, then a second QEMU boots that installed
# disk and verifies over SSH.
#
# Requirements: qemu-system-x86_64 (KVM), OVMF, genisoimage, curl, ssh.
# Network (host + guest NAT). ~30 GiB scratch. Usage: sudo test/e2e-ab.sh
#   FIRN_E2E_PRODUCT (default cayo-ab)   FIRN_E2E_DIR   FIRN_E2E_TIMEOUT (default 600)
set -euo pipefail

[[ $EUID -eq 0 ]] || { echo "e2e: must run as root (qemu KVM + disk images)" >&2; exit 1; }

here=$(cd "$(dirname "$0")/.." && pwd)
product=${FIRN_E2E_PRODUCT:-cayo-ab}
work=${FIRN_E2E_DIR:-$(mktemp -d /var/tmp/firn-e2e-ab.XXXXXX)}
timeout=${FIRN_E2E_TIMEOUT:-600}
# Default under /var: on a snosi A/B host root (and /root) is read-only
# erofs, so $HOME/.cache is unwritable under sudo.
cache=${FIRN_E2E_CACHE:-/var/tmp/firn-e2e-cache}
hostname=frn-ab-e2e
sshport=2223
base_url=https://cloud.debian.org/images/cloud/trixie/latest
base_img=debian-13-genericcloud-amd64.qcow2

ovmf_code=""
for c in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd /usr/share/edk2/x64/OVMF_CODE.4m.fd; do
  [[ -f $c ]] && ovmf_code=$c && break
done
ovmf_vars=${ovmf_code/CODE/VARS}
[[ -n $ovmf_code && -f $ovmf_vars ]] || { echo "e2e: OVMF firmware not found" >&2; exit 1; }

qemu_pid=""
cleanup() { [[ -n $qemu_pid ]] && kill "$qemu_pid" 2>/dev/null || true; }
trap cleanup EXIT

mkdir -p "$cache" "$work"

echo "e2e: providing a static firn for the guest"
if command -v go >/dev/null 2>&1; then
  (cd "$here" && CGO_ENABLED=0 go build -o "$work/firn" ./cmd/firn)
elif [[ -x $here/firn ]]; then
  # Root often lacks the user's go toolchain (linuxbrew); a prebuilt
  # ./firn from `just build` works. The guest runs it, not the host.
  cp "$here/firn" "$work/firn"
else
  echo "e2e: go not on PATH and no prebuilt ./firn — run 'just build' first" >&2
  exit 1
fi

if [[ ! -f $cache/$base_img ]]; then
  echo "e2e: fetching Debian cloud image (once)"
  curl -fSL --retry 3 -o "$cache/$base_img.tmp" "$base_url/$base_img"
  mv "$cache/$base_img.tmp" "$cache/$base_img"
fi

echo "e2e: preparing installer-env overlay + blank target disk"
qemu-img create -f qcow2 -F qcow2 -b "$cache/$base_img" "$work/installer.qcow2" 20G >/dev/null
truncate -s 30G "$work/target.raw"
ssh-keygen -t ed25519 -N "" -f "$work/id_e2e" -C firn-e2e >/dev/null
cp /usr/lib/systemd/import-pubring.gpg "$work/pubring.gpg" 2>/dev/null \
  || cp /usr/lib/snosi/os-update-pubring.gpg "$work/pubring.gpg"

# The recipe firn runs INSIDE the guest (target is the guest's /dev/vdb;
# the seeded root key file is copied to /root/id_e2e.pub in the guest).
cat >"$work/recipe.toml" <<EOF
version = 1

[image]
family = "ab"
product = "$product"

[target]
disk = "/dev/vdb"

[security]
encryption = "none"

[system]
hostname = "$hostname"
timezone = "America/Chicago"
locale = "en_US.UTF-8"
keyboard = "us"
root_ssh_authorized_key_file = "/root/id_e2e.pub"

[system.user]
name = "e2e"
password_hash = "\$6\$firn.e2e\$XjSAJP9d3TXbJ4wIcZarBOUpAo6yLh4uYUniEcpKPGqAe7EfWbrKZOfjfHiZ0KOhSjrqAGdRhrGxU0aTsTfW/1"
groups = ["sudo"]
ssh_authorized_key_file = "/root/id_e2e.pub"
EOF

# cloud-init NoCloud seed: just inject our key so the HOST can drive the
# install over SSH (far more debuggable than fire-and-forget runcmd).
cat >"$work/meta-data" <<EOF
instance-id: firn-ab-e2e
local-hostname: firn-installer
EOF
cat >"$work/user-data" <<EOF
#cloud-config
ssh_authorized_keys:
  - $(cat "$work/id_e2e.pub")
EOF
genisoimage -quiet -output "$work/seed.iso" -volid cidata -joliet -rock \
  "$work/user-data" "$work/meta-data"

# --- Install phase: drive firn over SSH inside a throwaway VM. ---
inst_port=2224
sshopts=(-i "$work/id_e2e" -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o BatchMode=yes)
gssh() { ssh "${sshopts[@]}" -p "$inst_port" debian@127.0.0.1 "$@"; }
gscp() { scp "${sshopts[@]}" -P "$inst_port" "$@"; }

cp "$ovmf_vars" "$work/vars.fd"
echo "e2e: booting installer VM (firn installs $product to the guest's /dev/vdb)"
qemu-system-x86_64 \
  -m 4096 -smp 2 -enable-kvm -cpu host \
  -drive if=pflash,format=raw,readonly=on,file="$ovmf_code" \
  -drive if=pflash,format=raw,file="$work/vars.fd" \
  -drive file="$work/installer.qcow2",format=qcow2,if=virtio \
  -drive file="$work/target.raw",format=raw,if=virtio \
  -drive file="$work/seed.iso",format=raw,if=virtio,readonly=on \
  -nic "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:$inst_port-:22" \
  -display none -serial file:"$work/installer-console.log" &
qemu_pid=$!

echo "e2e: waiting for installer VM SSH"
deadline=$((SECONDS + timeout)); up=0
while ((SECONDS < deadline)); do
  gssh true 2>/dev/null && { up=1; break; }
  kill -0 "$qemu_pid" 2>/dev/null || { echo "e2e: installer VM exited early" >&2; break; }
  sleep 5
done
((up)) || { echo "e2e: FAIL — installer VM never reachable over SSH (console: $work/installer-console.log)" >&2; exit 1; }

echo "e2e: staging firn + recipe into the installer VM"
gscp "$work/firn" "$work/recipe.toml" "$work/pubring.gpg" "$work/id_e2e.pub" debian@127.0.0.1:/tmp/ >/dev/null

echo "e2e: installing $product inside the VM (host kernel never scans the target)"
gssh 'sudo cp /tmp/id_e2e.pub /root/id_e2e.pub && sudo DEBIAN_FRONTEND=noninteractive sh -c "apt-get update -q && apt-get install -y -q xz-utils gpgv"' >/dev/null 2>&1 || {
  echo "e2e: FAIL — could not install tool deps in the guest" >&2; exit 1; }
set +e
gssh "sudo /tmp/firn install --uefi on --secure-boot off --tpm off \
  --pubring /tmp/pubring.gpg --confirm /dev/vdb --json-progress /tmp/recipe.toml" \
  >"$work/progress.ndjson" 2>"$work/firn.err"
inst_rc=$?
set -e

if ((inst_rc != 0)); then
  echo "e2e: FAIL — firn install exited $inst_rc inside the guest" >&2
  echo "---- firn.err ----"; tail -20 "$work/firn.err" >&2
  exit 1
fi
grep -q '"event":"done","ok":true' "$work/progress.ndjson" \
  || { echo "e2e: FAIL — no done event in progress stream" >&2; exit 1; }
echo "e2e: install OK — $product written to target.raw entirely within the guest"

gssh sudo poweroff 2>/dev/null || true
wait "$qemu_pid" 2>/dev/null || true; qemu_pid=""

# --- Verify phase: boot the installed disk, check over SSH. ---
echo "e2e: booting the INSTALLED disk (SSH probe on $sshport, up to ${timeout}s)"
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
  kill -0 "$qemu_pid" 2>/dev/null || { echo "e2e: installed VM exited early" >&2; break; }
  sleep 5
done
if ((!up)); then
  echo "e2e: FAIL — installed disk never reachable over SSH (console: $work/installed-console.log)" >&2
  echo "     (image may not ship sshd enabled; see roadmap note)" >&2
  exit 1
fi

fail=0
check() { if [[ $3 == *"$2"* ]]; then echo "e2e: ok   $1 = $3"; else echo "e2e: FAIL $1 = $3 (want $2)" >&2; fail=1; fi; }
check hostname "$hostname" "$(ssh "${sshopts[@]}" root@127.0.0.1 hostname)"
check user "uid=1000(e2e)" "$(ssh "${sshopts[@]}" root@127.0.0.1 id e2e)"
check groups sudo "$(ssh "${sshopts[@]}" root@127.0.0.1 id -Gn e2e)"
check timezone America/Chicago "$(ssh "${sshopts[@]}" root@127.0.0.1 readlink /etc/localtime)"
check locale "LANG=en_US.UTF-8" "$(ssh "${sshopts[@]}" root@127.0.0.1 cat /etc/locale.conf)"
check keyboard 'XKBLAYOUT="us"' "$(ssh "${sshopts[@]}" root@127.0.0.1 cat /etc/default/keyboard)"
check user-ssh-key firn-e2e "$(ssh "${sshopts[@]}" root@127.0.0.1 cat /var/home/e2e/.ssh/authorized_keys)"
check var-mount /var "$(ssh "${sshopts[@]}" root@127.0.0.1 findmnt -n -o TARGET /var)"
check install-info "$product" "$(ssh "${sshopts[@]}" root@127.0.0.1 cat /var/lib/snosi/install-info.json)"

ssh "${sshopts[@]}" root@127.0.0.1 poweroff 2>/dev/null || true
wait "$qemu_pid" 2>/dev/null || true; qemu_pid=""

((fail == 0)) || { echo "e2e: FAIL (details above; $work)" >&2; exit 1; }
echo "e2e: PASS — $product installed inside a VM and booted, verified over SSH ($work)"
