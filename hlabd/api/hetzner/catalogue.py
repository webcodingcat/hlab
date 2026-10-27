"""The JSON shapes of the catalogue: locations, types, system images, prices."""

from decimal import Decimal

from ...config import SERVER_CATEGORIES, LoadBalancerTypeEntry, ServerTypeEntry
from ...lab import Lab
from .common import CREATED, TB, hourly, price

LOCATION_IDS = {"fsn1": 1, "nbg1": 2, "hel1": 3}


COORDS = {
    "fsn1": (50.47612, 12.370071),
    "nbg1": (49.452102, 11.076665),
    "hel1": (60.169855, 24.938379),
}


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


def type_prices(lab: Lab, entry: ServerTypeEntry | LoadBalancerTypeEntry) -> list[dict]:
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

    def ip_prices(monthly: float, hourly_too: bool) -> list[dict]:
        per_location = {"price_monthly": price(lab, monthly)}
        if hourly_too:  # primary IPs are billed by the hour, Floating IPs by month
            per_location["price_hourly"] = price(lab, hourly(monthly))
        return [{"location": loc, **per_location} for loc in locations]

    return {
        "currency": "EUR",
        "vat_rate": f"{Decimal(str(pricing['vat_rate'])):.2f}",
        "primary_ips": [
            {
                "type": "ipv4",
                "prices": ip_prices(pricing["primary_ipv4_monthly"], True),
            },
            {
                "type": "ipv6",
                "prices": ip_prices(pricing["primary_ipv6_monthly"], True),
            },
        ],
        "floating_ips": [
            {
                "type": "ipv4",
                "prices": ip_prices(pricing["floating_ipv4_monthly"], False),
            },
            {
                "type": "ipv6",
                "prices": ip_prices(pricing["floating_ipv6_monthly"], False),
            },
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


def image_os(name: str) -> tuple[str, str | None]:
    """Return the OS flavor and version of an image name like "debian-13".

    A flavor Hetzner does not know is "unknown".
    """
    flavor, _, version = name.partition("-")
    known = ("debian", "ubuntu", "centos", "fedora", "rocky", "alma")
    return (flavor if flavor in known else "unknown"), version or None


def image_json(lab: Lab, name: str) -> dict:
    """Return the API object of a system image."""
    flavor, version = image_os(name)
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
