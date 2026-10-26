"""Put the state in place on the system.

Each function here first reads what it needs from the state, on the event loop. Most
of that goes through the pure functions of desired.py. Then it hands the plain data to
the fabric in a thread (`lab.blocking`); the fabric never sees the state. When the
fabric reports something back for the state (the routes it added), the function
stores it here, on the loop again.

Some functions put a whole picture in place: fault sets, latency, firewall, DHCP or
routes. They compute it and put it in place while they hold `lab.picture_lock`. So
pictures are put in place in the order they were computed, and an older picture never
overwrites a newer one. The lock is not re-entrant: a function that holds it must not
call another function here that takes it.
"""

from ..lab import Lab
from ..records import Client, Nic, Server
from ..state import nics_in
from . import desired

# --- whole pictures -------------------------------------------------------------------


async def apply_faults(lab: Lab) -> None:
    """Put the fault sets, sources and network members in place (table inet hlab)."""
    async with lab.picture_lock:
        sets = desired.fault_sets(lab.state)
        tap_networks = desired.tap_networks(lab.state)
        await lab.blocking(lab.fabric.faults.apply, sets, tap_networks)


async def apply_latency(lab: Lab) -> None:
    """Put the latency between locations and the netem faults in place."""
    async with lab.picture_lock:
        latency = desired.latency(lab.state, lab.cfg)
        await lab.blocking(lab.fabric.faults.apply_latency, latency)


async def apply_firewall(lab: Lab) -> None:
    """Put the firewall table in place with the rules of all firewalls."""
    async with lab.picture_lock:
        servers = desired.firewall_rules(lab.state)
        await lab.blocking(lab.fabric.firewall.apply, servers)


async def write_dhcp(lab: Lab) -> None:
    """Hand every tap that exists its DHCP configuration."""
    async with lab.picture_lock:
        hosts = desired.dhcp_hosts(lab.state)
        await lab.blocking(lab.fabric.network.write_dhcp, hosts)


async def apply_routes(lab: Lab) -> None:
    """Put the routes of cloud networks in place, replacing the routes added before.

    The state keeps the added routes (`net_routes`), so the next call can remove them.
    """
    state = lab.state
    async with lab.picture_lock:
        old_routes = list(state.data["net_routes"])
        wanted = desired.wanted_routes(state)
        applied = await lab.blocking(
            lab.fabric.network.apply_routes, old_routes, wanted
        )
        with state.change():
            state.data["net_routes"] = applied


async def route_floating_ip(lab: Lab, ip: str, server: Server | None) -> None:
    """Route a Floating IP to a server (or nowhere), in order with the reroutes.

    The caller stores the new assignment first. A reroute that read the old
    assignment then puts its route in place before this one, never after
    (`reroute_floating_ips`).
    """
    async with lab.picture_lock:
        await lab.blocking(lab.fabric.network.route_floating_ip, ip, server)


# --- a VM's start and stop ------------------------------------------------------------


async def start_vm(lab: Lab, server: Server) -> None:
    """Start the server's VM with its taps, DHCP and volumes.

    Then it applies the routes, faults and latency that involve the server. If the VM
    does not start, its taps are taken down again.
    """
    state, fabric = lab.state, lab.fabric
    if await lab.blocking(fabric.vms.is_alive, server):
        return
    try:
        taps = desired.server_taps(state, server)
        # at boot each network NIC gets its own root port (bp<n>); hp1..hp4 are free
        with state.change():
            for nic in server["_lab"]["nics"]:
                nic.pop("port", None)
        nics = await lab.blocking(fabric.network.taps_up, server, taps)
        await write_dhcp(lab)
        volumes = desired.boot_volumes(state, server)
        await lab.blocking(fabric.volumes.write_boot_list, server, volumes)
        await lab.blocking(
            fabric.vms.launch, server, server["name"], server["server_type"], nics
        )
    except Exception:  # whatever failed: remove the half-started VM, then re-raise
        await kill_vm(lab, server)
        raise
    await reroute_floating_ips(lab, server)
    await apply_faults(lab)
    await apply_latency(lab)
    await apply_routes(lab)


