"""The work on the system, all of it done through scripts/hlab.sh.

The API decides, the fabric carries it out and knows nothing of HTTP. It never sees the
state: the API computes from the state what should be (api/desired.py) and hands it to
the fabric's methods as plain data (the dataclasses of the parts); records handed in
(a server, a volume, a NIC) are read for what never changes once they exist (ID, name,
tap, MAC). `Fabric` holds its parts, each an object of its own that gets what it uses in
its constructor:

| Part | Module | Uses |
|---|---|---|
| `shell` (`Shell`) | shell.py | – (runs hlab.sh, talks to guest agents) |
| `vms` (`VMs`) | vm.py | shell |
| `volumes` (`Volumes`) | volumes.py | shell, vms |
| `faults` (`Faults`) | faults.py | shell, vms (faults and latency) |
| `network` (`Network`) | network.py | shell, vms |
| `firewall` (`Firewall`) | firewall.py | shell |
| `box` (`Box`) | storagebox.py | shell (the Storage Box) |

The address rules (MACs, gateways, routing tables) are plain functions in addresses.py.
Work that spans several parts (a VM's start and stop with its taps, volumes, routes
and faults) is put together by the API (api/system.py).
"""

import threading
from pathlib import Path

from .faults import Faults
from .firewall import Firewall
from .network import Network
from .shell import Shell
from .storagebox import Box
from .vm import VMs
from .volumes import Volumes


class Fabric:
    """The lab's fabric: its parts, on one shell and one network lock."""

    def __init__(self, cfg: dict, root: Path, shell: Shell | None = None):
        """Set up the parts on one shell and one network lock."""
        # the lab's directory: VMs, volumes, images, DHCP, nft scripts
        self.root = root
        self.lab_key = root / "id_ed25519"  # the lab's SSH key
        self.shell = shell or Shell()
        # Taps, tc and nft change only under this lock: the fabric's methods run in
        # threads side by side (two servers starting), they must not interleave, and a
        # tap checked to exist must still be there when it is configured. That the
        # newest picture of the system is put in place last is the API's job
        # (`Lab.picture_lock`, api/system.py).
        self.net_lock = threading.RLock()
        self.vms = VMs(cfg, root, self.shell)
        self.volumes = Volumes(root, self.shell, self.vms)
        self.faults = Faults(root, self.shell, self.net_lock, self.vms)
        self.network = Network(cfg, root, self.shell, self.net_lock, self.vms)
        self.firewall = Firewall(root, self.shell, self.net_lock)
        self.box = Box(self.shell)
