"""The lab's Storage Box: its sshd, accounts, their directories and snapshots on disk.

All of it goes through scripts/box.sh (via hlab.sh). The API (api/storagebox/) decides
what a box looks like and names its accounts and snapshots; this part of the fabric
(see __init__.py) only puts them on disk. It takes box IDs and names: the layout on
disk (data/<box>/, snapshots/<box>/<name>/) is known only here and in box.sh.
"""

import posixpath

from .shell import LONG_TIMEOUT, Shell

BOX = "/hlab/box"


def data_dir(box_id: int, path: str = ".") -> str:
    """Return the data directory of a box, or a directory inside it.

    The API has checked that `path` stays inside the box.
    """
    return posixpath.normpath(f"{BOX}/data/{box_id}/{path.lstrip('/')}")


def snapshot_dir(box_id: int, name: str = "") -> str:
    """Return the snapshot directory of a box, or of one of its snapshots."""
    return f"{BOX}/snapshots/{box_id}" + (f"/{name}" if name else "")


class Box:
    """The Storage Box's sshd, accounts and directories, through hlab.sh."""

    def __init__(self, shell: Shell):
        """Use `shell` to run the box commands."""
        self.shell = shell

    def start_box(self, size_gb: int) -> None:
        """Start the box's disk and its sshd on ports 22 and 23.

        The disk is limited to `size_gb` (0: no limit).
        """
        self.shell.run("start-box", str(size_gb), timeout=LONG_TIMEOUT)  # mkfs

    def stop_box(self) -> None:
        """Stop the box (failures are ignored)."""
        self.shell.run("stop-box", check=False)

    def set_account(
        self,
        user: str,
        box_id: int,
        home: str,
        password: str | None,
        quota: int,
        owner: int | str,
    ) -> None:
        """Create or update an account with its home, a directory inside the box.

        `owner` gives the account's IDs. A number is its UID: the main account of a
        box uses the box ID, so its files keep their owner across container restarts.
        A name is the box user whose IDs a sub-account shares. Without a password, the
        account cannot log in with a password.
        """
        self._set_account(user, data_dir(box_id, home), password, quota, owner)

    def set_lab_account(self, name: str, uid: int, quota: int) -> None:
        """Create or update a lab account outside the API (/_lab/box/users).

        It has its own directory next to the boxes' and no password.
        """
        self._set_account(name, f"{BOX}/data/hlab-{name}", None, quota, uid)

    def _set_account(
        self, user: str, path: str, password: str | None, quota: int, owner: int | str
    ) -> None:
        """Run set-account; the password goes over stdin, never in a process list."""
        self.shell.run(
            "set-account",
            user,
            path,
            str(quota),
            str(owner),
            stdin_text=(password or "") + "\n",
        )

    def set_access(self, user: str, access: dict, quota: int) -> None:
        """Set an account's access (SSH, external reach, read-only) and quota."""
        self.shell.run(
            "set-access",
            user,
            str(int(access["ssh_enabled"])),
            str(int(access["reachable_externally"])),
            str(int(access.get("readonly", False))),
            str(quota),
        )

    def set_zfs_view(self, user: str, box_id: int, shown: bool) -> None:
        """Show the box's snapshots as .zfs in the account's home, or hide them."""
        self.shell.run("set-zfs-view", user, snapshot_dir(box_id) if shown else "-")

    def set_home(self, user: str, box_id: int, home: str) -> None:
        """Set an account's home, a directory inside the box."""
        self.shell.run("set-home", user, data_dir(box_id, home))

    def write_authorized_keys(self, user: str, keys: list[str]) -> None:
        """Set an account's authorized SSH keys (its .ssh/authorized_keys)."""
        self.shell.run("write-authorized-keys", user, "\n".join(keys))

    def add_key(self, user: str, public_key: str) -> None:
        """Add a lab key (/hlab/box-keys) to an account.

        It works in addition to the account's .ssh/authorized_keys.
        """
        self.shell.run("add-key", user, public_key.strip())

    def remove_account(self, user: str, check: bool = True) -> None:
        """Remove an account (not its data)."""
        self.shell.run("remove-account", user, check=check)

    def remove_box_data(self, box_id: int, check: bool = True) -> None:
        """Remove a box's data and all its snapshots."""
        for path in (data_dir(box_id), snapshot_dir(box_id)):
            self.shell.run("remove-tree", path, check=check, timeout=LONG_TIMEOUT)

    def remove_snapshot(self, box_id: int, name: str, check: bool = True) -> None:
        """Remove the copy of a snapshot."""
        self.shell.run(
            "remove-tree", snapshot_dir(box_id, name), check=check, timeout=LONG_TIMEOUT
        )

    def clear_snapshot_work(self) -> None:
        """Remove the copies of snapshots that were never finished (errors ignored)."""
        self.shell.run(
            "remove-tree", f"{BOX}/snapshots/.work", check=False, timeout=LONG_TIMEOUT
        )

    def data_bytes(self, box_id: int) -> int:
        """Return the bytes a box's data uses."""
        return self._bytes_used(data_dir(box_id))

    def snapshot_bytes(self, box_id: int, name: str = "") -> int:
        """Return the bytes a box's snapshots use, or one of them."""
        return self._bytes_used(snapshot_dir(box_id, name))

    def _bytes_used(self, path: str) -> int:
        """Run print-bytes-used and return its number (0 if the path is missing)."""
        output = self.shell.run(
            "print-bytes-used", path, check=False, timeout=LONG_TIMEOUT
        )
        return int(output.strip() or 0)

    def take_snapshot(self, box_id: int, name: str) -> None:
        """Take a snapshot of a box's data under a name (owners are kept)."""
        self.shell.run(
            "take-snapshot",
            data_dir(box_id),
            snapshot_dir(box_id, name),
            timeout=LONG_TIMEOUT,
        )

    def roll_back(self, box_id: int, name: str) -> None:
        """Roll a box's data back to one of its snapshots (owners are kept)."""
        self.shell.run(
            "roll-back",
            snapshot_dir(box_id, name),
            data_dir(box_id),
            timeout=LONG_TIMEOUT,
        )

    def list_folders(self, box_id: int, path: str) -> list[str]:
        """Return the folders below a directory inside the box."""
        output = self.shell.run("list-folders", data_dir(box_id, path), check=False)
        return [folder for folder in output.splitlines() if folder]
