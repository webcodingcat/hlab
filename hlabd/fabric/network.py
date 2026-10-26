"""Networks: taps, DHCP, DNS, routes of cloud networks, Floating IPs, firewall table.

Part of Fabric (see __init__.py).
"""

import ipaddress
import time

from .. import config
from ..records import Client, Nic, Server
from ..state import nic_addresses, nics_in, primary_address_of
from .base import FabricBase
from .firewall import ruleset


def public_mac(index: int) -> str:
    """Return the MAC of a server's public NIC."""
    return f"96:00:00:00:00:{index:02x}"


def link_local(index: int) -> str:
    """Return the IPv6 link-local address a guest derives from its public MAC.

    The address comes from EUI-64; it is the only one the guest's tap lets in.
    """
    return f"fe80::9400:ff:fe00:{index:x}"


def private_mac(index: int, number: int) -> str:
    """Return the MAC of a server's NIC in a cloud network."""
    return f"86:00:00:00:{index:02x}:{number:02x}"


def network_gateway(ip_range: str) -> str:
    """Return the gateway of a cloud network: the first host of its range."""
    return str(next(ipaddress.ip_network(ip_range).hosts()))


def route_table(network_id: int) -> str:
    """Return the routing table of a cloud network: its ID.

    Network IDs start above two million (state.py), clear of the kernel's tables.
    """
    return str(network_id)


def prefix(ip: str) -> str:
    """Return an address as routed prefix.

    An IPv4 address gets /32, an IPv6 address /128; networks stay as they are.
    """
    return ip if "/" in ip else f"{ip}/32" if ":" not in ip else f"{ip}/128"


class Network(FabricBase):
    """Fabric mixin for taps, DHCP, DNS, routes, Floating IPs and the firewall table."""

    def nic_up(self, nic: Nic) -> None:
        """Bring up a NIC's tap in its cloud network.

        It gets the gateway address, routes to the server's IP and alias IPs, and the
        rules of the network's routing table.
        """
        net = self.state["networks"][nic["network"]]
        ip_range = ipaddress.ip_network(net["ip_range"])
        # two servers of one network starting side by side would both add its ip rules
        with self.net_lock:
            self.sh.run(
                "private-tap-up",
                nic["tap"],
                f"{network_gateway(net['ip_range'])}/{ip_range.prefixlen}",
                nic["ip"],
                route_table(nic["network"]),
                str(ip_range),
                *nic["alias_ips"],
            )

    def local_addresses(self, fresh: bool = False) -> set[str]:
        """Return the IPv4 addresses the lab container itself has.

        These are never handed to a server. They are read at most every 10 s (fresh:
        now).
        """
        now = time.monotonic()
        cache = self._local_addresses_cache
        if fresh or cache is None or now - cache[0] > 10:
            out = self.sh.run("local-addrs", check=False)
            cache = self._local_addresses_cache = (now, {a for a in out.split() if a})
        return cache[1]

    def refresh_network(self, nid: int) -> None:
        """Set a network up anew after its range changed.

        Its table and the gateway address on its members' taps are set anew.
        """
        with self.net_lock:
            self.sh.run("route-flush", route_table(nid))
            for server, nic in nics_in(self.state, nid):
                if self.vm_alive(server):
                    self.nic_up(nic)

    def attach_nic(self, server: Server, nic: Nic) -> None:
        """Attach a NIC to a server, hot-plugged if its VM runs."""
        if self.vm_alive(server):
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
            self.sh.run(
                "nic-add", str(self.vm_dir(server)), nic["tap"], nic["mac"], nic["port"]
            )
        else:
            self.write_dhcp()
        self.apply_faults()
        self.apply_routes()  # a gateway server attached now gets the network's routes
        self.apply_latency()

    def detach_nic(self, server: Server, nic: Nic) -> None:
        """Detach a NIC from a server and take its tap down."""
        # taps change only under the lock, so tc and nft find the taps they checked
        with self.net_lock:
            if self.vm_alive(server):
                self.sh.run(
                    "nic-del", str(self.vm_dir(server)), nic["tap"], check=False
                )
            self.sh.run("tap-down", nic["tap"])

    def neighbour_taps(self, ip: str) -> set[str]:
        """Return the taps on which the lab sees a neighbour with this address."""
        return set(self.sh.run("neigh-dev", ip, check=False).split())

    def add_client(self, client: Client) -> None:
        """Add an Internet client: its own network namespace behind a veth.

        The client gets its addresses there.
        """
        with self.net_lock:
            self.sh.run(
                "client-add",
                client["name"],
                client["veth"],
                client["ip"],
                client["ip6"],
            )
        self.apply_faults()

    def remove_client(self, client: Client) -> None:
        """Remove an Internet client and its namespace."""
        with self.net_lock:
            self.sh.run("client-del", client["name"], client["veth"])
        self.apply_faults()

    def drop_network(self, nid: int) -> None:
        """Clean up after a deleted network.

        Its routing table is flushed, and with it its routes; DHCP and fault sets
        follow.
        """
        with self.net_lock:
            self.sh.run("route-flush", route_table(nid), check=False)
        self.write_dhcp()
        self.apply_faults()
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
                if self.sh.ok("link-exists", tap)
            }
            for path in directory.glob("*.conf"):
                if path.stem not in configs:
                    self.sh.run("dhcp-stop", str(path))
                    path.unlink()
            for tap, dnsmasq_conf in configs.items():
                path = directory / f"{tap}.conf"
                if not path.is_file() or path.read_text() != dnsmasq_conf:
                    path.write_text(dnsmasq_conf)
                    self.sh.run("dhcp-restart", str(path))

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
                self.sh.run("route-del", dest, table, check=False)
            applied = []
            for route, table, holders in wanted:
                # the NIC of a running server that holds the route's gateway IP
                nic = next(
                    (nic for server, nic in holders if self.vm_alive(server)), None
                )
                if nic:
                    self.sh.run(
                        "route-via",
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
            self.sh.run("dns-start", str(self.root / "dns.conf"))

    def route_floating_ip(self, ip: str, server: Server | None) -> None:
        """Route a Floating IP to the server it is assigned to, or nowhere.

        Assigned to a running server, the IP goes to its tap (an IPv4 as /32, an IPv6
        /64 to the guest's link-local address); assigned to a server that is off, or to
        none, it goes nowhere – the old server does not keep it.
        """
        with self.net_lock:
            if server is None or not self.vm_alive(server):
                self.sh.run("route", prefix(ip))
            elif ":" in ip:
                tap, index = server["_lab"]["tap"], server["_lab"]["index"]
                self.sh.run("route6", prefix(ip), tap, link_local(index))
            else:
                self.sh.run("route", prefix(ip), server["_lab"]["tap"])

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

    def apply_firewalls(self) -> None:
        """Replace the firewall table (inet hlabfw) atomically.

        The new table holds the rules of all firewalls.
        """
        with self.net_lock:
            path = self.root / "firewall.nft"
            with self.state.lock:
                taps = {
                    s["id"]: s["_lab"]["tap"] for s in self.state["servers"].values()
                }
                text = ruleset(self.state, taps)
            path.write_text(text)
            self.sh.run("nft", "-f", str(path))
