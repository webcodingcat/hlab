"""The Storage Box API: boxes, sub-accounts, snapshots, plans, access settings."""

import pytest

from conftest import box_wait, new_box, off
from hlabd.api import storagebox


def test_types(box_api):
    types = box_api.get("/v1/storage_box_types").json()["storage_box_types"]
    assert [t["name"] for t in types] == ["bx11", "bx21", "bx31", "bx41"]
    assert types[0]["size"] == 2**40


def test_box_lifecycle(box_api):
    box = new_box(box_api, access_settings={"ssh_enabled": True, "reachable_externally": True})
    assert box["status"] == "active" and box["username"] == f"u{box['id']}"
    assert box["server"] == f"u{box['id']}.your-storagebox.de"
    sh = box_api.sh
    assert ("box-account", f"u{box['id']}", f"/hlab/box/data/{box['id']}", "@stdin", str(2**40),
            str(box["id"])) in sh.calls   # the box ID is the account's UID
    assert ("box-account", "Secret-Pass-123\n") in sh.stdin   # the password never in a process list
    assert ("box-access", f"u{box['id']}", "1", "1", "0", str(2**40)) in sh.calls

    box_wait(box_api, box_api.post(f"/v1/storage_boxes/{box['id']}/actions/update_access_settings",
                                   json={"zfs_enabled": True, "reachable_externally": False}).json()["action"])
    assert ("box-zfs", f"u{box['id']}", f"/hlab/box/snapshots/{box['id']}") in sh.calls
    assert ("box-access", f"u{box['id']}", "1", "0", "0", str(2**40)) in sh.calls
    box_wait(box_api, box_api.post(f"/v1/storage_boxes/{box['id']}/actions/reset_password",
                                   json={"password": "Other-Pass-456"}).json()["action"])
    assert ("box-account", "Other-Pass-456\n") in sh.stdin

    box_api.post(f"/v1/storage_boxes/{box['id']}/actions/change_protection", json={"delete": True})
    assert box_api.delete(f"/v1/storage_boxes/{box['id']}").status_code == 423
    box_api.post(f"/v1/storage_boxes/{box['id']}/actions/change_protection", json={"delete": False})
    box_wait(box_api, box_api.delete(f"/v1/storage_boxes/{box['id']}").json()["action"])
    assert ("box-remove", f"u{box['id']}") in sh.calls
    assert box_api.get("/v1/storage_boxes").json()["storage_boxes"] == []


def test_subaccounts(box_api):
    box = new_box(box_api)
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/subaccounts", json={
        "home_directory": "backups/cluster-a", "password": "Sub-Pass-123",
        "access_settings": {"ssh_enabled": True, "readonly": True}, "description": "cluster a"})
    assert response.status_code == 201, response.text
    subaccount = response.json()["subaccount"]
    assert subaccount["username"] == f"u{box['id']}-sub1" and subaccount["server"] == f"u{box['id']}-sub1.your-storagebox.de"
    assert ("box-account", subaccount["username"], f"/hlab/box/data/{box['id']}/backups/cluster-a", "@stdin",
            str(2**40), f"u{box['id']}") in box_api.sh.calls
    assert ("box-account", "Sub-Pass-123\n") in box_api.sh.stdin
    assert ("box-access", subaccount["username"], "1", "0", "1", str(2**40)) in box_api.sh.calls
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/subaccounts", json={"home_directory": "../escape",
                                                                                "password": "x"})
    assert response.status_code == 422
    box_api.post(f"/v1/storage_boxes/{box['id']}/subaccounts/{subaccount['id']}/actions/change_home_directory",
                 json={"home_directory": "other"})
    assert ("box-home", subaccount["username"], f"/hlab/box/data/{box['id']}/other") in box_api.sh.calls
    subs = box_api.get(f"/v1/storage_boxes/{box['id']}/subaccounts").json()["subaccounts"]
    assert [s["home_directory"] for s in subs] == ["other"]
    assert box_api.delete(f"/v1/storage_boxes/{box['id']}/subaccounts/{subaccount["id"]}").status_code == 201


def test_snapshots_and_plan(box_api):
    box = new_box(box_api)
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/snapshots", json={"description": "before upgrade"})
    assert response.status_code == 201, response.text
    snapshot = response.json()["snapshot"]
    assert not snapshot["is_automatic"]
    box_wait(box_api, response.json()["action"])                     # the copy runs as the action, as on Hetzner
    assert any(c[0] == "box-snapshot" for c in box_api.sh.calls)
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/snapshots", json={"description": "later"})
    box_wait(box_api, response.json()["action"])
    box_wait(box_api, box_api.post(f"/v1/storage_boxes/{box['id']}/actions/rollback_snapshot",
                                   json={"snapshot": snapshot["name"]}).json()["action"])
    assert ("box-rollback", f"/hlab/box/snapshots/{box['id']}/{snapshot['name']}", f"/hlab/box/data/{box['id']}",
            str(box["id"])) in box_api.sh.calls
    snaps = box_api.get(f"/v1/storage_boxes/{box['id']}/snapshots").json()["snapshots"]
    assert [x["id"] for x in snaps] == [snapshot["id"]]                # the newer one went with the rollback
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/actions/enable_snapshot_plan",
                            json={"max_snapshots": 2, "minute": 0, "hour": 3})
    assert response.status_code == 201
    record = box_api.lab.state["storage_boxes"][box["id"]]
    for _ in range(3):
        storagebox.snapshots.take_snapshot(box_api.lab, record, True)
    autos = [x for x in box_api.lab.state["box_snapshots"].values() if x["is_automatic"]]
    assert len(autos) == 2   # the plan keeps max_snapshots
    assert box_api.get(f"/v1/storage_boxes/{box['id']}").json()["storage_box"]["snapshot_plan"]["hour"] == 3
    assert box_api.delete(f"/v1/storage_boxes/{box['id']}/snapshots/{snapshot["id"]}").status_code == 201


