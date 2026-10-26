"""Primary IPs: a server's own public IPv4 and IPv6 /64.

Addresses come from documentation ranges outside RFC 1918, so firewall rules treat
them as public addresses, as they would on Hetzner.
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
from ..lab import SERVER_LOCKED, ActionTable, Lab, Plan, now
from ..records import PrimaryIP, Server
from ..state import primary_ip_field, primary_ip_of, server_by_id
from . import hetzner
from .catalogue import catalogue_name
from .public_addresses import contains, first_free, record_dns_ptr, set_dns_ptr
from .web import (
    change_delete_protection,
    filtered,
    paginate,
    read_body,
    ref_id,
    update_resource,
)


def server_ipv6_net(number: int) -> str:
    """Return the n-th /64 of the lab's prefix (a server's primary IPv6)."""
    return f"{config.IPV6_PREFIX}:{number:x}::/64"


def require_off(server: Server) -> None:
    """Refuse a change of a server's primary IPs unless it is off and unlocked."""
    if server.get("locked"):
        raise locked(SERVER_LOCKED)
    if server["status"] != "off":
        raise server_not_stopped()


def server_dns_ptr(lab: Lab, server: Server, body: dict) -> None:
    """Set the reverse DNS of one of a server's addresses (action change_dns_ptr).

    The address must belong to one of the server's primary IPs.
    """
    ip = str(body.get("ip"))
    for kind in ("ipv4", "ipv6"):
        primary_ip = primary_ip_of(lab.state, server, kind)
        if primary_ip and contains(primary_ip["ip"], ip):
            with lab.state.lock:
                address = str(ipaddress.ip_address(ip))
                set_dns_ptr(lab, primary_ip, address, body.get("dns_ptr"))
            return
    raise invalid("the IP does not belong to the server", "ip")


def free_ipv4(lab: Lab) -> str:
    """Return the lowest public IPv4 that no primary IP has.

    The caller holds the state's lock until it has stored the new record.
    """
    lab.state.lock.assert_held()
    used = {p["ip"] for p in lab.state["primary_ips"].values()}
    candidates = (f"{config.PUBLIC_NET}.{n}" for n in range(11, 251))
    error = limit_exceeded("the lab has no public IPv4 left", "primary_ips")
    return first_free(candidates, used, error)


def free_ipv6(lab: Lab) -> str:
    """Return the lowest public IPv6 /64 that no primary IP has.

    The caller holds the state's lock until it has stored the new record.
    """
    lab.state.lock.assert_held()
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

    The caller holds the state's lock, so no one else takes the address before the
    record is stored.
    """
    lab.state.lock.assert_held()
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
    """Check that the primary IPs a new server asks for exist, are free and match it.

    They must have the right type and the server's location. Refusals use Hetzner's
    error codes.
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
    lab.state.lock.assert_held()
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
    lab.state.lock.assert_held()
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

    Hetzner returns an action only when the IP is assigned on creation.
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
    with state.change():
        primary_ip = new_primary_ip(
            lab,
            body["type"],
            location,
            None,
            body["name"],
            bool(body.get("auto_delete", False)),
            body.get("labels"),
        )
    action = None
    if assignee:
        try:
            primary_ip_assign(lab, primary_ip, {"assignee_id": assignee["id"]})
        except ApiError:  # the server changed meanwhile: delete the new IP again
            with state.change():
                del state["primary_ips"][primary_ip["id"]]
            raise
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
    """Delete a primary IP; like Hetzner, refuse a protected or assigned one."""
    primary_ip = lab.get_or_404("primary_ips", pid, "primary IP")
    if primary_ip["protection"]["delete"]:
        raise protected("primary IP is delete protected")
    if primary_ip.get("assignee_id"):
        raise must_be_unassigned(
            "primary IP is assigned to a server; unassign it first"
        )
    with lab.state.change():
        del lab.state["primary_ips"][pid]


# Each primary IP action below is done at once (under the state's lock) and returns
# its plan (lab.Plan). After assign and unassign, the server's DHCP hands out its new
# address.


def primary_ip_assign(lab: Lab, primary_ip: PrimaryIP, body: dict) -> Plan:
    """Assign a primary IP to a server that is off, in its location."""
    state = lab.state
    with state.change():  # saved here: create_primary_ip calls this without act()
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
    lab.fabric.network.write_dhcp()
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
    lab.fabric.network.write_dhcp()
    return Plan()


def primary_ip_change_dns_ptr(lab: Lab, primary_ip: PrimaryIP, body: dict) -> Plan:
    """Set the reverse DNS of one of the primary IP's addresses (its own by default)."""
    return record_dns_ptr(lab, primary_ip, body, default_ip=primary_ip["ip"])


def primary_ip_change_protection(lab: Lab, primary_ip: PrimaryIP, body: dict) -> Plan:
    """Set the delete protection; Hetzner allows it only when auto_delete is false."""
    if body.get("delete") and primary_ip.get("auto_delete"):
        raise invalid("delete protection needs auto_delete false", "delete")
    return change_delete_protection(lab, primary_ip, body)


# the primary IP actions: Hetzner's name of the action and the function that plans it
PRIMARY_IP_ACTIONS: ActionTable[PrimaryIP] = {
    "assign": ("assign_primary_ip", primary_ip_assign),
    "unassign": ("unassign_primary_ip", primary_ip_unassign),
    "change_dns_ptr": ("change_dns_ptr", primary_ip_change_dns_ptr),
    "change_protection": ("change_protection", primary_ip_change_protection),
}


def run_primary_ip_action(lab: Lab, pid: int, cmd: str, body: dict) -> dict:
    """Run a primary IP action (all of them are done at once)."""
    primary_ip = lab.get_or_404("primary_ips", pid, "primary IP")
    return lab.act(PRIMARY_IP_ACTIONS, cmd, primary_ip, "primary_ip", body)


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the primary IP routes to the app."""
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
