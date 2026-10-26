"""What the fabric's mixins share, declared for the type checker and the IDE.

`Fabric.__init__` sets the attributes up; the methods are those a mixin calls on
another, each defined in the mixin its docstring names.
"""

import threading
from pathlib import Path
from typing import TYPE_CHECKING

from ..records import Nic, Server, Volume
from ..state import State
from .shell import Shell
from .storagebox import Box


class FabricBase:
    """The base of the fabric's mixins (VMs, Volumes, Network, Faults)."""

    cfg: dict  # the configuration (config.py)
    state: State
    root: Path  # the lab's directory (VMs, volumes, images, DHCP, nft scripts)
    sh: Shell  # runs scripts/hlab.sh
    box: Box  # the Storage Box
    lab_key: Path  # the lab's SSH key
    # taps, tc and nft change only under this lock (Fabric.__init__ says why)
    net_lock: threading.RLock
    # the container's own addresses and when they were read (Network.local_addresses)
    _local_addresses_cache: tuple[float, set[str]] | None

    if TYPE_CHECKING:

        def vm_dir(self, server: Server) -> Path:
            """Return the directory of a server's VM (VMs)."""

        def vm_alive(self, server: Server) -> bool:
            """Tell whether the server's VM process runs (VMs)."""

        def volume_file(self, volume: Volume) -> Path:
            """Return the path of a volume's raw disk file (Volumes)."""

        def nic_up(self, nic: Nic) -> None:
            """Bring up a NIC's tap in its cloud network (Network)."""

        def write_dhcp(self) -> None:
            """Run one dnsmasq per tap, only for taps that exist (Network)."""

        def apply_routes(self) -> None:
            """Put the routes of cloud networks in place (Network)."""

        def reroute_floating_ips(self, server: Server) -> None:
            """Route the Floating IPs assigned to a server to it again (Network)."""

        def apply_faults(self) -> None:
            """Re-create the nftables set elements (Faults)."""

        def apply_latency(self) -> None:
            """Delay packets towards each server, per tap (Faults)."""
