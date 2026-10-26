"""The lab's Storage Box: its sshd, accounts, their directories and snapshots on disk.

All of it goes through scripts/box.sh (via hlab.sh). The API (api/storagebox/) decides
what a box looks like; this part of the fabric (see __init__.py) only puts it on disk.
The methods of `Box` are named after the box.sh functions they run. The one exception
is `bytes_used`: it runs print_bytes_used and returns the number that it prints.
"""

from ..records import StorageBox
from .shell import Shell

BOX = "/hlab/box"
TB = 2**40


def username(box: StorageBox) -> str:
    """Return the account of a box (Hetzner: u<number>)."""
    return f"u{box['id']}"


def data_dir(box: StorageBox) -> str:
    """Return the data directory of a box."""
    return f"{BOX}/data/{box['id']}"


def lab_user_dir(name: str) -> str:
    """Return the directory of a lab account outside the API (/_lab/box/users)."""
    return f"{BOX}/data/hlab-{name}"


def snap_dir(box: StorageBox, name: str = "") -> str:
    """Return the snapshot directory of a box, or of one of its snapshots."""
    return f"{BOX}/snapshots/{box['id']}" + (f"/{name}" if name else "")


class Box:
    """The Storage Box's sshd, accounts and directories, through hlab.sh."""

    def __init__(self, shell: Shell):
        """Use `shell` to run the box commands."""
        self.shell = shell

    def start_box(self, size_gb: int) -> None:
        """Start the box's disk and its sshd on ports 22 and 23.

        The disk is limited to `size_gb` (0: no limit).
        """
        self.shell.run("start-box", str(size_gb))

    def stop_box(self) -> None:
        """Stop the box (failures are ignored)."""
        self.shell.run("stop-box", check=False)

    def set_account(
        self, user: str, home: str, password: str | None, quota: int, owner: int | str
    ) -> None:
        """Create or update an account with its home.

        `owner` gives the account's IDs. A number is its UID: the main account of a
        box uses the box ID, so its files keep their owner across container restarts.
        A name is the box user whose IDs a sub-account shares. Without a password, the
        account cannot log in with a password. The password goes over stdin, so it
        never shows in a process list.
        """
        self.shell.run(
            "set-account",
            user,
            home,
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

    def set_zfs_view(self, user: str, snapshots: str | None) -> None:
        """Show the snapshots as .zfs in the account's home, or none."""
        self.shell.run("set-zfs-view", user, snapshots or "-")

    def set_home(self, user: str, home: str) -> None:
        """Set an account's home directory."""
        self.shell.run("set-home", user, home)

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

    def remove_tree(self, path: str, check: bool = True) -> None:
        """Remove a directory tree on the box."""
        self.shell.run("remove-tree", path, check=check)

    def bytes_used(self, path: str) -> int:
        """Return the bytes used below a path (0 if it is missing)."""
        return int(self.shell.run("print-bytes-used", path, check=False).strip() or 0)

    def take_snapshot(self, data: str, dest: str) -> None:
        """Take a snapshot of a data directory into `dest` (owners are kept)."""
        self.shell.run("take-snapshot", data, dest)

    def roll_back(self, snapshot: str, data: str) -> None:
        """Roll a data directory back to a snapshot (owners are kept)."""
        self.shell.run("roll-back", snapshot, data)

    def list_folders(self, path: str) -> list[str]:
        """Return the folders below a path."""
        output = self.shell.run("list-folders", path, check=False)
        return [folder for folder in output.splitlines() if folder]
