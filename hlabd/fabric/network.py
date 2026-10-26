"""Networks: taps, DHCP, DNS, routes of cloud networks, Floating IPs, Internet clients.

A part of the fabric (see __init__.py). It asks the VMs (vm.py) whether a server runs.
The API computes from the state what each tap, DHCP server and route should look like
(api/desired.py). This part only puts that in place. A server is named by its ID
(`sid`), which never changes.
"""

import ipaddress
import threading
import time
from dataclasses import dataclass
from pathlib import Path

from .. import config
from .addresses import link_local, network_gateway, prefix, public_mac, route_table
from .shell import Shell
from .vm import VMs


@dataclass(frozen=True)
class PublicTap:
    """A server's public tap, with what routes to the server need."""

    sid: int  # the server's ID: whether its VM runs
    tap: str
    index: int  # the server's lab index: its MAC and IPv6 link-local address


@dataclass(frozen=True)
class ClientLink:
    """An Internet client's network namespace behind a veth."""

    name: str  # also the name of its namespace
    veth: str
    ip: str
    ip6: str


@dataclass
class PrivateTap:
    """A NIC's tap in a cloud network, as it is set up."""

    tap: str
    mac: str
    ip: str  # the NIC's private IP
    network: int  # the network's ID: its routing table
    ip_range: str  # the network's range: the gateway address and its routes
    alias_ips: list[str]  # routed to the tap as well


@dataclass
class ServerTaps:
    """What a server's taps are set up with when its VM starts."""

    public: PublicTap
    ipv4: str | None  # its primary IPv4, routed to its public tap
    ipv6_net: str | None  # its primary IPv6 /64, routed to its link-local address
    nics: list[PrivateTap]  # its NICs in cloud networks, in their order


@dataclass
class DhcpHost:
    """The one fixed address a tap's dnsmasq hands out, with its DHCP options."""

    tap: str
    mac: str
    ip: str
    options: list[str]  # dnsmasq lines: range, netmask, routes, resolver


@dataclass
class WantedRoute:
    """A route of a cloud network and the NICs that hold its gateway IP."""

    destination: str
    gateway: str
    network: int  # the network's ID: its routing table
    # (server ID, its tap in the network) for every NIC that holds the gateway IP;
    # the route goes to the first whose VM runs
    holders: list[tuple[int, str]]


def dnsmasq_config(host: DhcpHost) -> str:
    """Return a dnsmasq configuration serving one fixed address on a tap."""
    return (
        "\n".join(
            [
                "port=0",
                "bind-dynamic",
                f"interface={host.tap}",
                "dhcp-authoritative",
                "leasefile-ro",
                f"dhcp-host={host.mac},{host.ip}",
                *host.options,
            ]
        )
        + "\n"
    )


