"""Cloud firewalls: the API, with the checks of their rules.

What the rules do (nftables, with Hetzner's semantics) is in fabric/firewall.py; every
change here rewrites them.
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
    unsupported,
)
from ..lab import Lab, now
from ..labels import firewall_servers
from ..records import Action, Firewall, FirewallRule
from ..state import primary_address_of
from . import hetzner
from .web import filtered, paginate, read_body, update_resource

FIREWALLS_PER_SERVER = 5
PROTOCOLS = ("tcp", "udp", "icmp", "esp", "gre")
MAX_RULES = 50  # per firewall (Hetzner: 50 entries, 500 effective rules)


def checked_rules(rules) -> list[FirewallRule]:
    """Check rules like the API does (422 if one is wrong); return them normalised.

    The normalised rules have Hetzner's field set; no rules at all is an empty list.
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
    """Check that no server has more firewalls than Hetzner allows.

    The caller holds the state's lock.
    """
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


def finished_firewall_action(
    lab: Lab, firewall: Firewall, cmd: str, servers: set[int]
) -> Action:
    """Return the finished action of a firewall change, naming the servers it reaches.

    Firewall changes take effect at once.
    """
    resources = [(firewall["id"], "firewall")] + [
        (sid, "server") for sid in sorted(servers)
    ]
    return lab.finished_action(cmd, resources)


def rule_actions(lab: Lab, firewall: Firewall, created: bool = False) -> list[Action]:
    """Return the actions of new rules: set_firewall_rules, and apply_firewall.

    apply_firewall names the servers the firewall reaches. New rules get it only when
    there are such servers; a new firewall whenever it is applied to anything (a label
    selector may match no server yet).
    """
    actions = [finished_firewall_action(lab, firewall, "set_firewall_rules", set())]
    servers = firewall_servers(lab.state, firewall)
    if servers or (created and firewall["applied_to"]):
        actions.append(
            finished_firewall_action(lab, firewall, "apply_firewall", servers)
        )
    return actions


def create_firewall(lab: Lab, body: dict) -> dict:
    """Create a firewall with its rules and resources; return it and its actions."""
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
    with state.lock:
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
        state.save()
    lab.fabric.apply_firewalls()
    actions = rule_actions(lab, firewall, created=True)
    return {"firewall": hetzner.firewall_json(lab, firewall), "actions": actions}


def update_firewall(lab: Lab, fid: int, body: dict) -> dict:
    """Change a firewall's name or labels."""
    firewall = lab.get_or_404("firewalls", fid, "firewall")
    update_resource(lab, "firewalls", firewall, body, ("name", "labels"), "firewall")
    return {"firewall": hetzner.firewall_json(lab, firewall)}


def delete_firewall(lab: Lab, fid: int) -> None:
    """Delete a firewall that is applied to nothing (Hetzner)."""
    firewall = lab.get_or_404("firewalls", fid, "firewall")
    if firewall["applied_to"]:
        raise resource_in_use("firewall is still applied to resources")
    with lab.state.lock:
        del lab.state["firewalls"][fid]
        lab.state.save()


# --- firewall actions -----------------------------------------------------------------
# Each changes the firewall (under the state's lock) and returns its actions.


def set_rules(lab: Lab, firewall: Firewall, body: dict) -> list[Action]:
    """Replace the firewall's rules."""
    firewall["rules"] = checked_rules(body.get("rules"))
    return rule_actions(lab, firewall)


def apply_to_resources(lab: Lab, firewall: Firewall, body: dict) -> list[Action]:
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
    return [finished_firewall_action(lab, firewall, "apply_firewall", servers)]


def remove_from_resources(lab: Lab, firewall: Firewall, body: dict) -> list[Action]:
    """Remove the firewall from servers or label selectors.

    A server the firewall reaches only through a label selector cannot be removed.
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
    return [finished_firewall_action(lab, firewall, "remove_firewall", removed)]


# the firewall actions: the function that changes the firewall and returns its actions
FIREWALL_ACTIONS = {
    "set_rules": set_rules,
    "apply_to_resources": apply_to_resources,
    "remove_from_resources": remove_from_resources,
}


def run_firewall_action(lab: Lab, fid: int, cmd: str, body: dict) -> dict:
    """Run a firewall action and rewrite the firewall rules; return the actions."""
    firewall = lab.get_or_404("firewalls", fid, "firewall")
    if cmd not in FIREWALL_ACTIONS:
        raise unsupported(f"the action {cmd!r}")
    with lab.state.lock:
        actions = FIREWALL_ACTIONS[cmd](lab, firewall, body)
        lab.state.save()
    lab.fabric.apply_firewalls()
    return {"actions": actions}


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the firewall routes to the app."""
    state = lab.state

    @app.get("/v1/firewalls")
    def firewall_list(request: Request):
        with state.lock:
            items = filtered(request, list(state["firewalls"].values()))
            return paginate(
                request, [hetzner.firewall_json(lab, f) for f in items], "firewalls"
            )

    @app.post("/v1/firewalls", status_code=201)
    async def firewall_create(request: Request):
        return await lab.blocking(create_firewall, lab, await read_body(request))

    @app.get("/v1/firewalls/{fid}")
    def firewall_get(fid: int):
        return {
            "firewall": hetzner.firewall_json(
                lab, lab.get_or_404("firewalls", fid, "firewall")
            )
        }

    @app.put("/v1/firewalls/{fid}")
    async def firewall_update(fid: int, request: Request):
        return await lab.blocking(update_firewall, lab, fid, await read_body(request))

    @app.delete("/v1/firewalls/{fid}", status_code=204)
    def firewall_delete(fid: int):
        delete_firewall(lab, fid)
        return Response(status_code=204)

    @app.post("/v1/firewalls/{fid}/actions/{cmd}", status_code=201)
    async def firewall_action(fid: int, cmd: str, request: Request):
        return await lab.blocking(
            run_firewall_action, lab, fid, cmd, await read_body(request)
        )
