"""Storage Boxes: their types, the boxes themselves, their actions and folders."""

import posixpath

from fastapi import FastAPI, Request

from ...errors import check_unique_name, invalid, locked, protected
from ...fabric.storagebox import TB, data_dir, snap_dir, username
from ...lab import BOX_CREATE_SECONDS, ActionSpec, ActionTable, Lab, Plan, now
from ...records import StorageBox
from .. import hetzner
from ..actions import action_or_404, listed, touches
from ..catalogue import catalogue_entry_or_404, catalogue_name
from ..ssh_keys import ssh_fingerprint
from ..web import (
    change_delete_protection,
    filtered,
    paginate,
    read_body,
    update_resource,
)
from .base import (
    BUSY,
    box_account,
    box_busy,
    box_or_404,
    checked_access_settings,
    checked_password,
    normalized_box_path,
    quota_bytes,
    sync_box,
)
from .snapshots import rollback_snapshot


def box_type_json(lab: Lab, name: str) -> dict:
    """Return the API object of a box type."""
    cfg = lab.cfg
    entry = cfg["storage_box_types"][name]
    prices = [
        {
            "location": loc,
            "price_hourly": {"net": "0.0050", "gross": "0.0060"},
            "price_monthly": {"net": "3.2000", "gross": "3.8080"},
            "setup_fee": {"net": "0.0000", "gross": "0.0000"},
        }
        for loc in cfg["locations"]
    ]
    return {
        "id": hetzner.storage_box_type_id(lab, name),
        "name": name,
        "description": entry.get("description", name.upper()),
        "snapshot_limit": entry["snapshots"],
        "automatic_snapshot_limit": entry["snapshots"],
        "subaccounts_limit": entry["subaccounts"],
        "size": int(entry["size_tb"] * TB),
        "prices": prices,
        "deprecation": None,
    }


async def box_stats(lab: Lab, box: StorageBox) -> dict:
    """Return the box's disk usage: data, snapshots and both together."""
    data_path, snap_path = data_dir(box), snap_dir(box)
    data = await lab.blocking(lab.fabric.box.bytes_used, data_path)
    snapshots = await lab.blocking(lab.fabric.box.bytes_used, snap_path)
    return {"size": data + snapshots, "size_data": data, "size_snapshots": snapshots}


async def box_json(lab: Lab, box: StorageBox) -> dict:
    """Return the API object of a box.

    It reads the box's usage from disk first (in threads), then the record.
    """
    stats = await box_stats(lab, box)
    ready = box["status"] != "initializing"
    return {
        "id": box["id"],
        "name": box["name"],
        "storage_box_type": box_type_json(lab, box["type"]),
        "location": hetzner.location_json(lab, box["location"]),
        "access_settings": box["access"],
        "snapshot_plan": box.get("plan"),
        "protection": box["protection"],
        "labels": box["labels"],
        "status": box["status"],
        "username": username(box) if ready else None,
        "server": f"{username(box)}.your-storagebox.de" if ready else None,
        "system": f"{box['location'].upper()}-BX{box['id'] % 1000}",
        "stats": stats,
        "created": box["created"],
    }


def checked_ssh_keys(keys) -> list[str]:
    """Check a list of SSH public keys (one per entry, 64 KiB in all) and return it."""
    if (
        not isinstance(keys, list)
        or not all(isinstance(key, str) for key in keys)
        or sum(map(len, keys)) > 65536
    ):
        raise invalid("ssh_keys is a list of public keys (64 KiB in all)", "ssh_keys")
    for key in keys:
        try:
            ssh_fingerprint(key)
        # whatever the parser fails on: it is not a valid key
        except Exception:  # noqa: BLE001
            raise invalid("not an SSH public key", "ssh_keys") from None
        if any(ord(char) < 32 for char in key):
            raise invalid("one key per entry", "ssh_keys")
    return keys


async def set_up_box_account(
    lab: Lab, box: StorageBox, password: str, keys: list[str]
) -> None:
    """Set up the box's account.

    If that fails, nothing of the box stays.
    """
    fabric_box = lab.fabric.box
    try:
        await box_account(lab, box, password)
        # as on Hetzner: the keys end up in the box's .ssh/authorized_keys
        if keys:
            await lab.blocking(fabric_box.write_authorized_keys, username(box), keys)
        await sync_box(lab, box)
    except Exception:  # undo the partial change, then raise
        await lab.blocking(fabric_box.remove_account, username(box), check=False)
        await lab.blocking(fabric_box.remove_tree, data_dir(box), check=False)
        with lab.state.change():
            lab.state["storage_boxes"].pop(box["id"], None)
        raise


