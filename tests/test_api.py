"""The API itself.

Authentication, catalogue, validation, pagination, sorting, filters, actions.
"""

import asyncio

import pytest
from conftest import create_server, make_key, power_off, wait_action

from hlabd.lab import MAX_ACTIONS


def test_requests_without_a_valid_token_are_unauthorized(api):
    response = api.get("/v1/servers", headers={"Authorization": "Bearer wrong"})
    assert response.status_code == 401
    assert response.json()["error"]["code"] == "unauthorized"


def test_catalogue_lists_server_types_locations_and_images(api):
    types = api.get("/v1/server_types", params={"name": "cx33"}).json()["server_types"]
    assert [t["name"] for t in types] == ["cx33"]
    assert types[0]["memory"] == 8.0
    locs = api.get("/v1/locations").json()["locations"]
    assert {loc["name"] for loc in locs} == {"fsn1", "nbg1", "hel1"}
    assert next(loc for loc in locs if loc["name"] == "fsn1")["id"] == 1
    imgs = api.get(
        "/v1/images", params={"name": "debian-13", "architecture": "x86"}
    ).json()["images"]
    assert imgs[0]["os_flavor"] == "debian"


def test_lists_are_paginated(api):
    for i in range(3):
        api.post("/v1/ssh_keys", json={"name": f"k{i}", "public_key": make_key(i)})
    response = api.get("/v1/ssh_keys", params={"per_page": 2}).json()
    assert len(response["ssh_keys"]) == 2
    assert response["meta"]["pagination"]["next_page"] == 2
    assert response["meta"]["pagination"]["total_entries"] == 3


def test_datacenters_are_gone(api):
    for path in ("/v1/datacenters", "/v1/datacenters/1"):
        response = api.get(path)
        assert (
            response.status_code == 410
            and response.json()["error"]["code"] == "deprecated_api_endpoint"
        )
    server = create_server(api, "nodc")["server"]
    assert "datacenter" not in server and server["location"]["name"] == "fsn1"
    assert "datacenter" not in api.get("/v1/primary_ips").json()["primary_ips"][0]


def test_only_the_newest_images_are_offered(api):
    names = [i["name"] for i in api.get("/v1/images?type=system").json()["images"]]
    assert names == ["debian-13", "ubuntu-26.04"]


def test_null_where_an_id_belongs_is_invalid_input(api):
    floating_ip = api.post(
        "/v1/floating_ips", json={"type": "ipv4", "home_location": "fsn1"}
    ).json()["floating_ip"]
    response = api.post(
        f"/v1/floating_ips/{floating_ip['id']}/actions/assign", json={"server": None}
    )
    assert (
        response.status_code == 422
        and response.json()["error"]["code"] == "invalid_input"
    )
    server = create_server(api, "a")["server"]  # the Floating IP is not left locked
    response = api.post(
        f"/v1/floating_ips/{floating_ip['id']}/actions/assign",
        json={"server": server["id"]},
    )
    assert wait_action(api, response.json()["action"])["status"] == "success"
    assert (
        api.post(
            f"/v1/servers/{server['id']}/actions/attach_to_network",
            json={"network": None},
        ).status_code
        == 422
    )
    assert (
        api.post(
            "/v1/volumes", json={"name": "v", "size": None, "location": "fsn1"}
        ).status_code
        == 422
    )


def test_every_resource_has_its_own_action_routes(api):
    server = create_server(api, "a")
    for kind in (
        "volumes",
        "primary_ips",
        "servers",
        "networks",
        "floating_ips",
        "firewalls",
    ):
        assert api.get(f"/v1/{kind}/actions").status_code == 200, kind
    aid = server["action"]["id"]  # a server's action is no volume's action
    assert api.get(f"/v1/volumes/actions/{aid}").status_code == 404
    assert api.get(f"/v1/servers/actions/{aid}").status_code == 200
    assert (
        api.get(f"/v1/servers/{server['server']['id']}/actions/{aid}").status_code
        == 200
    )


def test_actions_do_not_pile_up(lab):
    running = lab.new_action("x", [])
    with lab.state.change():  # one block: the state is saved once
        for _ in range(MAX_ACTIONS + 50):
            lab.new_action("x", [])["status"] = "success"  # finished
    assert len(lab.state["actions"]) <= MAX_ACTIONS
    assert running["id"] in lab.state["actions"]  # a running action stays


def test_cancelled_action_is_an_error(lab):
    async def go():
        action = lab.new_action("x", [])

        async def steps():
            await asyncio.sleep(10)

        lab.run_action(action, steps)
        await asyncio.sleep(0)
        await lab.stop_actions()  # cancels the steps and waits for the finish
        return action

    assert asyncio.run(go())["status"] == "error"


@pytest.mark.parametrize(
    "labels",
    [
        {"-bad": "x"},
        {"ok": "-bad"},
        {"hetzner.cloud/x": "y"},
        {"Bad_Prefix!/x": "y"},
        {"k": "x" * 64},
    ],
)
def test_invalid_labels_are_refused(api, labels):
    response = api.post(
        "/v1/ssh_keys",
        json={"name": "k", "public_key": "ssh-ed25519 AAAA", "labels": labels},
    )
    assert response.status_code == 422
    err = response.json()["error"]
    assert (
        err["code"] == "invalid_input"
        and err["details"]["fields"][0]["name"] == "labels"
    )


