"""The Storage Box API: boxes, sub-accounts, snapshots, plans, access settings."""

import pytest

from conftest import box_wait, new_box, off
from hlabd.api import storagebox


def test_types(box_api):
    types = box_api.get("/v1/storage_box_types").json()["storage_box_types"]
    assert [t["name"] for t in types] == ["bx11", "bx21", "bx31", "bx41"]
    assert types[0]["size"] == 2**40


def test_box_lifecycle(box_api):
    b = new_box(box_api, access_settings={"ssh_enabled": True, "reachable_externally": True})
    assert b["status"] == "active" and b["username"] == f"u{b['id']}"
    assert b["server"] == f"u{b['id']}.your-storagebox.de"
    sh = box_api.sh
    assert ("box-account", f"u{b['id']}", f"/hlab/box/data/{b['id']}", "@stdin", str(2**40),
            str(b["id"])) in sh.calls   # the box ID is the account's UID
    assert ("box-account", "Secret-Pass-123\n") in sh.stdin   # the password never in a process list
    assert ("box-access", f"u{b['id']}", "1", "1", "0", str(2**40)) in sh.calls

    box_wait(box_api, box_api.post(f"/v1/storage_boxes/{b['id']}/actions/update_access_settings",
                                      json={"zfs_enabled": True, "reachable_externally": False}).json()["action"])
    assert ("box-zfs", f"u{b['id']}", f"/hlab/box/snapshots/{b['id']}") in sh.calls
    assert ("box-access", f"u{b['id']}", "1", "0", "0", str(2**40)) in sh.calls
    box_wait(box_api, box_api.post(f"/v1/storage_boxes/{b['id']}/actions/reset_password",
                                      json={"password": "Other-Pass-456"}).json()["action"])
    assert ("box-account", "Other-Pass-456\n") in sh.stdin

    box_api.post(f"/v1/storage_boxes/{b['id']}/actions/change_protection", json={"delete": True})
    assert box_api.delete(f"/v1/storage_boxes/{b['id']}").status_code == 423
    box_api.post(f"/v1/storage_boxes/{b['id']}/actions/change_protection", json={"delete": False})
    box_wait(box_api, box_api.delete(f"/v1/storage_boxes/{b['id']}").json()["action"])
    assert ("box-remove", f"u{b['id']}") in sh.calls
    assert box_api.get("/v1/storage_boxes").json()["storage_boxes"] == []


def test_subaccounts(box_api):
    b = new_box(box_api)
    r = box_api.post(f"/v1/storage_boxes/{b['id']}/subaccounts", json={
        "home_directory": "backups/cluster-a", "password": "Sub-Pass-123",
        "access_settings": {"ssh_enabled": True, "readonly": True}, "description": "cluster a"})
    assert r.status_code == 201, r.text
    a = r.json()["subaccount"]
    assert a["username"] == f"u{b['id']}-sub1" and a["server"] == f"u{b['id']}-sub1.your-storagebox.de"
    assert ("box-account", a["username"], f"/hlab/box/data/{b['id']}/backups/cluster-a", "@stdin",
            str(2**40), f"u{b['id']}") in box_api.sh.calls
    assert ("box-account", "Sub-Pass-123\n") in box_api.sh.stdin
    assert ("box-access", a["username"], "1", "0", "1", str(2**40)) in box_api.sh.calls
    r = box_api.post(f"/v1/storage_boxes/{b['id']}/subaccounts", json={"home_directory": "../escape",
                                                                         "password": "x"})
    assert r.status_code == 422
    box_api.post(f"/v1/storage_boxes/{b['id']}/subaccounts/{a['id']}/actions/change_home_directory",
                 json={"home_directory": "other"})
    assert ("box-home", a["username"], f"/hlab/box/data/{b['id']}/other") in box_api.sh.calls
    subs = box_api.get(f"/v1/storage_boxes/{b['id']}/subaccounts").json()["subaccounts"]
    assert [s["home_directory"] for s in subs] == ["other"]
    assert box_api.delete(f"/v1/storage_boxes/{b['id']}/subaccounts/{a["id"]}").status_code == 201


