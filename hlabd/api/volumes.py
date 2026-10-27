"""Volumes: block devices that attach to servers in their location, as on Hetzner.

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
from ..lab import (
    SERVER_LOCKED,
    VOLUME_SECONDS,
    ActionSpec,
    ActionTable,
    Lab,
    Plan,
    RecordGoneError,
    now,
)
from ..records import Server, Volume
from ..state import SERVERS, VOLUMES
from . import hetzner
from .catalogue import catalogue_name
from .resources import change_delete_protection, update_resource
from .web import filtered, paginate, read_body, ref_id

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
    attached = sum(
        1
        for other in lab.state[VOLUMES].values()
        if other.get("server") == server["id"]
    )
    if attached >= VOLUMES_PER_SERVER:
        raise limit_exceeded(
            f"at most {VOLUMES_PER_SERVER} volumes per server", "volumes_per_server"
        )


def record_attachment(
    lab: Lab, volume: Volume, server: Server, automount: bool
) -> None:
    """Record an attachment in the state; `check_attach` has checked it already.

    The disk itself comes later: hot-plugged, or at the next boot. The caller saves
    the change in its `change()`.
    """
    volume["server"], volume["automount"] = server["id"], bool(automount)


def save_detached(lab: Lab, volume: Volume) -> None:
    """Record in the state that the volume is attached to no server."""
    with lab.state.change():
        volume["server"], volume["automount"] = None, False


def create_volume(lab: Lab, body: dict) -> dict:
    """Create a volume, attached to a server if the body names one.

    The create action makes the disk in the background. A server named in the body
    stays locked until then, as for an attach (423 if an action runs on it), because
    the disk is hot-plugged into its VM at the end.
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
    with lab.holding() as held:
        with state.change():
            check_unique_name(state[VOLUMES].values(), body["name"], None, "volume")
            # checked before the volume exists: a refused request leaves nothing behind
            if server:
                check_room(lab, location, server)  # the new volume is not attached yet
                # the last check: nothing may refuse after the server is claimed
                held.claim(server, "_locked", SERVER_LOCKED)
            vid = state.new_id(VOLUMES)
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
            state[VOLUMES][vid] = volume
            if server:
                record_attachment(lab, volume, server, body.get("automount", False))
        action = lab.new_action("create_volume", [(vid, "volume")])
        next_actions = (
            [
                lab.new_action(
                    "attach_volume", [(vid, "volume"), (server["id"], "server")]
                )
            ]
            if server
            else []
        )

        sid = server["id"] if server else None

        def drop_volume() -> None:
            with state.change():
                state[VOLUMES].pop(vid, None)

        def make_available() -> None:
            """Mark the volume available, or raise RecordGoneError if it is gone.

            The API refuses to delete a volume that is still being created, and a lab
            reset cancels this action first, so this is only a guard.
            """
            with state.change():
                lab.current(VOLUMES, vid)["status"] = "available"

        async def steps():
            async def undo():
                # no volume stays "creating" (or attached) without its disk
                drop_volume()

            async with lab.undo_on_failure(undo):
                current = lab.current(VOLUMES, vid)
                await lab.blocking(
                    lab.fabric.volumes.create,
                    vid,
                    current["size"],
                    current.get("format"),
                )
            try:
                make_available()
            except RecordGoneError:
                # removed from the state while its disk was made: delete the disk too
                await lab.blocking(lab.fabric.volumes.delete, vid)
                raise
            if sid is not None and await lab.blocking(lab.fabric.vms.is_alive, sid):
                await lab.blocking(lab.fabric.volumes.attach, sid, vid)
            for next_action in next_actions:
                lab.finish(next_action)
            await lab.sleep(VOLUME_SECONDS)

        lab.run_action(action, steps, next_actions=next_actions, holding=held)
    return {
        "volume": hetzner.volume_json(lab, volume),
        "action": action,
        "next_actions": next_actions,
    }


def update_volume(lab: Lab, vid: int, body: dict) -> dict:
    """Change a volume's name or labels."""
    volume = lab.get_or_404(VOLUMES, vid, "volume")
    update_resource(lab, VOLUMES, volume, body, ("name", "labels"), "volume")
    return {"volume": hetzner.volume_json(lab, volume)}


async def delete_volume(lab: Lab, vid: int) -> None:
    """Delete a volume that is unprotected, detached and not busy.

    The volume is claimed during the checks, so no action starts on it while its disk
    is deleted. After the delete it stays claimed. If deleting the disk fails, it is
    free again.
    """
    volume = lab.get_or_404(VOLUMES, vid, "volume")
    with lab.holding() as held:
        if volume["protection"]["delete"]:
            raise protected("volume is delete protected")
        if volume.get("server"):
            raise ApiError(422, "volume_attached", "detach the volume first")
        if volume["status"] != "available":
            raise locked(VOLUME_BUSY)
        held.claim(volume, "_busy", VOLUME_BUSY)
        await lab.blocking(lab.fabric.volumes.delete, volume["id"])
        with lab.state.change():
            del lab.state[VOLUMES][vid]
            held.keep()  # a request that looked up the volume earlier still gets 423


