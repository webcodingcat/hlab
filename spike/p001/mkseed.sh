#!/bin/bash
# Build a NoCloud seed (cidata) as ISO and as VFAT VHDX, Hetzner-style network.
# Usage: mkseed.sh <hostname> <ipv4> <ipv6-addr> <mac aa:bb:..> <outdir>
set -euo pipefail
name=$1 ipv4=$2 ipv6=$3 mac=$4 out=$5
key=$(cat ~/hlab-spike-data/id_ed25519.pub)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cat > "$work/meta-data" <<META
instance-id: $name-$(date +%s)
local-hostname: $name
META
cat > "$work/user-data" <<USER
#cloud-config
ssh_authorized_keys:
  - $key
write_files:
  - path: /root/seed-marker
    content: "user-data from $name\n"
USER
cat > "$work/network-config" <<NET
version: 2
ethernets:
  eth0:
    match:
      macaddress: "$mac"
    set-name: eth0
    addresses: [$ipv4/32, $ipv6/64]
    nameservers:
      addresses: [198.18.0.2]
    routes:
      - to: 0.0.0.0/0
        via: 172.31.1.1
        on-link: true
      - to: "::/0"
        via: "fe80::1"
NET
mkdir -p "$out"
xorriso -as mkisofs -quiet -o "$out/cidata.iso" -V cidata -J -R \
  "$work/meta-data" "$work/user-data" "$work/network-config"
truncate -s 8M "$work/cidata.img"
mformat -i "$work/cidata.img" -v CIDATA -F ::
mcopy -i "$work/cidata.img" "$work/meta-data" "$work/user-data" "$work/network-config" ::
qemu-img convert -O vhdx -o subformat=dynamic "$work/cidata.img" "$out/cidata.vhdx"
