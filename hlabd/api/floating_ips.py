"""Floating IPs: public addresses that move between servers (~3 s)."""

from fastapi import FastAPI, Request
from fastapi.responses import Response

from .. import config
from ..errors import (
    check_unique_name,
    invalid,
    limit_exceeded,
    must_be_unassigned,
    protected,
)
from ..lab import FLOATING_IP_MOVE_SECONDS, ActionSpec, ActionTable, Lab, Plan, now
from ..records import FloatingIP, Server
from . import hetzner, system
from .catalogue import catalogue_name
from .public_addresses import first_free, record_dns_ptr
from .web import (
    change_delete_protection,
    filtered,
    paginate,
    read_body,
    ref_id,
    update_resource,
)


def free_floating_ip(lab: Lab, ip_type: str) -> str:
    """Return the lowest free Floating IP address of a type.

    IPv6 uses 2001:db8:ff01::/64 to ...:fffd::/64; the lab uses fffe and ffff for its
    services and clients. The caller stores the new record in the same `change()`.
    """
    used = {x["ip"] for x in lab.state["floating_ips"].values()}
    if ip_type == "ipv4":
        candidates = (f"{config.FLOATING_NET}.{10 + n}" for n in range(1, 241))
    else:
        candidates = (f"{config.IPV6_PREFIX}:ff{n:02x}::/64" for n in range(1, 0xFE))
    error = limit_exceeded(f"the lab has no Floating {ip_type} left", "floating_ips")
    return first_free(candidates, used, error)


async def create_floating_ip(lab: Lab, body: dict) -> dict:
    """Create a Floating IP, assigned to a server if the body names one."""
    state = lab.state
    ip_type = body.get("type")
    if ip_type not in ("ipv4", "ipv6"):
        raise invalid("type must be ipv4 or ipv6")
    server = None
    if body.get("server"):
        server = lab.server_or_404(ref_id(body["server"], "server"))
    home = body.get("home_location") or (server["location"] if server else None)
    if not home:
        raise invalid("home_location or server is required")
    home = catalogue_name(lab, "location", home, "home_location")
    with state.change():
        check_unique_name(
            state["floating_ips"].values(), body.get("name"), None, "Floating IP"
        )
        ip = free_floating_ip(lab, ip_type)
        fid = state.new_id("floating_ips")
        floating_ip: FloatingIP = {
            "id": fid,
            "name": body.get("name") or f"floating-ip-{fid}",
            "description": body.get("description"),
            "ip": ip,
            "type": ip_type,
            "server": None,
            "home_location": home,
            "labels": body.get("labels") or {},
            "protection": {"delete": False},
            "dns_ptr": [],
            "created": now(),
        }
        state["floating_ips"][fid] = floating_ip
    out = {
        "floating_ip": hetzner.floating_ip_json(lab, floating_ip),
        "action": None,
    }
    if server:
        assigned = await run_floating_ip_action(
            lab, fid, "assign", {"server": server["id"]}
        )
        out["action"] = assigned["action"]
    return out


def update_floating_ip(lab: Lab, fid: int, body: dict) -> dict:
    """Change a Floating IP's name, description or labels."""
    floating_ip = lab.get_or_404("floating_ips", fid, "Floating IP")
    keys = ("name", "description", "labels")
    update_resource(lab, "floating_ips", floating_ip, body, keys, "Floating IP")
    return {"floating_ip": hetzner.floating_ip_json(lab, floating_ip)}


def delete_floating_ip(lab: Lab, fid: int) -> None:
    """Delete a Floating IP that is not protected and not assigned."""
    floating_ip = lab.get_or_404("floating_ips", fid, "Floating IP")
    if floating_ip["protection"]["delete"]:
        raise protected("Floating IP is delete protected")
    if floating_ip.get("server"):  # Hetzner refuses this since 1 May 2026
        raise must_be_unassigned("the Floating IP is assigned; unassign it first")
    with lab.state.change():
        del lab.state["floating_ips"][fid]


# Each Floating IP action below checks the request and returns its plan (lab.Plan).
# Assign and unassign move the route in the background, one move at a time
# (`_moving`). The other actions change only the record.


