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
from . import hetzner
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


# client names name network namespaces (/run/netns/<name>, `ip netns`): plain ASCII,
# and no leading - or _, which `ip` would take for an option
CLIENT_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9_-]{0,11}")


# --- status ---------------------------------------------------------------------------


def status(lab: Lab) -> dict:
    """Return the lab's status: servers, Floating IPs, clients and faults.

    The injected API fault is added by the route, on the event loop (see `Lab`).
    """
    state, fabric = lab.state, lab.fabric
    with state.lock:  # a copy: asking whether a VM runs is a script call
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
        }
    for entry, server in zip(entries, servers, strict=True):
        entry["vm"] = "on" if fabric.vm_alive(server) else "off"
    return report


# --- faults ---------------------------------------------------------------------------


def fault_ref(lab: Lab, name: str) -> str:
    """Return what a fault points at for a server or client name, or raise 404.

    A server is named by ID (s:<id>), a client by name (c:<name>); see fabric/faults.py.
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
    with state.lock:
        if fault not in state.data["faults"]:
            state.data["faults"].append(fault)
        state.save()
    await lab.blocking(lab.fabric.apply_faults)
    return {"faults": state.data["faults"]}


async def remove_faults(lab: Lab, body: dict) -> dict:
    """Remove the faults that match every given field (a cut either way round)."""
    state = lab.state
    given = in_cut_order({k: v for k, v in body.items() if v is not None})
    with state.lock:
        state.data["faults"] = [
            fault
            for fault in state.data["faults"]
            if not all(fault.get(k) == v for k, v in given.items())
        ]
        state.save()
    await lab.blocking(lab.fabric.apply_faults)
    return {"faults": state.data["faults"]}


async def clear_faults(lab: Lab) -> dict:
    """Remove every fault: network faults, netem, dead hardware, API faults."""
    state = lab.state
    with state.lock:
        state.data["faults"] = []
        state.data["netem"] = {}
        for server in state["servers"].values():
            server.pop("_dead", None)
        lab.clear_api_fault()
        state.save()
    await lab.blocking(lab.fabric.apply_faults)
    await lab.blocking(lab.fabric.apply_latency)
    return {"ok": True}


def set_netem(lab: Lab, body: dict) -> dict:
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
    taps = lab.fabric.target_ifaces(ref, body.get("net") or "all")
    with state.lock:
        for tap in taps:
            if args:
                state.data["netem"][tap] = " ".join(args.split())
            else:
                state.data["netem"].pop(tap, None)
        state.save()
    lab.fabric.apply_latency()
    return {"netem": state.data["netem"]}


async def control_vm(lab: Lab, name: str, cmd: str) -> dict:
    """Pause or continue a VM, or mark its hardware dead or alive again.

    Dead hardware: a power-on succeeds, the VM stays off.
    """
    server = lab.server_by_name_or_404(name)
    if cmd == "pause":
        await lab.blocking(lab.fabric.pause_vm, server)
    elif cmd == "cont":
        await lab.blocking(lab.fabric.continue_vm, server)
    elif cmd in ("dead", "alive"):
        with lab.state.lock:
            server["_dead"] = cmd == "dead"
            lab.state.save()
    else:
        raise invalid("command must be pause, cont, dead or alive")
    return {"ok": True}


def set_api_fault(lab: Lab, body: dict) -> dict:
    """Inject faults into the API's answers; runs on the event loop (see `Lab`).

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


def add_client(lab: Lab, body: dict) -> dict:
    """Add an "Internet" client with its own addresses (or return the one there)."""
    state = lab.state
    name = body.get("name")
    if not isinstance(name, str) or not CLIENT_NAME.fullmatch(name):
        raise invalid(
            "client name: 1 to 12 ASCII letters, digits, - and _, "
            "starting with a letter or digit",
            "name",
        )
    with state.lock:
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
            state.save()
    lab.fabric.add_client(client)
    return {"client": client}


def remove_client(lab: Lab, name: str) -> dict:
    """Remove a client; its faults and netem are removed with it."""
    state = lab.state
    with state.lock:
        client = client_or_404(lab, name)
        del state["clients"][client["id"]]
        drop_faults(lab, f"c:{client['name']}", [client["veth"]])
        state.save()
    lab.fabric.remove_client(client)
    return {"ok": True}