async def remove_box(lab: Lab, box: StorageBox) -> None:
    """Remove the box with its sub-accounts, snapshots and data."""
    state, fabric_box = lab.state, lab.fabric.box
    mine = [x for x in state["subaccounts"].values() if x["box"] == box["id"]]
    for subaccount in mine:
        await lab.blocking(fabric_box.remove_account, subaccount["username"])
        del state["subaccounts"][subaccount["id"]]
    for s in [s for s in state["box_snapshots"].values() if s["box"] == box["id"]]:
        del state["box_snapshots"][s["id"]]
    await lab.blocking(fabric_box.remove_account, username(box))
    await lab.blocking(fabric_box.remove_tree, data_dir(box))
    await lab.blocking(fabric_box.remove_tree, snap_dir(box))
    with state.change():
        state["storage_boxes"].pop(box["id"], None)


# --- box actions ----------------------------------------------------------------------
# Each function gets the box and the request and returns its plan (lab.Plan).


async def change_type(lab: Lab, box: StorageBox, body: dict) -> Plan:
    """Change the box's type, if the new one has room for what the box uses."""
    type_name = catalogue_name(lab, "storage_box_type", body.get("storage_box_type"))
    used = (await box_stats(lab, box))["size"]
    if lab.cfg["storage_box_types"][type_name]["size_tb"] * TB < used:
        raise invalid(
            "the type offers less space than the box uses", "storage_box_type"
        )
    box["type"] = type_name
    await sync_box(lab, box)
    # sub-accounts share the quota
    mine = [a for a in lab.state["subaccounts"].values() if a["box"] == box["id"]]
    for subaccount in mine:
        await lab.blocking(
            lab.fabric.box.set_access,
            subaccount["username"],
            dict(subaccount["access"]),
            quota_bytes(lab, box),
        )
    return Plan()


async def reset_password(lab: Lab, box: StorageBox, body: dict) -> Plan:
    """Set a new password for the box's main account."""
    password = checked_password(body.get("password"))
    await box_account(lab, box, password)
    # stored only after it is set; a lab start sets the stored password again
    box["_password"] = password
    await sync_box(lab, box)
    return Plan()


async def update_access_settings(lab: Lab, box: StorageBox, body: dict) -> Plan:
    """Change the box's access settings; on failure the old ones stay."""
    old = dict(box["access"])
    box["access"].update(checked_access_settings(body, box["access"]))
    try:
        await sync_box(lab, box)
    except Exception:  # the settings did not take effect, so do not store them
        box["access"] = old
        raise
    return Plan()


def enable_snapshot_plan(lab: Lab, box: StorageBox, body: dict) -> Plan:
    """Set the box's snapshot plan, within the snapshot limit of its type."""
    if (
        not 1
        <= int(body["max_snapshots"])
        <= lab.cfg["storage_box_types"][box["type"]]["snapshots"]
    ):
        raise invalid("exceeds the snapshot limit of the type", "max_snapshots")
    keys = ("max_snapshots", "minute", "hour", "day_of_week", "day_of_month")
    box["plan"] = {key: body.get(key) for key in keys}
    return Plan()


def disable_snapshot_plan(lab: Lab, box: StorageBox, body: dict) -> Plan:
    """Remove the box's snapshot plan."""
    box["plan"] = None
    return Plan()


# the Storage Box actions: Hetzner's name of the action and the function that plans it
BOX_ACTIONS: ActionTable[StorageBox] = {
    "change_protection": ActionSpec(
        "change_protection", change_delete_protection, flag=False
    ),
    "change_type": ActionSpec("change_type", change_type),
    "reset_password": ActionSpec("reset_password", reset_password),
    "update_access_settings": ActionSpec(
        "update_access_settings", update_access_settings
    ),
    "enable_snapshot_plan": ActionSpec("enable_snapshot_plan", enable_snapshot_plan),
    "disable_snapshot_plan": ActionSpec("disable_snapshot_plan", disable_snapshot_plan),
    "rollback_snapshot": ActionSpec("rollback_snapshot", rollback_snapshot),
}


# --- route logic ----------------------------------------------------------------------


async def create_box(lab: Lab, body: dict) -> dict:
    """Create a box; its account is set up in the background."""
    state = lab.state
    type_name = catalogue_name(lab, "storage_box_type", body["storage_box_type"])
    location = catalogue_name(lab, "location", body["location"])
    check_unique_name(
        state["storage_boxes"].values(), body["name"], None, "Storage Box"
    )
    access_settings = {
        "reachable_externally": False,
        "samba_enabled": False,
        "ssh_enabled": False,
        "webdav_enabled": False,
        "zfs_enabled": False,
    }
    access_settings.update(
        checked_access_settings(body.get("access_settings"), access_settings)
    )
    keys = checked_ssh_keys(body.get("ssh_keys") or [])
    password = checked_password(body.get("password"))
    with state.change():
        bid = state.new_id("storage_boxes")
        box: StorageBox = {
            "id": bid,
            "name": body["name"],
            "type": type_name,
            "location": location,
            "access": access_settings,
            "plan": None,
            "protection": {"delete": False},
            "labels": body.get("labels") or {},
            "status": "initializing",
            "created": now(),
            "_password": password,
        }
        state["storage_boxes"][bid] = box
    action = lab.new_action("create", [(bid, "storage_box")])

    def activate() -> None:
        with state.change():
            box["status"] = "active"

    async def steps():
        await set_up_box_account(lab, box, password, keys)
        await lab.sleep(BOX_CREATE_SECONDS)
        activate()

    lab.run_action(action, steps)
    return {"storage_box": await box_json(lab, box), "action": action}


