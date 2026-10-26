#!/usr/bin/env bash
# Low-level lab primitives (taps, QEMU, QMP, nftables, Storage Box). Called by
# hlabd, which keeps the state and allocates names and addresses; nothing here
# decides anything.
#
# Like Hetzner's networks the segments are routed, not bridged: every VM NIC is
# its own tap, the container answers ARP for everything (proxy ARP) and routes
# between the taps, so a network cut is a drop rule in the forward chain. Every
# cloud network routes in its own table (the network's ID), selected by the
# incoming private tap: networks may overlap like on Hetzner, the same private
# IP in two networks are two different servers.
set -euo pipefail
# set -e also inside $(...)
shopt -s inherit_errexit

# The lab's addresses come from hlabd (hlabd/config.py) with every call.
readonly GATEWAY="${HLAB_GATEWAY:?}"
# The gateway's IPv6 link-local address, on every tap and client veth.
readonly GATEWAY6="${HLAB_GATEWAY6:?}"
readonly SERVICES="${HLAB_SERVICES:?}"
readonly SERVICES6="${HLAB_SERVICES6:?}"
readonly SERVICES_NET="${HLAB_SERVICES_NET:?}"
readonly METADATA="${HLAB_METADATA:?}"
# Where fe80::a9fe:a9fe goes (DNAT): the metadata service only.
readonly METADATA6="${HLAB_METADATA6:?}"
# Source of what the container itself sends to VMs (and of the consumers in its
# namespace).
readonly OPERATOR="${HLAB_OPERATOR:?}"
readonly OPERATOR6="${HLAB_OPERATOR6:?}"
# Source of `hlab ssh`, exempt from Cloud firewalls.
readonly ADMIN="${HLAB_ADMIN:?}"
readonly ADMIN6="${HLAB_ADMIN6:?}"
# The api.hetzner.com stand-in (Storage Boxes).
readonly HETZNER_API="${HLAB_HETZNER_API:?}"
readonly HETZNER_API6="${HLAB_HETZNER_API6:?}"
# Servers' IPv4 (<net>.0/24), Floating IPv4 (<net>.0/24), "Internet" clients
# (<net>.0/24) and the IPv6 prefix of everything (<prefix>::/32).
readonly PUBLIC_NET="${HLAB_PUBLIC_NET:?}"
readonly FLOATING_NET="${HLAB_FLOATING_NET:?}"
readonly CLIENT_NET="${HLAB_CLIENT_NET:?}"
readonly IPV6_PREFIX="${HLAB_IPV6_PREFIX:?}"

# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

# send_qmp <vm dir> <json command>: Send a QMP command to the VM's QEMU. Fail
# when QEMU does not answer or answers with an error, also when an HMP command
# (device_add, netdev_add, ...) returns an error text. QEMU's error goes to
# stderr.
send_qmp() {
  local vm_dir="$1" qmp_command="$2" answer errors
  answer="$(printf '{"execute":"qmp_capabilities"}\n%s\n' "${qmp_command}" \
    | socat -t1 - "UNIX-CONNECT:${vm_dir}/qmp.sock")" \
    || {
      echo "hlab.sh: qmp: no answer from ${vm_dir}" >&2
      return 1
    }
  # Print QMP errors; failing those, the string returns of HMP commands if one
  # of them is an error text. An answer that is not JSON counts as no error.
  errors="$(printf '%s\n' "${answer}" | jq -rs '
    def error_text: (.return | type) == "string"
      and (.return | test("^Error|error:"; "i"));
    if any(.[]; .error) then .[] | select(.error) | .error.desc
    elif any(.[]; error_text) then
      .[] | select((.return | type) == "string") | .return
    else empty end' 2>/dev/null)" || true
  if [[ -n "${errors}" ]]; then
    printf '%s\n' "${errors}" >&2
    return 1
  fi
}

# send_hmp <vm dir> <monitor command>: Send a human monitor command through
# QMP.
send_hmp() {
  local vm_dir="$1" monitor_command="$2"
  send_qmp "${vm_dir}" "$(jq -cn --arg c "${monitor_command}" \
    '{execute: "human-monitor-command", arguments: {"command-line": $c}}')"
}

