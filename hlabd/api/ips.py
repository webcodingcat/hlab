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
    check_unique_name,
    invalid,
    limit_exceeded,
    locked,
    must_be_unassigned,
    protected,
    server_not_stopped,
)
from ..lab import FLOATING_IP_MOVE_SECONDS, SERVER_LOCKED, Lab, Plan, now
from ..records import FloatingIP, PrimaryIP, Server
from ..state import primary_ip_field, primary_ip_of, server_by_id
from . import hetzner
from .catalogue import catalogue_name
from .spec import hostname_ok
from .web import (
    change_delete_protection,
    filtered,
    paginate,
    read_body,
    ref_id,
    update_resource,
)

# --- addresses ------------------------------------------------------------------------


def server_ipv6_net(number: int) -> str:
    """Return the n-th /64 of the lab's prefix (a server's primary IPv6)."""
    return f"{config.IPV6_PREFIX}:{number:x}::/64"


def first_free(candidates, used: set[str], error: ApiError) -> str:
    """Return the first candidate address that is not used, or raise the error."""
    address = next((c for c in candidates if c not in used), None)
    if address is None:
        raise error
    return address


def contains(prefix: str, ip: str) -> bool:
    """Return whether an address belongs to an IP (a single address, or an IPv6 /64)."""
    try:
        return ipaddress.ip_address(ip) in ipaddress.ip_network(prefix)
    except ValueError:
        return False


def require_off(server: Server) -> None:
    """Refuse a change of a server's primary IPs unless it is off and unlocked."""
    if server.get("locked"):
        raise locked(SERVER_LOCKED)
    if server["status"] != "off":
        raise server_not_stopped()


# --- reverse DNS ----------------------------------------------------------------------


def set_dns_ptr(record: PrimaryIP | FloatingIP, ip: str, ptr: str | None) -> None:
    """Set the reverse DNS entry of one address of a primary or Floating IP.

    None removes the entry. The caller holds the state's lock.
    """
    if ptr is not None and not (isinstance(ptr, str) and hostname_ok(ptr)):
        raise invalid("dns_ptr must be a host name (without trailing dot)", "dns_ptr")
    record["dns_ptr"] = [x for x in record.get("dns_ptr", []) if x["ip"] != ip]
    if ptr:
        record["dns_ptr"].append({"ip": ip, "dns_ptr": ptr})


def record_dns_ptr(
    lab: Lab, record: PrimaryIP | FloatingIP, body: dict, default_ip: str
) -> Plan:
    """Set the reverse DNS of one of a primary or Floating IP's addresses.

    The address is the body's `ip`, else `default_ip` (empty: the body must name one).
    """
    ip = str(body.get("ip") or default_ip)
    if not contains(record["ip"], ip):
        raise invalid("ip must be an address of the IP", "ip")
    with lab.state.lock:
        set_dns_ptr(record, str(ipaddress.ip_address(ip)), body.get("dns_ptr"))
    return Plan()


def server_dns_ptr(lab: Lab, server: Server, body: dict) -> None:
    """Set the reverse DNS of one of a server's addresses (its change_dns_ptr).

    The address must belong to one of the server's primary IPs.
    """
    ip = str(body.get("ip"))
    for kind in ("ipv4", "ipv6"):
        primary_ip = primary_ip_of(lab.state, server, kind)
        if primary_ip and contains(primary_ip["ip"], ip):
            with lab.state.lock:
                address = str(ipaddress.ip_address(ip))
                set_dns_ptr(primary_ip, address, body.get("dns_ptr"))
            return
    raise invalid("the IP does not belong to the server", "ip")


# --- primary IPs ----------------------------------------------------------------------


def free_ipv4(lab: Lab) -> str:
    """Return the lowest public IPv4 that no primary IP has.

    The caller holds the state's lock, up to the new record.
    """
    used = {p["ip"] for p in lab.state["primary_ips"].values()}
    candidates = (f"{config.PUBLIC_NET}.{n}" for n in range(11, 251))
    error = limit_exceeded("the lab has no public IPv4 left", "primary_ips")
    return first_free(candidates, used, error)


