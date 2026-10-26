"""JSON shapes of the Hetzner Cloud API for the catalogue and the lab's resources.

See https://docs.hetzner.cloud/.
"""

from ..lab import Lab
from ..labels import firewall_servers, selector_servers
from ..records import (
    CloudNetwork,
    DnsPtr,
    Firewall,
    FloatingIP,
    PrimaryIP,
    Server,
    SSHKey,
    Volume,
)
from ..state import nics_in, primary_ip_of

CREATED = "2026-01-01T00:00:00+00:00"  # catalogue entries
TRAFFIC = 21990232555520  # 20 TB included traffic

LOCATION_IDS = {"fsn1": 1, "nbg1": 2, "hel1": 3}
COORDS = {
    "fsn1": (50.47612, 12.370071),
    "nbg1": (49.452102, 11.076665),
    "hel1": (60.169855, 24.938379),
}


def error(code: str, message: str, details: dict | None = None) -> dict:
    """Return the API's error body."""
    return {"error": {"code": code, "message": message, "details": details or {}}}


def location_id(lab: Lab, name: str) -> int:
    """Return the ID of a location: Hetzner's for the known ones, else 10 and up."""
    return LOCATION_IDS.get(name) or 10 + list(lab.cfg["locations"]).index(name)


def location_json(lab: Lab, name: str) -> dict:
    """Return the API object of a location."""
    cfg = lab.cfg
    location = cfg["locations"][name]
    latitude, longitude = COORDS.get(name, (50.0, 10.0))
    return {
        "id": location_id(lab, name),
        "name": name,
        "description": f"{location.get('city', name)} DC Park 1",
        "country": location.get("country", "DE"),
        "city": location.get("city", name),
        "latitude": latitude,
        "longitude": longitude,
        "network_zone": location.get("network_zone", "eu-central"),
    }


def server_type_id(lab: Lab, name: str) -> int:
    """Return the ID of a server type (100 and up, in configuration order)."""
    return 100 + list(lab.cfg["server_types"]).index(name)


def server_type_json(lab: Lab, name: str) -> dict:
    """Return the API object of a server type, with prices in every location."""
    cfg = lab.cfg
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
        "id": server_type_id(lab, name),
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
                "id": location_id(lab, loc),
                "name": loc,
                "deprecation": None,
                "recommended": True,
                "available": True,
            }
            for loc in cfg["locations"]
        ],
    }


def image_id(lab: Lab, name: str) -> int:
    """Return the ID of a system image (200 and up, in configuration order)."""
    return 200 + list(lab.cfg["images"]).index(name)


def storage_box_type_id(lab: Lab, name: str) -> int:
    """Return the ID of a Storage Box type (1330 and up, in configuration order)."""
    return 1330 + list(lab.cfg["storage_box_types"]).index(name)


def _flavor(name: str) -> tuple[str, str | None]:
    """Return the OS flavor and version of an image name like "debian-13".

    A flavor Hetzner does not know is "unknown".
    """
    flavor, _, version = name.partition("-")
    known = ("debian", "ubuntu", "centos", "fedora", "rocky", "alma")
    return (flavor if flavor in known else "unknown"), version or None


