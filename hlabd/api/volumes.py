"""Volumes: block devices that attach to servers of their location, like Hetzner's: SCSI disks with vendor HC and
model Volume (/dev/disk/by-id/scsi-0HC_Volume_<id>), formatted on request, automounted at /mnt/HC_Volume_<id>."""

from pathlib import Path

from fastapi import FastAPI, Request
from fastapi.responses import Response

from . import hetzner as H
from .catalogue import resolve
from .web import ApiError, body, filtered, invalid, limit_exceeded, not_found, paginate, unique_name

MIN_GB, MAX_GB = 10, 10240
PER_SERVER = 16


def volume_json(lab, v: dict) -> dict:
    return {"id": v["id"], "name": v["name"], "created": v["created"], "server": v.get("server"),
            "location": H.location(lab.cfg, v["location"]), "size": v["size"],
            "linux_device": f"/dev/disk/by-id/scsi-0HC_Volume_{v['id']}", "protection": v["protection"],
            "labels": v["labels"], "status": v["status"], "format": v.get("format")}


def volume_file(lab, v: dict) -> Path:
    return lab.fabric.root / "volumes" / f"{v['id']}.raw"


def check_attach(lab, v: dict, s: dict) -> None:
    """Whether the volume can be attached to the server (Hetzner's errors)."""
    if v.get("server"):
        raise ApiError(422, "volume_already_attached", "the volume is attached to another server")
    if v["location"] != s["location"]:
        raise ApiError(422, "location_mismatch", "server and volume must be in the same location")
    if sum(1 for x in lab.state["volumes"].values() if x.get("server") == s["id"]) >= PER_SERVER:
        raise limit_exceeded(f"at most {PER_SERVER} volumes per server", "volumes_per_server")


def attach(lab, v: dict, s: dict, automount: bool) -> None:
    """Bookkeeping of an attachment; the disk itself follows (hot-plug or next boot)."""
    check_attach(lab, v, s)
    v["server"], v["automount"] = s["id"], bool(automount)


