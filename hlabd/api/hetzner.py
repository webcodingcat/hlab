"""JSON shapes of the Hetzner Cloud API for the catalogue and the lab's resources.

See https://docs.hetzner.cloud/.
"""

import base64
import hashlib

from .. import config
from ..labels import firewall_servers, match_labels

CREATED = "2026-01-01T00:00:00+00:00"  # catalogue entries
TRAFFIC = 21990232555520  # 20 TB included traffic

DC_NAMES = {
    "fsn1": "fsn1-dc14",
    "nbg1": "nbg1-dc3",
    "hel1": "hel1-dc2",
}  # metadata: availability-zone
LOCATION_IDS = {"fsn1": 1, "nbg1": 2, "hel1": 3}
COORDS = {
    "fsn1": (50.47612, 12.370071),
    "nbg1": (49.452102, 11.076665),
    "hel1": (60.169855, 24.938379),
}


def error(code: str, message: str, details: dict | None = None) -> dict:
    """Return the API's error body."""
    return {"error": {"code": code, "message": message, "details": details or {}}}


def location_id(cfg: dict, name: str) -> int:
    """Return the ID of a location: Hetzner's for the known ones, else 10 and up."""
    return LOCATION_IDS.get(name) or 10 + list(cfg["locations"]).index(name)


def location_json(cfg: dict, name: str) -> dict:
    """Return the API object of a location."""
    location = cfg["locations"][name]
    latitude, longitude = COORDS.get(name, (50.0, 10.0))
    return {
        "id": location_id(cfg, name),
        "name": name,
        "description": f"{location.get('city', name)} DC Park 1",
        "country": location.get("country", "DE"),
        "city": location.get("city", name),
        "latitude": latitude,
        "longitude": longitude,
        "network_zone": location.get("network_zone", "eu-central"),
    }


def server_type_id(cfg: dict, name: str) -> int:
    """Return the ID of a server type (100 and up, in configuration order)."""
    return 100 + list(cfg["server_types"]).index(name)


def server_type_json(cfg: dict, name: str) -> dict:
    """Return the API object of a server type, with prices in every location."""
    entry = cfg["server_types"][name]
    prices = [
        {
            "location": loc,
            "price_hourly": {"net": "0.0080", "gross": "0.0095"},
            "price_monthly": {"net": "4.9900", "gross": "5.9381"},
            "included_traffic": TRAFFIC,
            "price_per_tb_traffic": {"net": "1.0000", "gross": "1.1900"},
        }
        for loc in cfg["locations"]
    ]
    return {
        "id": server_type_id(cfg, name),
        "name": name,
        "description": name.upper(),
        "category": "cost_optimized",
        "cores": entry["cores"],
        "memory": float(entry["memory_gb"]),
        "disk": entry["disk_gb"],
        "deprecated": False,
        "deprecation": None,
        "prices": prices,
        "storage_type": "local",
        "cpu_type": "shared",
        "architecture": "x86",
        "included_traffic": TRAFFIC,
        "locations": [
            {
                "id": location_id(cfg, loc),
                "name": loc,
                "deprecation": None,
                "recommended": True,
                "available": True,
            }
            for loc in cfg["locations"]
        ],
    }


def image_id(cfg: dict, name: str) -> int:
    """Return the ID of a system image (200 and up, in configuration order)."""
    return 200 + list(cfg["images"]).index(name)


def _flavor(name: str) -> tuple[str, str | None]:
    flavor, _, version = name.partition("-")
    known = ("debian", "ubuntu", "centos", "fedora", "rocky", "alma")
    return (flavor if flavor in known else "unknown"), version or None


def image_json(cfg: dict, name: str) -> dict:
    """Return the API object of a system image."""
    flavor, version = _flavor(name)
    return {
        "id": image_id(cfg, name),
        "type": "system",
        "status": "available",
        "name": name,
        "description": name.replace("-", " ").title(),
        "image_size": None,
        "disk_size": 5,
        "created": CREATED,
        "created_from": None,
        "bound_to": None,
        "os_flavor": flavor,
        "os_version": version,
        "rapid_deploy": True,
        "protection": {"delete": False},
        "deprecated": None,
        "deprecation": None,
        "deleted": None,
        "labels": {},
        "architecture": "x86",
    }


