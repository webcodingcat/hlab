"""Actions: what the API returns for every change, read back by clients while they wait. Listed per resource kind
or resource; the project-wide list only by ID (Hetzner, 30 January 2025)."""

from fastapi import FastAPI, Request

from .web import invalid, not_found, paginate

# the resource types of actions, by the API path of their resources
KINDS = {"servers": "server", "networks": "network", "floating_ips": "floating_ip", "volumes": "volume",
         "firewalls": "firewall", "primary_ips": "primary_ip"}


def add_routes(app: FastAPI, lab) -> None:
    """Registered before the resource routes: their /{id} would take "actions" for an ID."""
    state = lab.state

    def listed(request: Request, items: list[dict]) -> dict:
        q = request.query_params
        ids = {int(i) for i in q.getlist("id") if i.isdigit()}
        if ids:
            items = [a for a in items if a["id"] in ids]
        statuses = {s for v in q.getlist("status") for s in v.split(",")}
        if statuses:
            items = [a for a in items if a["status"] in statuses]
        return paginate(request, sorted(items, key=lambda a: a["id"]), "actions")

    def kind_of(kind: str) -> str:
        if kind not in KINDS:
            raise not_found("resource")
        return KINDS[kind]

    def resource(kind: str, rid: str) -> tuple[str, int]:
        """The resource type and ID of /v1/<kind>/<rid>, or 404."""
        t = kind_of(kind)
        if not rid.isdigit() or int(rid) not in state.data.get(kind, {}):
            raise not_found(t)
        return t, int(rid)

    def touches(a: dict, t: str, rid: int | None = None) -> bool:
        return any(r["type"] == t and (rid is None or r["id"] == rid) for r in a["resources"])

    def action_or_404(aid: int, t: str | None = None, rid: int | None = None) -> dict:
        a = state["actions"].get(aid)
        if not a or (t and not touches(a, t, rid)):
            raise not_found("action")
        return a

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
        t = kind_of(kind)
        return listed(request, [a for a in state["actions"].values() if touches(a, t)])

    @app.get("/v1/{kind}/actions/{aid}")
    def kind_action_get(kind: str, aid: int):
        return {"action": action_or_404(aid, kind_of(kind))}

    @app.get("/v1/{kind}/{rid}/actions")
    def resource_actions(request: Request, kind: str, rid: str):
        t, i = resource(kind, rid)
        return listed(request, [a for a in state["actions"].values() if touches(a, t, i)])

    @app.get("/v1/{kind}/{rid}/actions/{aid}")
    def resource_action_get(kind: str, rid: str, aid: int):
        t, i = resource(kind, rid)
        return {"action": action_or_404(aid, t, i)}
