"""How things happen in the lab, all through scripts/hlab.sh: VMs (vm.py), volumes (volumes.py), networks, clients,
Floating IPs and the firewall table (network.py), faults and latency (faults.py), the Storage Box (storagebox.py).
The API decides, the fabric carries it out and knows nothing of HTTP.

`Fabric` is made of those parts. They are mixins: each uses what `Fabric.__init__` sets up below (configuration,
state, root directory, shell, network lock) and may call the others' methods."""

import threading
from pathlib import Path

from .faults import Faults
from .network import Network
from .shell import Shell
from .storagebox import Box
from .vm import VMs
from .volumes import Volumes


class Fabric(VMs, Volumes, Network, Faults):
    def __init__(self, cfg: dict, state, root: Path, shell: Shell | None = None):
        self.cfg = cfg
        self.state = state
        self.root = root
        self.sh = shell or Shell()
        self.box = Box(self.sh)   # the Storage Box
        self.lab_key = root / "id_ed25519"
        # Taps, tc and nft change only under this lock: concurrent actions (two servers starting) must not
        # interleave, and a tap checked to exist must still be there when it is configured.
        self.net_lock = threading.RLock()
        self._local: tuple[float, set[str]] | None = None
