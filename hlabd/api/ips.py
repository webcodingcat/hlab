"""Public addresses: primary IPs (a server's own IPv4 and IPv6 /64), Floating IPs (moved between servers, ~3 s) and
reverse DNS. Addresses come from documentation ranges outside RFC 1918, so firewall rules behave as in the cloud."""

import ipaddress

from fastapi import FastAPI, Request
from fastapi.responses import Response

from .. import config as C
from . import hetzner as H
from .catalogue import resolve
from .spec import hostname_ok
from .web import ApiError, body, filtered, invalid, limit_exceeded, not_found, paginate, unsupported


# --- reverse DNS ---------------------------------------------------------------------------------------------------

def set_dns_ptr(p: dict, ip: str, ptr: str | None) -> None:
    """The reverse DNS entry of one address of a primary or Floating IP (None removes it)."""
    if ptr is not None and not (isinstance(ptr, str) and hostname_ok(ptr)):
        raise invalid("dns_ptr must be a host name (without trailing dot)", "dns_ptr")
    p["dns_ptr"] = [x for x in p.get("dns_ptr", []) if x["ip"] != ip]
    if ptr:
        p["dns_ptr"].append({"ip": ip, "dns_ptr": ptr})


def contains(prefix: str, ip: str) -> bool:
    """Whether an address belongs to an IP (a single address, or an IPv6 /64)."""
    try:
        return ipaddress.ip_address(ip) in ipaddress.ip_network(prefix)
    except ValueError:
        return False


def server_dns_ptr(lab, s: dict, b: dict) -> None:
    """change_dns_ptr of a server: the address belongs to one of its primary IPs."""
    ip = str(b.get("ip"))
    for kind in ("ipv4", "ipv6"):
        p = lab.state["primary_ips"].get(s.get(f"{kind}_id"))
        if p and contains(p["ip"], ip):
            set_dns_ptr(p, str(ipaddress.ip_address(ip)), b.get("dns_ptr"))
            return
    raise invalid("the IP does not belong to the server", "ip")


# --- primary IPs ---------------------------------------------------------------------------------------------------

def free_ipv4(state) -> str:
    used = {p["ip"] for p in state["primary_ips"].values()}
    ip = next((f"{C.PUBLIC_NET}.{n}" for n in range(11, 251) if f"{C.PUBLIC_NET}.{n}" not in used), None)
    if ip is None:
        raise limit_exceeded("the lab has no public IPv4 left", "primary_ips")
    return ip


def free_ipv6(state) -> str:
    used = {p["ip"] for p in state["primary_ips"].values()}
    net = next((H.ipv6_net(n) for n in range(1, 0xff00) if H.ipv6_net(n) not in used), None)
    if net is None:
        raise limit_exceeded("the lab has no public IPv6 network left", "primary_ips")
    return net


def new_primary_ip(lab, kind: str, location: str, assignee: int | None = None, name: str | None = None,
                   auto_delete: bool = False, labels: dict | None = None) -> dict:
    state = lab.state
    pid = state.new_id("primary_ips")
    p = {"id": pid, "name": name or f"primary_ip-{pid}", "type": kind, "location": location,
         "ip": free_ipv4(state) if kind == "ipv4" else free_ipv6(state), "assignee_id": assignee,
         "auto_delete": auto_delete, "labels": labels or {}, "protection": {"delete": False}, "dns_ptr": [],
         "created": H.now()}
    state["primary_ips"][pid] = p
    return p


def check_server_ips(lab, s: dict, public_net: dict) -> None:
    """The primary IPs a new server asks for exist, are free and fit (Hetzner's errors)."""
    for kind in ("ipv4", "ipv6"):
        ref = public_net.get(kind)
        if ref is None:
            continue
        try:
            p = lab.state["primary_ips"].get(int(ref))
        except (TypeError, ValueError):
            p = None
        if not p:
            raise invalid(f"primary IP {ref} not found", f"public_net.{kind}")
        if p["type"] != kind:
            raise ApiError(412, "primary_ip_version_mismatch", f"primary IP {ref} is not an {kind}")
        if p.get("assignee_id"):
            raise ApiError(422, "primary_ip_assigned", f"primary IP {ref} is already assigned")
        if p["location"] != s["location"]:
            raise ApiError(422, "primary_ip_datacenter_mismatch", "primary IP is in another location")


