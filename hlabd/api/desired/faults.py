"""Fault sets, sources (anti-spoofing) and latency, as nftables and tc get them."""

from ... import config
from ...config import Config
from ...fabric.addresses import link_local, prefix
from ...fabric.balancers import namespace, private_veth
from ...fabric.faults import Latency, Shaping
from ...records import (
    Fault,
    Server,
    TargetRef,
    Which,
)
from ...state import (
    CLIENTS,
    FLOATING_IPS,
    LOAD_BALANCERS,
    SERVERS,
    State,
    client_by_name,
    primary_address_of,
)
from .balancers import balancer_ipv6

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


def target_ifaces(state: State, ref: TargetRef, which: Which = "all") -> list[str]:
    """Return the interfaces of what a fault points at: a server or a client.

    `which` picks 'public', 'private' or 'all'; a client has only a public link.
    Raise KeyError when the server or client is gone.
    """
    if "server" in ref:
        server = state[SERVERS].get(ref["server"])
        if not server:
            raise KeyError(f"{ref} is gone")
        public = [server["_lab"]["tap"]]
        private = [nic["tap"] for nic in server["_lab"]["nics"]]
        if which == "public":
            return public
        if which == "private":
            return private
        return public + private
    client = client_by_name(state, ref["client"])
    if not client:
        raise KeyError(f"{ref} is gone")
    return [client["veth"]] if which in ("public", "all") else []


def source_prefixes(state: State, server: Server, network: int | None) -> list[str]:
    """Return the source prefixes of a server's packets on one of its taps.

    On the public tap (network None), these are its public IPv4, its IPv6 /64 and its
    Floating IPs. On a tap in a cloud network, this is its private IP there; the
    network matters because networks may overlap.
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
        for f in state[FLOATING_IPS].values()
        if f.get("server") == server["id"]
    ]
    if ipv6_net:
        ips.append(ipv6_net)
    return ips


def fault_sets(state: State) -> dict[str, list[str]]:
    """Return the elements (nft syntax) of the nftables sets of table inet hlab.

    These are the faults, the cloud network members, and the sources each public tap,
    client and load balancer may send from (anti-spoofing).
    """
    sets: dict[str, list[str]] = {name: [] for name in FAULT_SETS}
    servers = list(state[SERVERS].values())
    # cloud network → private taps of its servers
    members: dict[int, list[str]] = {}
    for server in servers:
        for nic in server["_lab"]["nics"]:
            members.setdefault(nic["network"], []).append(nic["tap"])
    for balancer in state[LOAD_BALANCERS].values():
        for link in balancer["private_net"]:
            members.setdefault(link["network"], []).append(private_veth(balancer["id"]))
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
    for client in state[CLIENTS].values():
        sets["src4"].append(f'"{client["veth"]}" . {client["ip"]}')
        sets["src6"].append(f'"{client["veth"]}" . {client["ip6"]}/128')
    for balancer in state[LOAD_BALANCERS].values():
        veth = namespace(balancer["id"])
        sets["src4"].append(f'"{veth}" . {balancer["ipv4"]}')
        sets["src6"].append(f'"{veth}" . {balancer_ipv6(balancer)}/128')
    faults: list[Fault] = state.data["faults"]
    for fault in faults:
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
    """Return (tap or veth, network ID) for every NIC in a cloud network."""
    servers = [
        (nic["tap"], nic["network"])
        for server in state[SERVERS].values()
        for nic in server["_lab"]["nics"]
    ]
    balancers = [
        (private_veth(balancer["id"]), link["network"])
        for balancer in state[LOAD_BALANCERS].values()
        for link in balancer["private_net"]
    ]
    return servers + balancers


def tap_delays(
    state: State,
    cfg: Config,
    server: Server,
    network: int | None,
    servers: list[Server],
) -> dict[int, list[str]]:
    """Return the delays towards a server on one of its taps.

    The result maps the round-trip time (ms) to another server's location to that
    server's source prefixes in the tap's network. `network` None means the public
    network.
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


def latency(state: State, cfg: Config) -> Latency:
    """Return how each server's taps and each client's link are shaped.

    A tap with a netem fault gets that fault. Otherwise it gets a delay band for each
    round-trip time to the other servers' locations (`latency_ms` of `cfg`).
    """
    netem = state.data["netem"]
    servers = list(state[SERVERS].values())
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
        shaped.append((server["id"], shapings))
    clients = [
        Shaping(client["veth"], netem.get(client["veth"]))
        for client in state[CLIENTS].values()
    ]
    return Latency(shaped, clients)