class Network:
    """The lab's network: taps, DHCP, DNS, routes, Floating IPs, Internet clients."""

    def __init__(
        self,
        cfg: dict,
        root: Path,
        shell: Shell,
        net_lock: threading.RLock,
        vms: VMs,
    ):
        """Store what the network uses; `net_lock` is the fabric's lock (see Fabric)."""
        self.cfg = cfg
        self.root = root
        self.shell = shell
        self.net_lock = net_lock
        self.vms = vms
        # the container's own addresses and when they were read (read_local_addresses)
        self._local_addresses_cache: tuple[float, set[str]] | None = None

    def taps_up(self, taps: ServerTaps) -> list[str]:
        """Bring up the server's taps with their routes; return the NICs for QEMU.

        These are the public tap and the taps of its cloud networks, as tap=mac.
        """
        tap, index = taps.public.tap, taps.public.index
        self.shell.run("set-up-public-tap", tap, *([taps.ipv4] if taps.ipv4 else []))
        nics = [f"{tap}={public_mac(index)}"]
        for nic in taps.nics:
            self.nic_up(nic)
            nics.append(f"{nic.tap}={nic.mac}")
        # the /64 is routed to the guest's link-local address, as Hetzner routes it
        if taps.ipv6_net:
            self.shell.run("set-route6", taps.ipv6_net, tap, link_local(index))
        return nics

    def taps_down(self, taps: list[str]) -> None:
        """Take a server's taps down (their dnsmasq instances stop with them)."""
        # taps are removed only under the lock: tc and nft find the taps they checked
        with self.net_lock:
            for tap in taps:
                self.shell.run("tear-down-tap", tap)

    def nic_up(self, nic: PrivateTap) -> None:
        """Bring up a NIC's tap in its cloud network.

        It gets the gateway address, routes to the server's IP and alias IPs, and the
        rules of the network's routing table.
        """
        ip_range = ipaddress.ip_network(nic.ip_range)
        # two servers of one network that start side by side would both add its rules
        with self.net_lock:
            self.shell.run(
                "set-up-private-tap",
                nic.tap,
                f"{network_gateway(str(ip_range))}/{ip_range.prefixlen}",
                nic.ip,
                route_table(nic.network),
                str(ip_range),
                *nic.alias_ips,
            )

    def read_local_addresses(self, fresh: bool = False) -> set[str]:
        """Return the IPv4 addresses the lab container itself has; no server gets them.

        It runs a script, so the API calls it in a thread. The addresses rarely
        change, so requests within 10 s share one reading; with `fresh`, it reads now.
        """
        now = time.monotonic()
        cache = self._local_addresses_cache
        if fresh or cache is None or now - cache[0] > 10:
            out = self.shell.run("list-local-addrs", check=False)
            cache = self._local_addresses_cache = (now, {a for a in out.split() if a})
        return set(cache[1])

    def refresh_cloud_network(
        self, nid: int, members: list[tuple[int, PrivateTap]]
    ) -> None:
        """Set a network up again after its range changed.

        This sets up its routing table again, and the gateway address on the taps of
        its members whose VM runs.
        """
        with self.net_lock:
            self.shell.run("flush-routes", route_table(nid))
            for sid, nic in members:
                if self.vms.is_alive(sid):
                    self.nic_up(nic)

    def hot_plug_nic(self, sid: int, nic: PrivateTap, port: str) -> None:
        """Hot-plug a NIC whose tap is up into the running VM, on a free root port."""
        self.shell.run("add-nic", str(self.vms.directory(sid)), nic.tap, nic.mac, port)

    def detach_nic(self, sid: int, tap: str) -> None:
        """Detach a NIC (its tap) from a server and take its tap down."""
        # taps are removed only under the lock: tc and nft find the taps they checked
        with self.net_lock:
            if self.vms.is_alive(sid):
                self.shell.run(
                    "delete-nic",
                    str(self.vms.directory(sid)),
                    tap,
                    check=False,
                )
            self.shell.run("tear-down-tap", tap)

    def neighbour_taps(self, ip: str) -> set[str]:
        """Return the taps on which the lab sees a neighbour with this address."""
        return set(self.shell.run("list-neigh-devs", ip, check=False).split())

    def add_client(self, client: ClientLink) -> None:
        """Add an Internet client: its own network namespace behind a veth.

        The client gets its addresses there.
        """
        with self.net_lock:
            self.shell.run(
                "add-client", client.name, client.veth, client.ip, client.ip6
            )

    def remove_client(self, client: ClientLink) -> None:
        """Remove an Internet client and its namespace."""
        with self.net_lock:
            self.shell.run("delete-client", client.name, client.veth)

    def drop_cloud_network(self, nid: int) -> None:
        """Flush the routing table of a deleted network, with its routes."""
        with self.net_lock:
            self.shell.run("flush-routes", route_table(nid), check=False)

    def write_dhcp(self, hosts: list[DhcpHost]) -> None:
        """Run one dnsmasq per tap, only for taps that exist.

        dnsmasq binds to its interface at start. Only instances whose configuration
        changed are restarted. This runs under the lock: servers starting side by side
        must not restart dnsmasq concurrently.
        """
        directory = self.root / "dhcp"
        directory.mkdir(exist_ok=True)
        with self.net_lock:
            configs = {
                host.tap: dnsmasq_config(host)
                for host in hosts
                if self.shell.ok("link-exists", host.tap)
            }
            for path in directory.glob("*.conf"):
                if path.stem not in configs:
                    self.shell.run("stop-dhcp", str(path))
                    path.unlink()
            for tap, dnsmasq_conf in configs.items():
                path = directory / f"{tap}.conf"
                if not path.is_file() or path.read_text() != dnsmasq_conf:
                    path.write_text(dnsmasq_conf)
                    self.shell.run("restart-dhcp", str(path))

    def apply_routes(
        self, old_routes: list[list[str]], wanted: list[WantedRoute]
    ) -> list[list[str]]:
        """Replace the routes of cloud networks; return them as [destination, table].

        `old_routes` are what this method returned last time; they are removed. Each
        wanted route goes, in its network's routing table, to the tap of the first
        running server that holds the gateway IP.
        """
        with self.net_lock:
            for dest, table in old_routes:
                self.shell.run("delete-route", dest, table, check=False)
            applied = []
            for route in wanted:
                table = route_table(route.network)
                tap = next(
                    (tap for sid, tap in route.holders if self.vms.is_alive(sid)),
                    None,
                )
                if tap:
                    self.shell.run(
                        "set-route-via",
                        route.destination,
                        route.gateway,
                        tap,
                        table,
                        check=False,
                    )
                    applied.append([route.destination, table])
            return applied

    def write_dns(self) -> None:
        """Start the DNS resolver with the lab's names and the hlab.yaml records."""
        lines = [
            f"address=/{n}/{a}"
            for a in (config.SERVICES, config.SERVICES6)
            for n in ("api.hlab", "box.hlab", "your-storagebox.de")
        ]
        lines += [
            f"address=/hetzner-api.hlab/{a}"
            for a in (config.HETZNER_API, config.HETZNER_API6)
        ]
        lines += [
            f"address=/{name}/{ip}" for name, ip in (self.cfg.get("dns") or {}).items()
        ]
        with self.net_lock:
            (self.root / "dns.conf").write_text("\n".join(lines) + "\n")
            self.shell.run("start-dns", str(self.root / "dns.conf"))

    def route_floating_ip(self, ip: str, server: PublicTap | None) -> None:
        """Route a Floating IP to the server it is assigned to, or nowhere.

        If the server runs, the IP goes to its tap: an IPv4 address as /32, an IPv6 /64
        to the guest's link-local address. If the server is off, or there is no
        server, the IP goes nowhere; the old server does not keep it.
        """
        with self.net_lock:
            if server is None or not self.vms.is_alive(server.sid):
                self.shell.run("set-route", prefix(ip))
            elif ":" in ip:
                self.shell.run(
                    "set-route6", prefix(ip), server.tap, link_local(server.index)
                )
            else:
                self.shell.run("set-route", prefix(ip), server.tap)
