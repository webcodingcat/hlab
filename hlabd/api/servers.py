"""Servers.

This module creates and deletes servers and runs the server actions (power, rebuild,
...). The actions wait as long as Hetzner does (the timings are in lab.py).
"""

import base64
import secrets
from collections.abc import Callable
from dataclasses import dataclass

from fastapi import FastAPI, Request

from ..errors import (
    ApiError,
    check_unique_name,
    invalid,
    limit_exceeded,
    locked,
    protected,
    server_not_stopped,
)
from ..lab import (
    CHANGE_TYPE_SECONDS,
    CREATE_START_SECONDS,
    DELETE_SECONDS,
    POWEROFF_END_SECONDS,
    POWEROFF_SECONDS,
    POWERON_END_SECONDS,
    POWERON_SECONDS,
    REBUILD_SECONDS,
    RESTART_END_SECONDS,
    RESTART_SECONDS,
    SERVER_LOCKED,
    SHUTDOWN_SECONDS,
    ActionSpec,
    ActionTable,
    HeldFlags,
    Lab,
    Plan,
    Steps,
    now,
)
from ..records import Action, Server, Volume
from . import hetzner, nics, primary_ips, system, volumes
from .catalogue import by_name_or_id, catalogue_name, image_path
from .faults import drop_faults
from .spec import hostname_ok
from .web import filtered, paginate, read_body, ref_id, update_resource

USER_DATA_LIMIT = 32 * 1024


def check_user_data(user_data: object) -> None:
    """Check that user data is a string of at most 32 KiB, if given."""
    if user_data is None:
        return
    if not isinstance(user_data, str):
        raise invalid("user_data is a string", "user_data")
    if len(user_data.encode()) > USER_DATA_LIMIT:
        raise invalid("user_data is limited to 32 KiB", "user_data")


def check_hostname(name: str) -> None:
    """Check that a server name is a valid host name (RFC 1123)."""
    if not name or not hostname_ok(name):
        raise invalid("must be a valid hostname (RFC 1123)", "name")


# --- create and delete ----------------------------------------------------------------


@dataclass(frozen=True)
class ServerRequest:
    """A checked request to create a server: what the later phases need from it."""

    name: str
    server_type: str
    image: str
    location: str
    public_keys: list[str]
    public_net: dict
    user_data: str | None
    labels: dict
    firewall_ids: list[int]
    volumes: list[Volume]
    network_ids: list[int]
    automount: bool
    start: bool


async def create_server(lab: Lab, body: dict) -> dict:
    """Create a server and start its create action.

    Everything is checked before anything changes, so a refused request leaves nothing
    behind. The create action creates the VM in the background. records.py describes
    the lab-only fields of a server (`server["_lab"]`) and of its NICs.
    """
    request = checked_server_request(lab, body)
    local_addresses = (  # runs a script: the addresses that no NIC may get
        await lab.blocking(lab.fabric.network.read_local_addresses)
        if request.network_ids
        else set()
    )
    with lab.holding() as held:
        server = record_server(lab, request, local_addresses, held)
        sid = server["id"]
        action = lab.new_action("create_server", [(sid, "server")])
        next_actions = (
            [lab.new_action("start_server", [(sid, "server")])] if request.start else []
        )
        steps = create_steps(lab, server, request, next_actions)
        lab.run_action(action, steps, server, next_actions, holding=held)
    return {
        "server": hetzner.server_json(lab, server),
        "action": action,
        "next_actions": next_actions,
        "root_password": server["_lab"].get("root_password"),
    }


