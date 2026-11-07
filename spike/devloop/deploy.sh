#!/bin/bash
# P0-13: the planned "bin/dev deploy" loop, timed step by step.
# Build in WSL2 -> ask the running elevated daemon to exit -> copy -> start it again
# through the scheduled task (no UAC) -> wait until it reports the new version.
set -euo pipefail
cd "$(dirname "$0")"
version=${1:-$(date +%H%M%S%N)}
url=http://198.18.0.1:8090
t0=$(date +%s.%N)
GOOS=windows go build -ldflags "-X main.version=$version" -o /tmp/hlabd-dev.exe ./cmd/hlabd-dev
t_build=$(date +%s.%N)
token=$(cat /mnt/c/ProgramData/HLab/control.token)
curl -s -m 2 -X POST -H "Authorization: Bearer $token" "$url/_lab/shutdown" >/dev/null || true
for _ in $(seq 100); do curl -s -m 0.3 "$url/status" >/dev/null || break; sleep 0.1; done
t_stopped=$(date +%s.%N)
for _ in $(seq 50); do cp /tmp/hlabd-dev.exe /mnt/c/HLabDev/bin/hlabd-dev.exe 2>/dev/null && break; sleep 0.1; done
t_copied=$(date +%s.%N)
/mnt/c/Windows/System32/schtasks.exe /run /tn "HLab dev" >/dev/null
for _ in $(seq 300); do
  curl -s -m 0.5 "$url/status" | grep -q "version=$version " && break
  sleep 0.1
done
t_ready=$(date +%s.%N)
d() { echo "$2 - $1" | bc; }
printf 'build %.1fs, stop %.1fs, copy %.1fs, start until new version answers %.1fs, total %.1fs\n' \
  "$(d "$t0" "$t_build")" "$(d "$t_build" "$t_stopped")" "$(d "$t_stopped" "$t_copied")" \
  "$(d "$t_copied" "$t_ready")" "$(d "$t0" "$t_ready")"
