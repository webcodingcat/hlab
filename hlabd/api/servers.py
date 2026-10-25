"""Servers and SSH keys: create, delete, the power and other server actions, with the cloud's timings (power off
reports off after ~9 s, reset ~15 s)."""

import base64
import secrets

from fastapi import FastAPI, Request
from fastapi.responses import Response

from . import hetzner as H
from . import ips, networks, volumes
from .catalogue import image_name, image_path, resolve
from .spec import hostname_ok
from .web import ApiError, body, filtered, invalid, limit_exceeded, not_found, paginate, unique_name, unsupported

USER_DATA_LIMIT = 32 * 1024


def ref_id(value, field: str) -> int:
    """A resource ID from a request body, or invalid_input for the field."""
    try:
        return int(value)
    except (TypeError, ValueError):
        raise invalid(f"{value!r} is not an ID", field) from None


def check_user_data(user_data) -> None:
    if user_data is not None and not isinstance(user_data, str):
        raise invalid("user_data is a string", "user_data")
    if user_data is not None and len(user_data.encode()) > USER_DATA_LIMIT:
        raise invalid("user_data is limited to 32 KiB", "user_data")


# --- create and delete -------------------------------------------------------------------------------------------

def create_server(lab, b: dict) -> dict:
    """Everything is checked before anything changes (a refused request leaves nothing behind); the VM is made by the
    create action in the background."""
    state = lab.state
    name = b.get("name")
    if not name or not hostname_ok(name):
        raise invalid("must be a valid hostname (RFC 1123)", "name")
    stype = resolve(lab, "server_type", b.get("server_type"))
    image = image_name(lab, b.get("image"))
    loc = resolve(lab, "location", b.get("location") or "fsn1")
    keys = []
    for ref in b.get("ssh_keys") or []:
        k = next((k for k in state["ssh_keys"].values() if str(ref) in (k["name"], str(k["id"]))), None)
        if not k:
            raise invalid(f"ssh key {ref!r} not found")
        keys.append(k["public_key"])
    public_net = b.get("public_net") or {}
    check_user_data(b.get("user_data"))
    fws = [ref_id((f or {}).get("firewall"), "firewalls") for f in b.get("firewalls") or []]
    vols = []
    if len({str(v) for v in b.get("volumes") or []}) != len(b.get("volumes") or []):
        raise invalid("a volume is listed twice", "volumes")
    for vid in b.get("volumes") or []:
        v = state["volumes"].get(ref_id(vid, "volumes"))
        if not v:
            raise invalid(f"volume {vid} not found", "volumes")
        if v["status"] != "available" or v.get("_busy"):
            raise ApiError(423, "locked", f"volume {vid} is busy")
        vols.append(v)
    if len(vols) > volumes.PER_SERVER:
        raise limit_exceeded(f"at most {volumes.PER_SERVER} volumes per server", "volumes_per_server")
    nets = [ref_id(n, "networks") for n in b.get("networks") or []]
    start = bool(b.get("start_after_create", True))
    with state.lock:
        if any(s["name"] == name for s in state["servers"].values()):
            raise ApiError(409, "uniqueness_error", "server name is already used")
        index = state.free_index()
        if not index:
            raise limit_exceeded("the lab runs at most 239 servers", "servers")
        for fid in fws:
            if fid not in state["firewalls"]:
                raise invalid(f"firewall {fid} not found", "firewalls")
        sid = state.new_id("servers")
        s = {"id": sid, "name": name, "status": "initializing", "created": H.now(),
             "labels": b.get("labels") or {}, "server_type": stype, "image": image, "location": loc,
             "protection": {"delete": False, "rebuild": False}, "locked": True,   # its create action runs
             "user_data": b.get("user_data"), "public_keys": keys,
             "_lab": {"index": index, "tap": f"pub{index}", "nics": [], "start": start}}
        for v in vols:
            volumes.check_attach(lab, v, s)
        for nid in nets:   # only the new server changes here
            networks.add_nic(lab, s, nid, None)
        ips.check_server_ips(lab, s, public_net)
        try:
            ips.setup_server_ips(lab, s, public_net)   # the lab may run out of addresses
        except Exception:
            ips.release_server_ips(lab, s)
            raise
        # from here on nothing fails
        for v in vols:
            volumes.attach(lab, v, s, b.get("automount", False))
        for fid in fws:
            state["firewalls"][fid]["applied_to"].append({"type": "server", "server": {"id": sid}})
        if not keys:   # Hetzner sets a root password then and returns it once
            s["_lab"]["root_password"] = secrets.token_urlsafe(15)
        state["servers"][sid] = s
        state.save()
    a = lab.new_action("create_server", [(sid, "server")])
    nxt = [lab.new_action("start_server", [(sid, "server")])] if start else []

    async def steps():
        await lab.blocking(lab.fabric.apply_firewalls)
        base = await lab.blocking(image_path, lab, image)
        await lab.blocking(lab.fabric.create_vm, s, base)
        if start:
            lab.set_status(s, "starting")
            await lab.blocking(lab.fabric.start_vm, s)
            await lab.sleep(5)
            lab.set_status(s, "running")
        else:
            lab.set_status(s, "off")
        for n in nxt:
            lab.finish(n)
    lab.run_action(a, steps, s, nxt)
    return {"server": H.server(lab.cfg, state, s), "action": a, "next_actions": nxt,
            "root_password": s["_lab"].get("root_password")}