def checked_server_request(lab: Lab, body: dict) -> ServerRequest:
    """Check a create request as far as possible without changing anything.

    Some checks must still hold when the server is recorded: its name, a free slot,
    the firewalls, the addresses, and the volumes once more. They run in
    `record_server`.
    """
    state = lab.state
    name = body.get("name") or ""  # check_hostname refuses an empty name
    check_hostname(name)
    stype = catalogue_name(lab, "server_type", body.get("server_type"))
    image = catalogue_name(lab, "image", body.get("image"))
    location = catalogue_name(lab, "location", body.get("location") or "fsn1")
    keys = []
    for ref in body.get("ssh_keys") or []:
        ssh_key = by_name_or_id(
            state["ssh_keys"].values(), ref, lambda k: (k["name"], k["id"])
        )
        if not ssh_key:
            raise invalid(f"ssh key {ref!r} not found")
        keys.append(ssh_key["public_key"])
    check_user_data(body.get("user_data"))
    firewall_ids = [
        ref_id((f or {}).get("firewall"), "firewalls")
        for f in body.get("firewalls") or []
    ]
    volume_refs = body.get("volumes") or []
    if len({str(v) for v in volume_refs}) != len(volume_refs):
        raise invalid("a volume is listed twice", "volumes")
    given_volumes = []
    for vid in volume_refs:
        volume = state["volumes"].get(ref_id(vid, "volumes"))
        if not volume:
            raise invalid(f"volume {vid} not found", "volumes")
        if volume["status"] != "available" or volume.get("_busy"):
            raise locked(f"volume {vid} is busy")
        given_volumes.append(volume)
    if len(given_volumes) > volumes.VOLUMES_PER_SERVER:
        raise limit_exceeded(
            f"at most {volumes.VOLUMES_PER_SERVER} volumes per server",
            "volumes_per_server",
        )
    return ServerRequest(
        name=name,
        server_type=stype,
        image=image,
        location=location,
        public_keys=keys,
        public_net=body.get("public_net") or {},
        user_data=body.get("user_data"),
        labels=body.get("labels") or {},
        firewall_ids=firewall_ids,
        volumes=given_volumes,
        network_ids=[ref_id(n, "networks") for n in body.get("networks") or []],
        automount=body.get("automount", False),
        start=bool(body.get("start_after_create", True)),
    )


def record_server(
    lab: Lab, request: ServerRequest, local_addresses: set[str], held: HeldFlags
) -> Server:
    """Record the new server with its addresses, volumes and firewalls; save.

    It is one transaction: if a check fails, everything is undone (the addresses taken
    so far included), and nothing is saved. `local_addresses` are the container's own
    addresses, which no NIC gets. When this returns, the server's `_locked` flag is
    claimed through `held`.
    """
    state = lab.state
    with state.change():
        check_unique_name(state["servers"].values(), request.name, None, "server")
        index = state.free_index()
        if not index:
            raise limit_exceeded("the lab runs at most 239 servers", "servers")
        for fid in request.firewall_ids:
            if fid not in state["firewalls"]:
                raise invalid(f"firewall {fid} not found", "firewalls")
        server = new_server(lab, request, state.new_id("servers"), index)
        for volume in request.volumes:
            # check again: a volume action may have started since the first check,
            # and it must not run while the attachment is recorded
            if volume["status"] != "available" or volume.get("_busy"):
                raise locked(f"volume {volume['id']} is busy")
            volumes.check_attach(lab, volume, server)
        for nid in request.network_ids:  # only the new server changes here
            nics.add_nic(lab, server, nid, None, local_addresses)
        # the lab may run out of addresses; the block then undoes the NICs and the
        # addresses taken so far (they are only in the state)
        primary_ips.setup_server_ips(lab, server, request.public_net)
        # from here on nothing fails
        for volume in request.volumes:
            volumes.record_attachment(lab, volume, server, request.automount)
        for fid in request.firewall_ids:
            state["firewalls"][fid]["applied_to"].append(
                {"type": "server", "server": {"id": server["id"]}}
            )
        # without keys Hetzner sets a root password, shown only once
        if not request.public_keys:
            server["_lab"]["root_password"] = secrets.token_urlsafe(15)
        state["servers"][server["id"]] = server
        held.claim(server, "_locked", SERVER_LOCKED)  # new, so still free
    return server


def new_server(lab: Lab, request: ServerRequest, sid: int, index: int) -> Server:
    """Return the record of a new server, not yet in the state."""
    return {
        "id": sid,
        "name": request.name,
        "status": "initializing",
        "created": now(),
        "labels": request.labels,
        "server_type": request.server_type,
        "image": request.image,
        "location": request.location,
        "protection": {"delete": False, "rebuild": False},
        "user_data": request.user_data,
        "public_keys": request.public_keys,
        "_lab": {
            "index": index,
            "tap": f"pub{index}",
            "nics": [],
            "disk_gb": lab.cfg["server_types"][request.server_type]["disk_gb"],
            "target": "running" if request.start else "off",
        },
    }


