"""Actions: what the API returns for every change, read back by clients while they wait.

Listed per resource kind or resource; the project-wide list only by ID (Hetzner,
30 January 2025).
"""

from fastapi import FastAPI, Request

from ..errors import invalid, not_found
from .web import paginate

# the resource types of actions, by the API path of their resources
KINDS = {
    "servers": "server",
    "networks": "network",
    "floating_ips": "floating_ip",
    "volumes": "volume",
    "firewalls": "firewall",
    "primary_ips": "primary_ip",
}


def add_routes(app: FastAPI, lab) -> None:
    """Register the action routes.

    Registered before the resource routes: their /{id} would take "actions" for an ID.
    """
    state = lab.state

    def listed(request: Request, items: list[dict]) -> dict:
        query = request.query_params
        ids = {int(i) for i in query.getlist("id") if i.isdigit()}
        if ids:
            items = [a for a in items if a["id"] in ids]
        statuses = {s for v in query.getlist("status") for s in v.split(",")}
        if statuses:
            items = [a for a in items if a["status"] in statuses]
        return paginate(request, sorted(items, key=lambda a: a["id"]), "actions")

    def kind_of(kind: str) -> str:
        if kind not in KINDS:
            raise not_found("resource")
        return KINDS[kind]

    def resource(kind: str, rid: str) -> tuple[str, int]:
        """Return the resource type and ID of /v1/<kind>/<rid>, or raise a 404."""
        resource_type = kind_of(kind)
        if not rid.isdigit() or int(rid) not in state.data.get(kind, {}):
            raise not_found(resource_type)
        return resource_type, int(rid)

    def touches(action: dict, resource_type: str, rid: int | None = None) -> bool:
        return any(
            r["type"] == resource_type and (rid is None or r["id"] == rid)
            for r in action["resources"]
        )

    def action_or_404(
        aid: int, resource_type: str | None = None, rid: int | None = None
    ) -> dict:
        action = state["actions"].get(aid)
        if not action or (resource_type and not touches(action, resource_type, rid)):
            raise not_found("action")
        return action

    @app.get("/v1/actions")
    def actions(request: Request):
        if not request.query_params.getlist("id"):
            raise invalid("id is required", "id")
        return listed(request, list(state["actions"].values()))

    @app.get("/v1/actions/{aid}")
    def action_get(aid: int):
        return {"action": action_or_404(aid)}

    @app.get("/v1/{kind}/actions")
    def kind_actions(request: Request, kind: str):
        resource_type = kind_of(kind)
        return listed(
            request, [a for a in state["actions"].values() if touches(a, resource_type)]
        )

    @app.get("/v1/{kind}/actions/{aid}")
    def kind_action_get(kind: str, aid: int):
        return {"action": action_or_404(aid, kind_of(kind))}

    @app.get("/v1/{kind}/{rid}/actions")
    def resource_actions(request: Request, kind: str, rid: str):
        resource_type, resource_id = resource(kind, rid)
        return listed(
            request,
            [
                a
                for a in state["actions"].values()
                if touches(a, resource_type, resource_id)
            ],
        )

    @app.get("/v1/{kind}/{rid}/actions/{aid}")
    def resource_action_get(kind: str, rid: str, aid: int):
        resource_type, resource_id = resource(kind, rid)
        return {"action": action_or_404(aid, resource_type, resource_id)}
