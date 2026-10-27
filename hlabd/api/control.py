"""Lab control under /_lab/: faults, Internet clients, Storage Box accounts, reset.

The lab CLI and test harnesses use it, also from inside the VMs through
http://198.18.0.1/_lab/...
"""

import pwd
import re

from fastapi import FastAPI, Request
from fastapi.responses import PlainTextResponse

from .. import config
from ..errors import invalid, not_found
from ..lab import Lab
from ..records import Client, Fault, TargetRef
from ..state import (
    CLIENTS,
    FLOATING_IPS,
    NETWORKS,
    SERVERS,
    STORAGE_BOXES,
    SUBACCOUNTS,
    VOLUMES,
    client_by_name,
    primary_address_of,
    server_by_name,
)
from . import desired, hetzner, system
from .faults import drop_faults, fault_json
from .storagebox.base import set_up_box_user, username
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
    servers = sorted(state[SERVERS].values(), key=lambda s: s["id"])
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
            hetzner.floating_ip_json(lab, f) for f in state[FLOATING_IPS].values()
        ],
        "clients": list(state[CLIENTS].values()),
        "faults": [fault_json(fault) for fault in state.data["faults"]],
        "netem": dict(state.data["netem"]),
        "api_fault": dict(lab.api_fault),
    }
    for entry, server in zip(entries, servers, strict=True):
        alive = await lab.blocking(fabric.vms.is_alive, server["id"])
        entry["vm"] = "on" if alive else "off"
    return report


# --- faults ---------------------------------------------------------------------------


def fault_target(lab: Lab, name: str) -> TargetRef:
    """Return the reference a fault uses for a server or client name, or raise 404.

    A server is named by ID, a client by name (api/faults.py).
    """
    server = server_by_name(lab.state, name)
    if server:
        return {"server": server["id"]}
    if client_by_name(lab.state, name):
        return {"client": name}
    raise not_found(f"server or client {name!r}")


def in_cut_order(fault: dict) -> dict:
    """Return a fault with the two ends of a cut sorted by name.

    A cut is the same in both directions, so it is always stored in one order.
    """
    if fault.get("type") == "cut" and fault.get("first") and fault.get("second"):
        first, second = sorted((fault["first"], fault["second"]))
        return {**fault, "first": first, "second": second}
    return fault


def checked_fault(lab: Lab, body: dict) -> Fault:
    """Return the fault a request describes, with references to what it points at."""
    fault_type = body.get("type")
    if fault_type not in FAULT_TYPES:
        raise invalid(f"fault type must be one of {', '.join(FAULT_TYPES)}")
    given = {"type": fault_type}
    for field, default in FAULT_TYPES[fault_type].items():
        given[field] = body.get(field) or default
        if given[field] is None:
            raise invalid(f"{fault_type}: {field} is required")
    if fault_type == "cut" and given["net"] not in ("public", "private", "all"):
        raise invalid("net must be public, private or all")
    if fault_type == "block" and given["what"] not in ("api", "box", "internet"):
        raise invalid("what must be api, box or internet")
    given = in_cut_order(given)
    if fault_type == "cut":
        return {
            "type": "cut",
            "first": given["first"],
            "second": given["second"],
            "net": given["net"],
            "first_ref": fault_target(lab, given["first"]),
            "second_ref": fault_target(lab, given["second"]),
        }
    if fault_type == "isolate":
        return {
            "type": "isolate",
            "vm": given["vm"],
            "target_ref": fault_target(lab, given["vm"]),
        }
    return {
        "type": "block",
        "vm": given["vm"],
        "what": given["what"],
        "target_ref": fault_target(lab, given["vm"]),
    }


async def add_fault(lab: Lab, body: dict) -> dict:
    """Add a fault (once) and put it in place."""
    state = lab.state
    fault = checked_fault(lab, body)
    with state.change():
        if fault not in state.data["faults"]:
            state.data["faults"].append(fault)
        # the answer: faults after this change
        faults = [fault_json(fault) for fault in state.data["faults"]]
    await system.apply_faults(lab)
    return {"faults": faults}


async def remove_faults(lab: Lab, body: dict) -> dict:
    """Remove the faults that match every given field (a cut in either direction).

    The fields are compared as the API shows them (references as text).
    """
    state = lab.state
    given = in_cut_order({k: v for k, v in body.items() if v is not None})
    with state.change():
        faults: list[Fault] = state.data["faults"]
        state.data["faults"] = [
            fault
            for fault in faults
            if not all(fault_json(fault).get(k) == v for k, v in given.items())
        ]
        # the answer: faults after this change
        shown = [fault_json(fault) for fault in state.data["faults"]]
    await system.apply_faults(lab)
    return {"faults": shown}


