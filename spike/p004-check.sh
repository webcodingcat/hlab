#!/bin/bash
# P0-04: does everything on the host still have Internet, and do guests?
S=(-i ~/hlab-spike-data/id_ed25519 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes -o ConnectTimeout=3)
web() { timeout 8 curl -s -o /dev/null -w '%{http_code}' https://deb.debian.org/debian/dists/trixie/Release; }
echo "  windows:   $(/mnt/c/Windows/System32/curl.exe -s -o NUL -w '%{http_code}' -m 8 https://deb.debian.org/debian/dists/trixie/Release)"
echo "  wsl2:      $(web)"
echo "  docker:    $(timeout 60 docker run --rm curlimages/curl:8.11.1 -s -o /dev/null -w '%{http_code}' -m 8 https://deb.debian.org/debian/dists/trixie/Release 2>&1 | tail -1)"
for n in 10 16 22; do
  echo "  guest .$n: $(ssh "${S[@]}" debian@198.18.16.$n 'curl -s -o /dev/null -w "%{http_code}" -m 8 https://deb.debian.org/debian/dists/trixie/Release' 2>&1)"
done
echo "  host -> guest .10: $(ping -c1 -W1 198.18.16.10 >/dev/null && echo ok || echo FAIL)"
