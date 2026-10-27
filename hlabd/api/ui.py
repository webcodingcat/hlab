"""The web UI: the lab at a glance, in the operator's browser, and its controls.

An app of its own, on its own port (config.UI_PORT), which bin/hlab publishes only on
the host's localhost. It serves one page (hlabd/ui/), the JSON that the page polls,
and the lab's log. The JSON reuses the Cloud API's shapes and adds what only the lab
knows: whether a VM runs, the clients, the faults. The page controls the lab through
the same /_lab routes as the `hlab` command (api/control.py).
"""

import logging
from collections import deque
from pathlib import Path

from fastapi import FastAPI
from fastapi.responses import Response

from ..lab import Lab
from ..records import StorageBox
from ..state import (
    ACTIONS,
    BOX_SNAPSHOTS,
    CERTIFICATES,
    CLIENTS,
    FIREWALLS,
    FLOATING_IPS,
    IMAGES,
    LOAD_BALANCERS,
    NETWORKS,
    PLACEMENT_GROUPS,
    PRIMARY_IPS,
    SERVERS,
    SSH_KEYS,
    STORAGE_BOXES,
    SUBACCOUNTS,
    VOLUMES,
    ZONES,
    Kind,
)
from . import control, hetzner
from .faults import fault_json
from .storagebox.boxes import username
from .storagebox.snapshots import snapshot_json
from .storagebox.subaccounts import subaccount_json
from .web import install_error_handlers

PAGES = Path(__file__).parents[1] / "ui"
# the page's files: path → (file in PAGES, media type)
FILES = {
    "/": ("index.html", "text/html; charset=utf-8"),
    "/ui.css": ("ui.css", "text/css; charset=utf-8"),
    "/ui.js": ("ui.js", "text/javascript; charset=utf-8"),
}
RECENT_ACTIONS = 30  # the newest actions the overview shows
LOG_LINES = 500  # the newest lines of the log the page can get


class LogBuffer(logging.Handler):
    """The newest lines of the lab's log, numbered, for the page's live log.

    Any thread may log. Logging holds the handler's lock while it emits a line, and
    `since` takes the same lock, so the loop reads a whole copy.
    """

    def __init__(self, size: int = LOG_LINES):
        """Start empty; keep at most `size` lines."""
        super().__init__()
        self.lines: deque[tuple[int, str]] = deque(maxlen=size)
        self.count = 0  # the number of the newest line

    def emit(self, record: logging.LogRecord) -> None:
        """Keep a formatted line under the next number."""
        self.count += 1
        self.lines.append((self.count, self.format(record)))

    def since(self, after: int) -> list[dict]:
        """Return the kept lines numbered after `after`, oldest first."""
        self.acquire()
        try:
            return [{"n": n, "text": text} for n, text in self.lines if n > after]
        finally:
            self.release()


def ordered[R](lab: Lab, kind: Kind[R]) -> list[R]:
    """Return the records of a kind, oldest first (by ID)."""
    return [item for _, item in sorted(lab.state[kind].items())]


def box_entry(lab: Lab, box: StorageBox) -> dict:
    """Return a Storage Box as the page shows it, with its sub-accounts and snapshots.

    Not the API's shape: that one reads the disk usage, too slow to poll.
    """
    bid = box["id"]
    return {
        "id": bid,
        "name": box["name"],
        "type": box["type"],
        "location": box["location"],
        "status": box["status"],
        "username": username(bid),
        "subaccounts": [
            subaccount_json(lab, account)
            for account in ordered(lab, SUBACCOUNTS)
            if account["box"] == bid
        ],
        "snapshots": [
            snapshot_json(lab, snapshot)
            for snapshot in ordered(lab, BOX_SNAPSHOTS)
            if snapshot["box"] == bid
        ],
    }


async def overview(lab: Lab) -> dict:
    """Return everything the page shows, as one consistent picture of the state.

    Whether each VM runs is asked first (a script per server); everything else is
    read after the last await, so it all belongs to the same moment.
    """
    alive = {}
    for sid in list(lab.state[SERVERS]):
        alive[sid] = await lab.blocking(lab.fabric.vms.is_alive, sid)
    state = lab.state
    newest_actions = sorted(state[ACTIONS], reverse=True)[:RECENT_ACTIONS]
    return {
        "lab": {"name": lab.cfg["name"], "resetting": lab.resetting},
        "servers": [
            {
                **hetzner.server_json(lab, server),
                "vm": "on" if alive.get(server["id"]) else "off",
                "dead": bool(server.get("_dead")),
            }
            for server in ordered(lab, SERVERS)
        ],
        "networks": [hetzner.network_json(lab, n) for n in ordered(lab, NETWORKS)],
        "floating_ips": [
            hetzner.floating_ip_json(lab, f) for f in ordered(lab, FLOATING_IPS)
        ],
        "primary_ips": [
            hetzner.primary_ip_json(lab, p) for p in ordered(lab, PRIMARY_IPS)
        ],
        "volumes": [hetzner.volume_json(lab, v) for v in ordered(lab, VOLUMES)],
        "firewalls": [hetzner.firewall_json(lab, f) for f in ordered(lab, FIREWALLS)],
        "load_balancers": [
            hetzner.load_balancer_json(lab, b) for b in ordered(lab, LOAD_BALANCERS)
        ],
        "certificates": [
            hetzner.certificate_json(lab, c) for c in ordered(lab, CERTIFICATES)
        ],
        "zones": [hetzner.zone_json(lab, z) for z in ordered(lab, ZONES)],
        "images": [hetzner.own_image_json(lab, i) for i in ordered(lab, IMAGES)],
        "placement_groups": [
            hetzner.placement_group_json(lab, g) for g in ordered(lab, PLACEMENT_GROUPS)
        ],
        "ssh_keys": [hetzner.ssh_key_json(lab, k) for k in ordered(lab, SSH_KEYS)],
        "storage_boxes": [box_entry(lab, b) for b in ordered(lab, STORAGE_BOXES)],
        "clients": ordered(lab, CLIENTS),
        "faults": [fault_json(fault) for fault in state.data["faults"]],
        "netem": dict(state.data["netem"]),
        "api_fault": dict(lab.api_fault),
        "actions": [state[ACTIONS][aid] for aid in newest_actions],
    }


def page_route(body: bytes, media_type: str):
    """Return a route that answers with one of the page's files."""

    async def route() -> Response:
        # asked anew every time: a lab update brings new files under the same names
        headers = {"Cache-Control": "no-cache"}
        return Response(body, media_type=media_type, headers=headers)

    return route


def create_ui_app(lab: Lab, log_buffer: LogBuffer) -> FastAPI:
    """Build the app of the web UI: its page, the overview, the log, the lab control.

    The page's files are read here, at start: once the lab serves, code on the loop
    reads no files.
    """
    app = FastAPI(title="hlab: web UI", docs_url=None, redoc_url=None, openapi_url=None)
    install_error_handlers(app)
    control.add_routes(app, lab)
    for path, (file_name, media_type) in FILES.items():
        body = (PAGES / file_name).read_bytes()
        app.add_api_route(path, page_route(body, media_type), methods=["GET"])

    @app.get("/ui/api/overview")
    async def overview_get():
        return await overview(lab)

    @app.get("/ui/api/log")
    async def log_get(after: int = 0):
        return {"lines": log_buffer.since(after), "newest": log_buffer.count}

    return app
