"""The JSON shapes of cloud networks and firewalls."""

from ...lab import Lab
from ...labels import selector_servers
from ...records import (
    CloudNetwork,
    Firewall,
)
from ...state import (
    nics_in,
)


def network_json(lab: Lab, network: CloudNetwork) -> dict:
    """Return the API object of a network, with the servers attached to it."""
    servers = [server["id"] for server, _ in nics_in(lab.state, network["id"])]
    return {
        "id": network["id"],
        "name": network["name"],
        "ip_range": network["ip_range"],
        "subnets": network["subnets"],
        "routes": network["routes"],
        "servers": servers,
        "load_balancers": [],
        "protection": network["protection"],
        "labels": network["labels"],
        "created": network["created"],
        "expose_routes_to_vswitch": network["expose_routes_to_vswitch"],
    }


def firewall_json(lab: Lab, firewall: Firewall) -> dict:
    """Return the API object of a firewall, with the servers its selectors reach."""
    state = lab.state
    applied = []
    for entry in firewall["applied_to"]:
        if entry["type"] == "label_selector":
            selector = entry["label_selector"]["selector"]
            reached = [
                {"type": "server", "server": {"id": s["id"]}}
                for s in selector_servers(state, selector)
            ]
            applied.append(
                {
                    "type": "label_selector",
                    "label_selector": {"selector": selector},
                    "applied_to_resources": reached,
                }
            )
        else:
            applied.append(entry)
    return {
        "id": firewall["id"],
        "name": firewall["name"],
        "labels": firewall["labels"],
        "created": firewall["created"],
        "rules": firewall["rules"],
        "applied_to": applied,
    }
