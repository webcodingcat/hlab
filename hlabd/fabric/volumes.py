"""Volumes: raw disk files, hot-plugged into a running VM as SCSI disks.

A part of the fabric (see __init__.py); it plugs into the VMs of vm.py. A volume is
named by its ID (`vid`), a VM by its server's ID (`sid`).
"""

from pathlib import Path

from .shell import LONG_TIMEOUT, Shell
from .vm import VMs


class Volumes:
    """The volumes' disk files and their place in the VMs."""

    def __init__(self, root: Path, shell: Shell, vms: VMs):
        """Keep the lab's directory, the shell and the VMs to plug into."""
        self.root = root
        self.shell = shell
        self.vms = vms

    def path(self, vid: int) -> Path:
        """Return the path of a volume's raw disk file."""
        return self.root / "volumes" / f"{vid}.raw"

    def create(self, vid: int, size: int, fs_format: str | None) -> None:
        """Create the disk of a new volume (`size` GB), formatted if asked.

        If creating it fails, no file stays.
        """
        path = self.path(vid)
        path.parent.mkdir(parents=True, exist_ok=True)
        try:
            self.shell.run(
                "create-volume",
                str(path),
                str(size),
                fs_format or "",
                timeout=LONG_TIMEOUT,  # mkfs on a big volume
            )
        except Exception:  # whatever failed: remove the half-made file, then re-raise
            path.unlink(missing_ok=True)
            raise

    def delete(self, vid: int) -> None:
        """Delete a volume's disk file."""
        self.path(vid).unlink(missing_ok=True)

    def attach(self, sid: int, vid: int) -> None:
        """Hot-plug a volume into the running VM.

        A VM that starts gets its volumes at boot (write_boot_list).
        """
        self.shell.run(
            "attach-volume",
            str(self.vms.directory(sid)),
            str(vid),
            str(self.path(vid)),
        )

    def detach(self, sid: int, vid: int) -> None:
        """Hot-unplug a volume from the running VM."""
        self.shell.run("detach-volume", str(self.vms.directory(sid)), str(vid))

    def resize(self, vid: int, size: int, sid: int | None) -> None:
        """Grow a volume's disk.

        If the volume is attached to a running VM (`sid`), the VM sees the new size at
        once.
        """
        vm_dir = str(self.vms.directory(sid)) if sid is not None else ""
        self.shell.run(
            "resize-volume",
            str(self.path(vid)),
            str(size),
            vm_dir,
            str(vid),
        )

    def write_boot_list(self, sid: int, vids: list[int]) -> None:
        """Write the volumes the VM gets at boot (the API picks them: boot_volumes)."""
        (self.vms.directory(sid) / "volumes").write_text(
            "".join(f"{vid} {self.path(vid)}\n" for vid in vids)
        )
