"""Cloud firewalls: the API, with the checks of their rules. What the rules do (nftables, with Hetzner's semantics)
is in fabric/firewall.py; every change here rewrites them."""

import ipaddress

from fastapi import FastAPI, Request
from fastapi.responses import Response

from ..labels import firewall_servers
from . import hetzner as H
from .web import ApiError, body, filtered, invalid, limit_exceeded, not_found, paginate, unsupported

FIREWALLS_PER_SERVER = 5
PROTOCOLS = ("tcp", "udp", "icmp", "esp", "gre")
MAX_RULES = 50   # per firewall (Hetzner: 50 entries, 500 effective rules)


class RuleError(ValueError):
    pass


def validate_rules(rules) -> list[dict]:
    """Check rules like the API does and return them normalised (Hetzner's field set)."""
    if not isinstance(rules, list):
        raise RuleError("rules must be a list")
    if len(rules) > MAX_RULES:
        raise RuleError(f"at most {MAX_RULES} rules per firewall")
    out = []
    for i, r in enumerate(rules):
        if not isinstance(r, dict):
            raise RuleError(f"rules[{i}] must be an object")
        d, proto = r.get("direction"), r.get("protocol")
        if d not in ("in", "out"):
            raise RuleError(f"rules[{i}].direction must be in or out")
        if proto not in PROTOCOLS:
            raise RuleError(f"rules[{i}].protocol must be one of {', '.join(PROTOCOLS)}")
        port = r.get("port")
        if proto in ("tcp", "udp"):
            if not port:
                raise RuleError(f"rules[{i}].port is required for {proto}")
            parse_port(port, i)
        elif port:
            raise RuleError(f"rules[{i}].port is only allowed for tcp and udp")
        key = "source_ips" if d == "in" else "destination_ips"
        ips = r.get(key) or []
        if not ips:
            raise RuleError(f"rules[{i}].{key} must not be empty")
        for ip in ips:
            try:
                ipaddress.ip_network(ip, strict=True)
            except ValueError:
                raise RuleError(f"rules[{i}].{key}: {ip!r} is not a valid network") from None
        out.append({"direction": d, "protocol": proto, "port": port if proto in ("tcp", "udp") else None,
                    "source_ips": ips if d == "in" else [], "destination_ips": ips if d == "out" else [],
                    "description": r.get("description")})
    return out


def parse_port(port: str, i: int = 0) -> str:
    """'80', '80-85' or 'any' → nftables port expression."""
    if port == "any":
        return "1-65535"
    lo, _, hi = str(port).partition("-")
    try:
        a, b = int(lo), int(hi or lo)
    except ValueError:
        raise RuleError(f"rules[{i}].port must be a port, a range like 80-85 or any") from None
    if not (1 <= a <= b <= 65535):
        raise RuleError(f"rules[{i}].port out of range")
    return str(a) if a == b else f"{a}-{b}"