def free_ipv6(lab: Lab) -> str:
    """Return the lowest public IPv6 /64 that no primary IP has.

    The caller holds the state's lock, up to the new record.
    """
    used = {p["ip"] for p in lab.state["primary_ips"].values()}
    candidates = (server_ipv6_net(n) for n in range(1, 0xFF00))
    error = limit_exceeded("the lab has no public IPv6 network left", "primary_ips")
    return first_free(candidates, used, error)


def new_primary_ip(
    lab: Lab,
    ip_type: str,
    location: str,
    assignee: int | None = None,
    name: str | None = None,
    auto_delete: bool = False,
    labels: dict | None = None,
) -> PrimaryIP:
    """Create a primary IP with a free address and return it.

    The caller holds the state's lock (from the free address to the new record).
    """
    state = lab.state
    pid = state.new_id("primary_ips")
    primary_ip: PrimaryIP = {
        "id": pid,
        "name": name or f"primary_ip-{pid}",
        "type": ip_type,
        "location": location,
        "ip": free_ipv4(lab) if ip_type == "ipv4" else free_ipv6(lab),
        "assignee_id": assignee,
        "auto_delete": auto_delete,
        "labels": labels or {},
        "protection": {"delete": False},
        "dns_ptr": [],
        "created": now(),
    }
    state["primary_ips"][pid] = primary_ip
    return primary_ip


def check_server_ips(lab: Lab, server: Server, public_net: dict) -> None:
    """Check that the primary IPs a new server asks for exist, are free and fit.

    Refusals use Hetzner's errors.
    """
    for kind in ("ipv4", "ipv6"):
        ref = public_net.get(kind)
        if ref is None:
            continue
        primary_ip = lab.state["primary_ips"].get(ref_id(ref, f"public_net.{kind}"))
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


def setup_server_ips(lab: Lab, server: Server, public_net: dict) -> None:
    """Assign the primary IPs of a new server.

    These are the given ones (unassigned, same location) or new ones with auto_delete.
    The caller holds the state's lock.
    """
    check_server_ips(lab, server, public_net)
    for kind in ("ipv4", "ipv6"):
        ref = public_net.get(kind)
        if ref is not None:
            primary_ip = lab.state["primary_ips"][int(ref)]
            primary_ip["assignee_id"] = server["id"]
            server[primary_ip_field(kind)] = primary_ip["id"]
        elif public_net.get(f"enable_{kind}", True):
            server[primary_ip_field(kind)] = new_primary_ip(
                lab, kind, server["location"], server["id"], auto_delete=True
            )["id"]


def release_server_ips(lab: Lab, server: Server) -> None:
    """Release a server's primary IPs when it is deleted.

    Primary IPs with auto_delete are deleted, the others stay unassigned. The caller
    holds the state's lock.
    """
    for kind in ("ipv4", "ipv6"):
        primary_ip = primary_ip_of(lab.state, server, kind)
        if not primary_ip:
            continue
        if primary_ip.get("auto_delete"):
            del lab.state["primary_ips"][primary_ip["id"]]
        else:
            primary_ip["assignee_id"] = None


def check_assignable(lab: Lab, server: Server, ip_type: str) -> None:
    """Check that a server can get a primary IP of a kind.

    The server must be unlocked, off, and have no primary IP of that kind.
    """
    require_off(server)
    if primary_ip_of(lab.state, server, ip_type):
        raise ApiError(
            422,
            "server_has_ipv4" if ip_type == "ipv4" else "server_has_ipv6",
            f"the server already has a primary {ip_type}",
        )


