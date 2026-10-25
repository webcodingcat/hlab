"""Cloud firewalls: the API, with the checks of their rules.

What the rules do (nftables, with Hetzner's semantics) is in fabric/firewall.py; every
change here rewrites them.
"""

import ipaddress

from fastapi import FastAPI, Request
from fastapi.responses import Response

from ..errors import (
    ApiError,
    invalid,
    limit_exceeded,
    not_found,
    unique_name,
    unsupported,
)
from ..lab import now
from ..labels import firewall_servers
from . import hetzner
from .web import filtered, paginate, read_body

FIREWALLS_PER_SERVER = 5
PROTOCOLS = ("tcp", "udp", "icmp", "esp", "gre")
MAX_RULES = 50  # per firewall (Hetzner: 50 entries, 500 effective rules)


class RuleError(ValueError):
    """A firewall rule that the API refuses."""


def validate_rules(rules) -> list[dict]:
    """Check rules like the API does and return them normalised.

    The normalised rules have Hetzner's field set.
    """
    if not isinstance(rules, list):
        raise RuleError("rules must be a list")
    if len(rules) > MAX_RULES:
        raise RuleError(f"at most {MAX_RULES} rules per firewall")
    out = []
    for i, r in enumerate(rules):
        if not isinstance(r, dict):
            raise RuleError(f"rules[{i}] must be an object")
        direction, proto = r.get("direction"), r.get("protocol")
        if direction not in ("in", "out"):
            raise RuleError(f"rules[{i}].direction must be in or out")
        if proto not in PROTOCOLS:
            raise RuleError(
                f"rules[{i}].protocol must be one of {', '.join(PROTOCOLS)}"
            )
        port = r.get("port")
        if proto in ("tcp", "udp"):
            if not port:
                raise RuleError(f"rules[{i}].port is required for {proto}")
            parse_port(port, i)
        elif port:
            raise RuleError(f"rules[{i}].port is only allowed for tcp and udp")
        key = "source_ips" if direction == "in" else "destination_ips"
        ips = r.get(key) or []
        if not ips:
            raise RuleError(f"rules[{i}].{key} must not be empty")
        for ip in ips:
            try:
                ipaddress.ip_network(ip, strict=True)
            except ValueError:
                raise RuleError(
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


def parse_port(port: str, index: int = 0) -> str:
    """Turn '80', '80-85' or 'any' into an nftables port expression."""
    if port == "any":
        return "1-65535"
    low, _, high = str(port).partition("-")
    try:
        first, last = int(low), int(high or low)
    except ValueError:
        raise RuleError(
            f"rules[{index}].port must be a port, a range like 80-85 or any"
        ) from None
    if not (1 <= first <= last <= 65535):
        raise RuleError(f"rules[{index}].port out of range")
    return str(first) if first == last else f"{first}-{last}"


def firewall_or_404(lab, fid: int) -> dict:
    """Return a firewall, or raise not_found."""
    firewall = lab.state["firewalls"].get(fid)
    if not firewall:
        raise not_found("firewall")
    return firewall


def target(entry: dict) -> tuple:
    """Return what an apply_to entry points at: (type, server id, label selector).

    Clients send extra null fields, which are ignored.
    """
    server = entry.get("server") or {}
    selector = entry.get("label_selector") or {}
    return entry.get("type"), server.get("id"), selector.get("selector")


def resource(lab, entry: dict, missing: int = 422) -> dict:
    """Check an apply_to entry and return it normalised.

    A missing server is 422 on create, 404 in the actions (Hetzner's codes).
    """
    entry_type, sid, selector = target(entry)
    if entry_type == "server":
        server = lab.state["servers"].get(sid)
        if not server:
            raise ApiError(
                missing, "firewall_resource_not_found", f"server {sid} not found"
            )
        if not lab.fabric.ipv4(server) and not lab.fabric.ipv6_net(server):
            raise ApiError(
                422, "private_net_only_server", "the server has no public interface"
            )
        return {"type": entry_type, "server": {"id": sid}}
    if entry_type == "label_selector" and selector:
        return {"type": entry_type, "label_selector": {"selector": selector}}
    raise invalid(
        "apply_to: type server with server.id or label_selector with a selector"
    )


def checked_rules(given) -> list[dict]:
    """Return the checked rules, or raise invalid_input."""
    try:
        return validate_rules(given or [])
    except RuleError as error:
        raise invalid(str(error)) from None


def check_limit(lab) -> None:
    """Check that no server has more firewalls than Hetzner allows."""
    state = lab.state
    for sid in state["servers"]:
        if (
            sum(
                1
                for f in state["firewalls"].values()
                if sid in firewall_servers(f, state)
            )
            > FIREWALLS_PER_SERVER
        ):
            raise limit_exceeded(
                f"server {sid} would have more than {FIREWALLS_PER_SERVER} firewalls",
                "firewalls_per_server",
            )


def done(lab, firewall: dict, cmd: str, servers: set[int]) -> list[dict]:
    """Return a finished action for a firewall change.

    Firewall changes take effect at once; their actions name the servers they reach.
    """
    return [
        lab.done(
            cmd,
            [(firewall["id"], "firewall")] + [(s, "server") for s in sorted(servers)],
        )
    ]


def create_firewall(lab, body: dict) -> dict:
    """Create a firewall with its rules and resources; return it and its actions."""
    state = lab.state
    if not body.get("name"):
        raise invalid("name is required")
    rules = checked_rules(body.get("rules"))
    applied = [resource(lab, r) for r in body.get("apply_to") or []]
    servers = [target(r) for r in applied if r["type"] == "server"]
    if len(servers) != len(set(servers)):
        raise ApiError(
            422, "server_already_added", "a server is in apply_to more than once"
        )
    with state.lock:
        unique_name(state["firewalls"].values(), body["name"], None, "firewall")
        fid = state.new_id("firewalls")
        firewall = {
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
        state.save()
    lab.fabric.apply_firewalls()
    actions = done(lab, firewall, "set_firewall_rules", set())
    if firewall["applied_to"]:
        actions += done(
            lab, firewall, "apply_firewall", firewall_servers(firewall, state)
        )
    return {"firewall": hetzner.firewall_json(state, firewall), "actions": actions}


# --- firewall actions -----------------------------------------------------------------
# Each changes the firewall (under the state's lock) and returns its actions.


def set_rules(lab, firewall: dict, body: dict) -> list[dict]:
    """Replace the firewall's rules."""
    firewall["rules"] = checked_rules(body.get("rules"))
    servers = firewall_servers(firewall, lab.state)
    actions = done(lab, firewall, "set_firewall_rules", set())
    if servers:
        actions += done(lab, firewall, "apply_firewall", servers)
    return actions


def apply_to_resources(lab, firewall: dict, body: dict) -> list[dict]:
    """Apply the firewall to more servers or label selectors."""
    have = {target(r) for r in firewall["applied_to"]}
    new = [resource(lab, r, 404) for r in body.get("apply_to") or []]
    if any(target(r) in have for r in new):
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
    return done(lab, firewall, "apply_firewall", firewall_servers(firewall, lab.state))


def remove_from_resources(lab, firewall: dict, body: dict) -> list[dict]:
    """Remove the firewall from servers or label selectors.

    A server the firewall reaches only through a label selector cannot be removed.
    """
    state = lab.state
    remove = {target(r) for r in body.get("remove_from") or []}
    for t, sid, _ in remove:
        if t != "server":
            continue
        if sid not in state["servers"]:
            raise ApiError(
                404, "firewall_resource_not_found", f"server {sid} not found"
            )
        explicit = ("server", sid, None) in {target(r) for r in firewall["applied_to"]}
        if not explicit and sid in firewall_servers(firewall, state):
            raise ApiError(
                422,
                "firewall_managed_by_label_selector",
                "the firewall reaches this server through a label selector",
            )
    before = firewall_servers(firewall, state)
    firewall["applied_to"] = [
        r for r in firewall["applied_to"] if target(r) not in remove
    ]
    return done(
        lab, firewall, "remove_firewall", before - firewall_servers(firewall, state)
    )


ACTIONS = {
    "set_rules": set_rules,
    "apply_to_resources": apply_to_resources,
    "remove_from_resources": remove_from_resources,
}


def firewall_action(lab, fid: int, cmd: str, body: dict) -> dict:
    """Run a firewall action and rewrite the firewall rules; return the actions."""
    firewall = firewall_or_404(lab, fid)
    if cmd not in ACTIONS:
        raise unsupported(f"the action {cmd!r}")
    with lab.state.lock:
        actions = ACTIONS[cmd](lab, firewall, body)
        lab.state.save()
    lab.fabric.apply_firewalls()
    return {"actions": actions}


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab) -> None:
    """Add the firewall routes to the app."""
    state = lab.state

    @app.get("/v1/firewalls")
    def firewalls(request: Request):
        items = filtered(request, list(state["firewalls"].values()))
        return paginate(
            request, [hetzner.firewall_json(state, f) for f in items], "firewalls"
        )

    @app.post("/v1/firewalls", status_code=201)
    async def firewall_create(request: Request):
        return await lab.blocking(create_firewall, lab, await read_body(request))

    @app.get("/v1/firewalls/{fid}")
    def firewall_get(fid: int):
        return {"firewall": hetzner.firewall_json(state, firewall_or_404(lab, fid))}

    @app.put("/v1/firewalls/{fid}")
    async def firewall_update(fid: int, request: Request):
        firewall = firewall_or_404(lab, fid)
        body = await read_body(request)
        with state.lock:
            unique_name(
                state["firewalls"].values(), body.get("name"), firewall, "firewall"
            )
            for k in ("name", "labels"):
                if k in body:
                    firewall[k] = body[k] if k != "labels" else (body[k] or {})
            state.save()
        return {"firewall": hetzner.firewall_json(state, firewall)}

    @app.delete("/v1/firewalls/{fid}", status_code=204)
    def firewall_delete(fid: int):
        firewall = firewall_or_404(lab, fid)
        if firewall["applied_to"]:
            raise ApiError(
                422, "resource_in_use", "firewall is still applied to resources"
            )
        with state.lock:
            del state["firewalls"][fid]
            state.save()
        return Response(status_code=204)

    @app.post("/v1/firewalls/{fid}/actions/{cmd}", status_code=201)
    async def firewall_action_route(fid: int, cmd: str, request: Request):
        return await lab.blocking(
            firewall_action, lab, fid, cmd, await read_body(request)
        )