# --- Storage Box accounts and reset ---------------------------------------------------


def add_box_user(lab: Lab, body: dict) -> dict:
    """Create an SFTP account on the Storage Box with its own home.

    Body: {"name": "backup1", "public_key": "ssh-ed25519 ..."}. Names of the API's
    accounts (u<id>, u<id>-sub<n>) and of the container's own users are taken.
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
        pwd.getpwnam(name)
        if name not in state.data["box_users"]:
            raise invalid("a user of the container has this name", "name")
    except KeyError:
        pass
    set_up_box_user(lab, name, key)
    with state.lock:
        state.data["box_users"][name] = key.strip()
        state.save()
    return {"user": name, "host": config.SERVICES, "port": 23}


async def reset_lab(lab: Lab) -> dict:
    """Remove everything the APIs created.

    That is servers (VMs and disks), networks, keys, firewalls, Floating IPs, volumes,
    Storage Boxes with their data, clients, faults. System images, the lab key and the
    lab's own Storage Box accounts (/_lab/box/users) stay; IDs go on counting, as in
    the cloud, so nothing old matches a new one.
    """
    state, fabric = lab.state, lab.fabric
    # no old action may start a VM or change the state while the lab is emptied
    await lab.stop_actions()
    with state.lock:  # copies: the loops below run scripts
        servers = list(state["servers"].values())
        volumes = list(state["volumes"].values())
        subaccounts = list(state["subaccounts"].values())
        boxes = list(state["storage_boxes"].values())
        routed = [f["ip"] for f in state["floating_ips"].values() if f.get("server")]
        clients = list(state["clients"].values())
        networks = list(state["networks"])
    for server in servers:
        await lab.blocking(fabric.delete_vm, server)
    for volume in volumes:
        await lab.blocking(fabric.delete_volume, volume)
    for subaccount in subaccounts:
        await lab.blocking(fabric.box.remove_account, subaccount["username"])
    for box in boxes:
        await lab.blocking(fabric.box.remove_account, username(box))
        await lab.blocking(fabric.box.remove_tree, data_dir(box))
        await lab.blocking(fabric.box.remove_tree, snap_dir(box))
    for ip in routed:
        await lab.blocking(fabric.route_floating_ip, ip, None)
    for client in clients:
        await lab.blocking(fabric.remove_client, client)
    state.clear()
    lab.clear_api_fault()
    # a VM the state no longer knows (e.g. one whose server's creation was cut short)
    await lab.blocking(fabric.kill_all_vms)
    for nid in networks:  # no old routes may wait in the deleted networks' tables
        await lab.blocking(fabric.drop_network, nid)
    await lab.blocking(fabric.write_dhcp)
    await lab.blocking(fabric.apply_faults)
    await lab.blocking(fabric.apply_firewalls)
    return {"ok": True}


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the lab control routes (under /_lab/) to the app."""

    @app.get("/_lab/status")
    async def status_get():
        report = await lab.blocking(status, lab)
        return {**report, "api_fault": dict(lab.api_fault)}

    @app.post("/_lab/faults")
    async def fault_create(request: Request):
        return await add_fault(lab, await read_body(request))

    @app.post("/_lab/faults/remove")
    async def fault_delete(request: Request):
        return await remove_faults(lab, await read_body(request))

    @app.post("/_lab/faults/clear")
    async def fault_clear():
        return await clear_faults(lab)

    @app.post("/_lab/netem")
    async def netem_update(request: Request):
        return await lab.blocking(set_netem, lab, await read_body(request))

    @app.post("/_lab/vm/{name}/{cmd}")
    async def vm_action(name: str, cmd: str):
        return await control_vm(lab, name, cmd)

    @app.post("/_lab/api-fault")
    async def api_fault_update(request: Request):
        # on the event loop, like the middleware that counts the fault down
        return set_api_fault(lab, await read_body(request))

    @app.post("/_lab/clients")
    async def client_create(request: Request):
        return await lab.blocking(add_client, lab, await read_body(request))

    @app.delete("/_lab/clients/{name}")
    async def client_delete(name: str):
        return await lab.blocking(remove_client, lab, name)

    @app.post("/_lab/box/users")
    async def box_user_create(request: Request):
        return await lab.blocking(add_box_user, lab, await read_body(request))

    @app.post("/_lab/reset")
    async def reset_action():
        return await reset_lab(lab)