def delete_server(lab, sid: int) -> dict:
    s = lab.server(sid)
    if s["protection"]["delete"]:
        raise ApiError(423, "protected", "server is delete protected")
    lab.reserve(s)
    a = lab.new_action("delete_server", [(sid, "server")])
    lab.set_status(s, "deleting")   # a restart of the lab finishes what is started here

    async def steps():
        await lab.blocking(forget_server, lab, s)
        await lab.sleep(2)
    lab.run_action(a, steps, s)
    return {"action": a}


async def ensure_disk(lab, s: dict) -> None:
    """A server whose disk is missing (a lab restart cut its creation short) gets one from its image."""
    if not (lab.fabric.vm_dir(s) / "disk.qcow2").is_file():
        base = await lab.blocking(image_path, lab, s["image"])
        await lab.blocking(lab.fabric.create_vm, s, base)


def forget_server(lab, s: dict) -> None:
    """A server goes: its VM, its addresses and every reference to it; volumes stay, detached."""
    state, fabric, sid = lab.state, lab.fabric, s["id"]
    fabric.delete_vm(s)
    unrouted = []
    with state.lock:
        for f in state["floating_ips"].values():
            if f.get("server") == sid:
                f["server"] = None
                unrouted.append(f["ip"])
        for fw in state["firewalls"].values():
            fw["applied_to"] = [x for x in fw["applied_to"] if x.get("server", {}).get("id") != sid]
        ips.release_server_ips(lab, s)
        for v in state["volumes"].values():
            if v.get("server") == sid:
                v["server"], v["automount"] = None, False
        ref = f"s:{sid}"   # its faults go too
        state.data["faults"] = [x for x in state.data.get("faults", [])
                                if ref not in (x.get("a_ref"), x.get("b_ref"), x.get("vm_ref"))]
        netem = state.data.get("netem", {})   # the index (and the taps' names) will be used again
        for tap in [s["_lab"]["tap"]] + [n["tap"] for n in s["_lab"]["nics"]]:
            netem.pop(tap, None)
        state["servers"].pop(sid, None)
        state.save()
    for ip in unrouted:
        fabric.route_floating_ip(ip, None)
    fabric.write_dhcp()
    fabric.apply_faults()
    fabric.apply_firewalls()


# --- server actions ------------------------------------------------------------------------------------------------
# Each action below checks the request and returns (steps, extra answer fields); the steps run in the background
# while the server is locked.

def poweroff(lab, s: dict, b: dict):
    """Hard: the VM dies at once, the status follows."""
    async def steps():
        await lab.blocking(lab.fabric.kill_vm, s)
        lab.set_status(s, "stopping")
        await lab.sleep(8.7)
        lab.set_status(s, "off")
        await lab.sleep(1.2)
    return steps, {}


def shutdown(lab, s: dict, b: dict):
    """The ACPI power button: the action is done once it is pressed (Hetzner); the server reports off when its guest
    is down, and a guest that ignores the button keeps running."""
    async def steps():
        await lab.blocking(lab.fabric.qmp, s, "system_powerdown")
        lab.spawn(lab.off_when_down(s))
        await lab.sleep(1)
    return steps, {}


def poweron(lab, s: dict, b: dict):
    async def steps():
        if s["status"] == "running" and await lab.blocking(lab.fabric.vm_alive, s):
            return   # already on: the action succeeds, nothing changes
        lab.set_status(s, "starting")
        if s.get("_dead"):   # the fault "dead": broken hardware, the VM stays off
            await lab.sleep(7)
            lab.set_status(s, "off")
            return
        await ensure_disk(lab, s)
        await lab.blocking(lab.fabric.start_vm, s)
        await lab.sleep(7)
        lab.set_status(s, "running")
        await lab.sleep(1)
    return steps, {}


