"""Lab control under /_lab/: faults, Internet clients, Storage Box accounts, reset.

Used by the lab CLI and by test harnesses (also from inside the VMs, through
http://198.18.0.1/_lab/...).
"""

import pwd
import re

from fastapi import FastAPI, Request

from .. import config
from ..errors import invalid, not_found
from ..fabric.storagebox import data_dir, snap_dir, username
from ..lab import Lab
from ..records import Client
from ..state import client_by_name, primary_address_of, server_by_name
from . import desired, hetzner, system
from .faults import REF_FIELDS, drop_faults
from .storagebox.base import set_up_box_user
from .web import read_body

# the fields of each fault type, with their defaults (None: required). The field "vm"
# names a server or a client.
FAULT_TYPES: dict[str, dict[str, str | None]] = {
    "cut": {"first": None, "second": None, "net": "all"},
    "isolate": {"vm": None},
    "block": {"vm": None, "what": None},
}

NETEM_WORD = re.compile(
    r"(delay|loss|duplicate|corrupt|reorder|rate|limit|gap|distribution|normal|pareto|"
    r"paretonormal|uniform|random|state|gemodel|ecn|slot|"
    r"\d+(\.\d+)?(us|ms|s|%|bit|kbit|mbit|gbit)?)"
)


# a client's name is also the name of its network namespace (/run/netns/<name>,
# `ip netns`): plain ASCII, and no leading - or _, which `ip` would read as an option
CLIENT_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9_-]{0,11}")


# --- status ---------------------------------------------------------------------------


async def status(lab: Lab) -> dict:
    """Return the lab's status: servers, Floating IPs, clients, faults, API faults."""
    state, fabric = lab.state, lab.fabric
    # a copy: checking whether a VM runs calls a script
    servers = sorted(state["servers"].values(), key=lambda s: s["id"])
    entries: list[dict] = [
        {
            "id": s["id"],
            "name": s["name"],
            "status": s["status"],
            "location": s["location"],
            "server_type": s["server_type"],
            "public_ip": primary_address_of(state, s, "ipv4"),
            "ipv6": primary_address_of(state, s, "ipv6"),
            "private": [
                {"network": nic["network"], "ip": nic["ip"]}
                for nic in s["_lab"]["nics"]
            ],
            "dead": bool(s.get("_dead")),
        }
        for s in servers
    ]
    report = {
        "servers": entries,
        "floating_ips": [
            hetzner.floating_ip_json(lab, f) for f in state["floating_ips"].values()
        ],
        "clients": list(state["clients"].values()),
        "faults": list(state.data["faults"]),
        "netem": dict(state.data["netem"]),
        "api_fault": dict(lab.api_fault),
    }
    for entry, server in zip(entries, servers, strict=True):
        alive = await lab.blocking(fabric.vms.is_alive, server)
        entry["vm"] = "on" if alive else "off"
    return report


# --- faults ---------------------------------------------------------------------------


def fault_ref(lab: Lab, name: str) -> str:
    """Return the reference a fault uses for a server or client name, or raise 404.

    A server is named by ID (s:<id>), a client by name (c:<name>); see api/desired.py.
    """
    server = server_by_name(lab.state, name)
    if server:
        return f"s:{server['id']}"
    if client_by_name(lab.state, name):
        return f"c:{name}"
    raise not_found(f"server or client {name!r}")


def in_cut_order(fault: dict) -> dict:
    """Return a fault with the two ends of a cut sorted by name.

    A cut is the same either way round, so it is stored and removed in one order.
    """
    if fault.get("type") == "cut" and fault.get("first") and fault.get("second"):
        first, second = sorted((fault["first"], fault["second"]))
        return {**fault, "first": first, "second": second}
    return fault


def checked_fault(lab: Lab, body: dict) -> dict:
    """Return the fault a request describes, with references to what it points at."""
    fault_type = body.get("type")
    if fault_type not in FAULT_TYPES:
        raise invalid(f"fault type must be one of {', '.join(FAULT_TYPES)}")
    fault = {"type": fault_type}
    for field, default in FAULT_TYPES[fault_type].items():
        fault[field] = body.get(field) or default
        if fault[field] is None:
            raise invalid(f"{fault_type}: {field} is required")
    if fault_type == "cut" and fault["net"] not in ("public", "private", "all"):
        raise invalid("net must be public, private or all")
    if fault_type == "block" and fault["what"] not in ("api", "box", "internet"):
        raise invalid("what must be api, box or internet")
    fault = in_cut_order(fault)
    for field, ref_field in REF_FIELDS.items():
        if field in fault:
            fault[ref_field] = fault_ref(lab, fault[field])
    return fault