def create_primary_ip(lab: Lab, body: dict) -> dict:
    """Create a primary IP in a location, or assigned to a server that is off.

    Hetzner reports an action only when the IP is assigned at once.
    """
    state = lab.state
    with state.lock:
        check_unique_name(
            state["primary_ips"].values(), body["name"], None, "primary IP"
        )
    if body.get("location") and body.get("assignee_id"):
        raise invalid("location and assignee_id are mutually exclusive", "location")
    assignee = None
    if body.get("assignee_id"):
        assignee = state["servers"].get(ref_id(body["assignee_id"], "assignee_id"))
        if not assignee:
            raise invalid("server not found", "assignee_id")
    location = body.get("location") or (assignee["location"] if assignee else None)
    if not location:
        raise invalid("location or assignee_id is required", "location")
    location = catalogue_name(lab, "location", location)
    if assignee:
        # checked before the IP exists, so a refused request creates nothing
        check_assignable(lab, assignee, body["type"])
    with state.lock:
        primary_ip = new_primary_ip(
            lab,
            body["type"],
            location,
            None,
            body["name"],
            bool(body.get("auto_delete", False)),
            body.get("labels"),
        )
        state.save()
    action = None
    if assignee:
        try:
            primary_ip_assign(lab, primary_ip, {"assignee_id": assignee["id"]})
        except ApiError:  # the server changed meanwhile: the new IP is deleted
            with state.lock:
                del state["primary_ips"][primary_ip["id"]]
                state.save()
            raise
        state.save()
        resources = [(primary_ip["id"], "primary_ip"), (assignee["id"], "server")]
        action = lab.finished_action("assign_primary_ip", resources)
    return {"primary_ip": hetzner.primary_ip_json(lab, primary_ip), "action": action}


def update_primary_ip(lab: Lab, pid: int, body: dict) -> dict:
    """Change a primary IP's name, labels or auto_delete."""
    primary_ip = lab.get_or_404("primary_ips", pid, "primary IP")
    if body.get("auto_delete") and primary_ip["protection"]["delete"]:
        raise invalid(
            "a delete protected primary IP cannot be auto_delete", "auto_delete"
        )
    keys = ("name", "labels", "auto_delete")
    update_resource(lab, "primary_ips", primary_ip, body, keys, "primary IP")
    return {"primary_ip": hetzner.primary_ip_json(lab, primary_ip)}


def delete_primary_ip(lab: Lab, pid: int) -> None:
    """Delete a primary IP that is not protected and not assigned (Hetzner's rules)."""
    primary_ip = lab.get_or_404("primary_ips", pid, "primary IP")
    if primary_ip["protection"]["delete"]:
        raise protected("primary IP is delete protected")
    if primary_ip.get("assignee_id"):
        raise must_be_unassigned(
            "primary IP is assigned to a server; unassign it first"
        )
    with lab.state.lock:
        del lab.state["primary_ips"][pid]
        lab.state.save()


# Each primary IP action below is done at once (under the state's lock) and returns
# its plan (lab.Plan); after assign and unassign, the server's DHCP hands out its
# (new) address.


def primary_ip_assign(lab: Lab, primary_ip: PrimaryIP, body: dict) -> Plan:
    """Assign a primary IP to a server that is off, in its location."""
    state = lab.state
    with state.lock:
        server = state["servers"].get(ref_id(body.get("assignee_id"), "assignee_id"))
        if not server:
            raise invalid("server not found", "assignee_id")
        if primary_ip.get("assignee_id"):
            raise ApiError(
                422, "primary_ip_already_assigned", "primary IP is already assigned"
            )
        if primary_ip["location"] != server["location"]:
            raise ApiError(
                422, "primary_ip_location_mismatch", "primary IP is in another location"
            )
        check_assignable(lab, server, primary_ip["type"])
        primary_ip["assignee_id"] = server["id"]
        server[primary_ip_field(primary_ip["type"])] = primary_ip["id"]
    lab.fabric.write_dhcp()
    return Plan()


def primary_ip_unassign(lab: Lab, primary_ip: PrimaryIP, body: dict) -> Plan:
    """Take a primary IP from its server, which must be off."""
    state = lab.state
    with state.lock:
        server = server_by_id(state, primary_ip["assignee_id"])
        if server:
            require_off(server)
            server[primary_ip_field(primary_ip["type"])] = None
        primary_ip["assignee_id"] = None
    lab.fabric.write_dhcp()
    return Plan()


def primary_ip_change_dns_ptr(lab: Lab, primary_ip: PrimaryIP, body: dict) -> Plan:
    """Set the reverse DNS of one of the primary IP's addresses (its own by default)."""
    return record_dns_ptr(lab, primary_ip, body, default_ip=primary_ip["ip"])


