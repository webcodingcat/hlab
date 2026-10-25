"""Public addresses: primary IPs, Floating IPs and reverse DNS.

Primary IPs are a server's own IPv4 and IPv6 /64; Floating IPs move between servers
(~3 s). Addresses come from documentation ranges outside RFC 1918, so firewall rules
behave as in the cloud.
"""

import ipaddress

from fastapi import FastAPI, Request
from fastapi.responses import Response

from .. import config
from ..errors import (
    ApiError,
    invalid,
    limit_exceeded,
    locked,
    not_found,
    unique_name,
    unsupported,
)
from ..lab import now
from . import hetzner
from .catalogue import resolve
from .spec import hostname_ok
from .web import filtered, paginate, read_body

# --- reverse DNS ----------------------------------------------------------------------


def set_dns_ptr(record: dict, ip: str, ptr: str | None) -> None:
    """Set the reverse DNS entry of one address of a primary or Floating IP.

    None removes the entry.
    """
    if ptr is not None and not (isinstance(ptr, str) and hostname_ok(ptr)):
        raise invalid("dns_ptr must be a host name (without trailing dot)", "dns_ptr")
    record["dns_ptr"] = [x for x in record.get("dns_ptr", []) if x["ip"] != ip]
    if ptr:
        record["dns_ptr"].append({"ip": ip, "dns_ptr": ptr})


def contains(prefix: str, ip: str) -> bool:
    """Return whether an address belongs to an IP (a single address, or an IPv6 /64)."""
    try:
        return ipaddress.ip_address(ip) in ipaddress.ip_network(prefix)
    except ValueError:
        return False


def server_dns_ptr(lab, server: dict, body: dict) -> None:
    """Run change_dns_ptr of a server.

    The address must belong to one of the server's primary IPs.
    """
    ip = str(body.get("ip"))
    for kind in ("ipv4", "ipv6"):
        primary_ip = lab.state["primary_ips"].get(server.get(f"{kind}_id"))
        if primary_ip and contains(primary_ip["ip"], ip):
            set_dns_ptr(primary_ip, str(ipaddress.ip_address(ip)), body.get("dns_ptr"))
            return
    raise invalid("the IP does not belong to the server", "ip")


# --- primary IPs ----------------------------------------------------------------------


def free_ipv4(state) -> str:
    """Return the lowest public IPv4 that no primary IP has."""
    used = {p["ip"] for p in state["primary_ips"].values()}
    ip = next(
        (
            f"{config.PUBLIC_NET}.{n}"
            for n in range(11, 251)
            if f"{config.PUBLIC_NET}.{n}" not in used
        ),
        None,
    )
    if ip is None:
        raise limit_exceeded("the lab has no public IPv4 left", "primary_ips")
    return ip


def free_ipv6(state) -> str:
    """Return the lowest public IPv6 /64 that no primary IP has."""
    used = {p["ip"] for p in state["primary_ips"].values()}
    net = next(
        (
            hetzner.ipv6_net(n)
            for n in range(1, 0xFF00)
            if hetzner.ipv6_net(n) not in used
        ),
        None,
    )
    if net is None:
        raise limit_exceeded("the lab has no public IPv6 network left", "primary_ips")
    return net


def new_primary_ip(
    lab,
    kind: str,
    location: str,
    assignee: int | None = None,
    name: str | None = None,
    auto_delete: bool = False,
    labels: dict | None = None,
) -> dict:
    """Create a primary IP with a free address and return it."""
    state = lab.state
    pid = state.new_id("primary_ips")
    primary_ip = {
        "id": pid,
        "name": name or f"primary_ip-{pid}",
        "type": kind,
        "location": location,
        "ip": free_ipv4(state) if kind == "ipv4" else free_ipv6(state),
        "assignee_id": assignee,
        "auto_delete": auto_delete,
        "labels": labels or {},
        "protection": {"delete": False},
        "dns_ptr": [],
        "created": now(),
    }
    state["primary_ips"][pid] = primary_ip
    return primary_ip


