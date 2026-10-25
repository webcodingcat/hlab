"""JSON shapes of the Hetzner Cloud API (https://docs.hetzner.cloud/) for the catalogue and the lab's resources."""

import base64
import hashlib
from datetime import datetime, timezone

from .. import config as C
from ..labels import firewall_servers, match_labels

CREATED = "2026-01-01T00:00:00+00:00"   # catalogue entries
TRAFFIC = 21990232555520                 # 20 TB included traffic

DC_NAMES = {"fsn1": "fsn1-dc14", "nbg1": "nbg1-dc3", "hel1": "hel1-dc2"}  # metadata: availability-zone
LOCATION_IDS = {"fsn1": 1, "nbg1": 2, "hel1": 3}
COORDS = {"fsn1": (50.47612, 12.370071), "nbg1": (49.452102, 11.076665), "hel1": (60.169855, 24.938379)}


def now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat()


def error(code: str, message: str, details: dict | None = None) -> dict:
    return {"error": {"code": code, "message": message, "details": details or {}}}


def location_id(cfg: dict, name: str) -> int:
    return LOCATION_IDS.get(name) or 10 + list(cfg["locations"]).index(name)


def location(cfg: dict, name: str) -> dict:
    loc = cfg["locations"][name]
    lat, lon = COORDS.get(name, (50.0, 10.0))
    return {"id": location_id(cfg, name), "name": name, "description": f"{loc.get('city', name)} DC Park 1",
            "country": loc.get("country", "DE"), "city": loc.get("city", name), "latitude": lat, "longitude": lon,
            "network_zone": loc.get("network_zone", "eu-central")}


def server_type_id(cfg: dict, name: str) -> int:
    return 100 + list(cfg["server_types"]).index(name)


def server_type(cfg: dict, name: str) -> dict:
    t = cfg["server_types"][name]
    prices = [{"location": l, "price_hourly": {"net": "0.0080", "gross": "0.0095"},
               "price_monthly": {"net": "4.9900", "gross": "5.9381"}, "included_traffic": TRAFFIC,
               "price_per_tb_traffic": {"net": "1.0000", "gross": "1.1900"}} for l in cfg["locations"]]
    return {"id": server_type_id(cfg, name), "name": name, "description": name.upper(), "category": "cost_optimized",
            "cores": t["cores"], "memory": float(t["memory_gb"]), "disk": t["disk_gb"], "deprecated": False,
            "deprecation": None, "prices": prices, "storage_type": "local", "cpu_type": "shared",
            "architecture": "x86", "included_traffic": TRAFFIC,
            "locations": [{"id": location_id(cfg, l), "name": l, "deprecation": None, "recommended": True,
                           "available": True} for l in cfg["locations"]]}


def image_id(cfg: dict, name: str) -> int:
    return 200 + list(cfg["images"]).index(name)


def _flavor(name: str) -> tuple[str, str | None]:
    flavor, _, version = name.partition("-")
    return (flavor if flavor in ("debian", "ubuntu", "centos", "fedora", "rocky", "alma") else "unknown"), version or None


def image(cfg: dict, name: str) -> dict:
    flavor, version = _flavor(name)
    return {"id": image_id(cfg, name), "type": "system", "status": "available", "name": name,
            "description": name.replace("-", " ").title(), "image_size": None, "disk_size": 5, "created": CREATED,
            "created_from": None, "bound_to": None, "os_flavor": flavor, "os_version": version, "rapid_deploy": True,
            "protection": {"delete": False}, "deprecated": None, "deprecation": None, "deleted": None, "labels": {},
            "architecture": "x86"}


def server_image(cfg: dict, state, ref) -> dict | None:
    return image(cfg, ref) if ref in cfg["images"] else None


def server(cfg: dict, state, s: dict) -> dict:
    lab = s["_lab"]
    pip = state["primary_ips"].get(s.get("ipv4_id"))
    pip6 = state["primary_ips"].get(s.get("ipv6_id"))
    fw = [{"id": fid, "status": "applied"} for fid, f in state["firewalls"].items() if s["id"] in firewall_servers(f, state)]
    ptr4 = next((x["dns_ptr"] for x in (pip or {}).get("dns_ptr", []) if x["ip"] == (pip or {}).get("ip")), None)
    return {
        "id": s["id"], "name": s["name"], "status": s["status"], "created": s["created"], "labels": s["labels"],
        "public_net": {
            "ipv4": {"id": pip["id"], "ip": pip["ip"], "blocked": False,
                     "dns_ptr": ptr4 or default_ptr(pip["ip"])} if pip else None,
            "ipv6": {"id": pip6["id"], "ip": pip6["ip"], "blocked": False, "dns_ptr": pip6.get("dns_ptr", [])}
            if pip6 else None,
            "floating_ips": [f["id"] for f in state["floating_ips"].values() if f.get("server") == s["id"]],
            "firewalls": fw,
        },
        "private_net": [{"network": n["network"], "ip": n["ip"], "alias_ips": n.get("alias_ips", []),
                         "mac_address": n["mac"]} for n in lab["nics"]],
        "server_type": server_type(cfg, s["server_type"]),
        "location": location(cfg, s["location"]),
        "image": server_image(cfg, state, s["image"]),
        "iso": None, "rescue_enabled": False, "locked": s.get("locked", False), "backup_window": None,
        "outgoing_traffic": 0, "ingoing_traffic": 0, "included_traffic": TRAFFIC,
        "protection": s["protection"],
        "volumes": sorted(v["id"] for v in state["volumes"].values() if v.get("server") == s["id"]),
        "load_balancers": [],
        "primary_disk_size": s["_lab"].get("disk_gb") or cfg["server_types"][s["server_type"]]["disk_gb"],
        "placement_group": None,
    }


