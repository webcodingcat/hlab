"""What boxes, sub-accounts and snapshots share.

Finding a box, its quota, the accounts on disk, the checks of requests, finished
actions.
"""

import posixpath

from ...errors import invalid, locked, not_found
from ...fabric.storagebox import TB, data_dir, snap_dir, username

PASSWORD_CHARS = set(
    "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÜäöüß0123456789^°!§$%/()=?+#-.,;:~*@{}_&"
)
PASSWORD_SPECIAL = set("^°!§$%/()=?+#-.,;:~*@{}_&")


def box_or_404(lab, bid: int, ready: bool = False) -> dict:
    """Return a box by ID, or raise 404.

    With ready, the box must not be busy being set up, rolled back or deleted (Hetzner:
    423 locked).
    """
    box = lab.state["storage_boxes"].get(bid)
    if not box:
        raise not_found("Storage Box")
    if ready and (
        box["status"] == "initializing" or box.get("_deleting") or box.get("_busy")
    ):
        raise locked("the Storage Box is busy with another action")
    return box


def quota(lab, box: dict) -> int:
    """Return the box's size in bytes, as its type gives it."""
    return int(lab.cfg["storage_box_types"][box["type"]]["size_tb"] * TB)


def sync_box(lab, box: dict) -> None:
    """Apply the access settings of the box's main account and its .zfs view."""
    lab.fabric.box.access(username(box), box["access"], quota(lab, box))
    lab.fabric.box.zfs_view(
        username(box), snap_dir(box) if box["access"]["zfs_enabled"] else None
    )


def sub_home(box: dict, subaccount: dict) -> str:
    """Return a sub-account's home on disk.

    Homes are paths: whatever lies there now.
    """
    return posixpath.normpath(
        posixpath.join(data_dir(box), subaccount["home_directory"].lstrip("/"))
    )


def sync_subaccount(
    lab, box: dict, subaccount: dict, password: str | None = None
) -> None:
    """Apply a sub-account's home and access settings (and its password, if given)."""
    try:
        if password is not None:
            lab.fabric.box.account(
                subaccount["username"],
                sub_home(box, subaccount),
                password,
                quota(lab, box),
                username(box),
            )
        else:
            lab.fabric.box.home(subaccount["username"], sub_home(box, subaccount))
    except RuntimeError as error:
        # a symlink in the box pointing elsewhere (box.sh resolves it)
        if "leaves the Storage Box" in str(error):
            raise invalid(
                "must stay inside the Storage Box", "home_directory"
            ) from None
        raise
    lab.fabric.box.access(subaccount["username"], subaccount["access"], quota(lab, box))


def update_fields(item: dict, body: dict, keys: tuple[str, ...]) -> None:
    """Change the given fields of an item on PUT (labels null means none)."""
    for k in keys:
        if k in body:
            item[k] = (body[k] or {}) if k == "labels" else body[k]


def check_password(password, field: str = "password") -> str:
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


def check_home(path: str) -> str:
    """Return a directory inside the box, relative to its root ("." for the root)."""
    if ".." in (path or "").split("/"):
        raise invalid("must stay inside the Storage Box", "home_directory")
    normalized = posixpath.normpath("/" + (path or "").lstrip("/"))
    return normalized.lstrip("/") or "."


def access(given, known: dict) -> dict:
    """Return the access settings of a request.

    Known keys only; null leaves a setting as it is (partial updates).
    """
    if given is not None and not isinstance(given, dict):
        raise invalid("access_settings is an object", "access_settings")
    return {
        k: bool(v) for k, v in (given or {}).items() if k in known and v is not None
    }
