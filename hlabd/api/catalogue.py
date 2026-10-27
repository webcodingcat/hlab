"""The catalogue: server, load balancer and Storage Box types, locations, prices.

All of them come from the configuration. The images are loaded when the lab starts;
their routes are in api/images.py, next to the project's own images.
There are no datacenters: Hetzner removed them in 2026.
"""

import logging

from fastapi import FastAPI, Request

from ..errors import ApiError, invalid, not_found
from ..lab import Lab
from . import hetzner
from .web import filtered, paginate

log = logging.getLogger(__name__)

# the function that returns a catalogue entry's ID, per kind; the entries are the
# configuration's "<kind>s" (server_types, locations, images, storage_box_types,
# load_balancer_types)
ID_OF = {
    "server_type": hetzner.server_type_id,
    "location": hetzner.location_id,
    "image": hetzner.image_id,
    "storage_box_type": hetzner.storage_box_type_id,
    "load_balancer_type": hetzner.load_balancer_type_id,
}


def by_name_or_id(items, ref, name_and_id):
    """Return the first item that `ref` names, by name or by ID, or None.

    `name_and_id(item)` gives an item's name and ID. An ID matches as a number or as
    text ("5" and 5 alike), because clients send either.
    """
    return next(
        (item for item in items if str(ref) in map(str, name_and_id(item))), None
    )


def catalogue_name(lab: Lab, kind: str, ref, field: str | None = None) -> str:
    """Return the name of a catalogue entry that a request gives by name or ID.

    Resources store the name: a server stores the name of its image, a box the name
    of its type. The specification types these fields as strings, so the request check
    already refuses objects. An unknown entry raises invalid_input for `field` (by
    default the kind).
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
    """Return the qcow2 file of a system image, as loaded when the lab started."""
    name = catalogue_name(lab, "image", name)
    if not lab.images.get(name):
        raise RuntimeError(
            f"image {name} could not be loaded at the lab's start (see the lab's log)"
        )
    return lab.images[name]


def load_images(lab: Lab) -> None:
    """Load every image of the configuration when the lab starts.

    Each image is used in the version that its checksum file names now. It is
    downloaded unless it is there already. The files are named by checksum, so servers
    keep the version they were created from. Without network access, the newest local
    file is used.
    """
    # for the fallback: the newest local file
    directory = lab.fabric.root / "images" / "system"
    for name, src in lab.cfg["images"].items():
        log.info("image %s: checking %s", name, src["url"])
        try:
            lab.images[name] = lab.fabric.vms.fetch_image(
                name, src["url"], src["checksums"]
            )
            log.info("image %s: %s", name, lab.images[name])
        # the lab still starts; servers with this image fail with a clear message
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
    """Add the routes of the types, prices, locations and datacenters."""
    cfg = lab.cfg

    @app.get("/v1/server_types")
    async def server_type_list(request: Request):
        types = [hetzner.server_type_json(lab, n) for n in cfg["server_types"]]
        return paginate(request, filtered(request, types), "server_types")

    @app.get("/v1/server_types/{tid}")
    async def server_type_get(tid: int):
        name = catalogue_entry_or_404(lab, "server_type", tid, "server type")
        return {"server_type": hetzner.server_type_json(lab, name)}

    @app.get("/v1/load_balancer_types")
    async def load_balancer_type_list(request: Request):
        types = [
            hetzner.load_balancer_type_json(lab, n) for n in cfg["load_balancer_types"]
        ]
        return paginate(request, filtered(request, types), "load_balancer_types")

    @app.get("/v1/load_balancer_types/{tid}")
    async def load_balancer_type_get(tid: int):
        name = catalogue_entry_or_404(
            lab, "load_balancer_type", tid, "load balancer type"
        )
        return {"load_balancer_type": hetzner.load_balancer_type_json(lab, name)}

    @app.get("/v1/pricing")
    async def pricing_get():
        return {"pricing": hetzner.pricing_json(lab)}

    @app.get("/v1/locations")
    async def location_list(request: Request):
        return paginate(
            request,
            filtered(
                request, [hetzner.location_json(lab, n) for n in cfg["locations"]]
            ),
            "locations",
        )

    @app.get("/v1/locations/{lid}")
    async def location_get(lid: int):
        name = catalogue_entry_or_404(lab, "location", lid, "location")
        return {"location": hetzner.location_json(lab, name)}

    # removed by Hetzner on 1 Oct. 2026 (the "datacenter" properties of servers and
    # primary IPs on 1 July 2026)
    @app.get("/v1/datacenters")
    @app.get("/v1/datacenters/{did}")
    async def datacenter_removed(did: int = 0):
        raise ApiError(
            410,
            "deprecated_api_endpoint",
            "API functionality was removed",
            {
                "announcement": "https://docs.hetzner.cloud/changelog#2026-07-01-removing-datacenters"
            },
        )