def test_snapshots_and_plan(box_api):
    b = new_box(box_api)
    r = box_api.post(f"/v1/storage_boxes/{b['id']}/snapshots", json={"description": "before upgrade"})
    assert r.status_code == 201, r.text
    s = r.json()["snapshot"]
    assert not s["is_automatic"]
    box_wait(box_api, r.json()["action"])                     # the copy runs as the action, as on Hetzner
    assert any(c[0] == "box-snapshot" for c in box_api.sh.calls)
    r = box_api.post(f"/v1/storage_boxes/{b['id']}/snapshots", json={"description": "later"})
    box_wait(box_api, r.json()["action"])
    box_wait(box_api, box_api.post(f"/v1/storage_boxes/{b['id']}/actions/rollback_snapshot",
                                      json={"snapshot": s["name"]}).json()["action"])
    assert ("box-rollback", f"/hlab/box/snapshots/{b['id']}/{s['name']}", f"/hlab/box/data/{b['id']}",
            str(b["id"])) in box_api.sh.calls
    snaps = box_api.get(f"/v1/storage_boxes/{b['id']}/snapshots").json()["snapshots"]
    assert [x["id"] for x in snaps] == [s["id"]]                # the newer one went with the rollback
    r = box_api.post(f"/v1/storage_boxes/{b['id']}/actions/enable_snapshot_plan",
                     json={"max_snapshots": 2, "minute": 0, "hour": 3})
    assert r.status_code == 201
    box = box_api.lab.state["storage_boxes"][b["id"]]
    for _ in range(3):
        storagebox.take_snapshot(box_api.lab, box, True)
    autos = [x for x in box_api.lab.state["box_snapshots"].values() if x["is_automatic"]]
    assert len(autos) == 2   # the plan keeps max_snapshots
    assert box_api.get(f"/v1/storage_boxes/{b['id']}").json()["storage_box"]["snapshot_plan"]["hour"] == 3
    assert box_api.delete(f"/v1/storage_boxes/{b['id']}/snapshots/{s["id"]}").status_code == 201


def test_plan_due():
    from datetime import datetime, timezone
    plan = {"max_snapshots": 3, "minute": 30, "hour": 2, "day_of_week": 0, "day_of_month": None}
    sunday = datetime(2026, 10, 25, 2, 30, tzinfo=timezone.utc)
    assert storagebox.plan_due(plan, sunday)
    assert not storagebox.plan_due(plan, sunday.replace(day=26))
    assert not storagebox.plan_due({**plan, "day_of_week": None, "minute": 31}, sunday)


def test_validation_and_errors(box_api):
    r = box_api.post("/v1/storage_boxes", json={"name": "x", "location": "fsn1", "storage_box_type": "bx11"})
    assert r.status_code == 422 and r.json()["error"]["details"]["fields"][0]["name"] == "password"
    assert box_api.get("/v1/storage_boxes/1").status_code == 404


def test_passwords_follow_hetzners_policy(box_api):
    for pw in ("Short-1a", "no-upper-case-123", "NoSpecialChar123", "Has-Newline-123\nroot:x", "Has Space-123A"):
        r = box_api.post("/v1/storage_boxes", json={"name": "p", "location": "fsn1", "storage_box_type": "bx11",
                                                    "password": pw})
        assert r.status_code == 422, pw
    assert not box_api.get("/v1/storage_boxes").json()["storage_boxes"]
    b = new_box(box_api)
    r = box_api.post(f"/v1/storage_boxes/{b['id']}/actions/reset_password", json={"password": "weak"})
    assert r.status_code == 422


