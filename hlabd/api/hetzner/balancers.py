"""The JSON shapes of certificates and load balancers."""

from ...lab import Lab
from ...records import (
    LoadBalancer,
    TlsCertificate,
)
from ...state import (
    LOAD_BALANCERS,
)
from .. import desired
from .catalogue import load_balancer_type_json, location_json
from .common import TB, default_ptr


def certificate_json(lab: Lab, certificate: TlsCertificate) -> dict:
    """Return the API object of a certificate (never its private key)."""
    used_by = [
        {"id": lid, "type": "load_balancer"}
        for lid, balancer in lab.state[LOAD_BALANCERS].items()
        if any(
            certificate["id"] in (s.get("http") or {}).get("certificates", [])
            for s in balancer["services"]
        )
    ]
    return {
        "id": certificate["id"],
        "name": certificate["name"],
        "labels": certificate["labels"],
        "type": certificate["type"],
        "certificate": certificate["certificate"],
        "created": certificate["created"],
        "not_valid_before": certificate["not_valid_before"],
        "not_valid_after": certificate["not_valid_after"],
        "domain_names": certificate["domain_names"],
        "fingerprint": certificate["fingerprint"],
        "status": certificate["status"],
        "used_by": used_by,
    }


# HAProxy's check status → Hetzner's health status of a target
HEALTH = {"UP": "healthy", "DOWN": "unhealthy"}


def health_status(
    health: dict[tuple[str, str], str], ports: list[int], name: str
) -> list[dict]:
    """Return a member's health per service, from HAProxy's checks of the backends."""
    return [
        {
            "listen_port": port,
            "status": HEALTH.get(
                health.get((f"b{port}", name), "").split(" ")[0], "unknown"
            ),
        }
        for port in ports
    ]


def target_json(lab: Lab, balancer: LoadBalancer, target: dict) -> dict:
    """Return a load balancer target with its health per service.

    A label selector target lists the servers it matches, each with its health.
    """
    health = lab.readings.health.get(balancer["id"], {})
    ports = [s["listen_port"] for s in balancer["services"]]
    members = desired.target_members(
        lab.state, target, desired.balancer_network(balancer)
    )
    if target["type"] == "label_selector":
        return {
            **target,
            "targets": [
                {
                    "type": "server",
                    "server": {"id": int(name[1:]), "ip": ip},
                    "health_status": health_status(health, ports, name),
                    "use_private_ip": target["use_private_ip"],
                }
                for name, ip in members
            ],
        }
    if target["type"] == "server":
        ip = members[0][1] if members else ""
        target = {**target, "server": {**target["server"], "ip": ip}}
    name = desired.member_name(target)
    return {**target, "health_status": health_status(health, ports, name)}


def load_balancer_json(lab: Lab, balancer: LoadBalancer) -> dict:
    """Return the API object of a load balancer."""
    ipv4, ipv6 = balancer["ipv4"], desired.balancer_ipv6(balancer)
    entry = lab.cfg["load_balancer_types"][balancer["type"]]
    return {
        "id": balancer["id"],
        "name": balancer["name"],
        "public_net": {
            "enabled": balancer["public_interface"],
            "ipv4": {
                "id": None,
                "ip": ipv4,
                "blocked": False,
                "dns_ptr": balancer["dns_ptr"].get(ipv4) or default_ptr(ipv4),
            },
            "ipv6": {
                "id": None,
                "ip": ipv6,
                "blocked": False,
                "dns_ptr": balancer["dns_ptr"].get(ipv6),
            },
        },
        "private_net": balancer["private_net"],
        "location": location_json(lab, balancer["location"]),
        "load_balancer_type": load_balancer_type_json(lab, balancer["type"]),
        "protection": balancer["protection"],
        "labels": balancer["labels"],
        "created": balancer["created"],
        "services": balancer["services"],
        "targets": [target_json(lab, balancer, t) for t in balancer["targets"]],
        "algorithm": {"type": balancer["algorithm"]},
        "outgoing_traffic": 0,
        "ingoing_traffic": 0,
        "included_traffic": entry["traffic_tb"] * TB,
    }
