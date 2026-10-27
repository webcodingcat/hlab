"""What the system should look like, computed from the state.

These are pure functions, one module per part of the system; everything is used as
`desired.<name>`. They take the state (or a record) and return plain data: the
dataclasses of the fabric's parts, IDs, lists and strings, never a record. They run
nothing and change nothing. api/system.py hands what they return to the fabric
(DEVELOPING.md, Where code runs).
"""

from .balancers import (
    ALGORITHMS,
    balancer_ipv6,
    balancer_network,
    balancer_specs,
    bind_lines,
    haproxy_config,
    health_check_lines,
    member_name,
    server_target_address,
    target_members,
)
from .dns import zone_files, zone_text
from .faults import (
    FAULT_SETS,
    fault_sets,
    latency,
    source_prefixes,
    tap_delays,
    tap_networks,
    target_ifaces,
)
from .firewall import firewall_rule, firewall_rules
from .network import (
    HOT_PLUG_PORTS,
    boot_volumes,
    client_link,
    dhcp_hosts,
    free_hot_plug_port,
    private_tap,
    public_tap,
    server_taps,
    wanted_routes,
)

__all__ = [
    "ALGORITHMS",
    "FAULT_SETS",
    "HOT_PLUG_PORTS",
    "balancer_ipv6",
    "balancer_network",
    "balancer_specs",
    "bind_lines",
    "boot_volumes",
    "client_link",
    "dhcp_hosts",
    "fault_sets",
    "firewall_rule",
    "firewall_rules",
    "free_hot_plug_port",
    "haproxy_config",
    "health_check_lines",
    "latency",
    "member_name",
    "private_tap",
    "public_tap",
    "server_taps",
    "server_target_address",
    "source_prefixes",
    "tap_delays",
    "tap_networks",
    "target_ifaces",
    "target_members",
    "wanted_routes",
    "zone_files",
    "zone_text",
]