def image_json(lab: Lab, name: str) -> dict:
    """Return the API object of a system image."""
    flavor, version = _flavor(name)
    return {
        "id": image_id(lab, name),
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


def server_json(lab: Lab, server: Server) -> dict:
    """Return the API object of a server."""
    cfg, state = lab.cfg, lab.state
    ipv4 = primary_ip_of(state, server, "ipv4")
    ipv6 = primary_ip_of(state, server, "ipv6")
    firewalls = [
        {"id": fid, "status": "applied"}
        for fid, firewall in state["firewalls"].items()
        if server["id"] in firewall_servers(state, firewall)
    ]
    floating_ips = [
        f["id"] for f in state["floating_ips"].values() if f["server"] == server["id"]
    ]
    private_net = [
        {
            "network": nic["network"],
            "ip": nic["ip"],
            "alias_ips": nic["alias_ips"],
            "mac_address": nic["mac"],
        }
        for nic in server["_lab"]["nics"]
    ]
    volumes = sorted(
        v["id"] for v in state["volumes"].values() if v.get("server") == server["id"]
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
                # a server shows the entry of its own address, as a string
                "dns_ptr": dns_ptrs(ipv4)[0]["dns_ptr"],
            }
            if ipv4
            else None,
            "ipv6": {
                "id": ipv6["id"],
                "ip": ipv6["ip"],
                "blocked": False,
                "dns_ptr": dns_ptrs(ipv6),
            }
            if ipv6
            else None,
            "floating_ips": floating_ips,
            "firewalls": firewalls,
        },
        "private_net": private_net,
        "server_type": server_type_json(lab, server["server_type"]),
        "location": location_json(lab, server["location"]),
        "image": image_json(lab, server["image"])
        if server["image"] in cfg["images"]
        else None,
        "iso": None,
        "rescue_enabled": False,
        "locked": server.get("_locked", False),
        "backup_window": None,
        "outgoing_traffic": 0,
        "ingoing_traffic": 0,
        "included_traffic": TRAFFIC,
        "protection": server["protection"],
        "volumes": volumes,
        "load_balancers": [],
        "primary_disk_size": server["_lab"]["disk_gb"],
        "placement_group": None,
    }


def default_ptr(ip: str) -> str:
    """Return Hetzner's reverse DNS for an IPv4 without its own entry.

    The octets are reversed (static.4.3.2.1.clients...).
    """
    return f"static.{'.'.join(reversed(ip.split('.')))}.clients.your-server.de"


def dns_ptrs(record: PrimaryIP | FloatingIP) -> list[DnsPtr]:
    """Return the reverse DNS entries of a primary or Floating IP.

    Entries are per address (several in an IPv6 /64); an IPv4 without its own entry has
    Hetzner's default.
    """
    if record["type"] == "ipv4" and not record["dns_ptr"]:
        return [{"ip": record["ip"], "dns_ptr": default_ptr(record["ip"])}]
    return record["dns_ptr"]


def ssh_key_json(lab: Lab, ssh_key: SSHKey) -> dict:
    """Return the API object of an SSH key."""
    return {
        "id": ssh_key["id"],
        "name": ssh_key["name"],
        "fingerprint": ssh_key["fingerprint"],
        "public_key": ssh_key["public_key"],
        "labels": ssh_key["labels"],
        "created": ssh_key["created"],
    }


def primary_ip_json(lab: Lab, primary_ip: PrimaryIP) -> dict:
    """Return the API object of a primary IP."""
    return {
        "id": primary_ip["id"],
        "name": primary_ip["name"],
        "ip": primary_ip["ip"],
        "type": primary_ip["type"],
        "assignee_id": primary_ip["assignee_id"],
        "assignee_type": "server" if primary_ip["assignee_id"] else "unassigned",
        "auto_delete": primary_ip["auto_delete"],
        "blocked": False,
        "location": location_json(lab, primary_ip["location"]),
        "dns_ptr": dns_ptrs(primary_ip),
        "labels": primary_ip["labels"],
        "protection": primary_ip["protection"],
        "created": primary_ip["created"],
    }


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


def floating_ip_json(lab: Lab, floating_ip: FloatingIP) -> dict:
    """Return the API object of a Floating IP."""
    return {
        "id": floating_ip["id"],
        "name": floating_ip["name"],
        "description": floating_ip["description"],
        "ip": floating_ip["ip"],
        "type": floating_ip["type"],
        "server": floating_ip["server"],
        "dns_ptr": dns_ptrs(floating_ip),
        "home_location": location_json(lab, floating_ip["home_location"]),
        "blocked": False,
        "protection": floating_ip["protection"],
        "labels": floating_ip["labels"],
        "created": floating_ip["created"],
    }


def volume_json(lab: Lab, volume: Volume) -> dict:
    """Return the API object of a volume."""
    return {
        "id": volume["id"],
        "name": volume["name"],
        "created": volume["created"],
        "server": volume["server"],
        "location": location_json(lab, volume["location"]),
        "size": volume["size"],
        "linux_device": f"/dev/disk/by-id/scsi-0HC_Volume_{volume['id']}",
        "protection": volume["protection"],
        "labels": volume["labels"],
        "status": volume["status"],
        "format": volume["format"],
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
