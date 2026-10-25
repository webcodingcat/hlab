"""VMs: a server's disk, its QEMU process and its console.

Part of Fabric (see __init__.py).
"""

import logging
import shutil
from pathlib import Path

from .network import link_local, public_mac

log = logging.getLogger("hlabd.vm")


class VMs:
    """Fabric mixin for server VMs."""

    def vm_dir(self, server: dict) -> Path:
        """Return the directory of a server's VM."""
        return self.root / "vm" / str(server["id"])

    def create_vm(self, server: dict, base: str) -> None:
        """Create the disk of a new server: an overlay on a system image.

        Everything else comes from the metadata service at boot, as on Hetzner
        (cloud-init's Hetzner datasource).
        """
        directory = self.vm_dir(server)
        directory.mkdir(parents=True, exist_ok=True)
        # the server's disk size: a type change without upgrade_disk kept the smaller
        # one
        size = (
            server["_lab"].get("disk_gb")
            or self.cfg["server_types"][server["server_type"]]["disk_gb"]
        )
        self.sh.run("vm-disk", str(directory), base, str(size))
        server["_lab"]["disk_gb"] = size

    def start_vm(self, server: dict) -> None:
        """Start the server's VM with its taps, DHCP and volumes.

        Then the routes, faults and latency that involve it are applied. If the VM does
        not start, its taps go again.
        """
        if self.vm_alive(server):
            return
        try:
            nics = self.taps_up(server)
            self.write_volume_list(server)
            self.launch_vm(server, nics)
        except Exception:
            self.kill_vm(server)
            raise
        self.route_floating_ips(server)
        self.apply_faults()
        self.apply_latency()
        self.apply_routes()

    def taps_up(self, server: dict) -> list[str]:
        """Bring up the server's public tap and the taps of its cloud networks.

        Their routes and DHCP come with them. Returns the NICs for QEMU (tap=mac).
        """
        lab = server["_lab"]
        self.sh.run(
            "public-tap-up",
            lab["tap"],
            *([self.ipv4(server)] if self.ipv4(server) else []),
        )
        nics = [f"{lab['tap']}={public_mac(lab['index'])}"]
        for nic in lab["nics"]:
            self.nic_up(nic)
            # at boot every NIC sits on the root bus; the hot-plug ports are free again
            nic.pop("port", None)
            nics.append(f"{nic['tap']}={nic['mac']}")
        ipv6_net = self.ipv6_net(server)
        # the /64 goes to the guest's link-local address, as Hetzner routes it
        if ipv6_net:
            self.sh.run("route6", ipv6_net, lab["tap"], link_local(lab["index"]))
        self.write_dhcp()
        return nics

    def write_volume_list(self, server: dict) -> None:
        """Write the volumes the VM gets at boot: those attached whose disk exists."""
        volumes = [
            v
            for v in list(self.state["volumes"].values())
            if v.get("server") == server["id"] and v["status"] == "available"
        ]
        (self.vm_dir(server) / "volumes").write_text(
            "".join(f"{v['id']} {self.volume_file(v)}\n" for v in volumes)
        )

    def launch_vm(self, server: dict, nics: list[str]) -> None:
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

    def resize_disk(self, server: dict, gb: int) -> None:
        """Grow a server's disk to `gb` GB."""
        self.sh.run("vm-disk-resize", str(self.vm_dir(server)), str(gb))
        server["_lab"]["disk_gb"] = gb

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
            shutil.rmtree(d, True)

    def ipv4(self, server: dict) -> str | None:
        """Return the server's primary IPv4, if it has one."""
        primary_ip = self.state["primary_ips"].get(server.get("ipv4_id"))
        return primary_ip["ip"] if primary_ip else None

    def ipv6_net(self, server: dict) -> str | None:
        """Return the server's primary IPv6 network, if it has one."""
        primary_ip = self.state["primary_ips"].get(server.get("ipv6_id"))
        return primary_ip["ip"] if primary_ip else None

    def kill_vm(self, server: dict) -> None:
        """Stop the server's VM and take its taps and DHCP down."""
        log.info("VM %s (server %s) stops", server["name"], server["id"])
        lab = server["_lab"]
        self.sh.run("vm-kill", str(self.vm_dir(server)))
        # taps come and go only under the lock: tc and nft changes find what they
        # checked
        with self.net_lock:
            self.sh.run("tap-down", lab["tap"])
            for nic in lab["nics"]:
                self.sh.run("tap-down", nic["tap"])
        self.write_dhcp()  # the taps' dnsmasq instances go too

    def vm_alive(self, server: dict) -> bool:
        """Tell whether the server's VM process runs."""
        return self.sh.ok("vm-alive", str(self.vm_dir(server)))

    def delete_vm(self, server: dict) -> None:
        """Stop the server's VM and remove its directory."""
        self.kill_vm(server)
        directory = self.vm_dir(server)
        if directory.is_dir():
            for f in directory.iterdir():
                f.unlink()
            directory.rmdir()

    def qmp(self, server: dict, command: str) -> None:
        """Send a QMP command (without arguments) to the server's QEMU."""
        self.sh.run("qmp", str(self.vm_dir(server)), f'{{"execute":"{command}"}}')

    def hmp(self, server: dict, command: str) -> None:
        """Send a human monitor command to the server's QEMU."""
        self.sh.run("hmp", str(self.vm_dir(server)), command)

    def qga(self, server: dict, command: dict) -> dict | None:
        """Send a command to the QEMU guest agent; None when no agent answers."""
        if not self.sh.ok("vm-alive", str(self.vm_dir(server))):
            return None
        return self.sh.qga(self.vm_dir(server) / "qga.sock", command)

    def trim_console(self, server: dict, limit: int = 8 << 20) -> None:
        """Cut a console log above the limit down to its last MiB.

        QEMU appends, so it writes on at the new end.
        """
        path = self.vm_dir(server) / "console.log"
        try:
            if path.stat().st_size > limit:
                with path.open("r+b") as console:
                    console.seek(-(1 << 20), 2)
                    tail = console.read()
                    console.seek(0)
                    console.truncate()
                    console.write(tail)
        except OSError:
            pass