def test_plan_due():
    from datetime import datetime, timezone
    plan = {"max_snapshots": 3, "minute": 30, "hour": 2, "day_of_week": 0, "day_of_month": None}
    sunday = datetime(2026, 10, 25, 2, 30, tzinfo=timezone.utc)
    assert storagebox.snapshots.plan_due(plan, sunday)
    assert not storagebox.snapshots.plan_due(plan, sunday.replace(day=26))
    assert not storagebox.snapshots.plan_due({**plan, "day_of_week": None, "minute": 31}, sunday)


def test_validation_and_errors(box_api):
    response = box_api.post("/v1/storage_boxes", json={"name": "x", "location": "fsn1", "storage_box_type": "bx11"})
    assert response.status_code == 422 and response.json()["error"]["details"]["fields"][0]["name"] == "password"
    assert box_api.get("/v1/storage_boxes/1").status_code == 404


def test_passwords_follow_hetzners_policy(box_api):
    for pw in ("Short-1a", "no-upper-case-123", "NoSpecialChar123", "Has-Newline-123\nroot:x", "Has Space-123A"):
        response = box_api.post("/v1/storage_boxes", json={"name": "p", "location": "fsn1", "storage_box_type": "bx11",
                                                           "password": pw})
        assert response.status_code == 422, pw
    assert not box_api.get("/v1/storage_boxes").json()["storage_boxes"]
    box = new_box(box_api)
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/actions/reset_password", json={"password": "weak"})
    assert response.status_code == 422


def test_box_access_null_leaves_it(box_api):
    box = new_box(box_api, access_settings={"ssh_enabled": True})
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/actions/update_access_settings", json={"ssh_enabled": None})
    box_wait(box_api, response.json()["action"])
    assert box_api.get(f"/v1/storage_boxes/{box['id']}").json()["storage_box"]["access_settings"]["ssh_enabled"]
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/subaccounts",
                            json={"password": "Secret-Pass-123", "home_directory": "sub", "access_settings": {}})
    box_wait(box_api, response.json()["action"])
    user = box_api.get(f"/v1/storage_boxes/{box['id']}/subaccounts").json()["subaccounts"][0]["username"]
    found = box_api.get(f"/v1/storage_boxes/{box['id']}/subaccounts?name={user}").json()["subaccounts"]
    assert [a["username"] for a in found] == [user]   # the default name is the username


def test_storage_box_rules(box_api):
    box = new_box(box_api)
    url = f"/v1/storage_boxes/{box['id']}/subaccounts"
    body = {"password": "Secret-Pass-123", "home_directory": "one", "access_settings": {}, "name": "backup"}
    assert box_api.post(url, json=body).status_code == 201
    response = box_api.post(url, json={**body, "home_directory": "two"})
    assert response.status_code == 409   # names are unique per box
    running = box_api.get("/v1/storage_boxes/actions?status=running").json()["actions"]
    assert all(a["status"] == "running" for a in running)
    other = box_api.lab.new_action("start_server", [(1, "server")])
    assert box_api.get(f"/v1/storage_boxes/actions/{other['id']}").status_code == 404
    box_api.lab.state["storage_boxes"][box["id"]]["_deleting"] = True
    assert box_api.post(f"/v1/storage_boxes/{box['id']}/snapshots", json={}).status_code == 423


def test_box_access_settings_on_create(box_api):
    box = new_box(box_api, access_settings={"ssh_enabled": None, "bogus": True, "zfs_enabled": True})
    acc = box["access_settings"]
    assert acc["ssh_enabled"] is False and acc["zfs_enabled"] is True and "bogus" not in acc
    box_api.sh.fail.add("box-access")   # not applied: not reported either
    with pytest.raises(RuntimeError):   # the API answers 500 (the test client passes the error on)
        box_api.post(f"/v1/storage_boxes/{box['id']}/actions/update_access_settings", json={"ssh_enabled": True})
    box_api.sh.fail.discard("box-access")
    assert box_api.get(f"/v1/storage_boxes/{box['id']}").json()["storage_box"]["access_settings"]["ssh_enabled"] is False


def test_box_busy_while_rolled_back(box_api):
    box = new_box(box_api)
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/snapshots", json={})
    box_wait(box_api, response.json()["action"])
    snap = response.json()["snapshot"]
    box_api.lab.state["storage_boxes"][box["id"]]["_busy"] = True   # a rollback runs
    assert box_api.post(f"/v1/storage_boxes/{box['id']}/snapshots", json={}).status_code == 423
    assert box_api.delete(f"/v1/storage_boxes/{box['id']}/snapshots/{snap['id']}").status_code == 423
    box_api.lab.state["storage_boxes"][box["id"]].pop("_busy")
    box_api.lab.state["box_snapshots"][snap["id"]]["_copying"] = True   # still being taken
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/actions/rollback_snapshot", json={"snapshot": snap["id"]})
    assert response.status_code == 423
    storagebox.restore(box_api.lab)   # a lab start: the cut-off copy goes
    assert snap["id"] not in box_api.lab.state["box_snapshots"]


def test_rollback_frees_the_box(box_api):
    box = new_box(box_api)
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/snapshots", json={})
    box_wait(box_api, response.json()["action"])
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/actions/rollback_snapshot", json={"snapshot": response.json()["snapshot"]["id"]})
    assert box_wait(box_api, response.json()["action"])["status"] == "success"
    assert not box_api.lab.state["storage_boxes"][box["id"]].get("_busy")
