"""Servers and SSH keys.

Create, delete, the power and other server actions, with the cloud's timings (power
off reports off after ~9 s, reset ~15 s).
"""

import base64
import secrets

from fastapi import FastAPI, Request
from fastapi.responses import Response

from ..errors import (
    ApiError,
    invalid,
    limit_exceeded,
    locked,
    not_found,
    unique_name,
    unsupported,
)
from ..lab import now
from . import hetzner, ips, networks, volumes
from .catalogue import image_name, image_path, resolve
from .spec import hostname_ok
from .web import filtered, paginate, read_body

USER_DATA_LIMIT = 32 * 1024


def ref_id(value, field: str) -> int:
    """Return a resource ID from a request body, or raise invalid_input."""
    try:
        return int(value)
    except (TypeError, ValueError):
        raise invalid(f"{value!r} is not an ID", field) from None


def check_user_data(user_data) -> None:
    """Check that user data is a string of at most 32 KiB, if given."""
    if user_data is not None and not isinstance(user_data, str):
        raise invalid("user_data is a string", "user_data")
    if user_data is not None and len(user_data.encode()) > USER_DATA_LIMIT:
        raise invalid("user_data is limited to 32 KiB", "user_data")


# --- create and delete ----------------------------------------------------------------


def create_server(lab, body: dict) -> dict:
    """Create a server and start its create action.

    Everything is checked before anything changes (a refused request leaves nothing
    behind); the VM is made by the create action in the background.
    """
    state = lab.state
    name = body.get("name")
    if not name or not hostname_ok(name):
        raise invalid("must be a valid hostname (RFC 1123)", "name")
    stype = resolve(lab, "server_type", body.get("server_type"))
    image = image_name(lab, body.get("image"))
    location = resolve(lab, "location", body.get("location") or "fsn1")
    keys = []
    for ref in body.get("ssh_keys") or []:
        ssh_key = next(
            (
                k
                for k in state["ssh_keys"].values()
                if str(ref) in (k["name"], str(k["id"]))
            ),
            None,
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
    given_volumes = []
    if len({str(v) for v in body.get("volumes") or []}) != len(
        body.get("volumes") or []
    ):
        raise invalid("a volume is listed twice", "volumes")
    for vid in body.get("volumes") or []:
        volume = state["volumes"].get(ref_id(vid, "volumes"))
        if not volume:
            raise invalid(f"volume {vid} not found", "volumes")
        if volume["status"] != "available" or volume.get("_busy"):
            raise locked(f"volume {vid} is busy")
        given_volumes.append(volume)
    if len(given_volumes) > volumes.PER_SERVER:
        raise limit_exceeded(
            f"at most {volumes.PER_SERVER} volumes per server", "volumes_per_server"
        )
    network_ids = [ref_id(n, "networks") for n in body.get("networks") or []]
    start = bool(body.get("start_after_create", True))
    with state.lock:
        unique_name(state["servers"].values(), name, None, "server")
        index = state.free_index()
        if not index:
            raise limit_exceeded("the lab runs at most 239 servers", "servers")
        for fid in firewalls:
            if fid not in state["firewalls"]:
                raise invalid(f"firewall {fid} not found", "firewalls")
        sid = state.new_id("servers")
        server = {
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
            "_lab": {"index": index, "tap": f"pub{index}", "nics": [], "start": start},
        }
        for volume in given_volumes:
            volumes.check_attach(lab, volume, server)
        for nid in network_ids:  # only the new server changes here
            networks.add_nic(lab, server, nid, None)
        ips.check_server_ips(lab, server, public_net)
        try:  # the lab may run out of addresses
            ips.setup_server_ips(lab, server, public_net)
        except Exception:
            ips.release_server_ips(lab, server)
            raise
        # from here on nothing fails
        for volume in given_volumes:
            volumes.attach(lab, volume, server, body.get("automount", False))
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
            await lab.sleep(5)
            lab.set_status(server, "running")
        else:
            lab.set_status(server, "off")
        for n in next_actions:
            lab.finish(n)

    lab.run_action(action, steps, server, next_actions)
    return {
        "server": hetzner.server_json(lab.cfg, state, server),
        "action": action,
        "next_actions": next_actions,
        "root_password": server["_lab"].get("root_password"),
    }


def delete_server(lab, sid: int) -> dict:
    """Start deleting a server unless it is delete protected; return the action."""
    server = lab.server(sid)
    if server["protection"]["delete"]:
        raise ApiError(423, "protected", "server is delete protected")
    lab.reserve(server)
    action = lab.new_action("delete_server", [(sid, "server")])
    # a restart of the lab finishes what is started here
    lab.set_status(server, "deleting")

    async def steps():
        await lab.blocking(forget_server, lab, server)
        await lab.sleep(2)

    lab.run_action(action, steps, server)
    return {"action": action}


async def ensure_disk(lab, server: dict) -> None:
    """Create the server's disk from its image if it is missing.

    A lab restart can cut the creation of a server short and leave it without a disk.
    """
    if not (lab.fabric.vm_dir(server) / "disk.qcow2").is_file():
        base = await lab.blocking(image_path, lab, server["image"])
        await lab.blocking(lab.fabric.create_vm, server, base)


def forget_server(lab, server: dict) -> None:
    """Remove a server: its VM, its addresses and every reference to it.

    Its volumes stay, detached.
    """
    state, fabric, sid = lab.state, lab.fabric, server["id"]
    fabric.delete_vm(server)
    unrouted = []
    with state.lock:
        for f in state["floating_ips"].values():
            if f.get("server") == sid:
                f["server"] = None
                unrouted.append(f["ip"])
        for fw in state["firewalls"].values():
            fw["applied_to"] = [
                x for x in fw["applied_to"] if x.get("server", {}).get("id") != sid
            ]
        ips.release_server_ips(lab, server)
        for v in state["volumes"].values():
            if v.get("server") == sid:
                v["server"], v["automount"] = None, False
        fabric.drop_faults(
            f"s:{sid}",
            [server["_lab"]["tap"]] + [n["tap"] for n in server["_lab"]["nics"]],
        )
        state["servers"].pop(sid, None)
        state.save()
    for ip in unrouted:
        fabric.route_floating_ip(ip, None)
    fabric.write_dhcp()
    fabric.apply_faults()
    fabric.apply_firewalls()


# --- server actions -------------------------------------------------------------------
# Each action below checks the request and returns (steps, extra answer fields); the
# steps run in the background while the server is locked.


def poweroff(lab, server: dict, body: dict):
    """Check a hard power off and return its steps.

    The VM dies at once, the status follows.
    """

    async def steps():
        await lab.blocking(lab.fabric.kill_vm, server)
        lab.set_status(server, "stopping")
        await lab.sleep(8.7)
        lab.set_status(server, "off")
        await lab.sleep(1.2)

    return steps, {}


def shutdown(lab, server: dict, body: dict):
    """Check a shutdown and return its steps, which press the ACPI power button.

    The action is done once the button is pressed (Hetzner); the server reports off
    when its guest is down, and a guest that ignores the button keeps running.
    """

    async def steps():
        await lab.blocking(lab.fabric.qmp, server, "system_powerdown")
        lab.spawn(lab.off_when_down(server))
        await lab.sleep(1)

    return steps, {}


def poweron(lab, server: dict, body: dict):
    """Check a power on and return its steps; a running server stays as it is."""

    async def steps():
        if server["status"] == "running" and await lab.blocking(
            lab.fabric.vm_alive, server
        ):
            return  # already on: the action succeeds, nothing changes
        lab.set_status(server, "starting")
        if server.get("_dead"):  # the fault "dead": broken hardware, the VM stays off
            await lab.sleep(7)
            lab.set_status(server, "off")
            return
        await ensure_disk(lab, server)
        await lab.blocking(lab.fabric.start_vm, server)
        await lab.sleep(7)
        lab.set_status(server, "running")
        await lab.sleep(1)

    return steps, {}


def reboot_or_reset(soft: bool):
    """Return the action function for a soft reboot or a hard reset."""

    def prepare(lab, server: dict, body: dict):
        """Check a reboot or reset and return its steps.

        Reboot is soft (the guest reboots itself after ctrl-alt-delete, as Hetzner's
        ACPI reboot), reset is hard.
        """

        async def steps():
            if server["status"] != "running":
                return
            if soft:
                await lab.blocking(lab.fabric.hmp, server, "sendkey ctrl-alt-delete")
            else:
                await lab.blocking(lab.fabric.qmp, server, "system_reset")
            lab.set_status(server, "starting")
            await lab.sleep(13)
            lab.set_status(server, "running")
            await lab.sleep(2)

        return steps, {}

    return prepare


def rebuild(lab, server: dict, body: dict):
    """Check a rebuild and return its steps, which put a fresh disk from an image.

    New user data is used if given (else the old stays); a server without SSH keys
    gets a new root password.
    """
    if server["protection"]["rebuild"]:
        raise ApiError(423, "protected", "server is rebuild protected")
    image = image_name(lab, body.get("image"))
    check_user_data(body.get("user_data"))
    if not server["public_keys"]:
        server["_lab"]["root_password"] = secrets.token_urlsafe(15)

    async def steps():
        previous_status = server["status"]
        server["_lab"]["after_rebuild"] = (
            "running" if previous_status == "running" else "off"
        )  # for a lab restart
        base = await lab.blocking(image_path, lab, image)  # before the old disk goes
        await lab.blocking(lab.fabric.delete_vm, server)
        server["image"] = image
        lab.set_status(server, "rebuilding")
        await lab.blocking(lab.fabric.create_vm, server, base)
        if "user_data" in body:
            server["user_data"] = body["user_data"]
        if previous_status == "running":
            await lab.blocking(lab.fabric.start_vm, server)
        await lab.sleep(10)
        lab.set_status(
            server,
            previous_status if previous_status in ("running", "off") else "running",
        )

    return steps, {"root_password": server["_lab"].get("root_password")}


def change_type(lab, server: dict, body: dict):
    """Check a server type change and return its steps; only while the server is off.

    The disk grows with upgrade_disk; a grown disk cannot go back to a smaller type
    (Hetzner).
    """
    if server["status"] != "off":
        raise ApiError(422, "server_not_stopped", "server must be stopped")
    cfg = lab.cfg
    stype = resolve(lab, "server_type", body.get("server_type"))
    disk = (
        server["_lab"].get("disk_gb")
        or cfg["server_types"][server["server_type"]]["disk_gb"]
    )
    new_disk = cfg["server_types"][stype]["disk_gb"]
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
        else:
            server["_lab"]["disk_gb"] = disk
        server["server_type"] = stype
        lab.state.save()
        await lab.sleep(30)

    return steps, {}


def reset_password(lab, server: dict, body: dict):
    """Check a root password reset and return its steps.

    The password is set through the QEMU guest agent in the guest, as on Hetzner.
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

    return steps, {"root_password": password}


# action name on Hetzner (default: the command) and the function that prepares it
SERVER_ACTIONS = {
    "poweroff": ("stop_server", poweroff),
    "shutdown": ("shutdown_server", shutdown),
    "poweron": ("start_server", poweron),
    "reboot": ("reboot_server", reboot_or_reset(soft=True)),
    "reset": ("reset_server", reboot_or_reset(soft=False)),
    "rebuild": ("rebuild_server", rebuild),
    "change_type": ("change_server_type", change_type),
    "reset_password": ("reset_password", reset_password),
    "attach_to_network": ("attach_to_network", networks.attach_to_network),
    "detach_from_network": ("detach_from_network", networks.detach_from_network),
    "change_alias_ips": ("change_alias_ips", networks.change_alias_ips),
}


def ssh_key_or_404(lab, kid: int) -> dict:
    """Return an SSH key, or raise not_found."""
    ssh_key = lab.state["ssh_keys"].get(kid)
    if not ssh_key:
        raise not_found("SSH key")
    return ssh_key


def server_action(lab, sid: int, cmd: str, body: dict) -> dict:
    """Run a server action; return the action and its extra answer fields."""
    server = lab.server(sid)
    if cmd == "change_protection":  # no lock: it changes nothing on the VM
        given = {bool(body[k]) for k in ("delete", "rebuild") if k in body}
        if len(given) != 1:  # Hetzner: delete and rebuild protection go together
            raise invalid("delete and rebuild need the same value", "delete")
        server["protection"]["delete"] = server["protection"]["rebuild"] = given.pop()
        return {"action": lab.done(cmd, [(sid, "server")])}
    if cmd == "change_dns_ptr":
        ips.server_dns_ptr(lab, server, body)
        return {"action": lab.done(cmd, [(sid, "server")])}
    if cmd not in SERVER_ACTIONS:
        raise unsupported(f"the action {cmd!r}")
    name, prepare = SERVER_ACTIONS[cmd]
    lab.reserve(server)  # one action at a time (423), as on Hetzner
    try:
        steps, extra = prepare(lab, server, body)
    except Exception:  # refused: nothing runs, the server is free again
        lab.release(server)
        raise
    action = lab.new_action(name, [(sid, "server")])
    lab.run_action(action, steps, server)
    return {"action": action, **extra}


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab) -> None:
    """Add the server and SSH key routes to the app."""
    cfg, state = lab.cfg, lab.state

    @app.get("/v1/servers")
    def servers(request: Request):
        items = filtered(
            request,
            list(state["servers"].values()),
            {"status": lambda s, v: s["status"] in v.split(",")},
        )
        return paginate(
            request, [hetzner.server_json(cfg, state, s) for s in items], "servers"
        )

    @app.post("/v1/servers", status_code=201)
    async def server_create(request: Request):
        return await lab.blocking(create_server, lab, await read_body(request))

    @app.get("/v1/servers/{sid}")
    def server_get(sid: int):
        return {"server": hetzner.server_json(cfg, state, lab.server(sid))}

    @app.put("/v1/servers/{sid}")
    async def server_update(sid: int, request: Request):
        server = lab.server(sid)
        body = await read_body(request)
        if "name" in body and not hostname_ok(body["name"]):
            raise invalid("must be a valid hostname (RFC 1123)", "name")
        with state.lock:
            unique_name(state["servers"].values(), body.get("name"), server, "server")
            if "name" in body:
                server["name"] = body["name"]
            if "labels" in body:
                server["labels"] = body["labels"] or {}
            state.save()
        if "labels" in body:  # label selectors of firewalls may match differently now
            await lab.blocking(lab.fabric.apply_firewalls)
        return {"server": hetzner.server_json(cfg, state, server)}

    @app.delete("/v1/servers/{sid}")
    def server_delete(sid: int):
        return delete_server(lab, sid)

    @app.post("/v1/servers/{sid}/actions/{cmd}", status_code=201)
    async def server_post_action(sid: int, cmd: str, request: Request):
        return await lab.blocking(
            server_action, lab, sid, cmd, await read_body(request)
        )

    # SSH keys
    @app.get("/v1/ssh_keys")
    def ssh_keys(request: Request):
        items = filtered(
            request,
            list(state["ssh_keys"].values()),
            {"fingerprint": lambda k, v: k["fingerprint"] == v},
        )
        return paginate(request, items, "ssh_keys")

    @app.post("/v1/ssh_keys", status_code=201)
    async def ssh_key_create(request: Request):
        body = await read_body(request)
        if not body.get("name") or not body.get("public_key"):
            raise invalid("name and public_key are required")
        try:
            fingerprint = hetzner.ssh_fingerprint(body["public_key"])
        except Exception:  # noqa: BLE001
            raise invalid("invalid public key") from None
        with state.lock:
            if any(k["name"] == body["name"] for k in state["ssh_keys"].values()):
                raise ApiError(
                    409, "uniqueness_error", "SSH key with the same name already exists"
                )
            if any(k["fingerprint"] == fingerprint for k in state["ssh_keys"].values()):
                raise ApiError(
                    409,
                    "uniqueness_error",
                    "SSH key with the same fingerprint already exists",
                )
            kid = state.new_id("ssh_keys")
            ssh_key = {
                "id": kid,
                "name": body["name"],
                "fingerprint": fingerprint,
                "public_key": body["public_key"].strip(),
                "labels": body.get("labels") or {},
                "created": now(),
            }
            state["ssh_keys"][kid] = ssh_key
            state.save()
        return {"ssh_key": ssh_key}

    @app.get("/v1/ssh_keys/{kid}")
    def ssh_key_get(kid: int):
        return {"ssh_key": ssh_key_or_404(lab, kid)}

    @app.put("/v1/ssh_keys/{kid}")
    async def ssh_key_update(kid: int, request: Request):
        ssh_key = ssh_key_or_404(lab, kid)
        body = await read_body(request)
        with state.lock:
            unique_name(
                state["ssh_keys"].values(), body.get("name"), ssh_key, "SSH key"
            )
            if "name" in body:
                ssh_key["name"] = body["name"]
            if "labels" in body:
                ssh_key["labels"] = body["labels"] or {}
            state.save()
        return {"ssh_key": ssh_key}

    @app.delete("/v1/ssh_keys/{kid}", status_code=204)
    def ssh_key_delete(kid: int):
        with state.lock:
            ssh_key_or_404(lab, kid)
            del state["ssh_keys"][kid]
            state.save()
        return Response(status_code=204)
