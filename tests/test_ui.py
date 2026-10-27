"""The web UI: its page, the overview the page polls, the lab control, the log."""

import logging

from conftest import create_network, create_server, state_of
from fastapi.testclient import TestClient

from hlabd import config
from hlabd.api.ui import LogBuffer, create_ui_app
from hlabd.daemon import http_app


def ui_client(api, log_buffer: LogBuffer | None = None) -> TestClient:
    """Return a client of the web UI on `api`'s loop, which owns the state."""
    client = TestClient(create_ui_app(api.lab, log_buffer or LogBuffer()))
    client.portal = api.portal
    return client


def test_the_page_controls_the_lab_through_the_same_lab_routes(api):
    ui = ui_client(api)
    create_server(api, "web")
    assert ui.post("/_lab/clients", json={"name": "alice"}).status_code == 200
    response = ui.post("/_lab/faults", json={"type": "isolate", "vm": "web"})
    assert response.status_code == 200, response.text
    assert [c["name"] for c in state_of(api, "clients").values()] == ["alice"]
    assert [f["type"] for f in state_of(api, "faults")] == ["isolate"]
    # errors in Hetzner's shape, which the page shows
    response = ui.post("/_lab/faults", json={"type": "flood"})
    assert response.status_code == 422
    assert "fault type must be one of" in response.json()["error"]["message"]


def test_the_log_gives_the_newest_lines_after_a_number():
    log_buffer = LogBuffer(size=3)
    logger = logging.getLogger("hlabd.test_ui")
    logger.addHandler(log_buffer)
    try:
        for n in range(1, 6):
            logger.warning("line %s", n)
    finally:
        logger.removeHandler(log_buffer)
    # only the newest three are kept, numbered as they came
    assert [line["n"] for line in log_buffer.since(0)] == [3, 4, 5]
    assert [line["text"] for line in log_buffer.since(4)] == ["line 5"]
    assert log_buffer.count == 5


def test_the_log_route_answers_the_lines_and_the_newest_number(api):
    log_buffer = LogBuffer()
    log_buffer.handle(logging.makeLogRecord({"msg": "hello", "levelno": 20}))
    ui = ui_client(api, log_buffer)
    assert ui.get("/ui/api/log").json() == {
        "lines": [{"n": 1, "text": "hello"}],
        "newest": 1,
    }
    assert ui.get("/ui/api/log?after=1").json() == {"lines": [], "newest": 1}


def test_the_pages_polling_stays_out_of_the_log(api, caplog):
    # the whole HTTP server, as the daemon runs it, reached on the web UI's port
    client = TestClient(
        http_app(api.lab, LogBuffer()), base_url=f"http://localhost:{config.UI_PORT}"
    )
    client.portal = api.portal
    with caplog.at_level(logging.INFO, logger="hlabd.daemon"):
        assert client.get("/ui/api/overview").status_code == 200
        assert client.post("/_lab/clients", json={"name": "bob"}).status_code == 200
    logged = [r.getMessage() for r in caplog.records if r.name == "hlabd.daemon"]
    assert not any("/ui/api/overview" in line for line in logged)
    assert any("POST /_lab/clients 200" in line for line in logged)


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
        assert response.headers["cache-control"] == "no-cache"
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
