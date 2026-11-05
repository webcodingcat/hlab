#!/usr/bin/env bash
# Low-level lab commands: taps, QEMU, QMP, nftables, Storage Box. hlabd calls
# them. hlabd keeps the state and picks names and addresses; nothing here
# decides anything. The nftables table is in hlab.nft.
#
# As on Hetzner, the segments are routed, not bridged. Every VM NIC is a
# separate tap. The container answers ARP for every address (proxy ARP) and
# routes between the taps. So a network cut is a drop rule in the forward
# chain. Every cloud network routes in its own table (the network's ID), and
# the private tap that a packet comes in on picks the table. So networks may
# overlap, as on Hetzner: the same private IP in two networks belongs to two
# different servers.
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
# The web UI's port; Docker publishes it on the host's localhost. Only
# print_ruleset reads it, by name.
# shellcheck disable=SC2034
readonly UI_PORT="${HLAB_UI_PORT:?}"
# The SOCKS5 proxy's port for the operator's host, published the same way.
readonly SOCKS_PORT="${HLAB_SOCKS_PORT:?}"

# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

# How long QEMU may take to answer a QMP command, in seconds. A VM that boots
# on a busy host can take seconds; hlabd's own timeout for a command is
# longer.
readonly QMP_TIMEOUT=30

# send_qmp <vm dir> <json command>: Send a QMP command to the VM's QEMU. Fail
# when QEMU does not answer or answers with an error, also when an HMP command
# (device_add, netdev_add, ...) returns an error text. QEMU's error goes to
# stderr.
send_qmp() {
  local vm_dir="$1" qmp_command="$2" answer error
  answer="$(query_qmp "${vm_dir}" "${qmp_command}")" || return 1
  error="$(jq -r '
    if .error then .error.desc
    elif (.return | type) == "string"
      and (.return | test("^Error|error:"; "i")) then .return
    else empty end' <<<"${answer}")"
  if [[ -n "${error}" ]]; then
    printf '%s\n' "${error}" >&2
    return 1
  fi
}

# print_vm_counters <vm dir> <tap>: Print the VM's counters as one line of
# JSON: the CPU seconds of its QEMU process, the I/O of its root disk and the
# traffic of its public tap (the tap receives what the guest sends). Fail when
# the VM does not run.
print_vm_counters() {
  local vm_dir="$1" tap="$2" pid cpu_ticks disk stats
  pid="$(cat "${vm_dir}/qemu.pid")"
  # utime and stime: fields 14 and 15, counted after the name in parentheses
  cpu_ticks="$(sed 's/.*) //' "/proc/${pid}/stat" | awk '{print $12 + $13}')"
  disk="$(query_qmp "${vm_dir}" '{"execute":"query-blockstats"}' \
    | jq -c '[.return[] | select(.device == "root") | .stats][0]')"
  stats="/sys/class/net/${tap}/statistics"
  jq -cn \
    --argjson ticks "${cpu_ticks}" --argjson hz "$(getconf CLK_TCK)" \
    --argjson disk "${disk}" \
    --argjson packets_in "$(<"${stats}/tx_packets")" \
    --argjson packets_out "$(<"${stats}/rx_packets")" \
    --argjson bytes_in "$(<"${stats}/tx_bytes")" \
    --argjson bytes_out "$(<"${stats}/rx_bytes")" \
    '{cpu_seconds: ($ticks / $hz),
      disk_reads: $disk.rd_operations, disk_writes: $disk.wr_operations,
      disk_read_bytes: $disk.rd_bytes, disk_write_bytes: $disk.wr_bytes,
      packets_in: $packets_in, packets_out: $packets_out,
      bytes_in: $bytes_in, bytes_out: $bytes_out}'
}

