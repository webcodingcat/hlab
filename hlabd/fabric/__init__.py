"""How things happen in the lab, all through scripts/hlab.sh: VMs (vm.py), networks, Floating IPs and the firewall
table (network.py), faults and latency (faults.py), the Storage Box (storagebox.py). The API decides, the fabric
carries it out and knows nothing of HTTP.

`Fabric` is made of those parts; they share its state, configuration, shell and network lock."""

import threading
from pathlib import Path

from .faults import Faults
from .network import Network
from .shell import Shell
from .storagebox import Box
from .vm import VMs


class Fabric(VMs, Network, Faults):
    def __init__(self, cfg: dict, state, root: Path, shell: Shell | None = None):
        self.cfg = cfg
        self.state = state
        self.root = root
        self.sh = shell or Shell()
        self.box = Box(self.sh)   # the Storage Box
        self.lab_key = root / "id_ed25519"
        # tc and nft changes of concurrent actions (two servers starting) must not interleave
        self.net_lock = threading.RLock()
        self._local: tuple[float, set[str]] | None = None
