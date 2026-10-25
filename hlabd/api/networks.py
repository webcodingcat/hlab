"""Cloud networks: subnets, routes, a server's NICs (attach, detach, alias IPs). Ranges, subnets and routes are
checked like Hetzner checks them. Each network routes in its own table, so networks may overlap."""

import ipaddress

from fastapi import FastAPI, Request
from fastapi.responses import Response

from .. import config as C
from ..fabric import network_gateway, private_mac, route_table
from . import hetzner as H
from .web import ApiError, body, filtered, invalid, limit_exceeded, not_found, paginate, unique_name, unsupported

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
    rng = cidr(value, "ip_range")
    if not private_range(rng):
        raise invalid("ip_range must be a private range (RFC 1918)", "ip_range")
    if rng.prefixlen > 24:
        raise invalid("the smallest network is a /24", "ip_range")
    return rng


def addresses_in(state, nid: int, but: dict | None = None) -> set[str]:
    """The private and alias IPs servers have in a network (but one NIC)."""
    return {ip for s in state["servers"].values() for n in s["_lab"]["nics"] if n["network"] == nid and n is not but
            for ip in [n["ip"], *(n.get("alias_ips") or [])]}


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
            addr = ipaddress.ip_address(str(ip))
        except ValueError:
            raise invalid("ip must be an IP address", "ip") from None
        if ip_range is not None and addr not in subnets[0]:
            raise invalid("ip is not part of ip_range", "ip")
        if str(addr) in used or not any(addr in sn for sn in subnets):
            raise ApiError(422, "ip_not_available", f"IP {addr} is not available")
        return str(addr)
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
    taken = addresses_in(lab.state, net["id"], but=nic) | lab_addresses(lab) | {nic["ip"]}
    out = []
    for ip in ips:
        try:
            a = ipaddress.IPv4Address(str(ip))
        except ValueError:
            raise invalid(f"{ip!r} is not an IPv4 address", "alias_ips") from None
        if not any(a in sn for sn in subnets) or str(a) in taken or str(a) in out:
            raise ApiError(422, "ip_not_available", f"IP {a} is not available")
        out.append(str(a))
    return out


def add_nic(lab, s: dict, nid: int, ip: str | None, ip_range: str | None = None) -> dict:
    """A NIC of the server in a network (state only; the fabric adds it to the VM)."""
    state = lab.state
    net = state["networks"].get(nid)
    if not net or net.get("_deleting"):
        raise invalid(f"network {nid} not found")
    nics = s["_lab"]["nics"]
    if any(n["network"] == nid for n in nics):
        raise ApiError(422, "server_already_attached", "server is already attached to this network")
    if len(nics) >= NETWORKS_PER_SERVER:
        raise limit_exceeded(f"a server is in at most {NETWORKS_PER_SERVER} networks", "networks")
    rng = ipaddress.ip_network(net["ip_range"])
    if any(rng.overlaps(ipaddress.ip_network(state["networks"][n["network"]]["ip_range"])) for n in nics):
        raise ApiError(422, "networks_overlap", "the network overlaps another network of the server")
    zone = lab.cfg["locations"][s["location"]].get("network_zone", "eu-central")
    ip = network_ip(lab, net, ip, zone, ip_range)
    used = {int(n["tap"].rsplit("x", 1)[1]) for n in nics}
    k = next(i for i in range(1, 256) if i not in used)   # the lowest free: MAC and tap name stay valid
    nic = {"network": nid, "ip": ip, "alias_ips": [], "tap": f"prv{s['_lab']['index']}x{k}",
           "mac": private_mac(s["_lab"]["index"], k)}
    nics.append(nic)
    return nic


def server_nic(s: dict, b: dict) -> dict:
    nid = int(b.get("network") or 0)
    nic = next((n for n in s["_lab"]["nics"] if n["network"] == nid), None)
    if not nic:
        raise invalid("server is not attached to this network")
    return nic


# --- server actions (see servers.SERVER_ACTIONS) -------------------------------------------------------------------

def attach_to_network(lab, s: dict, b: dict):
    """A running server gets the NIC hot-plugged; one that is off at its next start."""
    with lab.state.lock:
        nic = add_nic(lab, s, int(b.get("network") or 0), b.get("ip"), b.get("ip_range"))
        try:
            nic["alias_ips"] = alias_ips(lab, nic, b.get("alias_ips"))
        except ApiError:
            s["_lab"]["nics"].remove(nic)
            raise
        lab.state.save()

    async def steps():
        try:
            await lab.blocking(lab.fabric.attach_nic, s, nic)
        except Exception:   # the NIC goes again: no network reported that the VM does not have
            await lab.blocking(lab.fabric.detach_nic, s, nic)
            with lab.state.lock:
                if nic in s["_lab"]["nics"]:
                    s["_lab"]["nics"].remove(nic)
                lab.state.save()
            await lab.blocking(lab.fabric.write_dhcp)
            raise
        await lab.sleep(3)
    return steps, {}


