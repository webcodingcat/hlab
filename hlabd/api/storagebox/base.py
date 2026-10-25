"""What boxes, sub-accounts and snapshots share: finding a box, its quota, the accounts on disk, the checks of
requests, finished actions."""

import posixpath

from ...fabric.storagebox import TB, data_dir, snap_dir, username
from ..web import ApiError, invalid, not_found

PASSWORD_CHARS = set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÜäöüß0123456789^°!§$%/()=?+#-.,;:~*@{}_&")
PASSWORD_SPECIAL = set("^°!§$%/()=?+#-.,;:~*@{}_&")


def box_or_404(lab, bid: int, ready: bool = False) -> dict:
    """A box by ID; ready: it must not be busy being set up, rolled back or deleted (Hetzner: 423 locked)."""
    b = lab.state["storage_boxes"].get(bid)
    if not b:
        raise not_found("Storage Box")
    if ready and (b["status"] == "initializing" or b.get("_deleting") or b.get("_busy")):
        raise ApiError(423, "locked", "the Storage Box is busy with another action")
    return b


def quota(lab, b: dict) -> int:
    return int(lab.cfg["storage_box_types"][b["type"]]["size_tb"] * TB)


def sync_box(lab, b: dict) -> None:
    """Access settings of the box's main account and its snapshot view (.zfs)."""
    lab.fabric.box.access(username(b), b["access"], quota(lab, b))
    lab.fabric.box.zfs_view(username(b), snap_dir(b) if b["access"]["zfs_enabled"] else None)


def sub_home(b: dict, a: dict) -> str:
    """A sub-account's home on disk (homes are paths: whatever lies there now)."""
    return posixpath.normpath(posixpath.join(data_dir(b), a["home_directory"].lstrip("/")))


def sync_subaccount(lab, b: dict, a: dict, password: str | None = None) -> None:
    """A sub-account's home and access settings (and its password, if one is given)."""
    try:
        if password is not None:
            lab.fabric.box.account(a["username"], sub_home(b, a), password, quota(lab, b), username(b))
        else:
            lab.fabric.box.home(a["username"], sub_home(b, a))
    except RuntimeError as e:
        if "leaves the Storage Box" in str(e):   # a symlink in the box pointing elsewhere (box.sh resolves it)
            raise invalid("must stay inside the Storage Box", "home_directory") from None
        raise
    lab.fabric.box.access(a["username"], a["access"], quota(lab, b))


def done(lab, cmd: str, resources: list[tuple[int, str]]) -> dict:
    """An action for a change that is done at once."""
    a = lab.new_action(cmd, resources)
    lab.finish(a)
    return a


def update_fields(item: dict, bd: dict, keys: tuple[str, ...]) -> None:
    """PUT: the given fields change (labels null means none)."""
    for k in keys:
        if k in bd:
            item[k] = (bd[k] or {}) if k == "labels" else bd[k]


def check_password(pw, field: str = "password") -> str:
    """Hetzner's password policy for boxes and sub-accounts (except the check against known breaches)."""
    if not isinstance(pw, str) or not 12 <= len(pw) <= 128:
        raise invalid("must be 12 to 128 characters long", field)
    if not set(pw) <= PASSWORD_CHARS:
        raise invalid("contains characters outside the allowed set", field)
    if not (any(c.isupper() for c in pw) and any(c.islower() for c in pw) and any(c.isdigit() for c in pw)
            and set(pw) & PASSWORD_SPECIAL):
        raise invalid("needs an upper case letter, a lower case letter, a number and a special character", field)
    return pw


def check_home(path: str) -> str:
    """A directory inside the box, relative to its root ("." for the root)."""
    if ".." in (path or "").split("/"):
        raise invalid("must stay inside the Storage Box", "home_directory")
    p = posixpath.normpath("/" + (path or "").lstrip("/"))
    return p.lstrip("/") or "."


def access(given, known: dict) -> dict:
    """Access settings of a request: known keys only, null leaves a setting as it is (partial updates)."""
    if given is not None and not isinstance(given, dict):
        raise invalid("access_settings is an object", "access_settings")
    return {k: bool(v) for k, v in (given or {}).items() if k in known and v is not None}
