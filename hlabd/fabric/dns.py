"""DNS: the lab resolver and NSD, the nameserver of the DNS API's zones.

A part of the fabric (see __init__.py). The resolver (dnsmasq on the services
address, handed to every server by DHCP) answers the lab's own names and sends
questions about a zone to NSD, which serves the zone files the API renders
(api/desired/, `zone_files`). Other names go to the Docker host's resolver.
"""

import threading
from dataclasses import dataclass
from pathlib import Path

from .. import config
from .files import write_if_changed
from .shell import Shell

# NSD listens on the loopback only: just the resolver asks it
NSD_ADDRESS = "127.0.0.1"
NSD_PORT = 5353


@dataclass(frozen=True)
class ZoneFile:
    """A zone and its zone file, as NSD reads it."""

    name: str  # without the final dot
    text: str


def nsd_config(zones_dir: Path, zones: list[ZoneFile]) -> str:
    """Return NSD's configuration serving these zones from their files."""
    lines = [
        "server:",
        f"  ip-address: {NSD_ADDRESS}@{NSD_PORT}",
        "  pidfile: /run/nsd-hlab.pid",
        f'  zonesdir: "{zones_dir}"',
        '  database: ""',
        '  username: ""',
        "  hide-version: yes",
        "remote-control:",
        "  control-enable: no",
    ]
    for zone in zones:
        lines += ["zone:", f"  name: {zone.name}", f"  zonefile: {zone.name}.zone"]
    return "\n".join(lines) + "\n"


def resolver_config(zones: list[ZoneFile]) -> str:
    """Return the resolver's configuration: the lab's names, the zones to NSD."""
    lines = [
        f"address=/{name}/{address}"
        for address in (config.SERVICES, config.SERVICES6)
        for name in ("api.hlab", "box.hlab", "your-storagebox.de")
    ]
    lines += [
        f"address=/hetzner-api.hlab/{address}"
        for address in (config.HETZNER_API, config.HETZNER_API6)
    ]
    # Pebble listens on IPv4 only
    lines.append(f"address=/{config.ACME_NAME}/{config.SERVICES}")
    lines += [f"server=/{zone.name}/{NSD_ADDRESS}#{NSD_PORT}" for zone in zones]
    return "\n".join(lines) + "\n"


class Dns:
    """The lab resolver and NSD, configured through files under the lab's directory."""

    def __init__(self, root: Path, shell: Shell):
        """Keep the lab's directory and the shell."""
        self.root = root
        self.shell = shell
        # apply() may run in two threads at once: one writes and restarts at a time
        self.lock = threading.Lock()
        # NSD runs only once apply() started it in this process: the files under
        # /hlab outlive the container, NSD does not
        self.nsd_started = False

    def apply(self, zones: list[ZoneFile]) -> None:
        """Serve exactly these zones, and restart what their change touches.

        NSD restarts when a zone file or its configuration changed. The resolver
        restarts with it, which also drops the answers it cached, so a changed record
        is seen at once (a test lab's choice; real resolvers wait for the TTL).
        """
        directory = self.root / "dns"
        zones_dir = directory / "zones"
        with self.lock:
            zones_dir.mkdir(parents=True, exist_ok=True)
            changed = write_if_changed(
                directory / "nsd.conf", nsd_config(zones_dir, zones)
            )
            wanted = {f"{zone.name}.zone" for zone in zones}
            for old in zones_dir.glob("*.zone"):
                if old.name not in wanted:
                    old.unlink()
                    changed = True
            for zone in zones:
                changed |= write_if_changed(zones_dir / f"{zone.name}.zone", zone.text)
            changed |= write_if_changed(
                directory / "resolver.conf", resolver_config(zones)
            )
            if changed or not self.nsd_started:
                self.shell.run("start-nsd", str(directory / "nsd.conf"))
                self.shell.run("start-dns", str(directory / "resolver.conf"))
                self.nsd_started = True

    def start(self) -> None:
        """Start the resolver with the lab's own names, before any zone is known."""
        directory = self.root / "dns"
        with self.lock:
            directory.mkdir(parents=True, exist_ok=True)
            (directory / "resolver.conf").write_text(resolver_config([]))
            self.shell.run("start-dns", str(directory / "resolver.conf"))