def reboot_or_reset(soft: bool):
    def prepare(lab, s: dict, b: dict):
        """reboot is soft (the guest reboots itself after ctrl-alt-delete, as Hetzner's ACPI reboot), reset hard."""
        async def steps():
            if s["status"] != "running":
                return
            if soft:
                await lab.blocking(lab.fabric.hmp, s, "sendkey ctrl-alt-delete")
            else:
                await lab.blocking(lab.fabric.qmp, s, "system_reset")
            lab.set_status(s, "starting")
            await lab.sleep(13)
            lab.set_status(s, "running")
            await lab.sleep(2)
        return steps, {}
    return prepare


def rebuild(lab, s: dict, b: dict):
    """A fresh disk from an image; new user data if given (else the old stays), a new root password without keys."""
    if s["protection"]["rebuild"]:
        raise ApiError(423, "protected", "server is rebuild protected")
    image = image_name(lab, b.get("image"))
    check_user_data(b.get("user_data"))
    if not s["public_keys"]:
        s["_lab"]["root_password"] = secrets.token_urlsafe(15)

    async def steps():
        was = s["status"]
        s["_lab"]["after_rebuild"] = "running" if was == "running" else "off"   # for a lab restart
        base = await lab.blocking(image_path, lab, image)   # before the old disk goes
        await lab.blocking(lab.fabric.delete_vm, s)
        s["image"] = image
        lab.set_status(s, "rebuilding")
        await lab.blocking(lab.fabric.create_vm, s, base)
        if "user_data" in b:
            s["user_data"] = b["user_data"]
        if was == "running":
            await lab.blocking(lab.fabric.start_vm, s)
        await lab.sleep(10)
        lab.set_status(s, was if was in ("running", "off") else "running")
    return steps, {"root_password": s["_lab"].get("root_password")}


def change_type(lab, s: dict, b: dict):
    """Only while off. The disk grows with upgrade_disk; a grown disk cannot go back to a smaller type (Hetzner)."""
    if s["status"] != "off":
        raise ApiError(422, "server_not_stopped", "server must be stopped")
    cfg = lab.cfg
    stype = resolve(lab, "server_type", b.get("server_type"))
    disk = s["_lab"].get("disk_gb") or cfg["server_types"][s["server_type"]]["disk_gb"]
    new_disk = cfg["server_types"][stype]["disk_gb"]
    if new_disk < disk:
        raise ApiError(422, "invalid_server_type", "the server type's disk is smaller than the server's")
    upgrade = bool(b.get("upgrade_disk"))

    async def steps():
        if upgrade and new_disk > disk:
            await lab.blocking(lab.fabric.sh.run, "vm-disk-resize", str(lab.fabric.vm_dir(s)), str(new_disk))
            s["_lab"]["disk_gb"] = new_disk
        else:
            s["_lab"]["disk_gb"] = disk
        s["server_type"] = stype
        lab.state.save()
        await lab.sleep(30)
    return steps, {}


def reset_password(lab, s: dict, b: dict):
    """Through the QEMU guest agent in the guest, as on Hetzner."""
    if not lab.fabric.vm_alive(s):
        raise ApiError(409, "server_not_running", "the server is not running")
    password = secrets.token_urlsafe(15)

    async def steps():
        cmd = {"execute": "guest-set-user-password", "arguments": {
            "username": "root", "password": base64.b64encode(password.encode()).decode(), "crypted": False}}
        out = await lab.blocking(lab.fabric.qga, s, cmd)
        if out is None or "error" in out:
            raise RuntimeError("the QEMU guest agent did not answer (qemu-guest-agent not running?)")
    return steps, {"root_password": password}


# action name on Hetzner (default: the command) and the function that prepares it
SERVER_ACTIONS = {
    "poweroff": ("stop_server", poweroff), "shutdown": ("shutdown_server", shutdown),
    "poweron": ("start_server", poweron), "reboot": ("reboot_server", reboot_or_reset(soft=True)),
    "reset": ("reset_server", reboot_or_reset(soft=False)), "rebuild": ("rebuild_server", rebuild),
    "change_type": ("change_server_type", change_type), "reset_password": ("reset_password", reset_password),
    "attach_to_network": ("attach_to_network", networks.attach_to_network),
    "detach_from_network": ("detach_from_network", networks.detach_from_network),
    "change_alias_ips": ("change_alias_ips", networks.change_alias_ips),
}


def server_action(lab, sid: int, cmd: str, b: dict) -> dict:
    s = lab.server(sid)
    if cmd == "change_protection":   # no lock: it changes nothing on the VM
        given = {bool(b[k]) for k in ("delete", "rebuild") if k in b}
        if len(given) != 1:   # Hetzner: delete and rebuild protection go together
            raise invalid("delete and rebuild need the same value", "delete")
        s["protection"]["delete"] = s["protection"]["rebuild"] = given.pop()
        return done(lab, s, cmd)
    if cmd == "change_dns_ptr":
        ips.server_dns_ptr(lab, s, b)
        return done(lab, s, cmd)
    if cmd not in SERVER_ACTIONS:
        raise unsupported(f"the action {cmd!r}")
    name, prepare = SERVER_ACTIONS[cmd]
    lab.reserve(s)   # one action at a time (423), as on Hetzner
    try:
        steps, extra = prepare(lab, s, b)
    except Exception:   # refused: nothing runs, the server is free again
        s["locked"] = False
        raise
    a = lab.new_action(name, [(sid, "server")])
    lab.run_action(a, steps, s)
    return {"action": a, **extra}


