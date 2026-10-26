"""Cloud networks: subnets, routes, a server's NICs (attach, detach, alias IPs).

Ranges, subnets and routes are checked like Hetzner checks them. Each network routes in
its own table, so networks may overlap.
"""

import ipaddress

from fastapi import FastAPI, Request
from fastapi.responses import Response

from .. import config
from ..errors import (
    ApiError,
    check_unique_name,
    invalid,
    ip_not_available,
    limit_exceeded,
    no_subnet_available,
    protected,
)
from ..fabric.network import network_gateway, private_mac
from ..lab import (
    ALIAS_IPS_SECONDS,
    NIC_ATTACH_SECONDS,
    NIC_DETACH_SECONDS,
    SERVER_LOCKED,
    Lab,
    Plan,
    now,
)
from ..records import CloudNetwork, Nic, Route, Server, Subnet
from ..state import nic_addresses, nics_in
from . import hetzner
from .faults import drop_netem
from .web import (
    change_delete_protection,
    filtered,
    paginate,
    read_body,
    ref_id,
    update_resource,
)

NETWORKS_PER_SERVER = 3  # Hetzner's limit
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


def lab_addresses(lab: Lab) -> set[str]:
    """Return the addresses no server gets.

    These are the networks' gateways (the lab's), Hetzner's gateway and the container's
    own addresses.
    """
    with lab.state.lock:
        gateways = {
            network_gateway(n["ip_range"]) for n in lab.state["networks"].values()
        }
    return gateways | {config.GATEWAY} | lab.fabric.local_addresses()


def network_ip(
    lab: Lab,
    net: CloudNetwork,
    ip: str | None,
    zone: str = "eu-central",
    ip_range: str | None = None,
) -> str:
    """Return the requested address of a cloud network if it is free, else a free one.

    `ip_range` picks the subnet.
    """
    subnets = [
        ipaddress.ip_network(x["ip_range"])
        for x in net["subnets"]
        if x["network_zone"] == zone
    ]
    if not subnets:
        raise no_subnet_available("network has no subnet in the network zone")
    if ip_range is not None:
        try:
            want = ipaddress.ip_network(str(ip_range))
        except ValueError:
            raise invalid(
                "ip_range must be a subnet in CIDR notation", "ip_range"
            ) from None
        subnets = [sn for sn in subnets if sn == want]
        if not subnets:
            raise invalid(
                "ip_range is not a subnet of the network in its network zone",
                "ip_range",
            )
    used = addresses_in(lab, net["id"]) | lab_addresses(lab)
    if ip:
        try:
            address = ipaddress.ip_address(str(ip))
        except ValueError:
            raise invalid("ip must be an IP address", "ip") from None
        if ip_range is not None and address not in subnets[0]:
            raise invalid("ip is not part of ip_range", "ip")
        if str(address) in used or not any(address in sn for sn in subnets):
            raise ip_not_available(address)
        return str(address)
    ip = next(
        (str(h) for sn in subnets for h in sn.hosts() if str(h) not in used), None
    )
    if not ip:
        raise no_subnet_available("no free IP in the network")
    return ip


def checked_alias_ips(lab: Lab, nic: Nic, ips) -> list[str]:
    """Return the checked alias IPs of a NIC.

    They must be free addresses of the network's subnets (reserved like primary ones).
    """
    if ips is None:
        return []
    if not isinstance(ips, list):
        raise invalid("alias_ips is a list of IPs", "alias_ips")
    net = lab.state["networks"][nic["network"]]
    subnets = [ipaddress.ip_network(x["ip_range"]) for x in net["subnets"]]
    taken = (
        addresses_in(lab, net["id"], except_nic=nic) | lab_addresses(lab) | {nic["ip"]}
    )
    out = []
    for ip in ips:
        try:
            address = ipaddress.IPv4Address(str(ip))
        except ValueError:
            raise invalid(f"{ip!r} is not an IPv4 address", "alias_ips") from None
        if (
            not any(address in sn for sn in subnets)
            or str(address) in taken
            or str(address) in out
        ):
            raise ip_not_available(address)
        out.append(str(address))
    return out


