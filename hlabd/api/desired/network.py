"""Taps, DHCP, routes and boot volumes of the servers, and the clients' links."""

import ipaddress

from ... import config
from ...fabric.addresses import network_gateway, public_mac
from ...fabric.network import (
    ClientLink,
    DhcpHost,
    PrivateTap,
    PublicTap,
    ServerTaps,
    WantedRoute,
)
from ...records import (
    Client,
    Nic,
    Server,
)
from ...state import (
    NETWORKS,
    SERVERS,
    VOLUMES,
    State,
    nic_addresses,
    nics_in,
    primary_address_of,
)

# the PCIe root ports a NIC can be hot-plugged into (the lab hot-plugs 4 NICs per boot)
HOT_PLUG_PORTS = ("hp1", "hp2", "hp3", "hp4")


def public_tap(server: Server) -> PublicTap:
    """Return a server's public tap, with what routes to the server need."""
    return PublicTap(server["id"], server["_lab"]["tap"], server["_lab"]["index"])


def client_link(client: Client) -> ClientLink:
    """Return an Internet client's namespace and veth, as the fabric sets them up."""
    return ClientLink(client["name"], client["veth"], client["ip"], client["ip6"])


def private_tap(state: State, nic: Nic) -> PrivateTap:
    """Return how a NIC's tap in its cloud network is set up."""
    return PrivateTap(
        nic["tap"],
        nic["mac"],
        nic["ip"],
        nic["network"],
        state[NETWORKS][nic["network"]]["ip_range"],
        list(nic["alias_ips"]),
    )


def server_taps(state: State, server: Server) -> ServerTaps:
    """Return what a server's taps are set up with when its VM starts."""
    return ServerTaps(
        public_tap(server),
        primary_address_of(state, server, "ipv4"),
        primary_address_of(state, server, "ipv6"),
        [private_tap(state, nic) for nic in server["_lab"]["nics"]],
    )


def free_hot_plug_port(server: Server) -> str | None:
    """Return a root port free for hot-plugging a NIC into the server, or None."""
    used = {nic.get("port") for nic in server["_lab"]["nics"]}
    return next((port for port in HOT_PLUG_PORTS if port not in used), None)


def dhcp_hosts(state: State) -> list[DhcpHost]:
    """Return the fixed address of every tap, with DHCP options like Hetzner's.

    Each NIC gets a fixed address as a /32, with routes. On the public interface the
    route goes to 172.31.1.1. In a cloud network it goes to the network's gateway and
    range.
    """
    hosts = []
    for server in state[SERVERS].values():
        server_lab = server["_lab"]
        ipv4 = primary_address_of(state, server, "ipv4")
        if ipv4:
            # primary IPv4 as /32, on-link gateway 172.31.1.1, lab resolver
            hosts.append(
                DhcpHost(
                    server_lab["tap"],
                    public_mac(server_lab["index"]),
                    ipv4,
                    [
                        f"dhcp-range={config.PUBLIC_NET}.0,"
                        "static,255.255.255.0,infinite",
                        "dhcp-option=option:netmask,255.255.255.255",
                        f"dhcp-option=option:router,{config.GATEWAY}",
                        f"dhcp-option=121,{config.GATEWAY}/32,"
                        f"0.0.0.0,0.0.0.0/0,{config.GATEWAY}",
                        f"dhcp-option=option:dns-server,{config.SERVICES}",
                    ],
                )
            )
        for nic in server_lab["nics"]:
            network = state[NETWORKS].get(nic["network"])
            if not network:  # its network was deleted in the meantime
                continue
            ip_range = ipaddress.ip_network(network["ip_range"])
            gateway = network_gateway(network["ip_range"])
            # the network's routes go to its gateway too; the lab forwards them to
            # the route's server
            routes = "".join(
                f",{r['destination']},{gateway}" for r in network["routes"]
            )
            hosts.append(
                DhcpHost(
                    nic["tap"],
                    nic["mac"],
                    nic["ip"],
                    [
                        f"dhcp-range={ip_range.network_address},"
                        f"static,{ip_range.netmask},infinite",
                        "dhcp-option=option:netmask,255.255.255.255",
                        "dhcp-option=option:router",
                        f"dhcp-option=121,{gateway}/32,0.0.0.0,"
                        f"{ip_range},{gateway}{routes}",
                    ],
                )
            )
    return hosts


def wanted_routes(state: State) -> list[WantedRoute]:
    """Return every route of every cloud network, with the NICs holding its gateway."""
    return [
        WantedRoute(
            route["destination"],
            route["gateway"],
            network["id"],
            [
                (server["id"], nic["tap"])
                for server, nic in nics_in(state, network["id"])
                if route["gateway"] in nic_addresses(nic)
            ],
        )
        for network in state[NETWORKS].values()
        for route in network["routes"]
    ]


def boot_volumes(state: State, server: Server) -> list[int]:
    """Return the IDs of the volumes a server's VM gets at boot.

    Those are the volumes attached to it whose disk exists.
    """
    return [
        v["id"]
        for v in state[VOLUMES].values()
        if v.get("server") == server["id"] and v["status"] == "available"
    ]
