"""Servers and SSH keys.

Create, delete, power and the other server actions, with the cloud's timings (the
table in lab.py).
"""

import base64
import hashlib
import secrets

from fastapi import FastAPI, Request
from fastapi.responses import Response

from ..errors import (
    ApiError,
    check_unique_name,
    invalid,
    limit_exceeded,
    locked,
    not_unique,
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
    Lab,
    Plan,
    now,
)
from ..records import Server, SSHKey
from . import hetzner, ips, networks, volumes
from .catalogue import by_name_or_id, catalogue_name, image_path
from .faults import drop_faults
from .spec import hostname_ok
from .web import filtered, paginate, read_body, ref_id, update_resource

USER_DATA_LIMIT = 32 * 1024


def check_user_data(user_data) -> None:
    """Check that user data is a string of at most 32 KiB, if given."""
    if user_data is not None and not isinstance(user_data, str):
        raise invalid("user_data is a string", "user_data")
    if user_data is not None and len(user_data.encode()) > USER_DATA_LIMIT:
        raise invalid("user_data is limited to 32 KiB", "user_data")


def check_hostname(name) -> None:
    """Check that a server name is a valid host name (RFC 1123)."""
    if not name or not hostname_ok(name):
        raise invalid("must be a valid hostname (RFC 1123)", "name")


# --- create and delete ----------------------------------------------------------------


