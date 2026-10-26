"""The fake Hetzner Cloud API.

It covers the part that OpenTofu's hcloud provider and cluster tooling use. Each
resource has its own module (servers, ssh_keys, networks, primary_ips, floating_ips,
firewalls, volumes, catalogue, actions). Lab control (faults, clients, Storage Box
accounts) is under /_lab/ on the same server.
"""

from fastapi import FastAPI

from ..lab import Lab
from . import (
    actions,
    catalogue,
    control,
    firewalls,
    floating_ips,
    networks,
    primary_ips,
    servers,
    ssh_keys,
    volumes,
)
from .web import install


def create_app(lab: Lab) -> FastAPI:
    """Build the app: the fake Cloud API and the lab control routes."""
    app = FastAPI(
        title="hlab: fake Hetzner Cloud API",
        docs_url="/_docs",
        openapi_url="/_openapi.json",
    )
    # auth, rate limit, injected faults, spec checks, error answers
    install(app, lab, "cloud.json", "/v1")
    # first, so that /v1/<kind>/actions is not read as /v1/<kind>/<id>
    actions.add_routes(app, lab)
    for module in (
        catalogue,
        servers,
        ssh_keys,
        networks,
        primary_ips,
        floating_ips,
        firewalls,
        volumes,
        control,
    ):
        module.add_routes(app, lab)
    return app
