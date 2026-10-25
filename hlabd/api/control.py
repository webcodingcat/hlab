"""Lab control under /_lab/: faults, Internet clients, Storage Box accounts, reset. Used by the lab CLI and by test
harnesses (also from inside the VMs, through http://198.18.0.1/_lab/...)."""

import asyncio
import os
import pwd
import re
import shutil
import tempfile

from fastapi import FastAPI, Request

from .. import config as C
from ..fabric import route_table
from ..fabric.storagebox import data_dir, snap_dir, username
from ..state import ID_BASE, KINDS
from . import hetzner as H
from .web import ApiError, body, invalid

FAULT_TYPES = {"cut": ("a", "b", "net"), "isolate": ("vm",), "block": ("vm", "what")}


NETEM_WORD = re.compile(r"(delay|loss|duplicate|corrupt|reorder|rate|limit|gap|distribution|normal|pareto|"
                        r"paretonormal|uniform|random|state|gemodel|ecn|slot|\d+(\.\d+)?(us|ms|s|%|bit|kbit|mbit|gbit)?)")


def add_routes(app: FastAPI, lab) -> None:
    state, fabric = lab.state, lab.fabric

    def fault_of(b: dict) -> dict:
        t = b.get("type")
        if t not in FAULT_TYPES:
            raise invalid(f"fault type must be one of {', '.join(FAULT_TYPES)}")
        f = {"type": t}
        for k in FAULT_TYPES[t]:
            f[k] = b.get(k) or ("all" if k == "net" else None)
            if f[k] is None:
                raise invalid(f"{t}: {k} is required")
        if t == "cut" and f["net"] not in ("public", "private", "all"):
            raise invalid("net must be public, private or all")
        if t == "block" and f["what"] not in ("api", "box", "internet"):
            raise invalid("what must be api, box or internet")
        for k in ("a", "b", "vm"):
            if k in f:
                f[f"{k}_ref"] = fabric.fault_ref(f[k])  # KeyError → 404 below
        return f

    @app.get("/_lab/status")
    def status():
        servers = [{"id": s["id"], "name": s["name"], "status": s["status"], "location": s["location"],
                    "server_type": s["server_type"], "public_ip": H.server(lab.cfg, state, s)["public_net"]["ipv4"],
                    "ipv6": fabric.ipv6_net(s),
                    "private": [{"network": n["network"], "ip": n["ip"]} for n in s["_lab"]["nics"]],
                    "vm": "on" if fabric.vm_alive(s) else "off", "dead": bool(s.get("_dead"))}
                   for s in sorted(state["servers"].values(), key=lambda s: s["id"])]
        return {"servers": servers, "floating_ips": [H.floating_ip(lab.cfg, f) for f in state["floating_ips"].values()],
                "clients": list(state["clients"].values()), "faults": state.data.get("faults", []),
                "netem": state.data.get("netem", {}), "api_fault": lab.fault}

    @app.post("/_lab/faults")
    async def fault_add(request: Request):
        try:
            f = fault_of(await body(request))
        except KeyError as e:
            raise ApiError(404, "not_found", str(e)) from None
        with state.lock:
            faults = state.data.setdefault("faults", [])
            if f not in faults:
                faults.append(f)
            state.save()
        await lab.blocking(fabric.apply_faults)
        return {"faults": state.data["faults"]}

    @app.post("/_lab/faults/remove")
    async def fault_remove(request: Request):
        b = await body(request)
        with state.lock:
            faults = state.data.setdefault("faults", [])
            state.data["faults"] = [f for f in faults if not all(f.get(k) == v for k, v in b.items() if v is not None)]
            state.save()
        await lab.blocking(fabric.apply_faults)
        return {"faults": state.data["faults"]}

    @app.post("/_lab/faults/clear")
    async def faults_clear():
        with state.lock:
            state.data["faults"] = []
            state.data["netem"] = {}
            for s in state["servers"].values():
                s.pop("_dead", None)
            lab.fault.update(status=0, count=0, delay_ms=0)
            state.save()
        await lab.blocking(fabric.apply_faults)
        await lab.blocking(fabric.apply_latency)
        return {"ok": True}

    @app.post("/_lab/netem")
    async def netem(request: Request):
        """{"vm": name, "net": public|private|all, "args": "delay 100ms loss 5%"} – empty args clears."""
        b = await body(request)
        args = b.get("args") or ""
        if not isinstance(args, str) or (args and not all(NETEM_WORD.fullmatch(w) for w in args.split())):
            raise invalid("args are netem options like 'delay 100ms 10ms loss 5%'", "args")
        try:
            taps = fabric.server_ifaces(b.get("vm", ""), b.get("net") or "all")
        except KeyError as e:
            raise ApiError(404, "not_found", str(e)) from None
        with state.lock:
            ne = state.data.setdefault("netem", {})
            for t in taps:
                if args:
                    ne[t] = " ".join(args.split())
                else:
                    ne.pop(t, None)
            state.save()
        await lab.blocking(fabric.apply_latency)
        return {"netem": state.data["netem"]}

    @app.post("/_lab/vm/{name}/{cmd}")
    async def vm(name: str, cmd: str):
        s = lab.server_by_name(name)
        if cmd == "pause":
            await lab.blocking(fabric.qmp, s, "stop")
        elif cmd == "cont":
            await lab.blocking(fabric.qmp, s, "cont")
        elif cmd in ("dead", "alive"):  # broken hardware: a power-on succeeds, the VM stays off
            s["_dead"] = cmd == "dead"
            state.save()
        else:
            raise invalid("command must be pause, cont, dead or alive")
        return {"ok": True}

    @app.post("/_lab/api-fault")
    async def api_fault(request: Request):
        """{"status": 503, "count": 3} answers the next requests with that status; {"delay_ms": n} delays all."""
        b = await body(request)
        try:
            new = {k: int(b.get(k) or 0) for k in ("status", "count", "delay_ms")}
        except (TypeError, ValueError):
            raise invalid("status, count and delay_ms are numbers") from None
        lab.fault.update(new)
        return {"api_fault": lab.fault}

    @app.post("/_lab/clients")
    async def client_add(request: Request):
        b = await body(request)
        name = b.get("name", "")
        if not name.isidentifier() or len(name) > 12:
            raise invalid("client name: letters, digits, underscore, at most 12 characters")
        with state.lock:
            c = next((c for c in state["clients"].values() if c["name"] == name), None)
            if not c:
                used = {c["n"] for c in state["clients"].values()}
                n = next(i for i in range(1, 200) if i not in used)
                cid = state.new_id("clients")
                c = {"id": cid, "name": name, "n": n, "veth": f"cl{n}", "ip": f"{C.CLIENT_NET}.{10 + n}",
                     "ip6": f"{C.CLIENT6}::{10 + n:x}"}
                state["clients"][cid] = c
                state.save()
        await lab.blocking(fabric.sh.run, "client-add", c["name"], c["veth"], c["ip"], c["ip6"])
        await lab.blocking(fabric.apply_faults)
        return {"client": c}

    @app.delete("/_lab/clients/{name}")
    async def client_delete(name: str):
        with state.lock:
            c = next((c for c in state["clients"].values() if c["name"] == name), None)
            if not c:
                raise ApiError(404, "not_found", "client not found")
            del state["clients"][c["id"]]
            ref = f"c:{c['name']}"   # its faults and netem go with it (a new client may get the same link)
            state.data["faults"] = [x for x in state.data.get("faults", [])
                                    if ref not in (x.get("a_ref"), x.get("b_ref"), x.get("vm_ref"))]
            state.data.get("netem", {}).pop(c["veth"], None)
            state.save()
        await lab.blocking(fabric.sh.run, "client-del", c["name"], c["veth"])
        await lab.blocking(fabric.apply_faults)
        return {"ok": True}

    @app.post("/_lab/box/users")
    async def box_user(request: Request):
        """{"name": "backup1", "public_key": "ssh-ed25519 ..."} – an SFTP account with its own home. Names of the
        API's accounts (u<id>, u<id>-sub<n>) and of the container's own users are taken."""
        b = await body(request)
        name, key = str(b.get("name") or ""), str(b.get("public_key") or "")
        keyed = key.startswith(("ssh-", "ecdsa-sha2-", "sk-ssh-", "sk-ecdsa-"))   # ed25519, RSA, ECDSA, FIDO
        if not re.fullmatch(r"[a-z][a-z0-9_-]{0,31}", name) or not keyed or "\n" in key.strip():
            raise invalid("name (lower case letters, digits, - and _) and one public_key are required")
        if re.fullmatch(r"u[0-9]+(-sub[0-9]+)?", name):
            raise invalid("names like u<id> belong to the Storage Box API", "name")
        try:
            pwd.getpwnam(name)
            if name not in state.data.get("box_users", {}):
                raise invalid("a user of the container has this name", "name")
        except KeyError:
            pass
        with tempfile.NamedTemporaryFile("w", suffix=".pub", delete=False) as f:
            f.write(key.strip() + "\n")
        try:
            await lab.blocking(fabric.box.lab_account, state.data.setdefault("box_uids", {}), name, f.name)
        finally:
            os.unlink(f.name)
        with state.lock:
            state.data.setdefault("box_users", {})[name] = key.strip()
            state.save()
        return {"user": name, "host": "198.18.0.1", "port": 23}

    @app.post("/_lab/reset")
    async def reset():
        """Everything the APIs created goes: servers (VMs and disks), networks, keys, firewalls, Floating IPs,
        volumes, Storage Boxes with their data, clients, faults. System images, the lab key and the lab's own
        Storage Box accounts (/_lab/box/users) stay."""
        for t in list(lab.tasks):
            t.cancel()
        for s in list(state["servers"].values()):
            await lab.blocking(fabric.delete_vm, s)
        for v in list(state["volumes"].values()):
            (fabric.root / "volumes" / f"{v['id']}.raw").unlink(missing_ok=True)
        for a in list(state["subaccounts"].values()):
            await lab.blocking(fabric.sh.run, "box-remove", a["username"])
        for b in list(state["storage_boxes"].values()):
            await lab.blocking(fabric.sh.run, "box-remove", username(b))
            await lab.blocking(fabric.sh.run, "box-rmtree", data_dir(b))
            await lab.blocking(fabric.sh.run, "box-rmtree", snap_dir(b))
        for f in list(state["floating_ips"].values()):
            if f.get("server"):
                await lab.blocking(fabric.route_floating_ip, f["ip"], None)
        for c in list(state["clients"].values()):
            await lab.blocking(fabric.sh.run, "client-del", c["name"], c["veth"])
        tables = [route_table(nid) for nid in state["networks"]]
        with state.lock:
            keep = {k: state.data[k] for k in ("box_users", "box_uids") if k in state.data}
            next_id = dict(state.data.get("next_id") or ID_BASE)
            state.data.clear()
            for k in KINDS:
                state.data[k] = {}
            state.data["next_id"] = next_id   # IDs go on counting, as in the cloud: nothing old matches a new one
            state.data.update(keep)
            lab.fault.update(status=0, count=0, delay_ms=0)
            state.save()
        # an action that was inside a blocking call when it was cancelled may still have started a VM
        await asyncio.sleep(1)
        for d in (fabric.root / "vm").glob("*"):
            await lab.blocking(fabric.sh.run, "vm-kill", str(d), check=False)
            await lab.blocking(shutil.rmtree, d, True)
        for t in tables:   # no old routes may wait in the deleted networks' tables
            await lab.blocking(fabric.sh.run, "route-flush", t, check=False)
        await lab.blocking(fabric.write_dhcp)
        await lab.blocking(fabric.apply_faults)
        await lab.blocking(fabric.apply_firewalls)
        return {"ok": True}
