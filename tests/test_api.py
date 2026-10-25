"""The API itself: authentication, catalogue, validation, pagination, rate limit, actions."""

import asyncio
import time

import pytest
import yaml
from fastapi.testclient import TestClient

from conftest import create_server, make_key, network, off, wait_action
from hlabd.api.app import create_app
from hlabd.api.web import RateLimit


def test_auth_required(api):
    r = api.get("/v1/servers", headers={"Authorization": "Bearer wrong"})
    assert r.status_code == 401
    assert r.json()["error"]["code"] == "unauthorized"


def test_catalogue(api):
    types = api.get("/v1/server_types", params={"name": "cx33"}).json()["server_types"]
    assert [t["name"] for t in types] == ["cx33"]
    assert types[0]["memory"] == 8.0
    locs = api.get("/v1/locations").json()["locations"]
    assert {l["name"] for l in locs} == {"fsn1", "nbg1", "hel1"}
    assert next(l for l in locs if l["name"] == "fsn1")["id"] == 1
    imgs = api.get("/v1/images", params={"name": "debian-13", "architecture": "x86"}).json()["images"]
    assert imgs[0]["os_flavor"] == "debian"


def test_injected_api_faults(api):
    api.post("/_lab/api-fault", json={"status": 503, "count": 2})
    assert api.get("/v1/servers").status_code == 503
    assert api.get("/v1/servers").status_code == 503
    assert api.get("/v1/servers").status_code == 200


def test_pagination(api):
    for i in range(3):
        api.post("/v1/ssh_keys", json={"name": f"k{i}", "public_key": make_key(i)})
    r = api.get("/v1/ssh_keys", params={"per_page": 2}).json()
    assert len(r["ssh_keys"]) == 2
    assert r["meta"]["pagination"]["next_page"] == 2
    assert r["meta"]["pagination"]["total_entries"] == 3


def test_datacenters_are_gone(api):
    for path in ("/v1/datacenters", "/v1/datacenters/1"):
        r = api.get(path)
        assert r.status_code == 410 and r.json()["error"]["code"] == "deprecated_api_endpoint"
    s = create_server(api, "nodc")["server"]
    assert "datacenter" not in s and s["location"]["name"] == "fsn1"
    assert "datacenter" not in api.get("/v1/primary_ips").json()["primary_ips"][0]


def test_only_the_newest_images(api):
    names = [i["name"] for i in api.get("/v1/images?type=system").json()["images"]]
    assert names == ["debian-13", "ubuntu-26.04"]


def test_configuration_typos_are_reported(tmp_path):
    import pytest
    from hlabd import config
    f = tmp_path / "hlab.yaml"
    f.write_text("name: x\nstorage_boxes: {enabled: false}\n")
    with pytest.raises(ValueError, match="unknown settings storage_boxes"):
        config.load(f)
    f.write_text("name: x\nstorage_box: true\n")
    with pytest.raises(ValueError, match="storage_box must be a mapping"):
        config.load(f)


def test_null_where_an_id_belongs(api):
    f = api.post("/v1/floating_ips", json={"type": "ipv4", "home_location": "fsn1"}).json()["floating_ip"]
    r = api.post(f"/v1/floating_ips/{f['id']}/actions/assign", json={"server": None})
    assert r.status_code == 422 and r.json()["error"]["code"] == "invalid_input"
    s = create_server(api, "a")["server"]   # the Floating IP is not left locked
    r = api.post(f"/v1/floating_ips/{f['id']}/actions/assign", json={"server": s["id"]})
    assert wait_action(api, r.json()["action"])["status"] == "success"
    assert api.post(f"/v1/servers/{s['id']}/actions/attach_to_network", json={"network": None}).status_code == 422
    assert api.post("/v1/volumes", json={"name": "v", "size": None, "location": "fsn1"}).status_code == 422


def test_action_routes_of_every_resource(api):
    s = create_server(api, "a")
    for kind in ("volumes", "primary_ips", "servers", "networks", "floating_ips", "firewalls"):
        assert api.get(f"/v1/{kind}/actions").status_code == 200, kind
    aid = s["action"]["id"]   # a server's action is no volume's action
    assert api.get(f"/v1/volumes/actions/{aid}").status_code == 404
    assert api.get(f"/v1/servers/actions/{aid}").status_code == 200
    assert api.get(f"/v1/servers/{s['server']['id']}/actions/{aid}").status_code == 200


def test_rate_limit_buckets_stay_bounded():
    r = RateLimit(3600)
    for i in range(5000):
        r.take(f"token{i}")
    assert len(r.buckets) <= 1000


def test_actions_do_not_pile_up(lab):
    for _ in range(10050):
        lab.new_action("x", [])["status"] = "success"   # finished (without saving the state each time)
    assert len(lab.state["actions"]) <= 10000