def create_server(lab: Lab, body: dict) -> dict:
    """Create a server and start its create action.

    Everything is checked before anything changes, so a refused request leaves nothing
    behind. The create action makes the VM in the background. The lab's own fields of
    a server (`server["_lab"]`) and of its NICs are in records.py.
    """
    state, cfg = lab.state, lab.cfg
    name = body.get("name") or ""  # check_hostname refuses an empty name
    check_hostname(name)
    stype = catalogue_name(lab, "server_type", body.get("server_type"))
    image = catalogue_name(lab, "image", body.get("image"))
    location = catalogue_name(lab, "location", body.get("location") or "fsn1")
    keys = []
    for ref in body.get("ssh_keys") or []:
        with state.lock:
            ssh_key = by_name_or_id(
                state["ssh_keys"].values(), ref, lambda k: (k["name"], k["id"])
            )
        if not ssh_key:
            raise invalid(f"ssh key {ref!r} not found")
        keys.append(ssh_key["public_key"])
    public_net = body.get("public_net") or {}
    check_user_data(body.get("user_data"))
    firewalls = [
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
    network_ids = [ref_id(n, "networks") for n in body.get("networks") or []]
    start = bool(body.get("start_after_create", True))
    with state.lock:
        check_unique_name(state["servers"].values(), name, None, "server")
        index = state.free_index()
        if not index:
            raise limit_exceeded("the lab runs at most 239 servers", "servers")
        for fid in firewalls:
            if fid not in state["firewalls"]:
                raise invalid(f"firewall {fid} not found", "firewalls")
        sid = state.new_id("servers")
        server: Server = {
            "id": sid,
            "name": name,
            "status": "initializing",
            "created": now(),
            "labels": body.get("labels") or {},
            "server_type": stype,
            "image": image,
            "location": location,
            "protection": {"delete": False, "rebuild": False},
            "locked": True,  # its create action runs
            "user_data": body.get("user_data"),
            "public_keys": keys,
            "_lab": {
                "index": index,
                "tap": f"pub{index}",
                "nics": [],
                "disk_gb": cfg["server_types"][stype]["disk_gb"],
                "target": "running" if start else "off",
            },
        }
        for volume in given_volumes:
            # again under the lock: a volume action may have started since the check
            # above, and it may not run beside the attachment recorded below
            if volume["status"] != "available" or volume.get("_busy"):
                raise locked(f"volume {volume['id']} is busy")
            volumes.check_attach(lab, volume, server)
        for nid in network_ids:  # only the new server changes here
            networks.add_nic(lab, server, nid, None)
        try:  # the lab may run out of addresses
            ips.setup_server_ips(lab, server, public_net)
        except Exception:  # whatever failed: no address stays taken, then re-raise
            ips.release_server_ips(lab, server)
            raise
        # from here on nothing fails
        for volume in given_volumes:
            volumes.record_attachment(volume, server, body.get("automount", False))
        for fid in firewalls:
            state["firewalls"][fid]["applied_to"].append(
                {"type": "server", "server": {"id": sid}}
            )
        if not keys:  # Hetzner sets a root password then and returns it once
            server["_lab"]["root_password"] = secrets.token_urlsafe(15)
        state["servers"][sid] = server
        state.save()
    action = lab.new_action("create_server", [(sid, "server")])
    next_actions = [lab.new_action("start_server", [(sid, "server")])] if start else []

    async def steps():
        await lab.blocking(lab.fabric.apply_firewalls)
        base = await lab.blocking(image_path, lab, image)
        await lab.blocking(lab.fabric.create_vm, server, base)
        if start:
            lab.set_status(server, "starting")
            await lab.blocking(lab.fabric.start_vm, server)
            await lab.sleep(CREATE_START_SECONDS)
            lab.set_status(server, "running")
        else:
            lab.set_status(server, "off")
        for next_action in next_actions:
            lab.finish(next_action)

    lab.run_action(action, steps, server, next_actions, holding=[(server, "locked")])
    return {
        "server": hetzner.server_json(lab, server),
        "action": action,
        "next_actions": next_actions,
        "root_password": server["_lab"].get("root_password"),
    }


def update_server(lab: Lab, sid: int, body: dict) -> dict:
    """Change a server's name or labels; firewalls follow new labels."""
    server = lab.server_or_404(sid)
    if "name" in body:
        check_hostname(body["name"])
    update_resource(lab, "servers", server, body, ("name", "labels"), "server")
    if "labels" in body:  # label selectors of firewalls may match differently now
        lab.fabric.apply_firewalls()
    return {"server": hetzner.server_json(lab, server)}


def delete_server(lab: Lab, sid: int) -> dict:
    """Start deleting a server unless it is delete protected; return the action.

    The server is locked until its VM and records are gone.
    """
    server = lab.server_or_404(sid)
    if server["protection"]["delete"]:
        raise protected("server is delete protected")
    lab.claim(server, "locked", SERVER_LOCKED)
    previous_status = server["status"]

    async def steps():
        await lab.blocking(remove_server, lab, server)
        await lab.sleep(DELETE_SECONDS)

    try:
        action = lab.new_action("delete_server", [(sid, "server")])
        # a restart of the lab finishes what is started here
        lab.set_status(server, "deleting")
        lab.run_action(action, steps, server, holding=[(server, "locked")])
    except BaseException:  # nothing runs: the server is as it was, and free again
        lab.set_status(server, previous_status)
        lab.release(server)
        raise
    return {"action": action}


async def ensure_disk(lab: Lab, server: Server) -> None:
    """Create the server's disk from its image if it is missing.

    A lab restart can cut the creation of a server short and leave it without a disk.
    """
    if not (lab.fabric.vm_dir(server) / "disk.qcow2").is_file():
        base = await lab.blocking(image_path, lab, server["image"])
        await lab.blocking(lab.fabric.create_vm, server, base)


def remove_server(lab: Lab, server: Server) -> None:
    """Remove a server: its VM, its addresses and every reference to it.

    Its volumes stay, detached.
    """
    state, fabric, sid = lab.state, lab.fabric, server["id"]
    fabric.delete_vm(server)
    unrouted = []
    with state.lock:
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
        ips.release_server_ips(lab, server)
        for volume in state["volumes"].values():
            if volume.get("server") == sid:
                volume["server"], volume["automount"] = None, False
        drop_faults(
            lab,
            f"s:{sid}",
            [server["_lab"]["tap"]] + [nic["tap"] for nic in server["_lab"]["nics"]],
        )
        state["servers"].pop(sid, None)
        state.save()
    for ip in unrouted:
        fabric.route_floating_ip(ip, None)
    fabric.write_dhcp()
    fabric.apply_faults()
    fabric.apply_firewalls()


# --- server actions -------------------------------------------------------------------
# Each action below checks the request and returns its plan (lab.Plan). The steps run
# in the background while the server is locked.


def change_protection(lab: Lab, server: Server, body: dict) -> Plan:
    """Set delete and rebuild protection (Hetzner: both go together)."""
    given = {bool(body[k]) for k in ("delete", "rebuild") if k in body}
    if len(given) != 1:
        raise invalid("delete and rebuild need the same value", "delete")
    with lab.state.lock:
        server["protection"]["delete"] = server["protection"]["rebuild"] = given.pop()
    return Plan()


def change_dns_ptr(lab: Lab, server: Server, body: dict) -> Plan:
    """Set the reverse DNS of one of the server's addresses."""
    ips.server_dns_ptr(lab, server, body)
    return Plan()


def poweroff(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a hard power off: the VM dies at once, the status follows."""

    async def steps():
        await lab.blocking(lab.fabric.kill_vm, server)
        lab.set_status(server, "stopping")
        await lab.sleep(POWEROFF_SECONDS)
        lab.set_status(server, "off")
        await lab.sleep(POWEROFF_END_SECONDS)

    return Plan(steps, server=server)


def shutdown(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a shutdown: press the ACPI power button.

    The action is done once the button is pressed (as on Hetzner). The server reports
    off when its guest is down (the VM watcher sees it end, `lab.watch_vms`); a guest
    that ignores the button keeps running.
    """

    async def steps():
        await lab.blocking(lab.fabric.press_power_button, server)
        await lab.sleep(SHUTDOWN_SECONDS)

    return Plan(steps, server=server)


def poweron(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a power on; a running server stays as it is."""

    async def steps():
        if server["status"] == "running" and await lab.blocking(
            lab.fabric.vm_alive, server
        ):
            return  # already on: the action succeeds, nothing changes
        lab.set_status(server, "starting")
        if server.get("_dead"):  # the fault "dead": broken hardware, the VM stays off
            await lab.sleep(POWERON_SECONDS)
            lab.set_status(server, "off")
            return
        await ensure_disk(lab, server)
        await lab.blocking(lab.fabric.start_vm, server)
        await lab.sleep(POWERON_SECONDS)
        lab.set_status(server, "running")
        await lab.sleep(POWERON_END_SECONDS)

    return Plan(steps, server=server)


def restart_vm(lab: Lab, server: Server, press) -> Plan:
    """Plan a reboot or reset: `press(server)` restarts a running VM.

    A server that is not running stays as it is.
    """

    async def steps():
        if server["status"] != "running":
            return
        await lab.blocking(press, server)
        lab.set_status(server, "starting")
        await lab.sleep(RESTART_SECONDS)
        lab.set_status(server, "running")
        await lab.sleep(RESTART_END_SECONDS)

    return Plan(steps, server=server)


def reboot(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a soft reboot: the guest reboots itself after ctrl-alt-delete.

    That is what Hetzner's ACPI reboot comes to.
    """
    return restart_vm(lab, server, lab.fabric.send_ctrl_alt_delete)


def reset(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a hard reset, like pressing the VM's reset button."""
    return restart_vm(lab, server, lab.fabric.reset_vm)


def rebuild(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a rebuild: a fresh disk from an image.

    New user data is used if given (else the old stays). A server without SSH keys
    gets a new root password.
    """
    if server["protection"]["rebuild"]:
        raise protected("server is rebuild protected")
    image = catalogue_name(lab, "image", body.get("image"))
    check_user_data(body.get("user_data"))
    if not server["public_keys"]:
        with lab.state.lock:
            server["_lab"]["root_password"] = secrets.token_urlsafe(15)

    async def steps():
        previous_status = server["status"]
        with lab.state.lock:  # for a lab restart
            target = "running" if previous_status == "running" else "off"
            server["_lab"]["target"] = target
        base = await lab.blocking(image_path, lab, image)  # before the old disk is gone
        await lab.blocking(lab.fabric.delete_vm, server)
        with lab.state.lock:
            server["image"] = image
            lab.set_status(server, "rebuilding")
        await lab.blocking(lab.fabric.create_vm, server, base)
        if "user_data" in body:
            with lab.state.lock:
                server["user_data"] = body["user_data"]
        if previous_status == "running":
            await lab.blocking(lab.fabric.start_vm, server)
        await lab.sleep(REBUILD_SECONDS)
        lab.set_status(
            server,
            previous_status if previous_status in ("running", "off") else "running",
        )

    return Plan(
        steps,
        extra={"root_password": server["_lab"].get("root_password")},
        server=server,
    )


def change_type(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a change of the server type; only while the server is off.

    The disk grows with upgrade_disk. A grown disk cannot go back to a smaller type
    (as on Hetzner).
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
    upgrade = bool(body.get("upgrade_disk"))

    async def steps():
        if upgrade and new_disk > disk:
            await lab.blocking(lab.fabric.resize_disk, server, new_disk)
            with lab.state.lock:
                server["_lab"]["disk_gb"] = new_disk
        with lab.state.lock:
            server["server_type"] = stype
            lab.state.save()
        await lab.sleep(CHANGE_TYPE_SECONDS)

    return Plan(steps, server=server)


def reset_password(lab: Lab, server: Server, body: dict) -> Plan:
    """Plan a root password reset.

    The password is set in the guest through the QEMU guest agent, as on Hetzner.
    """
    if not lab.fabric.vm_alive(server):
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
        out = await lab.blocking(lab.fabric.qga, server, cmd)
        if out is None or "error" in out:
            raise RuntimeError(
                "the QEMU guest agent did not answer (qemu-guest-agent not running?)"
            )

    return Plan(steps, extra={"root_password": password}, server=server)


# the server actions: Hetzner's name of the action and the function that plans it
SERVER_ACTIONS = {
    "change_protection": ("change_protection", change_protection),
    "change_dns_ptr": ("change_dns_ptr", change_dns_ptr),
    "poweroff": ("stop_server", poweroff),
    "shutdown": ("shutdown_server", shutdown),
    "poweron": ("start_server", poweron),
    "reboot": ("reboot_server", reboot),
    "reset": ("reset_server", reset),
    "rebuild": ("rebuild_server", rebuild),
    "change_type": ("change_server_type", change_type),
    "reset_password": ("reset_password", reset_password),
    "attach_to_network": ("attach_to_network", networks.attach_to_network),
    "detach_from_network": ("detach_from_network", networks.detach_from_network),
    "change_alias_ips": ("change_alias_ips", networks.change_alias_ips),
}


def run_server_action(lab: Lab, sid: int, cmd: str, body: dict) -> dict:
    """Run a server action: one at a time per server (423 otherwise), as on Hetzner."""
    server = lab.server_or_404(sid)
    return lab.act(SERVER_ACTIONS, cmd, server, "server", body, "locked", SERVER_LOCKED)


# --- SSH keys -------------------------------------------------------------------------


def ssh_fingerprint(public_key: str) -> str:
    """Return the MD5 fingerprint of an OpenSSH public key.

    An invalid key raises ValueError: its blob does not name its type, or it has
    leading whitespace (Hetzner refuses that since 2026-04).
    """
    parts = public_key.split()
    if len(parts) < 2 or public_key != public_key.lstrip():
        raise ValueError("invalid public key")
    blob = base64.b64decode(parts[1], validate=True)
    length = int.from_bytes(blob[:4], "big")
    if len(blob) < 4 + length or blob[4 : 4 + length] != parts[0].encode():
        raise ValueError("invalid public key")
    digest = hashlib.md5(blob).hexdigest()
    return ":".join(digest[i : i + 2] for i in range(0, 32, 2))


def create_ssh_key(lab: Lab, body: dict) -> dict:
    """Create an SSH key; names and fingerprints are unique (Hetzner)."""
    state = lab.state
    if not body.get("name") or not body.get("public_key"):
        raise invalid("name and public_key are required")
    try:
        fingerprint = ssh_fingerprint(body["public_key"])
    # binascii.Error, ValueError, ...: whatever a broken key raises, it is invalid
    except Exception:  # noqa: BLE001
        raise invalid("invalid public key") from None
    with state.lock:
        ssh_keys = state["ssh_keys"].values()
        check_unique_name(
            ssh_keys, body["name"], message="SSH key with the same name already exists"
        )
        if any(k["fingerprint"] == fingerprint for k in ssh_keys):
            raise not_unique(
                "SSH key with the same fingerprint already exists", "public_key"
            )
        kid = state.new_id("ssh_keys")
        ssh_key: SSHKey = {
            "id": kid,
            "name": body["name"],
            "fingerprint": fingerprint,
            "public_key": body["public_key"].strip(),
            "labels": body.get("labels") or {},
            "created": now(),
        }
        state["ssh_keys"][kid] = ssh_key
        state.save()
    return {"ssh_key": hetzner.ssh_key_json(lab, ssh_key)}


def update_ssh_key(lab: Lab, kid: int, body: dict) -> dict:
    """Change an SSH key's name or labels."""
    ssh_key = lab.get_or_404("ssh_keys", kid, "SSH key")
    update_resource(lab, "ssh_keys", ssh_key, body, ("name", "labels"), "SSH key")
    return {"ssh_key": hetzner.ssh_key_json(lab, ssh_key)}


def delete_ssh_key(lab: Lab, kid: int) -> None:
    """Delete an SSH key (servers keep the keys they were made with)."""
    with lab.state.lock:
        lab.get_or_404("ssh_keys", kid, "SSH key")
        del lab.state["ssh_keys"][kid]
        lab.state.save()


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the server and SSH key routes to the app."""
    state = lab.state

    @app.get("/v1/servers")
    def server_list(request: Request):
        with state.lock:
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
        return await lab.blocking(create_server, lab, await read_body(request))

    @app.get("/v1/servers/{sid}")
    def server_get(sid: int):
        return {"server": hetzner.server_json(lab, lab.server_or_404(sid))}

    @app.put("/v1/servers/{sid}")
    async def server_update(sid: int, request: Request):
        return await lab.blocking(update_server, lab, sid, await read_body(request))

    @app.delete("/v1/servers/{sid}")
    def server_delete(sid: int):
        return delete_server(lab, sid)

    @app.post("/v1/servers/{sid}/actions/{cmd}", status_code=201)
    async def server_action(sid: int, cmd: str, request: Request):
        return await lab.blocking(
            run_server_action, lab, sid, cmd, await read_body(request)
        )

    @app.get("/v1/ssh_keys")
    def ssh_key_list(request: Request):
        with state.lock:
            items = filtered(
                request,
                list(state["ssh_keys"].values()),
                {"fingerprint": lambda ssh_key, value: ssh_key["fingerprint"] == value},
            )
            return paginate(
                request, [hetzner.ssh_key_json(lab, k) for k in items], "ssh_keys"
            )

    @app.post("/v1/ssh_keys", status_code=201)
    async def ssh_key_create(request: Request):
        return await lab.blocking(create_ssh_key, lab, await read_body(request))

    @app.get("/v1/ssh_keys/{kid}")
    def ssh_key_get(kid: int):
        ssh_key = lab.get_or_404("ssh_keys", kid, "SSH key")
        return {"ssh_key": hetzner.ssh_key_json(lab, ssh_key)}

    @app.put("/v1/ssh_keys/{kid}")
    async def ssh_key_update(kid: int, request: Request):
        return await lab.blocking(update_ssh_key, lab, kid, await read_body(request))

    @app.delete("/v1/ssh_keys/{kid}", status_code=204)
    def ssh_key_delete(kid: int):
        delete_ssh_key(lab, kid)
        return Response(status_code=204)
