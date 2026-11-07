#!/bin/bash
# P0-03 update: appliance B (already running, no switch) gets the full state over
# hv_sock; then the switch ports move from A to B (same MACs) and A stops.
# Measures the data plane outage from WSL.
# Usage: update.sh <old-vm-name> <new-vm-name>
set -uo pipefail
here=$(dirname "$0")
old=$1 new=$2
PS=/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe
HC=/mnt/c/HLabSpike/hostctl.exe
new_id=$(cd /mnt/c && $PS -NoProfile -Command "(Get-VM $new).Id.Guid" | tr -d '\r')
(cd /mnt/c/HLabSpike && $HC "$new_id" wait 90 | tr -d '\r')
(cd /mnt/c/HLabSpike && for f in state.ip:/ip uplink.nft:/nft ruleset-20-10.nft:/nft; do $HC "$new_id" post "${f#*:}" "C:\\HLabSpike\\${f%%:*}" | tr -d '\r' | head -1; done)
log=$(mktemp)
ssh_opts=(-i ~/hlab-spike-data/id_ed25519 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes -o ServerAliveInterval=2 -o ServerAliveCountMax=30)
"$here/probe.sh" 198.18.16.10 "$log" &
prober=$!
ssh "${ssh_opts[@]}" debian@198.18.16.10 'for i in $(seq 1 100); do echo $i; sleep 0.2; done' > "$log.ssh" 2>&1 &
sleep 2
t_swap=$(date +%s.%N)
# One PowerShell call: the order is what the service would do.
# ORDER=connect-first connects B before A lets go (same MACs on the switch for a moment).
order=${ORDER:-disconnect-first}
(cd /mnt/c && $PS -NoProfile -Command "
  function Mark(\$what) { '{0} {1}' -f (Get-Date).ToUniversalTime().ToString('HH:mm:ss.fff'), \$what }
  \$switches = 'HLabLink', 'HLabPublic', 'Default Switch'
  \$oldNics = Get-VMNetworkAdapter -VMName $old | Sort-Object MacAddress
  \$newNics = Get-VMNetworkAdapter -VMName $new | Sort-Object MacAddress
  Mark 'start ($order)'
  if ('$order' -eq 'disconnect-first') { \$oldNics | Disconnect-VMNetworkAdapter; Mark 'old disconnected' }
  for (\$i = 0; \$i -lt 3; \$i++) { Connect-VMNetworkAdapter -VMNetworkAdapter \$newNics[\$i] -SwitchName \$switches[\$i] }
  Mark 'new connected'
  if ('$order' -ne 'disconnect-first') { \$oldNics | Disconnect-VMNetworkAdapter; Mark 'old disconnected' }
  if ('${STOP_OLD:-yes}' -eq 'yes') { Stop-VM $old -TurnOff -Force; Mark 'old stopped' }" | tr -d '\r')
t_done=$(date +%s.%N)
sleep 8
# The end of probing counts as an answer, so "never came back" shows as a long gap.
end=$(date +%s.%N)
kill $prober
wait 2>/dev/null
printf 'swap commands took %.2fs\n' "$(echo "$t_done-$t_swap" | bc)"
awk -v end="$end" '{ if (prev && $1-prev > gap) { gap=$1-prev; from=prev; to=$1 } prev=$1 }
  END { if (end-prev > gap) { gap=end-prev; from=prev; to=end }
        printf "data plane outage (longest gap between answers): %.2f s (%s -> %s UTC)\n", gap,
          strftime("%H:%M:%S", from, 1) sprintf(".%03d", (from%1)*1000), strftime("%H:%M:%S", to, 1) sprintf(".%03d", (to%1)*1000) }' "$log"
echo "ssh session through router: last line $(tail -1 "$log.ssh") (100 = survived)"
rm -f "$log" "$log.ssh"
