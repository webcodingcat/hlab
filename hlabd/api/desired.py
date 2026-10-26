"""What the system should look like, computed from the state.

Pure functions: the state (or a record) in, plain data out – the dataclasses of the
fabric's parts, lists and strings. They run nothing and change nothing. api/system.py
hands what they return to the fabric, which never sees the state. They run on the
event loop, which owns the state, so the records they read fit together.
"""

import copy
import ipaddress

from .. import config
from ..fabric.addresses import link_local, network_gateway, prefix, public_mac
from ..fabric.faults import Latency, Shaping
from ..fabric.firewall import ServerRules
from ..fabric.network import DhcpHost, PrivateTap, ServerTaps, WantedRoute
from ..labels import firewall_servers
from ..records import Nic, Server, Volume
from ..state import (
    State,
    client_by_name,
    nic_addresses,
    nics_in,
    primary_address_of,
)

# the PCIe root ports a NIC can be hot-plugged into (the lab hot-plugs 4 NICs per boot)
HOT_PLUG_PORTS = ("hp1", "hp2", "hp3", "hp4")

# the sets of table inet hlab that the faults fill (scripts/hlab.nft declares them)
FAULT_SETS = (
    "cuts",
    "isolated",
    "blocked_svc",
    "blocked_inet",
    "private_pairs",
    "src4",
    "src6",
)


# --- faults and latency ---------------------------------------------------------------


def target_ifaces(state: State, ref: str, which: str = "all") -> list[str]:
    """Return the interfaces of what a fault points at: a server or a client.

    `which` picks 'public', 'private' or 'all' (a client has a public link only).
    Raises KeyError when the server or client is gone.
    """
    kind, key = ref.split(":", 1)
    if kind == "s":
        server = state["servers"].get(int(key))
        if not server:
            raise KeyError(f"{ref} is gone")
        public = [server["_lab"]["tap"]]
        private = [nic["tap"] for nic in server["_lab"]["nics"]]
        if which == "public":
            return public
        if which == "private":
            return private
        return public + private
    client = client_by_name(state, key)
    if kind != "c" or not client:
        raise KeyError(f"{ref} is gone")
    return [client["veth"]] if which in ("public", "all") else []


def source_prefixes(state: State, server: Server, network: int | None) -> list[str]:
    """Return the source prefixes of a server's packets on one of its taps.

    On a public tap (network None): its public IPv4, its IPv6 /64, its Floating
    IPs. On a tap of a cloud network: its private IP there (networks may overlap).
    """
    if network is not None:
        return [
            prefix(nic["ip"])
            for nic in server["_lab"]["nics"]
            if nic["network"] == network
        ]
    ipv4 = primary_address_of(state, server, "ipv4")
    ipv6_net = primary_address_of(state, server, "ipv6")
    ips = [prefix(ipv4)] if ipv4 else []
    ips += [
        prefix(f["ip"])
        for f in state["floating_ips"].values()
        if f.get("server") == server["id"]
    ]
    if ipv6_net:
        ips.append(ipv6_net)
    return ips


def fault_sets(state: State) -> dict[str, list[str]]:
    """Return the elements (nft syntax) of the nftables sets of table inet hlab.

    These are the faults, the cloud network members, and the sources each public tap
    and client may send from (anti-spoofing).
    """
    sets: dict[str, list[str]] = {name: [] for name in FAULT_SETS}
    servers = list(state["servers"].values())
    # cloud network → private taps of its servers
    members: dict[int, list[str]] = {}
    for server in servers:
        for nic in server["_lab"]["nics"]:
            members.setdefault(nic["network"], []).append(nic["tap"])
    sets["private_pairs"] = [
        f'"{a}" . "{b}"'
        for taps in members.values()
        for a in taps
        for b in taps
        if a != b
    ]
    for server in servers:
        tap = server["_lab"]["tap"]
        # its own link-local only
        sets["src6"].append(f'"{tap}" . {link_local(server["_lab"]["index"])}/128')
        for ip in source_prefixes(state, server, None):
            if ":" in ip:
                sets["src6"].append(f'"{tap}" . {ip}')
            else:
                sets["src4"].append(f'"{tap}" . {ip.split("/")[0]}')
    for client in state["clients"].values():
        sets["src4"].append(f'"{client["veth"]}" . {client["ip"]}')
        sets["src6"].append(f'"{client["veth"]}" . {client["ip6"]}/128')
    for fault in state.data["faults"]:
        try:
            if fault["type"] == "cut":
                first = target_ifaces(state, fault["first_ref"], fault["net"])
                second = target_ifaces(state, fault["second_ref"], fault["net"])
                sets["cuts"] += [f'"{x}" . "{y}"' for x in first for y in second]
                sets["cuts"] += [f'"{y}" . "{x}"' for x in first for y in second]
            elif fault["type"] == "isolate":
                sets["isolated"] += [
                    f'"{x}"' for x in target_ifaces(state, fault["target_ref"])
                ]
            elif fault["type"] == "block":
                ifaces = target_ifaces(state, fault["target_ref"])
                if fault["what"] == "internet":
                    sets["blocked_inet"] += [f'"{x}"' for x in ifaces]
                else:
                    # api: HTTP; box: SFTP and SSH
                    ports = {"api": [80], "box": [22, 23]}[fault["what"]]
                    sets["blocked_svc"] += [
                        f'"{x}" . {port}' for x in ifaces for port in ports
                    ]
        except KeyError:
            continue  # the server is gone; the fault stays listed until cleared
    return sets


