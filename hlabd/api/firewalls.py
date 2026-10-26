"""Cloud firewalls: the API, with the checks of their rules.

fabric/firewall.py turns the rules into nftables, with Hetzner's semantics. Every
change here rewrites the nftables rules.
"""

import ipaddress

from fastapi import FastAPI, Request
from fastapi.responses import Response

from ..errors import (
    ApiError,
    check_unique_name,
    firewall_resource_not_found,
    invalid,
    limit_exceeded,
    resource_in_use,
)
from ..lab import ActionSpec, ActionTable, Lab, Plan, now
from ..labels import firewall_servers
from ..records import Firewall, FirewallRule
from ..state import primary_address_of
from . import hetzner, system
from .web import filtered, paginate, read_body, update_resource

FIREWALLS_PER_SERVER = 5
PROTOCOLS = ("tcp", "udp", "icmp", "esp", "gre")
MAX_RULES = 50  # per firewall (Hetzner: 50 entries, 500 effective rules)


def checked_rules(rules) -> list[FirewallRule]:
    """Check rules like Hetzner's API (422 if one is wrong); return them normalised.

    Normalised rules have exactly Hetzner's fields. No rules give an empty list.
    """
    rules = rules or []
    if not isinstance(rules, list):
        raise invalid("rules must be a list")
    if len(rules) > MAX_RULES:
        raise invalid(f"at most {MAX_RULES} rules per firewall")
    out: list[FirewallRule] = []
    for i, r in enumerate(rules):
        if not isinstance(r, dict):
            raise invalid(f"rules[{i}] must be an object")
        direction, proto = r.get("direction"), r.get("protocol")
        if direction not in ("in", "out"):
            raise invalid(f"rules[{i}].direction must be in or out")
        if proto not in PROTOCOLS:
            raise invalid(f"rules[{i}].protocol must be one of {', '.join(PROTOCOLS)}")
        port = r.get("port")
        if proto in ("tcp", "udp"):
            if not port:
                raise invalid(f"rules[{i}].port is required for {proto}")
            check_port(port, i)
        elif port:
            raise invalid(f"rules[{i}].port is only allowed for tcp and udp")
        key = "source_ips" if direction == "in" else "destination_ips"
        ips = r.get(key) or []
        if not ips:
            raise invalid(f"rules[{i}].{key} must not be empty")
        for ip in ips:
            try:
                ipaddress.ip_network(ip, strict=True)
            except ValueError:
                raise invalid(
                    f"rules[{i}].{key}: {ip!r} is not a valid network"
                ) from None
        out.append(
            {
                "direction": direction,
                "protocol": proto,
                "port": port if proto in ("tcp", "udp") else None,
                "source_ips": ips if direction == "in" else [],
                "destination_ips": ips if direction == "out" else [],
                "description": r.get("description"),
            }
        )
    return out


def check_port(port: str, index: int = 0) -> None:
    """Check a rule's port: '80', a range like '80-85', or 'any'."""
    if port == "any":
        return
    low, _, high = str(port).partition("-")
    try:
        first, last = int(low), int(high or low)
    except ValueError:
        raise invalid(
            f"rules[{index}].port must be a port, a range like 80-85 or any"
        ) from None
    if not (1 <= first <= last <= 65535):
        raise invalid(f"rules[{index}].port out of range")


def apply_to_key(entry: dict) -> tuple:
    """Return what an apply_to entry points at: (type, server id, label selector).

    Clients send extra null fields, which are ignored.
    """
    server = entry.get("server") or {}
    selector = entry.get("label_selector") or {}
    return entry.get("type"), server.get("id"), selector.get("selector")