def setup_server_ips(lab, s: dict, public_net: dict) -> None:
    """Primary IPs of a new server: given ones (unassigned, same location) or new ones with auto_delete."""
    check_server_ips(lab, s, public_net)
    for kind in ("ipv4", "ipv6"):
        ref = public_net.get(kind)
        if ref is not None:
            p = lab.state["primary_ips"][int(ref)]
            p["assignee_id"] = s["id"]
            s[f"{kind}_id"] = p["id"]
        elif public_net.get(f"enable_{kind}", True):
            s[f"{kind}_id"] = new_primary_ip(lab, kind, s["location"], s["id"], auto_delete=True)["id"]


def release_server_ips(lab, s: dict) -> None:
    """On server deletion: primary IPs with auto_delete go, the others stay unassigned."""
    for kind in ("ipv4", "ipv6"):
        p = lab.state["primary_ips"].get(s.get(f"{kind}_id"))
        if not p:
            continue
        if p.get("auto_delete"):
            del lab.state["primary_ips"][p["id"]]
        else:
            p["assignee_id"] = None


def check_assignable(lab, s: dict, kind: str) -> None:
    """A primary IP is assigned to a server that is off and has none of that kind."""
    if s.get("locked"):
        raise ApiError(423, "locked", "server is locked by another action")
    if s["status"] != "off":
        raise ApiError(422, "server_not_stopped", "the server must be powered off")
    if lab.state["primary_ips"].get(s.get(f"{kind}_id")):
        raise ApiError(422, "server_has_ipv4" if kind == "ipv4" else "server_has_ipv6",
                       f"the server already has a primary {kind}")


def primary_ip_action(lab, pid: int, cmd: str, b: dict) -> dict:
    state = lab.state
    p = state["primary_ips"].get(pid)
    if not p:
        raise not_found("primary IP")
    with state.lock:
        if cmd == "assign":
            s = state["servers"].get(int(b.get("assignee_id") or 0))
            if not s:
                raise invalid("server not found", "assignee_id")
            if p.get("assignee_id"):
                raise ApiError(422, "primary_ip_already_assigned", "primary IP is already assigned")
            if p["location"] != s["location"]:
                raise ApiError(422, "primary_ip_location_mismatch", "primary IP is in another location")
            check_assignable(lab, s, p["type"])
            p["assignee_id"], s[f"{p['type']}_id"] = s["id"], p["id"]
        elif cmd == "unassign":
            s = state["servers"].get(p.get("assignee_id"))
            if s:
                if s.get("locked"):
                    raise ApiError(423, "locked", "server is locked by another action")
                if s["status"] != "off":
                    raise ApiError(422, "server_not_stopped", "the server must be powered off")
                s[f"{p['type']}_id"] = None
            p["assignee_id"] = None
        elif cmd == "change_dns_ptr":
            ip = str(b.get("ip") or p["ip"])
            if not contains(p["ip"], ip):
                raise invalid("ip must be an address of the primary IP", "ip")
            set_dns_ptr(p, str(ipaddress.ip_address(ip)), b.get("dns_ptr"))
        elif cmd == "change_protection":
            if b.get("delete") and p.get("auto_delete"):   # Hetzner: only for auto_delete false
                raise invalid("delete protection needs auto_delete false", "delete")
            p["protection"]["delete"] = bool(b.get("delete", p["protection"]["delete"]))
        else:
            raise unsupported(f"the action {cmd!r}")
        state.save()
    lab.fabric.write_dhcp()   # the server's DHCP hands out its (new) address
    a = lab.new_action({"assign": "assign_primary_ip", "unassign": "unassign_primary_ip"}.get(cmd, cmd),
                       [(pid, "primary_ip")])
    lab.finish(a)
    return {"action": a}


# --- Floating IPs --------------------------------------------------------------------------------------------------

def create_floating_ip(lab, b: dict) -> dict:
    state = lab.state
    kind = b.get("type")
    if kind not in ("ipv4", "ipv6"):
        raise invalid("type must be ipv4 or ipv6")
    server = lab.server(int(b["server"])) if b.get("server") else None
    home = b.get("home_location") or (server["location"] if server else None)
    if not home:
        raise invalid("home_location or server is required")
    home = resolve(lab, "location", home)
    with state.lock:
        if b.get("name") and any(x["name"] == b["name"] for x in state["floating_ips"].values()):
            raise ApiError(409, "uniqueness_error", "Floating IP name is already used", {"fields": [{"name": "name"}]})
        # the lowest free address (IPv6: 2001:db8:ff01::/64 to ...:fffd::/64, clear of the lab's own ff fe/ff)
        used = {x["ip"] for x in state["floating_ips"].values()}
        candidates = ([f"{C.FLOATING_NET}.{10 + n}" for n in range(1, 241)] if kind == "ipv4"
                      else [f"{C.IPV6_PREFIX}:ff{n:02x}::/64" for n in range(1, 0xfe)])
        ip = next((c for c in candidates if c not in used), None)
        if ip is None:
            raise limit_exceeded(f"the lab has no Floating {kind} left", "floating_ips")
        fid = state.new_id("floating_ips")
        f = {"id": fid, "name": b.get("name") or f"floating-ip-{fid}", "description": b.get("description"),
             "ip": ip, "type": kind, "server": None, "home_location": home, "labels": b.get("labels") or {},
             "protection": {"delete": False}, "dns_ptr": [], "created": H.now()}
        state["floating_ips"][fid] = f
        state.save()
    out = {"floating_ip": H.floating_ip(lab.cfg, f), "action": None}
    if server:
        out["action"] = floating_ip_action(lab, fid, "assign", {"server": server["id"]})["action"]
    return out


