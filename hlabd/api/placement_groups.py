"""Placement groups: servers of a "spread" group run on different hosts.

The lab has one host, so a group is bookkeeping only: its limits and errors are
Hetzner's, the servers run where they always run. A server names its group
(`placement_group`); the group's server list follows from that, so deleting a server
leaves its group by itself.
"""

from fastapi import FastAPI, Request
from fastapi.responses import Response

from ..errors import (
    already_in_placement_group,
    check_unique_name,
    invalid,
    limit_exceeded,
    placement_error,
    resource_in_use,
    server_not_stopped,
)
from ..lab import Lab, Plan, now
from ..records import PlacementGroup, Server
from ..state import PLACEMENT_GROUPS, SERVERS
from . import hetzner
from .resources import update_resource
from .web import filtered, paginate, read_body, ref_id

GROUPS_PER_PROJECT = 50
SERVERS_PER_GROUP = 10  # for the type "spread"


def checked_group(lab: Lab, ref, field: str) -> int:
    """Return the ID of the group that a request names, if it can take one more server.

    An unknown group is invalid input for `field`; a full one is a placement error.
    """
    gid = ref_id(ref, field)
    if gid not in lab.state[PLACEMENT_GROUPS]:
        raise invalid(f"placement group {ref} not found", field)
    members = [s for s in lab.state[SERVERS].values() if s["placement_group"] == gid]
    if len(members) >= SERVERS_PER_GROUP:
        raise placement_error(
            f"a spread placement group holds at most {SERVERS_PER_GROUP} servers"
        )
    return gid


def create_placement_group(lab: Lab, body: dict) -> dict:
    """Create a placement group; as on Hetzner, without an action."""
    state = lab.state
    if not body.get("name"):
        raise invalid("name is required", "name")
    with state.change():
        groups = state[PLACEMENT_GROUPS].values()
        check_unique_name(groups, body["name"], None, "placement group")
        if len(groups) >= GROUPS_PER_PROJECT:
            raise limit_exceeded(
                f"at most {GROUPS_PER_PROJECT} placement groups per project",
                "placement_groups",
            )
        gid = state.new_id(PLACEMENT_GROUPS)
        group: PlacementGroup = {
            "id": gid,
            "name": body["name"],
            "type": body.get("type") or "spread",
            "labels": body.get("labels") or {},
            "created": now(),
        }
        state[PLACEMENT_GROUPS][gid] = group
    return {"placement_group": hetzner.placement_group_json(lab, group), "action": None}


def update_placement_group(lab: Lab, gid: int, body: dict) -> dict:
    """Change a placement group's name or labels."""
    group = lab.get_or_404(PLACEMENT_GROUPS, gid, "placement group")
    update_resource(
        lab, PLACEMENT_GROUPS, group, body, ("name", "labels"), "placement group"
    )
    return {"placement_group": hetzner.placement_group_json(lab, group)}


def delete_placement_group(lab: Lab, gid: int) -> None:
    """Delete a placement group that no server is in."""
    with lab.state.change():
        lab.get_or_404(PLACEMENT_GROUPS, gid, "placement group")
        if any(s["placement_group"] == gid for s in lab.state[SERVERS].values()):
            # not checked against Hetzner; firewalls behave so
            raise resource_in_use("the placement group still has servers")
        del lab.state[PLACEMENT_GROUPS][gid]


# --- server actions (api/servers.py lists them in SERVER_ACTIONS) ---------------------


async def add_to_placement_group(lab: Lab, server: Server, body: dict) -> Plan:
    """Put a server that is off into a placement group."""
    if server["status"] != "off":
        raise server_not_stopped()
    if server["placement_group"]:
        raise already_in_placement_group()
    gid = checked_group(lab, body.get("placement_group"), "placement_group")
    with lab.state.change():
        server["placement_group"] = gid
    return Plan(resources=[(gid, "placement_group")])


async def remove_from_placement_group(lab: Lab, server: Server, body: dict) -> Plan:
    """Take a server out of its placement group (a server in none stays so)."""
    gid = server["placement_group"]
    with lab.state.change():
        server["placement_group"] = None
    return Plan(resources=[(gid, "placement_group")] if gid else [])


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the placement group routes to the app."""
    state = lab.state

    @app.get("/v1/placement_groups")
    async def placement_group_list(request: Request):
        items = filtered(
            request,
            list(state[PLACEMENT_GROUPS].values()),
            {"type": lambda group, value: group["type"] in value.split(",")},
        )
        return paginate(
            request,
            [hetzner.placement_group_json(lab, g) for g in items],
            "placement_groups",
        )

    @app.post("/v1/placement_groups", status_code=201)
    async def placement_group_create(request: Request):
        return create_placement_group(lab, await read_body(request))

    @app.get("/v1/placement_groups/{gid}")
    async def placement_group_get(gid: int):
        group = lab.get_or_404(PLACEMENT_GROUPS, gid, "placement group")
        return {"placement_group": hetzner.placement_group_json(lab, group)}

    @app.put("/v1/placement_groups/{gid}")
    async def placement_group_update(gid: int, request: Request):
        return update_placement_group(lab, gid, await read_body(request))

    @app.delete("/v1/placement_groups/{gid}", status_code=204)
    async def placement_group_delete(gid: int):
        delete_placement_group(lab, gid)
        return Response(status_code=204)
