#!/usr/bin/env bash
# E2E: recipe-driven bootc install under UEFI Secure Boot (ADR-0014).
#
# Proves firn's secure-install schema-1: firn stages the ESP chain (shim ->
# MOK-signed systemd-boot -> MokManager) and enrolls the MOK via mokutil, and
# the result boots under ENFORCED Secure Boot.
#
# WHY NESTED: firn's mok-stage runs `mokutil --import`, which writes the MOK
# request into the RUNNING machine's firmware NVRAM. Run on the host that would
# be the snow-ab dev box's firmware — a stray MokManager prompt on its next
# boot. Inside a throwaway VM the mokutil write lands in the guest's OVMF
# varstore, which is discarded. (bootc disks may be loop-mounted on the host
# for the ESP assertion — ADR-0009 exempts them.)
#
# THE MOK DANCE: on a real machine shim launches MokManager on first boot to
# enroll the pending request firn staged; a human types the one-time password.
# That is not automatable unattended, so — exactly as dakota's secure harness
# does — we prove the boot host-side: boot the installed disk with a FRESH
# MS-keys varstore into which virt-fw-vars has enrolled the snosi MOK (MokList),
# with no pending MokNew, so shim trusts the MOK-signed second stage and boots
# with no prompt. The install proves firn staged the chain + ran mokutil; the
# secure boot proves that chain is what firmware accepts.
#
# Requirements: root; qemu-system-x86_64 (KVM); OVMF (plain + secboot code and
# an MS-keys varstore); virt-fw-vars; genisoimage; curl; ssh; the snosi MOK
# certificate. Network. ~30 GiB scratch.
#
# Usage: sudo test/e2e-bootc-secure.sh
#   FIRN_E2E_IMAGE    secureboot-capable bootc image (default ghcr.io/frostyard/cayo:latest)
#   FIRN_E2E_MOK_CERT snosi MOK cert (default: snosi checkout shared/native-ab/keys/mok-2026.crt)
#   FIRN_E2E_DIR      scratch dir     FIRN_E2E_TIMEOUT seconds (default 900)
set -euo pipefail

[[ $EUID -eq 0 ]] || { echo "e2e: must run as root (qemu KVM + disk images)" >&2; exit 1; }

here=$(cd "$(dirname "$0")/.." && pwd)
image=${FIRN_E2E_IMAGE:-ghcr.io/frostyard/cayo:latest}
work=${FIRN_E2E_DIR:-$(mktemp -d /var/tmp/firn-e2e-bootc-secure.XXXXXX)}
timeout=${FIRN_E2E_TIMEOUT:-900}
cache=${FIRN_E2E_CACHE:-/var/tmp/firn-e2e-cache}
hostname=frn-sb-e2e
base_url=https://cloud.debian.org/images/cloud/trixie/latest
base_img=debian-13-genericcloud-amd64.qcow2

# The snosi MOK: the cert firn's mok-stage enrolls and that signs the second
# stage. Same key the native-ab path uses; the image ships it at
# /usr/lib/snosi/mok.crt. We enroll it host-side into the boot varstore.
mok_cert=${FIRN_E2E_MOK_CERT:-}
if [[ -z $mok_cert ]]; then
  for c in "$here/../snosi/shared/native-ab/keys/mok-2026.crt" \
           /home/bjk/projects/frostyard/snosi/shared/native-ab/keys/mok-2026.crt; do
    [[ -f $c ]] && mok_cert=$c && break
  done
fi
[[ -n $mok_cert && -f $mok_cert ]] || { echo "e2e: snosi MOK cert not found; set FIRN_E2E_MOK_CERT" >&2; exit 1; }

# Plain OVMF for the install guest (a Debian cloud image boots fine, and the
# install only writes to the target); secboot OVMF + MS-keys varstore for the
# verify boot, where Secure Boot is genuinely enforced.
plain_code=""
for c in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd; do
  [[ -f $c ]] && plain_code=$c && break
done
plain_vars=${plain_code/CODE/VARS}
sb_code=/usr/share/OVMF/OVMF_CODE_4M.secboot.fd
sb_vars=/usr/share/OVMF/OVMF_VARS_4M.ms.fd
for f in "$plain_code" "$plain_vars" "$sb_code" "$sb_vars"; do
  [[ -f $f ]] || { echo "e2e: firmware missing: $f" >&2; exit 1; }
done
command -v virt-fw-vars >/dev/null || { echo "e2e: virt-fw-vars required" >&2; exit 1; }

qemu_pid=""
cleanup() { [[ -n $qemu_pid ]] && kill "$qemu_pid" 2>/dev/null || true; }
trap cleanup EXIT
mkdir -p "$cache" "$work"

echo "e2e: providing a static firn for the guest"
if command -v go >/dev/null 2>&1; then
  (cd "$here" && CGO_ENABLED=0 go build -o "$work/firn" ./cmd/firn-cli)
