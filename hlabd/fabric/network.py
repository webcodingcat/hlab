"""Networks: taps, DHCP, DNS, routes of cloud networks, Floating IPs, Internet clients.

A part of the fabric (see __init__.py). It asks the VMs (vm.py) whether a server runs.
The API computes from the state what each tap, DHCP server and route should look like
(api/desired.py). This part only puts that in place.
"""

import ipaddress
import threading
import time
from dataclasses import dataclass
from pathlib import Path

from .. import config
from ..records import Client, Nic, Server
from .addresses import link_local, network_gateway, prefix, public_mac, route_table
from .shell import Shell
from .vm import VMs


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
    # (server, its tap in the network) for every NIC that holds the gateway IP; the
    # route goes to the first whose VM runs
    holders: list[tuple[Server, str]]


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
        # the container's own addresses and when they were read (local_addresses)
        self._local_addresses_cache: tuple[float, set[str]] | None = None

    def taps_up(self, server: Server, taps: ServerTaps) -> list[str]:
        """Bring up the server's public tap and the taps of its cloud networks.

        It also sets up their routes. Return the NICs for QEMU (tap=mac).
        """
        server_lab = server["_lab"]
        tap, index = server_lab["tap"], server_lab["index"]
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
        # taps change only under the lock, so tc and nft find the taps they checked
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
        """Read the IPv4 addresses the lab container itself has, at most every 10 s.

        It runs a script, so the API calls it in a thread. Afterwards
        `local_addresses()` returns what it read without running a script. With
        `fresh`, it reads now.
        """
        now = time.monotonic()
        cache = self._local_addresses_cache
        if fresh or cache is None or now - cache[0] > 10:
            out = self.shell.run("list-local-addrs", check=False)
            cache = self._local_addresses_cache = (now, {a for a in out.split() if a})
        return cache[1]

    def local_addresses(self) -> set[str]:
        """Return the container's addresses as last read, without running a script.

        These are never handed to a server.
        """
        cache = self._local_addresses_cache
        return cache[1] if cache else set()

    def refresh_cloud_network(
        self, nid: int, members: list[tuple[Server, PrivateTap]]
    ) -> None:
        """Set a network up again after its range changed.

        This sets up its routing table again, and the gateway address on the taps of
        its members whose VM runs.
        """
        with self.net_lock:
            self.shell.run("flush-routes", route_table(nid))
            for server, nic in members:
                if self.vms.is_alive(server):
                    self.nic_up(nic)

    def hot_plug_nic(self, server: Server, nic: PrivateTap, port: str) -> None:
        """Hot-plug a NIC whose tap is up into the running VM, on a free root port."""
        self.shell.run(
            "add-nic", str(self.vms.directory(server)), nic.tap, nic.mac, port
        )

    def detach_nic(self, server: Server, nic: Nic) -> None:
        """Detach a NIC from a server and take its tap down."""
        # taps change only under the lock, so tc and nft find the taps they checked
        with self.net_lock:
            if self.vms.is_alive(server):
                self.shell.run(
                    "delete-nic",
                    str(self.vms.directory(server)),
                    nic["tap"],
                    check=False,
                )
            self.shell.run("tear-down-tap", nic["tap"])

    def neighbour_taps(self, ip: str) -> set[str]:
        """Return the taps on which the lab sees a neighbour with this address."""
        return set(self.shell.run("list-neigh-devs", ip, check=False).split())

    def add_client(self, client: Client) -> None:
        """Add an Internet client: its own network namespace behind a veth.

        The client gets its addresses there.
        """
        with self.net_lock:
            self.shell.run(
                "add-client",
                client["name"],
                client["veth"],
                client["ip"],
                client["ip6"],
            )

    def remove_client(self, client: Client) -> None:
        """Remove an Internet client and its namespace."""
        with self.net_lock:
            self.shell.run("delete-client", client["name"], client["veth"])

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
        """Put the routes of cloud networks in place; return [destination, table] each.

        First it removes the routes added last time (`old_routes`, as this method
        returned them). Each route goes, in the network's routing table, to the tap of
        the first running server that holds the gateway IP.
        """
        with self.net_lock:
            for dest, table in old_routes:
                self.shell.run("delete-route", dest, table, check=False)
            applied = []
            for route in wanted:
                table = route_table(route.network)
                tap = next(
                    (tap for server, tap in route.holders if self.vms.is_alive(server)),
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

    def route_floating_ip(self, ip: str, server: Server | None) -> None:
        """Route a Floating IP to the server it is assigned to, or nowhere.

        If the server runs, the IP goes to its tap: an IPv4 address as /32, an IPv6 /64
        to the guest's link-local address. If the server is off, or there is no
        server, the IP goes nowhere; the old server does not keep it.
        """
        with self.net_lock:
            if server is None or not self.vms.is_alive(server):
                self.shell.run("set-route", prefix(ip))
            elif ":" in ip:
                tap, index = server["_lab"]["tap"], server["_lab"]["index"]
                self.shell.run("set-route6", prefix(ip), tap, link_local(index))
            else:
                self.shell.run("set-route", prefix(ip), server["_lab"]["tap"])