async def clear_faults(lab: Lab) -> dict:
    """Remove the network faults, netem and dead hardware.

    The route clears the API faults itself.
    """
    state = lab.state
    with state.change():
        state.data["faults"] = []
        state.data["netem"] = {}
        for server in state[SERVERS].values():
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
    target = fault_target(lab, body.get("vm", ""))
    with state.change():
        taps = desired.target_ifaces(state, target, body.get("net") or "all")
        for tap in taps:
            if args:
                state.data["netem"][tap] = " ".join(args.split())
            else:
                state.data["netem"].pop(tap, None)
        netem = dict(state.data["netem"])  # the answer: netem after this change
    await system.apply_latency(lab)
    return {"netem": netem}


async def control_vm(lab: Lab, name: str, cmd: str) -> dict:
    """Pause or continue a VM, or mark its hardware dead or alive again.

    With dead hardware, a power on succeeds, but the VM stays off.
    """
    server = lab.server_by_name_or_404(name)
    if cmd == "pause":
        await lab.blocking(lab.fabric.vms.pause, server["id"])
    elif cmd == "cont":
        await lab.blocking(lab.fabric.vms.resume, server["id"])
    elif cmd in ("dead", "alive"):
        with lab.state.change():
            server["_dead"] = cmd == "dead"
    else:
        raise invalid("command must be pause, cont, dead or alive")
    return {"ok": True}


def set_api_fault(lab: Lab, body: dict) -> dict:
    """Inject faults into the API's answers.

    {"status": 503, "count": 3} answers the next 3 requests with that status.
    {"delay_ms": n} delays every request by n milliseconds.
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
            used = {c["number"] for c in state[CLIENTS].values()}
            number = next(i for i in range(1, 200) if i not in used)
            cid = state.new_id(CLIENTS)
            client = {
                "id": cid,
                "name": name,
                "number": number,
                "veth": f"cl{number}",
                "ip": f"{config.CLIENT_NET}.{10 + number}",
                "ip6": f"{config.CLIENT6}::{10 + number:x}",
            }
            state[CLIENTS][cid] = client
    await system.add_client(lab, client)
    return {"client": client}


async def remove_client(lab: Lab, name: str) -> dict:
    """Remove a client; its faults and netem are removed with it."""
    state = lab.state
    with state.change():
        client = client_or_404(lab, name)
        del state[CLIENTS][client["id"]]
        drop_faults(lab, {"client": client["name"]}, [client["veth"]])
    await system.remove_client(lab, client)
    return {"ok": True}


# --- Storage Box accounts and reset ---------------------------------------------------


async def add_box_user(lab: Lab, body: dict) -> dict:
    """Create an SFTP account on the Storage Box with its own home.

    Body: {"name": "backup1", "public_key": "ssh-ed25519 ..."}. The names of the API's
    accounts (u<id>, u<id>-sub<n>) and the users of the container cannot be used.
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
        await lab.blocking(pwd.getpwnam, name)  # reads /etc/passwd
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
    Storage Boxes with their data, clients and faults. System images, the lab key and
    the Storage Box accounts made through /_lab/box/users stay. IDs keep counting up,
    as on Hetzner, so no old ID matches a new resource.
    """
    state, fabric = lab.state, lab.fabric
    # no old action may start a VM or change the state while the lab is emptied
    await lab.stop_actions()
    # copies: the loops below run scripts, so the state may change in between; what
    # was there is torn down as it was last seen, gone from the state or not
    servers = list(state[SERVERS].values())  # record: torn down as last seen
    volumes = list(state[VOLUMES].values())  # record: torn down as last seen
    subaccounts = list(state[SUBACCOUNTS].values())  # record: torn down as last seen
    boxes = list(state[STORAGE_BOXES].values())  # record: torn down as last seen
    routed = [f["ip"] for f in state[FLOATING_IPS].values() if f.get("server")]
    clients = list(state[CLIENTS].values())
    networks = list(state[NETWORKS])
    for server in servers:
        await system.delete_vm(lab, server)
    for volume in volumes:
        await lab.blocking(fabric.volumes.delete, volume["id"])
    for subaccount in subaccounts:
        await lab.blocking(fabric.box.remove_account, subaccount["username"])
    for box in boxes:
        await lab.blocking(fabric.box.remove_account, username(box["id"]))
        await lab.blocking(fabric.box.remove_box_data, box["id"])
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

    @app.get("/_lab/acme/ca.pem")
    async def acme_ca():
        # the lab CA (Pebble's HTTPS) and Pebble's current root and intermediate
        bundle = await lab.blocking(lab.fabric.acme.ca_bundle)
        return PlainTextResponse(bundle, media_type="application/x-pem-file")

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