def checked_apply_to(lab: Lab, entry: dict, missing_status: int = 422) -> dict:
    """Check an apply_to entry and return it normalised.

    A missing server is 422 on create, 404 in the actions (Hetzner's codes).
    """
    entry_type, sid, selector = apply_to_key(entry)
    if entry_type == "server":
        server = lab.state["servers"].get(sid)
        if not server:
            raise firewall_resource_not_found(sid, missing_status)
        public = [
            primary_address_of(lab.state, server, kind) for kind in ("ipv4", "ipv6")
        ]
        if not any(public):
            raise ApiError(
                422, "private_net_only_server", "the server has no public interface"
            )
        return {"type": entry_type, "server": {"id": sid}}
    if entry_type == "label_selector" and selector:
        return {"type": entry_type, "label_selector": {"selector": selector}}
    raise invalid(
        "apply_to: type server with server.id or label_selector with a selector"
    )


def check_limit(lab: Lab) -> None:
    """Check that no server has more firewalls than Hetzner allows."""
    state = lab.state
    for sid in state["servers"]:
        if (
            sum(
                1
                for f in state["firewalls"].values()
                if sid in firewall_servers(state, f)
            )
            > FIREWALLS_PER_SERVER
        ):
            raise limit_exceeded(
                f"server {sid} would have more than {FIREWALLS_PER_SERVER} firewalls",
                "firewalls_per_server",
            )


def server_resources(servers: set[int]) -> list[tuple[int, str]]:
    """Return the (id, type) resources of the servers a firewall action names."""
    return [(sid, "server") for sid in sorted(servers)]


async def create_firewall(lab: Lab, body: dict) -> dict:
    """Create a firewall with its rules and resources; return it and its actions.

    Like a change of the rules, it brings the actions set_firewall_rules and
    apply_firewall. apply_firewall comes whenever the new firewall is applied to
    anything, even a label selector that matches no server yet.
    """
    state = lab.state
    if not body.get("name"):
        raise invalid("name is required")
    rules = checked_rules(body.get("rules"))
    applied = [checked_apply_to(lab, r) for r in body.get("apply_to") or []]
    servers = [apply_to_key(r) for r in applied if r["type"] == "server"]
    if len(servers) != len(set(servers)):
        raise ApiError(
            422, "server_already_added", "a server is in apply_to more than once"
        )
    with state.change():
        check_unique_name(state["firewalls"].values(), body["name"], None, "firewall")
        fid = state.new_id("firewalls")
        firewall: Firewall = {
            "id": fid,
            "name": body["name"],
            "labels": body.get("labels") or {},
            "created": now(),
            "rules": rules,
            "applied_to": applied,
        }
        state["firewalls"][fid] = firewall
        try:
            check_limit(lab)
        except ApiError:
            del state["firewalls"][fid]
            raise
    await system.apply_firewall(lab)
    resource = (fid, "firewall")
    actions = [lab.finished_action("set_firewall_rules", [resource])]
    reached = firewall_servers(state, firewall)
    if reached or applied:
        actions.append(
            lab.finished_action(
                "apply_firewall", [resource, *server_resources(reached)]
            )
        )
    return {"firewall": hetzner.firewall_json(lab, firewall), "actions": actions}


def update_firewall(lab: Lab, fid: int, body: dict) -> dict:
    """Change a firewall's name or labels."""
    firewall = lab.get_or_404("firewalls", fid, "firewall")
    update_resource(lab, "firewalls", firewall, body, ("name", "labels"), "firewall")
    return {"firewall": hetzner.firewall_json(lab, firewall)}


def delete_firewall(lab: Lab, fid: int) -> None:
    """Delete a firewall; as on Hetzner, refuse one that still applies to anything."""
    firewall = lab.get_or_404("firewalls", fid, "firewall")
    if firewall["applied_to"]:
        raise resource_in_use("firewall is still applied to resources")
    with lab.state.change():
        del lab.state["firewalls"][fid]


# --- firewall actions -----------------------------------------------------------------
# As on Hetzner, they take effect at once, take no flag, and answer with a list of
# finished actions: the action itself, which names the servers it changes, and the
# actions in `Plan.finished`.


def set_rules(lab: Lab, firewall: Firewall, body: dict) -> Plan:
    """Replace the firewall's rules.

    apply_firewall follows, naming the servers the firewall applies to, if there are
    any.
    """
    firewall["rules"] = checked_rules(body.get("rules"))
    servers = firewall_servers(lab.state, firewall)
    if not servers:
        return Plan()
    resources = [(firewall["id"], "firewall"), *server_resources(servers)]
    return Plan(finished=[("apply_firewall", resources)])


