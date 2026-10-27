"""Actions: the API returns one for every change, and clients poll it while they wait.

Actions are listed per resource type or per resource. The project-wide list returns
actions by ID only (Hetzner changed this on 30 January 2025).
"""

from fastapi import FastAPI, Request

from ..errors import invalid, not_found
from ..lab import Lab
from ..records import Action
from ..state import ACTIONS, KIND_TYPES
from .web import paginate

# the resource types of actions, by the API path of their resources
RESOURCE_TYPES = {
    "servers": "server",
    "networks": "network",
    "floating_ips": "floating_ip",
    "volumes": "volume",
    "firewalls": "firewall",
    "primary_ips": "primary_ip",
}


# --- lookups --------------------------------------------------------------------------


def listed(request: Request, actions: list[Action]) -> dict:
    """Return a page of actions, filtered by the `id` and `status` query parameters."""
    query = request.query_params
    ids = {int(i) for i in query.getlist("id") if i.isdigit()}
    if ids:
        actions = [a for a in actions if a["id"] in ids]
    statuses = {s for v in query.getlist("status") for s in v.split(",")}
    if statuses:
        actions = [a for a in actions if a["status"] in statuses]
    return paginate(request, sorted(actions, key=lambda a: a["id"]), "actions")


def resource_type_of(kind: str) -> str:
    """Return the resource type of an API path (`servers` → `server`), or raise 404."""
    if kind not in RESOURCE_TYPES:
        raise not_found("resource")
    return RESOURCE_TYPES[kind]


def resource_of(lab: Lab, kind: str, rid: str) -> tuple[str, int]:
    """Return the resource type and ID of /v1/<kind>/<rid>, or raise 404."""
    resource_type = resource_type_of(kind)
    # a kind named by the path: typed as any kind of record
    if not rid.isdigit() or int(rid) not in lab.state[KIND_TYPES[kind]]:
        raise not_found(resource_type)
    return resource_type, int(rid)


def touches(action: Action, resource_type: str, rid: int | None = None) -> bool:
    """Tell whether an action is about a resource type (or one resource of it)."""
    return any(
        r["type"] == resource_type and (rid is None or r["id"] == rid)
        for r in action["resources"]
    )


def action_or_404(
    lab: Lab, aid: int, resource_type: str | None = None, rid: int | None = None
) -> Action:
    """Return an action, or raise 404 (also if it is not about the resource)."""
    action = lab.state[ACTIONS].get(aid)
    if not action or (resource_type and not touches(action, resource_type, rid)):
        raise not_found("action")
    return action


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the action routes to the app.

    They must come before the resource routes, which would read "actions" as an ID.
    """
    state = lab.state

    @app.get("/v1/actions")
    async def action_list(request: Request):
        if not request.query_params.getlist("id"):
            raise invalid("id is required", "id")
        return listed(request, list(state[ACTIONS].values()))

    @app.get("/v1/actions/{aid}")
    async def action_get(aid: int):
        return {"action": action_or_404(lab, aid)}

    @app.get("/v1/{kind}/actions")
    async def kind_actions(
        request: Request, kind: str
    ):  # the actions of a resource kind
        resource_type = resource_type_of(kind)
        mine = [a for a in state[ACTIONS].values() if touches(a, resource_type)]
        return listed(request, mine)

    @app.get("/v1/{kind}/actions/{aid}")
    async def kind_action_get(kind: str, aid: int):
        return {"action": action_or_404(lab, aid, resource_type_of(kind))}

    @app.get("/v1/{kind}/{rid}/actions")
    async def resource_actions(request: Request, kind: str, rid: str):
        resource_type, resource_id = resource_of(lab, kind, rid)
        mine = [
            a for a in state[ACTIONS].values() if touches(a, resource_type, resource_id)
        ]
        return listed(request, mine)

    @app.get("/v1/{kind}/{rid}/actions/{aid}")
    async def resource_action_get(kind: str, rid: str, aid: int):
        resource_type, resource_id = resource_of(lab, kind, rid)
        return {"action": action_or_404(lab, aid, resource_type, resource_id)}