def add_routes(app: FastAPI, lab) -> None:
    state = lab.state

    def firewall_or_404(fid: int) -> dict:
        f = state["firewalls"].get(fid)
        if not f:
            raise not_found("firewall")
        return f

    def target(r: dict) -> tuple:
        """What an apply_to entry points at: (type, server id, label selector); clients send extra null fields."""
        return (r.get("type"), (r.get("server") or {}).get("id"), (r.get("label_selector") or {}).get("selector"))

    def resource(r: dict, missing: int = 422) -> dict:
        """An apply_to entry; a missing server is 422 on create, 404 in the actions (Hetzner's codes)."""
        t, sid, sel = target(r)
        if t == "server":
            if sid not in state["servers"]:
                raise ApiError(missing, "firewall_resource_not_found", f"server {sid} not found")
            s = state["servers"][sid]
            if not lab.fabric.ipv4(s) and not lab.fabric.ipv6_net(s):
                raise ApiError(422, "private_net_only_server", "the server has no public interface")
            return {"type": t, "server": {"id": sid}}
        if t == "label_selector" and sel:
            return {"type": t, "label_selector": {"selector": sel}}
        raise invalid("apply_to: type server with server.id or label_selector with a selector")

    def rules(given) -> list[dict]:
        try:
            return validate_rules(given or [])
        except RuleError as e:
            raise invalid(str(e)) from None

    def check_limit() -> None:
        for sid in state["servers"]:
            if sum(1 for f in state["firewalls"].values() if sid in firewall_servers(f, state)) > FIREWALLS_PER_SERVER:
                raise limit_exceeded(f"server {sid} would have more than {FIREWALLS_PER_SERVER} firewalls",
                                     "firewalls_per_server")

    def finished(fid: int, cmd: str, servers: set[int]) -> list[dict]:
        a = lab.new_action(cmd, [(fid, "firewall")] + [(s, "server") for s in sorted(servers)])
        lab.finish(a)
        return [a]

    @app.get("/v1/firewalls")
    def firewalls(request: Request):
        items = filtered(request, list(state["firewalls"].values()))
        return paginate(request, [H.firewall(state, f) for f in items], "firewalls")

    @app.post("/v1/firewalls", status_code=201)
    async def firewall_create(request: Request):
        b = await body(request)
        if not b.get("name"):
            raise invalid("name is required")
        given_rules = rules(b.get("rules"))
        applied = [resource(r) for r in b.get("apply_to") or []]
        servers = [target(r) for r in applied if r["type"] == "server"]
        if len(servers) != len(set(servers)):
            raise ApiError(422, "server_already_added", "a server is in apply_to more than once")
        with state.lock:
            if any(f["name"] == b["name"] for f in state["firewalls"].values()):
                raise ApiError(409, "uniqueness_error", "firewall name is already used")
            fid = state.new_id("firewalls")
            f = {"id": fid, "name": b["name"], "labels": b.get("labels") or {}, "created": H.now(),
                 "rules": given_rules, "applied_to": applied}
            state["firewalls"][fid] = f
            try:
                check_limit()
            except ApiError:
                del state["firewalls"][fid]
                raise
            state.save()
        await lab.blocking(lab.fabric.apply_firewalls)
        acts = finished(fid, "set_firewall_rules", set())
        if f["applied_to"]:
            acts += finished(fid, "apply_firewall", firewall_servers(f, state))
        return {"firewall": H.firewall(state, f), "actions": acts}

    @app.get("/v1/firewalls/{fid}")
    def firewall_get(fid: int):
        return {"firewall": H.firewall(state, firewall_or_404(fid))}

    @app.put("/v1/firewalls/{fid}")
    async def firewall_update(fid: int, request: Request):
        f = firewall_or_404(fid)
        b = await body(request)
        with state.lock:
            if b.get("name") and any(x["name"] == b["name"] and x is not f for x in state["firewalls"].values()):
                raise ApiError(409, "uniqueness_error", "firewall name is already used", {"fields": [{"name": "name"}]})
            if "name" in b:
                f["name"] = b["name"]
            if "labels" in b:
                f["labels"] = b["labels"] or {}
            state.save()
        return {"firewall": H.firewall(state, f)}

    @app.delete("/v1/firewalls/{fid}", status_code=204)
    def firewall_delete(fid: int):
        f = firewall_or_404(fid)
        if f["applied_to"]:
            raise ApiError(422, "resource_in_use", "firewall is still applied to resources")
        with state.lock:
            del state["firewalls"][fid]
            state.save()
        return Response(status_code=204)

    @app.post("/v1/firewalls/{fid}/actions/{cmd}", status_code=201)
    async def firewall_action(fid: int, cmd: str, request: Request):
        f = firewall_or_404(fid)
        b = await body(request)
        with state.lock:
            if cmd == "set_rules":
                f["rules"] = rules(b.get("rules"))
                servers = firewall_servers(f, state)
                acts = finished(fid, "set_firewall_rules", set())
                if servers:
                    acts += finished(fid, "apply_firewall", servers)
            elif cmd == "apply_to_resources":
                have = {target(r) for r in f["applied_to"]}
                new = [resource(r, 404) for r in b.get("apply_to") or []]
                if any(target(r) in have for r in new):
                    raise ApiError(422, "firewall_already_applied", "firewall is already applied to the resource")
                before = list(f["applied_to"])
                f["applied_to"] += new
                try:
                    check_limit()
                except ApiError:
                    f["applied_to"] = before
                    raise
                acts = finished(fid, "apply_firewall", firewall_servers(f, state))
            elif cmd == "remove_from_resources":
                remove = {target(r) for r in b.get("remove_from") or []}
                for t, sid, _ in remove:
                    if t != "server":
                        continue
                    if sid not in state["servers"]:
                        raise ApiError(404, "firewall_resource_not_found", f"server {sid} not found")
                    explicit = ("server", sid, None) in {target(r) for r in f["applied_to"]}
                    if not explicit and sid in firewall_servers(f, state):
                        raise ApiError(422, "firewall_managed_by_label_selector",
                                       "the firewall reaches this server through a label selector")
                before = firewall_servers(f, state)
                f["applied_to"] = [r for r in f["applied_to"] if target(r) not in remove]
                acts = finished(fid, "remove_firewall", before - firewall_servers(f, state))
            else:
                raise unsupported(f"the action {cmd!r}")
            state.save()
        await lab.blocking(lab.fabric.apply_firewalls)
        return {"actions": acts}
