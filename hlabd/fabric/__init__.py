"""How things happen in the lab, all through scripts/hlab.sh.

The parts: VMs (vm.py), volumes (volumes.py), networks, clients, Floating IPs and the
firewall table (network.py), faults and latency (faults.py), the Storage Box
(storagebox.py). The API decides, the fabric carries it out and knows nothing of HTTP.

`Fabric` is made of those parts. They are mixins, except the Storage Box, which is an
object of its own (`self.box`, a `Box`): each mixin uses what `Fabric.__init__` sets up
below (configuration, state, root directory, shell, network lock) and may call the
others' methods. `FabricBase` (base.py), the mixins' base, declares both for the type
checker and the IDE.
"""

import threading
from pathlib import Path

from ..state import State
from .faults import Faults
from .network import Network
from .shell import Shell
from .storagebox import Box
from .vm import VMs
from .volumes import Volumes


class Fabric(VMs, Volumes, Network, Faults):
    """The lab's fabric: VMs, volumes, network and faults in one object."""

    def __init__(self, cfg: dict, state: State, root: Path, shell: Shell | None = None):
        """Set up what the mixins share: configuration, state, root, shell, locks."""
        self.cfg = cfg
        self.state = state
        self.root = root
        self.sh = shell or Shell()
        self.box = Box(self.sh)  # the Storage Box
        self.lab_key = root / "id_ed25519"
        # Taps, tc and nft change only under this lock: concurrent actions (two servers
        # starting) must not interleave, and a tap checked to exist must still be there
        # when it is configured.
        self.net_lock = threading.RLock()
        # the container's own addresses and when they were read, kept a few seconds
        # (Network.local_addresses)
        self._local_addresses_cache: tuple[float, set[str]] | None = None