def floating_ip_action(lab, fid: int, cmd: str, b: dict) -> dict:
    """assign and unassign move the route (one move at a time: `_moving`); the others change the record only."""
    state = lab.state
    f = state["floating_ips"].get(fid)
    if not f:
        raise not_found("floating IP")
    if cmd == "change_protection":
        f["protection"]["delete"] = bool(b.get("delete", False))
        return finished(lab, fid, cmd)
    if cmd == "change_dns_ptr":
        ip = str(b.get("ip") or "")
        if not contains(f["ip"], ip):
            raise invalid("ip must be an address of the Floating IP", "ip")
        set_dns_ptr(f, str(ipaddress.ip_address(ip)), b.get("dns_ptr"))
        return finished(lab, fid, cmd)
    if cmd == "assign":
        sid = b.get("server")
        if isinstance(sid, bool) or not (isinstance(sid, int) or (isinstance(sid, str) and sid.isdigit())):
            raise invalid("server must be the ID of a server", "server")
        target = lab.server(int(sid))
    elif cmd != "unassign":
        raise unsupported(f"the action {cmd!r}")
    else:
        target = None
    with state.lock:
        if f.get("_moving"):
            raise ApiError(423, "locked", "the Floating IP is being moved")
        f["_moving"] = True
    a = (lab.new_action("assign_floating_ip", [(fid, "floating_ip"), (target["id"], "server")]) if target
         else lab.new_action("unassign_floating_ip", [(fid, "floating_ip")]))

    async def steps():
        try:
            if target:
                await lab.sleep(3)   # the route moves after ~3 s
            with state.lock:
                if target and (fid not in state["floating_ips"] or target["id"] not in state["servers"]):
                    raise RuntimeError("the Floating IP or the server was deleted meanwhile")
                f["server"] = target["id"] if target else None
                state.save()
            await lab.blocking(lab.fabric.route_floating, f)
            await lab.blocking(lab.fabric.apply_faults)   # the server may send from it now
            if target:
                await lab.blocking(lab.fabric.apply_latency)
        finally:
            f.pop("_moving", None)
    lab.run_action(a, steps)
    return {"action": a}


def finished(lab, fid: int, cmd: str) -> dict:
    a = lab.new_action(cmd, [(fid, "floating_ip")])
    lab.finish(a)
    return {"action": a}


# --- routes ------------------------------------------------------------------------------------------------------

