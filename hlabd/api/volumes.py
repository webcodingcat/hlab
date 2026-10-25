"""Volumes: block devices that attach to servers of their location, like Hetzner's.

They are SCSI disks with vendor HC and model Volume
(/dev/disk/by-id/scsi-0HC_Volume_<id>), formatted on request, automounted at
/mnt/HC_Volume_<id>.
"""

from fastapi import FastAPI, Request
from fastapi.responses import Response

from ..errors import (
    ApiError,
    invalid,
    limit_exceeded,
    locked,
    not_found,
    unique_name,
    unsupported,
)
from ..lab import now
from . import hetzner
from .catalogue import resolve
from .web import filtered, paginate, read_body

MIN_GB, MAX_GB = 10, 10240
PER_SERVER = 16


def volume_json(lab, volume: dict) -> dict:
    """Return a volume as the API shows it."""
    return {
        "id": volume["id"],
        "name": volume["name"],
        "created": volume["created"],
        "server": volume.get("server"),
        "location": hetzner.location_json(lab.cfg, volume["location"]),
        "size": volume["size"],
        "linux_device": f"/dev/disk/by-id/scsi-0HC_Volume_{volume['id']}",
        "protection": volume["protection"],
        "labels": volume["labels"],
        "status": volume["status"],
        "format": volume.get("format"),
    }


def volume_or_404(lab, vid: int) -> dict:
    """Return a volume, or raise not_found."""
    volume = lab.state["volumes"].get(vid)
    if not volume:
        raise not_found("volume")
    return volume


def check_attach(lab, volume: dict, server: dict) -> None:
    """Check that the volume can be attached to the server (Hetzner's errors)."""
    if volume.get("server"):
        raise ApiError(
            422, "volume_already_attached", "the volume is attached to another server"
        )
    if volume["location"] != server["location"]:
        raise ApiError(
            422, "location_mismatch", "server and volume must be in the same location"
        )
    if (
        sum(1 for x in lab.state["volumes"].values() if x.get("server") == server["id"])
        >= PER_SERVER
    ):
        raise limit_exceeded(
            f"at most {PER_SERVER} volumes per server", "volumes_per_server"
        )


def attach(lab, volume: dict, server: dict, automount: bool) -> None:
    """Record an attachment in the state.

    The disk itself follows (hot-plug or next boot).
    """
    check_attach(lab, volume, server)
    volume["server"], volume["automount"] = server["id"], bool(automount)


def alive(lab, server: dict | None) -> bool:
    """Return whether there is a server and its VM runs."""
    return bool(server) and lab.fabric.vm_alive(server)


def create_volume(lab, body: dict) -> dict:
    """Create a volume, attached to a server if the body names one.

    The disk is made by the create action in the background.
    """
    state = lab.state
    size = int(body["size"])
    if not MIN_GB <= size <= MAX_GB:
        raise invalid(f"must be between {MIN_GB} and {MAX_GB}", "size")
    if body.get("format") not in (None, "ext4", "xfs"):
        raise invalid("must be ext4 or xfs", "format")
    server = lab.server(int(body["server"])) if body.get("server") else None
    if not (body.get("location") or server):
        raise invalid("location or server is required", "location")
    location = resolve(lab, "location", body.get("location") or server["location"])
    with state.lock:
        unique_name(state["volumes"].values(), body["name"], None, "volume")
        # checked before the volume exists: a refused request leaves nothing behind
        if server:
            check_attach(lab, {"server": None, "location": location}, server)
        vid = state.new_id("volumes")
        volume = {
            "id": vid,
            "name": body["name"],
            "size": size,
            "location": location,
            "labels": body.get("labels") or {},
            "protection": {"delete": False},
            "status": "creating",
            "format": body.get("format"),
            "created": now(),
            "server": None,
        }
        state["volumes"][vid] = volume
        if server:
            attach(lab, volume, server, body.get("automount", False))
        state.save()
    action = lab.new_action("create_volume", [(vid, "volume")])
    next_actions = (
        [lab.new_action("attach_volume", [(vid, "volume"), (server["id"], "server")])]
        if server
        else []
    )

    async def steps():
        try:
            await lab.blocking(lab.fabric.create_volume, volume)
        except Exception:
            # no volume stays "creating" (or attached) without its disk
            with state.lock:
                state["volumes"].pop(vid, None)
                state.save()
            raise
        # deleted meanwhile: its disk goes too
        if state["volumes"].get(vid) is not volume:
            await lab.blocking(lab.fabric.delete_volume, volume)
            return
        volume["status"] = "available"
        if await lab.blocking(alive, lab, server):
            await lab.blocking(lab.fabric.attach_volume, server, volume)
        for n in next_actions:
            lab.finish(n)
        await lab.sleep(2)

    lab.run_action(action, steps, next_actions=next_actions)
    return {
        "volume": volume_json(lab, volume),
        "action": action,
        "next_actions": next_actions,
    }


def delete_volume(lab, vid: int) -> None:
    """Delete a volume that is unprotected, detached and not busy."""
    volume = volume_or_404(lab, vid)
    if volume["protection"]["delete"]:
        raise ApiError(423, "protected", "volume is delete protected")
    if volume.get("server"):
        raise ApiError(422, "volume_attached", "detach the volume first")
    if volume.get("_busy") or volume["status"] != "available":
        raise locked("the volume is busy")
    lab.fabric.delete_volume(volume)
    del lab.state["volumes"][vid]
    lab.state.save()