# init_net: Set up the gateway's addresses, the unreachable ends of the lab's
# ranges and the nftables ruleset.
init_net() {
  local uplink address
  uplink="$(ip -4 route show default | awk '{print $5; exit}')"
  link_exists hlabgw || ip link add hlabgw type dummy
  for address in "${GATEWAY}" "${SERVICES}" "${METADATA}" "${OPERATOR}" \
    "${ADMIN}" "${HETZNER_API}"; do
    ip addr replace "${address}/32" dev hlabgw
  done
  # Docker starts containers with IPv6 off; taps created later inherit
  # "default".
  sysctl -qw net.ipv6.conf.all.disable_ipv6=0 \
    net.ipv6.conf.default.disable_ipv6=0
  ip link set hlabgw up
  sysctl -qw net.ipv6.conf.hlabgw.disable_ipv6=0
  for address in "${SERVICES6}" "${OPERATOR6}" "${ADMIN6}" "${HETZNER_API6}" \
    "${METADATA6}"; do
    ip -6 addr replace "${address}/128" dev hlabgw nodad
  done
  sysctl -qw net.ipv4.ip_forward=1 net.ipv6.conf.all.forwarding=1
  # The lab's public ranges end here when nothing is routed to an address (a
  # Floating IP of a server that is off, an address no one has): never out of
  # the uplink into the Docker host's network.
  ip route replace unreachable "${PUBLIC_NET}.0/24"
  ip route replace unreachable "${FLOATING_NET}.0/24"
  ip -6 route replace unreachable "${IPV6_PREFIX}::/32"
  # Internet clients (each has its own route).
  ip route replace unreachable "${CLIENT_NET}.0/24"
  # The lab's services (local addresses win).
  ip route replace unreachable "${SERVICES_NET}"
  nft -f - <<NFT
table inet hlab
delete table inet hlab
table inet hlab {
    # traffic between two interfaces dropped
    set cuts { type ifname . ifname; }
    # interface cut off from everything
    set isolated { type ifname; }
    # interface may not reach a lab service port
    set blocked_svc { type ifname . inet_service; }
    # interface may not reach the real Internet
    set blocked_inet { type ifname; }
    # private taps of the same cloud network
    set private_pairs { type ifname . ifname; }
    # the IPv4 sources a public tap or client may send from
    set src4 { type ifname . ipv4_addr; }
    # and the IPv6 ones (prefixes)
    set src6 { type ifname . ipv6_addr; flags interval; }
    # private tap → its network's routing table
    map tap_tables { type ifname : mark; }
    chain antispoof {
        # Like Hetzner: a server sends from its own addresses (primary IPs,
        # assigned Floating IPs) only, a client from its own. Cloud networks
        # are not checked (a gateway server forwards for others there).
        type filter hook prerouting priority -150;
        iifname "pub*" ip saddr 0.0.0.0 udp dport 67 accept
        # DAD, MLD
        iifname "pub*" ip6 saddr :: icmpv6 type { nd-neighbor-solicit, mld2-listener-report } accept
        iifname "pub*" meta nfproto ipv4 iifname . ip saddr != @src4 drop
        iifname "pub*" meta nfproto ipv6 iifname . ip6 saddr != @src6 drop
        iifname "cl*" ip6 saddr { ::, fe80::/10 } accept
        iifname "cl*" meta nfproto ipv4 iifname . ip saddr != @src4 drop
        iifname "cl*" meta nfproto ipv6 iifname . ip6 saddr != @src6 drop
        # cloud networks carry IPv4 only
        iifname "prv*" meta nfproto ipv6 drop
        # the network's routing table, for the replies
        iifname "prv*" ct mark set iifname map @tap_tables
    }
    chain route_out {
        # what the lab itself sends on a connection from a cloud network goes
        # back through that network's table
        type route hook output priority mangle;
        ct mark != 0 meta mark set ct mark
    }
    chain input {
        type filter hook input priority 0;
        iifname "${uplink}" ct state established,related accept
        # neighbour containers and the Docker host get nothing of the lab
        iifname "${uplink}" drop
        iifname @isolated drop
        ip daddr { ${SERVICES}, ${HETZNER_API} } iifname . tcp dport @blocked_svc drop
        ip6 daddr { ${SERVICES6}, ${HETZNER_API6} } iifname . tcp dport @blocked_svc drop
    }
    chain output {
        type filter hook output priority 0;
        oifname @isolated drop
        # Cloud networks are not reachable from outside; only DHCP (and ICMP
        # errors) come from the gateway.
        oifname "prv*" udp sport 67 accept
        oifname "prv*" meta l4proto icmp accept
        # the metadata service, for private-only servers
        oifname "prv*" ip saddr ${METADATA} tcp sport 80 accept
        oifname "prv*" drop
    }
    chain forward {
        type filter hook forward priority 0;
        iifname "${uplink}" ct state established,related accept
        # no packets (with any source) from outside to the guests
        iifname "${uplink}" drop
        # the lab's IPv6 is its own: none of it leaves
        oifname "${uplink}" meta nfproto ipv6 drop
        iifname @isolated drop
        oifname @isolated drop
        iifname . oifname @cuts drop
        iifname @blocked_inet oifname "${uplink}" drop
        oifname "${uplink}" ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 100.64.0.0/10 } drop
        # A cloud network only carries traffic between its own members.
        oifname "prv*" iifname . oifname != @private_pairs drop
        # and nothing leaves them (Internet only through a gateway server)
        iifname "prv*" oifname != "prv*" drop
    }
    chain prerouting {
        type nat hook prerouting priority dstnat;
        # the metadata service over IPv6 (link-local, answered on every public
        # tap)
        ip6 daddr fe80::a9fe:a9fe tcp dport 80 dnat ip6 to [${METADATA6}]:80
    }
    chain postrouting {
        type nat hook postrouting priority srcnat;
        # public addresses only: private ones reach the Internet through a
        # gateway server, as on Hetzner
        oifname "${uplink}" ip saddr { ${PUBLIC_NET}.0/24, ${FLOATING_NET}.0/24, ${CLIENT_NET}.0/24 } masquerade
    }
}
NFT
}

