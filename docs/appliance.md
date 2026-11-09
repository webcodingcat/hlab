# The appliance and its agent

The data plane lives in the appliance, a small Debian 13 VM. This file
explains the pictures hlabd sends it, the agent that applies them and how
the appliance is built.

## Pictures and the agent

The data plane lives in the appliance, a small Debian 13 VM. hlabd never sends it a
change, only whole pictures; the agent inside makes the appliance match each one.

**Pictures** (`internal/picture`) are pure functions of a `picture.Lab`: plain
values the lab takes from its state and settings (the host link, the MAC set, the
servers with their Primary IPs). Each picture carries the Lab's generation; a
change of the servers or their addresses sends every picture anew
(`Machine.UpdatePictures`, one at a time, so that an older one never follows a
newer one). Their output is compared with golden files in
`internal/picture/testdata`; `go test ./internal/picture -update` writes them anew,
and the diff shows what changed.

| Picture | What | The agent |
|---|---|---|
| `links` | the four NICs by MAC (`link`, `public`, `private`, `uplink`), VRFs, VLANs, addresses, routes, sysctls | netlink: adds what is missing, removes what the picture lacks |
| `ruleset` | the whole nftables ruleset as text | `nft -f`: all or nothing |
| `dhcp` | a dnsmasq configuration per device it serves: `public` and each private network's VLAN | writes each, checks them all with `dnsmasq --test`, restarts `hlab-dnsmasq@<device>` for each that changed and stops those of devices the picture lacks |
| `metadata` | the metadata service's answers per server, with the server's public addresses and its private ones with their VRFs | serves them on `169.254.169.254:80` |

