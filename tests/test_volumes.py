"""Volumes: create, attach, detach, resize, and their busy states."""

from conftest import create_server, off, wait_action
from hlabd.api import ips
from hlabd.api.web import limit_exceeded


def test_volume_lifecycle(api, tmp_path):
    s = create_server(api, "a")["server"]
    r = api.post("/v1/volumes", json={"name": "data", "size": 10, "location": "fsn1", "format": "ext4"})
    assert r.status_code == 201, r.text
    v = r.json()["volume"]
    wait_action(api, r.json()["action"])
    f = str(tmp_path / "volumes" / f"{v['id']}.raw")
    assert ("volume-create", f, "10", "ext4") in api.sh.calls
    assert api.get(f"/v1/volumes/{v['id']}").json()["volume"]["status"] == "available"
    assert v["linux_device"] == f"/dev/disk/by-id/scsi-0HC_Volume_{v['id']}"

    wait_action(api, api.post(f"/v1/volumes/{v['id']}/actions/attach", json={"server": s["id"], "automount": True})
                .json()["action"])
    assert ("volume-attach", str(tmp_path / "vm" / str(s["id"])), str(v["id"]), f) in api.sh.calls
    assert api.get(f"/v1/servers/{s['id']}").json()["server"]["volumes"] == [v["id"]]
    assert api.delete(f"/v1/volumes/{v['id']}").json()["error"]["code"] == "volume_attached"

    r = api.post(f"/v1/volumes/{v['id']}/actions/resize", json={"size": 5})
    assert r.status_code == 422
    wait_action(api, api.post(f"/v1/volumes/{v['id']}/actions/resize", json={"size": 20}).json()["action"])
    assert api.get(f"/v1/volumes/{v['id']}").json()["volume"]["size"] == 20

    # boots with the volume after a power cycle
    off(api, s["id"])
    wait_action(api, api.post(f"/v1/servers/{s['id']}/actions/poweron").json()["action"])
    assert f"{v['id']} {f}" in (tmp_path / "vm" / str(s["id"]) / "volumes").read_text()

    wait_action(api, api.post(f"/v1/volumes/{v['id']}/actions/detach").json()["action"])
    assert api.get(f"/v1/volumes/{v['id']}").json()["volume"]["server"] is None
    assert api.delete(f"/v1/volumes/{v['id']}").status_code == 204


def test_volume_rules(api):
    s = create_server(api, "a", location="nbg1")["server"]
    assert api.post("/v1/volumes", json={"name": "x", "size": 5, "location": "fsn1"}).status_code == 422
    v = api.post("/v1/volumes", json={"name": "x", "size": 10, "location": "fsn1"}).json()
    wait_action(api, v["action"])
    r = api.post(f"/v1/volumes/{v['volume']['id']}/actions/attach", json={"server": s["id"]})
    assert r.json()["error"]["code"] == "location_mismatch"
    r = api.post("/v1/volumes", json={"name": "y", "size": 10, "server": s["id"], "automount": True, "format": "xfs"})
    assert r.json()["volume"]["server"] == s["id"] and r.json()["next_actions"][0]["command"] == "attach_volume"


def test_server_created_with_volume_and_deleted(api):
    v = api.post("/v1/volumes", json={"name": "z", "size": 10, "location": "fsn1"}).json()
    wait_action(api, v["action"])
    s = create_server(api, "with-vol", volumes=[v["volume"]["id"]], automount=False)["server"]
    assert s["volumes"] == [v["volume"]["id"]]
    wait_action(api, api.delete(f"/v1/servers/{s['id']}").json()["action"])
    assert api.get(f"/v1/volumes/{v['volume']['id']}").json()["volume"]["server"] is None


