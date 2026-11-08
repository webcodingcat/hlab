#!/usr/bin/env bash
# Finish the appliance's root file system. appliance/build.sh has mmdebstrap
# run it inside the new root (chroot), after the packages, the files of
# appliance/files and the agent are in place.
#   customize.sh <UUID of the root file system>
set -euo pipefail

main() {
  local root_uuid="$1"
  chmod 0755 /usr/local/sbin/hlab-agent
  echo "hlab-appliance" >/etc/hostname
  echo "UUID=${root_uuid} / ext4 errors=remount-ro,discard 0 1" >/etc/fstab
  # the appliance resolves with its own unbound
  rm -f /etc/resolv.conf
  echo "nameserver 127.0.0.1" >/etc/resolv.conf
  # nobody logs in: hlabd reaches the agent over hv_sock
  passwd --lock root >/dev/null
  # the Storage Boxes' sshd comes with phase 5, with host keys of each
  # appliance's own
  rm -f /etc/ssh/ssh_host_*
  systemctl enable --quiet hlab-agent.service nftables.service \
    systemd-networkd.service unbound.service
  # their pictures come with later phases (pebble has no unit); networkd
  # must not hold up the boot waiting for the uplink
  systemctl disable --quiet haproxy.service knot.service ssh.service \
    systemd-networkd-wait-online.service
  # the VM has no COM port, so a getty on the kernel's console=ttyS0 would
  # fail and restart every 10 s; nobody logs in anyway (the console: 6a)
  systemctl mask --quiet serial-getty@ttyS0.service
  # the kernel and its initrd, where the boot loader's grub.cfg finds them
  test -e /vmlinuz -a -e /initrd.img
}

main "$@"