def add_nic(
    lab: Lab, server: Server, nid: int, ip: str | None, ip_range: str | None = None
) -> Nic:
    """Add a NIC in a network to the server and return it.

    This changes the state only (the caller holds its lock); the fabric adds the NIC to
    the VM.
    """
    state = lab.state
    net = state["networks"].get(nid)
    if not net or net.get("_deleting"):
        raise invalid(f"network {nid} not found")
    nics = server["_lab"]["nics"]
    if any(n["network"] == nid for n in nics):
        raise ApiError(
            422, "server_already_attached", "server is already attached to this network"
        )
    if len(nics) >= NETWORKS_PER_SERVER:
        raise limit_exceeded(
            f"a server is in at most {NETWORKS_PER_SERVER} networks", "networks"
        )
    joined_range = ipaddress.ip_network(net["ip_range"])
    if any(
        joined_range.overlaps(
            ipaddress.ip_network(state["networks"][n["network"]]["ip_range"])
        )
        for n in nics
    ):
        raise ApiError(
            422,
            "networks_overlap",
            "the network overlaps another network of the server",
        )
    zone = lab.cfg["locations"][server["location"]].get("network_zone", "eu-central")
    ip = network_ip(lab, net, ip, zone, ip_range)
    used = {n["number"] for n in nics}
    # the lowest free: MAC and tap name stay valid
    number = next(i for i in range(1, 256) if i not in used)
    nic: Nic = {
        "network": nid,
        "ip": ip,
        "alias_ips": [],
        "number": number,
        "tap": f"prv{server['_lab']['index']}x{number}",
        "mac": private_mac(server["_lab"]["index"], number),
    }
    nics.append(nic)
    return nic


def server_nic(server: Server, body: dict) -> Nic:
    """Return the server's NIC in the network the body names, or raise invalid_input."""
    nid = ref_id(body.get("network"), "network")
    nic = next((n for n in server["_lab"]["nics"] if n["network"] == nid), None)
    if not nic:
        raise invalid("server is not attached to this network")
    return nic


# --- server actions (see servers.SERVER_ACTIONS) --------------------------------------


