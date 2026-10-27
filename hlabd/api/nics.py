"""A server's NICs in cloud networks: attach, detach and alias IPs.

The three server actions here are part of the server's action table
(servers.SERVER_ACTIONS). Each NIC gets a free address of the network.
"""

import ipaddress
from contextlib import suppress

from .. import config
from ..errors import (
    ApiError,
    invalid,
    ip_not_available,
    limit_exceeded,
    no_subnet_available,
)
from ..fabric.addresses import network_gateway, private_mac
from ..lab import (
    ALIAS_IPS_SECONDS,
    NIC_ATTACH_SECONDS,
    NIC_DETACH_SECONDS,
    Lab,
    Plan,
    RecordGoneError,
)
from ..records import CloudNetwork, Nic, Server
from ..state import NETWORKS, SERVERS
from . import system
from .faults import drop_netem
from .networks import addresses_in
from .web import ref_id

NETWORKS_PER_SERVER = 3  # Hetzner's limit


def lab_addresses(lab: Lab, local_addresses: set[str]) -> set[str]:
    """Return the addresses no server gets.

    These are the lab's gateways in the networks, Hetzner's gateway (config.GATEWAY)
    and the container's own addresses (`local_addresses`). The caller reads those in
    a thread, since that runs a script:
    `await lab.blocking(lab.fabric.network.read_local_addresses)`.
    """
    gateways = {network_gateway(n["ip_range"]) for n in lab.state[NETWORKS].values()}
    return gateways | {config.GATEWAY} | local_addresses


def network_ip(
    lab: Lab,
    net: CloudNetwork,
    ip: str | None,
    local_addresses: set[str],
    zone: str = "eu-central",
    ip_range: str | None = None,
) -> str:
    """Return the requested address of a cloud network if it is free, else a free one.

    `ip_range` picks the subnet; `local_addresses` are the container's own addresses
    (`lab_addresses`).
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
    used = addresses_in(lab, net["id"]) | lab_addresses(lab, local_addresses)
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


def checked_alias_ips(lab: Lab, nic: Nic, ips, local_addresses: set[str]) -> list[str]:
    """Return the checked alias IPs of a NIC.

    They must be free addresses in the network's subnets, and none of the container's
    own addresses (`local_addresses`, see `lab_addresses`). Once set, they are taken
    like a NIC's own IP. The caller sets them without an await in between, so the
    addresses found free are still free then.
    """
    if ips is None:
        return []
    if not isinstance(ips, list):
        raise invalid("alias_ips is a list of IPs", "alias_ips")
    net = lab.state[NETWORKS][nic["network"]]
    subnets = [ipaddress.ip_network(x["ip_range"]) for x in net["subnets"]]
    taken = (
        addresses_in(lab, net["id"], except_nic=nic)
        | lab_addresses(lab, local_addresses)
        | {nic["ip"]}
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
    lab: Lab,
    server: Server,
    nid: int,
    ip: str | None,
    local_addresses: set[str],
    ip_range: str | None = None,
) -> Nic:
    """Add a NIC in a network to the server and return it.

    This changes only the state, in the caller's `change()`. The fabric adds the NIC
    to the VM later. `local_addresses` are the container's own addresses, which the
    NIC must not get (`lab_addresses`).
    """
    state = lab.state
    net = state[NETWORKS].get(nid)
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
            ipaddress.ip_network(state[NETWORKS][n["network"]]["ip_range"])
        )
        for n in nics
    ):
        raise ApiError(
            422,
            "networks_overlap",
            "the network overlaps another network of the server",
        )
    zone = lab.cfg["locations"][server["location"]].get("network_zone", "eu-central")
    ip = network_ip(lab, net, ip, local_addresses, zone, ip_range)
    used = {n["number"] for n in nics}
    # the lowest free number, so the MAC and the tap name stay valid
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


def server_nic(lab: Lab, server: Server, body: dict) -> Nic:
    """Return the server's NIC in the network the body names, or raise invalid_input."""
    nid = ref_id(body.get("network"), "network")
    nic = next((n for n in server["_lab"]["nics"] if n["network"] == nid), None)
    if not nic:
        raise invalid("server is not attached to this network")
    return nic


