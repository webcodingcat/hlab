"""JSON shapes of the Hetzner Cloud API, for the catalogue and the resources.

See https://docs.hetzner.cloud/.
"""

from decimal import Decimal

from ..config import SERVER_CATEGORIES
from ..lab import Lab
from ..labels import firewall_servers, selector_servers
from ..records import (
    CloudNetwork,
    DnsPtr,
    Firewall,
    FloatingIP,
    Image,
    LoadBalancer,
    PlacementGroup,
    PrimaryIP,
    RRSet,
    Server,
    SSHKey,
    TlsCertificate,
    Volume,
    Zone,
)
from ..state import (
    FIREWALLS,
    FLOATING_IPS,
    IMAGES,
    LOAD_BALANCERS,
    PLACEMENT_GROUPS,
    RRSETS,
    SERVERS,
    VOLUMES,
    nics_in,
    primary_ip_of,
)
from . import desired

CREATED = "2026-01-01T00:00:00+00:00"  # catalogue entries
TB = 1024**4  # Hetzner counts traffic in binary terabytes
# Hetzner's hourly price is the monthly one spread over 624 hours (26 days): a
# server costs its monthly price at most
HOURS_PER_MONTH = Decimal(624)

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


def price(lab: Lab, net) -> dict:
    """Return a price as the API gives it: net and with VAT, as decimal strings.

    `net` is a number of the configuration (EUR without VAT).
    """
    amount = Decimal(str(net))
    vat = Decimal(str(lab.cfg["pricing"]["vat_rate"])) / 100
    return {"net": f"{amount:.10f}", "gross": f"{amount * (1 + vat):.10f}"}


def hourly(monthly) -> Decimal:
    """Return Hetzner's hourly price for a monthly price, to four decimals."""
    return (Decimal(str(monthly)) / HOURS_PER_MONTH).quantize(Decimal("0.0001"))


def server_type_prices(lab: Lab, name: str) -> list[dict]:
    """Return a server type's prices: the same in every location of the lab."""
    return type_prices(lab, lab.cfg["server_types"][name])


