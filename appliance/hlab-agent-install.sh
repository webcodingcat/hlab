#!/bin/sh
# Copies the hlab-agent binary, its secret and its transit address from the appliance's cidata
# ISO (volume label "cidata"); hlabd writes a new ISO at every cloud start.
# The .deb files on the ISO are installed only while their tool is missing:
# xfsprogs and its libraries while mkfs.xfs is missing, HAProxy and its
# libraries while haproxy is missing, and on the storage VM btrfs-progs,
# rsync, borgbackup and their libraries while btrfs is missing, Samba while
# smbd is missing, Apache with mpm-itk while apache2 is missing and vsftpd
# while vsftpd is missing. A failure there does not keep the agent down.
set -eu

dev=$(blkid -L cidata)
mnt=$(mktemp -d)
trap 'umount "$mnt" 2>/dev/null || true; rmdir "$mnt"' EXIT

mount -o ro "$dev" "$mnt"
install -m 0755 "$mnt/hlab-agent" /usr/local/sbin/hlab-agent
install -d -m 0700 /etc/hlab-agent
install -m 0600 "$mnt/secret" /etc/hlab-agent/secret
install -m 0644 "$mnt/transit" /etc/hlab-agent/transit
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

# Only the appliance's ISO carries xfsprogs and haproxy; the storage VM's
# shares some of their libraries.
if [ -e "$mnt/xfsprogs.deb" ] && ! command -v mkfs.xfs >/dev/null 2>&1; then
	install_debs xfsprogs libicu76 libinih1
fi

if [ -e "$mnt/haproxy.deb" ] && ! command -v haproxy >/dev/null 2>&1; then
	install_debs haproxy liblua5.4-0 libopentracing-c-wrapper0t64 libopentracing1 lsb-base
	# The agent runs its own HAProxy instances; Debian's service stays off.
	systemctl disable --now haproxy >/dev/null 2>&1 || true
fi

# Only the storage VM's ISO carries btrfs-progs.
if [ -e "$mnt/btrfs-progs.deb" ] && ! command -v btrfs >/dev/null 2>&1; then
	install_debs btrfs-progs liblzo2-2 libreiserfscore0t64 rsync borgbackup python3-msgpack python3-packaging
	# The agent runs rsync through rrsync over SSH; the rsync daemon stays off.
	systemctl disable --now rsync.service >/dev/null 2>&1 || true
fi

# install_daemon_debs installs packages whose postinst would start a daemon
# with Debian's configuration: policy-rc.d keeps every service down during
# dpkg; the storage agent configures and starts them.
install_daemon_debs() {
	printf '#!/bin/sh\nexit 101\n' >/usr/sbin/policy-rc.d
	chmod 0755 /usr/sbin/policy-rc.d
	install_debs "$@"
	rm -f /usr/sbin/policy-rc.d
}

# Only the storage VM's ISO carries Samba, Apache and vsftpd. Their services
# stay off until the storage agent has configured them.
if [ -e "$mnt/samba.deb" ] && ! command -v smbd >/dev/null 2>&1; then
	install_daemon_debs samba samba-common samba-common-bin samba-libs libldb2 libtalloc2 libtdb1 \
		libtevent0t64 libwbclient0 libcups2t64 libavahi-client3 libavahi-common3 \
		libavahi-common-data libicu76 libncurses6 libjansson4
	systemctl disable --now nmbd smbd >/dev/null 2>&1 || true
fi

if [ -e "$mnt/apache2.deb" ] && ! command -v apache2 >/dev/null 2>&1; then
	install_daemon_debs apache2 apache2-bin apache2-data apache2-utils libapache2-mpm-itk libapr1t64 \
		libaprutil1t64 libaprutil1-ldap libaprutil1-dbd-sqlite3 liblua5.4-0 perl libperl5.40 \
		perl-modules-5.40 libgdbm-compat4t64
	systemctl disable --now apache2 >/dev/null 2>&1 || true
	if command -v apache2 >/dev/null 2>&1; then
		# The agent writes one site with a Listen per box address; Debian's
		# wildcard Listen lines and default site would take the ports.
		sed -i '/^[[:space:]]*Listen[[:space:]]/d' /etc/apache2/ports.conf || true
		rm -f /etc/apache2/sites-enabled/000-default.conf
		a2dismod -f mpm_event >/dev/null || true
		a2enmod mpm_prefork mpm_itk ssl dav dav_fs auth_basic authn_file authz_user authz_host >/dev/null ||
			echo "hlab-agent-install: a2enmod failed" >&2
	fi
fi

if [ -e "$mnt/vsftpd.deb" ] && ! command -v vsftpd >/dev/null 2>&1; then
	install_daemon_debs vsftpd lsb-base
	systemctl disable --now vsftpd >/dev/null 2>&1 || true
fi