# query_qmp <vm dir> <json command>: Print QEMU's answer to a QMP command, one
# line of JSON ({"return": ...} or {"error": ...}). Fail when QEMU gives none
# within QMP_TIMEOUT seconds.
#
# The connection stays open until the answer is there: QEMU queues commands
# and runs them later in its main loop, and drops those still queued when the
# client hangs up. So a client that hangs up early loses its command, most
# often while the VM boots or the host is busy.
query_qmp() {
  local vm_dir="$1" qmp_command="$2" request line deadline answered=1
  local to_qemu from_qemu qmp_pid
  request="$(jq -c '. + {id: "hlab"}' <<<"${qmp_command}")"
  coproc QMP { exec socat - "UNIX-CONNECT:${vm_dir}/qmp.sock" 2>/dev/null; }
  # copies of the coproc's pipes: bash closes its own when socat ends and
  # keeps them from subshells
  qmp_pid="$!"
  exec {to_qemu}>&"${QMP[1]}" {from_qemu}<&"${QMP[0]}"
  # if socat could not connect, the write fails (no SIGPIPE ends the script)
  # and the read below finds no answer
  (
    trap '' PIPE
    printf '{"execute":"qmp_capabilities"}\n%s\n' "${request}" >&"${to_qemu}"
  ) 2>/dev/null || true
  deadline=$((SECONDS + QMP_TIMEOUT))
  # skip the greeting, the capabilities' answer and events
  while ((SECONDS < deadline)) \
    && read -r -t "$((deadline - SECONDS))" line <&"${from_qemu}"; do
    if jq -e '.id? == "hlab"' <<<"${line}" >/dev/null 2>&1; then
      printf '%s\n' "${line}"
      answered=0
      break
    fi
  done
  exec {to_qemu}>&- {from_qemu}<&-
  kill "${qmp_pid}" 2>/dev/null || true
  wait "${qmp_pid}" 2>/dev/null || true
  if ((answered != 0)); then
    echo "hlab.sh: qmp: no answer from ${vm_dir}" >&2
  fi
  return "${answered}"
}

# wait_device_gone <vm dir> <device id>: Wait until QEMU has removed a device,
# 10 seconds at most. A guest acknowledges a PCI unplug on its own time; a
# SCSI disk goes at once.
wait_device_gone() {
  local vm_dir="$1" device="$2" i
  local list='{"execute":"qom-list","arguments":{"path":"/machine/peripheral"}}'
  for ((i = 0; i < 100; i++)); do
    query_qmp "${vm_dir}" "${list}" 2>/dev/null \
      | jq -e --arg device "${device}" '[.return[].name] | any(. == $device)' \
        >/dev/null || return 0
    sleep 0.1
  done
}

# send_hmp <vm dir> <monitor command>: Send a human monitor command through
# QMP.
send_hmp() {
  local vm_dir="$1" monitor_command="$2"
  send_qmp "${vm_dir}" "$(jq -cn --arg c "${monitor_command}" \
    '{execute: "human-monitor-command", arguments: {"command-line": $c}}')"
}

# print_ruleset <uplink> <docker host>: Print the nftables table of hlab.nft,
# with the uplink, the Docker host's address and the lab's addresses put in
# for its ${NAME} placeholders.
print_ruleset() {
  local uplink="$1" docker_host="$2" ruleset name
  ruleset="$(<"$(dirname "$0")/hlab.nft")"
  for name in uplink docker_host SERVICES SERVICES6 HETZNER_API HETZNER_API6 \
    METADATA METADATA6 PUBLIC_NET FLOATING_NET CLIENT_NET UI_PORT SOCKS_PORT; do
    ruleset="${ruleset//"\${${name}}"/${!name}}"
  done
  printf '%s\n' "${ruleset}"
}

