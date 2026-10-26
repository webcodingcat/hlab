"""VMs: a server's disk, its QEMU process and its console, and the system images.

A part of the fabric (see __init__.py). Its taps belong to network.py; a server's
whole start and stop, taps included, is in `Fabric.start_vm` and `Fabric.kill_vm`.
"""

import logging
import shutil
from pathlib import Path

from ..records import Server
from .shell import Shell

log = logging.getLogger(__name__)

# a console log above this size is cut down to its last MiB
CONSOLE_LIMIT = 8 << 20


class VMs:
    """The servers' VMs: disks, QEMU processes, monitors, guest agents, consoles."""

    def __init__(self, cfg: dict, root: Path, shell: Shell):
        """Keep the server types (`cfg`), the lab's directory and the shell."""
        self.cfg = cfg
        self.root = root
        self.shell = shell

    def directory(self, server: Server) -> Path:
        """Return the directory of a server's VM."""
        return self.root / "vm" / str(server["id"])

    def is_alive(self, server: Server) -> bool:
        """Tell whether the server's VM process runs."""
        return self.shell.ok("is-vm-alive", str(self.directory(server)))

    def create_disk(self, server: Server, base: str, size_gb: int) -> None:
        """Create the disk of a new server: an overlay on a system image, `size_gb` big.

        Everything else comes from the metadata service at boot, as on Hetzner
        (cloud-init's Hetzner datasource).
        """
        directory = self.directory(server)
        directory.mkdir(parents=True, exist_ok=True)
        self.shell.run("create-vm-disk", str(directory), base, str(size_gb))

    def resize_disk(self, server: Server, gb: int) -> None:
        """Grow a server's disk to `gb` GB."""
        self.shell.run("resize-vm-disk", str(self.directory(server)), str(gb))

    def fetch_image(self, name: str, url: str, checksums: str) -> str:
        """Return the file of a system image, downloaded unless it is there.

        It is the version that the image's checksum file names now.
        """
        return self.shell.run(
            "fetch-image", name, url, checksums, str(self.root / "images" / "system")
        ).strip()

    def launch(
        self, server: Server, name: str, type_name: str, nics: list[str]
    ) -> None:
        """Start the QEMU process with its NICs (tap=mac), sized by the server type.

        Memory is scaled by memory_scale.
        """
        server_type = self.cfg["server_types"][type_name]
        ram = max(512, int(server_type["memory_gb"] * 1024 * self.cfg["memory_scale"]))
        log.info(
            "VM %s (server %s) starts: %s MB, %s cores",
            name,
            server["id"],
            ram,
            server_type["cores"],
        )
        self.shell.run(
            "start-vm",
            str(self.directory(server)),
            name,
            str(ram),
            str(server_type["cores"]),
            str(server["id"]),
            *nics,
        )

    def kill(self, server: Server, name: str) -> None:
        """Kill the server's QEMU process (its taps stay)."""
        log.info("VM %s (server %s) stops", name, server["id"])
        self.shell.run("kill-vm", str(self.directory(server)))

    def kill_all(self) -> None:
        """Kill every VM process and remove every VM directory (a lab reset).

        This covers VMs known to the state or not.
        """
        for directory in (self.root / "vm").glob("*"):
            self.shell.run("kill-vm", str(directory), check=False)
            shutil.rmtree(directory, ignore_errors=True)

    def remove_directory(self, server: Server) -> None:
        """Remove the directory of a server's VM, its disk included."""
        shutil.rmtree(self.directory(server), ignore_errors=True)

    def qmp(self, server: Server, command: str) -> None:
        """Send a QMP command (without arguments) to the server's QEMU."""
        self.shell.run(
            "send-qmp", str(self.directory(server)), f'{{"execute":"{command}"}}'
        )

    def hmp(self, server: Server, command: str) -> None:
        """Send a human monitor command to the server's QEMU."""
        self.shell.run("send-hmp", str(self.directory(server)), command)

    def press_power_button(self, server: Server) -> None:
        """Press the VM's ACPI power button (the guest shuts down if it listens)."""
        self.qmp(server, "system_powerdown")

    def send_ctrl_alt_delete(self, server: Server) -> None:
        """Send ctrl-alt-delete to the VM (the guest reboots itself)."""
        self.hmp(server, "sendkey ctrl-alt-delete")

    def reset(self, server: Server) -> None:
        """Reset the VM like its reset button (hard)."""
        self.qmp(server, "system_reset")

    def pause(self, server: Server) -> None:
        """Freeze the VM (its clock and processes stop until resume)."""
        self.qmp(server, "stop")

    def resume(self, server: Server) -> None:
        """Let a paused VM run again."""
        self.qmp(server, "cont")

    def qga(self, server: Server, command: dict) -> dict | None:
        """Send a command to the QEMU guest agent; None when no agent answers."""
        if not self.is_alive(server):
            return None
        return self.shell.qga(self.directory(server) / "qga.sock", command)

    def trim_console(self, server: Server) -> None:
        """Cut a console log above CONSOLE_LIMIT down to its last MiB.

        QEMU opens the log for appending, so it keeps writing at the new end.
        """
        path = self.directory(server) / "console.log"
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
