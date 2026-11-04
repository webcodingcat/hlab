"""Images: the system images of hlab.yaml and the project's snapshots and backups.

A snapshot or backup is a copy of a server's disk in a file of its own (the fabric
keeps it). A server made from one gets a copy of that file, so the image can be
deleted any time, as on Hetzner. Backups belong to their server: it keeps at most
`BACKUP_SLOTS`, they are taken once a day while backups are enabled, and they go with
the server or with `disable_backup`. Converting one to a snapshot keeps it.

The lab control copies an image to a qcow2 file and adds a qcow2 file as a snapshot
(`hlab image export|import`), so a prepared image can outlive its lab.
"""

import logging
import math

from fastapi import FastAPI, Request
from fastapi.responses import Response

from ..errors import invalid, locked, not_found, protected
from ..lab import ActionSpec, ActionTable, Lab, Plan, now
from ..records import Image, Server
from ..state import IMAGES
from . import hetzner
from .catalogue import by_name_or_id, catalogue_entry_or_404, image_path
from .resources import change_delete_protection, update_fields
from .web import filtered, paginate, read_body

log = logging.getLogger(__name__)

BACKUP_SLOTS = 7  # Hetzner keeps the seven newest backups of a server
# Hetzner picks a backup window for the server; the lab always picks this one (UTC),
# and takes the day's backup when it begins
BACKUP_WINDOW = "22-02"
BACKUP_HOUR = 22
GB = 1024**3


def image_ref(lab: Lab, ref, field: str = "image") -> str | int:
    """Return what a request's `image` names: a system image's name or an image's ID.

    System images are found by name or ID, the project's images by ID (they have no
    name). An image still being created, or unknown, is invalid input for `field`.
    """
    names = lab.cfg["images"]
    name = by_name_or_id(names, ref, lambda n: (n, hetzner.image_id(lab, n)))
    if name is not None:
        return name
    image = by_name_or_id(lab.state[IMAGES].values(), ref, lambda i: (i["id"],))
    if image is None or image["status"] != "available":
        raise invalid(f"image {ref!r} not found", field)
    return image["id"]


def check_disk_fits(lab: Lab, image: str | int, disk_gb: int) -> None:
    """Refuse an image whose disk is larger than the server's (Hetzner does too)."""
    if isinstance(image, int) and lab.state[IMAGES][image]["disk_size"] > disk_gb:
        raise invalid("the image's disk is larger than the server type's", "image")


def os_image_of(lab: Lab, server: Server) -> str:
    """Return the system image a server's disk came from in the end, for os_flavor.

    A server made from an image of the project that is gone since is "unknown".
    """
    image = server["image"]
    if isinstance(image, str):
        return image
    own = lab.state.get(IMAGES, image)
    return own["os_image"] if own is not None else "unknown"


def check_usable(lab: Lab, image: str | int) -> None:
    """Raise RuntimeError if a disk cannot be made from the image (any more).

    A system image may have failed to load at the lab's start; an image of the
    project may have been deleted since the request was checked.
    """
    if isinstance(image, str):
        image_path(lab, image)  # raises if it was not loaded
    elif image not in lab.state[IMAGES]:
        raise RuntimeError(f"the image {image} was deleted")


async def create_disk(lab: Lab, sid: int, image: str | int, disk_gb: int) -> None:
    """Create a server's disk from a system image or an image of the project."""
    check_usable(lab, image)
    if isinstance(image, str):
        base = image_path(lab, image)
        await lab.blocking(lab.fabric.vms.create_disk, sid, base, disk_gb)
    else:
        await lab.blocking(lab.fabric.vms.create_disk_from_image, sid, image, disk_gb)


async def delete_images(lab: Lab, iids: list[int]) -> None:
    """Delete images of the project: their records, then their files."""
    with lab.state.change():
        for iid in iids:
            lab.state[IMAGES].pop(iid, None)
    for iid in iids:
        await lab.blocking(lab.fabric.vms.delete_image, iid)


def backups_of(lab: Lab, sid: int) -> list[int]:
    """Return the IDs of a server's backups, oldest first."""
    return sorted(
        iid
        for iid, image in lab.state[IMAGES].items()
        if image["type"] == "backup" and image["bound_to"] == sid
    )


# --- server actions (api/servers.py adds them to SERVER_ACTIONS) ----------------------


