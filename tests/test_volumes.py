"""Volumes: create, attach, detach, resize, and their busy states."""

import pytest
from conftest import create_server, power_off, wait_action

from hlabd.api import ips, volumes
from hlabd.errors import limit_exceeded


def test_volume_is_created_attached_resized_and_deleted(api, tmp_path):
    server = create_server(api, "a")["server"]
    response = api.post(
        "/v1/volumes",
        json={"name": "data", "size": 10, "location": "fsn1", "format": "ext4"},
    )
    assert response.status_code == 201, response.text
    volume = response.json()["volume"]
    wait_action(api, response.json()["action"])
    path = str(tmp_path / "volumes" / f"{volume['id']}.raw")
    assert ("volume-create", path, "10", "ext4") in api.sh.calls
    assert (
        api.get(f"/v1/volumes/{volume['id']}").json()["volume"]["status"] == "available"
    )
    assert volume["linux_device"] == f"/dev/disk/by-id/scsi-0HC_Volume_{volume['id']}"

    wait_action(
        api,
        api.post(
            f"/v1/volumes/{volume['id']}/actions/attach",
            json={"server": server["id"], "automount": True},
        ).json()["action"],
    )
    assert (
        "volume-attach",
        str(tmp_path / "vm" / str(server["id"])),
        str(volume["id"]),
        path,
    ) in api.sh.calls
    assert api.get(f"/v1/servers/{server['id']}").json()["server"]["volumes"] == [
        volume["id"]
    ]
    assert (
        api.delete(f"/v1/volumes/{volume['id']}").json()["error"]["code"]
        == "volume_attached"
    )

    response = api.post(f"/v1/volumes/{volume['id']}/actions/resize", json={"size": 5})
    assert response.status_code == 422
    wait_action(
        api,
        api.post(
            f"/v1/volumes/{volume['id']}/actions/resize", json={"size": 20}
        ).json()["action"],
    )
    assert api.get(f"/v1/volumes/{volume['id']}").json()["volume"]["size"] == 20

    # boots with the volume after a power cycle
    power_off(api, server["id"])
    wait_action(
        api, api.post(f"/v1/servers/{server['id']}/actions/poweron").json()["action"]
    )
    assert (
        f"{volume['id']} {path}"
        in (tmp_path / "vm" / str(server["id"]) / "volumes").read_text()
    )

    wait_action(
        api, api.post(f"/v1/volumes/{volume['id']}/actions/detach").json()["action"]
    )
    assert api.get(f"/v1/volumes/{volume['id']}").json()["volume"]["server"] is None
    assert api.delete(f"/v1/volumes/{volume['id']}").status_code == 204


def test_volume_size_and_location_follow_hetzners_rules(api):
    server = create_server(api, "a", location="nbg1")["server"]
    assert (
        api.post(
            "/v1/volumes", json={"name": "x", "size": 5, "location": "fsn1"}
        ).status_code
        == 422
    )
    volume = api.post(
        "/v1/volumes", json={"name": "x", "size": 10, "location": "fsn1"}
    ).json()
    wait_action(api, volume["action"])
    response = api.post(
        f"/v1/volumes/{volume['volume']['id']}/actions/attach",
        json={"server": server["id"]},
    )
    assert response.json()["error"]["code"] == "location_mismatch"
    response = api.post(
        "/v1/volumes",
        json={
            "name": "y",
            "size": 10,
            "server": server["id"],
            "automount": True,
            "format": "xfs",
        },
    )
    assert (
        response.json()["volume"]["server"] == server["id"]
        and response.json()["next_actions"][0]["command"] == "attach_volume"
    )


def test_server_created_with_a_volume_frees_it_when_deleted(api):
    volume = api.post(
        "/v1/volumes", json={"name": "z", "size": 10, "location": "fsn1"}
    ).json()
    wait_action(api, volume["action"])
    server = create_server(
        api, "with-vol", volumes=[volume["volume"]["id"]], automount=False
    )["server"]
    assert server["volumes"] == [volume["volume"]["id"]]
    wait_action(api, api.delete(f"/v1/servers/{server['id']}").json()["action"])
    assert (
        api.get(f"/v1/volumes/{volume['volume']['id']}").json()["volume"]["server"]
        is None
    )


