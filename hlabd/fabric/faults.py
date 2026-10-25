"""Faults (cuts, isolation, blocked services, anti-spoofing sources in nftables sets) and latency (tc netem between
locations, netem faults). Part of Fabric (see __init__.py)."""

import logging

from .. import config as C
from .network import link_local, prefix, route_table

log = logging.getLogger("hlabd.net")


class Faults:
    """Faults are kept in the state (`faults`, `netem`) by the API; a fault points at servers by ID (s:<id>) and at
    clients by name (c:<name>), so a renamed server keeps its faults and a new one under an old name does not
    inherit them."""

    def server_ifaces(self, ref: str, which: str = "all") -> list[str]:
        """Interfaces of a server ('public', 'private', 'all') or of a client, by reference."""
        kind, key = ref.split(":", 1)
        if kind == "s":
            server = self.state["servers"].get(int(key))
            if server:
                public = [server["_lab"]["tap"]]
                private = [nic["tap"] for nic in server["_lab"]["nics"]]
                return {"public": public, "private": private, "all": public + private}[which]
        for client in self.state["clients"].values():
            if kind == "c" and client["name"] == key:
                return [client["veth"]] if which in ("public", "all") else []
        raise KeyError(f"{ref} is gone")

    def drop_faults(self, ref: str, links: list[str]) -> None:
        """A server or client that goes takes its faults and the netem of its links along (a new one may get the
        same links). Called under the state's lock; the fault sets follow with apply_faults()."""
        self.state.data["faults"] = [x for x in self.state.data.get("faults", [])
                                     if ref not in (x.get("a_ref"), x.get("b_ref"), x.get("vm_ref"))]
        netem = self.state.data.get("netem", {})
        for link in links:
            netem.pop(link, None)

    def fault_sets(self) -> dict[str, list[str]]:
        """The elements of the nftables sets of table inet hlab: faults, cloud network members, the sources each
        public tap and client may send from (anti-spoofing)."""
        sets: dict[str, list[str]] = {name: [] for name in ("cuts", "isolated", "blocked_svc", "blocked_inet",
                                                            "private_pairs", "src4", "src6")}
        servers = list(self.state["servers"].values())
        members: dict[int, list[str]] = {}  # cloud network → private taps of its servers
        for server in servers:
            for nic in server["_lab"]["nics"]:
                members.setdefault(nic["network"], []).append(nic["tap"])
        sets["private_pairs"] = [f'"{a}" . "{b}"' for taps in members.values() for a in taps for b in taps if a != b]
        for server in servers:
            tap = server["_lab"]["tap"]
            sets["src6"].append(f'"{tap}" . {link_local(server["_lab"]["index"])}/128')   # its own link-local only
            for ip in self.source_prefixes(server, None):
                if ":" in ip:
                    sets["src6"].append(f'"{tap}" . {ip}')
                else:
                    sets["src4"].append(f'"{tap}" . {ip.split("/")[0]}')
        for client in list(self.state["clients"].values()):
            sets["src4"].append(f'"{client["veth"]}" . {client["ip"]}')
            sets["src6"].append(f'"{client["veth"]}" . {client["ip6"]}/128')
        for fault in self.state.data.setdefault("faults", []):
            try:
                if fault["type"] == "cut":
                    side_a = self.server_ifaces(fault["a_ref"], fault["net"])
                    side_b = self.server_ifaces(fault["b_ref"], fault["net"])
                    sets["cuts"] += [f'"{x}" . "{y}"' for x in side_a for y in side_b]
                    sets["cuts"] += [f'"{y}" . "{x}"' for x in side_a for y in side_b]
                elif fault["type"] == "isolate":
                    sets["isolated"] += [f'"{x}"' for x in self.server_ifaces(fault["vm_ref"])]
                elif fault["type"] == "block":
                    ifaces = self.server_ifaces(fault["vm_ref"])
                    if fault["what"] == "internet":
                        sets["blocked_inet"] += [f'"{x}"' for x in ifaces]
                    else:
                        ports = {"api": [80], "box": [22, 23]}[fault["what"]]   # the box: SFTP and SSH
                        sets["blocked_svc"] += [f'"{x}" . {port}' for x in ifaces for port in ports]
            except KeyError:
                continue  # the server is gone; the fault stays listed until cleared
        return sets

    def apply_faults(self) -> None:
        """Re-create the nftables set elements (interfaces come and go with power cycles), all in one nft
        transaction: no moment without anti-spoofing sources (which would drop every packet) or with faults lifted."""
        with self.net_lock:
            sets = self.fault_sets()
            tables = [f'"{nic["tap"]}" : {route_table(nic["network"])}'
                      for server in self.state["servers"].values() for nic in server["_lab"]["nics"]]
            lines = [f"flush set inet hlab {name}" for name in sets] + ["flush map inet hlab tap_tables"]
            if tables:
                lines.append(f"add element inet hlab tap_tables {{ {', '.join(tables)} }}")
            lines += [f"add element inet hlab {name} {{ {', '.join(elems)} }}" for name, elems in sets.items() if elems]
            script = self.root / "faults.nft"
            script.write_text("\n".join(lines) + "\n")
            self.sh.run("nft", "-f", str(script))

    def source_prefixes(self, server: dict, network: int | None) -> list[str]:
        """Source prefixes of a server's packets on a public tap (network None: public IPv4, its IPv6 /64, its
        Floating IPs) or on a tap of a cloud network (its private IP there – networks may overlap)."""
        if network is not None:
            return [prefix(nic["ip"]) for nic in server["_lab"]["nics"] if nic["network"] == network]
        ips = [prefix(self.ipv4(server))] if self.ipv4(server) else []
        ips += [prefix(f["ip"]) for f in list(self.state["floating_ips"].values()) if f.get("server") == server["id"]]
        if self.ipv6_net(server):
            ips.append(self.ipv6_net(server))
        return ips

    def apply_latency(self) -> None:
        """Delay packets towards a server by half the round-trip time to the sender's location, per tap; a netem
        fault on a tap replaces this until cleared."""
        with self.net_lock:
            netem = self.state.data.setdefault("netem", {})
            servers = list(self.state["servers"].values())
            for server in servers:
                if not self.vm_alive(server):
                    continue
                lab = server["_lab"]
                for tap, network in [(lab["tap"], None)] + [(nic["tap"], nic["network"]) for nic in lab["nics"]]:
                    try:   # one tap that cannot be set up must not fail another server's action
                        self.set_tap_latency(server, tap, network, servers, netem)
                    except RuntimeError as error:
                        log.warning("latency on %s: %s", tap, error)
            for client in self.state["clients"].values():   # an Internet client's link: only a netem fault
                link = client["veth"]
                if not self.sh.ok("link-exists", link):
                    continue
                self.sh.run("tc", "qdisc", "del", "dev", link, "root", check=False)
                if link in netem:
                    self.sh.run("tc", "qdisc", "replace", "dev", link, "root", "netem", *netem[link].split(),
                                check=False)

    def set_tap_latency(self, server: dict, tap: str, network: int | None, servers: list[dict], netem: dict) -> None:
        """The packets towards a server on one of its taps: a netem fault, or a delay band per round-trip time."""
        if not self.sh.ok("link-exists", tap):
            return  # a NIC being attached right now: its tap gets the rules when it comes up
        self.sh.run("tc", "qdisc", "del", "dev", tap, "root", check=False)
        if tap in netem:   # checked when it was set; a failure here must not stop a server's start
            self.sh.run("tc", "qdisc", "replace", "dev", tap, "root", "netem", *netem[tap].split(), check=False)
            return
        by_rtt: dict[int, list[str]] = {}
        for other in servers:
            if other is server or other["location"] == server["location"]:
                continue
            rtt = self.cfg["latency_ms"].get(C.location_pair(other["location"], server["location"]), 0)
            if rtt:
                by_rtt.setdefault(rtt, []).extend(self.source_prefixes(other, network))
        if not by_rtt:
            return
        self.sh.run("tc", "qdisc", "replace", "dev", tap, "root", "handle", "1:", "prio", "bands",
                    str(3 + len(by_rtt)), "priomap", *["1", "2", "2", "2", "1", "2", "0", "0"] + ["1"] * 8)
        for band, (rtt, ips) in enumerate(sorted(by_rtt.items()), start=4):
            self.sh.run("tc", "qdisc", "replace", "dev", tap, "parent", f"1:{band}", "handle", f"{band}0:",
                        "netem", "delay", f"{rtt / 2}ms")
            for ip in ips:
                v6 = ":" in ip
                self.sh.run("tc", "filter", "add", "dev", tap, "parent", "1:", "protocol", "ipv6" if v6 else "ip",
                            "prio", "2" if v6 else "1", "u32", "match", "ip6" if v6 else "ip", "src", ip,
                            "flowid", f"1:{band}")
