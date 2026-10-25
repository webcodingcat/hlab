"""The catalogue: server types, locations and the system images of the configuration (loaded at the lab's start).
Datacenters are gone, as on Hetzner since 2026."""

import logging

from fastapi import FastAPI, Request

from ..errors import ApiError, invalid, not_found
from . import hetzner as H
from .web import filtered, paginate

log = logging.getLogger("hlabd.images")


# --- images ------------------------------------------------------------------------------------------------------

def image_name(lab, ref) -> str:
    """The name of a system image of the configuration, by name or ID (what a server stores as its image)."""
    if isinstance(ref, dict):
        ref = ref.get("id") or ref.get("name")
    for n in lab.cfg["images"]:
        if str(ref) in (n, str(H.image_id(lab.cfg, n))):
            return n
    raise invalid(f"image {ref!r} not found", "image")


def image_path(lab, ref) -> str:
    """The qcow2 file of a system image, as loaded at the lab's start."""
    name = image_name(lab, ref)
    if not lab.images.get(name):
        raise RuntimeError(f"image {name} could not be loaded at the lab's start (see the lab's log)")
    return lab.images[name]


def resolve(lab, kind: str, ref) -> str:
    """A server type or location of the configuration, by name or ID (or an object with one of them) → its name."""
    if isinstance(ref, dict):
        ref = ref.get("name") or ref.get("id")
    id_of = {"server_type": H.server_type_id, "location": H.location_id}[kind]
    for n in lab.cfg[f"{kind}s"]:
        if str(ref) in (n, str(id_of(lab.cfg, n))):
            return n
    raise invalid(f"{kind} {ref!r} not found")


def load_images(lab) -> None:
    """At the lab's start: every image of the configuration in the version its checksum file names now, downloaded
    unless it is there already. The files are named by checksum, so servers keep the version they were made from;
    without network the newest one there is used."""
    directory = lab.fabric.root / "images" / "system"   # for the fallback: the newest file there
    for name, src in lab.cfg["images"].items():
        log.info("image %s: checking %s", name, src["url"])
        try:
            lab.images[name] = lab.fabric.fetch_image(name, src["url"], src["checksums"])
            log.info("image %s: %s", name, lab.images[name])
        except Exception as error:  # noqa: BLE001 – the lab comes up; servers of this image fail with a clear message
            have = sorted(directory.glob(f"{name}-*.qcow2"), key=lambda p: p.stat().st_mtime)
            if have:
                lab.images[name] = str(have[-1])
                log.warning("image %s: %s; using %s", name, error, have[-1])
            else:
                log.error("image %s: not available: %s", name, error)


# --- routes ------------------------------------------------------------------------------------------------------

def add_routes(app: FastAPI, lab) -> None:
    cfg = lab.cfg

    def by_id(names, id_of, rid: int, what: str) -> str:
        for n in names:
            if id_of(cfg, n) == rid:
                return n
        raise not_found(what)

    @app.get("/v1/server_types")
    def server_types(request: Request):
        types = [H.server_type_json(cfg, n) for n in cfg["server_types"]]
        return paginate(request, filtered(request, types), "server_types")

    @app.get("/v1/server_types/{tid}")
    def server_type_get(tid: int):
        name = by_id(cfg["server_types"], H.server_type_id, tid, "server type")
        return {"server_type": H.server_type_json(cfg, name)}

    @app.get("/v1/locations")
    def locations(request: Request):
        return paginate(request, filtered(request, [H.location_json(cfg, n) for n in cfg["locations"]]), "locations")

    @app.get("/v1/locations/{lid}")
    def location_get(lid: int):
        return {"location": H.location_json(cfg, by_id(cfg["locations"], H.location_id, lid, "location"))}

    @app.get("/v1/images")
    def images(request: Request):
        query = request.query_params
        items = [H.image_json(cfg, n) for n in cfg["images"]]
        if query.getlist("type"):
            items = [i for i in items if i["type"] in query.getlist("type")]
        if query.get("architecture"):
            items = [i for i in items if i["architecture"] == query["architecture"]]
        return paginate(request, filtered(request, items), "images")

    @app.get("/v1/images/{iid}")
    def image_get(iid: int):
        return {"image": H.image_json(cfg, by_id(cfg["images"], H.image_id, iid, "image"))}

    # removed by Hetzner on 1 Oct. 2026 (the "datacenter" properties of servers and primary IPs on 1 July 2026)
    @app.get("/v1/datacenters")
    @app.get("/v1/datacenters/{did}")
    def datacenters(did: int = 0):
        raise ApiError(410, "deprecated_api_endpoint", "API functionality was removed",
                       {"announcement": "https://docs.hetzner.cloud/changelog#2026-07-01-removing-datacenters"})