def test_refused_volume_leaves_nothing(api):
    server = create_server(api, "a")["server"]
    response = api.post(
        "/v1/volumes",
        json={"name": "data", "size": 10, "location": "nbg1", "server": server["id"]},
    )
    assert response.status_code == 422
    assert api.get("/v1/volumes").json()["volumes"] == []
    assert (
        api.post(
            "/v1/volumes", json={"name": "data", "size": 10, "server": server["id"]}
        ).status_code
        == 201
    )
    volume = api.post(
        "/v1/volumes", json={"name": "v2", "size": 10, "location": "fsn1"}
    ).json()["volume"]
    response = api.post(
        "/v1/servers",
        json={
            "name": "b",
            "server_type": "cx23",
            "image": "debian-13",
            "location": "fsn1",
            "volumes": [volume["id"], volume["id"]],
        },
    )
    assert response.status_code == 422
    assert api.get(f"/v1/volumes/{volume['id']}").json()["volume"]["server"] is None


def test_failed_volume_create_leaves_no_volume(api):
    api.sh.fail.add("volume-create")
    response = api.post(
        "/v1/volumes", json={"name": "v", "size": 10, "location": "fsn1"}
    ).json()
    assert wait_action(api, response["action"])["status"] == "error"
    assert api.get("/v1/volumes").json()["volumes"] == []


def test_volume_runs_one_action_at_a_time(api):
    volume = api.post(
        "/v1/volumes", json={"name": "v", "size": 10, "location": "fsn1"}
    ).json()
    wait_action(api, volume["action"])
    vid = volume["volume"]["id"]
    # not attached
    assert api.post(f"/v1/volumes/{vid}/actions/detach").status_code == 422
    api.lab.state["volumes"][vid]["_busy"] = True
    assert (
        api.post(f"/v1/volumes/{vid}/actions/resize", json={"size": 20}).status_code
        == 423
    )
    api.lab.state["volumes"][vid].pop("_busy")
    server = create_server(api, "a")["server"]
    api.sh.fail.add("volume-attach")  # the hot-plug fails: the volume is not attached
    response = api.post(
        f"/v1/volumes/{vid}/actions/attach", json={"server": server["id"]}
    )
    assert wait_action(api, response.json()["action"])["status"] == "error"
    volume = api.get(f"/v1/volumes/{vid}").json()["volume"]
    assert volume["server"] is None
    api.sh.fail.discard("volume-attach")
    # and not left busy
    response = api.post(
        f"/v1/volumes/{vid}/actions/attach", json={"server": server["id"]}
    )
    assert wait_action(api, response.json()["action"])["status"] == "success"


def test_volumes_wait_for_a_busy_server_and_are_not_deleted_while_busy(api):
    server = create_server(api, "a")["server"]
    volume = api.post(
        "/v1/volumes", json={"name": "v", "size": 10, "location": "fsn1"}
    ).json()
    wait_action(api, volume["action"])
    vid = volume["volume"]["id"]
    # an action of the server runs
    api.lab.state["servers"][server["id"]]["locked"] = True
    assert (
        api.post(
            f"/v1/volumes/{vid}/actions/attach", json={"server": server["id"]}
        ).status_code
        == 423
    )
    api.lab.state["servers"][server["id"]]["locked"] = False
    api.lab.state["volumes"][vid]["_busy"] = True
    assert api.delete(f"/v1/volumes/{vid}").status_code == 423
    api.lab.state["volumes"][vid].pop("_busy")
    response = api.post(
        f"/v1/volumes/{vid}/actions/attach", json={"server": server["id"]}
    )
    assert wait_action(api, response.json()["action"])["status"] == "success"
    # the server is free again
    assert not api.lab.state["servers"][server["id"]]["locked"]