def remove_nic(lab: Lab, server: Server, nic: Nic) -> None:
    """Remove a NIC from the server's record, with its netem, and save the state."""
    with lab.state.change():
        if nic in server["_lab"]["nics"]:
            server["_lab"]["nics"].remove(nic)
        drop_netem(lab, [nic["tap"]])  # the next NIC may get this tap name


async def attach_to_network(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan an attach to a network.

    A running server gets the NIC hot-plugged. A server that is off gets it at its
    next start.
    """
    sid = server["id"]
    # runs a script: the addresses that no NIC may get
    local_addresses = await lab.blocking(lab.fabric.network.read_local_addresses)
    server = lab.server_or_404(sid)  # looked up again after the await
    with lab.state.change():
        nic = add_nic(
            lab,
            server,
            ref_id(body.get("network"), "network"),
            body.get("ip"),
            local_addresses,
            body.get("ip_range"),
        )
        nic["alias_ips"] = checked_alias_ips(
            lab, nic, body.get("alias_ips"), local_addresses
        )

    nid, tap = nic["network"], nic["tap"]

    async def steps():
        async def undo():
            # remove the NIC again, so the API reports no network the VM does not have
            await lab.blocking(lab.fabric.network.detach_nic, sid, tap)
            with suppress(RecordGoneError):  # nothing to undo if it is gone
                remove_nic(
                    lab, lab.current(SERVERS, sid), system.current_nic(lab, sid, nid)
                )
            await system.write_dhcp(lab)

        async with lab.undo_on_failure(undo):
            await system.attach_nic(
                lab, lab.current(SERVERS, sid), system.current_nic(lab, sid, nid)
            )
        await lab.sleep(NIC_ATTACH_SECONDS)

    return Plan(steps, server=server)


async def detach_from_network(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a detach from a network."""
    nic = server_nic(lab, server, body)
    sid, nid, tap = server["id"], nic["network"], nic["tap"]

    async def steps():
        await lab.blocking(lab.fabric.network.detach_nic, sid, tap)
        remove_nic(lab, lab.current(SERVERS, sid), system.current_nic(lab, sid, nid))
        await system.write_dhcp(lab)
        await system.apply_faults(lab)
        await lab.sleep(NIC_DETACH_SECONDS)

    return Plan(steps, server=server)


async def change_alias_ips(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a change of alias IPs.

    The lab routes the alias IPs to the server; the guest sets them up itself.
    """
    sid = server["id"]
    server_nic(lab, server, body)  # checked before the script runs
    # runs a script: the addresses that no NIC may get
    local_addresses = await lab.blocking(lab.fabric.network.read_local_addresses)
    server = lab.server_or_404(sid)  # looked up again after the await
    nic = server_nic(lab, server, body)
    nid, old = nic["network"], list(nic["alias_ips"])
    with lab.state.change():
        nic["alias_ips"] = checked_alias_ips(
            lab, nic, body.get("alias_ips"), local_addresses
        )

    async def undo():
        # the old alias IPs come back, in the state and on the system
        with suppress(RecordGoneError):  # nothing to undo if it is gone
            with lab.state.change():
                system.current_nic(lab, sid, nid)["alias_ips"] = old
            if await lab.blocking(lab.fabric.vms.is_alive, sid):
                await system.nic_up(lab, system.current_nic(lab, sid, nid))
        await system.apply_routes(lab)

    async def steps():
        async with lab.undo_on_failure(undo):
            if await lab.blocking(lab.fabric.vms.is_alive, sid):
                await system.nic_up(lab, system.current_nic(lab, sid, nid))
            # an alias IP may be the gateway of a route
            await system.apply_routes(lab)
        await lab.sleep(ALIAS_IPS_SECONDS)

    return Plan(steps, server=server)