def check_server_ips(lab, server: dict, public_net: dict) -> None:
    """Check that the primary IPs a new server asks for exist, are free and fit.

    Refusals use Hetzner's errors.
    """
    for kind in ("ipv4", "ipv6"):
        ref = public_net.get(kind)
        if ref is None:
            continue
        try:
            primary_ip = lab.state["primary_ips"].get(int(ref))
        except (TypeError, ValueError):
            primary_ip = None
        if not primary_ip:
            raise invalid(f"primary IP {ref} not found", f"public_net.{kind}")
        if primary_ip["type"] != kind:
            raise ApiError(
                412, "primary_ip_version_mismatch", f"primary IP {ref} is not an {kind}"
            )
        if primary_ip.get("assignee_id"):
            raise ApiError(
                422, "primary_ip_assigned", f"primary IP {ref} is already assigned"
            )
        if primary_ip["location"] != server["location"]:
            raise ApiError(
                422,
                "primary_ip_datacenter_mismatch",
                "primary IP is in another location",
            )


def setup_server_ips(lab, server: dict, public_net: dict) -> None:
    """Assign the primary IPs of a new server.

    These are the given ones (unassigned, same location) or new ones with auto_delete.
    """
    check_server_ips(lab, server, public_net)
    for kind in ("ipv4", "ipv6"):
        ref = public_net.get(kind)
        if ref is not None:
            primary_ip = lab.state["primary_ips"][int(ref)]
            primary_ip["assignee_id"] = server["id"]
            server[f"{kind}_id"] = primary_ip["id"]
        elif public_net.get(f"enable_{kind}", True):
            server[f"{kind}_id"] = new_primary_ip(
                lab, kind, server["location"], server["id"], auto_delete=True
            )["id"]


def release_server_ips(lab, server: dict) -> None:
    """Release a server's primary IPs when it is deleted.

    Primary IPs with auto_delete go, the others stay unassigned.
    """
    for kind in ("ipv4", "ipv6"):
        primary_ip = lab.state["primary_ips"].get(server.get(f"{kind}_id"))
        if not primary_ip:
            continue
        if primary_ip.get("auto_delete"):
            del lab.state["primary_ips"][primary_ip["id"]]
        else:
            primary_ip["assignee_id"] = None


def check_assignable(lab, server: dict, kind: str) -> None:
    """Check that a server can get a primary IP of a kind.

    The server must be unlocked, off, and have no primary IP of that kind.
    """
    if server.get("locked"):
        raise locked("server is locked by another action")
    if server["status"] != "off":
        raise ApiError(422, "server_not_stopped", "the server must be powered off")
    if lab.state["primary_ips"].get(server.get(f"{kind}_id")):
        raise ApiError(
            422,
            "server_has_ipv4" if kind == "ipv4" else "server_has_ipv6",
            f"the server already has a primary {kind}",
        )


def primary_ip_or_404(lab, pid: int) -> dict:
    """Return a primary IP, or raise not_found."""
    primary_ip = lab.state["primary_ips"].get(pid)
    if not primary_ip:
        raise not_found("primary IP")
    return primary_ip


def floating_ip_or_404(lab, fid: int) -> dict:
    """Return a Floating IP, or raise not_found."""
    floating_ip = lab.state["floating_ips"].get(fid)
    if not floating_ip:
        raise not_found("floating IP")
    return floating_ip