def create_steps(
    lab: Lab, server: Server, request: ServerRequest, next_actions: list[Action]
) -> Steps:
    """Return the background steps of a create action: disk, then start or off."""

    async def steps():
        await system.apply_firewall(lab)
        base = image_path(lab, request.image)
        disk_gb = server["_lab"]["disk_gb"]
        await lab.blocking(lab.fabric.vms.create_disk, server["id"], base, disk_gb)
        if request.start:
            lab.set_status(server, "starting")
            await system.start_vm(lab, server)
            await lab.sleep(CREATE_START_SECONDS)
            lab.set_status(server, "running")
        else:
            lab.set_status(server, "off")
        for next_action in next_actions:
            lab.finish(next_action)

    return steps


async def update_server(lab: Lab, sid: int, body: dict) -> dict:
    """Change a server's name or labels; new labels update the firewalls."""
    server = lab.server_or_404(sid)
    if "name" in body:
        check_hostname(body["name"])
    update_resource(lab, "servers", server, body, ("name", "labels"), "server")
    if "labels" in body:  # label selectors of firewalls may match differently now
        await system.apply_firewall(lab)
    return {"server": hetzner.server_json(lab, server)}


def delete_server(lab: Lab, sid: int) -> dict:
    """Start deleting a server unless it is delete protected; return the action.

    The server is locked until its VM and records are gone.
    """
    server = lab.server_or_404(sid)
    if server["protection"]["delete"]:
        raise protected("server is delete protected")

    async def steps():
        await remove_server(lab, server)
        await lab.sleep(DELETE_SECONDS)

    with lab.holding() as held:
        held.claim(server, "_locked", SERVER_LOCKED)
        previous_status = server["status"]
        try:
            action = lab.new_action("delete_server", [(sid, "server")])
            # if the lab restarts, it finishes this delete (status deleting)
            lab.set_status(server, "deleting")
            lab.run_action(action, steps, server, holding=held)
        except BaseException:  # nothing runs: restore the status; the flag comes off
            lab.set_status(server, previous_status)
            raise
    return {"action": action}


async def ensure_disk(lab: Lab, server: Server) -> None:
    """Create the server's disk from its image if it is missing.

    A lab restart can cut the creation of a server short and leave it without a disk.
    """
    if not await lab.blocking(lab.fabric.vms.has_disk, server["id"]):
        base = image_path(lab, server["image"])
        disk_gb = server["_lab"]["disk_gb"]
        await lab.blocking(lab.fabric.vms.create_disk, server["id"], base, disk_gb)


async def remove_server(lab: Lab, server: Server) -> None:
    """Remove a server: its VM, its addresses and every reference to it.

    Its volumes stay, detached.
    """
    state, sid = lab.state, server["id"]
    await system.delete_vm(lab, server)
    unrouted = []
    with state.change():
        for floating_ip in state["floating_ips"].values():
            if floating_ip.get("server") == sid:
                floating_ip["server"] = None
                unrouted.append(floating_ip["ip"])
        for firewall in state["firewalls"].values():
            firewall["applied_to"] = [
                x
                for x in firewall["applied_to"]
                if x.get("server", {}).get("id") != sid
            ]
        primary_ips.release_server_ips(lab, server)
        for volume in state["volumes"].values():
            if volume.get("server") == sid:
                volume["server"], volume["automount"] = None, False
        drop_faults(
            lab,
            {"server": sid},
            [server["_lab"]["tap"]] + [nic["tap"] for nic in server["_lab"]["nics"]],
        )
        state["servers"].pop(sid, None)
    for ip in unrouted:
        await system.route_floating_ip(lab, ip, None)
    await system.write_dhcp(lab)
    await system.apply_faults(lab)
    await system.apply_firewall(lab)


# --- server actions -------------------------------------------------------------------
# Each action below checks the request and returns its plan (lab.Plan). The steps run
# in the background while the server is locked.


async def change_protection(lab: Lab, server: Server, body: dict) -> Plan:
    """Set delete and rebuild protection (Hetzner: both go together)."""
    given = {bool(body[k]) for k in ("delete", "rebuild") if k in body}
    if len(given) != 1:
        raise invalid("delete and rebuild need the same value", "delete")
    with lab.state.change():
        server["protection"]["delete"] = server["protection"]["rebuild"] = given.pop()
    return Plan()


