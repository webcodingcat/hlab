"""Networks: taps, DHCP, DNS, routes of cloud networks, Floating IPs, Internet clients.

A part of the fabric (see __init__.py). It asks the VMs (vm.py) whether a server runs
and has the faults (faults.py) re-applied when taps or clients come and go.
"""

import ipaddress
import threading
import time
from pathlib import Path

from .. import config
from ..records import Client, Nic, Server
from ..state import State, nic_addresses, nics_in, primary_address_of
from .addresses import link_local, network_gateway, prefix, public_mac, route_table
from .faults import Faults
from .shell import Shell
from .vm import VMs


class Network:
    """The lab's network: taps, DHCP, DNS, routes, Floating IPs, Internet clients."""

    def __init__(
        self,
        cfg: dict,
        state: State,
        root: Path,
        shell: Shell,
        net_lock: threading.RLock,
        vms: VMs,
        faults: Faults,
    ):
        """Store what the network uses; `net_lock` is the fabric's lock (see Fabric)."""
        self.cfg = cfg
        self.state = state
        self.root = root
        self.shell = shell
        self.net_lock = net_lock
        self.vms = vms
        self.faults = faults
        # the container's own addresses and when they were read (local_addresses)
        self._local_addresses_cache: tuple[float, set[str]] | None = None

    def taps_up(self, server: Server) -> list[str]:
        """Bring up the server's public tap and the taps of its cloud networks.

        Their routes and DHCP are set up with them. Returns the NICs for QEMU (tap=mac).
        """
        server_lab = server["_lab"]
        tap, index = server_lab["tap"], server_lab["index"]
        ipv4 = primary_address_of(self.state, server, "ipv4")
        self.shell.run("set-up-public-tap", tap, *([ipv4] if ipv4 else []))
        nics = [f"{tap}={public_mac(index)}"]
        with self.state.lock:  # a copy: bringing a tap up runs a script
            server_nics = list(server_lab["nics"])
        for nic in server_nics:
            self.nic_up(nic)
            # at boot each network NIC gets its own root port (bp<n>); hp1..hp4 are free
            with self.state.lock:
                nic.pop("port", None)
            nics.append(f"{nic['tap']}={nic['mac']}")
        ipv6_net = primary_address_of(self.state, server, "ipv6")
        # the /64 is routed to the guest's link-local address, as Hetzner routes it
        if ipv6_net:
            self.shell.run("set-route6", ipv6_net, tap, link_local(index))
        self.write_dhcp()
        return nics

    def taps_down(self, server: Server) -> None:
        """Take the server's taps down, and their DHCP with them."""
        server_lab = server["_lab"]
        with self.state.lock:  # a copy: taking a tap down runs a script
            taps = [server_lab["tap"], *(nic["tap"] for nic in server_lab["nics"])]
        # taps change only under the lock, so tc and nft find the taps they checked
        with self.net_lock:
            for tap in taps:
                self.shell.run("tear-down-tap", tap)
        self.write_dhcp()  # the taps' dnsmasq instances are stopped too

    def nic_up(self, nic: Nic) -> None:
        """Bring up a NIC's tap in its cloud network.

        It gets the gateway address, routes to the server's IP and alias IPs, and the
        rules of the network's routing table.
        """
        with self.state.lock:  # copies that fit together: the script runs unlocked
            ip_range = ipaddress.ip_network(
                self.state["networks"][nic["network"]]["ip_range"]
            )
            alias_ips = list(nic["alias_ips"])
        # two servers of one network starting side by side would both add its ip rules
        with self.net_lock:
            self.shell.run(
                "set-up-private-tap",
                nic["tap"],
                f"{network_gateway(str(ip_range))}/{ip_range.prefixlen}",
                nic["ip"],
                route_table(nic["network"]),
                str(ip_range),
                *alias_ips,
            )

    def read_local_addresses(self, fresh: bool = False) -> set[str]:
        """Read the IPv4 addresses the lab container itself has, at most every 10 s.

        It runs a script, so call it before taking the state's lock; under the lock,
        `local_addresses()` gives what it read. `fresh` reads now.
        """
        now = time.monotonic()
        cache = self._local_addresses_cache
        if fresh or cache is None or now - cache[0] > 10:
            out = self.shell.run("list-local-addrs", check=False)
            cache = self._local_addresses_cache = (now, {a for a in out.split() if a})
        return cache[1]

    def local_addresses(self) -> set[str]:
        """Return the container's own addresses as last read; never runs a script.

        These are never handed to a server.
        """
        cache = self._local_addresses_cache
        return cache[1] if cache else set()

    def refresh_cloud_network(self, nid: int) -> None:
        """Set a network up again after its range changed.

        Its routing table and the gateway address on its members' taps are set up again.
        """
        with self.net_lock:
            self.shell.run("flush-routes", route_table(nid))
            for server, nic in nics_in(self.state, nid):
                if self.vms.is_alive(server):
                    self.nic_up(nic)

    def attach_nic(self, server: Server, nic: Nic) -> None:
        """Attach a NIC to a server, hot-plugged if its VM runs."""
        if self.vms.is_alive(server):
            self.nic_up(nic)
            with self.state.lock:
                used = {n.get("port") for n in server["_lab"]["nics"]}
                nic["port"] = next(
                    (p for p in ("hp1", "hp2", "hp3", "hp4") if p not in used), None
                )
            if nic["port"] is None:
                raise RuntimeError(
                    "no free hot-plug port (the lab hot-plugs 4 NICs per boot)"
                )
            self.write_dhcp()  # the tap exists now: its dnsmasq binds to it
            self.shell.run(
                "add-nic",
                str(self.vms.directory(server)),
                nic["tap"],
                nic["mac"],
                nic["port"],
            )
        else:
            self.write_dhcp()
        self.faults.apply()
        self.apply_routes()  # a gateway server attached now gets the network's routes
        self.faults.apply_latency()

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
        self.faults.apply()

    def remove_client(self, client: Client) -> None:
        """Remove an Internet client and its namespace."""
        with self.net_lock:
            self.shell.run("delete-client", client["name"], client["veth"])
        self.faults.apply()

    def drop_cloud_network(self, nid: int) -> None:
        """Clean up after a deleted network.

        Its routing table is flushed with its routes; DHCP and the fault sets are
        updated.
        """
        with self.net_lock:
            self.shell.run("flush-routes", route_table(nid), check=False)
        self.write_dhcp()
        self.faults.apply()
        self.apply_routes()

    def dhcp_configs(self) -> dict[str, str]:
        """Return one dnsmasq configuration per tap, with DHCP like Hetzner's.

        Each NIC gets a fixed address, a /32 with routes – on public interfaces to
        172.31.1.1, on cloud networks to the network's gateway and range. The caller
        holds the state's lock.
        """
        configs: dict[str, str] = {}
        for server in self.state["servers"].values():
            server_lab = server["_lab"]
            ipv4 = primary_address_of(self.state, server, "ipv4")
            if ipv4:
                # primary IPv4 as /32, on-link gateway 172.31.1.1, lab resolver
                configs[server_lab["tap"]] = self.dnsmasq_config(
                    server_lab["tap"],
                    public_mac(server_lab["index"]),
                    ipv4,
                    [
                        f"dhcp-range={config.PUBLIC_NET}.0,"
                        "static,255.255.255.0,infinite",
                        "dhcp-option=option:netmask,255.255.255.255",
                        f"dhcp-option=option:router,{config.GATEWAY}",
                        f"dhcp-option=121,{config.GATEWAY}/32,"
                        f"0.0.0.0,0.0.0.0/0,{config.GATEWAY}",
                        f"dhcp-option=option:dns-server,{config.SERVICES}",
                    ],
                )
            for nic in server_lab["nics"]:
                network = self.state["networks"].get(nic["network"])
                if not network:  # its network was deleted meanwhile
                    continue
                ip_range = ipaddress.ip_network(network["ip_range"])
                gateway = network_gateway(network["ip_range"])
                # the network's routes go to its gateway too; the lab forwards them to
                # the route's server
                routes = "".join(
                    f",{r['destination']},{gateway}" for r in network["routes"]
                )
                configs[nic["tap"]] = self.dnsmasq_config(
                    nic["tap"],
                    nic["mac"],
                    nic["ip"],
                    [
                        f"dhcp-range={ip_range.network_address},"
                        f"static,{ip_range.netmask},infinite",
                        "dhcp-option=option:netmask,255.255.255.255",
                        "dhcp-option=option:router",
                        f"dhcp-option=121,{gateway}/32,0.0.0.0,"
                        f"{ip_range},{gateway}{routes}",
                    ],
                )
        return configs

    def dnsmasq_config(self, tap: str, mac: str, ip: str, options: list[str]) -> str:
        """Return a dnsmasq configuration serving one fixed address on a tap."""
        return (
            "\n".join(
                [
                    "port=0",
                    "bind-dynamic",
                    f"interface={tap}",
                    "dhcp-authoritative",
                    "leasefile-ro",
                    f"dhcp-host={mac},{ip}",
                    *options,
                ]
            )
            + "\n"
        )

    def write_dhcp(self) -> None:
        """Run one dnsmasq per tap, only for taps that exist.

        dnsmasq binds to its interface at start. Only instances whose configuration
        changed are restarted. This runs under the lock: servers starting side by side
        must not restart dnsmasq concurrently.
        """
        directory = self.root / "dhcp"
        directory.mkdir(exist_ok=True)
        with self.net_lock:
            with self.state.lock:
                all_configs = self.dhcp_configs()
            configs = {
                tap: c
                for tap, c in all_configs.items()
                if self.shell.ok("link-exists", tap)
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

    def apply_routes(self) -> None:
        """Put the routes of cloud networks in place.

        A route's destination goes to the tap of the server that holds the gateway IP,
        in the network's routing table.
        """
        with self.net_lock:
            with self.state.lock:  # copies: the loops below run scripts
                old_routes = list(self.state.data["net_routes"])
                # (route, its table, the servers' NICs that hold its gateway IP)
                wanted = [
                    (
                        route,
                        route_table(net["id"]),
                        [
                            (server, nic)
                            for server, nic in nics_in(self.state, net["id"])
                            if route["gateway"] in nic_addresses(nic)
                        ],
                    )
                    for net in self.state["networks"].values()
                    for route in net["routes"]
                ]
            for dest, table in old_routes:
                self.shell.run("delete-route", dest, table, check=False)
            applied = []
            for route, table, holders in wanted:
                # the NIC of a running server that holds the route's gateway IP
                nic = next(
                    (nic for server, nic in holders if self.vms.is_alive(server)), None
                )
                if nic:
                    self.shell.run(
                        "set-route-via",
                        route["destination"],
                        route["gateway"],
                        nic["tap"],
                        table,
                        check=False,
                    )
                    applied.append([route["destination"], table])
            with self.state.lock:
                self.state.data["net_routes"] = applied

    def write_dns(self) -> None:
        """Start the lab resolver with its own names and the hlab.yaml records."""
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

        Assigned to a running server, the IP goes to its tap (an IPv4 as /32, an IPv6
        /64 to the guest's link-local address); assigned to a server that is off, or to
        none, it goes nowhere – the old server does not keep it.
        """
        with self.net_lock:
            if server is None or not self.vms.is_alive(server):
                self.shell.run("set-route", prefix(ip))
            elif ":" in ip:
                tap, index = server["_lab"]["tap"], server["_lab"]["index"]
                self.shell.run("set-route6", prefix(ip), tap, link_local(index))
            else:
                self.shell.run("set-route", prefix(ip), server["_lab"]["tap"])

    def reroute_floating_ips(self, server: Server) -> None:
        """Route the Floating IPs assigned to a server to it again, after it started.

        Hetzner keeps routing an assigned IP across a power cycle; the route was removed
        with the old tap. The check and the route happen under one lock: a move that
        routes the IP elsewhere meanwhile comes before or after, never in between.
        """
        with self.net_lock:
            with self.state.lock:
                floating_ips = list(self.state["floating_ips"].values())
            for floating_ip in floating_ips:
                if floating_ip.get("server") == server["id"]:
                    self.route_floating_ip(floating_ip["ip"], server)
