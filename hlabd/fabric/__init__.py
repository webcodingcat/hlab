"""The work on the system: VMs, taps, routes, nftables, tc, DHCP, DNS, the Storage Box.

Most of it runs as verbs of scripts/hlab.sh. The lab's own files under its directory
are handled here in Python: VM directories, console logs, volume files, and the files
a verb then loads (nft scripts, dnsmasq and DNS configuration).

The fabric gets plain data (mostly the dataclasses of its parts, computed by
api/desired.py) and never sees the state; DEVELOPING.md (The pieces, Where code runs)
has the rules.

`Fabric` holds its parts. Each part is an object that gets what it uses in its
constructor:

| Part | Module | Uses |
|---|---|---|
| `shell` (`Shell`) | shell.py | – (runs hlab.sh, talks to guest agents) |
| `vms` (`VMs`) | vm.py | shell |
| `volumes` (`Volumes`) | volumes.py | shell, vms |
| `faults` (`Faults`) | faults.py | shell, vms (faults and latency) |
| `network` (`Network`) | network.py | shell, vms |
| `firewall` (`Firewall`) | firewall.py | shell |
| `box` (`Box`) | storagebox.py | shell (the Storage Box) |
| `dns` (`Dns`) | dns.py | shell (the lab resolver, NSD for the DNS zones) |

The address rules (MACs, gateways, routing tables) are plain functions in addresses.py.
Work that spans several parts, such as a VM's start and stop with its taps, volumes,
routes and faults, is put together by the API (api/system.py).
"""

import threading
from pathlib import Path

from .dns import Dns
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
        # Taps are torn down, and nft, tc and dnsmasq are changed, only while this
        # lock is held. The fabric's methods run in threads side by side (for example
        # when two servers start), and these changes must not interleave. A tap that
        # was checked to exist must still be there when it is configured. Putting the
        # newest picture of the system in place last is the API's job
        # (`Lab.picture_lock`, api/system.py).
        self.net_lock = threading.RLock()
        self.vms = VMs(cfg, root, self.shell)
        self.volumes = Volumes(root, self.shell, self.vms)
        self.faults = Faults(root, self.shell, self.net_lock, self.vms)
        self.network = Network(cfg, root, self.shell, self.net_lock, self.vms)
        self.firewall = Firewall(root, self.shell, self.net_lock)
        self.box = Box(self.shell)
        self.dns = Dns(root, self.shell)