# --- volume actions -------------------------------------------------------------------
# Each action below checks the request and returns its plan (lab.Plan). The steps run
# in the background. Meanwhile the volume is busy and its server, if any, is locked
# (lab.act).


def require_available(volume: Volume) -> None:
    """Refuse an action on a volume whose disk is still being made (423)."""
    if volume["status"] != "available":
        raise locked(VOLUME_BUSY)


async def attach(lab: Lab, volume: Volume, body: dict) -> Plan:
    """Plan an attach to a server.

    The attachment is recorded when the action runs, while the server is locked. A
    running server gets the disk hot-plugged. If that fails, the volume is detached
    again.
    """
    require_available(volume)
    server = lab.server_or_404(ref_id(body.get("server"), "server"))
    check_attach(lab, volume, server)
    automount = body.get("automount", False)
    vid, sid = volume["id"], server["id"]

    def save_attachment() -> None:
        with lab.state.change():
            record_attachment(
                lab, lab.current(VOLUMES, vid), lab.current(SERVERS, sid), automount
            )

    async def steps():
        save_attachment()
        if await lab.blocking(lab.fabric.vms.is_alive, sid):

            async def undo():  # not attached: record that in the volume
                volume_now = lab.state.get(VOLUMES, vid)  # nothing to undo if gone
                if volume_now is not None:
                    save_detached(lab, volume_now)

            async with lab.undo_on_failure(undo):
                await lab.blocking(lab.fabric.volumes.attach, sid, vid)
        await lab.sleep(VOLUME_SECONDS)

    return Plan(steps, server=server, resources=[(server["id"], "server")])


async def detach(lab: Lab, volume: Volume, body: dict) -> Plan:
    """Plan a detach from the volume's server."""
    require_available(volume)
    if not volume.get("server"):
        raise invalid("the volume is not attached", "volume")
    server = lab.state.get(SERVERS, volume["server"])
    vid, sid = volume["id"], server["id"] if server is not None else None

    async def steps():
        if sid is not None and await lab.blocking(lab.fabric.vms.is_alive, sid):
            await lab.blocking(lab.fabric.volumes.detach, sid, vid)
        save_detached(lab, lab.current(VOLUMES, vid))
        await lab.sleep(VOLUME_SECONDS)

    resources = [(server["id"], "server")] if server else []
    return Plan(steps, server=server, resources=resources)


async def resize(lab: Lab, volume: Volume, body: dict) -> Plan:
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
    server = lab.state.get(SERVERS, volume["server"])
    vid, sid = volume["id"], server["id"] if server is not None else None

    def save_size() -> None:
        with lab.state.change():
            lab.current(VOLUMES, vid)["size"] = size

    async def steps():
        live = sid is not None and await lab.blocking(lab.fabric.vms.is_alive, sid)
        await lab.blocking(lab.fabric.volumes.resize, vid, size, sid if live else None)
        save_size()
        await lab.sleep(VOLUME_SECONDS)

    return Plan(steps, server=server)


# the volume actions: Hetzner's name of the action and the function that plans it
VOLUME_ACTIONS = ActionTable[Volume](
    "volume",
    {
        "change_protection": ActionSpec(
            "change_protection", change_delete_protection, flag=False
        ),
        "attach": ActionSpec("attach_volume", attach),
        "detach": ActionSpec("detach_volume", detach),
        "resize": ActionSpec("resize_volume", resize),
    },
    flag="_busy",
    busy=VOLUME_BUSY,
)


async def run_volume_action(lab: Lab, vid: int, cmd: str, body: dict) -> dict:
    """Run a volume action: one at a time per volume (423 otherwise)."""
    volume = lab.get_or_404(VOLUMES, vid, "volume")
    return await lab.act(VOLUME_ACTIONS, cmd, volume, body)


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the volume routes to the app."""
    state = lab.state

    @app.get("/v1/volumes")
    async def volume_list(request: Request):
        items = filtered(
            request,
            list(state[VOLUMES].values()),
            {"status": lambda volume, value: volume["status"] in value.split(",")},
        )
        return paginate(
            request, [hetzner.volume_json(lab, v) for v in items], "volumes"
        )

    @app.post("/v1/volumes", status_code=201)
    async def volume_create(request: Request):
        return create_volume(lab, await read_body(request))

    @app.get("/v1/volumes/{vid}")
    async def volume_get(vid: int):
        return {
            "volume": hetzner.volume_json(lab, lab.get_or_404(VOLUMES, vid, "volume"))
        }

    @app.put("/v1/volumes/{vid}")
    async def volume_update(vid: int, request: Request):
        return update_volume(lab, vid, await read_body(request))

    @app.delete("/v1/volumes/{vid}", status_code=204)
    async def volume_delete(vid: int):
        await delete_volume(lab, vid)
        return Response(status_code=204)

    @app.post("/v1/volumes/{vid}/actions/{cmd}", status_code=201)
    async def volume_action(vid: int, cmd: str, request: Request):
        return await run_volume_action(lab, vid, cmd, await read_body(request))
