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
    # name → {url, checksums}: the current release and its checksum file, loaded at the lab's start
    "images": {
        "debian-13": {"url": "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-generic-amd64.qcow2",
                      "checksums": "https://cloud.debian.org/images/cloud/trixie/latest/SHA512SUMS"},
        "ubuntu-26.04": {"url": "https://cloud-images.ubuntu.com/releases/26.04/release/ubuntu-26.04-server-cloudimg-amd64.img",
                         "checksums": "https://cloud-images.ubuntu.com/releases/26.04/release/SHA256SUMS"},
    },
    "storage_box": {"enabled": True, "size_gb": 10},
    # Storage Box types of the Storage Box API (size in TB is reported; the lab's box holds storage_box.size_gb)
    "storage_box_types": {
        "bx11": {"size_tb": 1, "snapshots": 10, "subaccounts": 100, "description": "BX11"},
        "bx21": {"size_tb": 5, "snapshots": 20, "subaccounts": 100, "description": "BX21"},
        "bx31": {"size_tb": 10, "snapshots": 30, "subaccounts": 100, "description": "BX31"},
        "bx41": {"size_tb": 20, "snapshots": 40, "subaccounts": 100, "description": "BX41"},
    },
    "dns": {},
}

# Fixed addresses of every lab (each lab has its own network namespace).
GATEWAY = "172.31.1.1"            # Hetzner's public gateway, on-link on every server
SERVICES = "198.18.0.1"           # fake Cloud API (port 80), Storage Box (ports 22, 23), DNS
HETZNER_API = "198.18.0.3"        # fake api.hetzner.com (Storage Boxes), port 80
METADATA = "169.254.169.254"
METADATA6 = "2001:db8:fffe::a9fe"   # where fe80::a9fe:a9fe is DNATed to: the metadata service only
PUBLIC_NET = "198.51.100"         # server primary IPv4: .10 + index
FLOATING_NET = "203.0.113"        # Floating IPv4: .10 + n
CLIENT_NET = "192.0.2"            # "Internet" clients: .10 + n, gateway .1
OPERATOR = "192.0.2.2"            # source of everything the lab container itself (and its consumers) sends to VMs
ADMIN = "198.18.0.2"              # source of `hlab ssh`: the lab's own access, not filtered by Cloud firewalls
# IPv6, all from the documentation prefix 2001:db8::/32. Every server routes a /64 (guest: <prefix>::1, gateway fe80::1).
IPV6_PREFIX = "2001:db8"          # server /64: 2001:db8:<index>::/64, Floating IPv6: 2001:db8:ff<n>::/64
GATEWAY6 = "fe80::1"
SERVICES6 = "2001:db8:fffe::1"    # fake Cloud API, Storage Box, DNS over IPv6
HETZNER_API6 = "2001:db8:fffe::4"
OPERATOR6 = "2001:db8:fffe::2"
ADMIN6 = "2001:db8:fffe::3"
CLIENT6 = "2001:db8:ffff"         # clients: 2001:db8:ffff::<10 + n>
ANY_TOKEN = "hlab" * 16           # what `hlab env` hands out when any token is accepted (hcloud wants 64 characters)


def _merge(base: dict, over: dict) -> dict:
    out = copy.deepcopy(base)
    for k, v in (over or {}).items():
        # Catalogue-like maps (types, locations, images, dns) are replaced, not merged, so a lab can drop entries.
        if isinstance(v, dict) and isinstance(out.get(k), dict) and k in ("api", "storage_box"):
            out[k] = _merge(out[k], v)
        else:
            out[k] = copy.deepcopy(v)
    return out


def load(path: str | Path | None) -> dict:
    data = {}
    if path and Path(path).is_file():
        data = yaml.safe_load(Path(path).read_text(encoding="utf-8")) or {}
    if not isinstance(data, dict):
        raise ValueError(f"{path}: expected a mapping at the top level")
    unknown = sorted(set(data) - set(DEFAULTS))
    if unknown:   # a typo would otherwise be ignored silently
        raise ValueError(f"{path}: unknown settings {', '.join(unknown)} (known: {', '.join(sorted(DEFAULTS))})")
    for key, default in DEFAULTS.items():
        if key in data and data[key] is not None and isinstance(default, dict) != isinstance(data[key], dict):
            raise ValueError(f"{path}: {key} must be {'a mapping' if isinstance(default, dict) else 'a value'}")
    return _merge(DEFAULTS, data)


def location_pair(a: str, b: str) -> str:
    return "-".join(sorted((a, b)))