def add_routes(app: FastAPI, lab) -> None:
    cfg, state, fabric = lab.cfg, lab.state, lab.fabric

    def vol(vid: int) -> dict:
        v = state["volumes"].get(vid)
        if not v:
            raise not_found("volume")
        return v

    @app.get("/v1/volumes")
    def volumes(request: Request):
        items = filtered(request, list(state["volumes"].values()),
                         {"status": lambda v, x: v["status"] in x.split(",")})
        return paginate(request, [volume_json(lab, v) for v in items], "volumes")

    @app.post("/v1/volumes", status_code=201)
    async def volume_create(request: Request):
        b = await body(request)
        size = int(b["size"])
        if not MIN_GB <= size <= MAX_GB:
            raise invalid(f"must be between {MIN_GB} and {MAX_GB}", "size")
        if any(v["name"] == b["name"] for v in state["volumes"].values()):
            raise ApiError(409, "uniqueness_error", "volume name is already used", {"fields": [{"name": "name"}]})
        if b.get("format") not in (None, "ext4", "xfs"):
            raise invalid("must be ext4 or xfs", "format")
        s = lab.server(int(b["server"])) if b.get("server") else None
        loc = resolve(lab, "location", b.get("location") or (s or {}).get("location") or "") \
            if (b.get("location") or s) else None
        if not loc:
            raise invalid("location or server is required", "location")
        with state.lock:
            if s:   # checked before the volume exists: a refused request leaves nothing behind
                check_attach(lab, {"server": None, "location": loc}, s)
            vid = state.new_id("volumes")
            v = {"id": vid, "name": b["name"], "size": size, "location": loc, "labels": b.get("labels") or {},
                 "protection": {"delete": False}, "status": "creating", "format": b.get("format"),
                 "created": H.now(), "server": None}
            state["volumes"][vid] = v
            if s:
                attach(lab, v, s, b.get("automount", False))
            state.save()
        a = lab.new_action("create_volume", [(vid, "volume")])
        nxt = [lab.new_action("attach_volume", [(vid, "volume"), (s["id"], "server")])] if s else []

        async def steps():
            f = volume_file(lab, v)
            f.parent.mkdir(parents=True, exist_ok=True)
            try:
                await lab.blocking(fabric.sh.run, "volume-create", str(f), str(size), v.get("format") or "")
            except Exception:
                with state.lock:   # no volume stays "creating" (or attached) without its disk
                    state["volumes"].pop(vid, None)
                    state.save()
                f.unlink(missing_ok=True)
                raise
            if state["volumes"].get(vid) is not v:   # deleted meanwhile: its disk goes too
                f.unlink(missing_ok=True)
                return
            v["status"] = "available"
            if s and await lab.blocking(fabric.vm_alive, s):
                await lab.blocking(fabric.sh.run, "volume-attach", str(fabric.vm_dir(s)), str(vid), str(f))
            for n in nxt:
                lab.finish(n)
            await lab.sleep(2)
        lab.run_action(a, steps, nxt=nxt)
        return {"volume": volume_json(lab, v), "action": a, "next_actions": nxt}

    @app.get("/v1/volumes/{vid}")
    def volume_get(vid: int):
        return {"volume": volume_json(lab, vol(vid))}

    @app.put("/v1/volumes/{vid}")
    async def volume_update(vid: int, request: Request):
        v = vol(vid)
        b = await body(request)
        unique_name(state["volumes"].values(), b.get("name"), v, "volume")
        for k in ("name", "labels"):
            if k in b:
                v[k] = b[k] if k != "labels" else (b[k] or {})
        state.save()
        return {"volume": volume_json(lab, v)}

    @app.delete("/v1/volumes/{vid}", status_code=204)
    def volume_delete(vid: int):
        v = vol(vid)
        if v["protection"]["delete"]:
            raise ApiError(423, "protected", "volume is delete protected")
        if v.get("server"):
            raise ApiError(422, "volume_attached", "detach the volume first")
        if v.get("_busy") or v["status"] != "available":
            raise ApiError(423, "locked", "the volume is busy")
        volume_file(lab, v).unlink(missing_ok=True)
        del state["volumes"][vid]
        state.save()
        return Response(status_code=204)

    def alive(s: dict | None) -> bool:
        return bool(s) and fabric.vm_alive(s)

    @app.post("/v1/volumes/{vid}/actions/{cmd}", status_code=201)
    async def volume_action(vid: int, cmd: str, request: Request):
        v = vol(vid)
        b = await body(request)
        f = volume_file(lab, v)
        if cmd in ("attach", "detach", "resize"):
            with state.lock:   # one action of a volume at a time, and only once it exists
                if v["status"] != "available" or v.get("_busy"):
                    raise ApiError(423, "locked", "the volume is busy")
                v["_busy"] = True
        try:
            if cmd == "attach":
                sid = b.get("server")
                if isinstance(sid, bool) or not isinstance(sid, int):
                    raise invalid("server must be the ID of a server", "server")
                s = lab.server(sid)
                lab.reserve(s)   # 423 while the server has an action running
                try:
                    with state.lock:
                        attach(lab, v, s, b.get("automount", False))
                        state.save()
                except BaseException:
                    s["locked"] = False
                    raise
                a = lab.new_action("attach_volume", [(vid, "volume"), (s["id"], "server")])

                async def steps():
                    if await lab.blocking(alive, s):
                        try:
                            await lab.blocking(fabric.sh.run, "volume-attach", str(fabric.vm_dir(s)), str(vid), str(f))
                        except Exception:   # not attached: the volume says so
                            with state.lock:
                                v["server"], v["automount"] = None, False
                                state.save()
                            raise
                    await lab.sleep(2)
            elif cmd == "detach":
                s = state["servers"].get(v.get("server"))
                if not v.get("server"):
                    raise invalid("the volume is not attached", "volume")
                if s:
                    lab.reserve(s)
                a = lab.new_action("detach_volume", [(vid, "volume")] + ([(s["id"], "server")] if s else []))

                async def steps():
                    if await lab.blocking(alive, s):
                        await lab.blocking(fabric.sh.run, "volume-detach", str(fabric.vm_dir(s)), str(vid))
                    with state.lock:
                        v["server"], v["automount"] = None, False
                        state.save()
                    await lab.sleep(2)
            elif cmd == "resize":
                size = b.get("size")
                if isinstance(size, bool) or not isinstance(size, int) or size <= v["size"] or size > MAX_GB:
                    raise invalid(f"must be larger than {v['size']} and at most {MAX_GB}", "size")
                s = state["servers"].get(v.get("server"))
                if s:
                    lab.reserve(s)
                a = lab.new_action("resize_volume", [(vid, "volume")])

                async def steps():
                    live = await lab.blocking(alive, s)
                    await lab.blocking(fabric.sh.run, "volume-resize", str(f), str(size),
                                       str(fabric.vm_dir(s)) if live else "", str(vid))
                    v["size"] = size
                    state.save()
                    await lab.sleep(2)
        except BaseException:
            v.pop("_busy", None)
            raise
        if cmd == "change_protection":
            v["protection"]["delete"] = bool(b.get("delete", v["protection"]["delete"]))
            state.save()
            a = lab.new_action("change_protection", [(vid, "volume")])
            lab.finish(a)
            return {"action": a}
        elif cmd not in ("attach", "detach", "resize"):
            raise ApiError(422, "unsupported_error", f"the lab does not support the action {cmd!r}")

        async def busy():
            try:
                await steps()
            finally:
                v.pop("_busy", None)
        lab.run_action(a, busy, s)   # the server is unlocked when the steps end
        return {"action": a}
