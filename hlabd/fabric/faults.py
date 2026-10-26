"""Faults and latency.

Faults are cuts, isolation, blocked services and anti-spoofing sources in nftables sets;
latency is tc netem between locations, and netem faults. A part of the fabric (see
__init__.py); it asks the VMs (vm.py) whether a server runs.

What the sets hold and how each tap is shaped is computed from the state by the API
(api/desired.py); this part only puts it in place.
"""

import logging
import threading
from dataclasses import dataclass, field
from pathlib import Path

from ..records import Server
from .addresses import route_table
from .shell import Shell
from .vm import VMs

log = logging.getLogger(__name__)


@dataclass
class Shaping:
    """How the packets towards one tap (or a client's link) are shaped."""

    tap: str
    netem: str | None  # its netem fault; it replaces the delays
    # round-trip time (ms) → the source prefixes delayed by half of it; a time may
    # have no prefixes (its band is made all the same)
    delays: dict[int, list[str]] = field(default_factory=dict)


@dataclass
class Latency:
    """The shaping of every server's taps and every client's link."""

    servers: list[tuple[Server, list[Shaping]]]  # shaped only while its VM runs
    clients: list[Shaping]  # shaped only while the link exists (netem only)


class Faults:
    """Faults and latency, in nftables sets and tc qdiscs.

    Faults are kept in the state (`faults`, `netem`) by the API, which also drops them
    with a deleted server or client (api/faults.py). A fault points at servers by ID
    (s:<id>) and at clients by name (c:<name>), so a renamed server keeps its faults
    and a new one under an old name does not inherit them.
    """

    def __init__(
        self,
        root: Path,
        shell: Shell,
        net_lock: threading.RLock,
        vms: VMs,
    ):
        """Store what the faults use; `net_lock` is the fabric's lock (see Fabric)."""
        self.root = root
        self.shell = shell
        self.net_lock = net_lock
        self.vms = vms

    def apply(
        self, sets: dict[str, list[str]], tap_networks: list[tuple[str, int]]
    ) -> None:
        """Re-create the nftables set elements, all in one nft transaction.

        `sets` are the elements (nft syntax) of each set; `tap_networks` maps each
        private tap to its cloud network, whose routing table it uses. Interfaces come
        and go with power cycles. With one transaction there is no moment without
        anti-spoofing sources (which would drop every packet) and no moment without
        the faults.
        """
        tables = [f'"{tap}" : {route_table(nid)}' for tap, nid in tap_networks]
        with self.net_lock:
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

    def apply_latency(self, latency: Latency) -> None:
        """Delay packets towards each running server, per tap.

        The delay is half the round-trip time to the sender's location; a netem fault on
        a tap replaces this until cleared.
        """
        with self.net_lock:
            for server, shapings in latency.servers:
                if not self.vms.is_alive(server):
                    continue
                for shaping in shapings:
                    # a broken tap must not fail another server's action
                    try:
                        self.set_tap_latency(shaping)
                    except RuntimeError as error:
                        log.warning("latency on %s: %s", shaping.tap, error)
            # an Internet client's link: only a netem fault
            for shaping in latency.clients:
                if self.shell.ok("link-exists", shaping.tap):
                    self.apply_netem(shaping.tap, shaping.netem)

    def apply_netem(self, link: str, netem: str | None) -> bool:
        """Return whether a link has a netem fault, after applying it to the link.

        Clears the link's queueing first. The fault was checked when it was set; a
        failure here must not stop a server's start.
        """
        self.tc(f"qdisc del dev {link} root", check=False)
        if netem:
            self.tc(f"qdisc replace dev {link} root netem {netem}", check=False)
        return bool(netem)

    def set_tap_latency(self, shaping: Shaping) -> None:
        """Shape the packets towards a server on one of its taps.

        They get its netem fault, or else a delay band per round-trip time to the other
        servers' locations.
        """
        tap = shaping.tap
        if not self.shell.ok("link-exists", tap):
            # a NIC being attached right now: its tap gets the rules when it comes up
            return
        if self.apply_netem(tap, shaping.netem):
            return
        if not shaping.delays:
            return
        # a prio qdisc with one band per round-trip time; the default bands keep the
        # usual priority map
        priomap = " ".join(["1", "2", "2", "2", "1", "2", "0", "0"] + ["1"] * 8)
        bands = 3 + len(shaping.delays)
        self.tc(
            f"qdisc replace dev {tap} root handle 1:"
            f" prio bands {bands} priomap {priomap}"
        )
        for band, (rtt, ips) in enumerate(sorted(shaping.delays.items()), start=4):
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