def primary_ip_action(lab, pid: int, cmd: str, body: dict) -> dict:
    """Run a primary IP action at once and return the finished action."""
    state = lab.state
    primary_ip = primary_ip_or_404(lab, pid)
    with state.lock:
        if cmd == "assign":
            server = state["servers"].get(int(body.get("assignee_id") or 0))
            if not server:
                raise invalid("server not found", "assignee_id")
            if primary_ip.get("assignee_id"):
                raise ApiError(
                    422, "primary_ip_already_assigned", "primary IP is already assigned"
                )
            if primary_ip["location"] != server["location"]:
                raise ApiError(
                    422,
                    "primary_ip_location_mismatch",
                    "primary IP is in another location",
                )
            check_assignable(lab, server, primary_ip["type"])
            primary_ip["assignee_id"], server[f"{primary_ip['type']}_id"] = (
                server["id"],
                primary_ip["id"],
            )
        elif cmd == "unassign":
            server = state["servers"].get(primary_ip.get("assignee_id"))
            if server:
                if server.get("locked"):
                    raise locked("server is locked by another action")
                if server["status"] != "off":
                    raise ApiError(
                        422, "server_not_stopped", "the server must be powered off"
                    )
                server[f"{primary_ip['type']}_id"] = None
            primary_ip["assignee_id"] = None
        elif cmd == "change_dns_ptr":
            ip = str(body.get("ip") or primary_ip["ip"])
            if not contains(primary_ip["ip"], ip):
                raise invalid("ip must be an address of the primary IP", "ip")
            set_dns_ptr(primary_ip, str(ipaddress.ip_address(ip)), body.get("dns_ptr"))
        elif cmd == "change_protection":
            # Hetzner: only for auto_delete false
            if body.get("delete") and primary_ip.get("auto_delete"):
                raise invalid("delete protection needs auto_delete false", "delete")
            primary_ip["protection"]["delete"] = bool(
                body.get("delete", primary_ip["protection"]["delete"])
            )
        else:
            raise unsupported(f"the action {cmd!r}")
        state.save()
    lab.fabric.write_dhcp()  # the server's DHCP hands out its (new) address
    name = {"assign": "assign_primary_ip", "unassign": "unassign_primary_ip"}.get(
        cmd, cmd
    )
    return {"action": lab.done(name, [(pid, "primary_ip")])}


# --- Floating IPs ---------------------------------------------------------------------


def create_floating_ip(lab, body: dict) -> dict:
    """Create a Floating IP, assigned to a server if the body names one."""
    state = lab.state
    kind = body.get("type")
    if kind not in ("ipv4", "ipv6"):
        raise invalid("type must be ipv4 or ipv6")
    server = lab.server(int(body["server"])) if body.get("server") else None
    home = body.get("home_location") or (server["location"] if server else None)
    if not home:
        raise invalid("home_location or server is required")
    home = resolve(lab, "location", home)
    with state.lock:
        unique_name(
            state["floating_ips"].values(), body.get("name"), None, "Floating IP"
        )
        # the lowest free address (IPv6: 2001:db8:ff01::/64 to ...:fffd::/64, clear of
        # the lab's own ff fe/ff)
        used = {x["ip"] for x in state["floating_ips"].values()}
        candidates = (
            [f"{config.FLOATING_NET}.{10 + n}" for n in range(1, 241)]
            if kind == "ipv4"
            else [f"{config.IPV6_PREFIX}:ff{n:02x}::/64" for n in range(1, 0xFE)]
        )
        ip = next((c for c in candidates if c not in used), None)
        if ip is None:
            raise limit_exceeded(f"the lab has no Floating {kind} left", "floating_ips")
        fid = state.new_id("floating_ips")
        floating_ip = {
            "id": fid,
            "name": body.get("name") or f"floating-ip-{fid}",
            "description": body.get("description"),
            "ip": ip,
            "type": kind,
            "server": None,
            "home_location": home,
            "labels": body.get("labels") or {},
            "protection": {"delete": False},
            "dns_ptr": [],
            "created": now(),
        }
        state["floating_ips"][fid] = floating_ip
        state.save()
    out = {
        "floating_ip": hetzner.floating_ip_json(lab.cfg, floating_ip),
        "action": None,
    }
    if server:
        out["action"] = floating_ip_action(
            lab, fid, "assign", {"server": server["id"]}
        )["action"]
    return out


