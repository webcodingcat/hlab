"""Load balancers: HAProxy in a network namespace of its own, behind a veth.

A part of the fabric (see __init__.py). A load balancer's namespace is built like an
Internet client's (hlab.sh add-namespace): its public addresses are routed to its veth,
and Hetzner's gateway is its on-link gateway, as on a server. HAProxy runs inside with
the configuration the API renders (api/desired.py), so connections reach the targets
from the load balancer's own address, and the client's address reaches it unchanged.
"""

import csv
import ipaddress
import shutil
import socket
import threading
from dataclasses import dataclass, field
from pathlib import Path

from .. import config
from .addresses import network_gateway, route_table
from .files import write_if_changed
from .shell import Shell


@dataclass(frozen=True)
class BalancerLink:
    """A load balancer's NIC in a cloud network, as it is set up."""

    ip: str
    network: int  # the network's ID: its routing table
    ip_range: str  # the network's range: the gateway address and its route


@dataclass(frozen=True)
class BalancerSpec:
    """What a load balancer runs: its addresses, HAProxy's configuration, its certs."""

    lid: int
    ipv4: str
    ipv6: str
    config: str  # haproxy.cfg; paths in it are relative to the balancer's directory
    certificates: dict[str, str]  # file name → PEM chain and private key
    public: bool = True  # off: its public addresses are not routed to it
    links: list[BalancerLink] = field(default_factory=list)  # one at most


@dataclass(frozen=True)
class BalancerCounters:
    """A load balancer's counters since HAProxy started; the metrics are their rates."""

    open_connections: int  # now, not counted up
    connections: int
    requests: int  # HTTP(S) only
    bytes_in: int  # from the clients
    bytes_out: int


def namespace(lid: int) -> str:
    """Return the name of a load balancer's namespace and of its veth."""
    return f"lb{lid}"


def private_veth(lid: int) -> str:
    """Return the name of a load balancer's veth in its cloud network.

    It starts with "prv" like a server's NIC, so the lab treats it as one.
    """
    return f"prvl{lid}"


class Balancers:
    """The load balancers' namespaces and HAProxy processes."""

    def __init__(self, root: Path, shell: Shell, net_lock: threading.RLock):
        """Keep the directory of the balancers' files, the shell and the network lock.

        `net_lock` is the fabric's (see Fabric): routes and veths change under it.
        """
        self.directory = root / "lb"
        self.shell = shell
        self.net_lock = net_lock

    def apply(self, specs: list[BalancerSpec]) -> None:
        """Run exactly these load balancers: start, reload or remove each one.

        HAProxy reloads only when its configuration or a certificate changed; a
        reload hands open connections over to the new process.
        """
        self.directory.mkdir(parents=True, exist_ok=True)
        wanted = {str(spec.lid) for spec in specs}
        for old in self.directory.iterdir():
            if old.name not in wanted:
                self.remove(int(old.name))
        for spec in specs:
            self.run(spec)

    def run(self, spec: BalancerSpec) -> None:
        """Set up a load balancer's namespace and (re)start its HAProxy if needed."""
        name = namespace(spec.lid)
        directory = self.directory / str(spec.lid)
        (directory / "certs").mkdir(parents=True, exist_ok=True)
        with self.net_lock:
            self.shell.run(
                "add-namespace", name, name, spec.ipv4, spec.ipv6, config.GATEWAY
            )
            if not spec.public:  # nothing routes its public addresses to it
                self.shell.run("set-route", f"{spec.ipv4}/32")
                self.shell.run("set-route", f"{spec.ipv6}/128")
            self.link_up(spec.lid, spec.links)
        changed = write_if_changed(directory / "haproxy.cfg", spec.config)
        for old in (directory / "certs").iterdir():
            if old.name not in spec.certificates:
                old.unlink()
                changed = True
        for file_name, pem in spec.certificates.items():
            changed |= write_if_changed(directory / "certs" / file_name, pem)
        if changed or not self.shell.ok("is-balancer-alive", str(directory)):
            self.shell.run("start-balancer", name, str(directory))

    def link_up(self, lid: int, links: list[BalancerLink]) -> None:
        """Put a load balancer into its cloud network, or take it out of it."""
        veth = private_veth(lid)
        if not links:
            self.shell.run("delete-namespace-nic", veth)
            return
        link = links[0]
        ip_range = ipaddress.ip_network(link.ip_range)
        self.shell.run(
            "add-namespace-nic",
            namespace(lid),
            veth,
            link.ip,
            f"{network_gateway(str(ip_range))}/{ip_range.prefixlen}",
            route_table(link.network),
            str(ip_range),
        )

    def remove(self, lid: int) -> None:
        """Stop a load balancer's HAProxy and remove its namespace and files."""
        directory = self.directory / str(lid)
        self.shell.run("stop-balancer", str(directory), check=False)
        with self.net_lock:  # the veth in a cloud network goes with the namespace
            self.shell.run("delete-namespace", namespace(lid), namespace(lid))
        shutil.rmtree(directory, ignore_errors=True)

    def show_stat(self, lid: int) -> list[dict[str, str]]:
        """Return the rows of HAProxy's statistics; none if it does not answer."""
        sock = self.directory / str(lid) / "haproxy.sock"
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
                connection.settimeout(2)
                connection.connect(str(sock))
                connection.sendall(b"show stat\n")
                chunks = []
                while chunk := connection.recv(65536):
                    chunks.append(chunk)
        except OSError:
            return []
        lines = b"".join(chunks).decode().lstrip("# ").splitlines()
        return list(csv.DictReader(lines))

    def health(self, lid: int) -> dict[tuple[str, str], str]:
        """Return HAProxy's check status per (backend, server); empty if it is down.

        The status is HAProxy's ("UP", "DOWN 1/3", "no check", ...).
        """
        return {
            (row["pxname"], row["svname"]): row["status"]
            for row in self.show_stat(lid)
            if row.get("svname") not in (None, "FRONTEND", "BACKEND")
        }

    def counters(self, lid: int) -> BalancerCounters | None:
        """Return the load balancer's counters over all its frontends; None if down."""
        frontends = [r for r in self.show_stat(lid) if r.get("svname") == "FRONTEND"]
        if not frontends:
            return None

        def total(column: str) -> int:
            return sum(int(row.get(column) or 0) for row in frontends)

        return BalancerCounters(
            total("scur"), total("stot"), total("req_tot"), total("bin"), total("bout")
        )