# set_up_tap <tap> <gateway cidr> [guest ip] [noprefixroute]: Create a tap
# with the gateway address and proxy ARP, and route the guest IP to it.
set_up_tap() {
  local tap="$1" gateway_cidr="$2" guest_ip="${3:-}" noprefixroute="${4:-}"
  link_exists "${tap}" || ip tuntap add "${tap}" mode tap
  sysctl -qw "net.ipv6.conf.${tap}.disable_ipv6=0"
  ip link set "${tap}" mtu 1450 up
  # noprefixroute: several taps carry the same gateway address; only the /32
  # routes below decide where packets go.
  ip addr replace "${gateway_cidr}" dev "${tap}" \
    ${noprefixroute:+noprefixroute}
  ip -6 addr replace "${GATEWAY6}/64" dev "${tap}" nodad
  if [[ -n "${guest_ip}" ]]; then
    ip route replace "${guest_ip}/32" dev "${tap}" src "${OPERATOR}"
  fi
  sysctl -qw "net.ipv4.conf.${tap}.proxy_arp=1"
}

# set_up_public_tap <tap> [guest ip]: Set up a server's public NIC, with DHCP
# and IPv6 metadata like Hetzner's.
set_up_public_tap() {
  local tap="$1" guest_ip="${2:-}"
  set_up_tap "${tap}" "${GATEWAY}/32" "${guest_ip}"
  # Hetzner's public network: 1500 (cloud networks: 1450).
  ip link set "${tap}" mtu 1500
  # An address in the public range, so dnsmasq serves DHCP here (the /32 routes
  # still decide).
  ip addr replace "${PUBLIC_NET}.1/24" dev "${tap}" noprefixroute
  ip -6 addr replace fe80::a9fe:a9fe/64 dev "${tap}" nodad
}

