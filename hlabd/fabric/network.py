"""Networks: taps, DHCP, DNS, routes of cloud networks, Floating IPs and the firewall table. Part of Fabric (see
__init__.py)."""

import ipaddress
import time

from .. import config as C
from .firewall import ruleset


def public_mac(index: int) -> str:
    return f"96:00:00:00:00:{index:02x}"


def link_local(index: int) -> str:
    """The IPv6 link-local address a guest derives from its public MAC (EUI-64), the only one its tap lets in."""
    return f"fe80::9400:ff:fe00:{index:x}"


def private_mac(index: int, number: int) -> str:
    return f"86:00:00:00:{index:02x}:{number:02x}"


def network_gateway(ip_range: str) -> str:
    return str(next(ipaddress.ip_network(ip_range).hosts()))


def route_table(network_id: int) -> str:
    """The routing table of a cloud network: its ID (networks start at 2001, clear of the kernel's tables)."""
    return str(network_id)


def prefix(ip: str) -> str:
    """An address as routed prefix: IPv4 → /32, IPv6 networks stay as they are."""
    return ip if "/" in ip else f"{ip}/32" if ":" not in ip else f"{ip}/128"


class Network:
    def nic_up(self, nic: dict) -> None:
        """A NIC's tap in its cloud network: gateway address, routes to the server's IP and alias IPs, the rules of
        the network's routing table."""
        net = self.state["networks"][nic["network"]]
        ip_range = ipaddress.ip_network(net["ip_range"])
        with self.net_lock:   # two servers of one network starting side by side would both add its ip rules
            self.sh.run("private-tap-up", nic["tap"], f"{network_gateway(net['ip_range'])}/{ip_range.prefixlen}",
                        nic["ip"], route_table(nic["network"]), str(ip_range), *(nic.get("alias_ips") or []))

    def local_addresses(self, fresh: bool = False) -> set[str]:
        """The IPv4 addresses the lab container itself has (never handed to a server); read at most every 10 s
        (fresh: now)."""
        now = time.monotonic()
        if fresh or self._local is None or now - self._local[0] > 10:
            out = self.sh.run("local-addrs", check=False)
            self._local = (now, {a for a in out.split() if a})
        return self._local[1]

    def refresh_network(self, nid: int) -> None:
        """A network's range changed: its table and the gateway address on its members' taps are set anew."""
        with self.net_lock:
            self.sh.run("route-flush", route_table(nid))
            for s in list(self.state["servers"].values()):
                if self.vm_alive(s):
                    for nic in list(s["_lab"]["nics"]):
                        if nic["network"] == nid:
                            self.nic_up(nic)

    def attach_nic(self, server: dict, nic: dict) -> None:
        if self.vm_alive(server):
            self.nic_up(nic)
            used = {n.get("port") for n in server["_lab"]["nics"]}
            nic["port"] = next((p for p in ("hp1", "hp2", "hp3", "hp4") if p not in used), None)
            if nic["port"] is None:
                raise RuntimeError("no free hot-plug port (the lab hot-plugs 4 NICs per boot)")
            self.write_dhcp()  # the tap exists now: its dnsmasq binds to it
            self.sh.run("nic-add", str(self.vm_dir(server)), nic["tap"], nic["tap"], nic["mac"], nic["port"])
        else:
            self.write_dhcp()
        self.apply_faults()
        self.apply_routes()   # a gateway server attached now gets the network's routes
        self.apply_latency()

    def detach_nic(self, server: dict, nic: dict) -> None:
        with self.net_lock:   # taps come and go only under the lock: tc and nft changes find what they checked
            if self.vm_alive(server):
                self.sh.run("nic-del", str(self.vm_dir(server)), nic["tap"], check=False)
            self.sh.run("tap-down", nic["tap"])
            self.state.data.get("netem", {}).pop(nic["tap"], None)   # the next NIC gets the tap name again

    def neighbour_taps(self, ip: str) -> set[str]:
        """The taps on which the lab sees a neighbour with this address."""
        return set(self.sh.run("neigh-dev", ip, check=False).split())

    def add_client(self, client: dict) -> None:
        """An Internet client: its own network namespace behind a veth, with its addresses."""
        with self.net_lock:
            self.sh.run("client-add", client["name"], client["veth"], client["ip"], client["ip6"])
        self.apply_faults()

    def remove_client(self, client: dict) -> None:
        with self.net_lock:
            self.sh.run("client-del", client["name"], client["veth"])
        self.apply_faults()

    def drop_network(self, nid: int) -> None:
        """A deleted network: its routing table goes, and with it its routes; DHCP and fault sets follow."""
        with self.net_lock:
            self.sh.run("route-flush", route_table(nid), check=False)
        self.write_dhcp()
        self.apply_faults()
        self.apply_routes()

    def dhcp_configs(self) -> dict[str, str]:
        """DHCP like Hetzner's: a fixed address per NIC, a /32 with routes – on public interfaces to 172.31.1.1, on
        cloud networks to the network's gateway and range. One dnsmasq configuration per tap."""
        configs: dict[str, str] = {}
        for server in list(self.state["servers"].values()):
            lab = server["_lab"]
            if self.ipv4(server):
                # the primary IPv4 as /32, on-link gateway 172.31.1.1, the lab's resolver
                configs[lab["tap"]] = self.dnsmasq_config(lab["tap"], public_mac(lab["index"]), self.ipv4(server), [
                    f"dhcp-range={C.PUBLIC_NET}.0,static,255.255.255.0,infinite",
                    "dhcp-option=option:netmask,255.255.255.255",
                    f"dhcp-option=option:router,{C.GATEWAY}",
                    f"dhcp-option=121,{C.GATEWAY}/32,0.0.0.0,0.0.0.0/0,{C.GATEWAY}",
                    f"dhcp-option=option:dns-server,{C.SERVICES}"])
            for nic in list(lab["nics"]):
                network = self.state["networks"].get(nic["network"])
                if not network:   # its network went meanwhile
                    continue
                ip_range = ipaddress.ip_network(network["ip_range"])
                gateway = network_gateway(network["ip_range"])
                # the network's routes go to its gateway too; the lab forwards them to the route's server
                routes = "".join(f",{r['destination']},{gateway}" for r in network["routes"])
                configs[nic["tap"]] = self.dnsmasq_config(nic["tap"], nic["mac"], nic["ip"], [
                    f"dhcp-range={ip_range.network_address},static,{ip_range.netmask},infinite",
                    "dhcp-option=option:netmask,255.255.255.255",
                    "dhcp-option=option:router",
                    f"dhcp-option=121,{gateway}/32,0.0.0.0,{ip_range},{gateway}{routes}"])
        return configs

    def dnsmasq_config(self, tap: str, mac: str, ip: str, options: list[str]) -> str:
        return "\n".join(["port=0", "bind-dynamic", f"interface={tap}", "dhcp-authoritative", "leasefile-ro",
                          f"pid-file={self.root / 'dhcp' / (tap + '.pid')}", f"dhcp-host={mac},{ip}",
                          *options]) + "\n"

    def write_dhcp(self) -> None:
        """One dnsmasq per tap, only for taps that exist (dnsmasq binds to its interface at start); only instances
        whose configuration changed are restarted. Under the lock: servers starting side by side must not restart
        dnsmasq concurrently."""
        directory = self.root / "dhcp"
        directory.mkdir(exist_ok=True)
        with self.net_lock:
            configs = {tap: c for tap, c in self.dhcp_configs().items() if self.sh.ok("link-exists", tap)}
            for path in directory.glob("*.conf"):
                if path.stem not in configs:
                    self.sh.run("dhcp-stop", str(path))
                    path.unlink()
            for tap, config in configs.items():
                path = directory / f"{tap}.conf"
                if not path.is_file() or path.read_text() != config:
                    path.write_text(config)
                    self.sh.run("dhcp-restart", str(path))

    def apply_routes(self) -> None:
        """Routes of cloud networks: the destination goes to the tap of the server that holds the gateway IP, in
        the network's routing table."""
        with self.net_lock:
            for dest, table in (r for r in self.state.data.get("net_routes", []) if isinstance(r, list)):
                self.sh.run("route-del", dest, table, check=False)
            applied = []
            for net in list(self.state["networks"].values()):
                for r in net["routes"]:
                    nic = next((n for s in list(self.state["servers"].values()) for n in s["_lab"]["nics"]
                                if n["network"] == net["id"] and r["gateway"] in [n["ip"], *(n.get("alias_ips") or [])]
                                and self.vm_alive(s)), None)
                    if nic:
                        table = route_table(net["id"])
                        self.sh.run("route-via", r["destination"], r["gateway"], nic["tap"], table, check=False)
                        applied.append([r["destination"], table])
            self.state.data["net_routes"] = applied

    def write_dns(self) -> None:
        """The lab resolver: its own names and the hlab.yaml records."""
        lines = [f"address=/{n}/{a}" for a in (C.SERVICES, C.SERVICES6)
                 for n in ("api.hlab", "box.hlab", "your-storagebox.de")]
        lines += [f"address=/hetzner-api.hlab/{a}" for a in (C.HETZNER_API, C.HETZNER_API6)]
        lines += [f"address=/{name}/{ip}" for name, ip in (self.cfg.get("dns") or {}).items()]
        with self.net_lock:
            (self.root / "dns.conf").write_text("\n".join(lines) + "\n")
            self.sh.run("dns-start", str(self.root / "dns.conf"))

    def _route_to(self, ip: str, server: dict) -> None:
        """An IPv4 (/32) to the server's tap; an IPv6 /64 to its link-local address there."""
        lab = server["_lab"]
        if ":" in ip:
            self.sh.run("route6", prefix(ip), lab["tap"], link_local(lab["index"]))
        else:
            self.sh.run("route", prefix(ip), lab["tap"])

    def route_floating_ips(self, server: dict) -> None:
        """Hetzner keeps routing an assigned IP across a power cycle; the route went with the old tap."""
        with self.net_lock:
            for f in list(self.state["floating_ips"].values()):
                if f.get("server") == server["id"]:
                    self._route_to(f["ip"], server)

    def route_floating_ip(self, ip: str, server: dict | None) -> None:
        """Assigned to a running server, the IP goes to it; to a server that is off (or to none), it goes nowhere
        – the old server does not keep it."""
        with self.net_lock:
            if server is not None and self.vm_alive(server):
                self._route_to(ip, server)
            else:
                self.sh.run("route", prefix(ip))

    def route_floating(self, floating_ip: dict) -> None:
        """A Floating IP routed as the state says now: of two moves in a row, the later one wins."""
        with self.net_lock:
            server = self.state["servers"].get(floating_ip["server"]) if floating_ip.get("server") else None
            self.route_floating_ip(floating_ip["ip"], server)

    def apply_firewalls(self) -> None:
        """Replace the firewall table (inet hlabfw) atomically with the rules of all firewalls."""
        with self.net_lock:
            path = self.root / "firewall.nft"
            path.write_text(ruleset(self.state, {s["id"]: s["_lab"]["tap"] for s in self.state["servers"].values()}))
            self.sh.run("nft", "-f", str(path))
