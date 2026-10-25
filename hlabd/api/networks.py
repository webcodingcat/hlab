"""Cloud networks: subnets, routes, a server's NICs (attach, detach, alias IPs). Ranges, subnets and routes are
checked like Hetzner checks them. Each network routes in its own table, so networks may overlap."""

import ipaddress

from fastapi import FastAPI, Request
from fastapi.responses import Response

from .. import config as C
from ..errors import ApiError, invalid, limit_exceeded, locked, not_found, unique_name, unsupported
from ..fabric.network import network_gateway, private_mac
from ..lab import now
from . import hetzner as H
from .web import read_body, filtered, paginate

NETWORKS_PER_SERVER = 3   # Hetzner's limit
PRIVATE_RANGES = [ipaddress.ip_network(n) for n in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")]


# --- checks of ranges and addresses ------------------------------------------------------------------------------

def cidr(value, field: str) -> ipaddress.IPv4Network:
    """An IPv4 prefix in CIDR notation (host bits not set), or invalid_input for the field."""
    try:
        return ipaddress.IPv4Network(str(value))
    except ValueError:
        raise invalid(f"{value!r} is not an IPv4 range in CIDR notation", field) from None


def private_range(net: ipaddress.IPv4Network) -> bool:
    return any(net.subnet_of(r) for r in PRIVATE_RANGES)


def network_range(value) -> ipaddress.IPv4Network:
    """The ip_range of a network: RFC 1918, at least a /24."""
    ip_range = cidr(value, "ip_range")
    if not private_range(ip_range):
        raise invalid("ip_range must be a private range (RFC 1918)", "ip_range")
    if ip_range.prefixlen > 24:
        raise invalid("the smallest network is a /24", "ip_range")
    return ip_range


def addresses_in(state, nid: int, except_nic: dict | None = None) -> set[str]:
    """The private and alias IPs servers have in a network (but one NIC)."""
    nics = [n for s in state["servers"].values() for n in s["_lab"]["nics"]
            if n["network"] == nid and n is not except_nic]
    return {ip for n in nics for ip in [n["ip"], *(n.get("alias_ips") or [])]}


def lab_addresses(lab) -> set[str]:
    """Addresses no server gets: the networks' gateways (the lab's), Hetzner's gateway, the container's own."""
    return ({network_gateway(n["ip_range"]) for n in lab.state["networks"].values()} | {C.GATEWAY}
            | lab.fabric.local_addresses())


def network_ip(lab, net: dict, ip: str | None, zone: str = "eu-central", ip_range: str | None = None) -> str:
    """A free address of a cloud network, or the requested one if it is free; ip_range picks the subnet."""
    subnets = [ipaddress.ip_network(x["ip_range"]) for x in net["subnets"] if x["network_zone"] == zone]
    if not subnets:
        raise ApiError(422, "no_subnet_available", "network has no subnet in the network zone")
    if ip_range is not None:
        try:
            want = ipaddress.ip_network(str(ip_range))
        except ValueError:
            raise invalid("ip_range must be a subnet in CIDR notation", "ip_range") from None
        subnets = [sn for sn in subnets if sn == want]
        if not subnets:
            raise invalid("ip_range is not a subnet of the network in its network zone", "ip_range")
    used = addresses_in(lab.state, net["id"]) | lab_addresses(lab)
    if ip:
        try:
            address = ipaddress.ip_address(str(ip))
        except ValueError:
            raise invalid("ip must be an IP address", "ip") from None
        if ip_range is not None and address not in subnets[0]:
            raise invalid("ip is not part of ip_range", "ip")
        if str(address) in used or not any(address in sn for sn in subnets):
            raise ApiError(422, "ip_not_available", f"IP {address} is not available")
        return str(address)
    ip = next((str(h) for sn in subnets for h in sn.hosts() if str(h) not in used), None)
    if not ip:
        raise ApiError(422, "no_subnet_available", "no free IP in the network")
    return ip


def alias_ips(lab, nic: dict, ips) -> list[str]:
    """Alias IPs of a NIC: free addresses of the network's subnets (reserved like primary ones)."""
    if ips is None:
        return []
    if not isinstance(ips, list):
        raise invalid("alias_ips is a list of IPs", "alias_ips")
    net = lab.state["networks"][nic["network"]]
    subnets = [ipaddress.ip_network(x["ip_range"]) for x in net["subnets"]]
    taken = addresses_in(lab.state, net["id"], except_nic=nic) | lab_addresses(lab) | {nic["ip"]}
    out = []
    for ip in ips:
        try:
            address = ipaddress.IPv4Address(str(ip))
        except ValueError:
            raise invalid(f"{ip!r} is not an IPv4 address", "alias_ips") from None
        if not any(address in sn for sn in subnets) or str(address) in taken or str(address) in out:
            raise ApiError(422, "ip_not_available", f"IP {address} is not available")
        out.append(str(address))
    return out


def add_nic(lab, server: dict, nid: int, ip: str | None, ip_range: str | None = None) -> dict:
    """A NIC of the server in a network (state only; the fabric adds it to the VM)."""
    state = lab.state
    net = state["networks"].get(nid)
    if not net or net.get("_deleting"):
        raise invalid(f"network {nid} not found")
    nics = server["_lab"]["nics"]
    if any(n["network"] == nid for n in nics):
        raise ApiError(422, "server_already_attached", "server is already attached to this network")
    if len(nics) >= NETWORKS_PER_SERVER:
        raise limit_exceeded(f"a server is in at most {NETWORKS_PER_SERVER} networks", "networks")
    network_range = ipaddress.ip_network(net["ip_range"])
    if any(network_range.overlaps(ipaddress.ip_network(state["networks"][n["network"]]["ip_range"])) for n in nics):
        raise ApiError(422, "networks_overlap", "the network overlaps another network of the server")
    zone = lab.cfg["locations"][server["location"]].get("network_zone", "eu-central")
    ip = network_ip(lab, net, ip, zone, ip_range)
    used = {int(n["tap"].rsplit("x", 1)[1]) for n in nics}
    number = next(i for i in range(1, 256) if i not in used)   # the lowest free: MAC and tap name stay valid
    nic = {"network": nid, "ip": ip, "alias_ips": [], "tap": f"prv{server['_lab']['index']}x{number}",
           "mac": private_mac(server["_lab"]["index"], number)}
    nics.append(nic)
    return nic


def server_nic(server: dict, body: dict) -> dict:
    nid = int(body.get("network") or 0)
    nic = next((n for n in server["_lab"]["nics"] if n["network"] == nid), None)
    if not nic:
        raise invalid("server is not attached to this network")
    return nic


# --- server actions (see servers.SERVER_ACTIONS) -------------------------------------------------------------------

def attach_to_network(lab, server: dict, body: dict):
    """A running server gets the NIC hot-plugged; one that is off at its next start."""
    with lab.state.lock:
        nic = add_nic(lab, server, int(body.get("network") or 0), body.get("ip"), body.get("ip_range"))
        try:
            nic["alias_ips"] = alias_ips(lab, nic, body.get("alias_ips"))
        except ApiError:
            server["_lab"]["nics"].remove(nic)
            raise
        lab.state.save()

    async def steps():
        try:
            await lab.blocking(lab.fabric.attach_nic, server, nic)
        except Exception:   # the NIC goes again: no network reported that the VM does not have
            await lab.blocking(lab.fabric.detach_nic, server, nic)
            with lab.state.lock:
                if nic in server["_lab"]["nics"]:
                    server["_lab"]["nics"].remove(nic)
                lab.state.save()
            await lab.blocking(lab.fabric.write_dhcp)
            raise
        await lab.sleep(3)
    return steps, {}


def detach_from_network(lab, server: dict, body: dict):
    nic = server_nic(server, body)

    async def steps():
        await lab.blocking(lab.fabric.detach_nic, server, nic)
        with lab.state.lock:
            server["_lab"]["nics"].remove(nic)
            lab.state.save()
        await lab.blocking(lab.fabric.write_dhcp)
        await lab.blocking(lab.fabric.apply_faults)
        await lab.sleep(2)
    return steps, {}


def change_alias_ips(lab, server: dict, body: dict):
    """The lab routes them to the server; the guest configures them itself."""
    nic = server_nic(server, body)
    with lab.state.lock:
        nic["alias_ips"] = alias_ips(lab, nic, body.get("alias_ips"))
        lab.state.save()

    async def steps():
        if await lab.blocking(lab.fabric.vm_alive, server):
            await lab.blocking(lab.fabric.nic_up, nic)
        await lab.blocking(lab.fabric.apply_routes)   # an alias IP may be the gateway of a route
        await lab.sleep(2)
    return steps, {}


# --- networks ----------------------------------------------------------------------------------------------------

def add_subnet(network: dict, subnet: dict) -> None:
    """A subnet as Hetzner checks it: inside the network, at least /30, no overlap with subnets or routes."""
    ip_range = ipaddress.ip_network(network["ip_range"])
    taken = [ipaddress.ip_network(x["ip_range"]) for x in network["subnets"]]
    taken += [ipaddress.ip_network(r["destination"]) for r in network["routes"]]
    if subnet.get("ip_range"):
        subnet_range = cidr(subnet["ip_range"], "ip_range")
    else:   # the next free /24
        subnet_range = next((c for c in ip_range.subnets(new_prefix=max(24, ip_range.prefixlen))
                             if not any(c.overlaps(t) for t in taken)), None)
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
    network["subnets"].append({"type": subnet.get("type", "cloud"), "ip_range": str(subnet_range),
                               "network_zone": str(subnet.get("network_zone", "eu-central")),
                               "gateway": network_gateway(network["ip_range"]), "vswitch_id": subnet.get("vswitch_id")})


def add_route(network: dict, route: dict) -> None:
    """A route as Hetzner checks it: an RFC 1918 destination (or 0.0.0.0/0) clear of subnets and other routes, a
    gateway inside the network that is neither its first IP nor 172.31.1.1."""
    dest = cidr(route.get("destination"), "destination")
    default = dest == ipaddress.ip_network("0.0.0.0/0")
    if not default and not private_range(dest):
        raise invalid("destination must be a private range (RFC 1918) or 0.0.0.0/0", "destination")
    taken = [ipaddress.ip_network(x["ip_range"]) for x in network["subnets"]]
    taken += [ipaddress.ip_network(x["destination"]) for x in network["routes"]]
    if not default and any(dest.overlaps(t) for t in taken):
        raise invalid("destination overlaps a subnet or another route", "destination")
    try:
        gateway = ipaddress.IPv4Address(str(route.get("gateway")))
    except ValueError:
        raise invalid("gateway must be an IPv4 address", "gateway") from None
    reserved = (network_gateway(network["ip_range"]), C.GATEWAY)
    if gateway not in ipaddress.ip_network(network["ip_range"]) or str(gateway) in reserved:
        raise invalid("gateway must be an address of the network other than its first IP", "gateway")
    network["routes"].append({"destination": str(dest), "gateway": str(gateway)})


def create_network(lab, body: dict) -> dict:
    state = lab.state
    if not body.get("name") or not body.get("ip_range"):
        raise invalid("name and ip_range are required")
    ip_range = network_range(body["ip_range"])
    network = {"name": body["name"], "ip_range": str(ip_range), "subnets": [], "routes": [],
               "labels": body.get("labels") or {}, "protection": {"delete": False}, "created": now(),
               "expose_routes_to_vswitch": bool(body.get("expose_routes_to_vswitch", False))}
    for sn in body.get("subnets") or []:   # everything checked before the network exists
        add_subnet(network, sn)
    for r in body.get("routes") or []:
        add_route(network, r)
    gateway = network_gateway(network["ip_range"])
    with state.lock:
        unique_name(state["networks"].values(), network["name"], None, "network")
        # the first address becomes the lab's (its gateway in the network): not one in use elsewhere – the gateway
        # of another network is fine (overlapping networks share it)
        used = {ip for x in state["networks"] for ip in addresses_in(state, x)}
        shared = {network_gateway(x["ip_range"]) for x in state["networks"].values()}
        if gateway in used or (gateway in lab.fabric.local_addresses(fresh=True) and gateway not in shared):
            raise invalid(f"the network's first address {gateway} (the lab's gateway in it) is in use", "ip_range")
        network["id"] = state.new_id("networks")
        state["networks"][network["id"]] = network
        state.save()
    return {"network": H.network_json(state, network)}


def network_or_404(lab, nid: int) -> dict:
    network = lab.state["networks"].get(nid)
    if not network:
        raise not_found("network")
    return network


def network_action(lab, nid: int, cmd: str, body: dict) -> dict:
    network = network_or_404(lab, nid)
    with lab.state.lock:
        if cmd == "add_subnet":
            add_subnet(network, body)
        elif cmd == "delete_subnet":
            subnet_range = cidr(body.get("ip_range"), "ip_range")
            if any(ipaddress.ip_address(ip) in subnet_range for ip in addresses_in(lab.state, nid)):
                raise invalid("subnets with attached resources cannot be deleted", "ip_range")
            network["subnets"] = [x for x in network["subnets"] if x["ip_range"] != str(subnet_range)]
        elif cmd == "add_route":
            add_route(network, body)
        elif cmd == "delete_route":
            network["routes"] = [r for r in network["routes"]
                                 if (r["destination"], r["gateway"]) != (body.get("destination"), body.get("gateway"))]
        elif cmd == "change_ip_range":
            old, new = ipaddress.ip_network(network["ip_range"]), network_range(body.get("ip_range"))
            if new.network_address != old.network_address or new.prefixlen > old.prefixlen:
                raise invalid("the range can only be extended: same first address, smaller netmask", "ip_range")
            network["ip_range"] = str(new)
        elif cmd == "change_protection":
            network["protection"]["delete"] = bool(body.get("delete", network["protection"]["delete"]))
        else:
            raise unsupported(f"the action {cmd!r}")
        lab.state.save()
    if cmd == "change_ip_range":
        lab.fabric.refresh_network(nid)
    lab.fabric.write_dhcp()
    lab.fabric.apply_routes()
    return {"action": lab.done(cmd, [(nid, "network")])}


def delete_network(lab, nid: int) -> None:
    """Deleting a network detaches its servers, as on Hetzner (each of them locked meanwhile)."""
    state = lab.state
    network = network_or_404(lab, nid)
    if network["protection"]["delete"]:
        raise ApiError(423, "protected", "network is delete protected")
    with state.lock:   # from here no server can join it (add_nic refuses)
        if network.get("_deleting"):
            raise locked("the network is being deleted")
        network["_deleting"] = True
        members = [s for s in state["servers"].values() if any(x["network"] == nid for x in s["_lab"]["nics"])]
    reserved = []
    try:
        for s in members:   # 423 if one of them has an action running
            lab.reserve(s)
            reserved.append(s)
        for s in members:
            for nic in [x for x in s["_lab"]["nics"] if x["network"] == nid]:
                lab.fabric.detach_nic(s, nic)
                with state.lock:
                    s["_lab"]["nics"].remove(nic)
        with state.lock:
            del state["networks"][nid]
            state.save()
    finally:
        network.pop("_deleting", None)
        for s in reserved:
            lab.release(s)
    lab.fabric.drop_network(nid)


# --- routes ------------------------------------------------------------------------------------------------------

def add_routes(app: FastAPI, lab) -> None:
    state = lab.state

    @app.get("/v1/networks")
    def networks(request: Request):
        items = filtered(request, list(state["networks"].values()))
        return paginate(request, [H.network_json(state, n) for n in items], "networks")

    @app.post("/v1/networks", status_code=201)
    async def network_create(request: Request):
        return await lab.blocking(create_network, lab, await read_body(request))

    @app.get("/v1/networks/{nid}/members")
    def network_members(request: Request, nid: int):
        network = network_or_404(lab, nid)

        def subnet(ip: str) -> str:
            return next((x["ip_range"] for x in network["subnets"]
                         if ipaddress.ip_address(ip) in ipaddress.ip_network(x["ip_range"])), network["ip_range"])
        members = [{"type": "server", "id": s["id"], "ip": nic["ip"], "status": "ok",
                    "alias_ips": nic.get("alias_ips") or [], "subnet": subnet(nic["ip"])}
                   for s in state["servers"].values() for nic in s["_lab"]["nics"] if nic["network"] == nid]
        query = request.query_params
        if query.get("type"):
            members = [m for m in members if m["type"] in query["type"].split(",")]
        if query.get("subnet"):
            members = [m for m in members if m["subnet"] == query["subnet"]]
        return paginate(request, members, "members")

    @app.get("/v1/networks/{nid}")
    def network_get(nid: int):
        return {"network": H.network_json(state, network_or_404(lab, nid))}

    @app.put("/v1/networks/{nid}")
    async def network_update(nid: int, request: Request):
        network = network_or_404(lab, nid)
        body = await read_body(request)
        with state.lock:
            unique_name(state["networks"].values(), body.get("name"), network, "network")
            for k in ("name", "labels", "expose_routes_to_vswitch"):
                if k in body:
                    network[k] = body[k] if k != "labels" else (body[k] or {})
            state.save()
        return {"network": H.network_json(state, network)}

    @app.delete("/v1/networks/{nid}", status_code=204)
    def network_delete(nid: int):
        delete_network(lab, nid)
        return Response(status_code=204)

    @app.post("/v1/networks/{nid}/actions/{cmd}", status_code=201)
    async def network_post_action(nid: int, cmd: str, request: Request):
        return await lab.blocking(network_action, lab, nid, cmd, await read_body(request))