def primary_ip_change_protection(lab: Lab, primary_ip: PrimaryIP, body: dict) -> Plan:
    """Set the delete protection (Hetzner: only for auto_delete false)."""
    if body.get("delete") and primary_ip.get("auto_delete"):
        raise invalid("delete protection needs auto_delete false", "delete")
    return change_delete_protection(lab, primary_ip, body)


# the primary IP actions: Hetzner's name of the action and the function that plans it
PRIMARY_IP_ACTIONS = {
    "assign": ("assign_primary_ip", primary_ip_assign),
    "unassign": ("unassign_primary_ip", primary_ip_unassign),
    "change_dns_ptr": ("change_dns_ptr", primary_ip_change_dns_ptr),
    "change_protection": ("change_protection", primary_ip_change_protection),
}


def run_primary_ip_action(lab: Lab, pid: int, cmd: str, body: dict) -> dict:
    """Run a primary IP action (all of them are done at once)."""
    primary_ip = lab.get_or_404("primary_ips", pid, "primary IP")
    return lab.act(PRIMARY_IP_ACTIONS, cmd, primary_ip, "primary_ip", body)


# --- Floating IPs ---------------------------------------------------------------------


def free_floating_ip(lab: Lab, ip_type: str) -> str:
    """Return the lowest free Floating IP address of a type.

    IPv6: 2001:db8:ff01::/64 to ...:fffd::/64, clear of the lab's own fffe and ffff.
    The caller holds the state's lock, up to the new record.
    """
    used = {x["ip"] for x in lab.state["floating_ips"].values()}
    if ip_type == "ipv4":
        candidates = (f"{config.FLOATING_NET}.{10 + n}" for n in range(1, 241))
    else:
        candidates = (f"{config.IPV6_PREFIX}:ff{n:02x}::/64" for n in range(1, 0xFE))
    error = limit_exceeded(f"the lab has no Floating {ip_type} left", "floating_ips")
    return first_free(candidates, used, error)


def create_floating_ip(lab: Lab, body: dict) -> dict:
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
    with state.lock:
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
        state.save()
    out = {
        "floating_ip": hetzner.floating_ip_json(lab, floating_ip),
        "action": None,
    }
    if server:
        out["action"] = run_floating_ip_action(
            lab, fid, "assign", {"server": server["id"]}
        )["action"]
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
    if floating_ip.get("server"):  # Hetzner since 1 May 2026
        raise must_be_unassigned("the Floating IP is assigned; unassign it first")
    with lab.state.lock:
        del lab.state["floating_ips"][fid]
        lab.state.save()


# Each Floating IP action below checks the request and returns its plan (lab.Plan).
# Assign and unassign move the route in the background, one move at a time
# (`_moving`); the others change the record only.


def floating_ip_change_dns_ptr(lab: Lab, floating_ip: FloatingIP, body: dict) -> Plan:
    """Set the reverse DNS of one of the Floating IP's addresses (named in the body)."""
    return record_dns_ptr(lab, floating_ip, body, default_ip="")


def move_floating_ip(lab: Lab, floating_ip: FloatingIP, target: Server | None) -> Plan:
    """Plan a move of the Floating IP to a server (or to none).

    The route moves after ~3 s, as on Hetzner; the server may send from the IP then.
    """
    state = lab.state

    async def steps():
        if target:
            await lab.sleep(FLOATING_IP_MOVE_SECONDS)
        with state.lock:
            deleted = floating_ip["id"] not in state["floating_ips"] or (
                target and target["id"] not in state["servers"]
            )
            if deleted:
                raise RuntimeError(
                    "the Floating IP or the server was deleted meanwhile"
                )
            floating_ip["server"] = target["id"] if target else None
            state.save()
        await lab.blocking(lab.fabric.route_floating_ip, floating_ip["ip"], target)
        await lab.blocking(lab.fabric.apply_faults)
        if target:
            await lab.blocking(lab.fabric.apply_latency)

    return Plan(steps, resources=[(target["id"], "server")] if target else [])


def floating_ip_assign(lab: Lab, floating_ip: FloatingIP, body: dict) -> Plan:
    """Plan the move of the Floating IP to the server the body names."""
    server = lab.server_or_404(ref_id(body.get("server"), "server"))
    return move_floating_ip(lab, floating_ip, server)


