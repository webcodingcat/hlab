#!/bin/bash
# Start a test VM and measure until SSH answers from WSL and cloud-init is done.
# Usage: boot.sh <name> <base> <mac> <ipv4> <user> <iso|vhdx> <on|off>
set -euo pipefail
name=$1 base=$2 mac=$3 ip=$4 user=$5 seed=$6 sb=$7
mkdir -p /mnt/c/HLabSpike/scripts
cp "$(dirname "$0")/../windows/"*.ps1 /mnt/c/HLabSpike/scripts/
ssh-keygen -R "$ip" >/dev/null 2>&1 || true
start=$(date +%s.%N)
/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe -NoProfile -ExecutionPolicy Bypass \
  -File 'C:\HLabSpike\scripts\p001-vm.ps1' -Name "$name" -Base "$base" -Mac "$mac" -Seed "$seed" -SecureBoot "$sb" | tr -d '\r' >/dev/null
started=$(date +%s.%N)
ssh_opts=(-i ~/hlab-spike-data/id_ed25519 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=1 -o BatchMode=yes -o LogLevel=ERROR)
ready=""
for _ in $(seq 1 180); do
  if ssh "${ssh_opts[@]}" "$user@$ip" true 2>/dev/null; then ready=$(date +%s.%N); break; fi
  sleep 0.5
done
if [[ -z "$ready" ]]; then echo "$name: no SSH after 90+ s"; exit 1; fi
ssh "${ssh_opts[@]}" "$user@$ip" 'sudo cloud-init status --wait >/dev/null; true'
done_at=$(date +%s.%N)
# Kernel start as wall time (guest clock follows the host via hv_utils), relative to Start-VM.
kernel_at=$(ssh "${ssh_opts[@]}" "$user@$ip" 'awk -v now=$(date +%s.%N) "{printf \"%.3f\", now - \$1}" /proc/uptime')
printf 'kernel_start_after_start=%.1fs\n' "$(echo "$kernel_at-$started" | bc)"
printf '%s base=%s seed=%s secureboot=%s create=%.1fs ssh_after_start=%.1fs cloudinit_done=%.1fs\n' \
  "$name" "$base" "$seed" "$sb" "$(echo "$started-$start" | bc)" "$(echo "$ready-$started" | bc)" "$(echo "$done_at-$started" | bc)"
ssh "${ssh_opts[@]}" "$user@$ip" 'hostname; cat /root/seed-marker 2>/dev/null || sudo cat /root/seed-marker; uname -r; lsmod | grep -E "^hv_(netvsc|storvsc|vmbus)|hv_vmbus" | awk "{print \$1}" | tr "\n" " "; echo; ip -br a show eth0; ip r; ip -6 r | grep default; mokutil --sb-state 2>/dev/null || echo "mokutil n/a"; sudo cloud-init status --long | grep -E "status|detail|datasource"; systemd-analyze | head -1'
