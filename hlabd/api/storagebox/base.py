"""What boxes, sub-accounts and snapshots share.

Finding a box and whether it is busy, its quota, the accounts on disk (also the lab's
own, /_lab/box/users), the checks of requests.
"""

import posixpath

from ...errors import invalid, locked, not_found
from ...fabric.storagebox import TB, data_dir, lab_user_dir, snap_dir, username
from ...lab import Lab
from ...records import StorageBox, Subaccount

PASSWORD_SPECIAL = set("^°!§$%/()=?+#-.,;:~*@{}_&")
PASSWORD_CHARS = (
    set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÜäöüß0123456789")
    | PASSWORD_SPECIAL
)
BUSY = "the Storage Box is busy with another action"


def box_busy(lab: Lab, box: StorageBox, for_delete: bool = False) -> bool:
    """Check whether the box is busy being set up, rolled back or deleted.

    For a delete, a snapshot still being copied and a sub-account still being set up
    count too: their data or account would be removed under them.
    """
    if box["status"] == "initializing" or box.get("_deleting") or box.get("_busy"):
        return True
    if not for_delete:
        return False
    state = lab.state
    with state.lock:
        copying = any(
            s["box"] == box["id"] and s.get("_copying")
            for s in state["box_snapshots"].values()
        )
        creating = any(
            a["box"] == box["id"] and a.get("_creating")
            for a in state["subaccounts"].values()
        )
    return copying or creating


def box_or_404(lab: Lab, bid: int, ready: bool = False) -> StorageBox:
    """Return a box by ID, or raise 404.

    With ready, the box must not be busy (Hetzner: 423 locked).
    """
    box = lab.state["storage_boxes"].get(bid)
    if not box:
        raise not_found("Storage Box")
    if ready and box_busy(lab, box):
        raise locked(BUSY)
    return box


def quota_bytes(lab: Lab, box: StorageBox) -> int:
    """Return the box's size in bytes, as its type gives it."""
    return int(lab.cfg["storage_box_types"][box["type"]]["size_tb"] * TB)


def box_account(lab: Lab, box: StorageBox, password: str | None) -> None:
    """Create or update the box's main account (its UID is the box ID)."""
    lab.fabric.box.set_account(
        username(box), data_dir(box), password, quota_bytes(lab, box), box["id"]
    )


def sync_box(lab: Lab, box: StorageBox) -> None:
    """Apply the access settings of the box's main account and its .zfs view."""
    lab.fabric.box.set_access(username(box), box["access"], quota_bytes(lab, box))
    lab.fabric.box.set_zfs_view(
        username(box), snap_dir(box) if box["access"]["zfs_enabled"] else None
    )


def sub_home(box: StorageBox, subaccount: Subaccount) -> str:
    """Return a sub-account's home on disk.

    Homes are paths: whatever lies there now.
    """
    return posixpath.normpath(
        posixpath.join(data_dir(box), subaccount["home_directory"].lstrip("/"))
    )


def sync_subaccount(
    lab: Lab, box: StorageBox, subaccount: Subaccount, password: str | None = None
) -> None:
    """Apply a sub-account's home and access settings (and its password, if given).

    With a password the account is created if it is missing; it shares the box user's
    IDs, so the box user keeps full access to its files.
    """
    user, home = subaccount["username"], sub_home(box, subaccount)
    if password is not None:
        lab.fabric.box.set_account(
            user, home, password, quota_bytes(lab, box), username(box)
        )
    else:
        lab.fabric.box.set_home(user, home)
    lab.fabric.box.set_access(user, subaccount["access"], quota_bytes(lab, box))


def set_up_box_user(lab: Lab, name: str, public_key: str) -> None:
    """Create or update a lab account on the box, with its own directory and key.

    Such accounts live outside the Storage Box API (/_lab/box/users). The UID is fixed
    in the state (`box_uids`), like a box's: files outlive the container's /etc/passwd.
    """
    with lab.state.lock:
        uids = lab.state.data["box_uids"]
        uid = uids.setdefault(name, 30000 + len(uids))
    box = lab.fabric.box
    box.set_account(name, lab_user_dir(name), None, TB, uid)
    box.set_access(name, {"ssh_enabled": True, "reachable_externally": True}, TB)
    box.add_key(name, public_key)


def checked_password(password, field: str = "password") -> str:
    """Check a password against Hetzner's policy for boxes and sub-accounts.

    The check against known breaches is left out.
    """
    if not isinstance(password, str) or not 12 <= len(password) <= 128:
        raise invalid("must be 12 to 128 characters long", field)
    if not set(password) <= PASSWORD_CHARS:
        raise invalid("contains characters outside the allowed set", field)
    if not (
        any(c.isupper() for c in password)
        and any(c.islower() for c in password)
        and any(c.isdigit() for c in password)
        and set(password) & PASSWORD_SPECIAL
    ):
        raise invalid(
            "needs an upper case letter, a lower case letter, a number"
            " and a special character",
            field,
        )
    return password


def normalized_box_path(path: str | None, field: str) -> str:
    """Return a directory inside the box, relative to its root ("." for the root).

    A path that leaves the box is invalid input in `field`.
    """
    if ".." in (path or "").split("/"):
        raise invalid("must stay inside the Storage Box", field)
    normalized = posixpath.normpath("/" + (path or "").lstrip("/"))
    return normalized.lstrip("/") or "."


def checked_access_settings(given, known: dict) -> dict:
    """Return the access settings of a request.

    Known keys only; null leaves a setting as it is (partial updates).
    """
    if given is not None and not isinstance(given, dict):
        raise invalid("access_settings is an object", "access_settings")
    return {
        key: bool(value)
        for key, value in (given or {}).items()
        if key in known and value is not None
    }
