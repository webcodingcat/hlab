"""Cloud networks: their ranges, subnets and routes.

Ranges, subnets and routes are checked the way Hetzner checks them. Each network
routes in its own table, so networks may overlap. A server's NICs are in nics.py.
"""

import ipaddress

from fastapi import FastAPI, Request
from fastapi.responses import Response

from .. import config
from ..errors import check_unique_name, invalid, protected
from ..fabric.addresses import network_gateway
from ..lab import SERVER_LOCKED, ActionSpec, ActionTable, Lab, Plan, now
from ..records import CloudNetwork, Nic, Route, Subnet
from ..state import nic_addresses, nics_in
from . import hetzner, system
from .faults import drop_netem
from .web import (
    change_delete_protection,
    filtered,
    paginate,
    read_body,
    update_resource,
)

PRIVATE_RANGES = [
    ipaddress.IPv4Network(n) for n in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")
]


# --- checks of ranges and addresses ---------------------------------------------------


def cidr(value, field: str) -> ipaddress.IPv4Network:
    """Return an IPv4 prefix in CIDR notation, or raise invalid_input for the field.

    Host bits must not be set.
    """
    try:
        return ipaddress.IPv4Network(str(value))
    except ValueError:
        raise invalid(
            f"{value!r} is not an IPv4 range in CIDR notation", field
        ) from None


def private_range(net: ipaddress.IPv4Network) -> bool:
    """Return whether a range lies in a private range (RFC 1918)."""
    return any(net.subnet_of(r) for r in PRIVATE_RANGES)


def network_range(value) -> ipaddress.IPv4Network:
    """Return the checked ip_range of a network: RFC 1918, at least a /24."""
    ip_range = cidr(value, "ip_range")
    if not private_range(ip_range):
        raise invalid("ip_range must be a private range (RFC 1918)", "ip_range")
    if ip_range.prefixlen > 24:
        raise invalid("the smallest network is a /24", "ip_range")
    return ip_range


def addresses_in(lab: Lab, nid: int, except_nic: Nic | None = None) -> set[str]:
    """Return the private and alias IPs servers have in a network.

    The addresses of except_nic are left out.
    """
    return {
        ip
        for _, nic in nics_in(lab.state, nid)
        if nic is not except_nic
        for ip in nic_addresses(nic)
    }


def subnet_of(network: CloudNetwork, ip: str) -> str:
    """Return the subnet of a network an address lies in (else the network's range)."""
    address = ipaddress.ip_address(ip)
    return next(
        (
            subnet["ip_range"]
            for subnet in network["subnets"]
            if address in ipaddress.ip_network(subnet["ip_range"])
        ),
        network["ip_range"],
    )


# --- networks -------------------------------------------------------------------------


def checked_subnet(network: CloudNetwork, subnet: dict) -> Subnet:
    """Check a new subnet of the network as Hetzner does; return it normalised.

    It must be inside the network, at least a /30, and must not overlap subnets or
    routes. Without an ip_range it takes the next free /24.
    """
    ip_range = ipaddress.IPv4Network(network["ip_range"])
    taken = [ipaddress.ip_network(x["ip_range"]) for x in network["subnets"]]
    taken += [ipaddress.ip_network(r["destination"]) for r in network["routes"]]
    subnet_range: ipaddress.IPv4Network | None
    if subnet.get("ip_range"):
        subnet_range = cidr(subnet["ip_range"], "ip_range")
    else:  # the next free /24
        subnet_range = next(
            (
                c
                for c in ip_range.subnets(new_prefix=max(24, ip_range.prefixlen))
                if not any(c.overlaps(t) for t in taken)
            ),
            None,
        )
        if subnet_range is None:
            raise invalid("no space for a subnet", "ip_range")
    if not subnet_range.subnet_of(ip_range):
        raise invalid("subnet must be inside the network's ip_range", "ip_range")
    if subnet_range.prefixlen > 30:
        raise invalid("the smallest subnet is a /30", "ip_range")
    if any(subnet_range.overlaps(t) for t in taken):
        raise invalid("subnet overlaps another subnet or a route", "ip_range")
    if subnet.get("type", "cloud") not in ("cloud", "server", "vswitch"):
        raise invalid("type must be cloud, server or vswitch", "type")
    return {
        "type": subnet.get("type", "cloud"),
        "ip_range": str(subnet_range),
        "network_zone": str(subnet.get("network_zone", "eu-central")),
        "gateway": network_gateway(network["ip_range"]),
        "vswitch_id": subnet.get("vswitch_id"),
    }


