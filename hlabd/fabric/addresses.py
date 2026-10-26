"""The lab's addressing rules: MACs, link-local addresses, gateways, routing tables.

Plain functions, used by the fabric's parts and by the API (metadata, networks).
"""

import ipaddress


def public_mac(index: int) -> str:
    """Return the MAC of a server's public NIC."""
    return f"96:00:00:00:00:{index:02x}"


def link_local(index: int) -> str:
    """Return the IPv6 link-local address a guest derives from its public MAC.

    The address comes from EUI-64; it is the only one the guest's tap lets in.
    """
    return f"fe80::9400:ff:fe00:{index:x}"


def private_mac(index: int, number: int) -> str:
    """Return the MAC of a server's NIC in a cloud network."""
    return f"86:00:00:00:{index:02x}:{number:02x}"


def network_gateway(ip_range: str) -> str:
    """Return the gateway of a cloud network: the first host of its range."""
    return str(next(ipaddress.ip_network(ip_range).hosts()))


def route_table(network_id: int) -> str:
    """Return the routing table of a cloud network: its ID.

    Network IDs start above two million (state.py), clear of the kernel's tables.
    """
    return str(network_id)


def prefix(ip: str) -> str:
    """Return an address as a prefix for a route.

    An IPv4 address gets /32, an IPv6 address /128; networks stay as they are.
    """
    return ip if "/" in ip else f"{ip}/32" if ":" not in ip else f"{ip}/128"
