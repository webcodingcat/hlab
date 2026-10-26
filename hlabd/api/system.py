"""Putting the state in place on the system.

Each function here reads what it needs from the state under its lock (mostly through
the pure functions of desired.py), then hands that plain data to the fabric, which never
sees the state. What the fabric reports back for the state (the routes it put in) is
stored here.

A whole picture (fault sets, latency, firewall, DHCP, routes) is computed and put in
place under the fabric's `net_lock`, so two of them are put in place in the order they
were computed and the last one put in place is the newest. The lock order is `net_lock`,
then `state.lock`: never call these with the state's lock held.
"""

from typing import TYPE_CHECKING

from ..records import Client, Nic, Server
from ..state import nics_in
from . import desired

if TYPE_CHECKING:
    from ..lab import Lab


# --- whole pictures -------------------------------------------------------------------


def apply_faults(lab: "Lab") -> None:
    """Put the fault sets, sources and network members in place (table inet hlab)."""
    with lab.fabric.net_lock:
        with lab.state.lock:
            sets = desired.fault_sets(lab.state)
            tap_networks = desired.tap_networks(lab.state)
        lab.fabric.faults.apply(sets, tap_networks)


def apply_latency(lab: "Lab") -> None:
    """Put the latency between locations and the netem faults in place."""
    with lab.fabric.net_lock:
        with lab.state.lock:
            latency = desired.latency(lab.state, lab.cfg)
        lab.fabric.faults.apply_latency(latency)


def apply_firewall(lab: "Lab") -> None:
    """Put the firewall table in place with the rules of all firewalls."""
    with lab.fabric.net_lock:
        with lab.state.lock:
            servers = desired.firewall_rules(lab.state)
        lab.fabric.firewall.apply(servers)


def write_dhcp(lab: "Lab") -> None:
    """Hand every tap that exists its DHCP configuration."""
    with lab.fabric.net_lock:
        with lab.state.lock:
            hosts = desired.dhcp_hosts(lab.state)
        lab.fabric.network.write_dhcp(hosts)


def apply_routes(lab: "Lab") -> None:
    """Put the routes of cloud networks in place, instead of those put in before.

    The routes put in are kept in the state (`net_routes`), to be removed next time.
    """
    state = lab.state
    with lab.fabric.net_lock:
        with state.lock:
            old_routes = list(state.data["net_routes"])
            wanted = desired.wanted_routes(state)
        applied = lab.fabric.network.apply_routes(old_routes, wanted)
        with state.lock:
            state.data["net_routes"] = applied


# --- a VM's start and stop ------------------------------------------------------------


def start_vm(lab: "Lab", server: Server) -> None:
    """Start the server's VM with its taps, DHCP and volumes.

    Then the routes, faults and latency that involve it are applied. If the VM does
    not start, its taps are taken down again.
    """
    state, fabric = lab.state, lab.fabric
    if fabric.vms.is_alive(server):
        return
    try:
        with state.lock:
            taps = desired.server_taps(state, server)
            # at boot each network NIC gets its own root port (bp<n>); hp1..hp4 are free
            for nic in server["_lab"]["nics"]:
                nic.pop("port", None)
        nics = fabric.network.taps_up(server, taps)
        write_dhcp(lab)
        with state.lock:
            volumes = desired.boot_volumes(state, server)
        fabric.volumes.write_boot_list(server, volumes)
        fabric.vms.launch(server, nics)
    except Exception:  # whatever failed: remove the half-started VM, then re-raise
        kill_vm(lab, server)
        raise
    reroute_floating_ips(lab, server)
    apply_faults(lab)
    apply_latency(lab)
    apply_routes(lab)


def kill_vm(lab: "Lab", server: Server) -> None:
    """Stop the server's VM and take its taps and DHCP down."""
    lab.fabric.vms.kill(server)
    with lab.state.lock:  # a copy: taking a tap down runs a script
        server_lab = server["_lab"]
        taps = [server_lab["tap"], *(nic["tap"] for nic in server_lab["nics"])]
    lab.fabric.network.taps_down(taps)
    write_dhcp(lab)  # the taps' dnsmasq instances are stopped too


def delete_vm(lab: "Lab", server: Server) -> None:
    """Stop the server's VM and remove its directory."""
    kill_vm(lab, server)
    lab.fabric.vms.remove_directory(server)


def reroute_floating_ips(lab: "Lab", server: Server) -> None:
    """Route the Floating IPs assigned to a server to it again, after it started.

    Hetzner keeps routing an assigned IP across a power cycle; the route was removed
    with the old tap. The check and the route happen under one lock: a move that
    routes the IP elsewhere meanwhile comes before or after, never in between.
    """
    with lab.fabric.net_lock:
        with lab.state.lock:
            ips = [
                f["ip"]
                for f in lab.state["floating_ips"].values()
                if f.get("server") == server["id"]
            ]
        for ip in ips:
            lab.fabric.network.route_floating_ip(ip, server)


# --- NICs, networks, clients ----------------------------------------------------------


def nic_up(lab: "Lab", nic: Nic) -> None:
    """Bring up a NIC's tap with its network's range and its alias IPs."""
    with lab.state.lock:
        tap = desired.private_tap(lab.state, nic)
    lab.fabric.network.nic_up(tap)


def attach_nic(lab: "Lab", server: Server, nic: Nic) -> None:
    """Attach a NIC to a server, hot-plugged if its VM runs.

    The NIC is in the state already; then its faults, routes and latency follow.
    """
    state, fabric = lab.state, lab.fabric
    if fabric.vms.is_alive(server):
        with state.lock:
            tap = desired.private_tap(state, nic)
        fabric.network.nic_up(tap)
        with state.lock:
            port = nic["port"] = desired.free_hot_plug_port(server)
        if port is None:
            raise RuntimeError(
                "no free hot-plug port (the lab hot-plugs 4 NICs per boot)"
            )
        write_dhcp(lab)  # the tap exists now: its dnsmasq binds to it
        fabric.network.hot_plug_nic(server, tap, port)
    else:
        write_dhcp(lab)
    apply_faults(lab)
    apply_routes(lab)  # a gateway server attached now gets the network's routes
    apply_latency(lab)


def refresh_cloud_network(lab: "Lab", nid: int) -> None:
    """Set a network up again after its range changed: its table, its members' taps."""
    with lab.fabric.net_lock:
        with lab.state.lock:
            members = [
                (server, desired.private_tap(lab.state, nic))
                for server, nic in nics_in(lab.state, nid)
            ]
        lab.fabric.network.refresh_cloud_network(nid, members)


def drop_cloud_network(lab: "Lab", nid: int) -> None:
    """Clean up after a deleted network.

    Its routing table is flushed with its routes; DHCP, the fault sets and the routes
    of the other networks are updated.
    """
    lab.fabric.network.drop_cloud_network(nid)
    write_dhcp(lab)
    apply_faults(lab)
    apply_routes(lab)


def add_client(lab: "Lab", client: Client) -> None:
    """Add an Internet client with its namespace, then its sources and faults."""
    lab.fabric.network.add_client(client)
    apply_faults(lab)


def remove_client(lab: "Lab", client: Client) -> None:
    """Remove an Internet client with its namespace, then its sources and faults."""
    lab.fabric.network.remove_client(client)
    apply_faults(lab)