**The address plan** as the pictures use it: the host link `198.18.0.0/29` and
`2001:db8:0:ffff::/64` (host `.1`/`::1`, appliance `.2`/`::2`, `.3` the source for
ACME checks; the appliance's link-local address there is `fe80::2`: the kernel
solicits a neighbor only from a link-local address, unless the waiting packet
comes from the appliance itself); the servers see Hetzner's gateways `172.31.1.1` (on-link to their
`/32`) and `fe80::1`, and the resolvers `198.18.0.2` and `2001:db8:0:ffff::2`.
A server with IPv6 only gets a service address of Hetzner's
`100.64.0.0/10` by DHCP (docs/servers.md), which only the appliance's
`public` NIC routes, as a `/32` with the source `172.31.1.1`: never the
host, so a host's Tailscale, whose addresses are of the same range, does
not meet it, and the uplink rejects the range anyway. The
appliance's MAC addresses are `02:48:4c:0a:00:0<n>` (set A) or `02:48:4c:0b:00:0<n>`
(set B), n = NIC 1 to 4; an update starts the new appliance with the other set.
A Floating IP's route (`/32` or `/64`) goes through its server's Primary IP
of the same family (IPv6: `::1` of the server's `/64`), where the guest
answers ARP and neighbor solicitations already: a move changes only the
route, and no neighbor cache holds the old server. The guest configures
the Floating IP itself, as on Hetzner. A server without a Primary IP of
that family gets the route on-link, and its guest must answer for the
Floating IP.

**The ruleset's fixed part**, before any firewall (`ruleset.nft.tmpl`):

- Servers reach the host (`198.18.0.1`, `2001:db8:0:ffff::1`) only on TCP 8080
  and 8081; the lab control on 8090 stays closed to them.
- The uplink reaches the Internet only: private and link-local ranges, the
  Default Switch's and the LAN's among them, are rejected, so no server reaches the
  Windows host or its LAN. So is the lab's own range `198.18.0.0/15`, which the
  `links` picture's blackhole route already keeps from it. IPv6 to the
  uplink is rejected (the Default Switch has none), and so is IPv4 from servers
  without Internet, from their Floating IPs too.
- No rule depends on conntrack seeing a connection start. Between host and servers
  and among servers nothing is tracked; the answers to the host's connections are
  `established`, or TCP with ACK and without SYN: conntrack takes up a running
  TCP connection after an update of the appliance, but calls its first packet
  `new`, and a server that sends first would hang. Such a packet opens no
  connection.
- Connections to the Internet do not live through an update or a restart
  of the appliance: their NAT state is in the old one, and an update's
  appliance gets another uplink address (docs/roadmap.md, Known gaps).
- The servers reach DHCP, the metadata service and DNS on the appliance; the host
  reaches all of it.
- A service address (`100.64.0.0/10`, a server with IPv6 only) reaches
  DHCP and the metadata service on the appliance and nothing else: no
  DNS, nothing through the appliance (the Internet, Hetzner's DHCP server
  `188.40.24.200` that its lease routes to, the host, other servers), and
  nothing reaches it. Cloud Firewalls play no part, as on Hetzner, where
  they leave DHCP and the metadata service alone. What the address
  reaches on Hetzner beyond its routes is not measured; dropping the rest
  is the lab's assumption.

**The firewalls** (`internal/picture/firewalls.go`, golden files
`firewall-*.golden`) come after the fixed part, in two base chains of
their own on the forward hook: `firewall_out` (priority filter + 10)
filters what a server sends to another server or the uplink, then
`firewall_in` (filter + 20) what reaches a server. An `accept` in one
leaves the packet to the next, so a packet between two servers passes the
sender's `out` and the receiver's `in` rules. Verdict maps send a server's
public addresses (Primary IPs and Floating IPs) to its chains
`server_<id>_in` and `server_<id>_out`; those let answers pass
(`established`, or TCP with ACK and without SYN, as in the fixed part),
jump to the chains `firewall_<id>_in` and `firewall_<id>_out` of each of
its firewalls, which hold the rules, and drop the rest. A server without
an outbound rule has no `out` chain: everything goes out, as on Hetzner.
What the fixed part allows of its own accord is never filtered: the lab's
endpoints in the chain `input`, and the host link's way to the host's APIs
(docs/servers.md, Firewalls).

**An address nobody holds** answers nothing, as on Hetzner: the `links`
picture ends the lab's ranges (`198.18.0.0/15`, `2001:db8::/32`) in a
blackhole route of the main table. The routes of the servers, their
Floating IPs and the host link are more specific, the appliance's own
addresses are local.

**Private networks** (`internal/picture/networks.go`): each network is a
VLAN `private.<vlan>` on the `private` NIC, the trunk, in a VRF of its own
`vrf-net-<network-id>` (table 10000 + VLAN). The VLAN holds the
network's first address, the gateway, as a `/32`. The VRF's table routes
each address and alias IP a server holds (`/32`, on-link), the network's
routes through their gateways (on-link, as Hetzner takes a gateway no
server holds yet) and ends in a blackhole, so that nothing leaves the VRF
for the main table and an address nobody holds answers nothing. Two
networks with the same range stay apart in their VRFs; a packet that
comes in on a VRF's VLAN shows the VRF as its interface in nftables, so
the ruleset accepts DHCP from `vrf-net-*` and forwards `vrf-net-*` to
`private.*` without any firewall. Each network has a dnsmasq instance of
its own, bound to its VLAN (one instance's leases would mix two networks
with the same addresses): a server's address as a `/32` for a day, option
121 with the gateway on-link and the range through it, MTU 1450, and
neither a router nor name servers, as on Hetzner. A server without
public addresses (the dnsmasq tag `private-only`) also gets the route
`169.254.169.254/32` through the gateway: the VLAN holds that address
too, and the ruleset lets TCP 80 to it in from `vrf-net-*`.

**Redirects stay off** on every interface (sysctl in the image and per interface in
the `links` picture): a router that sends ICMP redirects teaches servers to reach
each other past the firewalls.

**The agent** (`internal/agent`, Linux only):

- It answers hlabd on vsock port 5000 (`agentproto.Port`): `GET /v1/status` (version
  and the generation of each picture applied) and `PUT /v1/pictures/<name>`. It
  applies one picture at a time. 204 means applied; 400 a body that is not the
  picture (also unknown fields, which come from a newer hlabd); 422 that it refused
  the picture and nothing changed (its own `Check`, nft or dnsmasq refused it); 500
  that applying failed part way, and hlabd sends the picture again.
- `links` is not atomic, but every step makes a state the next picture can start
  from. Its order: rename the NICs (a NIC must be down for that), VRFs, VLANs,
  sysctls, links up (`lo` among them), addresses, routes: the on-link ones
  first, then those through a gateway (a Floating IP's, through its server's
  Primary IP), which the kernel takes only once it reaches the gateway. Its
  routes carry the protocol number 72, so the agent removes only its own. The `uplink` is networkd's: the agent only names
  it.
- **Announcing.** Whenever a device's carrier comes up, and after each `links`
  picture, the agent sends each of the device's announced addresses twice: a
  gratuitous ARP for IPv4, an unsolicited neighbor advertisement (override) for
  IPv6. After an update the new appliance has new MAC addresses; without this the
  neighbors keep the old ones for seconds.
- **The metadata service** tells servers apart by the request's source address.
  It listens with `IP_FREEBIND`, because `169.254.169.254` on `lo` comes with the
  first `links` picture. With `net.ipv4.tcp_l3mdev_accept` (the `links`
  picture) its listener in the main VRF takes connections from the
  networks' VRFs too; the kernel binds each to its VRF, which the service
  reads (`SO_BINDTOIFINDEX`) to tell apart two servers with the same
  address in two networks.
- It decides nothing: what the appliance does comes from the pictures. After a
  restart it has none and reports none; hlabd sends all of them again.
- **Formats** are its one command besides the pictures
  (`agentproto.PathFormat`): a file system is made once, so there is no
  state to picture. hlabd attaches a new volume's disk to the appliance
  (slot 63, one at a time) and names it by the WWN its disk ID gives
  (`vhdx.WWN`); the agent finds the disk whose `device/wwid` in sysfs
  matches, makes the file system without discarding (`mkfs.ext4 -E
  nodiscard`, `mkfs.xfs -K`), and hlabd detaches the disk again.

## The appliance

`bin/dev appliance` builds `build/appliance/hlab-appliance.vhdx` with the agent of
the working tree (`appliance/build.sh`). It needs no root:

1. `mmdebstrap --mode=unshare` builds Debian 13 (trixie with its updates and
   security updates) as a tarball. It installs only Debian packages, puts in
   `appliance/files` and the agent, and runs `appliance/customize.sh`.
2. `mkfs.ext4 -d` fills the root file system from the tarball; `mkfs.vfat` and
   mtools build the EFI partition with Debian's shim and grub, signed for the
   "Microsoft UEFI Certificate Authority", which the VMs' Secure Boot template
   trusts. grub boots `/vmlinuz` of the root file system.
3. `sfdisk` puts both into a 4 GB disk with a GPT, and `qemu-img` writes it as a
   dynamic VHDX with 1 MB blocks.

What is in it:

- **Packages:** the cloud kernel (Hyper-V's drivers, signed), systemd, nftables,
  dnsmasq (only `dnsmasq-base`: the agent runs it), knot, unbound with its trust
  anchor (`dns-root-data`), haproxy, pebble, e2fsprogs and xfsprogs (the
  volumes' formats), openssh-server, rsync, borgbackup, btrfs-progs,
  qemu-utils. `--variant=apt` installs no Recommends: a package
  that the appliance needs from them is named.
- **Running:** `hlab-agent` before `sysinit.target`, after the modules (`hv_sock`,
  `vrf`, `8021q`) and the boot ruleset; it must not wait for the network, which it
  sets up itself. networkd only for the uplink (DHCP, matched by both MAC sets,
  `ManageForeignRoutes=no`). unbound as the lab's resolver on `198.18.0.2` and
  `2001:db8:0:ffff::2`. `hlab-dnsmasq` once the agent has its configuration.
- **The uplink's own routing table** (`uplink`, 200): networkd puts the DHCP
  default route there, not into the main table. Rules after the main table
  send only the servers' traffic (from `public`) and the appliance's own (from
  `lo`) to it, and `198.18.0.0/15` is unreachable in it: a lab address that
  nobody holds, which the main table's blackhole takes first, could not
  leave through the uplink either, and nothing else reaches it.
- **Off until their phase:** knot, haproxy, sshd (its host keys are removed;
  each appliance makes its own in phase 5), and `systemd-networkd-wait-online`.
  pebble has no unit of its own. `serial-getty@ttyS0` is masked: the VM has no
  COM port until the console (6a).
- **The clock:** Hyper-V gives a VM the host's local time as its RTC, which
  Linux reads as UTC, so the appliance starts off by the time zone. Hyper-V's
  time sync (`hv_utils`) corrects it within a second of the boot; the journal
  shows the jump.
- **Boot ruleset** (`/etc/nftables.conf`): forwards nothing until the first
  `ruleset` picture.
- Nobody logs in: root is locked, hlabd reaches the agent over hv_sock.
