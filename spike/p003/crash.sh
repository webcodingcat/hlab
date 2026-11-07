#!/bin/bash
# P0-03 crash: turn the appliance off hard, start it again, push the state over hv_sock,
# and measure the data plane outage seen from WSL (new flow every ~100 ms).
set -uo pipefail
here=$(dirname "$0")
PS=/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe
HC=/mnt/c/HLabSpike/hostctl.exe
vm=$(cd /mnt/c && $PS -NoProfile -Command '(Get-VM hlab-appliance).Id.Guid' | tr -d '\r')
log=$(mktemp)
ssh_opts=(-i ~/hlab-spike-data/id_ed25519 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes -o ServerAliveInterval=2 -o ServerAliveCountMax=30)
"$here/probe.sh" 198.18.16.10 "$log" &
prober=$!
# A TCP session through the router (no NAT): does it survive the restart?
ssh "${ssh_opts[@]}" debian@198.18.16.10 'for i in $(seq 1 150); do echo $i; sleep 0.2; done' > "$log.ssh" 2>&1 &
sleep 2
t_off=$(date +%s.%N)
(cd /mnt/c && $PS -NoProfile -Command 'Stop-VM hlab-appliance -TurnOff -Force; Start-VM hlab-appliance')
t_started=$(date +%s.%N)
(cd /mnt/c/HLabSpike && $HC "$vm" wait 60 | tr -d '\r')
t_agent=$(date +%s.%N)
(cd /mnt/c/HLabSpike && for f in state.ip:/ip uplink.nft:/nft ruleset-20-10.nft:/nft; do $HC "$vm" post "${f#*:}" "C:\\HLabSpike\\${f%%:*}" | tr -d '\r' | head -1; done)
t_pushed=$(date +%s.%N)
sleep 3
kill $prober
wait 2>/dev/null
printf 'turnoff+start returned %.2fs, agent ready %.2fs, state pushed %.2fs (after Stop-VM call)\n' \
  "$(echo "$t_started-$t_off" | bc)" "$(echo "$t_agent-$t_off" | bc)" "$(echo "$t_pushed-$t_off" | bc)"
awk '{ if (prev && $1-prev > gap) gap=$1-prev; prev=$1 } END { printf "data plane outage (longest gap between answers): %.2f s\n", gap }' "$log"
echo "ssh session through router: last line $(tail -1 "$log.ssh") (150 = survived)"
rm -f "$log" "$log.ssh"