def test_valid_labels_are_accepted(api):
    create_server(
        api,
        "lbl",
        labels={"environment": "prod", "example.com/my": "label", "just-a-key": ""},
    )


@pytest.mark.parametrize("name", ["under_score", "-dash", "x" * 64, "a..b", ""])
def test_server_name_must_be_a_hostname(api, name):
    response = api.post(
        "/v1/servers", json={"name": name, "server_type": "cx23", "image": "debian-13"}
    )
    assert (
        response.status_code == 422
        and response.json()["error"]["details"]["fields"][0]["name"] == "name"
    )


def test_schema_errors_name_the_field(api):
    response = api.post(
        "/v1/servers",
        json={
            "name": "x",
            "server_type": "cx23",
            "image": "debian-13",
            "start_after_create": "yes",
        },
    )
    err = response.json()["error"]
    assert response.status_code == 422 and err["code"] == "invalid_input"
    assert err["details"]["fields"][0]["name"] == "start_after_create"
    response = api.post("/v1/networks", json={"name": "n"})
    assert response.json()["error"]["details"]["fields"][0]["name"] == "ip_range"


def test_malformed_json_is_a_json_error(api):
    response = api.post(
        "/v1/servers", content=b"{nope", headers={"Content-Type": "application/json"}
    )
    assert (
        response.status_code == 400 and response.json()["error"]["code"] == "json_error"
    )


def test_malformed_json_on_lab_control_is_a_json_error_too(api):
    # /_lab has no middleware: read_body itself refuses the body
    response = api.post(
        "/_lab/api-fault",
        content=b"{nope",
        headers={"Content-Type": "application/json"},
    )
    assert (
        response.status_code == 400 and response.json()["error"]["code"] == "json_error"
    )


def test_unknown_routes_and_methods_get_hetzners_errors(api):
    response = api.get("/v1/nothing")
    assert (
        response.status_code == 404 and response.json()["error"]["code"] == "not_found"
    )
    response = api.patch("/v1/servers")
    assert (
        response.status_code == 405
        and response.json()["error"]["code"] == "method_not_allowed"
    )
    assert api.get("/v1/servers/abc").json()["error"]["code"] == "not_found"


def test_lists_sort_by_the_given_fields(api):
    for n in ("b", "a", "c"):
        create_server(api, n)

    def names(query):
        return [s["name"] for s in api.get(f"/v1/servers?{query}").json()["servers"]]

    assert names("sort=name") == ["a", "b", "c"]
    assert names("sort=name:desc") == ["c", "b", "a"]
    assert names("sort=id:desc") == ["c", "a", "b"]
    # contrary orders are ignored
    assert names("sort=name:asc&sort=name:desc") == ["b", "a", "c"]
    response = api.get("/v1/servers?sort=status")
    assert (
        response.status_code == 422
        and response.json()["error"]["details"]["fields"][0]["name"] == "sort"
    )


def test_dangerous_values_do_not_get_through(api):
    body = {
        "name": "srv\n",
        "server_type": "cx23",
        "image": "debian-13",
        "location": "fsn1",
    }
    assert api.post("/v1/servers", json=body).status_code == 422  # a trailing newline
    create_server(api, "a")
    response = api.post("/_lab/netem", json={"vm": "a", "args": "delay foo; reboot"})
    assert response.status_code == 422


def test_names_stay_unique_on_rename(api):
    api.post("/v1/networks", json={"name": "a", "ip_range": "10.0.0.0/16"})
    assert (
        api.post(
            "/v1/networks", json={"name": "a", "ip_range": "10.1.0.0/16"}
        ).status_code
        == 409
    )
    network_b = api.post(
        "/v1/networks", json={"name": "b", "ip_range": "10.1.0.0/16"}
    ).json()["network"]
    assert (
        api.put(f"/v1/networks/{network_b['id']}", json={"name": "a"}).status_code
        == 409
    )


def test_filters_given_several_times_match_any_value(api):
    server_a = create_server(api, "a")["server"]
    server_b = create_server(api, "b")["server"]
    power_off(api, server_b["id"])
    response = api.get("/v1/servers?status=running&status=off").json()["servers"]
    assert sorted(s["id"] for s in response) == sorted([server_a["id"], server_b["id"]])


def test_actions_are_listed_per_type_not_for_the_whole_project(api):
    create_server(api, "a")
    assert api.get("/v1/actions").status_code == 422
    acts = api.get("/v1/servers/actions").json()["actions"]
    assert acts and all(
        any(x["type"] == "server" for x in a["resources"]) for a in acts
    )
    assert api.get("/v1/servers/9999/actions").status_code == 404


def test_unreadable_pagination_falls_back_to_the_defaults(api):
    assert (
        api.get("/v1/servers?page=abc&per_page=x").json()["meta"]["pagination"][
            "per_page"
        ]
        == 25
    )
