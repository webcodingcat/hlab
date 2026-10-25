#!/usr/bin/env bash
# Low-level lab primitives (taps, QEMU, QMP, nftables, Storage Box). Called by hlabd, which keeps the state and
# allocates names and addresses; nothing here decides anything.
#
# Like Hetzner's networks the segments are routed, not bridged: every VM NIC is its own tap, the container answers
# ARP for everything (proxy ARP) and routes between the taps, so a network cut is a drop rule in the forward chain.
# Every cloud network routes in its own table (the network's ID), selected by the incoming private tap: networks
# may overlap like on Hetzner, the same private IP in two networks are two different servers.
set -euo pipefail

gw=172.31.1.1
svc=198.18.0.1
meta=169.254.169.254
operator=192.0.2.2      # source of what the container itself sends to VMs (consumers in its namespace too)
admin=198.18.0.2        # source of `hlab ssh`, exempt from Cloud firewalls
svc6=2001:db8:fffe::1
operator6=2001:db8:fffe::2
admin6=2001:db8:fffe::3
hetzner_api=198.18.0.3   # api.hetzner.com stand-in (Storage Boxes)
hetzner_api6=2001:db8:fffe::4
meta6=2001:db8:fffe::a9fe   # where fe80::a9fe:a9fe goes (DNAT): the metadata service only

# stop_pid <pid file> <comm prefix> [signal]: kill the process only if it is still the one the file names – the
# files outlive the container (/hlab is a volume), and in a new container their PIDs belong to others.
stop_pid() {
    local f="$1" want="$2" sig="${3:-TERM}" p
    [[ -f "$f" ]] || return 0
    p="$(cat "$f")"
    if [[ "$(cat "/proc/${p}/comm" 2>/dev/null)" == "${want}"* ]]; then kill -s "${sig}" "${p}" 2>/dev/null || true; fi
    rm -f "$f"
}

alive() { # alive <pid file> <comm prefix>
    [[ -f "$1" ]] && [[ "$(cat "/proc/$(cat "$1")/comm" 2>/dev/null)" == "$2"* ]]
}

qmp() { # qmp <vm dir> <json command>: fails when QEMU does not answer or answers with an error (also an HMP
    # command whose text output is an error: device_add, netdev_add, ...)
    local out
    out="$(printf '{"execute":"qmp_capabilities"}\n%s\n' "$2" | socat -t1 - "UNIX-CONNECT:$1/qmp.sock")" \
        || { echo "qmp: no answer from $1" >&2; return 1; }
    if printf '%s\n' "${out}" | jq -e -s 'map(select(.error)) | length > 0' >/dev/null 2>&1; then
        printf '%s\n' "${out}" | jq -r 'select(.error) | .error.desc' >&2
        return 1
    fi
    if printf '%s\n' "${out}" | jq -e -s 'map(select((.return | type) == "string" and (.return | test("^Error|error:"; "i"))))
            | length > 0' >/dev/null 2>&1; then
        printf '%s\n' "${out}" | jq -r 'select((.return | type) == "string") | .return' >&2
        return 1
    fi
}

hmp() { # hmp <vm dir> <monitor command>
    qmp "$1" "$(jq -cn --arg c "$2" '{execute: "human-monitor-command", arguments: {"command-line": $c}}')"
}