# init_net: Set up the gateway's addresses, the unreachable ends of the lab's
# ranges and the nftables ruleset.
init_net() {
  local uplink docker_host address
  uplink="$(ip -4 route show default | awk '{print $5; exit}')"
  # The default gateway is the Docker host: published ports come from there.
  docker_host="$(ip -4 route show default | awk '{print $3; exit}')"
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
  print_ruleset "${uplink}" "${docker_host}" | nft -f -
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
  # Only members are reachable: the rest of the range and everything else
  # ends here, never in the main table. If a default route (add_route
  # 0.0.0.0/0) is there already, the add fails with "File exists": fine.
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

# delete_route <prefix> <table>: Delete a network route and put the table's
# unreachable default back, in case the deleted route was a default route
# that had replaced it.
delete_route() {
  local prefix="$1" table="$2"
  # never routed (its gateway server was off) or gone already: fine; the
  # unreachable default is still there unless the deleted route replaced it
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

# add_namespace <name> <veth> <ipv4> <ipv6> <gateway>: Create a network
# namespace with public addresses behind a veth: an Internet client (gateway
# ${CLIENT_NET}.1) or a load balancer (Hetzner's gateway, as on a server).
add_namespace() {
  local name="$1" veth="$2" ipv4="$3" ipv6="$4" gateway="$5"
  [[ -e "/run/netns/${name}" ]] || ip netns add "${name}"
  link_exists "${veth}" \
    || ip link add "${veth}" type veth peer name eth0 netns "${name}"
  sysctl -qw "net.ipv6.conf.${veth}.disable_ipv6=0"
  ip netns exec "${name}" sysctl -qw net.ipv6.conf.all.disable_ipv6=0 \
    net.ipv6.conf.default.disable_ipv6=0 net.ipv6.conf.eth0.disable_ipv6=0
  ip link set "${veth}" up
  ip addr replace "${gateway}/32" dev "${veth}" noprefixroute
  ip -6 addr replace "${GATEWAY6}/64" dev "${veth}" nodad
  ip route replace "${ipv4}/32" dev "${veth}" src "${OPERATOR}"
  ip route replace "${ipv6}/128" dev "${veth}" src "${OPERATOR6}"
  sysctl -qw "net.ipv4.conf.${veth}.proxy_arp=1"
  ip -n "${name}" link set lo up
  ip -n "${name}" link set eth0 up
  ip -n "${name}" addr replace "${ipv4}/32" dev eth0
  ip -n "${name}" -6 addr replace "${ipv6}/128" dev eth0 nodad
  ip -n "${name}" route replace "${gateway}/32" dev eth0 scope link
  ip -n "${name}" route replace default via "${gateway}" dev eth0 onlink
  ip -n "${name}" -6 route replace default via "${GATEWAY6}" dev eth0
  mkdir -p "/etc/netns/${name}"
  echo "nameserver ${SERVICES}" >"/etc/netns/${name}/resolv.conf"
}

# add_namespace_nic <namespace> <veth> <ip> <gateway cidr> <table> <range>:
# Put a namespace of add_namespace (a load balancer) into a cloud network: a
# veth set up like a server's NIC (set_up_private_tap), and inside its address
# with a route to the network through the gateway.
add_namespace_nic() {
  local name="$1" veth="$2" ip="$3" gateway_cidr="$4" table="$5" range="$6"
  local gateway="${gateway_cidr%/*}"
  link_exists "${veth}" \
    || ip link add "${veth}" type veth peer name eth1 netns "${name}"
  set_up_private_tap "${veth}" "${gateway_cidr}" "${ip}" "${table}" "${range}"
  ip -n "${name}" link set eth1 mtu 1450 up
  ip -n "${name}" addr replace "${ip}/32" dev eth1
  ip -n "${name}" route replace "${gateway}/32" dev eth1 scope link
  ip -n "${name}" route replace "${range}" via "${gateway}" dev eth1 onlink
}

# delete_namespace_nic <veth>: Take a namespace out of its cloud network; its
# end inside goes with the veth.
delete_namespace_nic() {
  local veth="$1"
  if link_exists "${veth}"; then
    ip link del "${veth}"
  fi
}

# delete_namespace <name> <veth>: Remove a namespace of add_namespace.
delete_namespace() {
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

# start_nsd <conf file>: (Re)start NSD, the nameserver of the DNS zones, with
# the configuration hlabd wrote. The old one ends first, so the new one gets
# its port.
start_nsd() {
  local conf_file="$1"
  stop_pid /run/nsd-hlab.pid nsd
  nsd -c "${conf_file}"
}

# start_pebble <config file> <dns server>: (Re)start Pebble, the lab's ACME
# server, in the background. It never sleeps before a validation and never
# rejects a nonce at random (both are on in Pebble's defaults, to test clients).
# Its log goes to the container's log.
start_pebble() {
  local conf_file="$1" dns_server="$2" pid_file=/run/pebble.pid
  stop_pid "${pid_file}" pebble
  PEBBLE_VA_NOSLEEP=1 PEBBLE_WFE_NONCEREJECT=0 \
    setsid pebble -config "${conf_file}" -dnsserver "${dns_server}" \
    >/proc/1/fd/1 2>&1 </dev/null &
  echo "$!" >"${pid_file}"
}

# start_socks: (Re)start the SOCKS5 proxy (microsocks) for the operator's
# host. It runs in a mount namespace of its own whose /etc/resolv.conf names
# the lab resolver, so names of the DNS zones resolve as inside the lab.
start_socks() {
  local pid_file=/run/microsocks.pid resolv=/run/hlab-socks-resolv.conf
  stop_pid "${pid_file}" microsocks
  echo "nameserver ${SERVICES}" >"${resolv}"
  # the inner shell expands $1 and $2: the file and the port, its arguments
  # shellcheck disable=SC2016
  setsid unshare --mount sh -c 'mount --bind "$1" /etc/resolv.conf &&
    exec microsocks -q -i 0.0.0.0 -p "$2"' - "${resolv}" "${SOCKS_PORT}" \
    >/proc/1/fd/1 2>&1 </dev/null &
  echo "$!" >"${pid_file}"
}

# start_balancer <namespace> <dir>: Start HAProxy in a load balancer's
# namespace with <dir>/haproxy.cfg, or reload it: the new process takes over
# the listeners, the old one finishes its connections.
start_balancer() {
  local name="$1" dir="$2" old=()
  if is_alive "${dir}/haproxy.pid" haproxy; then
    old=(-sf "$(cat "${dir}/haproxy.pid")")
  fi
  # paths in the configuration are relative to the balancer's directory
  (cd "${dir}" && ip netns exec "${name}" haproxy -D -f haproxy.cfg \
    -p haproxy.pid "${old[@]}")
}

# is_balancer_alive <dir>: Succeed while the load balancer's HAProxy runs.
is_balancer_alive() {
  local dir="$1"
  is_alive "${dir}/haproxy.pid" haproxy
}

# stop_balancer <dir>: Stop a load balancer's HAProxy.
stop_balancer() {
  local dir="$1"
  stop_pid "${dir}/haproxy.pid" haproxy
}

# start_dns <conf file>: (Re)start the lab resolver on the services address.
# The old one ends first, so the new one gets the address.
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

# create_vm_disk_from_image <dir> <image> <size gb>: Create the server's disk
# as a copy of an image of the project, so the image can be deleted any time;
# keep the disk when it is there.
create_vm_disk_from_image() {
  local dir="$1" image="$2" size_gb="$3"
  [[ -f "${dir}/disk.qcow2" ]] && return 0
  cp --sparse=always "${image}" "${dir}/disk.qcow2.part"
  qemu-img resize -q "${dir}/disk.qcow2.part" "${size_gb}G"
  mv "${dir}/disk.qcow2.part" "${dir}/disk.qcow2"
}

# create_image <disk> <image>: Copy a server's disk into a standalone image
# file and print the file's size in bytes. It also copies the disk of a
# running VM (-U); as on Hetzner, such an image may be inconsistent.
create_image() {
  local disk="$1" image="$2"
  mkdir -p "$(dirname "${image}")"
  qemu-img convert -U -q -O qcow2 "${disk}" "${image}.part"
  mv "${image}.part" "${image}"
  stat -c %s "${image}"
}

# print_image_info <file>: Print qemu-img's view of an image file as JSON
# (format, virtual size, backing file); fail if it reads no image there.
print_image_info() {
  local file="$1"
  qemu-img info --output=json "${file}"
}

# copy_image <from> <to>: Copy an image file whole or not at all and print the
# copy's size in bytes. Image files are never changed in place, so a plain
# copy is a consistent one.
copy_image() {
  local from="$1" to="$2"
  mkdir -p "$(dirname "${to}")"
  cp --sparse=always "${from}" "${to}.part"
  mv "${to}.part" "${to}"
  stat -c %s "${to}"
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
  # qemu, and with cache=unsafe the qcow2 metadata and flushed data still in
  # qemu's memory would be lost. Hetzner keeps what a guest flushed.
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
  # USB, no 9p, no balloon, no QXL), no VAPIC option ROM, and QEMU's seccomp
  # sandbox: they cost nothing and keep the VM lean; they are no defence, the
  # lab does not guard against its guests. The virtio-scsi controller is there
  # from the boot: a disk on a controller hot-plugged just before is missed.
  # q35 hot-plugs only into PCIe root ports: four of them (hp1..hp4) for
  # networks attached later.
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
  wait_device_gone "${vm_dir}" "dev-${tap}"
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

# attach_volume <vm dir> <id> <file>: Hot-plug a volume. If QEMU does not add
# the disk, its drive goes again, so that another attach can add both.
attach_volume() {
  local vm_dir="$1" id="$2" file="$3"
  send_hmp "${vm_dir}" "drive_add 0 $(print_volume_drive "${id}" "${file}")"
  send_hmp "${vm_dir}" "device_add $(print_volume_device "${id}")" \
    || {
      send_hmp "${vm_dir}" "drive_del vol${id}" 2>/dev/null || true
      return 1
    }
}

# detach_volume <vm dir> <id>: Unplug a volume.
detach_volume() {
  local vm_dir="$1" id="$2"
  send_hmp "${vm_dir}" "device_del dvol${id}"
  wait_device_gone "${vm_dir}" "dvol${id}"
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

# main <verb> <arg>...: Run one verb. A verb is its function's name with dashes
# (create-volume runs create_volume); the Storage Box's verbs are box.sh's.
main() {
  local verb="${1:-}"
  shift || true
  case "${verb}" in
    init-net) init_net ;;
    set-up-public-tap) set_up_public_tap "$@" ;;
    set-up-private-tap) set_up_private_tap "$@" ;;
    tear-down-tap) tear_down_tap "$@" ;;
    link-exists) link_exists "$@" ;;
    list-neigh-devs) list_neigh_devs "$@" ;;
    list-local-addrs) list_local_addrs ;;
    create-volume) create_volume "$@" ;;
    attach-volume) attach_volume "$@" ;;
    detach-volume) detach_volume "$@" ;;
    resize-volume) resize_volume "$@" ;;
    fetch-image) fetch_image "$@" ;;
    create-vm-disk) create_vm_disk "$@" ;;
    create-vm-disk-from-image) create_vm_disk_from_image "$@" ;;
    create-image) create_image "$@" ;;
    print-image-info) print_image_info "$@" ;;
    copy-image) copy_image "$@" ;;
    resize-vm-disk) resize_vm_disk "$@" ;;
    start-vm) start_vm "$@" ;;
    kill-vm) kill_vm "$@" ;;
    is-vm-alive) is_vm_alive "$@" ;;
    send-qmp) send_qmp "$@" ;;
    print-vm-counters) print_vm_counters "$@" ;;
    send-hmp) send_hmp "$@" ;;
    add-nic) add_nic "$@" ;;
    delete-nic) delete_nic "$@" ;;
    set-route) set_route "$@" ;;
    set-route6) set_route6 "$@" ;;
    set-route-via) set_route_via "$@" ;;
    delete-route) delete_route "$@" ;;
    flush-routes) flush_routes "$@" ;;
    # nft and tc take hlabd's rules as they are (fabric/faults.py, firewall.py)
    nft) nft "$@" ;;
    tc) tc "$@" ;;
    add-namespace) add_namespace "$@" ;;
    delete-namespace) delete_namespace "$@" ;;
    add-namespace-nic) add_namespace_nic "$@" ;;
    delete-namespace-nic) delete_namespace_nic "$@" ;;
    stop-dhcp) stop_dhcp "$@" ;;
    restart-dhcp) restart_dhcp "$@" ;;
    start-dns) start_dns "$@" ;;
    start-nsd) start_nsd "$@" ;;
    start-pebble) start_pebble "$@" ;;
    start-socks) start_socks "$@" ;;
    start-balancer) start_balancer "$@" ;;
    is-balancer-alive) is_balancer_alive "$@" ;;
    stop-balancer) stop_balancer "$@" ;;
    start-box | stop-box | set-account | set-access | set-home | \
      set-zfs-view | remove-account | add-key | write-authorized-keys | \
      take-snapshot | roll-back | remove-tree | print-bytes-used | list-folders)
      exec "$(dirname "$0")/box.sh" "${verb}" "$@"
      ;;
    *)
      echo "hlab.sh: unknown command: ${verb}" >&2
      exit 2
      ;;
  esac
}

main "$@"
