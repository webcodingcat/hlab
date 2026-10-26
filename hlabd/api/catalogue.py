"""The catalogue: server types, locations, system images and Storage Box types.

All of them come from the configuration; the images are loaded at the lab's start.
Datacenters are gone, as on Hetzner since 2026.
"""

import logging

from fastapi import FastAPI, Request

from ..errors import ApiError, invalid, not_found
from ..lab import Lab
from . import hetzner
from .web import filtered, paginate

log = logging.getLogger(__name__)

# the ID of a catalogue entry by its kind; the entries are the configuration's
# "<kind>s" (server_types, locations, images, storage_box_types)
ID_OF = {
    "server_type": hetzner.server_type_id,
    "location": hetzner.location_id,
    "image": hetzner.image_id,
    "storage_box_type": hetzner.storage_box_type_id,
}


def by_name_or_id(items, ref, name_and_id):
    """Return the first item that `ref` names, by name or by ID, or None.

    `name_and_id(item)` gives an item's name and ID; an ID matches as number or as
    text ("5" and 5 alike), as clients send either.
    """
    return next(
        (item for item in items if str(ref) in map(str, name_and_id(item))), None
    )


def catalogue_name(lab: Lab, kind: str, ref, field: str | None = None) -> str:
    """Return the name of a catalogue entry a request gives by name or ID.

    The name is what a resource stores (a server its image, a box its type). The
    specification types these fields as strings, so the request check refuses objects.
    An unknown entry is invalid input in `field` (default: the kind).
    """
    id_of = ID_OF[kind]
    name = by_name_or_id(lab.cfg[f"{kind}s"], ref, lambda n: (n, id_of(lab, n)))
    if name is None:
        raise invalid(f"{kind} {ref!r} not found", field or kind)
    return name


def catalogue_entry_or_404(lab: Lab, kind: str, rid: int, what: str) -> str:
    """Return the name of the catalogue entry with this ID, or raise 404 (`what`)."""
    id_of = ID_OF[kind]
    name = next((n for n in lab.cfg[f"{kind}s"] if id_of(lab, n) == rid), None)
    if name is None:
        raise not_found(what)
    return name


def image_path(lab: Lab, name: str) -> str:
    """Return the qcow2 file of a system image, as loaded at the lab's start."""
    name = catalogue_name(lab, "image", name)
    if not lab.images.get(name):
        raise RuntimeError(
            f"image {name} could not be loaded at the lab's start (see the lab's log)"
        )
    return lab.images[name]


def load_images(lab: Lab) -> None:
    """Load every image of the configuration at the lab's start.

    Each image is taken in the version its checksum file names now, downloaded unless it
    is there already. The files are named by checksum, so servers keep the version they
    were made from; without network the newest one there is used.
    """
    # for the fallback: the newest file there
    directory = lab.fabric.root / "images" / "system"
    for name, src in lab.cfg["images"].items():
        log.info("image %s: checking %s", name, src["url"])
        try:
            lab.images[name] = lab.fabric.fetch_image(
                name, src["url"], src["checksums"]
            )
            log.info("image %s: %s", name, lab.images[name])
        # the lab comes up; servers of this image fail with a clear message
        except Exception as error:  # noqa: BLE001
            have = sorted(
                directory.glob(f"{name}-*.qcow2"), key=lambda p: p.stat().st_mtime
            )
            if have:
                lab.images[name] = str(have[-1])
                log.warning("image %s: %s; using %s", name, error, have[-1])
            else:
                log.error("image %s: not available: %s", name, error)


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the routes of server types, locations, images and datacenters to the app."""
    cfg = lab.cfg

    @app.get("/v1/server_types")
    def server_type_list(request: Request):
        types = [hetzner.server_type_json(lab, n) for n in cfg["server_types"]]
        return paginate(request, filtered(request, types), "server_types")

    @app.get("/v1/server_types/{tid}")
    def server_type_get(tid: int):
        name = catalogue_entry_or_404(lab, "server_type", tid, "server type")
        return {"server_type": hetzner.server_type_json(lab, name)}

    @app.get("/v1/locations")
    def location_list(request: Request):
        return paginate(
            request,
            filtered(
                request, [hetzner.location_json(lab, n) for n in cfg["locations"]]
            ),
            "locations",
        )

    @app.get("/v1/locations/{lid}")
    def location_get(lid: int):
        name = catalogue_entry_or_404(lab, "location", lid, "location")
        return {"location": hetzner.location_json(lab, name)}

    @app.get("/v1/images")
    def image_list(request: Request):
        items = filtered(
            request,
            [hetzner.image_json(lab, n) for n in cfg["images"]],
            {
                "type": lambda image, value: image["type"] in value.split(","),
                # an empty architecture filters nothing
                "architecture": lambda image, value: (
                    not value or image["architecture"] in value.split(",")
                ),
            },
        )
        return paginate(request, items, "images")

    @app.get("/v1/images/{iid}")
    def image_get(iid: int):
        name = catalogue_entry_or_404(lab, "image", iid, "image")
        return {"image": hetzner.image_json(lab, name)}

    # removed by Hetzner on 1 Oct. 2026 (the "datacenter" properties of servers and
    # primary IPs on 1 July 2026)
    @app.get("/v1/datacenters")
    @app.get("/v1/datacenters/{did}")
    def datacenter_removed(did: int = 0):
        raise ApiError(
            410,
            "deprecated_api_endpoint",
            "API functionality was removed",
            {
                "announcement": "https://docs.hetzner.cloud/changelog#2026-07-01-removing-datacenters"
            },
        )