def checked_route(network: CloudNetwork, route: dict) -> Route:
    """Check a new route of the network as Hetzner does; return it normalised.

    It needs an RFC 1918 destination (or 0.0.0.0/0) clear of subnets and other routes,
    and a gateway inside the network that is neither its first IP nor 172.31.1.1.
    """
    dest = cidr(route.get("destination"), "destination")
    default = dest == ipaddress.ip_network("0.0.0.0/0")
    if not default and not private_range(dest):
        raise invalid(
            "destination must be a private range (RFC 1918) or 0.0.0.0/0", "destination"
        )
    taken = [ipaddress.ip_network(x["ip_range"]) for x in network["subnets"]]
    taken += [ipaddress.ip_network(x["destination"]) for x in network["routes"]]
    if not default and any(dest.overlaps(t) for t in taken):
        raise invalid("destination overlaps a subnet or another route", "destination")
    try:
        gateway = ipaddress.IPv4Address(str(route.get("gateway")))
    except ValueError:
        raise invalid("gateway must be an IPv4 address", "gateway") from None
    reserved = (network_gateway(network["ip_range"]), config.GATEWAY)
    if (
        gateway not in ipaddress.ip_network(network["ip_range"])
        or str(gateway) in reserved
    ):
        raise invalid(
            "gateway must be an address of the network other than its first IP",
            "gateway",
        )
    return {"destination": str(dest), "gateway": str(gateway)}


async def create_network(lab: Lab, body: dict) -> dict:
    """Create a network with its subnets and routes."""
    state = lab.state
    if not body.get("name") or not body.get("ip_range"):
        raise invalid("name and ip_range are required")
    ip_range = network_range(body["ip_range"])
    network: CloudNetwork = {
        "id": 0,  # the real ID is set once everything is checked
        "name": body["name"],
        "ip_range": str(ip_range),
        "subnets": [],
        "routes": [],
        "labels": body.get("labels") or {},
        "protection": {"delete": False},
        "created": now(),
        "expose_routes_to_vswitch": bool(body.get("expose_routes_to_vswitch", False)),
    }
    # everything is checked before the network exists; each subnet and route is
    # checked against the ones before it
    for subnet in body.get("subnets") or []:
        network["subnets"].append(checked_subnet(network, subnet))
    for route in body.get("routes") or []:
        network["routes"].append(checked_route(network, route))
    gateway = network_gateway(network["ip_range"])
    # reading them fresh runs a script
    local_addresses = await lab.blocking(
        lab.fabric.network.read_local_addresses, fresh=True
    )
    with state.change():
        check_unique_name(state["networks"].values(), network["name"], None, "network")
        # the first address becomes the lab's gateway in the network, so it must not be
        # in use elsewhere; another network's gateway is fine (overlapping networks
        # share it)
        used = {ip for x in state["networks"] for ip in addresses_in(lab, x)}
        shared = {network_gateway(x["ip_range"]) for x in state["networks"].values()}
        if gateway in used or (gateway in local_addresses and gateway not in shared):
            raise invalid(
                f"the network's first address {gateway} "
                "(the lab's gateway in it) is in use",
                "ip_range",
            )
        network["id"] = state.new_id("networks")
        state["networks"][network["id"]] = network
    return {"network": hetzner.network_json(lab, network)}


# Each network action below is done at once (`act()` saves it) and returns an empty
# plan (lab.Plan). Then DHCP and the routing tables get the network's new settings.


async def apply_network_change(lab: Lab) -> None:
    """Hand the servers their new DHCP options and put the network routes in place."""
    await system.write_dhcp(lab)
    await system.apply_routes(lab)


async def add_subnet(lab: Lab, network: CloudNetwork, body: dict) -> Plan:
    """Add a subnet to the network."""
    network["subnets"].append(checked_subnet(network, body))
    await apply_network_change(lab)
    return Plan()


async def delete_subnet(lab: Lab, network: CloudNetwork, body: dict) -> Plan:
    """Delete a subnet that has no addresses in use."""
    subnet_range = cidr(body.get("ip_range"), "ip_range")
    in_use = addresses_in(lab, network["id"])
    if any(ipaddress.ip_address(ip) in subnet_range for ip in in_use):
        raise invalid("subnets with attached resources cannot be deleted", "ip_range")
    network["subnets"] = [
        x for x in network["subnets"] if x["ip_range"] != str(subnet_range)
    ]
    await apply_network_change(lab)
    return Plan()


async def add_route(lab: Lab, network: CloudNetwork, body: dict) -> Plan:
    """Add a route to the network."""
    network["routes"].append(checked_route(network, body))
    await apply_network_change(lab)
    return Plan()


async def delete_route(lab: Lab, network: CloudNetwork, body: dict) -> Plan:
    """Delete a route of the network."""
    given = (body.get("destination"), body.get("gateway"))
    network["routes"] = [
        r for r in network["routes"] if (r["destination"], r["gateway"]) != given
    ]
    await apply_network_change(lab)
    return Plan()