# set_up_private_tap <tap> <gateway cidr> <guest ip> <table> <range>
# [alias ip]...: Set up a NIC in a cloud network and route the guest IP and its
# alias IPs to the tap in the network's table.
set_up_private_tap() {
  local tap="$1" gateway_cidr="$2" guest_ip="$3" table="$4" range="$5"
  shift 5
  local alias_ips=("$@") keep route alias_ip mark
  # The gateway address of an earlier range is removed.
  if link_exists "${tap}"; then
    ip -4 addr flush dev "${tap}"
  fi
  set_up_tap "${tap}" "${gateway_cidr}" "" noprefixroute
  # Cloud networks are IPv4 only, as on Hetzner.
  sysctl -qw "net.ipv6.conf.${tap}.disable_ipv6=1"
  # Alias IPs given up are removed; routes through a gateway server (via) stay.
  # A table that never had a route does not exist yet: nothing to remove.
  keep=" ${guest_ip} ${alias_ips[*]} "
  for route in $(ip route show table "${table}" dev "${tap}" 2>/dev/null \
    | awk '!/ via / {print $1}'); do
    [[ "${keep}" == *" ${route%/32} "* ]] \
      || ip route del "${route}" dev "${tap}" table "${table}"
  done
  ip route replace "${guest_ip}/32" dev "${tap}" src "${OPERATOR}" \
    table "${table}"
  for alias_ip in "${alias_ips[@]}"; do
    ip route replace "${alias_ip}/32" dev "${tap}" src "${OPERATOR}" \
      table "${table}"
  done
  # Members only; nothing of the range leaks to the main table, unless a
  # default route is there (then the add fails with "File exists": fine).
  ip route replace unreachable "${range}" table "${table}"
  ip route add unreachable default table "${table}" 2>/dev/null || true
  mark="$(printf '0x%x' "${table}")"
  ip rule show pref 1002 \
    | grep -q "fwmark ${mark} lookup ${table}\$" \
    || ip rule add fwmark "${table}" lookup "${table}" pref 1002
  # Replies of the lab end there too when the table has no route (its gateway
  # server is off).
  ip rule show pref 1003 \
    | grep -q "fwmark ${mark} unreachable" \
    || ip rule add fwmark "${table}" unreachable pref 1003
  # Nothing else either: what the network's table has no route for (no
  # add_route, or its gateway server is off) ends at the second rule instead
  # of falling through to the main table. Rules are only replaced when they
  # differ, so a running tap never loses them for a moment.
  if ! ip rule show pref 1000 | grep -q "iif ${tap} lookup ${table}\$" \
    || ! ip rule show pref 1001 | grep -q "iif ${tap} unreachable"; then
    while ip rule del iif "${tap}" 2>/dev/null; do :; done
    ip rule add iif "${tap}" lookup "${table}" pref 1000
    ip rule add iif "${tap}" unreachable pref 1001
  fi
}

# tear_down_tap <tap>: Remove the tap and its rules.
tear_down_tap() {
  local tap="$1"
  if link_exists "${tap}"; then
    ip link del "${tap}"
  fi
  while ip rule del iif "${tap}" 2>/dev/null; do :; done
}

# link_exists <link>: Succeed if the network interface exists.
link_exists() {
  local link="$1"
  ip link show "${link}" >/dev/null 2>&1
}

# list_neigh_devs <ip>: Print every tap the address sits on.
list_neigh_devs() {
  local ip="$1"
  ip neigh show "${ip}" \
    | awk '{for (i = 1; i < NF; i++) if ($i == "dev") print $(i + 1)}' \
    | sort -u
}

# list_local_addrs: Print the container's own IPv4 addresses and its default
# gateway (the Docker host's).
list_local_addrs() {
  ip -4 -o addr show | awk '{print $4}' | cut -d/ -f1
  ip -4 route show default | awk '{print $3}'
}

# set_route <prefix> [tap]: Route an address or prefix (v4 or v6) to a tap, or
# remove the route.
set_route() {
  local prefix="$1" tap="${2:-}" src="${OPERATOR}"
  [[ "${prefix}" == *:* ]] && src="${OPERATOR6}"
  if [[ -n "${tap}" ]]; then
    ip route replace "${prefix}" dev "${tap}" src "${src}"
  else
    # not routed (anymore): fine
    ip route del "${prefix}" 2>/dev/null || true
  fi
}

# set_route6 <prefix> <tap> <guest link-local>: Route an IPv6 prefix to a
# guest, as Hetzner routes /64s.
set_route6() {
  local prefix="$1" tap="$2" guest_link_local="$3"
  ip -6 route replace "${prefix}" via "${guest_link_local}" dev "${tap}" \
    src "${OPERATOR6}"
}