elif [[ -x $here/build/firn || -x $here/firn ]]; then
  cp "$(ls "$here/build/firn" "$here/firn" 2>/dev/null | head -1)" "$work/firn"
else
  echo "e2e: go not on PATH and no prebuilt ./firn — run 'make build' first" >&2; exit 1
fi

if [[ ! -f $cache/$base_img ]]; then
  echo "e2e: fetching Debian cloud image (once)"
  curl -fSL --retry 3 -o "$cache/$base_img.tmp" "$base_url/$base_img"
  mv "$cache/$base_img.tmp" "$cache/$base_img"
fi

echo "e2e: preparing installer-env overlay + blank target disk"
qemu-img create -f qcow2 -F qcow2 -b "$cache/$base_img" "$work/installer.qcow2" 30G >/dev/null
truncate -s 30G "$work/target.raw"
ssh-keygen -t ed25519 -N "" -f "$work/id_e2e" -C firn-e2e >/dev/null

cat >"$work/recipe.toml" <<EOF
version = 1

[image]
family = "bootc"
ref = "$image"

[target]
disk = "/dev/vdb"
filesystem = "btrfs"
btrfs_subvolumes = true

[security]
encryption = "none"
mok = "enroll"
mok_password_file = "/run/firn-mok.pass"

[system]
hostname = "$hostname"
root_ssh_authorized_key_file = "/root/id_e2e.pub"

[system.user]
name = "e2e"
password_hash = "\$6\$firn.e2e\$XjSAJP9d3TXbJ4wIcZarBOUpAo6yLh4uYUniEcpKPGqAe7EfWbrKZOfjfHiZ0KOhSjrqAGdRhrGxU0aTsTfW/1"
groups = ["wheel"]
ssh_authorized_key_file = "/root/id_e2e.pub"
EOF

cat >"$work/meta-data" <<EOF
instance-id: firn-sb-e2e
local-hostname: firn-installer
EOF
cat >"$work/user-data" <<EOF
#cloud-config
ssh_authorized_keys:
  - $(cat "$work/id_e2e.pub")
EOF
genisoimage -quiet -output "$work/seed.iso" -volid cidata -joliet -rock \
  "$work/user-data" "$work/meta-data"

# --- Install phase: drive firn over SSH inside a throwaway VM (plain OVMF). ---
inst_port=2224
sshopts=(-i "$work/id_e2e" -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o BatchMode=yes)
gssh() { ssh "${sshopts[@]}" -p "$inst_port" debian@127.0.0.1 "$@"; }
gscp() { scp "${sshopts[@]}" -P "$inst_port" "$@"; }

cp "$plain_vars" "$work/vars.fd"
echo "e2e: booting installer VM (firn installs $image to the guest's /dev/vdb)"
qemu-system-x86_64 \
  -m 6144 -smp 4 -enable-kvm -cpu host \
  -drive if=pflash,format=raw,readonly=on,file="$plain_code" \
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
((up)) || { echo "e2e: FAIL — installer VM never reachable (console: $work/installer-console.log)" >&2; exit 1; }

echo "e2e: staging firn + recipe into the installer VM"
gscp "$work/firn" "$work/recipe.toml" "$work/id_e2e.pub" debian@127.0.0.1:/tmp/ >/dev/null

# Tools firn's bootc + secure-boot preflight demands. The one-time MokManager
# password is created in the guest (0600) so it never crosses scp.
echo "e2e: installing tool deps in the guest"
gssh "sudo DEBIAN_FRONTEND=noninteractive sh -c 'apt-get update -q && apt-get install -y -q \
  podman skopeo sbsigntool mokutil openssl btrfs-progs dosfstools e2fsprogs cryptsetup-bin'" >/dev/null 2>&1 \
  || { echo "e2e: FAIL — could not install guest tool deps" >&2; exit 1; }
gssh "sudo cp /tmp/id_e2e.pub /root/id_e2e.pub && \
  sudo sh -c 'LC_ALL=C tr -dc A-Za-z0-9 </dev/urandom | head -c 16 > /run/firn-mok.pass; chmod 600 /run/firn-mok.pass'" \
  || { echo "e2e: FAIL — could not seed MOK password in the guest" >&2; exit 1; }

echo "e2e: installing $image under Secure Boot inside the VM"
set +e
gssh "sudo /tmp/firn install --uefi on --secure-boot on --tpm off \
  --confirm /dev/vdb --json-progress /tmp/recipe.toml" \
  >"$work/progress.ndjson" 2>"$work/firn.err"
inst_rc=$?
set -e
if ((inst_rc != 0)); then
  echo "e2e: FAIL — firn install exited $inst_rc inside the guest" >&2
  echo "---- firn.err ----"; tail -25 "$work/firn.err" >&2
  exit 1
fi
grep -q '"event":"done","ok":true' "$work/progress.ndjson" \
  || { echo "e2e: FAIL — no done event in progress stream" >&2; exit 1; }