def add_routes(app: FastAPI, lab) -> None:
    cfg, state = lab.cfg, lab.state

    def pip_or_404(pid: int) -> dict:
        p = state["primary_ips"].get(pid)
        if not p:
            raise not_found("primary IP")
        return p

    def fip_or_404(fid: int) -> dict:
        f = state["floating_ips"].get(fid)
        if not f:
            raise not_found("floating IP")
        return f

    @app.get("/v1/primary_ips")
    def primary_ips(request: Request):
        items = filtered(request, list(state["primary_ips"].values()), {"ip": lambda p, v: p["ip"] == v})
        return paginate(request, [H.primary_ip(cfg, p) for p in items], "primary_ips")

    @app.post("/v1/primary_ips", status_code=201)
    async def primary_ip_create(request: Request):
        b = await body(request)
        if any(p["name"] == b["name"] for p in state["primary_ips"].values()):
            raise ApiError(409, "uniqueness_error", "primary IP name is already used", {"fields": [{"name": "name"}]})
        if b.get("location") and b.get("assignee_id"):
            raise invalid("location and assignee_id are mutually exclusive", "location")
        assignee = state["servers"].get(int(b["assignee_id"])) if b.get("assignee_id") else None
        if b.get("assignee_id") and not assignee:
            raise invalid("server not found", "assignee_id")
        loc = b.get("location") or (assignee or {}).get("location")
        if not loc:
            raise invalid("location or assignee_id is required", "location")
        loc = resolve(lab, "location", loc)
        if assignee:
            check_assignable(lab, assignee, b["type"])
        with state.lock:
            p = new_primary_ip(lab, b["type"], loc, assignee["id"] if assignee else None, b["name"],
                               bool(b.get("auto_delete", False)), b.get("labels"))
            if assignee:
                assignee[f"{b['type']}_id"] = p["id"]
            state.save()
        a = None   # Hetzner: an action only when the IP is assigned
        if assignee:
            a = lab.new_action("assign_primary_ip", [(p["id"], "primary_ip"), (assignee["id"], "server")])
            lab.finish(a)
            await lab.blocking(lab.fabric.write_dhcp)
        return {"primary_ip": H.primary_ip(cfg, p), "action": a}

    @app.get("/v1/primary_ips/{pid}")
    def primary_ip_get(pid: int):
        return {"primary_ip": H.primary_ip(cfg, pip_or_404(pid))}

    @app.put("/v1/primary_ips/{pid}")
    async def primary_ip_update(pid: int, request: Request):
        p = pip_or_404(pid)
        b = await body(request)
        if b.get("name") and any(x["name"] == b["name"] and x is not p for x in state["primary_ips"].values()):
            raise ApiError(409, "uniqueness_error", "primary IP name is already used", {"fields": [{"name": "name"}]})
        if b.get("auto_delete") and p["protection"]["delete"]:
            raise invalid("a delete protected primary IP cannot be auto_delete", "auto_delete")
        for k in ("name", "labels", "auto_delete"):
            if k in b:
                p[k] = b[k] if k != "labels" else (b[k] or {})
        state.save()
        return {"primary_ip": H.primary_ip(cfg, p)}

    @app.delete("/v1/primary_ips/{pid}", status_code=204)
    def primary_ip_delete(pid: int):
        p = pip_or_404(pid)
        if p["protection"]["delete"]:
            raise ApiError(423, "protected", "primary IP is delete protected")
        if p.get("assignee_id"):
            raise ApiError(422, "must_be_unassigned", "primary IP is assigned to a server; unassign it first")
        del state["primary_ips"][pid]
        state.save()
        return Response(status_code=204)

    @app.post("/v1/primary_ips/{pid}/actions/{cmd}", status_code=201)
    async def primary_ip_post_action(pid: int, cmd: str, request: Request):
        return await lab.blocking(primary_ip_action, lab, pid, cmd, await body(request))

    @app.get("/v1/floating_ips")
    def floating_ips(request: Request):
        items = filtered(request, list(state["floating_ips"].values()))
        return paginate(request, [H.floating_ip(cfg, f) for f in items], "floating_ips")

    @app.post("/v1/floating_ips", status_code=201)
    async def floating_ip_create(request: Request):
        return await lab.blocking(create_floating_ip, lab, await body(request))

    @app.get("/v1/floating_ips/{fid}")
    def floating_ip_get(fid: int):
        return {"floating_ip": H.floating_ip(cfg, fip_or_404(fid))}

    @app.put("/v1/floating_ips/{fid}")
    async def floating_ip_update(fid: int, request: Request):
        f = fip_or_404(fid)
        b = await body(request)
        with state.lock:
            if b.get("name") and any(x["name"] == b["name"] and x is not f for x in state["floating_ips"].values()):
                raise ApiError(409, "uniqueness_error", "Floating IP name is already used",
                               {"fields": [{"name": "name"}]})
            for k in ("name", "description", "labels"):
                if k in b:
                    f[k] = b[k] if k != "labels" else (b[k] or {})
            state.save()
        return {"floating_ip": H.floating_ip(cfg, f)}

    @app.delete("/v1/floating_ips/{fid}", status_code=204)
    def floating_ip_delete(fid: int):
        f = fip_or_404(fid)
        if f["protection"]["delete"]:
            raise ApiError(423, "protected", "floating IP is delete protected")
        if f.get("server"):   # Hetzner since 1 May 2026
            raise ApiError(422, "must_be_unassigned", "the Floating IP is assigned; unassign it first")
        with state.lock:
            del state["floating_ips"][fid]
            state.save()
        return Response(status_code=204)

    @app.post("/v1/floating_ips/{fid}/actions/{cmd}", status_code=201)
    async def floating_ip_post_action(fid: int, cmd: str, request: Request):
        return await lab.blocking(floating_ip_action, lab, fid, cmd, await body(request))
