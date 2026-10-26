"""The fake Hetzner Cloud API.

It covers the part OpenTofu's hcloud provider and cluster tooling use. Each resource
lives in its own module (servers, networks, ips, firewalls, volumes, catalogue,
actions); lab control (faults, clients, Storage Box accounts) lives under /_lab/ on the
same server.
"""

from fastapi import FastAPI

from ..lab import Lab
from . import actions, catalogue, control, firewalls, ips, networks, servers, volumes
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
    # first: /v1/<kind>/actions must not be taken for /v1/<kind>/<id>
    actions.add_routes(app, lab)
    for module in (catalogue, servers, networks, ips, firewalls, volumes, control):
        module.add_routes(app, lab)
    return app