async def kill_vm(lab: Lab, server: Server) -> None:
    """Stop the server's VM and take its taps and DHCP down."""
    await lab.blocking(lab.fabric.vms.kill, server, server["name"])
    server_lab = server["_lab"]
    taps = [server_lab["tap"], *(nic["tap"] for nic in server_lab["nics"])]
    await lab.blocking(lab.fabric.network.taps_down, taps)
    await write_dhcp(lab)  # this also stops the dnsmasq of each removed tap


async def delete_vm(lab: Lab, server: Server) -> None:
    """Stop the server's VM and remove its directory."""
    await kill_vm(lab, server)
    await lab.blocking(lab.fabric.vms.remove_directory, server)


async def reroute_floating_ips(lab: Lab, server: Server) -> None:
    """Route the Floating IPs assigned to a server to it again, after it started.

    Hetzner keeps routing an assigned IP across a power cycle, but the lab removed the
    route together with the old tap. The IPs are read and routed while the picture
    lock is held. So a move that routes an IP elsewhere at the same time
    (`route_floating_ip`) comes before or after this, never in between.
    """
    async with lab.picture_lock:
        ips = [
            f["ip"]
            for f in lab.state["floating_ips"].values()
            if f.get("server") == server["id"]
        ]
        for ip in ips:
            await lab.blocking(lab.fabric.network.route_floating_ip, ip, server)


# --- NICs, networks, clients ----------------------------------------------------------


async def nic_up(lab: Lab, nic: Nic) -> None:
    """Bring up a NIC's tap with its network's range and its alias IPs."""
    tap = desired.private_tap(lab.state, nic)
    await lab.blocking(lab.fabric.network.nic_up, tap)


async def attach_nic(lab: Lab, server: Server, nic: Nic) -> None:
    """Attach a NIC to a server, hot-plugged if its VM runs.

    The NIC is already in the state. Afterwards its faults, routes and latency are
    applied.
    """
    state, fabric = lab.state, lab.fabric
    if await lab.blocking(fabric.vms.is_alive, server):
        tap = desired.private_tap(state, nic)
        await lab.blocking(fabric.network.nic_up, tap)
        port = desired.free_hot_plug_port(server)
        if port is None:
            raise RuntimeError(
                "no free hot-plug port (the lab hot-plugs 4 NICs per boot)"
            )
        with state.change():
            nic["port"] = port
        await write_dhcp(lab)  # the tap exists now: its dnsmasq binds to it
        await lab.blocking(fabric.network.hot_plug_nic, server, tap, port)
    else:
        await write_dhcp(lab)
    await apply_faults(lab)
    await apply_routes(lab)  # a gateway server attached now gets the network's routes
    await apply_latency(lab)


async def refresh_cloud_network(lab: Lab, nid: int) -> None:
    """Set a network up again after its range changed: its table, its members' taps."""
    async with lab.picture_lock:
        members = [
            (server, desired.private_tap(lab.state, nic))
            for server, nic in nics_in(lab.state, nid)
        ]
        await lab.blocking(lab.fabric.network.refresh_cloud_network, nid, members)


async def drop_cloud_network(lab: Lab, nid: int) -> None:
    """Clean up after a deleted network.

    This flushes the network's routing table and its routes. Then it updates DHCP, the
    fault sets and the routes of the other networks.
    """
    await lab.blocking(lab.fabric.network.drop_cloud_network, nid)
    await write_dhcp(lab)
    await apply_faults(lab)
    await apply_routes(lab)


async def add_client(lab: Lab, client: Client) -> None:
    """Add an Internet client with its namespace, then its sources and faults."""
    await lab.blocking(lab.fabric.network.add_client, client)
    await apply_faults(lab)


async def remove_client(lab: Lab, client: Client) -> None:
    """Remove an Internet client with its namespace, then its sources and faults."""
    await lab.blocking(lab.fabric.network.remove_client, client)
    await apply_faults(lab)