# Prove firn actually ran the secure-boot steps, not just a plain install
# (step_start events carry the step name; done:ok above means none errored).
grep -q '"event":"step_start"[^}]*"name":"esp-stage"' "$work/progress.ndjson" \
  || { echo "e2e: FAIL — esp-stage step never ran (secure boot not staged)" >&2; exit 1; }
grep -q '"event":"step_start"[^}]*"name":"mok-stage"' "$work/progress.ndjson" \
  || { echo "e2e: FAIL — mok-stage step never ran (MOK not enrolled)" >&2; exit 1; }
echo "e2e: install OK — esp-stage + mok-stage ran; $image on target.raw"

gssh sudo poweroff 2>/dev/null || true
wait "$qemu_pid" 2>/dev/null || true; qemu_pid=""

# --- ESP assertion: loop-mount the target ESP (bootc, host-safe) and confirm
#     firn staged the three-component chain. ---
echo "e2e: asserting the Secure Boot ESP chain on target.raw"
loop=$(losetup --show -Pf "$work/target.raw")
espmnt="$work/espmnt"; mkdir -p "$espmnt"
esp_ok=1
if mount "${loop}p1" "$espmnt" 2>/dev/null; then
  for c in BOOTX64.EFI grubx64.efi mmx64.efi; do
    if [[ -s $espmnt/EFI/BOOT/$c ]]; then echo "e2e: ok   ESP $c staged"; else echo "e2e: FAIL ESP $c missing" >&2; esp_ok=0; fi
  done
  umount "$espmnt"
else
  echo "e2e: FAIL — could not mount target ESP (${loop}p1)" >&2; esp_ok=0
fi
losetup -d "$loop" || true
((esp_ok)) || { echo "e2e: FAIL — ESP chain incomplete ($work)" >&2; exit 1; }

# --- Verify boot: enforced Secure Boot, snosi MOK enrolled host-side. ---
echo "e2e: enrolling snosi MOK into a fresh MS-keys varstore"
cp "$sb_vars" "$work/vars-secure.fd"
guid=$(cat /proc/sys/kernel/random/uuid 2>/dev/null || echo 62688093-79f4-4f5c-8e2b-1a2b3c4d5e6f)
virt-fw-vars --inplace "$work/vars-secure.fd" --add-mok "$guid" "$mok_cert" \
  || { echo "e2e: FAIL — virt-fw-vars could not enroll the MOK" >&2; exit 1; }
virt-fw-vars -i "$work/vars-secure.fd" -p 2>&1 | grep -q MokList \
  || { echo "e2e: FAIL — varstore has no MokList after enrollment" >&2; exit 1; }

echo "e2e: booting the INSTALLED disk under ENFORCED Secure Boot (SSH on 2226, up to ${timeout}s)"
sbport=2226
qemu-system-x86_64 \
  -m 4096 -smp 2 -enable-kvm -cpu host \
  -global driver=cfi.pflash01,property=secure,value=on \
  -drive if=pflash,format=raw,readonly=on,file="$sb_code" \
  -drive if=pflash,format=raw,file="$work/vars-secure.fd" \
  -drive file="$work/target.raw",format=raw,if=virtio \
  -nic "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:$sbport-:22" \
  -display none -serial file:"$work/installed-console.log" &
qemu_pid=$!

sshopts=(-i "$work/id_e2e" -p "$sbport" -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o BatchMode=yes)
deadline=$((SECONDS + timeout)); up=0
while ((SECONDS < deadline)); do
  ssh "${sshopts[@]}" root@127.0.0.1 true 2>/dev/null && { up=1; break; }
  kill -0 "$qemu_pid" 2>/dev/null || { echo "e2e: installed VM exited early" >&2; break; }
  sleep 5
done
if ((!up)); then
  echo "e2e: FAIL — installed disk never booted under Secure Boot (console: $work/installed-console.log)" >&2
  echo "     A Secure Boot rejection shows as 'Access Denied' in the console log." >&2
  exit 1
fi

fail=0
check() { if [[ $3 == *"$2"* ]]; then echo "e2e: ok   $1 = $3"; else echo "e2e: FAIL $1 = $3 (want $2)" >&2; fail=1; fi; }
check hostname "$hostname" "$(ssh "${sshopts[@]}" root@127.0.0.1 hostname)"
check secureboot enabled "$(ssh "${sshopts[@]}" root@127.0.0.1 'mokutil --sb-state 2>/dev/null || bootctl status 2>/dev/null | grep -i secure')"
check user "uid=1000(e2e)" "$(ssh "${sshopts[@]}" root@127.0.0.1 id e2e)"

ssh "${sshopts[@]}" root@127.0.0.1 poweroff 2>/dev/null || true
wait "$qemu_pid" 2>/dev/null || true; qemu_pid=""

((fail == 0)) || { echo "e2e: FAIL (details above; $work)" >&2; exit 1; }
echo "e2e: PASS — $image installed + booted under ENFORCED Secure Boot, verified over SSH ($work)"
