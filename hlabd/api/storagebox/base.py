"""What boxes, sub-accounts and snapshots share.

This module finds a box and tells whether it is busy. It has the quota, the accounts on
disk (also those made through /_lab/box/users), and the checks of requests.
"""

import posixpath

from ...errors import invalid, locked, not_found
from ...lab import Lab
from ...records import StorageBox, Subaccount

PASSWORD_SPECIAL = set("^°!§$%/()=?+#-.,;:~*@{}_&")
PASSWORD_CHARS = (
    set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÜäöüß0123456789")
    | PASSWORD_SPECIAL
)
BUSY = "the Storage Box is busy with another action"
TB = 2**40


def username(box_id: int) -> str:
    """Return the account of a box (Hetzner: u<number>)."""
    return f"u{box_id}"


def box_busy(lab: Lab, box: StorageBox, for_delete: bool = False) -> bool:
    """Check whether the box is busy being set up, rolled back or deleted.

    For a delete, a snapshot that is still being copied and a sub-account that is
    still being set up count too. Otherwise the delete would remove their data or
    account while they are in use.
    """
    if box["status"] == "initializing" or box.get("_deleting") or box.get("_busy"):
        return True
    if not for_delete:
        return False
    state = lab.state
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

    With `ready`, the box must not be busy (Hetzner: 423 locked).
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


async def box_account(lab: Lab, box: StorageBox, password: str | None) -> None:
    """Create or update the box's main account (its UID is the box ID)."""
    await lab.blocking(
        lab.fabric.box.set_account,
        username(box["id"]),
        box["id"],
        ".",
        password,
        quota_bytes(lab, box),
        box["id"],
    )


async def sync_box(lab: Lab, box: StorageBox) -> None:
    """Apply the access settings of the box's main account and its .zfs view."""
    access = dict(box["access"])  # a copy for the thread
    user = username(box["id"])
    await lab.blocking(lab.fabric.box.set_access, user, access, quota_bytes(lab, box))
    await lab.blocking(
        lab.fabric.box.set_zfs_view, user, box["id"], bool(access["zfs_enabled"])
    )


async def sync_subaccount(
    lab: Lab, box: StorageBox, subaccount: Subaccount, password: str | None = None
) -> None:
    """Apply a sub-account's home and access settings (and its password, if given).

    With a password, the account is created if it is missing. It shares the box
    user's IDs, so the box user keeps full access to its files. A home is a path
    inside the box; it holds whatever files are there now.
    """
    user, home = subaccount["username"], subaccount["home_directory"]
    access, quota = dict(subaccount["access"]), quota_bytes(lab, box)
    if password is not None:
        await lab.blocking(
            lab.fabric.box.set_account,
            user,
            box["id"],
            home,
            password,
            quota,
            username(box["id"]),
        )
    else:
        await lab.blocking(lab.fabric.box.set_home, user, box["id"], home)
    await lab.blocking(lab.fabric.box.set_access, user, access, quota)


async def set_up_box_user(lab: Lab, name: str, public_key: str) -> None:
    """Create or update a lab account on the box, with its own directory and key.

    Such accounts exist outside the Storage Box API (/_lab/box/users). Their UID is
    fixed in the state (`box_uids`), like a box's UID, because the files outlive the
    container's /etc/passwd.
    """
    uids = lab.state.data["box_uids"]
    with lab.state.change():
        uid = uids.setdefault(name, 30000 + len(uids))
    box = lab.fabric.box
    access = {"ssh_enabled": True, "reachable_externally": True}
    await lab.blocking(box.set_lab_account, name, uid, TB)
    await lab.blocking(box.set_access, name, access, TB)
    await lab.blocking(box.add_key, name, public_key)


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

    Only known keys count. A null value leaves a setting as it is (partial updates).
    """
    if given is not None and not isinstance(given, dict):
        raise invalid("access_settings is an object", "access_settings")
    return {
        key: bool(value)
        for key, value in (given or {}).items()
        if key in known and value is not None
    }
