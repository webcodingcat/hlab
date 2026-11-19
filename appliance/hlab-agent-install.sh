#!/bin/sh
# Copies the hlab-agent binary and its secret from the appliance's cidata
# ISO (volume label "cidata"); hlabd writes a new ISO at every cloud start.
# The .deb files on the ISO are installed only while their tool is missing:
# xfsprogs and its libraries while mkfs.xfs is missing, HAProxy and its
# libraries while haproxy is missing. A failure there does not keep the
# agent down.
set -eu

dev=$(blkid -L cidata)
mnt=$(mktemp -d)
trap 'umount "$mnt" 2>/dev/null || true; rmdir "$mnt"' EXIT

mount -o ro "$dev" "$mnt"
install -m 0755 "$mnt/hlab-agent" /usr/local/sbin/hlab-agent
install -d -m 0700 /etc/hlab-agent
install -m 0600 "$mnt/secret" /etc/hlab-agent/secret
install -d -m 0755 /run/hlab

# install_debs installs the named packages from the ISO in one dpkg call,
# skipping files hlabd could not download.
install_debs() {
	files=""
	for name in "$@"; do
		if [ -e "$mnt/$name.deb" ]; then
			files="$files $mnt/$name.deb"
		fi
	done
	if [ -n "$files" ]; then
		# shellcheck disable=SC2086 # the ISO paths contain no spaces
		dpkg -i $files || echo "hlab-agent-install: dpkg -i of $* failed" >&2
	fi
}

if ! command -v mkfs.xfs >/dev/null 2>&1; then
	install_debs xfsprogs libicu76 libinih1
fi

if ! command -v haproxy >/dev/null 2>&1; then
	install_debs haproxy liblua5.4-0 libopentracing-c-wrapper0t64 libopentracing1 lsb-base
	# The agent runs its own HAProxy instances; Debian's service stays off.
	systemctl disable --now haproxy >/dev/null 2>&1 || true
fi