def floating_ip_change_dns_ptr(lab: Lab, floating_ip: FloatingIP, body: dict) -> Plan:
    """Set the reverse DNS of one of the Floating IP's addresses (named in the body)."""
    return record_dns_ptr(lab, floating_ip, body, default_ip="")


def move_floating_ip(lab: Lab, floating_ip: FloatingIP, target: Server | None) -> Plan:
    """Plan a move of the Floating IP to a server (or to none).

    As on Hetzner, the route moves after about 3 s. From then on, the server may send
    from the IP.
    """
    state = lab.state

    def save_move() -> None:
        with state.change():
            deleted = floating_ip["id"] not in state["floating_ips"] or (
                target and target["id"] not in state["servers"]
            )
            if deleted:
                raise RuntimeError(
                    "the Floating IP or the server was deleted meanwhile"
                )
            floating_ip["server"] = target["id"] if target else None

    async def steps():
        if target:
            await lab.sleep(FLOATING_IP_MOVE_SECONDS)
        save_move()
        await system.route_floating_ip(lab, floating_ip["ip"], target)
        await system.apply_faults(lab)
        if target:
            await system.apply_latency(lab)

    return Plan(steps, resources=[(target["id"], "server")] if target else [])


def floating_ip_assign(lab: Lab, floating_ip: FloatingIP, body: dict) -> Plan:
    """Plan the move of the Floating IP to the server the body names."""
    server = lab.server_or_404(ref_id(body.get("server"), "server"))
    return move_floating_ip(lab, floating_ip, server)


def floating_ip_unassign(lab: Lab, floating_ip: FloatingIP, body: dict) -> Plan:
    """Plan the move of the Floating IP to no server."""
    return move_floating_ip(lab, floating_ip, None)


# the Floating IP actions: Hetzner's name of the action and the function that plans it
FLOATING_IP_ACTIONS: ActionTable[FloatingIP] = {
    "change_protection": ActionSpec(
        "change_protection", change_delete_protection, flag=False
    ),
    "change_dns_ptr": ActionSpec(
        "change_dns_ptr", floating_ip_change_dns_ptr, flag=False
    ),
    "assign": ActionSpec("assign_floating_ip", floating_ip_assign),
    "unassign": ActionSpec("unassign_floating_ip", floating_ip_unassign),
}


async def run_floating_ip_action(lab: Lab, fid: int, cmd: str, body: dict) -> dict:
    """Run a Floating IP action: one at a time per Floating IP (423 otherwise)."""
    floating_ip = lab.get_or_404("floating_ips", fid, "Floating IP")
    busy = "the Floating IP is being moved"
    return await lab.act(
        FLOATING_IP_ACTIONS, cmd, floating_ip, "floating_ip", body, "_moving", busy
    )


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the Floating IP routes to the app."""
    state = lab.state

    @app.get("/v1/floating_ips")
    async def floating_ip_list(request: Request):
        items = filtered(request, list(state["floating_ips"].values()))
        return paginate(
            request,
            [hetzner.floating_ip_json(lab, f) for f in items],
            "floating_ips",
        )

    @app.post("/v1/floating_ips", status_code=201)
    async def floating_ip_create(request: Request):
        return await create_floating_ip(lab, await read_body(request))

    @app.get("/v1/floating_ips/{fid}")
    async def floating_ip_get(fid: int):
        floating_ip = lab.get_or_404("floating_ips", fid, "Floating IP")
        return {"floating_ip": hetzner.floating_ip_json(lab, floating_ip)}

    @app.put("/v1/floating_ips/{fid}")
    async def floating_ip_update(fid: int, request: Request):
        body = await read_body(request)
        return update_floating_ip(lab, fid, body)

    @app.delete("/v1/floating_ips/{fid}", status_code=204)
    async def floating_ip_delete(fid: int):
        delete_floating_ip(lab, fid)
        return Response(status_code=204)

    @app.post("/v1/floating_ips/{fid}/actions/{cmd}", status_code=201)
    async def floating_ip_action(fid: int, cmd: str, request: Request):
        return await run_floating_ip_action(lab, fid, cmd, await read_body(request))
