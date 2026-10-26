"""VMs: a server's disk, its QEMU process and its console.

Part of Fabric (see __init__.py).
"""

import logging
import shutil
from pathlib import Path

from ..records import Server
from ..state import primary_address_of
from .base import FabricBase
from .network import link_local, public_mac

log = logging.getLogger(__name__)

# a console log above this size is cut down to its last MiB
CONSOLE_LIMIT = 8 << 20


class VMs(FabricBase):
    """Fabric mixin for server VMs."""

    def vm_dir(self, server: Server) -> Path:
        """Return the directory of a server's VM."""
        return self.root / "vm" / str(server["id"])

    def create_vm(self, server: Server, base: str) -> None:
        """Create the disk of a new server: an overlay on a system image.

        Everything else comes from the metadata service at boot, as on Hetzner
        (cloud-init's Hetzner datasource).
        """
        directory = self.vm_dir(server)
        directory.mkdir(parents=True, exist_ok=True)
        # a type change without upgrade_disk keeps the smaller disk
        size = server["_lab"]["disk_gb"]
        self.sh.run("vm-disk", str(directory), base, str(size))

    def start_vm(self, server: Server) -> None:
        """Start the server's VM with its taps, DHCP and volumes.

        Then the routes, faults and latency that involve it are applied. If the VM does
        not start, its taps are taken down again.
        """
        if self.vm_alive(server):
            return
        try:
            nics = self.taps_up(server)
            self.write_volume_list(server)
            self.launch_vm(server, nics)
        except Exception:  # whatever failed: no half-started VM stays, then re-raise
            self.kill_vm(server)
            raise
        self.reroute_floating_ips(server)
        self.apply_faults()
        self.apply_latency()
        self.apply_routes()

    def taps_up(self, server: Server) -> list[str]:
        """Bring up the server's public tap and the taps of its cloud networks.

        Their routes and DHCP come with them. Returns the NICs for QEMU (tap=mac).
        """
        server_lab = server["_lab"]
        tap, index = server_lab["tap"], server_lab["index"]
        ipv4 = primary_address_of(self.state, server, "ipv4")
        self.sh.run("public-tap-up", tap, *([ipv4] if ipv4 else []))
        nics = [f"{tap}={public_mac(index)}"]
        with self.state.lock:  # a copy: bringing a tap up runs a script
            server_nics = list(server_lab["nics"])
        for nic in server_nics:
            self.nic_up(nic)
            # at boot each network NIC gets its own root port (bp<n>); hp1..hp4 are free
            with self.state.lock:
                nic.pop("port", None)
            nics.append(f"{nic['tap']}={nic['mac']}")
        ipv6_net = primary_address_of(self.state, server, "ipv6")
        # the /64 is routed to the guest's link-local address, as Hetzner routes it
        if ipv6_net:
            self.sh.run("route6", ipv6_net, tap, link_local(index))
        self.write_dhcp()
        return nics

    def write_volume_list(self, server: Server) -> None:
        """Write the volumes the VM gets at boot: those attached whose disk exists."""
        with self.state.lock:
            volumes = [
                v
                for v in self.state["volumes"].values()
                if v.get("server") == server["id"] and v["status"] == "available"
            ]
        (self.vm_dir(server) / "volumes").write_text(
            "".join(f"{v['id']} {self.volume_file(v)}\n" for v in volumes)
        )

    def launch_vm(self, server: Server, nics: list[str]) -> None:
        """Start the QEMU process, sized by the server type.

        Memory is scaled by memory_scale.
        """
        server_type = self.cfg["server_types"][server["server_type"]]
        ram = max(512, int(server_type["memory_gb"] * 1024 * self.cfg["memory_scale"]))
        log.info(
            "VM %s (server %s) starts: %s MB, %s cores",
            server["name"],
            server["id"],
            ram,
            server_type["cores"],
        )
        self.sh.run(
            "vm-start",
            str(self.vm_dir(server)),
            server["name"],
            str(ram),
            str(server_type["cores"]),
            str(server["id"]),
            *nics,
        )

    def resize_disk(self, server: Server, gb: int) -> None:
        """Grow a server's disk to `gb` GB."""
        self.sh.run("vm-disk-resize", str(self.vm_dir(server)), str(gb))

    def fetch_image(self, name: str, url: str, checksums: str) -> str:
        """Return the file of a system image, downloaded unless it is there.

        The image is in the version its checksum file names now.
        """
        return self.sh.run(
            "image-fetch", name, url, checksums, str(self.root / "images" / "system")
        ).strip()

    def kill_all_vms(self) -> None:
        """Kill every VM process and remove every VM directory (a lab reset).

        This covers VMs known to the state or not.
        """
        for d in (self.root / "vm").glob("*"):
            self.sh.run("vm-kill", str(d), check=False)
            shutil.rmtree(d, ignore_errors=True)

    def kill_vm(self, server: Server) -> None:
        """Stop the server's VM and take its taps and DHCP down."""
        log.info("VM %s (server %s) stops", server["name"], server["id"])
        server_lab = server["_lab"]
        self.sh.run("vm-kill", str(self.vm_dir(server)))
        # taps change only under the lock, so tc and nft find the taps they checked
        with self.net_lock:
            self.sh.run("tap-down", server_lab["tap"])
            for nic in server_lab["nics"]:
                self.sh.run("tap-down", nic["tap"])
        self.write_dhcp()  # the taps' dnsmasq instances are stopped too

    def vm_alive(self, server: Server) -> bool:
        """Tell whether the server's VM process runs."""
        return self.sh.ok("vm-alive", str(self.vm_dir(server)))

    def delete_vm(self, server: Server) -> None:
        """Stop the server's VM and remove its directory."""
        self.kill_vm(server)
        shutil.rmtree(self.vm_dir(server), ignore_errors=True)

    def qmp(self, server: Server, command: str) -> None:
        """Send a QMP command (without arguments) to the server's QEMU."""
        self.sh.run("qmp", str(self.vm_dir(server)), f'{{"execute":"{command}"}}')

    def hmp(self, server: Server, command: str) -> None:
        """Send a human monitor command to the server's QEMU."""
        self.sh.run("hmp", str(self.vm_dir(server)), command)

    def press_power_button(self, server: Server) -> None:
        """Press the VM's ACPI power button (the guest shuts down if it listens)."""
        self.qmp(server, "system_powerdown")

    def send_ctrl_alt_delete(self, server: Server) -> None:
        """Send ctrl-alt-delete to the VM (the guest reboots itself)."""
        self.hmp(server, "sendkey ctrl-alt-delete")

    def reset_vm(self, server: Server) -> None:
        """Reset the VM like its reset button (hard)."""
        self.qmp(server, "system_reset")

    def pause_vm(self, server: Server) -> None:
        """Freeze the VM (its clock and processes stop until continue_vm)."""
        self.qmp(server, "stop")

    def continue_vm(self, server: Server) -> None:
        """Let a paused VM run on."""
        self.qmp(server, "cont")

    def qga(self, server: Server, command: dict) -> dict | None:
        """Send a command to the QEMU guest agent; None when no agent answers."""
        if not self.vm_alive(server):
            return None
        return self.sh.qga(self.vm_dir(server) / "qga.sock", command)

    def trim_console(self, server: Server) -> None:
        """Cut a console log above CONSOLE_LIMIT down to its last MiB.

        QEMU appends, so it writes on at the new end.
        """
        path = self.vm_dir(server) / "console.log"
        try:
            if path.stat().st_size > CONSOLE_LIMIT:
                with path.open("r+b") as console:
                    console.seek(-(1 << 20), 2)
                    tail = console.read()
                    console.seek(0)
                    console.truncate()
                    console.write(tail)
        except OSError:
            pass
