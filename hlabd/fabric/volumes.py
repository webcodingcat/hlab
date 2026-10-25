"""Volumes: raw disk files, hot-plugged into a running VM as SCSI disks. Part of Fabric (see __init__.py)."""

from pathlib import Path


class Volumes:
    def volume_file(self, volume: dict) -> Path:
        return self.root / "volumes" / f"{volume['id']}.raw"

    def create_volume(self, volume: dict) -> None:
        """The disk of a new volume, formatted if asked; without it, no file stays."""
        path = self.volume_file(volume)
        path.parent.mkdir(parents=True, exist_ok=True)
        try:
            self.sh.run("volume-create", str(path), str(volume["size"]), volume.get("format") or "")
        except Exception:
            path.unlink(missing_ok=True)
            raise

    def delete_volume(self, volume: dict) -> None:
        self.volume_file(volume).unlink(missing_ok=True)

    def attach_volume(self, server: dict, volume: dict) -> None:
        """Hot-plug into the running VM (a VM that starts gets its volumes at boot)."""
        self.sh.run("volume-attach", str(self.vm_dir(server)), str(volume["id"]), str(self.volume_file(volume)))

    def detach_volume(self, server: dict, volume: dict) -> None:
        self.sh.run("volume-detach", str(self.vm_dir(server)), str(volume["id"]))

    def resize_volume(self, volume: dict, size: int, server: dict | None) -> None:
        """Grow the disk; a running VM it is attached to sees the new size at once."""
        vm_dir = str(self.vm_dir(server)) if server else ""
        self.sh.run("volume-resize", str(self.volume_file(volume)), str(size), vm_dir, str(volume["id"]))
