"""Volumes: block devices that attach to servers of their location, like Hetzner's.

They are SCSI disks with vendor HC and model Volume
(/dev/disk/by-id/scsi-0HC_Volume_<id>), formatted on request, automounted at
/mnt/HC_Volume_<id>.
"""

from fastapi import FastAPI, Request
from fastapi.responses import Response

from ..errors import (
    ApiError,
    check_unique_name,
    invalid,
    limit_exceeded,
    locked,
    protected,
)
from ..lab import SERVER_LOCKED, VOLUME_SECONDS, Lab, Plan, now
from ..records import Server, Volume
from ..state import server_by_id
from . import hetzner
from .catalogue import catalogue_name
from .web import (
    change_delete_protection,
    filtered,
    paginate,
    read_body,
    ref_id,
    update_resource,
)

MIN_GB, MAX_GB = 10, 10240
VOLUMES_PER_SERVER = 16
VOLUME_BUSY = "the volume is busy"


def check_attach(lab: Lab, volume: Volume, server: Server) -> None:
    """Check that the volume can be attached to the server (Hetzner's errors)."""
    if volume.get("server"):
        raise ApiError(
            422, "volume_already_attached", "the volume is attached to another server"
        )
    check_room(lab, volume["location"], server)


def check_room(lab: Lab, location: str, server: Server) -> None:
    """Check that a volume in a location fits the server: same location, room left."""
    if location != server["location"]:
        raise ApiError(
            422, "location_mismatch", "server and volume must be in the same location"
        )
    with lab.state.lock:
        attached = sum(
            1 for x in lab.state["volumes"].values() if x.get("server") == server["id"]
        )
    if attached >= VOLUMES_PER_SERVER:
        raise limit_exceeded(
            f"at most {VOLUMES_PER_SERVER} volumes per server", "volumes_per_server"
        )


def record_attachment(volume: Volume, server: Server, automount: bool) -> None:
    """Record an attachment in the state, checked already (`check_attach`).

    The disk itself follows (hot-plug or next boot). The caller holds the state's lock.
    """
    volume["server"], volume["automount"] = server["id"], bool(automount)


def vm_running(lab: Lab, server: Server | None) -> bool:
    """Return whether there is a server and its VM runs.

    This asks the VM through a script (`hlab.sh vm-alive`), so steps call it through
    `lab.blocking`.
    """
    return server is not None and lab.fabric.vm_alive(server)