async def update_box(lab: Lab, bid: int, body: dict) -> dict:
    """Change a box's name and labels (names are unique)."""
    box = box_or_404(lab, bid)
    update_resource(lab, "storage_boxes", box, body, ("name", "labels"), "Storage Box")
    return {"storage_box": await box_json(lab, box)}


def delete_box(lab: Lab, bid: int) -> dict:
    """Delete a box with its sub-accounts, snapshots and data, in the background.

    The lab refuses this (423) while the box is being set up, rolled back, copied,
    getting a sub-account, or already deleted. If the delete fails, the box can be
    deleted again.
    """
    box = box_or_404(lab, bid)
    if box["protection"]["delete"]:
        raise protected("Storage Box is delete protected")

    async def steps():
        await remove_box(lab, box)

    with lab.holding() as held:
        if box_busy(lab, box, for_delete=True):
            raise locked(BUSY)
        held.claim(box, "_deleting", BUSY)
        action = lab.new_action("delete", [(bid, "storage_box")])
        lab.run_action(action, steps, holding=held)
    return {"action": action}


async def box_folders(lab: Lab, bid: int, path: str) -> dict:
    """Return the folders below a path of the box."""
    box = box_or_404(lab, bid)
    path = posixpath.join(data_dir(box), normalized_box_path(path, "path"))
    return {"folders": await lab.blocking(lab.fabric.box.list_folders, path)}


async def run_box_action(lab: Lab, bid: int, cmd: str, body: dict) -> dict:
    """Run an action of a box; the box is busy (`_busy`) until it ends."""
    box = box_or_404(lab, bid, ready=True)
    return await lab.act(BOX_ACTIONS, cmd, box, "storage_box", body, "_busy", BUSY)


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the routes of box types, boxes, their actions and folders to the app."""
    cfg, state = lab.cfg, lab.state

    @app.get("/v1/storage_box_types")
    async def box_type_list(request: Request):
        box_types = [box_type_json(lab, name) for name in cfg["storage_box_types"]]
        return paginate(request, filtered(request, box_types), "storage_box_types")

    @app.get("/v1/storage_box_types/{tid}")
    async def box_type_get(tid: int):
        name = catalogue_entry_or_404(lab, "storage_box_type", tid, "Storage Box type")
        return {"storage_box_type": box_type_json(lab, name)}

    # the actions of all Storage Boxes (those of one box are under its own path);
    # registered before /v1/storage_boxes/{bid}
    @app.get("/v1/storage_boxes/actions")
    async def box_action_list(request: Request):
        mine = [a for a in state["actions"].values() if touches(a, "storage_box")]
        return listed(request, mine)

    @app.get("/v1/storage_boxes/actions/{aid}")
    @app.get("/v1/storage_boxes/{bid}/actions/{aid}")
    async def box_action_get(aid: int, bid: int = 0):
        return {"action": action_or_404(lab, aid, "storage_box", bid or None)}

    @app.get("/v1/storage_boxes/{bid}/actions")
    async def box_actions(request: Request, bid: int):
        box_or_404(lab, bid)
        mine = [a for a in state["actions"].values() if touches(a, "storage_box", bid)]
        return listed(request, mine)

    # boxes
    @app.get("/v1/storage_boxes")
    async def box_list(request: Request):
        items = filtered(request, list(state["storage_boxes"].values()))
        shapes = [await box_json(lab, box) for box in items]
        return paginate(request, shapes, "storage_boxes")

    @app.post("/v1/storage_boxes", status_code=201)
    async def box_create(request: Request):
        return await create_box(lab, await read_body(request))

    @app.get("/v1/storage_boxes/{bid}")
    async def box_get(bid: int):
        return {"storage_box": await box_json(lab, box_or_404(lab, bid))}

    @app.put("/v1/storage_boxes/{bid}")
    async def box_update(bid: int, request: Request):
        return await update_box(lab, bid, await read_body(request))

    @app.delete("/v1/storage_boxes/{bid}", status_code=201)
    async def box_delete(bid: int):
        return delete_box(lab, bid)

    @app.get("/v1/storage_boxes/{bid}/folders")
    async def box_folder_list(bid: int, request: Request):
        return await box_folders(lab, bid, request.query_params.get("path", "."))

    @app.post("/v1/storage_boxes/{bid}/actions/{cmd}", status_code=201)
    async def box_action(bid: int, cmd: str, request: Request):
        body = await read_body(request)
        return await run_box_action(lab, bid, cmd, body)