async def add_fault(lab: Lab, body: dict) -> dict:
    """Add a fault (once) and put it in place."""
    state = lab.state
    fault = checked_fault(lab, body)
    with state.change():
        if fault not in state.data["faults"]:
            state.data["faults"].append(fault)
        faults = list(state.data["faults"])  # the answer, as this change left them
    await system.apply_faults(lab)
    return {"faults": faults}


async def remove_faults(lab: Lab, body: dict) -> dict:
    """Remove the faults that match every given field (a cut either way round)."""
    state = lab.state
    given = in_cut_order({k: v for k, v in body.items() if v is not None})
    with state.change():
        state.data["faults"] = [
            fault
            for fault in state.data["faults"]
            if not all(fault.get(k) == v for k, v in given.items())
        ]
        faults = list(state.data["faults"])  # the answer, as this change left them
    await system.apply_faults(lab)
    return {"faults": faults}


async def clear_faults(lab: Lab) -> dict:
    """Remove the network faults, netem and dead hardware.

    The route clears the API faults itself.
    """
    state = lab.state
    with state.change():
        state.data["faults"] = []
        state.data["netem"] = {}
        for server in state["servers"].values():
            server.pop("_dead", None)
    await system.apply_faults(lab)
    await system.apply_latency(lab)
    return {"ok": True}


async def set_netem(lab: Lab, body: dict) -> dict:
    """Set netem options on a server's (or client's) interfaces; empty args clear them.

    Body: {"vm": name, "net": public|private|all, "args": "delay 100ms loss 5%"}.
    """
    state = lab.state
    args = body.get("args") or ""
    if not isinstance(args, str) or (
        args and not all(NETEM_WORD.fullmatch(word) for word in args.split())
    ):
        raise invalid("args are netem options like 'delay 100ms 10ms loss 5%'", "args")
    ref = fault_ref(lab, body.get("vm", ""))
    with state.change():
        taps = desired.target_ifaces(state, ref, body.get("net") or "all")
        for tap in taps:
            if args:
                state.data["netem"][tap] = " ".join(args.split())
            else:
                state.data["netem"].pop(tap, None)
        netem = dict(state.data["netem"])  # the answer, as this change left them
    await system.apply_latency(lab)
    return {"netem": netem}


async def control_vm(lab: Lab, name: str, cmd: str) -> dict:
    """Pause or continue a VM, or mark its hardware dead or alive again.

    Dead hardware: a power-on succeeds, the VM stays off.
    """
    server = lab.server_by_name_or_404(name)
    if cmd == "pause":
        await lab.blocking(lab.fabric.vms.pause, server)
    elif cmd == "cont":
        await lab.blocking(lab.fabric.vms.resume, server)
    elif cmd in ("dead", "alive"):
        with lab.state.change():
            server["_dead"] = cmd == "dead"
    else:
        raise invalid("command must be pause, cont, dead or alive")
    return {"ok": True}


def set_api_fault(lab: Lab, body: dict) -> dict:
    """Inject faults into the API's answers.

    {"status": 503, "count": 3} answers the next requests with that status;
    {"delay_ms": n} delays all.
    """
    try:
        new = {k: int(body.get(k) or 0) for k in ("status", "count", "delay_ms")}
    except (TypeError, ValueError):
        raise invalid("status, count and delay_ms are numbers") from None
    lab.api_fault.update(new)
    return {"api_fault": lab.api_fault}


# --- clients --------------------------------------------------------------------------


def client_or_404(lab: Lab, name: str) -> Client:
    """Return the Internet client with this name, or raise 404."""
    client = client_by_name(lab.state, name)
    if not client:
        raise not_found("client")
    return client


async def add_client(lab: Lab, body: dict) -> dict:
    """Add an "Internet" client with its own addresses (or return the existing one)."""
    state = lab.state
    name = body.get("name")
    if not isinstance(name, str) or not CLIENT_NAME.fullmatch(name):
        raise invalid(
            "client name: 1 to 12 ASCII letters, digits, - and _, "
            "starting with a letter or digit",
            "name",
        )
    with state.change():
        client = client_by_name(state, name)
        if not client:
            used = {c["number"] for c in state["clients"].values()}
            number = next(i for i in range(1, 200) if i not in used)
            cid = state.new_id("clients")
            client = {
                "id": cid,
                "name": name,
                "number": number,
                "veth": f"cl{number}",
                "ip": f"{config.CLIENT_NET}.{10 + number}",
                "ip6": f"{config.CLIENT6}::{10 + number:x}",
            }
            state["clients"][cid] = client
    await system.add_client(lab, client)
    return {"client": client}


async def remove_client(lab: Lab, name: str) -> dict:
    """Remove a client; its faults and netem are removed with it."""
    state = lab.state
    with state.change():
        client = client_or_404(lab, name)
        del state["clients"][client["id"]]
        drop_faults(lab, f"c:{client['name']}", [client["veth"]])
    await system.remove_client(lab, client)
    return {"ok": True}