def test_cancelled_action_is_an_error(lab):
    async def go():
        lab.loop = asyncio.get_running_loop()
        a = lab.new_action("x", [])

        async def steps():
            await asyncio.sleep(10)
        lab.run_action(a, steps)
        await asyncio.sleep(0)
        for t in list(lab.tasks):
            t.cancel()
        await asyncio.sleep(0.05)
        return a
    assert asyncio.run(go())["status"] == "error"


@pytest.mark.parametrize("labels", [
    {"-bad": "x"}, {"ok": "-bad"}, {"hetzner.cloud/x": "y"}, {"Bad_Prefix!/x": "y"}, {"k": "x" * 64},
])
def test_invalid_labels(api, labels):
    r = api.post("/v1/ssh_keys", json={"name": "k", "public_key": "ssh-ed25519 AAAA", "labels": labels})
    assert r.status_code == 422
    err = r.json()["error"]
    assert err["code"] == "invalid_input" and err["details"]["fields"][0]["name"] == "labels"


def test_valid_labels(api):
    create_server(api, "lbl", labels={"environment": "prod", "example.com/my": "label", "just-a-key": ""})


@pytest.mark.parametrize("name", ["under_score", "-dash", "x" * 64, "a..b", ""])
def test_server_name_must_be_hostname(api, name):
    r = api.post("/v1/servers", json={"name": name, "server_type": "cx23", "image": "debian-13"})
    assert r.status_code == 422 and r.json()["error"]["details"]["fields"][0]["name"] == "name"


def test_schema_errors_name_the_field(api):
    r = api.post("/v1/servers", json={"name": "x", "server_type": "cx23", "image": "debian-13", "start_after_create": "yes"})
    err = r.json()["error"]
    assert r.status_code == 422 and err["code"] == "invalid_input"
    assert err["details"]["fields"][0]["name"] == "start_after_create"
    r = api.post("/v1/networks", json={"name": "n"})
    assert r.json()["error"]["details"]["fields"][0]["name"] == "ip_range"


def test_json_error(api):
    r = api.post("/v1/servers", content=b"{nope", headers={"Content-Type": "application/json"})
    assert r.status_code == 400 and r.json()["error"]["code"] == "json_error"


def test_unknown_route_and_method(api):
    r = api.get("/v1/nothing")
    assert r.status_code == 404 and r.json()["error"]["code"] == "not_found"
    r = api.patch("/v1/servers")
    assert r.status_code == 405 and r.json()["error"]["code"] == "method_not_allowed"
    assert api.get("/v1/servers/abc").json()["error"]["code"] == "not_found"


def test_sorting(api):
    for n in ("b", "a", "c"):
        create_server(api, n)
    names = lambda q: [s["name"] for s in api.get(f"/v1/servers?{q}").json()["servers"]]
    assert names("sort=name") == ["a", "b", "c"]
    assert names("sort=name:desc") == ["c", "b", "a"]
    assert names("sort=id:desc") == ["c", "a", "b"]
    assert names("sort=name:asc&sort=name:desc") == ["b", "a", "c"]   # contrary orders are ignored
    r = api.get("/v1/servers?sort=status")
    assert r.status_code == 422 and r.json()["error"]["details"]["fields"][0]["name"] == "sort"


def test_rate_limit(lab):
    lab.cfg["api"]["rate_limit"] = 3
    with TestClient(create_app(lab), headers={"Authorization": "Bearer secret"}) as c:
        codes = [c.get("/v1/locations") for _ in range(4)]
    assert [r.status_code for r in codes] == [200, 200, 200, 429]
    assert codes[0].headers["RateLimit-Limit"] == "3"
    assert codes[2].headers["RateLimit-Remaining"] == "0"
    assert codes[3].json()["error"]["code"] == "rate_limit_exceeded"
    assert int(codes[3].headers["RateLimit-Reset"]) > 0


def test_values_that_must_not_get_through(api):
    s = {"name": "srv\n", "server_type": "cx23", "image": "debian-13", "location": "fsn1"}
    assert api.post("/v1/servers", json=s).status_code == 422                          # a trailing newline
    create_server(api, "a")
    r = api.post("/_lab/netem", json={"vm": "a", "args": "delay foo; reboot"})
    assert r.status_code == 422


def test_names_unique_on_rename(api):
    api.post("/v1/networks", json={"name": "a", "ip_range": "10.0.0.0/16"})
    assert api.post("/v1/networks", json={"name": "a", "ip_range": "10.1.0.0/16"}).status_code == 409
    b = api.post("/v1/networks", json={"name": "b", "ip_range": "10.1.0.0/16"}).json()["network"]
    assert api.put(f"/v1/networks/{b['id']}", json={"name": "a"}).status_code == 409


def test_filters_given_several_times(api):
    a = create_server(api, "a")["server"]
    b = create_server(api, "b")["server"]
    off(api, b["id"])
    r = api.get("/v1/servers?status=running&status=off").json()["servers"]
    assert sorted(s["id"] for s in r) == sorted([a["id"], b["id"]])