def floating_ip_action(lab, fid: int, cmd: str, body: dict) -> dict:
    """Run a Floating IP action and return it.

    Assign and unassign move the route in the background (one move at a time:
    `_moving`); the others change the record only.
    """
    state = lab.state
    floating_ip = floating_ip_or_404(lab, fid)
    if cmd == "change_protection":
        floating_ip["protection"]["delete"] = bool(body.get("delete", False))
        return {"action": lab.done(cmd, [(fid, "floating_ip")])}
    if cmd == "change_dns_ptr":
        ip = str(body.get("ip") or "")
        if not contains(floating_ip["ip"], ip):
            raise invalid("ip must be an address of the Floating IP", "ip")
        set_dns_ptr(floating_ip, str(ipaddress.ip_address(ip)), body.get("dns_ptr"))
        return {"action": lab.done(cmd, [(fid, "floating_ip")])}
    if cmd == "assign":
        sid = body.get("server")
        if isinstance(sid, bool) or not (
            isinstance(sid, int) or (isinstance(sid, str) and sid.isdigit())
        ):
            raise invalid("server must be the ID of a server", "server")
        target = lab.server(int(sid))
    elif cmd != "unassign":
        raise unsupported(f"the action {cmd!r}")
    else:
        target = None
    with state.lock:
        if floating_ip.get("_moving"):
            raise locked("the Floating IP is being moved")
        floating_ip["_moving"] = True
    action = (
        lab.new_action(
            "assign_floating_ip", [(fid, "floating_ip"), (target["id"], "server")]
        )
        if target
        else lab.new_action("unassign_floating_ip", [(fid, "floating_ip")])
    )

    async def steps():
        try:
            if target:
                await lab.sleep(3)  # the route moves after ~3 s
            with state.lock:
                if target and (
                    fid not in state["floating_ips"]
                    or target["id"] not in state["servers"]
                ):
                    raise RuntimeError(
                        "the Floating IP or the server was deleted meanwhile"
                    )
                floating_ip["server"] = target["id"] if target else None
                state.save()
            await lab.blocking(lab.fabric.route_floating, floating_ip)
            # the server may send from it now
            await lab.blocking(lab.fabric.apply_faults)
            if target:
                await lab.blocking(lab.fabric.apply_latency)
        finally:
            floating_ip.pop("_moving", None)

    lab.run_action(action, steps)
    return {"action": action}


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab) -> None:
    """Add the primary IP and Floating IP routes to the app."""
    cfg, state = lab.cfg, lab.state

    @app.get("/v1/primary_ips")
    def primary_ips(request: Request):
        items = filtered(
            request,
            list(state["primary_ips"].values()),
            {"ip": lambda p, v: p["ip"] == v},
        )
        return paginate(
            request, [hetzner.primary_ip_json(cfg, p) for p in items], "primary_ips"
        )

    @app.post("/v1/primary_ips", status_code=201)
    async def primary_ip_create(request: Request):
        body = await read_body(request)
        unique_name(state["primary_ips"].values(), body["name"], None, "primary IP")
        if body.get("location") and body.get("assignee_id"):
            raise invalid("location and assignee_id are mutually exclusive", "location")
        assignee = (
            state["servers"].get(int(body["assignee_id"]))
            if body.get("assignee_id")
            else None
        )
        if body.get("assignee_id") and not assignee:
            raise invalid("server not found", "assignee_id")
        location = body.get("location") or (assignee or {}).get("location")
        if not location:
            raise invalid("location or assignee_id is required", "location")
        location = resolve(lab, "location", location)
        if assignee:
            check_assignable(lab, assignee, body["type"])
        with state.lock:
            primary_ip = new_primary_ip(
                lab,
                body["type"],
                location,
                assignee["id"] if assignee else None,
                body["name"],
                bool(body.get("auto_delete", False)),
                body.get("labels"),
            )
            if assignee:
                assignee[f"{body['type']}_id"] = primary_ip["id"]
            state.save()
        action = None  # Hetzner: an action only when the IP is assigned
        if assignee:
            action = lab.new_action(
                "assign_primary_ip",
                [(primary_ip["id"], "primary_ip"), (assignee["id"], "server")],
            )
            lab.finish(action)
            await lab.blocking(lab.fabric.write_dhcp)
        return {
            "primary_ip": hetzner.primary_ip_json(cfg, primary_ip),
            "action": action,
        }

    @app.get("/v1/primary_ips/{pid}")
    def primary_ip_get(pid: int):
        return {"primary_ip": hetzner.primary_ip_json(cfg, primary_ip_or_404(lab, pid))}

    @app.put("/v1/primary_ips/{pid}")
    async def primary_ip_update(pid: int, request: Request):
        primary_ip = primary_ip_or_404(lab, pid)
        body = await read_body(request)
        unique_name(
            state["primary_ips"].values(), body.get("name"), primary_ip, "primary IP"
        )
        if body.get("auto_delete") and primary_ip["protection"]["delete"]:
            raise invalid(
                "a delete protected primary IP cannot be auto_delete", "auto_delete"
            )
        for k in ("name", "labels", "auto_delete"):
            if k in body:
                primary_ip[k] = body[k] if k != "labels" else (body[k] or {})
        state.save()
        return {"primary_ip": hetzner.primary_ip_json(cfg, primary_ip)}

    @app.delete("/v1/primary_ips/{pid}", status_code=204)
    def primary_ip_delete(pid: int):
        primary_ip = primary_ip_or_404(lab, pid)
        if primary_ip["protection"]["delete"]:
            raise ApiError(423, "protected", "primary IP is delete protected")
        if primary_ip.get("assignee_id"):
            raise ApiError(
                422,
                "must_be_unassigned",
                "primary IP is assigned to a server; unassign it first",
            )
        del state["primary_ips"][pid]
        state.save()
        return Response(status_code=204)

    @app.post("/v1/primary_ips/{pid}/actions/{cmd}", status_code=201)
    async def primary_ip_post_action(pid: int, cmd: str, request: Request):
        return await lab.blocking(
            primary_ip_action, lab, pid, cmd, await read_body(request)
        )

    @app.get("/v1/floating_ips")
    def floating_ips(request: Request):
        items = filtered(request, list(state["floating_ips"].values()))
        return paginate(
            request, [hetzner.floating_ip_json(cfg, f) for f in items], "floating_ips"
        )

    @app.post("/v1/floating_ips", status_code=201)
    async def floating_ip_create(request: Request):
        return await lab.blocking(create_floating_ip, lab, await read_body(request))

    @app.get("/v1/floating_ips/{fid}")
    def floating_ip_get(fid: int):
        return {
            "floating_ip": hetzner.floating_ip_json(cfg, floating_ip_or_404(lab, fid))
        }

    @app.put("/v1/floating_ips/{fid}")
    async def floating_ip_update(fid: int, request: Request):
        floating_ip = floating_ip_or_404(lab, fid)
        body = await read_body(request)
        with state.lock:
            unique_name(
                state["floating_ips"].values(),
                body.get("name"),
                floating_ip,
                "Floating IP",
            )
            for k in ("name", "description", "labels"):
                if k in body:
                    floating_ip[k] = body[k] if k != "labels" else (body[k] or {})
            state.save()
        return {"floating_ip": hetzner.floating_ip_json(cfg, floating_ip)}

    @app.delete("/v1/floating_ips/{fid}", status_code=204)
    def floating_ip_delete(fid: int):
        floating_ip = floating_ip_or_404(lab, fid)
        if floating_ip["protection"]["delete"]:
            raise ApiError(423, "protected", "floating IP is delete protected")
        if floating_ip.get("server"):  # Hetzner since 1 May 2026
            raise ApiError(
                422,
                "must_be_unassigned",
                "the Floating IP is assigned; unassign it first",
            )
        with state.lock:
            del state["floating_ips"][fid]
            state.save()
        return Response(status_code=204)

    @app.post("/v1/floating_ips/{fid}/actions/{cmd}", status_code=201)
    async def floating_ip_post_action(fid: int, cmd: str, request: Request):
        return await lab.blocking(
            floating_ip_action, lab, fid, cmd, await read_body(request)
        )