net_init() {
    local uplink
    uplink="$(ip -4 route show default | awk '{print $5; exit}')"
    ip link add hlabgw type dummy 2>/dev/null || true
    ip addr replace "${gw}/32" dev hlabgw
    ip addr replace "${svc}/32" dev hlabgw
    ip addr replace "${meta}/32" dev hlabgw
    ip addr replace "${operator}/32" dev hlabgw
    ip addr replace "${admin}/32" dev hlabgw
    ip addr replace "${hetzner_api}/32" dev hlabgw
    # Docker starts containers with IPv6 off; taps created later inherit "default".
    sysctl -qw net.ipv6.conf.all.disable_ipv6=0 net.ipv6.conf.default.disable_ipv6=0
    ip link set hlabgw up
    sysctl -qw net.ipv6.conf.hlabgw.disable_ipv6=0
    for a in "${svc6}" "${operator6}" "${admin6}" "${hetzner_api6}" "${meta6}"; do
        ip -6 addr replace "${a}/128" dev hlabgw nodad
    done
    sysctl -qw net.ipv4.ip_forward=1 net.ipv6.conf.all.forwarding=1
    # the lab's public ranges end here when nothing is routed to an address (a Floating IP of a server that is
    # off, an address no one has): never out of the uplink into the Docker host's network
    ip route replace unreachable 198.51.100.0/24
    ip route replace unreachable 203.0.113.0/24
    ip -6 route replace unreachable 2001:db8::/32
    ip route replace unreachable 192.0.2.0/24     # Internet clients (each has its own route)
    ip route replace unreachable 198.18.0.0/15    # the lab's services (local addresses win)
    nft -f - <<NFT
table inet hlab
delete table inet hlab
table inet hlab {
    set cuts { type ifname . ifname; }          # traffic between two interfaces dropped
    set isolated { type ifname; }               # interface cut off from everything
    set blocked_svc { type ifname . inet_service; }   # interface may not reach a lab service port
    set blocked_inet { type ifname; }           # interface may not reach the real Internet
    set private_pairs { type ifname . ifname; } # private taps of the same cloud network
    set src4 { type ifname . ipv4_addr; }       # the IPv4 sources a public tap or client may send from
    set src6 { type ifname . ipv6_addr; flags interval; }   # and the IPv6 ones (prefixes)
    map tap_tables { type ifname : mark; }      # private tap → its network's routing table
    chain antispoof {
        # Like Hetzner: a server sends from its own addresses (primary IPs, assigned Floating IPs) only, a client
        # from its own. Cloud networks are not checked (a gateway server forwards for others there).
        type filter hook prerouting priority -150;
        iifname "pub*" ip saddr 0.0.0.0 udp dport 67 accept
        iifname "pub*" ip6 saddr :: icmpv6 type { nd-neighbor-solicit, mld2-listener-report } accept   # DAD, MLD
        iifname "pub*" meta nfproto ipv4 iifname . ip saddr != @src4 drop
        iifname "pub*" meta nfproto ipv6 iifname . ip6 saddr != @src6 drop
        iifname "cl*" ip6 saddr { ::, fe80::/10 } accept
        iifname "cl*" meta nfproto ipv4 iifname . ip saddr != @src4 drop
        iifname "cl*" meta nfproto ipv6 iifname . ip6 saddr != @src6 drop
        iifname "prv*" meta nfproto ipv6 drop       # cloud networks carry IPv4 only
        iifname "prv*" ct mark set iifname map @tap_tables   # the network's routing table, for the replies
    }
    chain route_out {
        # what the lab itself sends on a connection from a cloud network goes back through that network's table
        type route hook output priority mangle;
        ct mark != 0 meta mark set ct mark
    }
    chain input {
        type filter hook input priority 0;
        iifname "${uplink}" ct state established,related accept
        iifname "${uplink}" drop                    # neighbour containers and the Docker host get nothing of the lab
        iifname @isolated drop
        ip daddr { ${svc}, ${hetzner_api} } iifname . tcp dport @blocked_svc drop
        ip6 daddr { ${svc6}, ${hetzner_api6} } iifname . tcp dport @blocked_svc drop
    }
    chain output {
        type filter hook output priority 0;
        oifname @isolated drop
        # Cloud networks are not reachable from outside; only DHCP (and ICMP errors) come from the gateway.
        oifname "prv*" udp sport 67 accept
        oifname "prv*" meta l4proto icmp accept
        oifname "prv*" ip saddr ${meta} tcp sport 80 accept   # the metadata service, for private-only servers
        oifname "prv*" drop
    }
    chain forward {
        type filter hook forward priority 0;
        iifname "${uplink}" ct state established,related accept
        iifname "${uplink}" drop                    # no packets (with any source) from outside to the guests
        oifname "${uplink}" meta nfproto ipv6 drop  # the lab's IPv6 is its own: none of it leaves
        iifname @isolated drop
        oifname @isolated drop
        iifname . oifname @cuts drop
        iifname @blocked_inet oifname "${uplink}" drop
        oifname "${uplink}" ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 100.64.0.0/10 } drop
        # A cloud network only carries traffic between its own members.
        oifname "prv*" iifname . oifname != @private_pairs drop
        iifname "prv*" oifname != "prv*" drop       # and nothing leaves them (Internet only through a gateway server)
    }
    chain prerouting {
        type nat hook prerouting priority dstnat;
        # the metadata service over IPv6 (link-local, answered on every public tap)
        ip6 daddr fe80::a9fe:a9fe tcp dport 80 dnat ip6 to [${meta6}]:80
    }
    chain postrouting {
        type nat hook postrouting priority srcnat;
        # public addresses only: private ones reach the Internet through a gateway server, as on Hetzner
        oifname "${uplink}" ip saddr { 198.51.100.0/24, 203.0.113.0/24, 192.0.2.0/24 } masquerade
    }
}
NFT
}