async def change_ip_range(lab: Lab, network: CloudNetwork, body: dict) -> Plan:
    """Extend the network's range: same first address, smaller netmask."""
    old = ipaddress.ip_network(network["ip_range"])
    new = network_range(body.get("ip_range"))
    if new.network_address != old.network_address or new.prefixlen > old.prefixlen:
        raise invalid(
            "the range can only be extended: same first address, smaller netmask",
            "ip_range",
        )
    network["ip_range"] = str(new)
    await system.refresh_cloud_network(lab, network["id"])
    await apply_network_change(lab)
    return Plan()


# the network actions: Hetzner's name of the action and the function that plans it
NETWORK_ACTIONS: ActionTable[CloudNetwork] = {
    "add_subnet": ActionSpec("add_subnet", add_subnet),
    "delete_subnet": ActionSpec("delete_subnet", delete_subnet),
    "add_route": ActionSpec("add_route", add_route),
    "delete_route": ActionSpec("delete_route", delete_route),
    "change_ip_range": ActionSpec("change_ip_range", change_ip_range),
    "change_protection": ActionSpec(
        "change_protection", change_delete_protection, flag=False
    ),
}


async def run_network_action(lab: Lab, nid: int, cmd: str, body: dict) -> dict:
    """Run a network action (all of them are done at once)."""
    network = lab.get_or_404("networks", nid, "network")
    return await lab.act(NETWORK_ACTIONS, cmd, network, "network", body)


async def delete_network(lab: Lab, nid: int) -> None:
    """Delete a network and detach its servers, as on Hetzner.

    Each of the servers is locked meanwhile. A NIC is removed from the state only after
    its detach worked, and the state is saved after each one. So if a detach fails, the
    network keeps the NICs not yet detached, and a second delete goes on from there.
    """
    state = lab.state
    network = lab.get_or_404("networks", nid, "network")
    if network["protection"]["delete"]:
        raise protected("network is delete protected")
    with lab.holding() as held:
        # from here no server can join it (add_nic refuses)
        held.claim(network, "_deleting", "the network is being deleted")
        members = nics_in(state, nid)  # a server has one NIC per network at most
        for server, _ in members:  # 423 if one of them has an action running
            held.claim(server, "_locked", SERVER_LOCKED)
        try:
            for server, nic in members:
                await lab.blocking(lab.fabric.network.detach_nic, server, nic)
                with state.change():
                    server["_lab"]["nics"].remove(nic)
                    drop_netem(lab, [nic["tap"]])
        except Exception:  # whatever failed: sync, then re-raise
            # update DHCP and the fault sets for the NICs detached so far
            await system.write_dhcp(lab)
            await system.apply_faults(lab)
            raise
        with state.change():
            del state["networks"][nid]
    await system.drop_cloud_network(lab, nid)


def update_network(lab: Lab, nid: int, body: dict) -> dict:
    """Change a network's name, labels or expose_routes_to_vswitch."""
    network = lab.get_or_404("networks", nid, "network")
    keys = ("name", "labels", "expose_routes_to_vswitch")
    update_resource(lab, "networks", network, body, keys, "network")
    return {"network": hetzner.network_json(lab, network)}


def network_members(lab: Lab, network: CloudNetwork, query) -> list[dict]:
    """Return the members of a network, filtered by the type and subnet queries."""
    members = [
        {
            "type": "server",
            "id": server["id"],
            "ip": nic["ip"],
            "status": "ok",
            "alias_ips": nic["alias_ips"],
            "subnet": subnet_of(network, nic["ip"]),
        }
        for server, nic in nics_in(lab.state, network["id"])
    ]
    if query.get("type"):
        members = [m for m in members if m["type"] in query["type"].split(",")]
    if query.get("subnet"):
        members = [m for m in members if m["subnet"] == query["subnet"]]
    return members


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the network routes to the app."""
    state = lab.state

    @app.get("/v1/networks")
    async def network_list(request: Request):
        items = filtered(request, list(state["networks"].values()))
        return paginate(
            request, [hetzner.network_json(lab, n) for n in items], "networks"
        )

    @app.post("/v1/networks", status_code=201)
    async def network_create(request: Request):
        return await create_network(lab, await read_body(request))

    @app.get("/v1/networks/{nid}/members")
    async def network_member_list(request: Request, nid: int):
        network = lab.get_or_404("networks", nid, "network")
        members = network_members(lab, network, request.query_params)
        return paginate(request, members, "members")

    @app.get("/v1/networks/{nid}")
    async def network_get(nid: int):
        return {
            "network": hetzner.network_json(
                lab, lab.get_or_404("networks", nid, "network")
            )
        }

    @app.put("/v1/networks/{nid}")
    async def network_update(nid: int, request: Request):
        return update_network(lab, nid, await read_body(request))

    @app.delete("/v1/networks/{nid}", status_code=204)
    async def network_delete(nid: int):
        await delete_network(lab, nid)
        return Response(status_code=204)

    @app.post("/v1/networks/{nid}/actions/{cmd}", status_code=201)
    async def network_action(nid: int, cmd: str, request: Request):
        return await run_network_action(lab, nid, cmd, await read_body(request))
