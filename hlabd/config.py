"""Lab configuration (hlab.yaml) with defaults."""

import copy
from pathlib import Path

import yaml

# Hetzner's server categories and the kind of vCPU each has
SERVER_CATEGORIES = {
    "cost_optimized": "shared",
    "regular_purpose": "shared",
    "general_purpose": "dedicated",
}

# the keys every entry of a catalogue in hlab.yaml needs
ENTRY_KEYS = {
    "server_types": (
        "category",
        "cores",
        "memory_gb",
        "disk_gb",
        "traffic_tb",
        "price_monthly",
    ),
    "storage_box_types": (
        "size_tb",
        "snapshots",
        "automatic_snapshots",
        "subaccounts",
        "price_monthly",
    ),
    "load_balancer_types": (
        "max_connections",
        "max_services",
        "max_targets",
        "max_certificates",
        "traffic_tb",
        "price_monthly",
    ),
}


def _server_type(
    category: str,
    cores: int,
    memory_gb: int,
    disk_gb: int,
    traffic_tb: int,
    price_monthly: float,
) -> dict:
    """Return a server type entry of the defaults, in the order of its columns."""
    return {
        "category": category,
        "cores": cores,
        "memory_gb": memory_gb,
        "disk_gb": disk_gb,
        "traffic_tb": traffic_tb,
        "price_monthly": price_monthly,
    }


def _load_balancer_type(
    max_connections: int,
    max_services: int,
    max_targets: int,
    max_certificates: int,
    price_monthly: float,
) -> dict:
    """Return a load balancer type entry of the defaults (20 TB traffic in the EU)."""
    return {
        "max_connections": max_connections,
        "max_services": max_services,
        "max_targets": max_targets,
        "max_certificates": max_certificates,
        "traffic_tb": 20,
        "price_monthly": price_monthly,
    }


def _box_type(size_tb: int, snapshots: int, price_monthly: float) -> dict:
    """Return a Storage Box type entry of the defaults.

    On Hetzner every type allows as many automatic snapshots as manual ones, and 100
    sub-accounts.
    """
    return {
        "size_tb": size_tb,
        "snapshots": snapshots,
        "automatic_snapshots": snapshots,
        "subaccounts": 100,
        "price_monthly": price_monthly,
    }


