"""The JSON shapes of servers and their images, keys, IPs, volumes and groups."""

from ...lab import Lab
from ...labels import firewall_servers
from ...records import (
    FloatingIP,
    Image,
    PlacementGroup,
    PrimaryIP,
    Server,
    SSHKey,
    Volume,
)
from ...state import (
    FIREWALLS,
    FLOATING_IPS,
    IMAGES,
    PLACEMENT_GROUPS,
    SERVERS,
    VOLUMES,
    primary_ip_of,
)
from .catalogue import image_json, image_os, location_json, server_type_json
from .common import TB, dns_ptrs


def own_image_json(lab: Lab, image: Image) -> dict:
    """Return the API object of an image of the project (a snapshot or a backup)."""
    flavor, version = image_os(image["os_image"])
    return {
        "id": image["id"],
        "type": image["type"],
        "status": image["status"],
        "name": None,  # Hetzner names only system images
        "description": image["description"],
        "image_size": image["image_size"],
        "disk_size": image["disk_size"],
        "created": image["created"],
        "created_from": {
            "id": image["created_from_id"],
            "name": image["created_from_name"],
        },
        "bound_to": image["bound_to"],
        "os_flavor": flavor,
        "os_version": version,
        "rapid_deploy": False,
        "protection": image["protection"],
        "deprecated": None,
        "deprecation": None,
        "deleted": None,
        "labels": image["labels"],
        "architecture": "x86",
    }


def server_image_json(lab: Lab, server: Server) -> dict | None:
    """Return the image a server was made from, or None if it is gone."""
    image = server["image"]
    if isinstance(image, int):
        own = lab.state.get(IMAGES, image)
        return own_image_json(lab, own) if own is not None else None
    return image_json(lab, image) if image in lab.cfg["images"] else None


def server_json(lab: Lab, server: Server) -> dict:
    """Return the API object of a server."""
    cfg, state = lab.cfg, lab.state
    ipv4 = primary_ip_of(state, server, "ipv4")
    ipv6 = primary_ip_of(state, server, "ipv6")
    firewalls = [
        {"id": fid, "status": "applied"}
        for fid, firewall in state[FIREWALLS].items()
        if server["id"] in firewall_servers(state, firewall)
    ]
    floating_ips = [
        f["id"] for f in state[FLOATING_IPS].values() if f["server"] == server["id"]
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
        v["id"] for v in state[VOLUMES].values() if v.get("server") == server["id"]
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
        "image": server_image_json(lab, server),
        "iso": None,
        "rescue_enabled": False,
        "locked": server.get("_locked", False),
        "backup_window": server["backup_window"],
        "outgoing_traffic": 0,
        "ingoing_traffic": 0,
        "included_traffic": cfg["server_types"][server["server_type"]]["traffic_tb"]
        * TB,
        "protection": server["protection"],
        "volumes": volumes,
        "load_balancers": [],
        "primary_disk_size": server["_lab"]["disk_gb"],
        "placement_group": placement_group_json(
            lab, lab.state[PLACEMENT_GROUPS][server["placement_group"]]
        )
        if server["placement_group"]
        else None,
    }


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


def placement_group_json(lab: Lab, group: PlacementGroup) -> dict:
    """Return the API object of a placement group, with the IDs of its servers."""
    return {
        "id": group["id"],
        "name": group["name"],
        "labels": group["labels"],
        "type": group["type"],
        "created": group["created"],
        "servers": [
            sid
            for sid, server in sorted(lab.state[SERVERS].items())
            if server["placement_group"] == group["id"]
        ],
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
        # server is deprecated in favour of assignee; Hetzner still sends both
        "server": volume["server"],
        "assignee": {"id": volume["server"], "type": "server"}
        if volume["server"]
        else None,
        "location": location_json(lab, volume["location"]),
        "size": volume["size"],
        "linux_device": f"/dev/disk/by-id/scsi-0HC_Volume_{volume['id']}",
        "protection": volume["protection"],
        "labels": volume["labels"],
        "status": volume["status"],
        "format": volume["format"],
    }
