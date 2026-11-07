#!/bin/bash
# Probe a guest from WSL with a new ICMP flow every ~100 ms and log the time of each
# answer. A new flow each time, because Windows' NAT for WSL keeps a stale session
# for a long-running ping for 30-60 s after the appliance restarts.
# Usage: probe.sh <address> <logfile>   (runs until killed)
address=$1 log=$2
: > "$log"
while true; do
  if ping -c1 -W0.2 "$address" >/dev/null 2>&1; then
    date +%s.%N >> "$log"
  fi
  sleep 0.1
done