DEFAULTS: dict = {
    "name": "demo",
    "api": {"token": None, "time_scale": 1.0, "rate_limit": 3600},
    "memory_scale": 1.0,
    # Hetzner's server types in eu-central (category, cores, memory GB, disk GB,
    # included traffic TB, EUR per month without VAT and IPv4)
    "server_types": {
        "cx23": _server_type("cost_optimized", 2, 4, 40, 20, 5.49),
        "cx33": _server_type("cost_optimized", 4, 8, 80, 20, 8.49),
        "cx43": _server_type("cost_optimized", 8, 16, 160, 20, 15.99),
        "cx53": _server_type("cost_optimized", 16, 32, 320, 20, 29.49),
        "cpx02": _server_type("regular_purpose", 1, 1, 20, 20, 5.99),
        "cpx12": _server_type("regular_purpose", 1, 2, 40, 20, 11.49),
        "cpx22": _server_type("regular_purpose", 2, 4, 80, 20, 19.49),
        "cpx32": _server_type("regular_purpose", 4, 8, 160, 20, 35.49),
        "cpx42": _server_type("regular_purpose", 8, 16, 320, 20, 69.49),
        "cpx52": _server_type("regular_purpose", 12, 24, 480, 20, 100.49),
        "cpx62": _server_type("regular_purpose", 16, 32, 640, 20, 129.99),
        "ccx13": _server_type("general_purpose", 2, 8, 80, 20, 42.99),
        "ccx23": _server_type("general_purpose", 4, 16, 160, 20, 85.99),
        "ccx33": _server_type("general_purpose", 8, 32, 240, 30, 138.49),
        "ccx43": _server_type("general_purpose", 16, 64, 360, 40, 275.99),
        "ccx53": _server_type("general_purpose", 32, 128, 600, 50, 533.49),
        "ccx63": _server_type("general_purpose", 48, 192, 960, 60, 853.49),
    },
    "locations": {
        "fsn1": {"city": "Falkenstein", "country": "DE", "network_zone": "eu-central"},
        "nbg1": {"city": "Nuremberg", "country": "DE", "network_zone": "eu-central"},
        "hel1": {"city": "Helsinki", "country": "FI", "network_zone": "eu-central"},
    },
    "latency_ms": {"fsn1-nbg1": 3, "fsn1-hel1": 24, "hel1-nbg1": 24},
    # name → {url, checksums}: the current release and its checksum file, loaded at the
    # lab's start
    "images": {
        "debian-13": {
            "url": (
                "https://cloud.debian.org/images/cloud/trixie/latest/"
                "debian-13-generic-amd64.qcow2"
            ),
            "checksums": (
                "https://cloud.debian.org/images/cloud/trixie/latest/SHA512SUMS"
            ),
        },
        "ubuntu-26.04": {
            "url": (
                "https://cloud-images.ubuntu.com/releases/26.04/release/"
                "ubuntu-26.04-server-cloudimg-amd64.img"
            ),
            "checksums": (
                "https://cloud-images.ubuntu.com/releases/26.04/release/SHA256SUMS"
            ),
        },
    },
    # Hetzner's load balancer types (max connections, services, targets and assigned
    # certificates, EUR per month without VAT)
    "load_balancer_types": {
        "lb11": _load_balancer_type(10_000, 5, 25, 10, 7.49),
        "lb21": _load_balancer_type(20_000, 15, 75, 25, 21.49),
        "lb31": _load_balancer_type(40_000, 30, 150, 50, 42.99),
    },
    "storage_box": {"enabled": True, "size_gb": 10},
    # Storage Box types of the Storage Box API (size TB, snapshots, EUR per month
    # without VAT); the API reports their size, the lab's box really holds
    # storage_box.size_gb
    "storage_box_types": {
        "bx11": _box_type(1, 10, 3.20),
        "bx21": _box_type(5, 20, 10.90),
        "bx31": _box_type(10, 30, 20.80),
        "bx41": _box_type(20, 40, 40.60),
    },
    # what /v1/pricing reports besides the types; EUR without VAT
    "pricing": {
        "vat_rate": 19,
        "traffic_per_tb": 1.00,
        "primary_ipv4_monthly": 0.50,
        "primary_ipv6_monthly": 0.00,
        "floating_ipv4_monthly": 3.00,
        "floating_ipv6_monthly": 1.00,
        "volume_per_gb_monthly": 0.044,
        "image_per_gb_monthly": 0.0119,
        "backup_percent": 20,
    },
}

# the lab's volume: state, VM disks, images, keys
ROOT = Path("/hlab")

# Fixed addresses of every lab (each lab has its own network namespace).
GATEWAY = "172.31.1.1"  # Hetzner's public gateway, on-link on every server
# the lab's services use the benchmarking range 198.18.0.0/15
SERVICES_NET = "198.18.0.0/15"
SERVICES = "198.18.0.1"  # Cloud API (80), Storage Box (22, 23), DNS, ACME (14000)
HETZNER_API = "198.18.0.3"  # fake api.hetzner.com (Storage Boxes), port 80
# the ACME server (Pebble) on the services address, under this name of the lab resolver
ACME_NAME = "acme.hlab"
ACME_PORT = 14000
METADATA = "169.254.169.254"
# where fe80::a9fe:a9fe is DNATed to: the metadata service only
METADATA6 = "2001:db8:fffe::a9fe"
# primary IPv4: the lowest free of .11 to .250 (api/primary_ips.py, free_ipv4)
PUBLIC_NET = "198.51.100"
FLOATING_NET = "203.0.113"  # Floating IPv4: .10 + n
CLIENT_NET = "192.0.2"  # "Internet" clients: .10 + n, gateway .1
# source of everything the lab container itself (and its consumers) sends to VMs
OPERATOR = "192.0.2.2"
# source of `hlab ssh`: the lab's admin access; Cloud firewalls do not filter it
ADMIN = "198.18.0.2"
# IPv6, all from the documentation prefix 2001:db8::/32. Every server routes a /64
# (guest: <prefix>::1, gateway fe80::1).
# primary IPv6: the lowest free 2001:db8:<n>::/64 (api/primary_ips.py, free_ipv6);
# Floating IPv6: 2001:db8:ff<n>::/64
IPV6_PREFIX = "2001:db8"
GATEWAY6 = "fe80::1"
SERVICES6 = "2001:db8:fffe::1"  # fake Cloud API, Storage Box, DNS over IPv6
HETZNER_API6 = "2001:db8:fffe::4"
OPERATOR6 = "2001:db8:fffe::2"
ADMIN6 = "2001:db8:fffe::3"
CLIENT6 = "2001:db8:ffff"  # clients: 2001:db8:ffff::<10 + n>
SERVICES_NET6 = "2001:db8:fffe"  # the /48 of the service IPv6 addresses above

