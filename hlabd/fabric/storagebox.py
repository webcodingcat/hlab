"""The lab's Storage Box: its sshd, accounts, their directories and snapshots on disk.

All of it goes through scripts/box.sh (via hlab.sh). The API (api/storagebox/) decides
what a box looks like; this module only carries it out.
"""

import os
import tempfile

BOX = "/hlab/box"
TB = 2**40


def username(box: dict) -> str:
    """Return the account of a box (Hetzner: u<number>)."""
    return f"u{box['id']}"


def data_dir(box: dict) -> str:
    """Return the data directory of a box."""
    return f"{BOX}/data/{box['id']}"


def snap_dir(box: dict, name: str = "") -> str:
    """Return the snapshot directory of a box, or of one of its snapshots."""
    return f"{BOX}/snapshots/{box['id']}" + (f"/{name}" if name else "")


class Box:
    """The Storage Box's sshd, accounts and directories, through hlab.sh."""

    def __init__(self, sh):
        """Use `sh` to run the box commands."""
        self.sh = sh

    def start(self, size_gb: int) -> None:
        """Start the box's disk and its sshd on ports 22 and 23.

        The disk is limited to `size_gb` (0: no limit).
        """
        self.sh.run("box-start", str(size_gb))

    def stop(self) -> None:
        """Stop the box (failures are ignored)."""
        self.sh.run("box-stop", check=False)

    def account(
        self, user: str, home: str, password: str | None, quota: int, owner: str
    ) -> None:
        """Create or update an account with its home.

        The password goes over stdin (never into a process list).
        """
        if password and password != "-":
            self.sh.run(
                "box-account",
                user,
                home,
                "@stdin",
                str(quota),
                owner,
                input=password + "\n",
            )
        else:
            self.sh.run("box-account", user, home, "-", str(quota), owner)

    def access(self, user: str, access: dict, quota: int) -> None:
        """Set an account's access (SSH, external reach, read-only) and quota."""
        self.sh.run(
            "box-access",
            user,
            str(int(access["ssh_enabled"])),
            str(int(access["reachable_externally"])),
            str(int(access.get("readonly", False))),
            str(quota),
        )

    def zfs_view(self, user: str, snapshots: str | None) -> None:
        """Show the snapshots as .zfs in the account's home, or none."""
        self.sh.run("box-zfs", user, snapshots or "-")

    def home(self, user: str, home: str) -> None:
        """Set an account's home directory."""
        self.sh.run("box-home", user, home)

    def authorized_keys(self, user: str, keys: list[str]) -> None:
        """Set an account's authorized SSH keys."""
        self.sh.run("box-authorized-keys", user, "\n".join(keys))

    def key(self, user: str, public_key_file: str) -> None:
        """Add a key of the lab's own (/hlab/box-keys) to an account.

        It sits next to the account's .ssh/authorized_keys.
        """
        self.sh.run("box-key", user, public_key_file)

    def remove(self, user: str, check: bool = True) -> None:
        """Remove an account."""
        self.sh.run("box-remove", user, check=check)

    def rmtree(self, path: str, check: bool = True) -> None:
        """Remove a directory tree on the box."""
        self.sh.run("box-rmtree", path, check=check)

    def usage(self, path: str) -> int:
        """Return the bytes used below a path (0 if it is missing)."""
        return int(self.sh.run("box-usage", path, check=False).strip() or 0)

    def snapshot(self, data: str, dest: str, owner: str) -> None:
        """Take a snapshot of a data directory into `dest`."""
        self.sh.run("box-snapshot", data, dest, owner)

    def rollback(self, snapshot: str, data: str, owner: str) -> None:
        """Roll a data directory back to a snapshot."""
        self.sh.run("box-rollback", snapshot, data, owner)

    def folders(self, path: str) -> list[str]:
        """Return the folders below a path."""
        return [
            f for f in self.sh.run("box-folders", path, check=False).splitlines() if f
        ]

    def lab_account(self, uids: dict, name: str, public_key: str) -> None:
        """Create an SFTP account outside the API, with its own directory and key.

        Such accounts are set through lab control (/_lab/box/users). Its UID is fixed
        in `uids` (part of the state), like a box's: files outlive the container's
        /etc/passwd.
        """
        if name not in uids:
            uids[name] = 30000 + len(uids)
        self.account(name, f"{BOX}/data/hlab-{name}", None, TB, str(uids[name]))
        self.access(name, {"ssh_enabled": True, "reachable_externally": True}, TB)
        with tempfile.NamedTemporaryFile("w", suffix=".pub", delete=False) as key_file:
            key_file.write(public_key.strip() + "\n")
        try:
            self.key(name, key_file.name)
        finally:
            os.unlink(key_file.name)