def tap_networks(state: State) -> list[tuple[str, int]]:
    """Return (tap, network ID) for every NIC in a cloud network."""
    return [
        (nic["tap"], nic["network"])
        for server in state["servers"].values()
        for nic in server["_lab"]["nics"]
    ]


def tap_delays(
    state: State,
    cfg: dict,
    server: Server,
    network: int | None,
    servers: list[Server],
) -> dict[int, list[str]]:
    """Return the delays towards a server on one of its taps.

    Round-trip time (ms) to another server's location → that server's source prefixes
    on the tap's network (`network` None: the public one).
    """
    by_rtt: dict[int, list[str]] = {}
    for other in servers:
        if other["id"] == server["id"] or other["location"] == server["location"]:
            continue
        pair = config.location_pair(other["location"], server["location"])
        rtt = cfg["latency_ms"].get(pair, 0)
        if rtt:
            by_rtt.setdefault(rtt, []).extend(source_prefixes(state, other, network))
    return by_rtt


def latency(state: State, cfg: dict) -> Latency:
    """Return how each server's taps and each client's link are shaped.

    A tap with a netem fault gets that fault; else a delay band per round-trip time to
    the other servers' locations (`latency_ms` of `cfg`).
    """
    netem = state.data["netem"]
    servers = list(state["servers"].values())
    shaped = []
    for server in servers:
        server_lab = server["_lab"]
        taps: list[tuple[str, int | None]] = [(server_lab["tap"], None)]
        taps += [(nic["tap"], nic["network"]) for nic in server_lab["nics"]]
        shapings = [
            Shaping(
                tap,
                netem.get(tap),
                {} if netem.get(tap) else tap_delays(state, cfg, server, net, servers),
            )
            for tap, net in taps
        ]
        shaped.append((server, shapings))
    clients = [
        Shaping(client["veth"], netem.get(client["veth"]))
        for client in state["clients"].values()
    ]
    return Latency(shaped, clients)


# --- taps, DHCP and routes ------------------------------------------------------------


def private_tap(state: State, nic: Nic) -> PrivateTap:
    """Return how a NIC's tap in its cloud network is set up."""
    return PrivateTap(
        nic["tap"],
        nic["mac"],
        nic["ip"],
        nic["network"],
        state["networks"][nic["network"]]["ip_range"],
        list(nic["alias_ips"]),
    )


def server_taps(state: State, server: Server) -> ServerTaps:
    """Return what a server's taps are set up with when its VM starts."""
    return ServerTaps(
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

    Each NIC gets a fixed address, a /32 with routes – on public interfaces to
    172.31.1.1, on cloud networks to the network's gateway and range.
    """
    hosts = []
    for server in state["servers"].values():
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
            network = state["networks"].get(nic["network"])
            if not network:  # its network was deleted meanwhile
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
                (server, nic["tap"])
                for server, nic in nics_in(state, network["id"])
                if route["gateway"] in nic_addresses(nic)
            ],
        )
        for network in state["networks"].values()
        for route in network["routes"]
    ]


# --- firewalls and volumes ------------------------------------------------------------


def firewall_rules(state: State) -> list[ServerRules]:
    """Return the rules of every server that a firewall applies to, by server ID.

    A server's rules are those of all its firewalls (copies).
    """
    out = []
    for sid, server in sorted(state["servers"].items()):
        firewalls = [
            f for f in state["firewalls"].values() if sid in firewall_servers(state, f)
        ]
        if firewalls:
            rules = copy.deepcopy([r for f in firewalls for r in f["rules"]])
            out.append(ServerRules(sid, server["_lab"]["tap"], rules))
    return out


def boot_volumes(state: State, server: Server) -> list[Volume]:
    """Return the volumes a server's VM gets at boot.

    Those are the volumes attached to it whose disk exists.
    """
    return [
        v
        for v in state["volumes"].values()
        if v.get("server") == server["id"] and v["status"] == "available"
    ]
