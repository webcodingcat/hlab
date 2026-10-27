"""Load balancers: HAProxy in a network namespace of its own, behind a veth.

A part of the fabric (see __init__.py). A load balancer's namespace is built like an
Internet client's (hlab.sh add-namespace): its public addresses are routed to its veth,
and Hetzner's gateway is its on-link gateway, as on a server. HAProxy runs inside with
the configuration the API renders (api/desired.py), so connections reach the targets
from the load balancer's own address, and the client's address reaches it unchanged.
"""

import csv
import shutil
import socket
from dataclasses import dataclass
from pathlib import Path

from .. import config
from .files import write_if_changed
from .shell import Shell


@dataclass(frozen=True)
class BalancerSpec:
    """What a load balancer runs: its addresses, HAProxy's configuration, its certs."""

    lid: int
    ipv4: str
    ipv6: str
    config: str  # haproxy.cfg; paths in it are relative to the balancer's directory
    certificates: dict[str, str]  # file name → PEM chain and private key


def namespace(lid: int) -> str:
    """Return the name of a load balancer's namespace and of its veth."""
    return f"lb{lid}"


class Balancers:
    """The load balancers' namespaces and HAProxy processes."""

    def __init__(self, root: Path, shell: Shell):
        """Keep the directory of the balancers' files and the shell."""
        self.directory = root / "lb"
        self.shell = shell

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
        self.shell.run(
            "add-namespace", name, name, spec.ipv4, spec.ipv6, config.GATEWAY
        )
        changed = write_if_changed(directory / "haproxy.cfg", spec.config)
        for old in (directory / "certs").iterdir():
            if old.name not in spec.certificates:
                old.unlink()
                changed = True
        for file_name, pem in spec.certificates.items():
            changed |= write_if_changed(directory / "certs" / file_name, pem)
        if changed or not self.shell.ok("is-balancer-alive", str(directory)):
            self.shell.run("start-balancer", name, str(directory))

    def remove(self, lid: int) -> None:
        """Stop a load balancer's HAProxy and remove its namespace and files."""
        directory = self.directory / str(lid)
        self.shell.run("stop-balancer", str(directory), check=False)
        self.shell.run("delete-namespace", namespace(lid), namespace(lid))
        shutil.rmtree(directory, ignore_errors=True)

    def health(self, lid: int) -> dict[tuple[str, str], str]:
        """Return HAProxy's check status per (backend, server); empty if it is down.

        The status is HAProxy's ("UP", "DOWN 1/3", "no check", ...).
        """
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
            return {}
        lines = b"".join(chunks).decode().lstrip("# ").splitlines()
        return {
            (row["pxname"], row["svname"]): row["status"]
            for row in csv.DictReader(lines)
            if row.get("svname") not in (None, "FRONTEND", "BACKEND")
        }