def test_refused_volume_leaves_nothing(api):
    s = create_server(api, "a")["server"]
    r = api.post("/v1/volumes", json={"name": "data", "size": 10, "location": "nbg1", "server": s["id"]})
    assert r.status_code == 422
    assert api.get("/v1/volumes").json()["volumes"] == []
    assert api.post("/v1/volumes", json={"name": "data", "size": 10, "server": s["id"]}).status_code == 201
    v = api.post("/v1/volumes", json={"name": "v2", "size": 10, "location": "fsn1"}).json()["volume"]
    r = api.post("/v1/servers", json={"name": "b", "server_type": "cx23", "image": "debian-13", "location": "fsn1",
                                      "volumes": [v["id"], v["id"]]})
    assert r.status_code == 422
    assert api.get(f"/v1/volumes/{v['id']}").json()["volume"]["server"] is None


def test_failed_volume_create_goes_again(api):
    api.sh.fail.add("volume-create")
    r = api.post("/v1/volumes", json={"name": "v", "size": 10, "location": "fsn1"}).json()
    assert wait_action(api, r["action"])["status"] == "error"
    assert api.get("/v1/volumes").json()["volumes"] == []


def test_volume_actions_one_at_a_time(api):
    v = api.post("/v1/volumes", json={"name": "v", "size": 10, "location": "fsn1"}).json()
    wait_action(api, v["action"])
    vid = v["volume"]["id"]
    assert api.post(f"/v1/volumes/{vid}/actions/detach").status_code == 422   # not attached
    api.lab.state["volumes"][vid]["_busy"] = True
    assert api.post(f"/v1/volumes/{vid}/actions/resize", json={"size": 20}).status_code == 423
    api.lab.state["volumes"][vid].pop("_busy")
    s = create_server(api, "a")["server"]
    api.sh.fail.add("volume-attach")   # the hot-plug fails: the volume is not attached
    r = api.post(f"/v1/volumes/{vid}/actions/attach", json={"server": s["id"]})
    assert wait_action(api, r.json()["action"])["status"] == "error"
    v = api.get(f"/v1/volumes/{vid}").json()["volume"]
    assert v["server"] is None
    api.sh.fail.discard("volume-attach")
    r = api.post(f"/v1/volumes/{vid}/actions/attach", json={"server": s["id"]})   # and not left busy
    assert wait_action(api, r.json()["action"])["status"] == "success"


def test_volumes_wait_for_a_busy_server_and_are_not_deleted_while_busy(api):
    s = create_server(api, "a")["server"]
    v = api.post("/v1/volumes", json={"name": "v", "size": 10, "location": "fsn1"}).json()
    wait_action(api, v["action"])
    vid = v["volume"]["id"]
    api.lab.state["servers"][s["id"]]["locked"] = True   # an action of the server runs
    assert api.post(f"/v1/volumes/{vid}/actions/attach", json={"server": s["id"]}).status_code == 423
    api.lab.state["servers"][s["id"]]["locked"] = False
    api.lab.state["volumes"][vid]["_busy"] = True
    assert api.delete(f"/v1/volumes/{vid}").status_code == 423
    api.lab.state["volumes"][vid].pop("_busy")
    r = api.post(f"/v1/volumes/{vid}/actions/attach", json={"server": s["id"]})
    assert wait_action(api, r.json()["action"])["status"] == "success"
    assert not api.lab.state["servers"][s["id"]]["locked"]   # the server is free again


def test_server_create_takes_only_available_volumes_and_leaves_nothing(api, monkeypatch):
    v = api.post("/v1/volumes", json={"name": "v", "size": 10, "location": "fsn1"}).json()
    wait_action(api, v["action"])
    vid = v["volume"]["id"]
    api.lab.state["volumes"][vid]["status"] = "creating"
    r = api.post("/v1/servers", json={"name": "a", "server_type": "cx23", "image": "debian-13", "location": "fsn1",
                                      "volumes": [vid]})
    assert r.status_code == 423
    api.lab.state["volumes"][vid]["status"] = "available"

    def no_ips(*_):
        raise limit_exceeded("no IPv4 left", "primary_ips")
    monkeypatch.setattr(ips, "setup_server_ips", no_ips)
    r = api.post("/v1/servers", json={"name": "a", "server_type": "cx23", "image": "debian-13", "location": "fsn1",
                                      "volumes": [vid]})
    assert r.status_code == 403
    assert api.get(f"/v1/volumes/{vid}").json()["volume"]["server"] is None   # nothing left pointing at it
