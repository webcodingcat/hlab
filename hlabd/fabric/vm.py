"""VMs: a server's disk, its QEMU process and its console. Part of Fabric (see __init__.py)."""

import logging
from pathlib import Path

from .network import link_local, public_mac

log = logging.getLogger("hlabd.vm")


class VMs:
    def vm_dir(self, server: dict) -> Path:
        return self.root / "vm" / str(server["id"])

    def create_vm(self, server: dict, base: str) -> None:
        """The disk of a new server: an overlay on a system image. Everything else comes from the metadata service
        at boot, as on Hetzner (cloud-init's Hetzner datasource)."""
        d = self.vm_dir(server)
        d.mkdir(parents=True, exist_ok=True)
        # the server's disk size: a type change without upgrade_disk kept the smaller one
        size = server["_lab"].get("disk_gb") or self.cfg["server_types"][server["server_type"]]["disk_gb"]
        self.sh.run("vm-disk", str(d), base, str(size))
        server["_lab"]["disk_gb"] = size

    def start_vm(self, server: dict) -> None:
        lab = server["_lab"]
        d = self.vm_dir(server)
        if self.sh.ok("vm-alive", str(d)):
            return
        self.sh.run("public-tap-up", lab["tap"], *([self.ipv4(server)] if self.ipv4(server) else []))
        nics = [f"{lab['tap']}={public_mac(lab['index'])}"]
        for nic in lab["nics"]:
            self._private_tap_up(nic)
            nic.pop("port", None)  # at boot every NIC sits on the root bus; the hot-plug ports are free again
            nics.append(f"{nic['tap']}={nic['mac']}")
        v6 = self.ipv6_net(server)
        if v6:   # the /64 goes to the guest's link-local address, as Hetzner routes it
            self.sh.run("route6", v6, lab["tap"], link_local(lab["index"]))
        self.write_dhcp()
        vols = [v for v in list(self.state["volumes"].values())   # only those whose disk exists
                if v.get("server") == server["id"] and v["status"] == "available"]
        (d / "volumes").write_text("".join(f"{v['id']} {self.root / 'volumes' / (str(v['id']) + '.raw')}\n"
                                           for v in vols))
        stype = self.cfg["server_types"][server["server_type"]]
        ram = max(512, int(stype["memory_gb"] * 1024 * self.cfg["memory_scale"]))
        log.info("VM %s (server %s) starts: %s MB, %s cores", server["name"], server["id"], ram, stype["cores"])
        self.sh.run("vm-start", str(d), server["name"], str(ram), str(stype["cores"]), str(server["id"]), *nics)
        self.route_floating_ips(server)
        self.apply_faults()
        self.apply_latency()
        self.apply_routes()

    def ipv4(self, server: dict) -> str | None:
        p = self.state["primary_ips"].get(server.get("ipv4_id"))
        return p["ip"] if p else None

    def ipv6_net(self, server: dict) -> str | None:
        p = self.state["primary_ips"].get(server.get("ipv6_id"))
        return p["ip"] if p else None

    def kill_vm(self, server: dict) -> None:
        log.info("VM %s (server %s) stops", server["name"], server["id"])
        lab = server["_lab"]
        self.sh.run("vm-kill", str(self.vm_dir(server)))
        self.sh.run("tap-down", lab["tap"])
        for nic in lab["nics"]:
            self.sh.run("tap-down", nic["tap"])
        self.write_dhcp()   # the taps' dnsmasq instances go too

    def vm_alive(self, server: dict) -> bool:
        return self.sh.ok("vm-alive", str(self.vm_dir(server)))

    def delete_vm(self, server: dict) -> None:
        self.kill_vm(server)
        d = self.vm_dir(server)
        if d.is_dir():
            for f in d.iterdir():
                f.unlink()
            d.rmdir()

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
        p = self.vm_dir(server) / "console.log"
        try:
            if p.stat().st_size > limit:
                with p.open("r+b") as f:
                    f.seek(-(1 << 20), 2)
                    tail = f.read()
                    f.seek(0)
                    f.truncate()
                    f.write(tail)
        except OSError:
            pass
