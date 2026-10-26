"""Faults and latency.

Faults are cuts, isolation, blocked services and anti-spoofing sources in nftables sets;
latency is tc netem between locations, and netem faults. A part of the fabric (see
__init__.py); it asks the VMs (vm.py) whether a server runs.
"""

import logging
import threading
from pathlib import Path

from .. import config
from ..records import Server
from ..state import State, client_by_name, primary_address_of
from .addresses import link_local, prefix, route_table
from .shell import Shell
from .vm import VMs

log = logging.getLogger(__name__)


class Faults:
    """Faults and latency, in nftables sets and tc qdiscs.

    Faults are kept in the state (`faults`, `netem`) by the API, which also drops them
    with a deleted server or client (api/faults.py). A fault points at servers by ID
    (s:<id>) and at clients by name (c:<name>), so a renamed server keeps its faults
    and a new one under an old name does not inherit them.
    """

    def __init__(
        self,
        cfg: dict,
        state: State,
        root: Path,
        shell: Shell,
        net_lock: threading.RLock,
        vms: VMs,
    ):
        """Store what the faults use; `net_lock` is the fabric's lock (see Fabric)."""
        self.cfg = cfg
        self.state = state
        self.root = root
        self.shell = shell
        self.net_lock = net_lock
        self.vms = vms

    def target_ifaces(self, ref: str, which: str = "all") -> list[str]:
        """Return the interfaces of what a fault points at: a server or a client.

        `which` picks 'public', 'private' or 'all' (a client has a public link only).
        Raises KeyError when the server or client is gone.
        """
        kind, key = ref.split(":", 1)
        if kind == "s":
            server = self.state["servers"].get(int(key))
            if not server:
                raise KeyError(f"{ref} is gone")
            public = [server["_lab"]["tap"]]
            with self.state.lock:
                private = [nic["tap"] for nic in server["_lab"]["nics"]]
            if which == "public":
                return public
            if which == "private":
                return private
            return public + private
        client = client_by_name(self.state, key)
        if kind != "c" or not client:
            raise KeyError(f"{ref} is gone")
        return [client["veth"]] if which in ("public", "all") else []

    def fault_sets(self) -> dict[str, list[str]]:
        """Return the elements of the nftables sets of table inet hlab.

        These are the faults, the cloud network members, and the sources each public tap
        and client may send from (anti-spoofing). The caller holds the state's lock.
        """
        self.state.lock.assert_held()
        sets: dict[str, list[str]] = {
            name: []
            for name in (
                "cuts",
                "isolated",
                "blocked_svc",
                "blocked_inet",
                "private_pairs",
                "src4",
                "src6",
            )
        }
        servers = list(self.state["servers"].values())
        # cloud network → private taps of its servers
        members: dict[int, list[str]] = {}
        for server in servers:
            for nic in server["_lab"]["nics"]:
                members.setdefault(nic["network"], []).append(nic["tap"])
        sets["private_pairs"] = [
            f'"{a}" . "{b}"'
            for taps in members.values()
            for a in taps
            for b in taps
            if a != b
        ]
        for server in servers:
            tap = server["_lab"]["tap"]
            # its own link-local only
            sets["src6"].append(f'"{tap}" . {link_local(server["_lab"]["index"])}/128')
            for ip in self.source_prefixes(server, None):
                if ":" in ip:
                    sets["src6"].append(f'"{tap}" . {ip}')
                else:
                    sets["src4"].append(f'"{tap}" . {ip.split("/")[0]}')
        for client in list(self.state["clients"].values()):
            sets["src4"].append(f'"{client["veth"]}" . {client["ip"]}')
            sets["src6"].append(f'"{client["veth"]}" . {client["ip6"]}/128')
        for fault in self.state.data["faults"]:
            try:
                if fault["type"] == "cut":
                    first = self.target_ifaces(fault["first_ref"], fault["net"])
                    second = self.target_ifaces(fault["second_ref"], fault["net"])
                    sets["cuts"] += [f'"{x}" . "{y}"' for x in first for y in second]
                    sets["cuts"] += [f'"{y}" . "{x}"' for x in first for y in second]
                elif fault["type"] == "isolate":
                    sets["isolated"] += [
                        f'"{x}"' for x in self.target_ifaces(fault["target_ref"])
                    ]
                elif fault["type"] == "block":
                    ifaces = self.target_ifaces(fault["target_ref"])
                    if fault["what"] == "internet":
                        sets["blocked_inet"] += [f'"{x}"' for x in ifaces]
                    else:
                        # api: HTTP; box: SFTP and SSH
                        ports = {"api": [80], "box": [22, 23]}[fault["what"]]
                        sets["blocked_svc"] += [
                            f'"{x}" . {port}' for x in ifaces for port in ports
                        ]
            except KeyError:
                continue  # the server is gone; the fault stays listed until cleared
        return sets

    def apply(self) -> None:
        """Re-create the nftables set elements, all in one nft transaction.

        Interfaces come and go with power cycles. With one transaction there is no
        moment without anti-spoofing sources (which would drop every packet) and no
        moment without the faults.
        """
        with self.net_lock:
            with self.state.lock:
                sets = self.fault_sets()
                tables = [
                    f'"{nic["tap"]}" : {route_table(nic["network"])}'
                    for server in self.state["servers"].values()
                    for nic in server["_lab"]["nics"]
                ]
            lines = [f"flush set inet hlab {name}" for name in sets] + [
                "flush map inet hlab tap_tables"
            ]
            if tables:
                lines.append(
                    f"add element inet hlab tap_tables {{ {', '.join(tables)} }}"
                )
            lines += [
                f"add element inet hlab {name} {{ {', '.join(elems)} }}"
                for name, elems in sets.items()
                if elems
            ]
            script = self.root / "faults.nft"
            script.write_text("\n".join(lines) + "\n")
            self.shell.run("nft", "-f", str(script))

    def source_prefixes(self, server: Server, network: int | None) -> list[str]:
        """Return the source prefixes of a server's packets on one of its taps.

        On a public tap (network None): its public IPv4, its IPv6 /64, its Floating
        IPs. On a tap of a cloud network: its private IP there (networks may overlap).
        """
        if network is not None:
            with self.state.lock:
                return [
                    prefix(nic["ip"])
                    for nic in server["_lab"]["nics"]
                    if nic["network"] == network
                ]
        ipv4 = primary_address_of(self.state, server, "ipv4")
        ipv6_net = primary_address_of(self.state, server, "ipv6")
        ips = [prefix(ipv4)] if ipv4 else []
        with self.state.lock:
            ips += [
                prefix(f["ip"])
                for f in self.state["floating_ips"].values()
                if f.get("server") == server["id"]
            ]
        if ipv6_net:
            ips.append(ipv6_net)
        return ips

    def apply_latency(self) -> None:
        """Delay packets towards each server, per tap.

        The delay is half the round-trip time to the sender's location; a netem fault on
        a tap replaces this until cleared.
        """
        with self.net_lock:
            with self.state.lock:  # copies: the loops below run scripts
                servers = list(self.state["servers"].values())
                clients = list(self.state["clients"].values())
            for server in servers:
                if not self.vms.is_alive(server):
                    continue
                server_lab = server["_lab"]
                taps: list[tuple[str, int | None]] = [(server_lab["tap"], None)]
                with self.state.lock:
                    taps += [(nic["tap"], nic["network"]) for nic in server_lab["nics"]]
                for tap, network in taps:
                    # a broken tap must not fail another server's action
                    try:
                        self.set_tap_latency(server, tap, network, servers)
                    except RuntimeError as error:
                        log.warning("latency on %s: %s", tap, error)
            # an Internet client's link: only a netem fault
            for client in clients:
                if self.shell.ok("link-exists", client["veth"]):
                    self.apply_netem(client["veth"])

    def apply_netem(self, link: str) -> bool:
        """Return whether a link has a netem fault, after applying it to the link.

        Clears the link's queueing first. The fault was checked when it was set; a
        failure here must not stop a server's start.
        """
        self.tc(f"qdisc del dev {link} root", check=False)
        fault = self.state.data["netem"].get(link)
        if fault:
            self.tc(f"qdisc replace dev {link} root netem {fault}", check=False)
        return bool(fault)

    def set_tap_latency(
        self, server: Server, tap: str, network: int | None, servers: list[Server]
    ) -> None:
        """Shape the packets towards a server on one of its taps.

        They get its netem fault, or else a delay band per round-trip time to the other
        servers' locations.
        """
        if not self.shell.ok("link-exists", tap):
            # a NIC being attached right now: its tap gets the rules when it comes up
            return
        if self.apply_netem(tap):
            return
        by_rtt: dict[int, list[str]] = {}
        for other in servers:
            if other is server or other["location"] == server["location"]:
                continue
            pair = config.location_pair(other["location"], server["location"])
            rtt = self.cfg["latency_ms"].get(pair, 0)
            if rtt:
                by_rtt.setdefault(rtt, []).extend(self.source_prefixes(other, network))
        if not by_rtt:
            return
        # a prio qdisc with one band per round-trip time; the default bands keep the
        # usual priority map
        priomap = " ".join(["1", "2", "2", "2", "1", "2", "0", "0"] + ["1"] * 8)
        bands = 3 + len(by_rtt)
        self.tc(
            f"qdisc replace dev {tap} root handle 1:"
            f" prio bands {bands} priomap {priomap}"
        )
        for band, (rtt, ips) in enumerate(sorted(by_rtt.items()), start=4):
            self.tc(
                f"qdisc replace dev {tap} parent 1:{band} handle {band}0:"
                f" netem delay {rtt / 2}ms"
            )
            for ip in ips:
                if ":" in ip:
                    family, prio, match = "ipv6", 2, "ip6"
                else:
                    family, prio, match = "ip", 1, "ip"
                self.tc(
                    f"filter add dev {tap} parent 1: protocol {family} prio {prio}"
                    f" u32 match {match} src {ip} flowid 1:{band}"
                )

    def tc(self, command: str, check: bool = True) -> None:
        """Run a tc command, given as one line (no argument contains spaces)."""
        self.shell.run("tc", *command.split(), check=check)