def default_ptr(ip: str) -> str:
    """Hetzner's reverse DNS for an IPv4 without its own entry: the octets reversed (static.4.3.2.1.clients...)."""
    return f"static.{'.'.join(reversed(ip.split('.')))}.clients.your-server.de"


def primary_ip(cfg: dict, p: dict) -> dict:
    assignee = "server" if p.get("assignee_id") else "unassigned"
    return {"id": p["id"], "name": p["name"], "ip": p["ip"], "type": p["type"], "assignee_id": p.get("assignee_id"),
            "assignee_type": assignee, "auto_delete": p.get("auto_delete", True), "blocked": False,
            "location": location(cfg, p["location"]),
            "dns_ptr": p.get("dns_ptr") or ([{"ip": p["ip"], "dns_ptr": default_ptr(p["ip"])}]
                                            if p["type"] == "ipv4" else []), "labels": p.get("labels", {}),
            "protection": p.get("protection") or {"delete": False}, "created": p["created"]}


def network(state, n: dict) -> dict:
    servers = [s["id"] for s in state["servers"].values() if any(x["network"] == n["id"] for x in s["_lab"]["nics"])]
    return {"id": n["id"], "name": n["name"], "ip_range": n["ip_range"], "subnets": n["subnets"], "routes": n["routes"],
            "servers": servers, "load_balancers": [], "protection": n["protection"], "labels": n["labels"],
            "created": n["created"], "expose_routes_to_vswitch": n.get("expose_routes_to_vswitch", False)}


def floating_ip(cfg: dict, f: dict) -> dict:
    """Reverse DNS per address (several in an IPv6 /64); an IPv4 without its own entry has Hetzner's default."""
    ptrs = f.get("dns_ptr")
    ptrs = ptrs if isinstance(ptrs, list) else ([{"ip": f["ip"], "dns_ptr": ptrs}] if ptrs else [])
    if f["type"] == "ipv4" and not ptrs:
        ptrs = [{"ip": f["ip"], "dns_ptr": default_ptr(f["ip"])}]
    return {"id": f["id"], "name": f["name"], "description": f.get("description"), "ip": f["ip"], "type": f["type"],
            "server": f.get("server"), "dns_ptr": ptrs,
            "home_location": location(cfg, f["home_location"]), "blocked": False, "protection": f["protection"],
            "labels": f["labels"], "created": f["created"]}


def ssh_fingerprint(public_key: str) -> str:
    """The MD5 fingerprint of an OpenSSH public key; a key that is none (its blob does not name its type, leading
    whitespace as Hetzner refuses it since 2026-04) raises ValueError."""
    parts = public_key.split()
    if len(parts) < 2 or public_key != public_key.lstrip():
        raise ValueError("invalid public key")
    blob = base64.b64decode(parts[1], validate=True)
    n = int.from_bytes(blob[:4], "big")
    if len(blob) < 4 + n or blob[4:4 + n] != parts[0].encode():
        raise ValueError("invalid public key")
    digest = hashlib.md5(blob).hexdigest()
    return ":".join(digest[i:i + 2] for i in range(0, 32, 2))


def firewall(state, f: dict) -> dict:
    applied = []
    for a in f["applied_to"]:
        if a["type"] == "label_selector":
            sel = a["label_selector"]["selector"]
            res = [{"type": "server", "server": {"id": s["id"]}} for s in state["servers"].values()
                   if match_labels(s["labels"], sel)]
            applied.append({"type": "label_selector", "label_selector": {"selector": sel}, "applied_to_resources": res})
        else:
            applied.append(a)
    return {"id": f["id"], "name": f["name"], "labels": f["labels"], "created": f["created"], "rules": f["rules"],
            "applied_to": applied}


def ipv6_net(n: int) -> str:
    return f"{C.IPV6_PREFIX}:{n:x}::/64"
