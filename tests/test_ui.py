"""The web UI: its page and the overview the page polls."""

from conftest import create_network, create_server
from fastapi.testclient import TestClient

from hlabd.api.ui import create_ui_app


def ui_client(api) -> TestClient:
    """Return a client of the web UI on `api`'s loop, which owns the state."""
    client = TestClient(create_ui_app(api.lab))
    client.portal = api.portal
    return client


def test_the_ui_serves_its_page_with_the_right_media_types(api):
    ui = ui_client(api)
    for path, media_type in [
        ("/", "text/html"),
        ("/ui.css", "text/css"),
        ("/ui.js", "text/javascript"),
    ]:
        response = ui.get(path)
        assert response.status_code == 200
        assert response.headers["content-type"].startswith(media_type)
    assert '<script src="/ui.js">' in ui.get("/").text


def test_the_overview_shows_the_resources_and_what_only_the_lab_knows(api):
    network = create_network(api, "10.0.1.0/24")
    server = create_server(api, "web", networks=[network["id"]])["server"]
    api.post("/_lab/clients", json={"name": "alice"})

    overview = ui_client(api).get("/ui/api/overview").json()
    assert overview["lab"] == {"name": "demo", "resetting": False}
    (shown,) = overview["servers"]
    # the API's shape, plus whether the VM runs
    assert shown["name"] == "web" and shown["status"] == "running"
    assert shown["vm"] == "on" and shown["dead"] is False
    assert [n["name"] for n in overview["networks"]] == [network["name"]]
    assert [c["name"] for c in overview["clients"]] == ["alice"]
    commands = [a["command"] for a in overview["actions"]]
    assert "create_server" in commands
    # the newest action first
    ids = [a["id"] for a in overview["actions"]]
    assert ids == sorted(ids, reverse=True)
    assert server["id"] in [s["id"] for s in overview["servers"]]


def test_the_overview_lists_every_kind_even_when_empty(api):
    overview = ui_client(api).get("/ui/api/overview").json()
    for key in [
        "servers",
        "networks",
        "floating_ips",
        "primary_ips",
        "volumes",
        "firewalls",
        "load_balancers",
        "certificates",
        "zones",
        "images",
        "placement_groups",
        "ssh_keys",
        "storage_boxes",
        "clients",
        "faults",
        "actions",
    ]:
        assert overview[key] == [], key
