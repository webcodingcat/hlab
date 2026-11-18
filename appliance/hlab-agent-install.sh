#!/bin/sh
# Copies the hlab-agent binary and its secret from the appliance's cidata
# ISO (volume label "cidata"); hlabd writes a new ISO at every cloud start.
# The .deb files on the ISO (xfsprogs and its libraries) are installed only
# while mkfs.xfs is missing; a failure there does not keep the agent down.
set -eu

dev=$(blkid -L cidata)
mnt=$(mktemp -d)
trap 'umount "$mnt" 2>/dev/null || true; rmdir "$mnt"' EXIT

mount -o ro "$dev" "$mnt"
install -m 0755 "$mnt/hlab-agent" /usr/local/sbin/hlab-agent
install -d -m 0700 /etc/hlab-agent
install -m 0600 "$mnt/secret" /etc/hlab-agent/secret

if ! command -v mkfs.xfs >/dev/null 2>&1; then
	set -- "$mnt"/*.deb
	if [ -e "$1" ]; then
		dpkg -i "$@" || echo "hlab-agent-install: dpkg -i of the appliance packages failed" >&2
	fi
fi