def detach_from_network(lab, s: dict, b: dict):
    nic = server_nic(s, b)

    async def steps():
        await lab.blocking(lab.fabric.detach_nic, s, nic)
        with lab.state.lock:
            s["_lab"]["nics"].remove(nic)
            lab.state.save()
        await lab.blocking(lab.fabric.write_dhcp)
        await lab.blocking(lab.fabric.apply_faults)
        await lab.sleep(2)
    return steps, {}


def change_alias_ips(lab, s: dict, b: dict):
    """The lab routes them to the server; the guest configures them itself."""
    nic = server_nic(s, b)
    with lab.state.lock:
        nic["alias_ips"] = alias_ips(lab, nic, b.get("alias_ips"))
        lab.state.save()

    async def steps():
        if await lab.blocking(lab.fabric.vm_alive, s):
            await lab.blocking(lab.fabric._private_tap_up, nic)
        await lab.sleep(2)
    return steps, {}


# --- networks ----------------------------------------------------------------------------------------------------

def add_subnet(n: dict, sn: dict) -> None:
    """A subnet as Hetzner checks it: inside the network, at least /30, no overlap with subnets or routes."""
    rng = ipaddress.ip_network(n["ip_range"])
    taken = [ipaddress.ip_network(x["ip_range"]) for x in n["subnets"]]
    taken += [ipaddress.ip_network(r["destination"]) for r in n["routes"]]
    if sn.get("ip_range"):
        sub = cidr(sn["ip_range"], "ip_range")
    else:   # the next free /24
        sub = next((c for c in rng.subnets(new_prefix=max(24, rng.prefixlen))
                    if not any(c.overlaps(t) for t in taken)), None)
        if sub is None:
            raise invalid("no space for a subnet", "ip_range")
    if not sub.subnet_of(rng):
        raise invalid("subnet must be inside the network's ip_range", "ip_range")
    if sub.prefixlen > 30:
        raise invalid("the smallest subnet is a /30", "ip_range")
    if any(sub.overlaps(t) for t in taken):
        raise invalid("subnet overlaps another subnet or a route", "ip_range")
    if sn.get("type", "cloud") not in ("cloud", "server", "vswitch"):
        raise invalid("type must be cloud, server or vswitch", "type")
    n["subnets"].append({"type": sn.get("type", "cloud"), "ip_range": str(sub),
                         "network_zone": str(sn.get("network_zone", "eu-central")),
                         "gateway": network_gateway(n["ip_range"]), "vswitch_id": sn.get("vswitch_id")})


def add_route(n: dict, r: dict) -> None:
    """A route as Hetzner checks it: an RFC 1918 destination (or 0.0.0.0/0) clear of subnets and other routes, a
    gateway inside the network that is neither its first IP nor 172.31.1.1."""
    dest = cidr(r.get("destination"), "destination")
    default = dest == ipaddress.ip_network("0.0.0.0/0")
    if not default and not private_range(dest):
        raise invalid("destination must be a private range (RFC 1918) or 0.0.0.0/0", "destination")
    taken = [ipaddress.ip_network(x["ip_range"]) for x in n["subnets"]]
    taken += [ipaddress.ip_network(x["destination"]) for x in n["routes"]]
    if not default and any(dest.overlaps(t) for t in taken):
        raise invalid("destination overlaps a subnet or another route", "destination")
    try:
        gw = ipaddress.IPv4Address(str(r.get("gateway")))
    except ValueError:
        raise invalid("gateway must be an IPv4 address", "gateway") from None
    if gw not in ipaddress.ip_network(n["ip_range"]) or str(gw) in (network_gateway(n["ip_range"]), C.GATEWAY):
        raise invalid("gateway must be an address of the network other than its first IP", "gateway")
    n["routes"].append({"destination": str(dest), "gateway": str(gw)})


def create_network(lab, b: dict) -> dict:
    state = lab.state
    if not b.get("name") or not b.get("ip_range"):
        raise invalid("name and ip_range are required")
    rng = network_range(b["ip_range"])
    n = {"name": b["name"], "ip_range": str(rng), "subnets": [], "routes": [],
         "labels": b.get("labels") or {}, "protection": {"delete": False}, "created": H.now(),
         "expose_routes_to_vswitch": bool(b.get("expose_routes_to_vswitch", False))}
    for sn in b.get("subnets") or []:   # everything checked before the network exists
        add_subnet(n, sn)
    for r in b.get("routes") or []:
        add_route(n, r)
    gw = network_gateway(n["ip_range"])
    with state.lock:
        unique_name(state["networks"].values(), n["name"], None, "network")
        # the first address becomes the lab's (its gateway in the network): not one in use elsewhere – the gateway
        # of another network is fine (overlapping networks share it)
        used = {ip for x in state["networks"] for ip in addresses_in(state, x)}
        shared = {network_gateway(x["ip_range"]) for x in state["networks"].values()}
        if gw in used or (gw in lab.fabric.local_addresses(fresh=True) and gw not in shared):
            raise invalid(f"the network's first address {gw} (the lab's gateway in it) is in use", "ip_range")
        n["id"] = state.new_id("networks")
        state["networks"][n["id"]] = n
        state.save()
    return {"network": H.network(state, n)}