public_tap_up() { # public_tap_up <tap> [guest ip]: a server's public NIC, with DHCP and IPv6 metadata like Hetzner's
    tap_up "$1" "${gw}/32" "${2:-}"
    ip link set "$1" mtu 1500   # Hetzner's public network: 1500 (cloud networks: 1450)
    # an address in the public range, so dnsmasq serves DHCP here (the /32 routes still decide)
    ip addr replace 198.51.100.1/24 dev "$1" noprefixroute
    ip -6 addr replace fe80::a9fe:a9fe/64 dev "$1" nodad
}

tap_up() { # tap_up <tap> <gateway cidr> [guest ip] [noprefixroute]
    local tap="$1" addr="$2" guest="${3:-}"
    ip tuntap add "${tap}" mode tap 2>/dev/null || true
    sysctl -qw "net.ipv6.conf.${tap}.disable_ipv6=0"
    ip link set "${tap}" mtu 1450 up
    # noprefixroute: several taps carry the same gateway address; only the /32 routes below decide where packets go.
    ip addr replace "${addr}" dev "${tap}" ${4:+noprefixroute}
    ip -6 addr replace fe80::1/64 dev "${tap}" nodad
    if [[ -n "${guest}" ]]; then ip route replace "${guest}/32" dev "${tap}" src "${operator}"; fi
    sysctl -qw "net.ipv4.conf.${tap}.proxy_arp=1"
}

vm_start() { # vm_start <dir> <name> <ram mb> <cpus> <serial> <tap=mac>...
    local dir="$1" name="$2" ram="$3" cpus="$4" serial="$5" nics=() disks=() i=0 spec
    shift 5
    for spec in "$@"; do
        nics+=(-netdev "tap,id=${spec%%=*},ifname=${spec%%=*},script=no,downscript=no")
        if [[ "${i}" -eq 0 ]]; then   # the public NIC stays for the server's life: on the root bus
            nics+=(-device "virtio-net-pci,netdev=${spec%%=*},id=dev-${spec%%=*},mac=${spec#*=}")
        else   # a network's NIC can be detached later: q35 unplugs only from a root port, so it gets its own
            nics+=(-device "pcie-root-port,id=bp${i},chassis=$((10 + i)),slot=$((10 + i))"
                   -device "virtio-net-pci,netdev=${spec%%=*},id=dev-${spec%%=*},mac=${spec#*=},bus=bp${i}")
        fi
        i=$((i + 1))
    done
    # cache=writeback: the guest's flushes reach the host – a power-off kills qemu, and with cache=unsafe qcow2
    # metadata and flushed data still in qemu's memory were lost. Hetzner keeps what a guest flushed.
    # The virtio-scsi controller is there from the boot: a disk on a controller hot-plugged just before is missed.
    # q35 hot-plugs only into PCIe root ports: four of them (hp1..hp4) for networks attached later.
    disks=(-drive "file=${dir}/disk.qcow2,if=none,id=root,cache=writeback" -device "virtio-blk-pci,drive=root,bootindex=1")
    # Attached volumes (id path per line) sit on the SCSI controller from the boot.
    if [[ -f "${dir}/volumes" ]]; then
        while read -r vid vpath; do
            [[ -n "${vid}" ]] || continue
            disks+=(-drive "file=${vpath},format=raw,if=none,id=vol${vid},cache=none"
                    -device "scsi-hd,drive=vol${vid},id=dvol${vid},bus=scsi0.0,vendor=HC,product=Volume,serial=${vid}")
        done < "${dir}/volumes"
    fi
    # SMBIOS like Hetzner's: cloud-init recognises the platform by the manufacturer and checks the serial. No device
    # the guests do not need (no USB, no 9p, no balloon, no QXL) and no VAPIC option ROM: less to attack from a guest.
    qemu-system-x86_64 -machine q35,usb=off -enable-kvm -cpu host -smp "${cpus}" -m "${ram}" -name "${name}" \
        -global kvm-apic.vapic=off -sandbox on,obsolete=deny,resourcecontrol=deny \
        -smbios "type=1,manufacturer=Hetzner,product=vServer,serial=${serial}" \
        -device virtio-scsi-pci,id=scsi0 \
        "${disks[@]}" \
        -device pcie-root-port,id=hp1,chassis=1,slot=1 -device pcie-root-port,id=hp2,chassis=2,slot=2 \
        -device pcie-root-port,id=hp3,chassis=3,slot=3 -device pcie-root-port,id=hp4,chassis=4,slot=4 \
        "${nics[@]}" \
        -device virtio-serial-pci -chardev "socket,path=${dir}/qga.sock,server=on,wait=off,id=qga0" \
        -device virtserialport,chardev=qga0,name=org.qemu.guest_agent.0 \
        -qmp "unix:${dir}/qmp.sock,server,nowait" -chardev "file,id=ser0,path=${dir}/console.log,append=on" -serial chardev:ser0 \
        -display none -daemonize -pidfile "${dir}/qemu.pid"
}

