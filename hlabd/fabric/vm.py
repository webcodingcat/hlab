"""VMs: a server's disk, its QEMU process and its console. Part of Fabric (see __init__.py)."""

import logging
import shutil
from pathlib import Path

from .network import link_local, public_mac

log = logging.getLogger("hlabd.vm")


class VMs:
    def vm_dir(self, server: dict) -> Path:
        return self.root / "vm" / str(server["id"])

    def create_vm(self, server: dict, base: str) -> None:
        """The disk of a new server: an overlay on a system image. Everything else comes from the metadata service
        at boot, as on Hetzner (cloud-init's Hetzner datasource)."""
        directory = self.vm_dir(server)
        directory.mkdir(parents=True, exist_ok=True)
        # the server's disk size: a type change without upgrade_disk kept the smaller one
        size = server["_lab"].get("disk_gb") or self.cfg["server_types"][server["server_type"]]["disk_gb"]
        self.sh.run("vm-disk", str(directory), base, str(size))
        server["_lab"]["disk_gb"] = size

    def start_vm(self, server: dict) -> None:
        """The server's VM with its taps, DHCP and volumes; then the routes, faults and latency that involve it.
        If the VM does not start, its taps go again."""
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
        """The server's public tap and the taps of its cloud networks, with their routes and DHCP; returns the
        NICs for QEMU (tap=mac)."""
        lab = server["_lab"]
        self.sh.run("public-tap-up", lab["tap"], *([self.ipv4(server)] if self.ipv4(server) else []))
        nics = [f"{lab['tap']}={public_mac(lab['index'])}"]
        for nic in lab["nics"]:
            self.nic_up(nic)
            nic.pop("port", None)  # at boot every NIC sits on the root bus; the hot-plug ports are free again
            nics.append(f"{nic['tap']}={nic['mac']}")
        ipv6_net = self.ipv6_net(server)
        if ipv6_net:   # the /64 goes to the guest's link-local address, as Hetzner routes it
            self.sh.run("route6", ipv6_net, lab["tap"], link_local(lab["index"]))
        self.write_dhcp()
        return nics

    def write_volume_list(self, server: dict) -> None:
        """The volumes the VM gets at boot: those attached whose disk exists."""
        volumes = [v for v in list(self.state["volumes"].values())
                   if v.get("server") == server["id"] and v["status"] == "available"]
        (self.vm_dir(server) / "volumes").write_text("".join(f"{v['id']} {self.volume_file(v)}\n" for v in volumes))

    def launch_vm(self, server: dict, nics: list[str]) -> None:
        """The QEMU process, sized by the server type (memory scaled by memory_scale)."""
        server_type = self.cfg["server_types"][server["server_type"]]
        ram = max(512, int(server_type["memory_gb"] * 1024 * self.cfg["memory_scale"]))
        log.info("VM %s (server %s) starts: %s MB, %s cores", server["name"], server["id"], ram, server_type["cores"])
        self.sh.run("vm-start", str(self.vm_dir(server)), server["name"], str(ram), str(server_type["cores"]),
                    str(server["id"]), *nics)

    def resize_disk(self, server: dict, gb: int) -> None:
        self.sh.run("vm-disk-resize", str(self.vm_dir(server)), str(gb))
        server["_lab"]["disk_gb"] = gb

    def fetch_image(self, name: str, url: str, checksums: str) -> str:
        """A system image in the version its checksum file names now (downloaded unless it is there): its file."""
        return self.sh.run("image-fetch", name, url, checksums, str(self.root / "images" / "system")).strip()

    def kill_all_vms(self) -> None:
        """Every VM process and directory there is, known to the state or not (a lab reset)."""
        for d in (self.root / "vm").glob("*"):
            self.sh.run("vm-kill", str(d), check=False)
            shutil.rmtree(d, True)

    def ipv4(self, server: dict) -> str | None:
        primary_ip = self.state["primary_ips"].get(server.get("ipv4_id"))
        return primary_ip["ip"] if primary_ip else None

    def ipv6_net(self, server: dict) -> str | None:
        primary_ip = self.state["primary_ips"].get(server.get("ipv6_id"))
        return primary_ip["ip"] if primary_ip else None

    def kill_vm(self, server: dict) -> None:
        log.info("VM %s (server %s) stops", server["name"], server["id"])
        lab = server["_lab"]
        self.sh.run("vm-kill", str(self.vm_dir(server)))
        with self.net_lock:   # taps come and go only under the lock: tc and nft changes find what they checked
            self.sh.run("tap-down", lab["tap"])
            for nic in lab["nics"]:
                self.sh.run("tap-down", nic["tap"])
        self.write_dhcp()   # the taps' dnsmasq instances go too

    def vm_alive(self, server: dict) -> bool:
        return self.sh.ok("vm-alive", str(self.vm_dir(server)))

    def delete_vm(self, server: dict) -> None:
        self.kill_vm(server)
        directory = self.vm_dir(server)
        if directory.is_dir():
            for f in directory.iterdir():
                f.unlink()
            directory.rmdir()

    def qmp(self, server: dict, command: str) -> None:
        self.sh.run("qmp", str(self.vm_dir(server)), f'{{"execute":"{command}"}}')

    def hmp(self, server: dict, command: str) -> None:
        self.sh.run("hmp", str(self.vm_dir(server)), command)

    def qga(self, server: dict, command: dict) -> dict | None:
        """A command to the QEMU guest agent; None when no agent answers."""
        if not self.sh.ok("vm-alive", str(self.vm_dir(server))):
            return None
        return self.sh.qga(self.vm_dir(server) / "qga.sock", command)

    def trim_console(self, server: dict, limit: int = 8 << 20) -> None:
        """A console log above the limit keeps its last MiB (QEMU appends, so it writes on at the new end)."""
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
