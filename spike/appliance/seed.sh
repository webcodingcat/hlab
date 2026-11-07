#!/bin/bash
# Seed for the spike appliance: Debian genericcloud plus the packages of plan 1.3.
# eth0 host link, eth1 public switch (guest gateway), eth2 uplink (Default Switch, DHCP).
set -euo pipefail
out=$1
key=$(cat ~/hlab-spike-data/id_ed25519.pub)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
printf 'instance-id: appliance-%s\nlocal-hostname: appliance\n' "$(date +%s)" > "$work/meta-data"
cat > "$work/network-config" <<'NET'
version: 2
ethernets:
  eth0:
    match: {macaddress: "00:15:5d:bb:00:01"}
    set-name: eth0
    addresses: [198.18.0.2/29, "2001:db8:0:ffff::2/64"]
  eth1:
    match: {macaddress: "00:15:5d:bb:00:02"}
    set-name: eth1
    addresses: [172.31.1.1/32, "fe80::1/64"]
    ipv6-address-generation: eui64
  eth2:
    match: {macaddress: "00:15:5d:bb:00:03"}
    set-name: eth2
    dhcp4: true
    dhcp6: false
NET
cat > "$work/user-data" <<USER
#cloud-config
ssh_authorized_keys:
  - $key
package_update: true
packages: [nftables, dnsmasq, knot, unbound, haproxy, pebble, openssh-server, rsync,
           borgbackup, btrfs-progs, qemu-utils, iperf3, conntrack]
write_files:
  - path: /etc/sysctl.d/90-router.conf
    content: |
      net.ipv4.ip_forward = 1
      net.ipv6.conf.all.forwarding = 1
      net.ipv4.conf.all.send_redirects = 0
      net.ipv4.conf.eth1.send_redirects = 0
      net.ipv6.conf.all.accept_ra = 0
  - path: /usr/local/sbin/spike-routes
    permissions: "0755"
    content: |
      #!/bin/sh
      # Routes to the test guests (stage B: pushed by the agent).
      for n in 10 11 12; do
        ip route replace 198.18.16.\$n/32 dev eth1 src 172.31.1.1
        ip -6 route replace 2001:db8:1::/64 dev eth1
      done
runcmd:
  - [sysctl, --system]
  - [systemctl, disable, --now, dnsmasq, knot, unbound, haproxy]
  - [/usr/local/sbin/spike-routes]
USER
xorriso -as mkisofs -quiet -o "$out/cidata.iso" -V cidata -J -R "$work"/*