cmd="${1:-}"
shift || true
case "${cmd}" in
    net-init) net_init ;;
    public-tap-up) public_tap_up "$@" ;;
    neigh-dev) ip neigh show "$1" | awk '{for (i = 1; i < NF; i++) if ($i == "dev") print $(i + 1)}' | sort -u ;;  # every tap it sits on
    volume-create) # volume-create <file> <gb> [ext4|xfs]: a sparse raw volume, formatted on request
        truncate -s "$2G" "$1"
        case "${3:-}" in ext4) mkfs.ext4 -q -F "$1" ;; xfs) mkfs.xfs -q -f "$1" ;; esac ;;
    volume-attach) # volume-attach <vm dir> <id> <file>: hot-plug, identifying like Hetzner (scsi-0HC_Volume_<id>)
        hmp "$1" "drive_add 0 file=$3,format=raw,if=none,id=vol$2,cache=none"
        hmp "$1" "device_add scsi-hd,drive=vol$2,id=dvol$2,bus=scsi0.0,vendor=HC,product=Volume,serial=$2" ;;
    volume-detach) # volume-detach <vm dir> <id>
        hmp "$1" "device_del dvol$2"; sleep 1; hmp "$1" "drive_del vol$2" 2>/dev/null || true ;;
    volume-resize) # volume-resize <file> <gb> [vm dir] [id]: grow; a running VM sees the new size at once
        if [[ -n "${3:-}" ]]; then hmp "$3" "block_resize vol$4 $2G"; else truncate -s "$2G" "$1"; fi ;;
    image-fetch) # image-fetch <name> <url> <checksum file url> <dir>: the image in the version the checksum file
        # names, as <dir>/<name>-<checksum>.qcow2 – downloaded and checked unless it is there; prints its path
        sums="$(curl -fsSL --connect-timeout 30 --max-time 120 "$3")"
        want="$(printf '%s\n' "${sums}" | awk -v f="$(basename "$2")" '{n = $2; sub(/^\*/, "", n)} n == f {print $1}')"
        [[ -n "${want}" ]] || { echo "no checksum for $(basename "$2") in $3" >&2; exit 1; }
        dst="$4/$1-${want:0:12}.qcow2"
        if [[ ! -f "${dst}" ]]; then
            mkdir -p "$4"
            curl -fsSL --connect-timeout 30 --speed-limit 1024 --speed-time 60 -o "${dst}.tmp" "$2"
            sum=sha256sum; [[ ${#want} -eq 128 ]] && sum=sha512sum
            echo "${want}  ${dst}.tmp" | "${sum}" -c --quiet - >&2 && mv "${dst}.tmp" "${dst}"
        fi
        echo "${dst}" ;;
    hmp) hmp "$1" "$2" ;;
    private-tap-up) # private-tap-up <tap> <gateway cidr> <guest ip> <table> <range> [alias ip]...: a NIC in a
        # cloud network; the guest IP and its alias IPs are routed to the tap
        ip -4 addr flush dev "$1" 2>/dev/null || true   # the gateway address of an earlier range goes
        tap_up "$1" "$2" "" noprefixroute
        sysctl -qw "net.ipv6.conf.$1.disable_ipv6=1"     # cloud networks are IPv4 only, as on Hetzner
        keep=" $3 ${*:6} "
        for r in $(ip route show table "$4" dev "$1" 2>/dev/null | awk '!/ via / {print $1}'); do
            [[ "${keep}" == *" ${r%/32} "* ]] || ip route del "${r}" dev "$1" table "$4" 2>/dev/null || true
        done   # alias IPs given up go; routes through a gateway server (via) stay
        ip route replace "$3/32" dev "$1" src "${operator}" table "$4"
        for a in "${@:6}"; do ip route replace "${a}/32" dev "$1" src "${operator}" table "$4"; done
        ip route replace unreachable "$5" table "$4"   # members only; nothing of the range leaks to the main table
        ip route add unreachable default table "$4" 2>/dev/null || true   # unless a default route is there
        ip rule show pref 1002 | grep -q "fwmark $(printf '0x%x' "$4") lookup $4\$" \
            || ip rule add fwmark "$4" lookup "$4" pref 1002
        # replies of the lab end there too when the table has no route (its gateway server is off)
        ip rule show pref 1003 | grep -q "fwmark $(printf '0x%x' "$4") unreachable" \
            || ip rule add fwmark "$4" unreachable pref 1003
        # Nothing else either: what the network's table has no route for (no add_route, or its gateway server is
        # off) ends at the second rule instead of falling through to the main table. Rules are only replaced when
        # they differ, so a running tap never loses them for a moment.
        if ! ip rule show pref 1000 | grep -q "iif $1 lookup $4\$" || ! ip rule show pref 1001 | grep -q "iif $1 unreachable"; then
            while ip rule del iif "$1" 2>/dev/null; do :; done
            ip rule add iif "$1" lookup "$4" pref 1000
            ip rule add iif "$1" unreachable pref 1001
        fi ;;
    tap-down) ip link del "$1" 2>/dev/null || true; while ip rule del iif "$1" 2>/dev/null; do :; done ;;
    link-exists) ip link show "$1" >/dev/null 2>&1 ;;
    local-addrs) # the container's own IPv4 addresses and its default gateway (the Docker host's)
        ip -4 -o addr show | awk '{print $4}' | cut -d/ -f1
        ip -4 route show default | awk '{print $3}' ;;
    vm-disk) # vm-disk <dir> <base> <size gb>: the server's disk, an overlay on a system image
        [[ -f "$1/disk.qcow2" ]] && exit 0
        qemu-img create -q -f qcow2 -F qcow2 -b "$2" "$1/disk.qcow2" "$3G" ;;
    vm-disk-resize) qemu-img resize -q "$1/disk.qcow2" "$2G" ;;   # vm-disk-resize <dir> <size gb>: grows only
    vm-start) vm_start "$@" ;;
    vm-kill) stop_pid "$1/qemu.pid" qemu-system KILL ;;
    vm-alive) alive "$1/qemu.pid" qemu-system ;;
    qmp) qmp "$1" "$2" ;;
    nic-add) # nic-add <dir> <id> <tap> <mac> <root port>
        hmp "$1" "netdev_add tap,id=$2,ifname=$3,script=no,downscript=no"
        hmp "$1" "device_add virtio-net-pci,netdev=$2,id=dev-$2,mac=$4,bus=$5" ;;
    nic-del) hmp "$1" "device_del dev-$2"; sleep 1; hmp "$1" "netdev_del $2" ;;
    route) # route <prefix> [tap]: route an address or prefix (v4 or v6) to a tap, or remove the route
        src="${operator}"; [[ "$1" == *:* ]] && src="${operator6}"
        if [[ -n "${2:-}" ]]; then ip route replace "$1" dev "$2" src "${src}"; else ip route del "$1" 2>/dev/null || true; fi ;;
    route6) # route6 <prefix> <tap> <guest link-local>: an IPv6 prefix routed to a guest, as Hetzner routes /64s
        ip -6 route replace "$1" via "$3" dev "$2" src "${operator6}" ;;
    route-via) ip route replace "$1" via "$2" dev "$3" onlink table "$4" ;;  # route-via <prefix> <gateway ip> <tap> <table>
    route-del) # route-del <prefix> <table>: a network route goes; the table's end comes back (a default route had
        # taken its place)
        ip route del "$1" table "$2" 2>/dev/null || true
        ip route add unreachable default table "$2" 2>/dev/null || true ;;
    route-flush) # route-flush <table>: a network is gone
        ip route flush table "$1" 2>/dev/null || true
        while ip rule del fwmark "$1" 2>/dev/null; do :; done ;;
    nft) nft "$@" ;;
    tc) tc "$@" ;;
    client-add) # client-add <name> <veth> <ipv4> <ipv6>: a network namespace on the "Internet"
        ip netns add "$1" 2>/dev/null || true
        ip link add "$2" type veth peer name eth0 netns "$1" 2>/dev/null || true
        sysctl -qw "net.ipv6.conf.$2.disable_ipv6=0"
        ip netns exec "$1" sysctl -qw net.ipv6.conf.all.disable_ipv6=0 net.ipv6.conf.default.disable_ipv6=0             net.ipv6.conf.eth0.disable_ipv6=0
        ip link set "$2" up
        ip addr replace "192.0.2.1/32" dev "$2" noprefixroute
        ip -6 addr replace fe80::1/64 dev "$2" nodad
        ip route replace "$3/32" dev "$2" src "${operator}"
        ip route replace "$4/128" dev "$2" src "${operator6}"
        sysctl -qw "net.ipv4.conf.$2.proxy_arp=1"
        ip -n "$1" link set lo up
        ip -n "$1" link set eth0 up
        ip -n "$1" addr replace "$3/32" dev eth0
        ip -n "$1" -6 addr replace "$4/128" dev eth0 nodad
        ip -n "$1" route replace 192.0.2.1/32 dev eth0 scope link
        ip -n "$1" route replace default via 192.0.2.1 dev eth0 onlink
        ip -n "$1" -6 route replace default via fe80::1 dev eth0
        mkdir -p "/etc/netns/$1"
        echo "nameserver ${svc}" > "/etc/netns/$1/resolv.conf" ;;
    client-del) ip link del "$2" 2>/dev/null || true; ip netns del "$1" 2>/dev/null || true; rm -rf "/etc/netns/$1" ;;
    dhcp-restart|dhcp-stop) # dhcp-restart|dhcp-stop <conf>: the DHCP dnsmasq of one tap (hlabd writes the file)
        # One instance per tap: with a single interface dnsmasq binds its socket to it, so the same IP can be handed
        # out in two networks. Every earlier instance must be gone: a leftover one answers NAK ("address not available").
        pkill -f "^dnsmasq --conf-file=$1\$" 2>/dev/null || true
        for _ in $(seq 1 50); do pgrep -f "^dnsmasq --conf-file=$1\$" >/dev/null || break; sleep 0.1; done
        if [[ "${cmd}" == dhcp-restart ]]; then dnsmasq --conf-file="$1"; fi ;;
    dns-start) # dns-start <hosts file>: the lab resolver on the services address
        stop_pid /run/dnsmasq-dns.pid dnsmasq
        upstream="$(awk '/^nameserver/ {print $2; exit}' /etc/resolv.conf)"
        dnsmasq --listen-address="${svc},${svc6}" --bind-interfaces --no-resolv --server="${upstream}" \
            --conf-file="$1" --pid-file=/run/dnsmasq-dns.pid ;;
    box-*) exec "$(dirname "$0")/box.sh" "${cmd#box-}" "$@" ;;   # the Storage Box: scripts/box.sh
    *) echo "unknown command: ${cmd}" >&2; exit 2 ;;
esac
