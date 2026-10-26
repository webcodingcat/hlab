"""Volumes: raw disk files, hot-plugged into a running VM as SCSI disks.

A part of the fabric (see __init__.py); it plugs into the VMs of vm.py.
"""

from pathlib import Path

from ..records import Server, Volume
from ..state import State
from .shell import Shell
from .vm import VMs


class Volumes:
    """The volumes' disk files and their place in the VMs."""

    def __init__(self, state: State, root: Path, shell: Shell, vms: VMs):
        """Keep the state, the lab's directory, the shell and the VMs to plug into."""
        self.state = state
        self.root = root
        self.shell = shell
        self.vms = vms

    def path(self, volume: Volume) -> Path:
        """Return the path of a volume's raw disk file."""
        return self.root / "volumes" / f"{volume['id']}.raw"

    def create(self, volume: Volume) -> None:
        """Create the disk of a new volume, formatted if asked.

        If creating it fails, no file stays.
        """
        path = self.path(volume)
        path.parent.mkdir(parents=True, exist_ok=True)
        try:
            self.shell.run(
                "create-volume",
                str(path),
                str(volume["size"]),
                volume.get("format") or "",
            )
        except Exception:  # whatever failed: remove the half-made file, then re-raise
            path.unlink(missing_ok=True)
            raise

    def delete(self, volume: Volume) -> None:
        """Delete a volume's disk file."""
        self.path(volume).unlink(missing_ok=True)

    def attach(self, server: Server, volume: Volume) -> None:
        """Hot-plug a volume into the running VM.

        A VM that starts gets its volumes at boot (write_boot_list).
        """
        self.shell.run(
            "attach-volume",
            str(self.vms.directory(server)),
            str(volume["id"]),
            str(self.path(volume)),
        )

    def detach(self, server: Server, volume: Volume) -> None:
        """Hot-unplug a volume from the running VM."""
        self.shell.run(
            "detach-volume", str(self.vms.directory(server)), str(volume["id"])
        )

    def resize(self, volume: Volume, size: int, server: Server | None) -> None:
        """Grow a volume's disk.

        A running VM it is attached to sees the new size at once.
        """
        vm_dir = str(self.vms.directory(server)) if server else ""
        self.shell.run(
            "resize-volume",
            str(self.path(volume)),
            str(size),
            vm_dir,
            str(volume["id"]),
        )

    def write_boot_list(self, server: Server) -> None:
        """Write the volumes the VM gets at boot: those attached whose disk exists."""
        with self.state.lock:
            volumes = [
                v
                for v in self.state["volumes"].values()
                if v.get("server") == server["id"] and v["status"] == "available"
            ]
        (self.vms.directory(server) / "volumes").write_text(
            "".join(f"{v['id']} {self.path(v)}\n" for v in volumes)
        )