def floating_ip_unassign(lab: Lab, floating_ip: FloatingIP, body: dict) -> Plan:
    """Plan the move of the Floating IP to no server."""
    return move_floating_ip(lab, floating_ip, None)


# the Floating IP actions: Hetzner's name of the action and the function that plans it
FLOATING_IP_ACTIONS = {
    "change_protection": ("change_protection", change_delete_protection),
    "change_dns_ptr": ("change_dns_ptr", floating_ip_change_dns_ptr),
    "assign": ("assign_floating_ip", floating_ip_assign),
    "unassign": ("unassign_floating_ip", floating_ip_unassign),
}


def run_floating_ip_action(lab: Lab, fid: int, cmd: str, body: dict) -> dict:
    """Run a Floating IP action: one at a time per Floating IP (423 otherwise)."""
    floating_ip = lab.get_or_404("floating_ips", fid, "Floating IP")
    busy = "the Floating IP is being moved"
    return lab.act(
        FLOATING_IP_ACTIONS, cmd, floating_ip, "floating_ip", body, "_moving", busy
    )


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the primary IP and Floating IP routes to the app."""
    state = lab.state

    @app.get("/v1/primary_ips")
    def primary_ip_list(request: Request):
        with state.lock:
            items = filtered(
                request,
                list(state["primary_ips"].values()),
                {"ip": lambda primary_ip, value: primary_ip["ip"] == value},
            )
            return paginate(
                request, [hetzner.primary_ip_json(lab, p) for p in items], "primary_ips"
            )

    @app.post("/v1/primary_ips", status_code=201)
    async def primary_ip_create(request: Request):
        return await lab.blocking(create_primary_ip, lab, await read_body(request))

    @app.get("/v1/primary_ips/{pid}")
    def primary_ip_get(pid: int):
        primary_ip = lab.get_or_404("primary_ips", pid, "primary IP")
        return {"primary_ip": hetzner.primary_ip_json(lab, primary_ip)}

    @app.put("/v1/primary_ips/{pid}")
    async def primary_ip_update(pid: int, request: Request):
        body = await read_body(request)
        return await lab.blocking(update_primary_ip, lab, pid, body)

    @app.delete("/v1/primary_ips/{pid}", status_code=204)
    def primary_ip_delete(pid: int):
        delete_primary_ip(lab, pid)
        return Response(status_code=204)

    @app.post("/v1/primary_ips/{pid}/actions/{cmd}", status_code=201)
    async def primary_ip_action(pid: int, cmd: str, request: Request):
        return await lab.blocking(
            run_primary_ip_action, lab, pid, cmd, await read_body(request)
        )

    @app.get("/v1/floating_ips")
    def floating_ip_list(request: Request):
        with state.lock:
            items = filtered(request, list(state["floating_ips"].values()))
            return paginate(
                request,
                [hetzner.floating_ip_json(lab, f) for f in items],
                "floating_ips",
            )

    @app.post("/v1/floating_ips", status_code=201)
    async def floating_ip_create(request: Request):
        return await lab.blocking(create_floating_ip, lab, await read_body(request))

    @app.get("/v1/floating_ips/{fid}")
    def floating_ip_get(fid: int):
        floating_ip = lab.get_or_404("floating_ips", fid, "Floating IP")
        return {"floating_ip": hetzner.floating_ip_json(lab, floating_ip)}

    @app.put("/v1/floating_ips/{fid}")
    async def floating_ip_update(fid: int, request: Request):
        body = await read_body(request)
        return await lab.blocking(update_floating_ip, lab, fid, body)

    @app.delete("/v1/floating_ips/{fid}", status_code=204)
    def floating_ip_delete(fid: int):
        delete_floating_ip(lab, fid)
        return Response(status_code=204)

    @app.post("/v1/floating_ips/{fid}/actions/{cmd}", status_code=201)
    async def floating_ip_action(fid: int, cmd: str, request: Request):
        return await lab.blocking(
            run_floating_ip_action, lab, fid, cmd, await read_body(request)
        )
