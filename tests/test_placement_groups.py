"""Placement groups: create, list, servers in them, Hetzner's limits."""

from conftest import create_server, power_off, wait_action

from hlabd.api import placement_groups


def create_group(api, name="spread-1", **extra):
    """Create a placement group of type spread and return it."""
    response = api.post(
        "/v1/placement_groups", json={"name": name, "type": "spread", **extra}
    )
    assert response.status_code == 201, response.text
    assert response.json()["action"] is None  # Hetzner creates it without an action
    return response.json()["placement_group"]


def server_action(api, sid, cmd, body=None):
    """Run a server action; return the response."""
    return api.post(f"/v1/servers/{sid}/actions/{cmd}", json=body or {})


def test_placement_group_is_created_listed_renamed_and_deleted(api):
    group = create_group(api, labels={"env": "lab"})
    assert group["servers"] == [] and group["type"] == "spread"
    duplicate = api.post(
        "/v1/placement_groups", json={"name": "spread-1", "type": "spread"}
    )
    assert duplicate.json()["error"]["code"] == "uniqueness_error"
    listed = api.get(
        "/v1/placement_groups", params={"type": "spread", "label_selector": "env=lab"}
    ).json()["placement_groups"]
    assert [g["id"] for g in listed] == [group["id"]]
    renamed = api.put(
        f"/v1/placement_groups/{group['id']}", json={"name": "spread-2"}
    ).json()["placement_group"]
    assert renamed["name"] == "spread-2"
    assert api.delete(f"/v1/placement_groups/{group['id']}").status_code == 204
    assert api.get(f"/v1/placement_groups/{group['id']}").status_code == 404


def test_server_created_in_a_group_shows_it_and_leaves_it_when_deleted(api):
    group = create_group(api)
    server = create_server(api, "a", placement_group=group["id"])["server"]
    assert server["placement_group"]["id"] == group["id"]
    path = f"/v1/placement_groups/{group['id']}"
    assert api.get(path).json()["placement_group"]["servers"] == [server["id"]]
    # not checked against Hetzner: a group with servers cannot be deleted
    assert api.delete(path).json()["error"]["code"] == "resource_in_use"
    wait_action(api, api.delete(f"/v1/servers/{server['id']}").json()["action"])
    assert api.get(path).json()["placement_group"]["servers"] == []


def test_a_server_joins_a_group_only_when_off_and_only_one(api):
    # Hetzner: add_to_placement_group needs the server off (server_not_stopped) and
    # refuses a server in a group (already_in_placement_group)
    first, second = create_group(api, "g1"), create_group(api, "g2")
    sid = create_server(api, "a")["server"]["id"]
    add = {"placement_group": first["id"]}
    response = server_action(api, sid, "add_to_placement_group", add)
    assert response.json()["error"]["code"] == "server_not_stopped"
    power_off(api, sid)
    action = server_action(api, sid, "add_to_placement_group", add).json()["action"]
    assert {"id": first["id"], "type": "placement_group"} in action["resources"]
    wait_action(api, action)
    response = server_action(
        api, sid, "add_to_placement_group", {"placement_group": second["id"]}
    )
    assert response.json()["error"]["code"] == "already_in_placement_group"
    wait_action(
        api, server_action(api, sid, "remove_from_placement_group").json()["action"]
    )
    assert api.get(f"/v1/servers/{sid}").json()["server"]["placement_group"] is None


def test_a_full_group_refuses_another_server(api, monkeypatch):
    monkeypatch.setattr(placement_groups, "SERVERS_PER_GROUP", 1)
    group = create_group(api)
    create_server(api, "a", placement_group=group["id"])
    response = api.post(
        "/v1/servers",
        json={
            "name": "b",
            "server_type": "cx23",
            "image": "debian-13",
            "placement_group": group["id"],
        },
    )
    assert response.json()["error"]["code"] == "placement_error"


def test_a_project_has_a_limited_number_of_groups(api, monkeypatch):
    monkeypatch.setattr(placement_groups, "GROUPS_PER_PROJECT", 1)
    create_group(api, "g1")
    response = api.post("/v1/placement_groups", json={"name": "g2", "type": "spread"})
    assert response.json()["error"]["code"] == "resource_limit_exceeded"