def server_json(cfg: dict, state, server: dict) -> dict:
    """Return the API object of a server."""
    ipv4 = state["primary_ips"].get(server.get("ipv4_id"))
    ipv6 = state["primary_ips"].get(server.get("ipv6_id"))
    firewalls = [
        {"id": fid, "status": "applied"}
        for fid, firewall in state["firewalls"].items()
        if server["id"] in firewall_servers(firewall, state)
    ]
    ipv4_ptr = next(
        (
            x["dns_ptr"]
            for x in (ipv4 or {}).get("dns_ptr", [])
            if x["ip"] == (ipv4 or {}).get("ip")
        ),
        None,
    )
    return {
        "id": server["id"],
        "name": server["name"],
        "status": server["status"],
        "created": server["created"],
        "labels": server["labels"],
        "public_net": {
            "ipv4": {
                "id": ipv4["id"],
                "ip": ipv4["ip"],
                "blocked": False,
                "dns_ptr": ipv4_ptr or default_ptr(ipv4["ip"]),
            }
            if ipv4
            else None,
            "ipv6": {
                "id": ipv6["id"],
                "ip": ipv6["ip"],
                "blocked": False,
                "dns_ptr": ipv6.get("dns_ptr", []),
            }
            if ipv6
            else None,
            "floating_ips": [
                f["id"]
                for f in state["floating_ips"].values()
                if f.get("server") == server["id"]
            ],
            "firewalls": firewalls,
        },
        "private_net": [
            {
                "network": nic["network"],
                "ip": nic["ip"],
                "alias_ips": nic.get("alias_ips", []),
                "mac_address": nic["mac"],
            }
            for nic in server["_lab"]["nics"]
        ],
        "server_type": server_type_json(cfg, server["server_type"]),
        "location": location_json(cfg, server["location"]),
        "image": image_json(cfg, server["image"])
        if server["image"] in cfg["images"]
        else None,
        "iso": None,
        "rescue_enabled": False,
        "locked": server.get("locked", False),
        "backup_window": None,
        "outgoing_traffic": 0,
        "ingoing_traffic": 0,
        "included_traffic": TRAFFIC,
        "protection": server["protection"],
        "volumes": sorted(
            v["id"]
            for v in state["volumes"].values()
            if v.get("server") == server["id"]
        ),
        "load_balancers": [],
        "primary_disk_size": server["_lab"].get("disk_gb")
        or cfg["server_types"][server["server_type"]]["disk_gb"],
        "placement_group": None,
    }


def default_ptr(ip: str) -> str:
    """Return Hetzner's reverse DNS for an IPv4 without its own entry.

    The octets are reversed (static.4.3.2.1.clients...).
    """
    return f"static.{'.'.join(reversed(ip.split('.')))}.clients.your-server.de"


def primary_ip_json(cfg: dict, primary_ip: dict) -> dict:
    """Return the API object of a primary IP."""
    ip = primary_ip["ip"]
    default = (
        [{"ip": ip, "dns_ptr": default_ptr(ip)}] if primary_ip["type"] == "ipv4" else []
    )
    return {
        "id": primary_ip["id"],
        "name": primary_ip["name"],
        "ip": ip,
        "type": primary_ip["type"],
        "assignee_id": primary_ip.get("assignee_id"),
        "assignee_type": "server" if primary_ip.get("assignee_id") else "unassigned",
        "auto_delete": primary_ip.get("auto_delete", True),
        "blocked": False,
        "location": location_json(cfg, primary_ip["location"]),
        "dns_ptr": primary_ip.get("dns_ptr") or default,
        "labels": primary_ip.get("labels", {}),
        "protection": primary_ip.get("protection") or {"delete": False},
        "created": primary_ip["created"],
    }


def network_json(state, network: dict) -> dict:
    """Return the API object of a network, with the servers attached to it."""
    servers = [
        s["id"]
        for s in state["servers"].values()
        if any(nic["network"] == network["id"] for nic in s["_lab"]["nics"])
    ]
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
        "expose_routes_to_vswitch": network.get("expose_routes_to_vswitch", False),
    }


def floating_ip_json(cfg: dict, floating_ip: dict) -> dict:
    """Return the API object of a Floating IP.

    Reverse DNS is per address (several in an IPv6 /64); an IPv4 without its own entry
    has Hetzner's default.
    """
    ip = floating_ip["ip"]
    ptrs = floating_ip.get("dns_ptr")
    ptrs = (
        ptrs
        if isinstance(ptrs, list)
        else ([{"ip": ip, "dns_ptr": ptrs}] if ptrs else [])
    )
    if floating_ip["type"] == "ipv4" and not ptrs:
        ptrs = [{"ip": ip, "dns_ptr": default_ptr(ip)}]
    return {
        "id": floating_ip["id"],
        "name": floating_ip["name"],
        "description": floating_ip.get("description"),
        "ip": ip,
        "type": floating_ip["type"],
        "server": floating_ip.get("server"),
        "dns_ptr": ptrs,
        "home_location": location_json(cfg, floating_ip["home_location"]),
        "blocked": False,
        "protection": floating_ip["protection"],
        "labels": floating_ip["labels"],
        "created": floating_ip["created"],
    }


def ssh_fingerprint(public_key: str) -> str:
    """Return the MD5 fingerprint of an OpenSSH public key.

    A key that is none raises ValueError: its blob does not name its type, or it has
    leading whitespace (Hetzner refuses that since 2026-04).
    """
    parts = public_key.split()
    if len(parts) < 2 or public_key != public_key.lstrip():
        raise ValueError("invalid public key")
    blob = base64.b64decode(parts[1], validate=True)
    length = int.from_bytes(blob[:4], "big")
    if len(blob) < 4 + length or blob[4 : 4 + length] != parts[0].encode():
        raise ValueError("invalid public key")
    digest = hashlib.md5(blob).hexdigest()
    return ":".join(digest[i : i + 2] for i in range(0, 32, 2))


def firewall_json(state, firewall: dict) -> dict:
    """Return the API object of a firewall, with the servers its selectors reach."""
    applied = []
    for entry in firewall["applied_to"]:
        if entry["type"] == "label_selector":
            selector = entry["label_selector"]["selector"]
            reached = [
                {"type": "server", "server": {"id": s["id"]}}
                for s in state["servers"].values()
                if match_labels(s["labels"], selector)
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


def ipv6_net(index: int) -> str:
    """Return the IPv6 /64 with the given index in the lab's prefix."""
    return f"{config.IPV6_PREFIX}:{index:x}::/64"
