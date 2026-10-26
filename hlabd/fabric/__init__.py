"""The work on the system, all of it done through scripts/hlab.sh.

The API decides, the fabric carries it out and knows nothing of HTTP. `Fabric` holds
its parts, each an object of its own that gets what it uses in its constructor:

| Part | Module | Uses |
|---|---|---|
| `shell` (`Shell`) | shell.py | – (runs hlab.sh, talks to guest agents) |
| `vms` (`VMs`) | vm.py | shell |
| `volumes` (`Volumes`) | volumes.py | shell, vms |
| `faults` (`Faults`) | faults.py | shell, vms (faults and latency) |
| `network` (`Network`) | network.py | shell, vms, faults |
| `firewall` (`Firewall`) | firewall.py | shell |
| `box` (`Box`) | storagebox.py | shell (the Storage Box) |

The address rules (MACs, gateways, routing tables) are plain functions in addresses.py.
Work that spans several parts (a VM's start and stop with its taps, volumes, routes
and faults) is in `Fabric`'s own methods.
"""

import threading
from pathlib import Path

from ..records import Server
from ..state import State
from .faults import Faults
from .firewall import Firewall
from .network import Network
from .shell import Shell
from .storagebox import Box
from .vm import VMs
from .volumes import Volumes


class Fabric:
    """The lab's fabric: its parts, and a VM's start and stop across them."""

    def __init__(self, cfg: dict, state: State, root: Path, shell: Shell | None = None):
        """Set up the parts on one shell and one network lock."""
        # the lab's directory: VMs, volumes, images, DHCP, nft scripts
        self.root = root
        self.lab_key = root / "id_ed25519"  # the lab's SSH key
        self.shell = shell or Shell()
        # Taps, tc and nft change only under this lock: concurrent actions (two servers
        # starting) must not interleave, and a tap checked to exist must still be there
        # when it is configured.
        net_lock = threading.RLock()
        self.vms = VMs(cfg, root, self.shell)
        self.volumes = Volumes(state, root, self.shell, self.vms)
        self.faults = Faults(cfg, state, root, self.shell, net_lock, self.vms)
        self.network = Network(
            cfg, state, root, self.shell, net_lock, self.vms, self.faults
        )
        self.firewall = Firewall(state, root, self.shell, net_lock)
        self.box = Box(self.shell)

    def start_vm(self, server: Server) -> None:
        """Start the server's VM with its taps, DHCP and volumes.

        Then the routes, faults and latency that involve it are applied. If the VM does
        not start, its taps are taken down again.
        """
        if self.vms.is_alive(server):
            return
        try:
            nics = self.network.taps_up(server)
            self.volumes.write_boot_list(server)
            self.vms.launch(server, nics)
        except Exception:  # whatever failed: remove the half-started VM, then re-raise
            self.kill_vm(server)
            raise
        self.network.reroute_floating_ips(server)
        self.faults.apply()
        self.faults.apply_latency()
        self.network.apply_routes()

    def kill_vm(self, server: Server) -> None:
        """Stop the server's VM and take its taps and DHCP down."""
        self.vms.kill(server)
        self.network.taps_down(server)

    def delete_vm(self, server: Server) -> None:
        """Stop the server's VM and remove its directory."""
        self.kill_vm(server)
        self.vms.remove_directory(server)