def server_type_json(lab: Lab, name: str) -> dict:
    """Return the API object of a server type, offered in every location."""
    cfg = lab.cfg
    entry = cfg["server_types"][name]
    return {
        "id": server_type_id(lab, name),
        "name": name,
        "description": name.upper(),
        "category": entry["category"],
        "cores": entry["cores"],
        "memory": float(entry["memory_gb"]),
        "disk": entry["disk_gb"],
        "deprecated": False,
        "deprecation": None,
        "prices": server_type_prices(lab, name),
        "storage_type": "local",
        "cpu_type": SERVER_CATEGORIES[entry["category"]],
        "architecture": "x86",
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


def load_balancer_type_id(lab: Lab, name: str) -> int:
    """Return the ID of a load balancer type (1 and up, in configuration order)."""
    return 1 + list(lab.cfg["load_balancer_types"]).index(name)


def type_prices(lab: Lab, entry: dict) -> list[dict]:
    """Return the prices of a server or load balancer type, alike in every location."""
    return [
        {
            "location": loc,
            "price_hourly": price(lab, hourly(entry["price_monthly"])),
            "price_monthly": price(lab, entry["price_monthly"]),
            "included_traffic": entry["traffic_tb"] * TB,
            "price_per_tb_traffic": price(lab, lab.cfg["pricing"]["traffic_per_tb"]),
        }
        for loc in lab.cfg["locations"]
    ]


def load_balancer_type_json(lab: Lab, name: str) -> dict:
    """Return the API object of a load balancer type."""
    entry = lab.cfg["load_balancer_types"][name]
    return {
        "id": load_balancer_type_id(lab, name),
        "name": name,
        "description": name.upper(),
        "max_connections": entry["max_connections"],
        "max_services": entry["max_services"],
        "max_targets": entry["max_targets"],
        "max_assigned_certificates": entry["max_certificates"],
        "deprecated": None,
        "deprecation": None,
        "prices": type_prices(lab, entry),
    }


def pricing_json(lab: Lab) -> dict:
    """Return the price list of /v1/pricing."""
    pricing = lab.cfg["pricing"]
    locations = lab.cfg["locations"]

    def ip_prices(kind: str, hourly_too: bool) -> list[dict]:
        monthly = pricing[f"{kind}_monthly"]
        per_location = {"price_monthly": price(lab, monthly)}
        if hourly_too:  # primary IPs are billed by the hour, Floating IPs by month
            per_location["price_hourly"] = price(lab, hourly(monthly))
        return [{"location": loc, **per_location} for loc in locations]

    return {
        "currency": "EUR",
        "vat_rate": f"{Decimal(str(pricing['vat_rate'])):.2f}",
        "primary_ips": [
            {"type": "ipv4", "prices": ip_prices("primary_ipv4", True)},
            {"type": "ipv6", "prices": ip_prices("primary_ipv6", True)},
        ],
        "floating_ips": [
            {"type": "ipv4", "prices": ip_prices("floating_ipv4", False)},
            {"type": "ipv6", "prices": ip_prices("floating_ipv6", False)},
        ],
        # deprecated by Hetzner in favour of floating_ips, still required
        "floating_ip": {"price_monthly": price(lab, pricing["floating_ipv4_monthly"])},
        "image": {"price_per_gb_month": price(lab, pricing["image_per_gb_monthly"])},
        "volume": {"price_per_gb_month": price(lab, pricing["volume_per_gb_monthly"])},
        "server_backup": {
            "percentage": f"{Decimal(str(pricing['backup_percent'])):.2f}"
        },
        "server_types": [
            {
                "id": server_type_id(lab, name),
                "name": name,
                "prices": server_type_prices(lab, name),
            }
            for name in lab.cfg["server_types"]
        ],
        "load_balancer_types": [
            {
                "id": load_balancer_type_id(lab, name),
                "name": name,
                "prices": type_prices(lab, lab.cfg["load_balancer_types"][name]),
            }
            for name in lab.cfg["load_balancer_types"]
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


def own_image_json(lab: Lab, image: Image) -> dict:
    """Return the API object of an image of the project (a snapshot or a backup)."""
    flavor, version = _flavor(image["os_image"])
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


def default_ptr(ip: str) -> str:
    """Return Hetzner's reverse DNS for an IPv4 without its own entry.

    The octets are reversed (static.4.3.2.1.clients...).
    """
    return f"static.{'.'.join(reversed(ip.split('.')))}.clients.your-server.de"


def dns_ptrs(record: PrimaryIP | FloatingIP) -> list[DnsPtr]:
    """Return the reverse DNS entries of a primary or Floating IP.

    There is one entry per address, so an IPv6 /64 may have several. An IPv4 without
    its own entry gets Hetzner's default.
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


# Hetzner's nameservers, assigned to every zone; the lab answers for them itself
NAMESERVERS = (
    "hydrogen.ns.hetzner.com.",
    "oxygen.ns.hetzner.com.",
    "helium.ns.hetzner.de.",
)


def zone_json(lab: Lab, zone: Zone) -> dict:
    """Return the API object of a DNS zone (always in primary mode in the lab)."""
    rrsets = [r for r in lab.state[RRSETS].values() if r["zone"] == zone["id"]]
    return {
        "id": zone["id"],
        "name": zone["name"],
        "created": zone["created"],
        "mode": "primary",
        "labels": zone["labels"],
        "protection": zone["protection"],
        "ttl": zone["ttl"],
        "status": "ok",
        "record_count": sum(len(r["records"]) for r in rrsets),
        "authoritative_nameservers": {
            "assigned": list(NAMESERVERS),
            # the lab has no parent zones that could delegate
            "delegated": [],
            "delegation_last_check": None,
            "delegation_status": "unknown",
        },
        "registrar": "other",
    }


def rrset_json(rrset: RRSet) -> dict:
    """Return the API object of an RRSet; its ID is "<name>/<type>"."""
    return {
        "id": f"{rrset['name']}/{rrset['type']}",
        "name": rrset["name"],
        "type": rrset["type"],
        "ttl": rrset["ttl"],
        "labels": rrset["labels"],
        "protection": rrset["protection"],
        "records": rrset["records"],
        "zone": rrset["zone"],
    }


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
    health = lab.balancer_health.get(balancer["id"], {})
    ports = [s["listen_port"] for s in balancer["services"]]
    members = desired.target_members(lab.state, target)
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
            "enabled": True,
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
        "private_net": [],
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