# what `hlab env` hands out when any token is accepted (hcloud wants 64 characters)
ANY_TOKEN = "hlab" * 16


def script_env() -> dict[str, str]:
    """Return the lab's addresses for the scripts (scripts/hlab.sh, scripts/box.sh)."""
    return {
        "HLAB_GATEWAY": GATEWAY,
        "HLAB_GATEWAY6": GATEWAY6,
        "HLAB_SERVICES": SERVICES,
        "HLAB_SERVICES6": SERVICES6,
        "HLAB_SERVICES_NET": SERVICES_NET,
        "HLAB_SERVICES_NET6": SERVICES_NET6,
        "HLAB_ADMIN": ADMIN,
        "HLAB_ADMIN6": ADMIN6,
        "HLAB_HETZNER_API": HETZNER_API,
        "HLAB_HETZNER_API6": HETZNER_API6,
        "HLAB_OPERATOR": OPERATOR,
        "HLAB_OPERATOR6": OPERATOR6,
        "HLAB_METADATA": METADATA,
        "HLAB_METADATA6": METADATA6,
        "HLAB_PUBLIC_NET": PUBLIC_NET,
        "HLAB_FLOATING_NET": FLOATING_NET,
        "HLAB_CLIENT_NET": CLIENT_NET,
        "HLAB_IPV6_PREFIX": IPV6_PREFIX,
        "HLAB_CLIENT6": CLIENT6,
    }


def _merge(base: dict, over: dict) -> dict:
    """Return `base` updated with `over`; only the settings groups merge by key."""
    out = copy.deepcopy(base)
    for k, v in (over or {}).items():
        # Catalogue-like maps (types, locations, images) are replaced, not merged,
        # so a lab can drop entries.
        if (
            isinstance(v, dict)
            and isinstance(out.get(k), dict)
            and k in ("api", "storage_box", "pricing")
        ):
            out[k] = _merge(out[k], v)
        else:
            out[k] = copy.deepcopy(v)
    return out


def load(path: str | Path | None) -> dict:
    """Load hlab.yaml from `path` (if it exists) over the defaults.

    Raise ValueError on unknown settings, on a mapping where a value belongs (or the
    other way round), and on a catalogue entry that lacks a key or names an unknown
    server category.
    """
    data: dict = {}
    if path and Path(path).is_file():
        data = yaml.safe_load(Path(path).read_text(encoding="utf-8")) or {}
    if not isinstance(data, dict):
        raise ValueError(f"{path}: expected a mapping at the top level")
    if "dns" in data:  # removed: zones of the DNS API took its place
        raise ValueError(
            f"{path}: the setting dns is gone; create a DNS zone through the API "
            "(/v1/zones) instead"
        )
    unknown = sorted(set(data) - set(DEFAULTS))
    if unknown:  # a typo would otherwise be ignored silently
        raise ValueError(
            f"{path}: unknown settings {', '.join(unknown)} "
            f"(known: {', '.join(sorted(DEFAULTS))})"
        )
    for key, default in DEFAULTS.items():
        if (
            key in data
            and data[key] is not None
            and isinstance(default, dict) != isinstance(data[key], dict)
        ):
            raise ValueError(
                f"{path}: {key} must be "
                f"{'a mapping' if isinstance(default, dict) else 'a value'}"
            )
    cfg = _merge(DEFAULTS, data)
    for catalogue, keys in ENTRY_KEYS.items():
        for name, entry in cfg[catalogue].items():
            missing = [key for key in keys if key not in (entry or {})]
            if missing:  # older files lack the prices, for example
                raise ValueError(
                    f"{path}: {catalogue}.{name} lacks {', '.join(missing)}"
                )
    for name, entry in cfg["server_types"].items():
        if entry["category"] not in SERVER_CATEGORIES:
            raise ValueError(
                f"{path}: server_types.{name}: unknown category {entry['category']!r} "
                f"(known: {', '.join(SERVER_CATEGORIES)})"
            )
    return cfg


def location_pair(first: str, second: str) -> str:
    """Return the key of two locations in `latency_ms`, e.g. "fsn1-nbg1"."""
    return "-".join(sorted((first, second)))