def test_box_access_null_leaves_it(box_api):
    b = new_box(box_api, access_settings={"ssh_enabled": True})
    r = box_api.post(f"/v1/storage_boxes/{b['id']}/actions/update_access_settings", json={"ssh_enabled": None})
    box_wait(box_api, r.json()["action"])
    assert box_api.get(f"/v1/storage_boxes/{b['id']}").json()["storage_box"]["access_settings"]["ssh_enabled"]
    r = box_api.post(f"/v1/storage_boxes/{b['id']}/subaccounts",
                     json={"password": "Secret-Pass-123", "home_directory": "sub", "access_settings": {}})
    box_wait(box_api, r.json()["action"])
    user = box_api.get(f"/v1/storage_boxes/{b['id']}/subaccounts").json()["subaccounts"][0]["username"]
    found = box_api.get(f"/v1/storage_boxes/{b['id']}/subaccounts?name={user}").json()["subaccounts"]
    assert [a["username"] for a in found] == [user]   # the default name is the username


def test_storage_box_rules(box_api):
    b = new_box(box_api)
    url = f"/v1/storage_boxes/{b['id']}/subaccounts"
    body = {"password": "Secret-Pass-123", "home_directory": "one", "access_settings": {}, "name": "backup"}
    assert box_api.post(url, json=body).status_code == 201
    r = box_api.post(url, json={**body, "home_directory": "two"})
    assert r.status_code == 409   # names are unique per box
    running = box_api.get("/v1/storage_boxes/actions?status=running").json()["actions"]
    assert all(a["status"] == "running" for a in running)
    other = box_api.lab.new_action("start_server", [(1, "server")])
    assert box_api.get(f"/v1/storage_boxes/actions/{other['id']}").status_code == 404
    box_api.lab.state["storage_boxes"][b["id"]]["_deleting"] = True
    assert box_api.post(f"/v1/storage_boxes/{b['id']}/snapshots", json={}).status_code == 423


def test_box_access_settings_on_create(box_api):
    b = new_box(box_api, access_settings={"ssh_enabled": None, "bogus": True, "zfs_enabled": True})
    acc = b["access_settings"]
    assert acc["ssh_enabled"] is False and acc["zfs_enabled"] is True and "bogus" not in acc
    box_api.sh.fail.add("box-access")   # not applied: not reported either
    with pytest.raises(RuntimeError):   # the API answers 500 (the test client passes the error on)
        box_api.post(f"/v1/storage_boxes/{b['id']}/actions/update_access_settings", json={"ssh_enabled": True})
    box_api.sh.fail.discard("box-access")
    assert box_api.get(f"/v1/storage_boxes/{b['id']}").json()["storage_box"]["access_settings"]["ssh_enabled"] is False


def test_box_busy_while_rolled_back(box_api):
    b = new_box(box_api)
    r = box_api.post(f"/v1/storage_boxes/{b['id']}/snapshots", json={})
    box_wait(box_api, r.json()["action"])
    snap = r.json()["snapshot"]
    box_api.lab.state["storage_boxes"][b["id"]]["_busy"] = True   # a rollback runs
    assert box_api.post(f"/v1/storage_boxes/{b['id']}/snapshots", json={}).status_code == 423
    assert box_api.delete(f"/v1/storage_boxes/{b['id']}/snapshots/{snap['id']}").status_code == 423
    box_api.lab.state["storage_boxes"][b["id"]].pop("_busy")
    box_api.lab.state["box_snapshots"][snap["id"]]["_copying"] = True   # still being taken
    r = box_api.post(f"/v1/storage_boxes/{b['id']}/actions/rollback_snapshot", json={"snapshot": snap["id"]})
    assert r.status_code == 423
    storagebox.restore(box_api.lab)   # a lab start: the cut-off copy goes
    assert snap["id"] not in box_api.lab.state["box_snapshots"]


def test_rollback_frees_the_box(box_api):
    b = new_box(box_api)
    r = box_api.post(f"/v1/storage_boxes/{b['id']}/snapshots", json={})
    box_wait(box_api, r.json()["action"])
    r = box_api.post(f"/v1/storage_boxes/{b['id']}/actions/rollback_snapshot", json={"snapshot": r.json()["snapshot"]["id"]})
    assert box_wait(box_api, r.json()["action"])["status"] == "success"
    assert not box_api.lab.state["storage_boxes"][b["id"]].get("_busy")
