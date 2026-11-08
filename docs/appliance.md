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
(`Lab.UpdatePictures`, one at a time, so that an older one never follows a
newer one). Their output is compared with golden files in
`internal/picture/testdata`; `go test ./internal/picture -update` writes them anew,
and the diff shows what changed.

| Picture | What | The agent |
|---|---|---|
| `links` | the four NICs by MAC (`link`, `public`, `private`, `uplink`), VRFs, VLANs, addresses, routes, sysctls | netlink: adds what is missing, removes what the picture lacks |
| `ruleset` | the whole nftables ruleset as text | `nft -f`: all or nothing |
| `dhcp` | dnsmasq's whole configuration | writes it, checks it with `dnsmasq --test`, restarts `hlab-dnsmasq` if it changed |
| `metadata` | the metadata service's answers per server, with the server's addresses | serves them on `169.254.169.254:80` |

**The address plan** as the pictures use it: the host link `198.18.0.0/29` and
`2001:db8:0:ffff::/64` (host `.1`/`::1`, appliance `.2`/`::2`, `.3` the source for
ACME checks; the appliance's link-local address there is `fe80::2`: the kernel
solicits a neighbor only from a link-local address, unless the waiting packet
comes from the appliance itself); the servers see Hetzner's gateways `172.31.1.1` (on-link to their
`/32`) and `fe80::1`, and the resolvers `198.18.0.2` and `2001:db8:0:ffff::2`. The
appliance's MAC addresses are `02:48:4c:0a:00:0<n>` (set A) or `02:48:4c:0b:00:0<n>`
(set B), n = NIC 1 to 4; an update starts the new appliance with the other set.

**The ruleset's fixed part**, before any firewall (`ruleset.nft.tmpl`):

- Servers reach the host (`198.18.0.1`, `2001:db8:0:ffff::1`) only on TCP 8080
  and 8081; the lab control on 8090 stays closed to them.
- The uplink reaches the Internet only: private and link-local ranges, the
  Default Switch's and the LAN's among them, are rejected, so no server reaches the
  Windows host or its LAN. So is the lab's own range `198.18.0.0/15`, which the
  uplink's routing table already makes unreachable (The appliance). IPv6 to the
  uplink is rejected (the Default Switch has none), and so is IPv4 from servers
  without Internet.
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
  sysctls, links up (`lo` among them), addresses, routes. Its routes carry the protocol number 72, so
  the agent removes only its own. The `uplink` is networkd's: the agent only names
  it.
- **Announcing.** Whenever a device's carrier comes up, and after each `links`
  picture, the agent sends each of the device's announced addresses twice: a
  gratuitous ARP for IPv4, an unsolicited neighbor advertisement (override) for
  IPv6. After an update the new appliance has new MAC addresses; without this the
  neighbors keep the old ones for seconds.
- **The metadata service** tells servers apart by the request's source address.
  It listens with `IP_FREEBIND`, because `169.254.169.254` on `lo` comes with the
  first `links` picture.
- It decides nothing: what the appliance does comes from the pictures. After a
  restart it has none and reports none; hlabd sends all of them again.

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
  anchor (`dns-root-data`), haproxy, pebble, openssh-server, rsync, borgbackup,
  btrfs-progs, qemu-utils. `--variant=apt` installs no Recommends: a package
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
  nobody holds never leaves through the uplink, and nothing else reaches it.
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