async def change_dns_ptr(lab: Lab, server: Server, body: dict) -> Plan:
    """Set the reverse DNS of one of the server's addresses."""
    primary_ips.server_dns_ptr(lab, server, body)
    return Plan()


async def poweroff(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a hard power off: kill the VM at once; the status changes later."""

    async def steps():
        await system.kill_vm(lab, server)
        lab.set_status(server, "stopping")
        await lab.sleep(POWEROFF_SECONDS)
        lab.set_status(server, "off")
        await lab.sleep(POWEROFF_END_SECONDS)

    return Plan(steps, server=server)


async def shutdown(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a shutdown: press the ACPI power button.

    As on Hetzner, the action is finished once the button is pressed. The server's
    status becomes off when the guest has shut down: the VM watcher
    (`watcher.watch_vms`) sees the VM end. A guest that ignores the button keeps
    running.
    """

    async def steps():
        await lab.blocking(lab.fabric.vms.press_power_button, server["id"])
        await lab.sleep(SHUTDOWN_SECONDS)

    return Plan(steps, server=server)


async def poweron(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a power on; a running server stays as it is."""

    async def steps():
        if server["status"] == "running" and await lab.blocking(
            lab.fabric.vms.is_alive, server["id"]
        ):
            return  # already on: the action succeeds, nothing changes
        lab.set_status(server, "starting")
        if server.get("_dead"):  # the fault "dead": broken hardware, the VM stays off
            await lab.sleep(POWERON_SECONDS)
            lab.set_status(server, "off")
            return
        await ensure_disk(lab, server)
        await system.start_vm(lab, server)
        await lab.sleep(POWERON_SECONDS)
        lab.set_status(server, "running")
        await lab.sleep(POWERON_END_SECONDS)

    return Plan(steps, server=server)


def restart_vm(lab: Lab, server: Server, press: Callable[[int], None]) -> Plan:
    """Plan a reboot or reset: `press(sid)` restarts a running VM.

    A server that is not running stays as it is.
    """

    async def steps():
        if server["status"] != "running":
            return
        await lab.blocking(press, server["id"])
        lab.set_status(server, "starting")
        await lab.sleep(RESTART_SECONDS)
        lab.set_status(server, "running")
        await lab.sleep(RESTART_END_SECONDS)

    return Plan(steps, server=server)


async def reboot(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a soft reboot: the guest reboots itself after ctrl-alt-delete.

    This has the same effect as Hetzner's ACPI reboot.
    """
    return restart_vm(lab, server, lab.fabric.vms.send_ctrl_alt_delete)


async def reset(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a hard reset, like pressing the VM's reset button."""
    return restart_vm(lab, server, lab.fabric.vms.reset)


async def rebuild(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a rebuild: a fresh disk from an image.

    New user data is used if given; otherwise the old user data stays. A server
    without SSH keys gets a new root password.
    """
    if server["protection"]["rebuild"]:
        raise protected("server is rebuild protected")
    image = catalogue_name(lab, "image", body.get("image"))
    check_user_data(body.get("user_data"))
    # made while planning, because Hetzner's answer carries it at once; stored only
    # when the steps start, so a refused or failed act() keeps the old one
    password = None if server["public_keys"] else secrets.token_urlsafe(15)

    async def steps():
        # the server is locked, so it is running or off; it ends as it was
        target = "running" if server["status"] == "running" else "off"
        with lab.state.change():  # a lab restart reads the target to finish
            if password is not None:
                server["_lab"]["root_password"] = password
            server["_lab"]["target"] = target
        base = image_path(lab, image)  # before the old disk is gone
        await system.delete_vm(lab, server)
        with lab.state.change():
            server["image"] = image
            server["status"] = "rebuilding"
        disk_gb = server["_lab"]["disk_gb"]
        await lab.blocking(lab.fabric.vms.create_disk, server["id"], base, disk_gb)
        if "user_data" in body:
            with lab.state.change():
                server["user_data"] = body["user_data"]
        if target == "running":
            await system.start_vm(lab, server)
        await lab.sleep(REBUILD_SECONDS)
        lab.set_status(server, target)

    return Plan(
        steps,
        extra={"root_password": password or server["_lab"].get("root_password")},
        server=server,
    )


async def change_type(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a change of the server type; only while the server is off.

    The disk grows only with upgrade_disk. As on Hetzner, a server with a grown disk
    cannot change back to a type with a smaller disk.
    """
    if server["status"] != "off":
        raise server_not_stopped()
    stype = catalogue_name(lab, "server_type", body.get("server_type"))
    disk = server["_lab"]["disk_gb"]
    new_disk = lab.cfg["server_types"][stype]["disk_gb"]
    if new_disk < disk:
        raise ApiError(
            422,
            "invalid_server_type",
            "the server type's disk is smaller than the server's",
        )
    grow = bool(body.get("upgrade_disk")) and new_disk > disk

    async def steps():
        if grow:
            await lab.blocking(lab.fabric.vms.resize_disk, server["id"], new_disk)
        with lab.state.change():
            if grow:
                server["_lab"]["disk_gb"] = new_disk
            server["server_type"] = stype
        await lab.sleep(CHANGE_TYPE_SECONDS)

    return Plan(steps, server=server)


async def reset_password(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a root password reset.

    As on Hetzner, the password is set in the guest through the QEMU guest agent.
    """
    if not await lab.blocking(lab.fabric.vms.is_alive, server["id"]):
        raise ApiError(409, "server_not_running", "the server is not running")
    password = secrets.token_urlsafe(15)

    async def steps():
        cmd = {
            "execute": "guest-set-user-password",
            "arguments": {
                "username": "root",
                "password": base64.b64encode(password.encode()).decode(),
                "crypted": False,
            },
        }
        out = await lab.blocking(lab.fabric.vms.qga, server["id"], cmd)
        if out is None or "error" in out:
            raise RuntimeError(
                "the QEMU guest agent did not answer (qemu-guest-agent not running?)"
            )

    return Plan(steps, extra={"root_password": password}, server=server)


# the server actions: Hetzner's name of the action and the function that plans it
SERVER_ACTIONS: ActionTable[Server] = {
    "change_protection": ActionSpec("change_protection", change_protection, flag=False),
    "change_dns_ptr": ActionSpec("change_dns_ptr", change_dns_ptr, flag=False),
    "poweroff": ActionSpec("stop_server", poweroff),
    "shutdown": ActionSpec("shutdown_server", shutdown),
    "poweron": ActionSpec("start_server", poweron),
    "reboot": ActionSpec("reboot_server", reboot),
    "reset": ActionSpec("reset_server", reset),
    "rebuild": ActionSpec("rebuild_server", rebuild),
    "change_type": ActionSpec("change_server_type", change_type),
    "reset_password": ActionSpec("reset_password", reset_password),
    "attach_to_network": ActionSpec("attach_to_network", nics.attach_to_network),
    "detach_from_network": ActionSpec("detach_from_network", nics.detach_from_network),
    "change_alias_ips": ActionSpec("change_alias_ips", nics.change_alias_ips),
}


async def run_server_action(lab: Lab, sid: int, cmd: str, body: dict) -> dict:
    """Run a server action; as on Hetzner, one at a time per server (else 423)."""
    server = lab.server_or_404(sid)
    return await lab.act(
        SERVER_ACTIONS, cmd, server, "server", body, "_locked", SERVER_LOCKED
    )


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the server routes to the app."""
    state = lab.state

    @app.get("/v1/servers")
    async def server_list(request: Request):
        items = filtered(
            request,
            list(state["servers"].values()),
            {"status": lambda server, value: server["status"] in value.split(",")},
        )
        return paginate(
            request, [hetzner.server_json(lab, s) for s in items], "servers"
        )

    @app.post("/v1/servers", status_code=201)
    async def server_create(request: Request):
        return await create_server(lab, await read_body(request))

    @app.get("/v1/servers/{sid}")
    async def server_get(sid: int):
        return {"server": hetzner.server_json(lab, lab.server_or_404(sid))}

    @app.put("/v1/servers/{sid}")
    async def server_update(sid: int, request: Request):
        return await update_server(lab, sid, await read_body(request))

    @app.delete("/v1/servers/{sid}")
    async def server_delete(sid: int):
        return delete_server(lab, sid)

    @app.post("/v1/servers/{sid}/actions/{cmd}", status_code=201)
    async def server_action(sid: int, cmd: str, request: Request):
        return await run_server_action(lab, sid, cmd, await read_body(request))