def attach_to_network(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan an attach to a network.

    A running server gets the NIC hot-plugged; one that is off gets it at its next
    start.
    """
    with lab.state.lock:
        nic = add_nic(
            lab,
            server,
            ref_id(body.get("network"), "network"),
            body.get("ip"),
            body.get("ip_range"),
        )
        try:
            nic["alias_ips"] = checked_alias_ips(lab, nic, body.get("alias_ips"))
        except ApiError:
            server["_lab"]["nics"].remove(nic)
            raise
        lab.state.save()

    async def steps():
        try:
            await lab.blocking(lab.fabric.attach_nic, server, nic)
        except Exception:
            # the NIC is removed again: no network reported that the VM does not have
            await lab.blocking(lab.fabric.detach_nic, server, nic)
            with lab.state.lock:
                if nic in server["_lab"]["nics"]:
                    server["_lab"]["nics"].remove(nic)
                drop_netem(lab, [nic["tap"]])  # the next NIC gets the tap name again
                lab.state.save()
            await lab.blocking(lab.fabric.write_dhcp)
            raise
        await lab.sleep(NIC_ATTACH_SECONDS)

    return Plan(steps, server=server)


def detach_from_network(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a detach from a network."""
    nic = server_nic(server, body)

    async def steps():
        await lab.blocking(lab.fabric.detach_nic, server, nic)
        with lab.state.lock:
            server["_lab"]["nics"].remove(nic)
            drop_netem(lab, [nic["tap"]])  # the next NIC gets the tap name again
            lab.state.save()
        await lab.blocking(lab.fabric.write_dhcp)
        await lab.blocking(lab.fabric.apply_faults)
        await lab.sleep(NIC_DETACH_SECONDS)

    return Plan(steps, server=server)


def change_alias_ips(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a change of alias IPs.

    The lab routes the alias IPs to the server; the guest sets them up itself.
    """
    nic = server_nic(server, body)
    with lab.state.lock:
        nic["alias_ips"] = checked_alias_ips(lab, nic, body.get("alias_ips"))
        lab.state.save()

    async def steps():
        if await lab.blocking(lab.fabric.vm_alive, server):
            await lab.blocking(lab.fabric.nic_up, nic)
        # an alias IP may be the gateway of a route
        await lab.blocking(lab.fabric.apply_routes)
        await lab.sleep(ALIAS_IPS_SECONDS)

    return Plan(steps, server=server)


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


def create_network(lab: Lab, body: dict) -> dict:
    """Create a network with its subnets and routes."""
    state = lab.state
    if not body.get("name") or not body.get("ip_range"):
        raise invalid("name and ip_range are required")
    ip_range = network_range(body["ip_range"])
    network: CloudNetwork = {
        "id": 0,  # its own once everything is checked
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
    with state.lock:
        check_unique_name(state["networks"].values(), network["name"], None, "network")
        # the first address becomes the lab's (its gateway in the network): not one in
        # use elsewhere – the gateway of another network is fine (overlapping networks
        # share it)
        used = {ip for x in state["networks"] for ip in addresses_in(lab, x)}
        shared = {network_gateway(x["ip_range"]) for x in state["networks"].values()}
        if gateway in used or (
            gateway in lab.fabric.local_addresses(fresh=True) and gateway not in shared
        ):
            raise invalid(
                f"the network's first address {gateway} "
                "(the lab's gateway in it) is in use",
                "ip_range",
            )
        network["id"] = state.new_id("networks")
        state["networks"][network["id"]] = network
        state.save()
    return {"network": hetzner.network_json(lab, network)}


# Each network action below is done at once (under the state's lock) and returns an
# empty plan (lab.Plan). DHCP and the routing tables follow the network's new settings.


def apply_network_change(lab: Lab) -> None:
    """Hand the servers their new DHCP options and put the network routes in place."""
    lab.fabric.write_dhcp()
    lab.fabric.apply_routes()


def add_subnet(lab: Lab, network: CloudNetwork, body: dict) -> Plan:
    """Add a subnet to the network."""
    with lab.state.lock:
        network["subnets"].append(checked_subnet(network, body))
    apply_network_change(lab)
    return Plan()


def delete_subnet(lab: Lab, network: CloudNetwork, body: dict) -> Plan:
    """Delete a subnet that has no addresses in use."""
    subnet_range = cidr(body.get("ip_range"), "ip_range")
    with lab.state.lock:
        in_use = addresses_in(lab, network["id"])
        if any(ipaddress.ip_address(ip) in subnet_range for ip in in_use):
            raise invalid(
                "subnets with attached resources cannot be deleted", "ip_range"
            )
        network["subnets"] = [
            x for x in network["subnets"] if x["ip_range"] != str(subnet_range)
        ]
    apply_network_change(lab)
    return Plan()


def add_route(lab: Lab, network: CloudNetwork, body: dict) -> Plan:
    """Add a route to the network."""
    with lab.state.lock:
        network["routes"].append(checked_route(network, body))
    apply_network_change(lab)
    return Plan()


def delete_route(lab: Lab, network: CloudNetwork, body: dict) -> Plan:
    """Delete a route of the network."""
    given = (body.get("destination"), body.get("gateway"))
    with lab.state.lock:
        network["routes"] = [
            r for r in network["routes"] if (r["destination"], r["gateway"]) != given
        ]
    apply_network_change(lab)
    return Plan()


def change_ip_range(lab: Lab, network: CloudNetwork, body: dict) -> Plan:
    """Extend the network's range: same first address, smaller netmask."""
    old = ipaddress.ip_network(network["ip_range"])
    new = network_range(body.get("ip_range"))
    if new.network_address != old.network_address or new.prefixlen > old.prefixlen:
        raise invalid(
            "the range can only be extended: same first address, smaller netmask",
            "ip_range",
        )
    with lab.state.lock:
        network["ip_range"] = str(new)
    lab.fabric.refresh_network(network["id"])
    apply_network_change(lab)
    return Plan()


# the network actions: Hetzner's name of the action and the function that plans it
NETWORK_ACTIONS = {
    "add_subnet": ("add_subnet", add_subnet),
    "delete_subnet": ("delete_subnet", delete_subnet),
    "add_route": ("add_route", add_route),
    "delete_route": ("delete_route", delete_route),
    "change_ip_range": ("change_ip_range", change_ip_range),
    "change_protection": ("change_protection", change_delete_protection),
}


def run_network_action(lab: Lab, nid: int, cmd: str, body: dict) -> dict:
    """Run a network action (all of them are done at once)."""
    network = lab.get_or_404("networks", nid, "network")
    return lab.act(NETWORK_ACTIONS, cmd, network, "network", body)


def delete_network(lab: Lab, nid: int) -> None:
    """Delete a network and detach its servers, as on Hetzner.

    Each of the servers is locked meanwhile. A NIC leaves the state only after its
    detach worked, and the state is saved each time; so if a detach fails, the network
    stays with the NICs not yet detached, and deleting it again goes on from there.
    """
    state = lab.state
    network = lab.get_or_404("networks", nid, "network")
    if network["protection"]["delete"]:
        raise protected("network is delete protected")
    # from here no server can join it (add_nic refuses)
    lab.claim(network, "_deleting", "the network is being deleted")
    members = nics_in(state, nid)  # a server is in a network once
    claimed = []
    try:
        for server, _ in members:  # 423 if one of them has an action running
            lab.claim(server, "locked", SERVER_LOCKED)
            claimed.append(server)
        try:
            for server, nic in members:
                lab.fabric.detach_nic(server, nic)
                with state.lock:
                    server["_lab"]["nics"].remove(nic)
                    drop_netem(lab, [nic["tap"]])
                    state.save()
        except Exception:
            # DHCP and the fault sets follow the NICs detached so far
            lab.fabric.write_dhcp()
            lab.fabric.apply_faults()
            raise
        with state.lock:
            del state["networks"][nid]
            state.save()
    finally:
        lab.release(network, "_deleting")
        for server in claimed:
            lab.release(server)
    lab.fabric.drop_network(nid)


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
    def network_list(request: Request):
        with state.lock:
            items = filtered(request, list(state["networks"].values()))
            return paginate(
                request, [hetzner.network_json(lab, n) for n in items], "networks"
            )

    @app.post("/v1/networks", status_code=201)
    async def network_create(request: Request):
        return await lab.blocking(create_network, lab, await read_body(request))

    @app.get("/v1/networks/{nid}/members")
    def network_member_list(request: Request, nid: int):
        network = lab.get_or_404("networks", nid, "network")
        members = network_members(lab, network, request.query_params)
        return paginate(request, members, "members")

    @app.get("/v1/networks/{nid}")
    def network_get(nid: int):
        return {
            "network": hetzner.network_json(
                lab, lab.get_or_404("networks", nid, "network")
            )
        }

    @app.put("/v1/networks/{nid}")
    async def network_update(nid: int, request: Request):
        return await lab.blocking(update_network, lab, nid, await read_body(request))

    @app.delete("/v1/networks/{nid}", status_code=204)
    def network_delete(nid: int):
        delete_network(lab, nid)
        return Response(status_code=204)

    @app.post("/v1/networks/{nid}/actions/{cmd}", status_code=201)
    async def network_action(nid: int, cmd: str, request: Request):
        return await lab.blocking(
            run_network_action, lab, nid, cmd, await read_body(request)
        )