# --- volume actions -------------------------------------------------------------------
# Each action below checks the request and returns (action, steps, server); the steps
# run in the background while the volume is busy and its server (if any) locked.


def attach_volume(lab, volume: dict, body: dict):
    """Check an attach and return the action, its steps and the server.

    A running server gets the disk hot-plugged; if that fails, the volume is detached
    again.
    """
    sid = body.get("server")
    if isinstance(sid, bool) or not isinstance(sid, int):
        raise invalid("server must be the ID of a server", "server")
    server = lab.server(sid)
    lab.reserve(server)  # 423 while the server has an action running
    try:
        with lab.state.lock:
            attach(lab, volume, server, body.get("automount", False))
            lab.state.save()
    except BaseException:
        lab.release(server)
        raise
    action = lab.new_action(
        "attach_volume", [(volume["id"], "volume"), (server["id"], "server")]
    )

    async def steps():
        if await lab.blocking(alive, lab, server):
            try:
                await lab.blocking(lab.fabric.attach_volume, server, volume)
            except Exception:  # not attached: the volume says so
                with lab.state.lock:
                    volume["server"], volume["automount"] = None, False
                    lab.state.save()
                raise
        await lab.sleep(2)

    return action, steps, server


def detach_volume(lab, volume: dict, body: dict):
    """Check a detach and return the action, its steps and the server (if any)."""
    if not volume.get("server"):
        raise invalid("the volume is not attached", "volume")
    server = lab.state["servers"].get(volume["server"])
    if server:
        lab.reserve(server)
    resources = [(volume["id"], "volume")] + (
        [(server["id"], "server")] if server else []
    )
    action = lab.new_action("detach_volume", resources)

    async def steps():
        if await lab.blocking(alive, lab, server):
            await lab.blocking(lab.fabric.detach_volume, server, volume)
        with lab.state.lock:
            volume["server"], volume["automount"] = None, False
            lab.state.save()
        await lab.sleep(2)

    return action, steps, server


def resize_volume(lab, volume: dict, body: dict):
    """Check a resize and return the action, its steps and the server (if any).

    A volume can only grow.
    """
    size = body.get("size")
    if (
        isinstance(size, bool)
        or not isinstance(size, int)
        or size <= volume["size"]
        or size > MAX_GB
    ):
        raise invalid(
            f"must be larger than {volume['size']} and at most {MAX_GB}", "size"
        )
    server = lab.state["servers"].get(volume.get("server"))
    if server:
        lab.reserve(server)
    action = lab.new_action("resize_volume", [(volume["id"], "volume")])

    async def steps():
        live = await lab.blocking(alive, lab, server)
        await lab.blocking(
            lab.fabric.resize_volume, volume, size, server if live else None
        )
        volume["size"] = size
        lab.state.save()
        await lab.sleep(2)

    return action, steps, server


ACTIONS = {"attach": attach_volume, "detach": detach_volume, "resize": resize_volume}


def volume_action(lab, vid: int, cmd: str, body: dict) -> dict:
    """Run a volume action; one at a time per volume, only once it is available."""
    state = lab.state
    volume = volume_or_404(lab, vid)
    if cmd == "change_protection":  # nothing to wait for
        volume["protection"]["delete"] = bool(
            body.get("delete", volume["protection"]["delete"])
        )
        state.save()
        return {"action": lab.done(cmd, [(vid, "volume")])}
    if cmd not in ACTIONS:
        raise unsupported(f"the action {cmd!r}")
    with state.lock:  # one action of a volume at a time, and only once it exists
        if volume["status"] != "available" or volume.get("_busy"):
            raise locked("the volume is busy")
        volume["_busy"] = True
    try:
        action, steps, server = ACTIONS[cmd](lab, volume, body)
    except BaseException:
        volume.pop("_busy", None)
        raise

    async def busy():
        try:
            await steps()
        finally:
            volume.pop("_busy", None)

    lab.run_action(action, busy, server)  # the server is unlocked when the steps end
    return {"action": action}


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab) -> None:
    """Add the volume routes to the app."""
    state = lab.state

    @app.get("/v1/volumes")
    def volumes(request: Request):
        items = filtered(
            request,
            list(state["volumes"].values()),
            {"status": lambda v, x: v["status"] in x.split(",")},
        )
        return paginate(request, [volume_json(lab, v) for v in items], "volumes")

    @app.post("/v1/volumes", status_code=201)
    async def volume_create(request: Request):
        return create_volume(lab, await read_body(request))

    @app.get("/v1/volumes/{vid}")
    def volume_get(vid: int):
        return {"volume": volume_json(lab, volume_or_404(lab, vid))}

    @app.put("/v1/volumes/{vid}")
    async def volume_update(vid: int, request: Request):
        volume = volume_or_404(lab, vid)
        body = await read_body(request)
        with state.lock:
            unique_name(state["volumes"].values(), body.get("name"), volume, "volume")
            for k in ("name", "labels"):
                if k in body:
                    volume[k] = body[k] if k != "labels" else (body[k] or {})
            state.save()
        return {"volume": volume_json(lab, volume)}

    @app.delete("/v1/volumes/{vid}", status_code=204)
    def volume_delete(vid: int):
        delete_volume(lab, vid)
        return Response(status_code=204)

    @app.post("/v1/volumes/{vid}/actions/{cmd}", status_code=201)
    async def volume_action_route(vid: int, cmd: str, request: Request):
        return volume_action(lab, vid, cmd, await read_body(request))