def apply_to_resources(lab: Lab, firewall: Firewall, body: dict) -> Plan:
    """Apply the firewall to more servers or label selectors."""
    have = {apply_to_key(r) for r in firewall["applied_to"]}
    new = [checked_apply_to(lab, r, 404) for r in body.get("apply_to") or []]
    if any(apply_to_key(r) in have for r in new):
        raise ApiError(
            422,
            "firewall_already_applied",
            "firewall is already applied to the resource",
        )
    before = list(firewall["applied_to"])
    firewall["applied_to"] += new
    try:
        check_limit(lab)
    except ApiError:
        firewall["applied_to"] = before
        raise
    servers = firewall_servers(lab.state, firewall)
    return Plan(resources=server_resources(servers))


def remove_from_resources(lab: Lab, firewall: Firewall, body: dict) -> Plan:
    """Remove the firewall from servers or label selectors.

    A server that the firewall applies to only through a label selector cannot be
    removed.
    """
    state = lab.state
    remove = {apply_to_key(r) for r in body.get("remove_from") or []}
    for entry_type, sid, _ in remove:
        if entry_type != "server":
            continue
        if sid not in state["servers"]:
            raise firewall_resource_not_found(sid, 404)
        explicit = ("server", sid, None) in {
            apply_to_key(r) for r in firewall["applied_to"]
        }
        if not explicit and sid in firewall_servers(state, firewall):
            raise ApiError(
                422,
                "firewall_managed_by_label_selector",
                "the firewall reaches this server through a label selector",
            )
    before = firewall_servers(state, firewall)
    firewall["applied_to"] = [
        r for r in firewall["applied_to"] if apply_to_key(r) not in remove
    ]
    removed = before - firewall_servers(state, firewall)
    return Plan(resources=server_resources(removed))


FIREWALL_ACTIONS: ActionTable[Firewall] = {
    "set_rules": ActionSpec("set_firewall_rules", set_rules, answer="actions"),
    "apply_to_resources": ActionSpec(
        "apply_firewall", apply_to_resources, answer="actions"
    ),
    "remove_from_resources": ActionSpec(
        "remove_firewall", remove_from_resources, answer="actions"
    ),
}


async def run_firewall_action(lab: Lab, fid: int, cmd: str, body: dict) -> dict:
    """Run a firewall action, then rewrite the firewall rules; return the actions."""
    firewall = lab.get_or_404("firewalls", fid, "firewall")
    answer = await lab.act(FIREWALL_ACTIONS, cmd, firewall, "firewall", body)
    await system.apply_firewall(lab)
    return answer


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the firewall routes to the app."""
    state = lab.state

    @app.get("/v1/firewalls")
    async def firewall_list(request: Request):
        items = filtered(request, list(state["firewalls"].values()))
        return paginate(
            request, [hetzner.firewall_json(lab, f) for f in items], "firewalls"
        )

    @app.post("/v1/firewalls", status_code=201)
    async def firewall_create(request: Request):
        return await create_firewall(lab, await read_body(request))

    @app.get("/v1/firewalls/{fid}")
    async def firewall_get(fid: int):
        return {
            "firewall": hetzner.firewall_json(
                lab, lab.get_or_404("firewalls", fid, "firewall")
            )
        }

    @app.put("/v1/firewalls/{fid}")
    async def firewall_update(fid: int, request: Request):
        return update_firewall(lab, fid, await read_body(request))

    @app.delete("/v1/firewalls/{fid}", status_code=204)
    async def firewall_delete(fid: int):
        delete_firewall(lab, fid)
        return Response(status_code=204)

    @app.post("/v1/firewalls/{fid}/actions/{cmd}", status_code=201)
    async def firewall_action(fid: int, cmd: str, request: Request):
        return await run_firewall_action(lab, fid, cmd, await read_body(request))