def network_action(lab, nid: int, cmd: str, b: dict) -> dict:
    n = lab.state["networks"].get(nid)
    if not n:
        raise not_found("network")
    with lab.state.lock:
        if cmd == "add_subnet":
            add_subnet(n, b)
        elif cmd == "delete_subnet":
            sub = cidr(b.get("ip_range"), "ip_range")
            if any(ipaddress.ip_address(ip) in sub for ip in addresses_in(lab.state, nid)):
                raise invalid("subnets with attached resources cannot be deleted", "ip_range")
            n["subnets"] = [x for x in n["subnets"] if x["ip_range"] != str(sub)]
        elif cmd == "add_route":
            add_route(n, b)
        elif cmd == "delete_route":
            n["routes"] = [r for r in n["routes"]
                           if (r["destination"], r["gateway"]) != (b.get("destination"), b.get("gateway"))]
        elif cmd == "change_ip_range":
            old, new = ipaddress.ip_network(n["ip_range"]), network_range(b.get("ip_range"))
            if new.network_address != old.network_address or new.prefixlen > old.prefixlen:
                raise invalid("the range can only be extended: same first address, smaller netmask", "ip_range")
            n["ip_range"] = str(new)
        elif cmd == "change_protection":
            n["protection"]["delete"] = bool(b.get("delete", n["protection"]["delete"]))
        else:
            raise unsupported(f"the action {cmd!r}")
        lab.state.save()
    if cmd == "change_ip_range":
        lab.fabric.refresh_network(nid)
    lab.fabric.write_dhcp()
    lab.fabric.apply_routes()
    a = lab.new_action(cmd, [(nid, "network")])
    lab.finish(a)
    return {"action": a}


def delete_network(lab, nid: int) -> None:
    """Deleting a network detaches its servers, as on Hetzner (each of them locked meanwhile)."""
    state = lab.state
    n = state["networks"].get(nid)
    if not n:
        raise not_found("network")
    if n["protection"]["delete"]:
        raise ApiError(423, "protected", "network is delete protected")
    with state.lock:   # from here no server can join it (add_nic refuses)
        if n.get("_deleting"):
            raise ApiError(423, "locked", "the network is being deleted")
        n["_deleting"] = True
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
        n.pop("_deleting", None)
        for s in reserved:
            s["locked"] = False
    lab.fabric.sh.run("route-flush", route_table(nid))
    lab.fabric.write_dhcp()
    lab.fabric.apply_faults()


# --- routes ------------------------------------------------------------------------------------------------------

def add_routes(app: FastAPI, lab) -> None:
    state = lab.state

    def network_or_404(nid: int) -> dict:
        n = state["networks"].get(nid)
        if not n:
            raise not_found("network")
        return n

    @app.get("/v1/networks")
    def networks(request: Request):
        items = filtered(request, list(state["networks"].values()))
        return paginate(request, [H.network(state, n) for n in items], "networks")

    @app.post("/v1/networks", status_code=201)
    async def network_create(request: Request):
        return await lab.blocking(create_network, lab, await body(request))

    @app.get("/v1/networks/{nid}/members")
    def network_members(request: Request, nid: int):
        n = network_or_404(nid)

        def subnet(ip: str) -> str:
            return next((x["ip_range"] for x in n["subnets"]
                         if ipaddress.ip_address(ip) in ipaddress.ip_network(x["ip_range"])), n["ip_range"])
        members = [{"type": "server", "id": s["id"], "ip": nic["ip"], "status": "ok",
                    "alias_ips": nic.get("alias_ips") or [], "subnet": subnet(nic["ip"])}
                   for s in state["servers"].values() for nic in s["_lab"]["nics"] if nic["network"] == nid]
        q = request.query_params
        if q.get("type"):
            members = [m for m in members if m["type"] in q["type"].split(",")]
        if q.get("subnet"):
            members = [m for m in members if m["subnet"] == q["subnet"]]
        return paginate(request, members, "members")

    @app.get("/v1/networks/{nid}")
    def network_get(nid: int):
        return {"network": H.network(state, network_or_404(nid))}

    @app.put("/v1/networks/{nid}")
    async def network_update(nid: int, request: Request):
        n = network_or_404(nid)
        b = await body(request)
        with state.lock:
            unique_name(state["networks"].values(), b.get("name"), n, "network")
            for k in ("name", "labels", "expose_routes_to_vswitch"):
                if k in b:
                    n[k] = b[k] if k != "labels" else (b[k] or {})
            state.save()
        return {"network": H.network(state, n)}

    @app.delete("/v1/networks/{nid}", status_code=204)
    def network_delete(nid: int):
        delete_network(lab, nid)
        return Response(status_code=204)

    @app.post("/v1/networks/{nid}/actions/{cmd}", status_code=201)
    async def network_post_action(nid: int, cmd: str, request: Request):
        return await lab.blocking(network_action, lab, nid, cmd, await body(request))