def create_volume(lab: Lab, body: dict) -> dict:
    """Create a volume, attached to a server if the body names one.

    The disk is made by the create action in the background. A named server is locked
    until then, as by an attach (423 if an action runs on it): the disk is hot-plugged
    into its VM at the end.
    """
    state = lab.state
    size = int(body["size"])
    if not MIN_GB <= size <= MAX_GB:
        raise invalid(f"must be between {MIN_GB} and {MAX_GB}", "size")
    if body.get("format") not in (None, "ext4", "xfs"):
        raise invalid("must be ext4 or xfs", "format")
    server = None
    if body.get("server"):
        server = lab.server_or_404(ref_id(body["server"], "server"))
    if not (body.get("location") or server):
        raise invalid("location or server is required", "location")
    location = catalogue_name(
        lab,
        "location",
        body.get("location") or (server["location"] if server else None),
    )
    with state.lock:
        check_unique_name(state["volumes"].values(), body["name"], None, "volume")
        # checked before the volume exists: a refused request leaves nothing behind
        if server:
            check_room(lab, location, server)  # a new volume is attached to nothing
            # the last check: nothing is refused after the server is claimed
            lab.claim(server, "locked", SERVER_LOCKED)
        vid = state.new_id("volumes")
        volume: Volume = {
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
            record_attachment(volume, server, body.get("automount", False))
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
        # The volume left the state while its disk was made: its disk goes too. The API
        # refuses to delete a volume that is still being created, and a lab reset
        # cancels this action first, so this is only a guard. It checks identity, not
        # the ID: is this very record still in the state?
        if state["volumes"].get(vid) is not volume:
            await lab.blocking(lab.fabric.delete_volume, volume)
            return
        with state.lock:
            volume["status"] = "available"
            state.save()
        if await lab.blocking(vm_running, lab, server):
            await lab.blocking(lab.fabric.attach_volume, server, volume)
        for next_action in next_actions:
            lab.finish(next_action)
        await lab.sleep(VOLUME_SECONDS)

    holding = [(server, "locked")] if server else []
    lab.run_action(action, steps, next_actions=next_actions, holding=holding)
    return {
        "volume": hetzner.volume_json(lab, volume),
        "action": action,
        "next_actions": next_actions,
    }


def update_volume(lab: Lab, vid: int, body: dict) -> dict:
    """Change a volume's name or labels."""
    volume = lab.get_or_404("volumes", vid, "volume")
    update_resource(lab, "volumes", volume, body, ("name", "labels"), "volume")
    return {"volume": hetzner.volume_json(lab, volume)}


def delete_volume(lab: Lab, vid: int) -> None:
    """Delete a volume that is unprotected, detached and not busy.

    The volume is claimed with the checks, so no action starts on it while its disk is
    deleted; it stays claimed once deleted, and is free again if the disk stays.
    """
    volume = lab.get_or_404("volumes", vid, "volume")
    with lab.state.lock:
        if volume["protection"]["delete"]:
            raise protected("volume is delete protected")
        if volume.get("server"):
            raise ApiError(422, "volume_attached", "detach the volume first")
        if volume["status"] != "available":
            raise locked(VOLUME_BUSY)
        lab.claim(volume, "_busy", VOLUME_BUSY)
    try:
        lab.fabric.delete_volume(volume)
    except BaseException:
        lab.release(volume, "_busy")
        raise
    with lab.state.lock:
        del lab.state["volumes"][vid]
        lab.state.save()


# --- volume actions -------------------------------------------------------------------
# Each action below checks the request and returns its plan (lab.Plan). The steps run
# in the background while the volume is busy and its server (if any) is locked
# (lab.act).


def require_available(volume: Volume) -> None:
    """Refuse an action on a volume whose disk is still being made (423)."""
    if volume["status"] != "available":
        raise locked(VOLUME_BUSY)


def attach(lab: Lab, volume: Volume, body: dict) -> Plan:
    """Plan an attach to a server.

    The attachment is recorded when the action runs (the server is locked then); a
    running server gets the disk hot-plugged, and if that fails, the volume is detached
    again.
    """
    require_available(volume)
    server = lab.server_or_404(ref_id(body.get("server"), "server"))
    check_attach(lab, volume, server)
    automount = body.get("automount", False)

    async def steps():
        with lab.state.lock:
            record_attachment(volume, server, automount)
            lab.state.save()
        if await lab.blocking(vm_running, lab, server):
            try:
                await lab.blocking(lab.fabric.attach_volume, server, volume)
            except Exception:  # not attached: the volume says so
                with lab.state.lock:
                    volume["server"], volume["automount"] = None, False
                    lab.state.save()
                raise
        await lab.sleep(VOLUME_SECONDS)

    return Plan(steps, server=server, resources=[(server["id"], "server")])


def detach(lab: Lab, volume: Volume, body: dict) -> Plan:
    """Plan a detach from the volume's server."""
    require_available(volume)
    if not volume.get("server"):
        raise invalid("the volume is not attached", "volume")
    server = server_by_id(lab.state, volume["server"])

    async def steps():
        if await lab.blocking(vm_running, lab, server):
            await lab.blocking(lab.fabric.detach_volume, server, volume)
        with lab.state.lock:
            volume["server"], volume["automount"] = None, False
            lab.state.save()
        await lab.sleep(VOLUME_SECONDS)

    resources = [(server["id"], "server")] if server else []
    return Plan(steps, server=server, resources=resources)


def resize(lab: Lab, volume: Volume, body: dict) -> Plan:
    """Plan a resize; a volume can only grow."""
    require_available(volume)
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
    server = server_by_id(lab.state, volume["server"])

    async def steps():
        live = await lab.blocking(vm_running, lab, server)
        await lab.blocking(
            lab.fabric.resize_volume, volume, size, server if live else None
        )
        with lab.state.lock:
            volume["size"] = size
            lab.state.save()
        await lab.sleep(VOLUME_SECONDS)

    return Plan(steps, server=server)


# the volume actions: Hetzner's name of the action and the function that plans it
VOLUME_ACTIONS = {
    "change_protection": ("change_protection", change_delete_protection),
    "attach": ("attach_volume", attach),
    "detach": ("detach_volume", detach),
    "resize": ("resize_volume", resize),
}


def run_volume_action(lab: Lab, vid: int, cmd: str, body: dict) -> dict:
    """Run a volume action: one at a time per volume (423 otherwise)."""
    volume = lab.get_or_404("volumes", vid, "volume")
    return lab.act(VOLUME_ACTIONS, cmd, volume, "volume", body, "_busy", VOLUME_BUSY)


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the volume routes to the app."""
    state = lab.state

    @app.get("/v1/volumes")
    def volume_list(request: Request):
        with state.lock:
            items = filtered(
                request,
                list(state["volumes"].values()),
                {"status": lambda volume, value: volume["status"] in value.split(",")},
            )
            return paginate(
                request, [hetzner.volume_json(lab, v) for v in items], "volumes"
            )

    @app.post("/v1/volumes", status_code=201)
    async def volume_create(request: Request):
        return await lab.blocking(create_volume, lab, await read_body(request))

    @app.get("/v1/volumes/{vid}")
    def volume_get(vid: int):
        return {
            "volume": hetzner.volume_json(lab, lab.get_or_404("volumes", vid, "volume"))
        }

    @app.put("/v1/volumes/{vid}")
    async def volume_update(vid: int, request: Request):
        return await lab.blocking(update_volume, lab, vid, await read_body(request))

    @app.delete("/v1/volumes/{vid}", status_code=204)
    def volume_delete(vid: int):
        delete_volume(lab, vid)
        return Response(status_code=204)

    @app.post("/v1/volumes/{vid}/actions/{cmd}", status_code=201)
    async def volume_action(vid: int, cmd: str, request: Request):
        return await lab.blocking(
            run_volume_action, lab, vid, cmd, await read_body(request)
        )
