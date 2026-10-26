"""Lab configuration (hlab.yaml) with defaults."""

import copy
from pathlib import Path

import yaml

DEFAULTS: dict = {
    "name": "demo",
    "api": {"token": None, "time_scale": 1.0, "rate_limit": 3600},
    "memory_scale": 1.0,
    "server_types": {
        "cx23": {"cores": 2, "memory_gb": 4, "disk_gb": 40},
        "cx33": {"cores": 4, "memory_gb": 8, "disk_gb": 80},
        "cx43": {"cores": 8, "memory_gb": 16, "disk_gb": 160},
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
    "storage_box": {"enabled": True, "size_gb": 10},
    # Storage Box types of the Storage Box API (the API reports their size in TB; the
    # lab's box really holds storage_box.size_gb)
    "storage_box_types": {
        "bx11": {
            "size_tb": 1,
            "snapshots": 10,
            "subaccounts": 100,
            "description": "BX11",
        },
        "bx21": {
            "size_tb": 5,
            "snapshots": 20,
            "subaccounts": 100,
            "description": "BX21",
        },
        "bx31": {
            "size_tb": 10,
            "snapshots": 30,
            "subaccounts": 100,
            "description": "BX31",
        },
        "bx41": {
            "size_tb": 20,
            "snapshots": 40,
            "subaccounts": 100,
            "description": "BX41",
        },
    },
    "dns": {},
}

# the lab's volume: state, VM disks, images, keys
ROOT = Path("/hlab")

# Fixed addresses of every lab (each lab has its own network namespace).
GATEWAY = "172.31.1.1"  # Hetzner's public gateway, on-link on every server
# the lab's services sit in the benchmarking range (local addresses win)
SERVICES_NET = "198.18.0.0/15"
SERVICES = "198.18.0.1"  # fake Cloud API (port 80), Storage Box (ports 22, 23), DNS
HETZNER_API = "198.18.0.3"  # fake api.hetzner.com (Storage Boxes), port 80
METADATA = "169.254.169.254"
# where fe80::a9fe:a9fe is DNATed to: the metadata service only
METADATA6 = "2001:db8:fffe::a9fe"
# primary IPv4: the lowest free of .11 to .250 (api/primary_ips.py, free_ipv4)
PUBLIC_NET = "198.51.100"
FLOATING_NET = "203.0.113"  # Floating IPv4: .10 + n
CLIENT_NET = "192.0.2"  # "Internet" clients: .10 + n, gateway .1
# source of everything the lab container itself (and its consumers) sends to VMs
OPERATOR = "192.0.2.2"
# source of `hlab ssh`: the lab's own access, not filtered by Cloud firewalls
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
SERVICES_NET6 = "2001:db8:fffe"  # the /48 of the lab's own IPv6 addresses above

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
    """Return `base` with `over` laid on top; only `api` and `storage_box` merge."""
    out = copy.deepcopy(base)
    for k, v in (over or {}).items():
        # Catalogue-like maps (types, locations, images, dns) are replaced, not merged,
        # so a lab can drop entries.
        if (
            isinstance(v, dict)
            and isinstance(out.get(k), dict)
            and k in ("api", "storage_box")
        ):
            out[k] = _merge(out[k], v)
        else:
            out[k] = copy.deepcopy(v)
    return out


def load(path: str | Path | None) -> dict:
    """Load hlab.yaml from `path` (if it exists) over the defaults.

    Raises ValueError on unknown settings or a mapping where a value belongs (and the
    other way round).
    """
    data: dict = {}
    if path and Path(path).is_file():
        data = yaml.safe_load(Path(path).read_text(encoding="utf-8")) or {}
    if not isinstance(data, dict):
        raise ValueError(f"{path}: expected a mapping at the top level")
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
    return _merge(DEFAULTS, data)


def location_pair(first: str, second: str) -> str:
    """Return the key of two locations in `latency_ms`, e.g. "fsn1-nbg1"."""
    return "-".join(sorted((first, second)))