def test_server_create_takes_only_available_volumes_and_leaves_nothing(
    api, monkeypatch
):
    volume = api.post(
        "/v1/volumes", json={"name": "v", "size": 10, "location": "fsn1"}
    ).json()
    wait_action(api, volume["action"])
    vid = volume["volume"]["id"]
    api.lab.state["volumes"][vid]["status"] = "creating"
    response = api.post(
        "/v1/servers",
        json={
            "name": "a",
            "server_type": "cx23",
            "image": "debian-13",
            "location": "fsn1",
            "volumes": [vid],
        },
    )
    assert response.status_code == 423
    api.lab.state["volumes"][vid]["status"] = "available"

    def no_ips(*_):
        raise limit_exceeded("no IPv4 left", "primary_ips")

    monkeypatch.setattr(ips, "setup_server_ips", no_ips)
    response = api.post(
        "/v1/servers",
        json={
            "name": "a",
            "server_type": "cx23",
            "image": "debian-13",
            "location": "fsn1",
            "volumes": [vid],
        },
    )
    assert response.status_code == 403
    # nothing left pointing at it
    assert api.get(f"/v1/volumes/{vid}").json()["volume"]["server"] is None


def test_volume_created_for_a_server_locks_it_until_attached(api):
    server = create_server(api, "a")["server"]
    api.lab.state["servers"][server["id"]]["locked"] = True  # an action runs
    body = {"name": "v", "size": 10, "server": server["id"]}
    assert api.post("/v1/volumes", json=body).status_code == 423
    assert api.get("/v1/volumes").json()["volumes"] == []
    api.lab.state["servers"][server["id"]]["locked"] = False
    response = api.post("/v1/volumes", json=body).json()
    wait_action(api, response["action"])
    assert api.get(f"/v1/servers/{server['id']}").json()["server"]["locked"] is False
    assert api.get("/v1/volumes").json()["volumes"][0]["server"] == server["id"]


def test_volume_delete_claims_the_volume_and_frees_it_when_the_disk_stays(
    api, monkeypatch
):
    volume = api.post(
        "/v1/volumes", json={"name": "v", "size": 10, "location": "fsn1"}
    ).json()
    wait_action(api, volume["action"])
    vid = volume["volume"]["id"]
    busy_while_deleted = []

    def failing_delete(record):
        busy_while_deleted.append(record.get("_busy"))  # no action may start now
        raise RuntimeError("volume-delete failed")

    monkeypatch.setattr(api.lab.fabric, "delete_volume", failing_delete)
    with pytest.raises(RuntimeError):
        volumes.delete_volume(api.lab, vid)
    assert busy_while_deleted == [True]
    assert "_busy" not in api.lab.state["volumes"][vid]
    monkeypatch.undo()
    assert api.delete(f"/v1/volumes/{vid}").status_code == 204
    assert api.get("/v1/volumes").json()["volumes"] == []


def test_server_create_refuses_a_volume_whose_action_started_after_the_check(
    api, monkeypatch
):
    volume = api.post(
        "/v1/volumes", json={"name": "v", "size": 10, "location": "fsn1"}
    ).json()
    wait_action(api, volume["action"])
    vid = volume["volume"]["id"]
    state = api.lab.state
    free_index = state.free_index

    def action_starts_meanwhile():
        state["volumes"][vid]["_busy"] = True  # e.g. an attach elsewhere
        return free_index()

    monkeypatch.setattr(state, "free_index", action_starts_meanwhile)
    response = api.post(
        "/v1/servers",
        json={
            "name": "a",
            "server_type": "cx23",
            "image": "debian-13",
            "location": "fsn1",
            "volumes": [vid],
        },
    )
    assert response.status_code == 423
    assert api.get("/v1/servers").json()["servers"] == []
    assert state["volumes"][vid]["server"] is None