# set_route_via <prefix> <gateway ip> <tap> <table>: Route a prefix in a
# network's table through a gateway server.
set_route_via() {
  local prefix="$1" gateway_ip="$2" tap="$3" table="$4"
  ip route replace "${prefix}" via "${gateway_ip}" dev "${tap}" onlink \
    table "${table}"
}

# delete_route <prefix> <table>: Delete a network route and bring the table's
# end back (a default route had taken its place).
delete_route() {
  local prefix="$1" table="$2"
  # never routed (its gateway server was off) or gone already: fine; the end
  # is there already unless a default route had taken its place
  ip route del "${prefix}" table "${table}" 2>/dev/null || true
  ip route add unreachable default table "${table}" 2>/dev/null || true
}

# flush_routes <table>: Remove the routes and rules of a deleted network.
flush_routes() {
  local table="$1"
  # a table that never had a route does not exist: fine
  ip route flush table "${table}" 2>/dev/null || true
  while ip rule del fwmark "${table}" 2>/dev/null; do :; done
}

# add_client <name> <veth> <ipv4> <ipv6>: Create a client, a network
# namespace on the "Internet".
add_client() {
  local name="$1" veth="$2" ipv4="$3" ipv6="$4"
  local client_gateway="${CLIENT_NET}.1"
  [[ -e "/run/netns/${name}" ]] || ip netns add "${name}"
  link_exists "${veth}" \
    || ip link add "${veth}" type veth peer name eth0 netns "${name}"
  sysctl -qw "net.ipv6.conf.${veth}.disable_ipv6=0"
  ip netns exec "${name}" sysctl -qw net.ipv6.conf.all.disable_ipv6=0 \
    net.ipv6.conf.default.disable_ipv6=0 net.ipv6.conf.eth0.disable_ipv6=0
  ip link set "${veth}" up
  ip addr replace "${client_gateway}/32" dev "${veth}" noprefixroute
  ip -6 addr replace "${GATEWAY6}/64" dev "${veth}" nodad
  ip route replace "${ipv4}/32" dev "${veth}" src "${OPERATOR}"
  ip route replace "${ipv6}/128" dev "${veth}" src "${OPERATOR6}"
  sysctl -qw "net.ipv4.conf.${veth}.proxy_arp=1"
  ip -n "${name}" link set lo up
  ip -n "${name}" link set eth0 up
  ip -n "${name}" addr replace "${ipv4}/32" dev eth0
  ip -n "${name}" -6 addr replace "${ipv6}/128" dev eth0 nodad
  ip -n "${name}" route replace "${client_gateway}/32" dev eth0 scope link
  ip -n "${name}" route replace default via "${client_gateway}" dev eth0 onlink
  ip -n "${name}" -6 route replace default via "${GATEWAY6}" dev eth0
  mkdir -p "/etc/netns/${name}"
  echo "nameserver ${SERVICES}" >"/etc/netns/${name}/resolv.conf"
}

# delete_client <name> <veth>: Remove a client and its namespace.
delete_client() {
  local name="$1" veth="$2"
  if link_exists "${veth}"; then
    ip link del "${veth}"
  fi
  if [[ -e "/run/netns/${name}" ]]; then
    ip netns del "${name}"
  fi
  rm -rf "/etc/netns/${name}"
}

# stop_dhcp <conf>: Stop the DHCP dnsmasq of one tap (hlabd writes the file).
# One instance per tap: with a single interface dnsmasq binds its socket to it,
# so the same IP can be handed out in two networks. Every earlier instance must
# be gone: a leftover one answers NAK ("address not available").
stop_dhcp() {
  local conf="$1"
  # exit status 1: none runs
  pkill -f "^dnsmasq --conf-file=${conf}\$" || (($? == 1))
  for _ in $(seq 1 50); do
    pgrep -f "^dnsmasq --conf-file=${conf}\$" >/dev/null || break
    sleep 0.1
  done
}

# restart_dhcp <conf>: (Re)start the DHCP dnsmasq of one tap.
restart_dhcp() {
  local conf="$1"
  stop_dhcp "${conf}"
  dnsmasq --conf-file="${conf}"
}

