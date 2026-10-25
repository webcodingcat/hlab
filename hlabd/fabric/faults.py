"""Faults (cuts, isolation, blocked services, anti-spoofing sources in nftables sets) and latency (tc netem between
locations, netem faults). Part of Fabric (see __init__.py)."""

from .. import config as C
from .network import link_local, prefix, route_table


class Faults:
    def fault_ref(self, name: str) -> str:
        """What a fault points at, by ID (s:<id>) or client name (c:<name>): a renamed server keeps its faults, a
        new server under an old name does not inherit them."""
        for s in self.state["servers"].values():
            if s["name"] == name:
                return f"s:{s['id']}"
        for c in self.state["clients"].values():
            if c["name"] == name:
                return f"c:{name}"
        raise KeyError(f"no server or client named {name!r}")

    def server_ifaces(self, ref: str, which: str = "all") -> list[str]:
        """Interfaces of a server ('public', 'private', 'all') or of a client, by reference (fault_ref) or name."""
        if not ref.startswith(("s:", "c:")):
            ref = self.fault_ref(ref)
        kind, key = ref.split(":", 1)
        if kind == "s":
            s = self.state["servers"].get(int(key))
            if s:
                pub = [s["_lab"]["tap"]]
                prv = [n["tap"] for n in s["_lab"]["nics"]]
                return {"public": pub, "private": prv, "all": pub + prv}[which]
        for c in self.state["clients"].values():
            if kind == "c" and c["name"] == key:
                return [c["veth"]] if which in ("public", "all") else []
        raise KeyError(f"{ref} is gone")

    def apply_faults(self) -> None:
        """Re-create the nftables set elements from the fault list (interfaces come and go with power cycles)."""
        with self.net_lock:
            self._apply_faults()

    def _apply_faults(self) -> None:
        """All sets in one nft transaction: no moment without anti-spoofing sources (which would drop every packet)
        or with faults lifted."""
        faults = self.state.data.setdefault("faults", [])
        sets: dict[str, list[str]] = {s: [] for s in ("cuts", "isolated", "blocked_svc", "blocked_inet",
                                                       "private_pairs", "src4", "src6")}
        servers = list(self.state["servers"].values())
        members: dict[int, list[str]] = {}  # cloud network → private taps of its servers
        for srv in servers:
            for nic in srv["_lab"]["nics"]:
                members.setdefault(nic["network"], []).append(nic["tap"])
        sets["private_pairs"] = [f'"{a}" . "{b}"' for taps in members.values() for a in taps for b in taps if a != b]
        # anti-spoofing: the sources each public tap and client may send from
        for srv in servers:
            tap = srv["_lab"]["tap"]
            sets["src6"].append(f'"{tap}" . {link_local(srv["_lab"]["index"])}/128')   # its own link-local only
            for ip in self._server_ips(srv, None):
                if ":" in ip:
                    sets["src6"].append(f'"{tap}" . {ip}')
                else:
                    sets["src4"].append(f'"{tap}" . {ip.split("/")[0]}')
        for c in list(self.state["clients"].values()):
            sets["src4"].append(f'"{c["veth"]}" . {c["ip"]}')
            sets["src6"].append(f'"{c["veth"]}" . {c["ip6"]}/128')
        for flt in faults:
            try:
                if flt["type"] == "cut":
                    a = self.server_ifaces(flt.get("a_ref") or flt["a"], flt["net"])
                    b = self.server_ifaces(flt.get("b_ref") or flt["b"], flt["net"])
                    sets["cuts"] += [f'"{x}" . "{y}"' for x in a for y in b] + [f'"{y}" . "{x}"' for x in a for y in b]
                elif flt["type"] == "isolate":
                    sets["isolated"] += [f'"{x}"' for x in self.server_ifaces(flt.get("vm_ref") or flt["vm"])]
                elif flt["type"] == "block":
                    ifs = self.server_ifaces(flt.get("vm_ref") or flt["vm"])
                    if flt["what"] == "internet":
                        sets["blocked_inet"] += [f'"{x}"' for x in ifs]
                    else:
                        ports = {"api": [80], "box": [22, 23]}[flt["what"]]   # the box: SFTP and SSH
                        sets["blocked_svc"] += [f'"{x}" . {p}' for x in ifs for p in ports]
            except KeyError:
                continue  # the server is gone; the fault stays listed until cleared
        tables = [f'"{nic["tap"]}" : {route_table(nic["network"])}' for srv in servers for nic in srv["_lab"]["nics"]]
        lines = [f"flush set inet hlab {name}" for name in sets] + ["flush map inet hlab tap_tables"]
        if tables:
            lines.append(f"add element inet hlab tap_tables {{ {', '.join(tables)} }}")
        lines += [f"add element inet hlab {name} {{ {', '.join(elems)} }}" for name, elems in sets.items() if elems]
        script = self.root / "faults.nft"
        script.write_text("\n".join(lines) + "\n")
        self.sh.run("nft", "-f", str(script))

    def _server_ips(self, s: dict, network: int | None) -> list[str]:
        """Source prefixes of a server's packets on a public tap (network None: public IPv4, its IPv6 /64, its
        Floating IPs) or on a tap of a cloud network (its private IP there – networks may overlap)."""
        if network is not None:
            return [prefix(n["ip"]) for n in s["_lab"]["nics"] if n["network"] == network]
        ips = [prefix(self.ipv4(s))] if self.ipv4(s) else []
        ips += [prefix(f["ip"]) for f in list(self.state["floating_ips"].values()) if f.get("server") == s["id"]]
        if self.ipv6_net(s):
            ips.append(self.ipv6_net(s))
        return ips

    def apply_latency(self) -> None:
        """Delay packets towards a server by half the round-trip time to the sender's location, per tap; a netem
        fault on a tap replaces this until cleared."""
        with self.net_lock:
            self._apply_latency()

    def _apply_latency(self) -> None:
        netem = self.state.data.setdefault("netem", {})
        servers = list(self.state["servers"].values())
        for s in servers:
            if not self.vm_alive(s):
                continue
            taps = [(s["_lab"]["tap"], None)] + [(n["tap"], n["network"]) for n in s["_lab"]["nics"]]
            for tap, network in taps:
                if not self.sh.ok("link-exists", tap):
                    continue  # a NIC being attached right now: its tap gets the rules when it comes up
                self.sh.run("tc", "qdisc", "del", "dev", tap, "root", check=False)
                if tap in netem:   # checked when it was set; a failure here must not stop a server's start
                    self.sh.run("tc", "qdisc", "replace", "dev", tap, "root", "netem", *netem[tap].split(),
                                check=False)
                    continue
                by_rtt: dict[int, list[str]] = {}
                for o in servers:
                    if o is s or o["location"] == s["location"]:
                        continue
                    rtt = self.cfg["latency_ms"].get(C.location_pair(o["location"], s["location"]), 0)
                    if rtt:
                        by_rtt.setdefault(rtt, []).extend(self._server_ips(o, network))
                if not by_rtt:
                    continue
                self.sh.run("tc", "qdisc", "replace", "dev", tap, "root", "handle", "1:", "prio", "bands",
                            str(3 + len(by_rtt)), "priomap", *["1", "2", "2", "2", "1", "2", "0", "0"] + ["1"] * 8)
                for band, (rtt, ips) in enumerate(sorted(by_rtt.items()), start=4):
                    self.sh.run("tc", "qdisc", "replace", "dev", tap, "parent", f"1:{band}", "handle", f"{band}0:",
                                "netem", "delay", f"{rtt / 2}ms")
                    for ip in ips:
                        v6 = ":" in ip
                        self.sh.run("tc", "filter", "add", "dev", tap, "parent", "1:", "protocol",
                                    "ipv6" if v6 else "ip", "prio", "2" if v6 else "1", "u32", "match",
                                    "ip6" if v6 else "ip", "src", ip, "flowid", f"1:{band}")
        for c in self.state["clients"].values():   # an Internet client's link: only a netem fault
            if not self.sh.ok("link-exists", c["veth"]):
                continue
            self.sh.run("tc", "qdisc", "del", "dev", c["veth"], "root", check=False)
            if c["veth"] in netem:
                self.sh.run("tc", "qdisc", "replace", "dev", c["veth"], "root", "netem", *netem[c["veth"]].split(),
                            check=False)