# --- Storage Box accounts and reset ---------------------------------------------------


async def add_box_user(lab: Lab, body: dict) -> dict:
    """Create an SFTP account on the Storage Box with its own home.

    Body: {"name": "backup1", "public_key": "ssh-ed25519 ..."}. The names of the API's
    accounts (u<id>, u<id>-sub<n>) and of the container's own users cannot be used.
    """
    state = lab.state
    name, key = str(body.get("name") or ""), str(body.get("public_key") or "")
    # ed25519, RSA, ECDSA, FIDO
    keyed = key.startswith(("ssh-", "ecdsa-sha2-", "sk-ssh-", "sk-ecdsa-"))
    if (
        not re.fullmatch(r"[a-z][a-z0-9_-]{0,31}", name)
        or not keyed
        or "\n" in key.strip()
    ):
        raise invalid(
            "name (lower case letters, digits, - and _) and one public_key are required"
        )
    if re.fullmatch(r"u[0-9]+(-sub[0-9]+)?", name):
        raise invalid("names like u<id> belong to the Storage Box API", "name")
    try:
        pwd.getpwnam(name)  # a file read, small
        if name not in state.data["box_users"]:
            raise invalid("a user of the container has this name", "name")
    except KeyError:
        pass
    await set_up_box_user(lab, name, key)
    with state.change():
        state.data["box_users"][name] = key.strip()
    return {"user": name, "host": config.SERVICES, "port": 23}


async def reset_lab(lab: Lab) -> dict:
    """Remove everything the APIs created.

    That is servers (VMs and disks), networks, keys, firewalls, Floating IPs, volumes,
    Storage Boxes with their data, clients, faults. System images, the lab key and the
    lab's own Storage Box accounts (/_lab/box/users) stay. IDs keep counting up, as on
    Hetzner, so no old ID matches a new resource.
    """
    state, fabric = lab.state, lab.fabric
    # no old action may start a VM or change the state while the lab is emptied
    await lab.stop_actions()
    # copies: the loops below run scripts
    servers = list(state["servers"].values())
    volumes = list(state["volumes"].values())
    subaccounts = list(state["subaccounts"].values())
    boxes = list(state["storage_boxes"].values())
    routed = [f["ip"] for f in state["floating_ips"].values() if f.get("server")]
    clients = list(state["clients"].values())
    networks = list(state["networks"])
    for server in servers:
        await system.delete_vm(lab, server)
    for volume in volumes:
        await lab.blocking(fabric.volumes.delete, volume)
    for subaccount in subaccounts:
        await lab.blocking(fabric.box.remove_account, subaccount["username"])
    for box in boxes:
        await lab.blocking(fabric.box.remove_account, username(box))
        await lab.blocking(fabric.box.remove_tree, data_dir(box))
        await lab.blocking(fabric.box.remove_tree, snap_dir(box))
    for ip in routed:
        await system.route_floating_ip(lab, ip, None)
    for client in clients:
        await system.remove_client(lab, client)
    state.clear()
    await lab.blocking(state.flush)  # the empty lab is on disk before the answer
    lab.clear_api_fault()
    # a VM the state no longer knows (e.g. one whose server's creation was cut short)
    await lab.blocking(fabric.vms.kill_all)
    for nid in networks:  # clear the tables of the deleted networks
        await system.drop_cloud_network(lab, nid)
    await system.write_dhcp(lab)
    await system.apply_faults(lab)
    await system.apply_firewall(lab)
    return {"ok": True}


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the lab control routes (under /_lab/) to the app."""

    @app.get("/_lab/status")
    async def status_get():
        return await status(lab)

    @app.post("/_lab/faults")
    async def fault_create(request: Request):
        return await add_fault(lab, await read_body(request))

    @app.post("/_lab/faults/remove")
    async def fault_delete(request: Request):
        return await remove_faults(lab, await read_body(request))

    @app.post("/_lab/faults/clear")
    async def fault_clear():
        lab.clear_api_fault()
        return await clear_faults(lab)

    @app.post("/_lab/netem")
    async def netem_update(request: Request):
        return await set_netem(lab, await read_body(request))

    @app.post("/_lab/vm/{name}/{cmd}")
    async def vm_action(name: str, cmd: str):
        return await control_vm(lab, name, cmd)

    @app.post("/_lab/api-fault")
    async def api_fault_update(request: Request):
        return set_api_fault(lab, await read_body(request))

    @app.post("/_lab/clients")
    async def client_create(request: Request):
        return await add_client(lab, await read_body(request))

    @app.delete("/_lab/clients/{name}")
    async def client_delete(name: str):
        return await remove_client(lab, name)

    @app.post("/_lab/box/users")
    async def box_user_create(request: Request):
        return await add_box_user(lab, await read_body(request))

    @app.post("/_lab/reset")
    async def reset_action():
        return await reset_lab(lab)