# start_dns <conf file>: Start the lab resolver on the services address.
start_dns() {
  local conf_file="$1" upstream
  stop_pid /run/dnsmasq-dns.pid dnsmasq
  upstream="$(awk '/^nameserver/ {print $2; exit}' /etc/resolv.conf)"
  dnsmasq --listen-address="${SERVICES},${SERVICES6}" --bind-interfaces \
    --no-resolv --server="${upstream}" --conf-file="${conf_file}" \
    --pid-file=/run/dnsmasq-dns.pid
}

# fetch_image <name> <url> <checksum file url> <dir>: Download and check the
# image in the version the checksum file names, unless it is there, as
# <dir>/<name>-<first 12 characters of the checksum>.qcow2. Print its path.
fetch_image() {
  local name="$1" url="$2" sums_url="$3" dir="$4"
  local file sums checksum dst sum_tool
  file="$(basename "${url}")"
  sums="$(curl -fsSL --connect-timeout 30 --max-time 120 "${sums_url}")"
  checksum="$(printf '%s\n' "${sums}" \
    | awk -v f="${file}" '{n = $2; sub(/^\*/, "", n)} n == f {print $1}')"
  if [[ -z "${checksum}" ]]; then
    echo "hlab.sh: no checksum for ${file} in ${sums_url}" >&2
    return 1
  fi
  dst="${dir}/${name}-${checksum:0:12}.qcow2"
  if [[ ! -f "${dst}" ]]; then
    mkdir -p "${dir}"
    curl -fsSL --connect-timeout 30 --speed-limit 1024 --speed-time 60 \
      -o "${dst}.tmp" "${url}"
    sum_tool=sha256sum
    ((${#checksum} == 128)) && sum_tool=sha512sum
    if ! echo "${checksum}  ${dst}.tmp" | "${sum_tool}" -c --quiet - >&2; then
      rm -f "${dst}.tmp"
      echo "hlab.sh: checksum mismatch for ${url}" >&2
      return 1
    fi
    mv "${dst}.tmp" "${dst}"
  fi
  echo "${dst}"
}

# create_vm_disk <dir> <base> <size gb>: Create the server's disk, an overlay
# on a system image; keep it when it is there.
create_vm_disk() {
  local dir="$1" base="$2" size_gb="$3"
  [[ -f "${dir}/disk.qcow2" ]] && return 0
  qemu-img create -q -f qcow2 -F qcow2 -b "${base}" "${dir}/disk.qcow2" \
    "${size_gb}G"
}

# resize_vm_disk <dir> <size gb>: Grow the server's disk (qemu-img grows
# only).
resize_vm_disk() {
  local dir="$1" size_gb="$2"
  qemu-img resize -q "${dir}/disk.qcow2" "${size_gb}G"
}

# print_nic_netdev <tap>: Print the QEMU netdev of a NIC's tap (the tap names
# it).
print_nic_netdev() {
  local tap="$1"
  printf 'tap,id=%s,ifname=%s,script=no,downscript=no\n' "${tap}" "${tap}"
}

# print_nic_device <tap> <mac> [bus]: Print the QEMU device of a NIC, for the
# boot and for hot-plugging alike.
print_nic_device() {
  local tap="$1" mac="$2" bus="${3:-}"
  printf 'virtio-net-pci,netdev=%s,id=dev-%s,mac=%s%s\n' \
    "${tap}" "${tap}" "${mac}" "${bus:+,bus=${bus}}"
}

# print_volume_drive <id> <file>: Print the QEMU drive of a volume.
print_volume_drive() {
  local id="$1" file="$2"
  printf 'file=%s,format=raw,if=none,id=vol%s,cache=none\n' "${file}" "${id}"
}

# print_volume_device <id>: Print the QEMU device of a volume on the SCSI
# controller, identifying like Hetzner (scsi-0HC_Volume_<id>).
print_volume_device() {
  local id="$1"
  printf 'scsi-hd,drive=vol%s,id=dvol%s,bus=scsi0.0,%s\n' "${id}" "${id}" \
    "vendor=HC,product=Volume,serial=${id}"
}

# start_vm <dir> <name> <ram mb> <cpus> <serial> <tap=mac>...: Start QEMU; the
# first NIC is the public one.
start_vm() {
  local dir="$1" name="$2" ram="$3" cpus="$4" serial="$5"
  shift 5
  local nics=() disks=() nic_index=0 spec tap mac slot vid vpath
  for spec in "$@"; do
    tap="${spec%%=*}"
    mac="${spec#*=}"
    slot=$((10 + nic_index))
    nics+=(-netdev "$(print_nic_netdev "${tap}")")
    if ((nic_index == 0)); then
      # The public NIC stays for the server's life: on the root bus.
      nics+=(-device "$(print_nic_device "${tap}" "${mac}")")
    else
      # A network's NIC can be detached later: q35 unplugs only from a root
      # port, so it gets its own.
      nics+=(-device
        "pcie-root-port,id=bp${nic_index},chassis=${slot},slot=${slot}"
        -device "$(print_nic_device "${tap}" "${mac}" "bp${nic_index}")")
    fi
    nic_index=$((nic_index + 1))
  done
  # cache=writeback: the guest's flushes reach the host. A power-off kills
  # qemu, and with cache=unsafe qcow2 metadata and flushed data still in qemu's
  # memory were lost. Hetzner keeps what a guest flushed.
  disks=(-drive "file=${dir}/disk.qcow2,if=none,id=root,cache=writeback"
    -device "virtio-blk-pci,drive=root,bootindex=1")
  # Attached volumes (id path per line) sit on the SCSI controller from the
  # boot.
  if [[ -f "${dir}/volumes" ]]; then
    while read -r vid vpath; do
      [[ -n "${vid}" ]] || continue
      disks+=(-drive "$(print_volume_drive "${vid}" "${vpath}")"
      -device "$(print_volume_device "${vid}")")
    done <"${dir}/volumes"
  fi
  # SMBIOS like Hetzner's: cloud-init recognises the platform by the
  # manufacturer and checks the serial. No device the guests do not need (no
  # USB, no 9p, no balloon, no QXL) and no VAPIC option ROM: less to attack from
  # a guest. The virtio-scsi controller is there from the boot: a disk on a
  # controller hot-plugged just before is missed. q35 hot-plugs only into PCIe
  # root ports: four of them (hp1..hp4) for networks attached later.
  qemu-system-x86_64 -machine q35,usb=off -enable-kvm -cpu host \
    -smp "${cpus}" -m "${ram}" -name "${name}" \
    -global kvm-apic.vapic=off \
    -sandbox on,obsolete=deny,resourcecontrol=deny \
    -smbios "type=1,manufacturer=Hetzner,product=vServer,serial=${serial}" \
    -device virtio-scsi-pci,id=scsi0 \
    "${disks[@]}" \
    -device pcie-root-port,id=hp1,chassis=1,slot=1 \
    -device pcie-root-port,id=hp2,chassis=2,slot=2 \
    -device pcie-root-port,id=hp3,chassis=3,slot=3 \
    -device pcie-root-port,id=hp4,chassis=4,slot=4 \
    "${nics[@]}" \
    -device virtio-serial-pci \
    -chardev "socket,path=${dir}/qga.sock,server=on,wait=off,id=qga0" \
    -device virtserialport,chardev=qga0,name=org.qemu.guest_agent.0 \
    -qmp "unix:${dir}/qmp.sock,server,nowait" \
    -chardev "file,id=ser0,path=${dir}/console.log,append=on" \
    -serial chardev:ser0 \
    -display none -daemonize -pidfile "${dir}/qemu.pid"
}

# kill_vm <dir>: Kill the VM's QEMU.
kill_vm() {
  local dir="$1"
  stop_pid "${dir}/qemu.pid" qemu-system KILL
}

# is_vm_alive <dir>: Succeed while the VM's QEMU runs.
is_vm_alive() {
  local dir="$1"
  is_alive "${dir}/qemu.pid" qemu-system
}

# add_nic <vm dir> <tap> <mac> <root port>: Hot-plug a NIC; the tap names
# its QEMU netdev too.
add_nic() {
  local vm_dir="$1" tap="$2" mac="$3" root_port="$4"
  send_hmp "${vm_dir}" "netdev_add $(print_nic_netdev "${tap}")"
  send_hmp "${vm_dir}" \
    "device_add $(print_nic_device "${tap}" "${mac}" "${root_port}")"
}

# delete_nic <vm dir> <tap>: Unplug a NIC.
delete_nic() {
  local vm_dir="$1" tap="$2"
  send_hmp "${vm_dir}" "device_del dev-${tap}"
  sleep 1
  send_hmp "${vm_dir}" "netdev_del ${tap}"
}

# create_volume <file> <gb> [ext4|xfs]: Create a sparse raw volume, formatted
# on request.
create_volume() {
  local file="$1" size_gb="$2" format="${3:-}"
  truncate -s "${size_gb}G" "${file}"
  case "${format}" in
    ext4) mkfs.ext4 -q -F "${file}" ;;
    xfs) mkfs.xfs -q -f "${file}" ;;
  esac
}

# attach_volume <vm dir> <id> <file>: Hot-plug a volume.
attach_volume() {
  local vm_dir="$1" id="$2" file="$3"
  send_hmp "${vm_dir}" "drive_add 0 $(print_volume_drive "${id}" "${file}")"
  send_hmp "${vm_dir}" "device_add $(print_volume_device "${id}")"
}

# detach_volume <vm dir> <id>: Unplug a volume.
detach_volume() {
  local vm_dir="$1" id="$2"
  send_hmp "${vm_dir}" "device_del dvol${id}"
  sleep 1
  # QEMU may have removed the drive with its device already
  send_hmp "${vm_dir}" "drive_del vol${id}" 2>/dev/null || true
}

# resize_volume <file> <gb> [vm dir] [id]: Grow a volume; a running VM sees
# the new size at once.
resize_volume() {
  local file="$1" size_gb="$2" vm_dir="${3:-}" id="${4:-}"
  if [[ -n "${vm_dir}" ]]; then
    send_hmp "${vm_dir}" "block_resize vol${id} ${size_gb}G"
  else
    truncate -s "${size_gb}G" "${file}"
  fi
}

# main <verb> <arg>...: Run one verb.
main() {
  local verb="${1:-}"
  shift || true
  # verbs are <noun>-<verb>, the API side's vocabulary; each maps to a <verb>_<noun>
  # function, so volume-create is found in create_volume
  case "${verb}" in
    net-init) init_net ;;
    public-tap-up) set_up_public_tap "$@" ;;
    private-tap-up) set_up_private_tap "$@" ;;
    tap-down) tear_down_tap "$@" ;;
    link-exists) link_exists "$@" ;;
    neigh-dev) list_neigh_devs "$@" ;;
    local-addrs) list_local_addrs ;;
    volume-create) create_volume "$@" ;;
    volume-attach) attach_volume "$@" ;;
    volume-detach) detach_volume "$@" ;;
    volume-resize) resize_volume "$@" ;;
    image-fetch) fetch_image "$@" ;;
    vm-disk) create_vm_disk "$@" ;;
    vm-disk-resize) resize_vm_disk "$@" ;;
    vm-start) start_vm "$@" ;;
    vm-kill) kill_vm "$@" ;;
    vm-alive) is_vm_alive "$@" ;;
    qmp) send_qmp "$@" ;;
    hmp) send_hmp "$@" ;;
    nic-add) add_nic "$@" ;;
    nic-del) delete_nic "$@" ;;
    route) set_route "$@" ;;
    route6) set_route6 "$@" ;;
    route-via) set_route_via "$@" ;;
    route-del) delete_route "$@" ;;
    route-flush) flush_routes "$@" ;;
    # nft and tc take hlabd's rules as they are (fabric/faults.py, network.py,
    # firewall.py)
    nft) nft "$@" ;;
    tc) tc "$@" ;;
    client-add) add_client "$@" ;;
    client-del) delete_client "$@" ;;
    dhcp-stop) stop_dhcp "$@" ;;
    dhcp-restart) restart_dhcp "$@" ;;
    dns-start) start_dns "$@" ;;
    box-*) exec "$(dirname "$0")/box.sh" "${verb#box-}" "$@" ;;
    *)
      echo "hlab.sh: unknown command: ${verb}" >&2
      exit 2
      ;;
  esac
}

main "$@"