def done(lab, s: dict, command: str) -> dict:
    """An action that is finished at once."""
    a = lab.new_action(command, [(s["id"], "server")])
    lab.finish(a)
    return {"action": a}


# --- routes ------------------------------------------------------------------------------------------------------

def add_routes(app: FastAPI, lab) -> None:
    cfg, state = lab.cfg, lab.state

    @app.get("/v1/servers")
    def servers(request: Request):
        items = filtered(request, list(state["servers"].values()),
                         {"status": lambda s, v: s["status"] in v.split(",")})
        return paginate(request, [H.server(cfg, state, s) for s in items], "servers")

    @app.post("/v1/servers", status_code=201)
    async def server_create(request: Request):
        return await lab.blocking(create_server, lab, await body(request))

    @app.get("/v1/servers/{sid}")
    def server_get(sid: int):
        return {"server": H.server(cfg, state, lab.server(sid))}

    @app.put("/v1/servers/{sid}")
    async def server_update(sid: int, request: Request):
        s = lab.server(sid)
        b = await body(request)
        if "name" in b and not hostname_ok(b["name"]):
            raise invalid("must be a valid hostname (RFC 1123)", "name")
        with state.lock:
            unique_name(state["servers"].values(), b.get("name"), s, "server")
            if "name" in b:
                s["name"] = b["name"]
            if "labels" in b:
                s["labels"] = b["labels"] or {}
            state.save()
        if "labels" in b:   # label selectors of firewalls may match differently now
            await lab.blocking(lab.fabric.apply_firewalls)
        return {"server": H.server(cfg, state, s)}

    @app.delete("/v1/servers/{sid}")
    def server_delete(sid: int):
        return delete_server(lab, sid)

    @app.post("/v1/servers/{sid}/actions/{cmd}", status_code=201)
    async def server_post_action(sid: int, cmd: str, request: Request):
        return await lab.blocking(server_action, lab, sid, cmd, await body(request))

    # SSH keys
    def key_or_404(kid: int) -> dict:
        k = state["ssh_keys"].get(kid)
        if not k:
            raise not_found("SSH key")
        return k

    @app.get("/v1/ssh_keys")
    def ssh_keys(request: Request):
        items = filtered(request, list(state["ssh_keys"].values()),
                         {"fingerprint": lambda k, v: k["fingerprint"] == v})
        return paginate(request, items, "ssh_keys")

    @app.post("/v1/ssh_keys", status_code=201)
    async def ssh_key_create(request: Request):
        b = await body(request)
        if not b.get("name") or not b.get("public_key"):
            raise invalid("name and public_key are required")
        try:
            fp = H.ssh_fingerprint(b["public_key"])
        except Exception:  # noqa: BLE001
            raise invalid("invalid public key") from None
        with state.lock:
            if any(k["name"] == b["name"] for k in state["ssh_keys"].values()):
                raise ApiError(409, "uniqueness_error", "SSH key with the same name already exists")
            if any(k["fingerprint"] == fp for k in state["ssh_keys"].values()):
                raise ApiError(409, "uniqueness_error", "SSH key with the same fingerprint already exists")
            kid = state.new_id("ssh_keys")
            k = {"id": kid, "name": b["name"], "fingerprint": fp, "public_key": b["public_key"].strip(),
                 "labels": b.get("labels") or {}, "created": H.now()}
            state["ssh_keys"][kid] = k
            state.save()
        return {"ssh_key": k}

    @app.get("/v1/ssh_keys/{kid}")
    def ssh_key_get(kid: int):
        return {"ssh_key": key_or_404(kid)}

    @app.put("/v1/ssh_keys/{kid}")
    async def ssh_key_update(kid: int, request: Request):
        k = key_or_404(kid)
        b = await body(request)
        with state.lock:
            unique_name(state["ssh_keys"].values(), b.get("name"), k, "SSH key")
            if "name" in b:
                k["name"] = b["name"]
            if "labels" in b:
                k["labels"] = b["labels"] or {}
            state.save()
        return {"ssh_key": k}

    @app.delete("/v1/ssh_keys/{kid}", status_code=204)
    def ssh_key_delete(kid: int):
        with state.lock:
            key_or_404(kid)
            del state["ssh_keys"][kid]
            state.save()
        return Response(status_code=204)