async def create_image(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a snapshot or backup of the server's disk; the server stays locked.

    The image exists at once with the status creating, as on Hetzner. A running
    server is copied as it is: like on Hetzner, its image may be inconsistent.
    """
    kind = body.get("type") or "snapshot"
    if kind == "backup" and not server["backup_window"]:
        raise invalid("backups are not enabled for the server", "type")
    sid = server["id"]
    with lab.state.change():
        iid = lab.state.new_id(IMAGES)
        image: Image = {
            "id": iid,
            "type": kind,
            "status": "creating",
            "description": body.get("description") or f"{kind} of {server['name']}",
            "labels": body.get("labels") or {},
            "created": now(),
            "created_from_id": sid,
            "created_from_name": server["name"],
            "bound_to": sid if kind == "backup" else None,
            "os_image": os_image_of(lab, server),
            "disk_size": server["_lab"]["disk_gb"],
            "image_size": None,
            "protection": {"delete": False},
        }
        lab.state[IMAGES][iid] = image

    async def steps():
        async with lab.undo_on_failure(lambda: delete_images(lab, [iid])):
            size = await lab.blocking(lab.fabric.vms.create_image, sid, iid)
        with lab.state.change():
            image = lab.current(IMAGES, iid)
            image["status"] = "available"
            image["image_size"] = round(size / GB, 2)
        if kind == "backup":  # the oldest backups beyond the slots go
            await delete_images(lab, backups_of(lab, sid)[:-BACKUP_SLOTS])

    return Plan(
        steps,
        extra={"image": hetzner.own_image_json(lab, image)},
        server=server,
        resources=[(iid, "image")],
    )


async def enable_backup(lab: Lab, server: Server, body: dict) -> Plan:
    """Enable the daily backups (Hetzner's backup window is chosen for the server)."""
    with lab.state.change():
        server["backup_window"] = BACKUP_WINDOW
    return Plan()


async def disable_backup(lab: Lab, server: Server, body: dict) -> Plan:
    """Disable the daily backups and delete the server's backups at once."""
    sid = server["id"]
    with lab.state.change():
        server["backup_window"] = None
    backups = backups_of(lab, sid)
    if not backups:
        return Plan()

    async def steps():
        await delete_images(lab, backups)

    return Plan(steps, server=server)


# --- the project's images -------------------------------------------------------------


def own_image_or_404(lab: Lab, iid: int) -> Image:
    """Return an image of the project, or raise 404."""
    return lab.get_or_404(IMAGES, iid, "image")


def update_image(lab: Lab, iid: int, body: dict) -> dict:
    """Change an image's description or labels, or turn a backup into a snapshot.

    A snapshot is no longer bound to its server, so it outlives it.
    """
    image = own_image_or_404(lab, iid)
    with lab.state.change():
        update_fields(image, body, ("description", "labels"))
        if body.get("type") == "snapshot":
            image["type"], image["bound_to"] = "snapshot", None
    return {"image": hetzner.own_image_json(lab, image)}


async def delete_image(lab: Lab, iid: int) -> None:
    """Delete an image of the project unless it is protected or still being created."""
    image = own_image_or_404(lab, iid)
    if image["protection"]["delete"]:
        raise protected("image is delete protected")
    if image["status"] == "creating":
        raise locked("image is being created")
    await delete_images(lab, [iid])


async def image_change_protection(lab: Lab, image: Image, body: dict) -> Plan:
    """Set the delete protection; Hetzner allows it on snapshots only."""
    if image["type"] != "snapshot":
        raise invalid("only snapshots can be protected", "delete")
    return await change_delete_protection(lab, image, body)


# the image actions: Hetzner's name of the action and the function that plans it
IMAGE_ACTIONS = ActionTable[Image](
    "image",
    {
        "change_protection": ActionSpec(
            "change_protection", image_change_protection, flag=False
        ),
    },
)


async def drop_server_backups(lab: Lab, sid: int) -> None:
    """Delete the backups of a server that is deleted (Hetzner deletes them with it)."""
    await delete_images(lab, backups_of(lab, sid))


async def drop_half_created_images(lab: Lab) -> None:
    """Delete the images whose copy a lab restart cut off; their action has failed."""
    creating = [
        iid for iid, i in lab.state[IMAGES].items() if i["status"] == "creating"
    ]
    await delete_images(lab, creating)


# --- images in files of the container (the lab control, api/control.py) --------------


def checked_file(body: dict) -> str:
    """Return the request's `file`, a path in the container."""
    file = body.get("file")
    if not isinstance(file, str) or not file.startswith("/"):
        raise invalid("file must be an absolute path in the container", "file")
    return file


async def import_image(lab: Lab, body: dict) -> dict:
    """Add a qcow2 file of the container to the project as a snapshot; return it.

    Hetzner has no images of one's own, only snapshots, so an imported image is a
    snapshot taken from no server: `created_from` is null and the OS is unknown. The
    lab copies the file, which stays where it is. Body: {"file": "/path.qcow2",
    "description": "...", "labels": {"key": "value"}}.

    Like the other lab control routes, it answers when the work is done: the caller
    waits for the copy anyway, and there is no Hetzner action to poll.
    """
    file = checked_file(body)
    description = body.get("description") or "imported image"
    labels = body.get("labels") or {}
    # /_lab has no OpenAPI request check (api/web.py), so the body is checked here
    if not isinstance(description, str):
        raise invalid("description must be a string", "description")
    if not isinstance(labels, dict) or not all(
        isinstance(key, str) and isinstance(value, str) for key, value in labels.items()
    ):
        raise invalid("labels must map strings to strings", "labels")
    info = await lab.blocking(lab.fabric.vms.inspect_image_file, file)
    if info is None or info.format != "qcow2" or not info.standalone:
        raise invalid(f"{file} is no qcow2 image that stands alone", "file")
    # the image exists at once, still being created, like a snapshot: it cannot be
    # deleted half way, and a lab restart drops it (drop_half_created_images)
    with lab.state.change():
        iid = lab.state.new_id(IMAGES)
        lab.state[IMAGES][iid] = {
            "id": iid,
            "type": "snapshot",
            "status": "creating",
            "description": description,
            "labels": labels,
            "created": now(),
            "created_from_id": None,
            "created_from_name": None,
            "bound_to": None,
            "os_image": "unknown",
            "disk_size": math.ceil(info.virtual_size / GB),
            "image_size": None,
            "protection": {"delete": False},
        }
    async with lab.undo_on_failure(lambda: delete_images(lab, [iid])):
        size = await lab.blocking(lab.fabric.vms.import_image, file, iid)
    # a route, not an action's steps: a reset meanwhile makes this a 404
    with lab.state.change():
        image = own_image_or_404(lab, iid)
        image["status"] = "available"
        image["image_size"] = round(size / GB, 2)
    return {"image": hetzner.own_image_json(lab, image)}


async def export_image(lab: Lab, iid: int, body: dict) -> dict:
    """Copy an image of the project (a snapshot or backup) to a file of the container.

    The file is written whole or not at all. Body: {"file": "/path.qcow2"}. No flag
    keeps the image while it is copied: a copy that has begun reads on from the
    deleted file, and image files never change once they are whole.
    """
    file = checked_file(body)
    if own_image_or_404(lab, iid)["status"] == "creating":
        raise locked("image is being created")
    await lab.blocking(lab.fabric.vms.export_image, iid, file)
    return {"file": file}


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the image routes to the app: system images and the project's images."""
    cfg, state = lab.cfg, lab.state

    @app.get("/v1/images")
    async def image_list(request: Request):
        images = [hetzner.image_json(lab, n) for n in cfg["images"]] + [
            hetzner.own_image_json(lab, i) for i in state[IMAGES].values()
        ]
        items = filtered(
            request,
            images,
            {
                "type": lambda image, value: image["type"] in value.split(","),
                "status": lambda image, value: image["status"] in value.split(","),
                "bound_to": lambda image, value: (
                    str(image["bound_to"]) in value.split(",")
                ),
                # an empty architecture filters nothing
                "architecture": lambda image, value: (
                    not value or image["architecture"] in value.split(",")
                ),
            },
        )
        return paginate(request, items, "images")

    @app.get("/v1/images/{iid}")
    async def image_get(iid: int):
        if iid in state[IMAGES]:
            return {"image": hetzner.own_image_json(lab, state[IMAGES][iid])}
        name = catalogue_entry_or_404(lab, "image", iid, "image")
        return {"image": hetzner.image_json(lab, name)}

    @app.put("/v1/images/{iid}")
    async def image_update(iid: int, request: Request):
        if iid not in state[IMAGES]:
            catalogue_entry_or_404(lab, "image", iid, "image")
            raise invalid("system images cannot be changed")
        return update_image(lab, iid, await read_body(request))

    @app.delete("/v1/images/{iid}", status_code=204)
    async def image_delete(iid: int):
        if iid not in state[IMAGES]:
            catalogue_entry_or_404(lab, "image", iid, "image")
            raise invalid("system images cannot be deleted")
        await delete_image(lab, iid)
        return Response(status_code=204)

    @app.post("/v1/images/{iid}/actions/{cmd}", status_code=201)
    async def image_action(iid: int, cmd: str, request: Request):
        image = state.get(IMAGES, iid)
        if image is None:
            raise not_found("image")
        return await lab.act(IMAGE_ACTIONS, cmd, image, await read_body(request))
