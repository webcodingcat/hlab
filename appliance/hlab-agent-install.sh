#!/bin/sh
# Copies the hlab-agent binary and its secret from the appliance's cidata
# ISO (volume label "cidata"); hlabd writes a new ISO at every cloud start.
set -eu

dev=$(blkid -L cidata)
mnt=$(mktemp -d)
trap 'umount "$mnt" 2>/dev/null || true; rmdir "$mnt"' EXIT

mount -o ro "$dev" "$mnt"
install -m 0755 "$mnt/hlab-agent" /usr/local/sbin/hlab-agent
install -d -m 0700 /etc/hlab-agent
install -m 0600 "$mnt/secret" /etc/hlab-agent/secret
