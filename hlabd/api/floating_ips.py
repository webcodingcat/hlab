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
from ..state import FLOATING_IPS, SERVERS
from . import hetzner, system
from .catalogue import catalogue_name
from .public_addresses import first_free, record_dns_ptr
from .resources import change_delete_protection, update_resource
from .web import filtered, paginate, read_body, ref_id


def free_floating_ip(lab: Lab, ip_type: str) -> str:
    """Return the lowest free Floating IP address of a type.

    IPv6 uses 2001:db8:ff01::/64 to ...:fffd::/64; the lab uses fffe and ffff for its
    services and clients. The caller stores the new record in the same `change()`.
    """
    used = {x["ip"] for x in lab.state[FLOATING_IPS].values()}
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
            state[FLOATING_IPS].values(), body.get("name"), None, "Floating IP"
        )
        ip = free_floating_ip(lab, ip_type)
        fid = state.new_id(FLOATING_IPS)
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
        state[FLOATING_IPS][fid] = floating_ip
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
    floating_ip = lab.get_or_404(FLOATING_IPS, fid, "Floating IP")
    keys = ("name", "description", "labels")
    update_resource(lab, FLOATING_IPS, floating_ip, body, keys, "Floating IP")
    return {"floating_ip": hetzner.floating_ip_json(lab, floating_ip)}


def delete_floating_ip(lab: Lab, fid: int) -> None:
    """Delete a Floating IP that is not protected and not assigned."""
    floating_ip = lab.get_or_404(FLOATING_IPS, fid, "Floating IP")
    if floating_ip["protection"]["delete"]:
        raise protected("Floating IP is delete protected")
    if floating_ip.get("server"):  # Hetzner refuses this since 1 May 2026
        raise must_be_unassigned("the Floating IP is assigned; unassign it first")
    with lab.state.change():
        del lab.state[FLOATING_IPS][fid]


# Each Floating IP action below checks the request and returns its plan (lab.Plan).
# Assign and unassign move the route in the background, one move at a time
# (`_moving`). The other actions change only the record.


async def floating_ip_change_dns_ptr(
    lab: Lab, floating_ip: FloatingIP, body: dict
) -> Plan:
    """Set the reverse DNS of one of the Floating IP's addresses (named in the body)."""
    return record_dns_ptr(lab, floating_ip, body, default_ip="")


def move_floating_ip(lab: Lab, floating_ip: FloatingIP, target: Server | None) -> Plan:
    """Plan a move of the Floating IP to a server (or to none).

    As on Hetzner, the route moves after about 3 s. From then on, the server may send
    from the IP.
    """
    fid, ip = floating_ip["id"], floating_ip["ip"]
    target_id = target["id"] if target else None

    def save_move() -> Server | None:
        """Store the new assignment; return the target as it is now."""
        with lab.state.change():
            moved = lab.current(FLOATING_IPS, fid)
            target_now = lab.current(SERVERS, target_id) if target_id else None
            moved["server"] = target_id
        return target_now

    async def steps():
        if target_id is not None:
            await lab.sleep(FLOATING_IP_MOVE_SECONDS)
        target_now = save_move()
        await system.route_floating_ip(lab, ip, target_now)
        await system.apply_faults(lab)
        if target_id is not None:
            await system.apply_latency(lab)

    return Plan(steps, resources=[(target["id"], "server")] if target else [])


async def floating_ip_assign(lab: Lab, floating_ip: FloatingIP, body: dict) -> Plan:
    """Plan the move of the Floating IP to the server the body names."""
    server = lab.server_or_404(ref_id(body.get("server"), "server"))
    return move_floating_ip(lab, floating_ip, server)


async def floating_ip_unassign(lab: Lab, floating_ip: FloatingIP, body: dict) -> Plan:
    """Plan the move of the Floating IP to no server."""
    return move_floating_ip(lab, floating_ip, None)


# the Floating IP actions: Hetzner's name of the action and the function that plans it
FLOATING_IP_ACTIONS = ActionTable[FloatingIP](
    "floating_ip",
    {
        "change_protection": ActionSpec(
            "change_protection", change_delete_protection, flag=False
        ),
        "change_dns_ptr": ActionSpec(
            "change_dns_ptr", floating_ip_change_dns_ptr, flag=False
        ),
        "assign": ActionSpec("assign_floating_ip", floating_ip_assign),
        "unassign": ActionSpec("unassign_floating_ip", floating_ip_unassign),
    },
    flag="_moving",
    busy="the Floating IP is being moved",
)


async def run_floating_ip_action(lab: Lab, fid: int, cmd: str, body: dict) -> dict:
    """Run a Floating IP action: one at a time per Floating IP (423 otherwise)."""
    floating_ip = lab.get_or_404(FLOATING_IPS, fid, "Floating IP")
    return await lab.act(FLOATING_IP_ACTIONS, cmd, floating_ip, body)


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the Floating IP routes to the app."""
    state = lab.state

    @app.get("/v1/floating_ips")
    async def floating_ip_list(request: Request):
        items = filtered(request, list(state[FLOATING_IPS].values()))
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
        floating_ip = lab.get_or_404(FLOATING_IPS, fid, "Floating IP")
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
