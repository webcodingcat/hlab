"""The Storage Box API: boxes, sub-accounts, snapshots, plans, access settings."""

import asyncio
from datetime import datetime, timezone
from types import SimpleNamespace

import pytest
from conftest import change_state, state_of, wait_action

from hlabd.api import storagebox
from hlabd.state import STORAGE_BOXES

# the Storage Box API keeps its actions under /v1/storage_boxes/actions
BOX_ACTIONS_PREFIX = "/storage_boxes"


def create_box(api, name="backup", **extra):
    """Create a bx11 Storage Box in fsn1, wait for it and return the box."""
    response = api.post(
        "/v1/storage_boxes",
        json={
            "name": name,
            "location": "fsn1",
            "storage_box_type": "bx11",
            "password": "Secret-Pass-123",
            **extra,
        },
    )
    assert response.status_code == 201, response.text
    wait_action(api, response.json()["action"], prefix=BOX_ACTIONS_PREFIX)
    box_id = response.json()["storage_box"]["id"]
    return api.get(f"/v1/storage_boxes/{box_id}").json()["storage_box"]


def test_box_types_are_hetzners_four_sizes(box_api):
    types = box_api.get("/v1/storage_box_types").json()["storage_box_types"]
    assert [t["name"] for t in types] == ["bx11", "bx21", "bx31", "bx41"]
    assert types[0]["size"] == 2**40
    assert types[1]["description"] == "BX21"
    assert types[1]["prices"][0]["price_monthly"]["net"] == "10.9000000000"


def test_snapshot_plan_keeps_to_the_automatic_limit_of_the_type(box_api):
    # Hetzner limits manual and automatic snapshots separately
    box_api.lab.cfg["storage_box_types"]["bx11"]["automatic_snapshots"] = 2
    box = create_box(box_api)
    plan = f"/v1/storage_boxes/{box['id']}/actions/enable_snapshot_plan"
    response = box_api.post(plan, json={"max_snapshots": 3, "minute": 0, "hour": 3})
    assert response.json()["error"]["code"] == "invalid_input"
    response = box_api.post(plan, json={"max_snapshots": 2, "minute": 0, "hour": 3})
    assert response.status_code == 201
    automatic_limit = box_api.get("/v1/storage_box_types").json()["storage_box_types"][
        0
    ]["automatic_snapshot_limit"]
    assert automatic_limit == 2


def test_box_is_created_configured_protected_and_deleted(box_api):
    box = create_box(
        box_api, access_settings={"ssh_enabled": True, "reachable_externally": True}
    )
    assert box["status"] == "active" and box["username"] == f"u{box['id']}"
    assert box["server"] == f"u{box['id']}.your-storagebox.de"
    sh = box_api.sh
    # the box ID is the account's UID
    assert (
        "set-account",
        f"u{box['id']}",
        f"/hlab/box/data/{box['id']}",
        str(2**40),
        str(box["id"]),
    ) in sh.calls
    # the password never in a process list
    assert ("set-account", "Secret-Pass-123\n") in sh.stdin
    assert ("set-access", f"u{box['id']}", "1", "1", "0", str(2**40)) in sh.calls

    wait_action(
        box_api,
        box_api.post(
            f"/v1/storage_boxes/{box['id']}/actions/update_access_settings",
            json={"zfs_enabled": True, "reachable_externally": False},
        ).json()["action"],
        prefix=BOX_ACTIONS_PREFIX,
    )
    assert (
        "set-zfs-view",
        f"u{box['id']}",
        f"/hlab/box/snapshots/{box['id']}",
    ) in sh.calls
    assert ("set-access", f"u{box['id']}", "1", "0", "0", str(2**40)) in sh.calls
    wait_action(
        box_api,
        box_api.post(
            f"/v1/storage_boxes/{box['id']}/actions/reset_password",
            json={"password": "Other-Pass-456"},
        ).json()["action"],
        prefix=BOX_ACTIONS_PREFIX,
    )
    assert ("set-account", "Other-Pass-456\n") in sh.stdin

    box_api.post(
        f"/v1/storage_boxes/{box['id']}/actions/change_protection",
        json={"delete": True},
    )
    assert box_api.delete(f"/v1/storage_boxes/{box['id']}").status_code == 423
    box_api.post(
        f"/v1/storage_boxes/{box['id']}/actions/change_protection",
        json={"delete": False},
    )
    wait_action(
        box_api,
        box_api.delete(f"/v1/storage_boxes/{box['id']}").json()["action"],
        prefix=BOX_ACTIONS_PREFIX,
    )
    assert ("remove-account", f"u{box['id']}") in sh.calls
    assert box_api.get("/v1/storage_boxes").json()["storage_boxes"] == []


def test_subaccounts_get_their_own_account_and_home_directory(box_api):
    box = create_box(box_api)
    response = box_api.post(
        f"/v1/storage_boxes/{box['id']}/subaccounts",
        json={
            "home_directory": "backups/cluster-a",
            "password": "Sub-Pass-123",
            "access_settings": {"ssh_enabled": True, "readonly": True},
            "description": "cluster a",
        },
    )
    assert response.status_code == 201, response.text
    subaccount = response.json()["subaccount"]
    assert (
        subaccount["username"] == f"u{box['id']}-sub1"
        and subaccount["server"] == f"u{box['id']}-sub1.your-storagebox.de"
    )
    assert (
        "set-account",
        subaccount["username"],
        f"/hlab/box/data/{box['id']}/backups/cluster-a",
        str(2**40),
        f"u{box['id']}",
    ) in box_api.sh.calls
    assert ("set-account", "Sub-Pass-123\n") in box_api.sh.stdin
    assert (
        "set-access",
        subaccount["username"],
        "1",
        "0",
        "1",
        str(2**40),
    ) in box_api.sh.calls
    response = box_api.post(
        f"/v1/storage_boxes/{box['id']}/subaccounts",
        json={"home_directory": "../escape", "password": "x"},
    )
    assert response.status_code == 422
    box_api.post(
        f"/v1/storage_boxes/{box['id']}/subaccounts/{subaccount['id']}/actions/change_home_directory",
        json={"home_directory": "other"},
    )
    assert (
        "set-home",
        subaccount["username"],
        f"/hlab/box/data/{box['id']}/other",
    ) in box_api.sh.calls
    subs = box_api.get(f"/v1/storage_boxes/{box['id']}/subaccounts").json()[
        "subaccounts"
    ]
    assert [s["home_directory"] for s in subs] == ["other"]
    assert (
        box_api.delete(
            f"/v1/storage_boxes/{box['id']}/subaccounts/{subaccount['id']}"
        ).status_code
        == 201
    )


def test_snapshots_roll_back_and_the_plan_keeps_max_snapshots(box_api):
    box = create_box(box_api)
    response = box_api.post(
        f"/v1/storage_boxes/{box['id']}/snapshots",
        json={"description": "before upgrade"},
    )
    assert response.status_code == 201, response.text
    snapshot = response.json()["snapshot"]
    assert not snapshot["is_automatic"]
    # the copy runs as the action, as on Hetzner
    wait_action(box_api, response.json()["action"], prefix=BOX_ACTIONS_PREFIX)
    assert any(c[0] == "take-snapshot" for c in box_api.sh.calls)
    response = box_api.post(
        f"/v1/storage_boxes/{box['id']}/snapshots", json={"description": "later"}
    )
    wait_action(box_api, response.json()["action"], prefix=BOX_ACTIONS_PREFIX)
    wait_action(
        box_api,
        box_api.post(
            f"/v1/storage_boxes/{box['id']}/actions/rollback_snapshot",
            json={"snapshot": snapshot["name"]},
        ).json()["action"],
        prefix=BOX_ACTIONS_PREFIX,
    )
    assert (
        "roll-back",
        f"/hlab/box/snapshots/{box['id']}/{snapshot['name']}",
        f"/hlab/box/data/{box['id']}",
    ) in box_api.sh.calls
    snaps = box_api.get(f"/v1/storage_boxes/{box['id']}/snapshots").json()["snapshots"]
    # the newer one was deleted with the rollback
    assert [x["id"] for x in snaps] == [snapshot["id"]]
    response = box_api.post(
        f"/v1/storage_boxes/{box['id']}/actions/enable_snapshot_plan",
        json={"max_snapshots": 2, "minute": 0, "hour": 3},
    )
    assert response.status_code == 201
    record = state_of(box_api, "storage_boxes")[box["id"]]
    for _ in range(3):
        box_api.on_loop(
            storagebox.snapshots.take_automatic_snapshot, box_api.lab, record
        )
    autos = [
        x for x in state_of(box_api, "box_snapshots").values() if x["is_automatic"]
    ]
    assert len(autos) == 2  # the plan keeps max_snapshots
    assert (
        box_api.get(f"/v1/storage_boxes/{box['id']}").json()["storage_box"][
            "snapshot_plan"
        ]["hour"]
        == 3
    )
    assert (
        box_api.delete(
            f"/v1/storage_boxes/{box['id']}/snapshots/{snapshot['id']}"
        ).status_code
        == 201
    )


def test_snapshot_plan_is_due_at_its_time_and_day():

    plan = {
        "max_snapshots": 3,
        "minute": 30,
        "hour": 2,
        "day_of_week": 0,
        "day_of_month": None,
    }
    sunday = datetime(2026, 10, 25, 2, 30, tzinfo=timezone.utc)
    assert storagebox.snapshots.snapshot_plan_due(plan, sunday)
    assert not storagebox.snapshots.snapshot_plan_due(plan, sunday.replace(day=26))
    assert not storagebox.snapshots.snapshot_plan_due(
        {**plan, "day_of_week": None, "minute": 31}, sunday
    )


def test_snapshot_plan_due_while_the_box_is_busy_is_caught_up_once(monkeypatch):
    plan = {
        "max_snapshots": 3,
        "minute": 30,
        "hour": 2,
        "day_of_week": None,
        "day_of_month": None,
    }
    box = {"id": 7, "status": "active", "plan": plan, "_busy": True}

    lab = SimpleNamespace(state={STORAGE_BOXES: {7: box}})
    taken = []

    async def take_automatic_snapshot(lab, box):
        taken.append(box["id"])

    monkeypatch.setattr(
        storagebox.snapshots, "take_automatic_snapshot", take_automatic_snapshot
    )
    due = datetime(2026, 10, 25, 2, 30, tzinfo=timezone.utc)
    later = due.replace(minute=31)
    waiting: set[int] = set()

    async def run_plans(minutes):
        await storagebox.snapshots.run_snapshot_plans(lab, minutes, waiting)

    # due twice (two days) while a rollback runs: no snapshot yet
    asyncio.run(run_plans([due]))
    asyncio.run(run_plans([due.replace(day=26)]))
    assert taken == [] and waiting == {7}
    # free again: one snapshot for the missed slots, then none until the plan is due
    del box["_busy"]
    asyncio.run(run_plans([later]))
    asyncio.run(run_plans([later.replace(minute=32)]))
    assert taken == [7] and waiting == set()


def test_box_without_password_is_refused_and_unknown_box_not_found(box_api):
    response = box_api.post(
        "/v1/storage_boxes",
        json={"name": "x", "location": "fsn1", "storage_box_type": "bx11"},
    )
    assert (
        response.status_code == 422
        and response.json()["error"]["details"]["fields"][0]["name"] == "password"
    )
    assert box_api.get("/v1/storage_boxes/1").status_code == 404


def test_passwords_follow_hetzners_policy(box_api):
    for pw in (
        "Short-1a",
        "no-upper-case-123",
        "NoSpecialChar123",
        "Has-Newline-123\nroot:x",
        "Has Space-123A",
    ):
        response = box_api.post(
            "/v1/storage_boxes",
            json={
                "name": "p",
                "location": "fsn1",
                "storage_box_type": "bx11",
                "password": pw,
            },
        )
        assert response.status_code == 422, pw
    assert not box_api.get("/v1/storage_boxes").json()["storage_boxes"]
    box = create_box(box_api)
    response = box_api.post(
        f"/v1/storage_boxes/{box['id']}/actions/reset_password",
        json={"password": "weak"},
    )
    assert response.status_code == 422


def test_null_access_setting_leaves_it_unchanged(box_api):
    box = create_box(box_api, access_settings={"ssh_enabled": True})
    response = box_api.post(
        f"/v1/storage_boxes/{box['id']}/actions/update_access_settings",
        json={"ssh_enabled": None},
    )
    wait_action(box_api, response.json()["action"], prefix=BOX_ACTIONS_PREFIX)
    assert box_api.get(f"/v1/storage_boxes/{box['id']}").json()["storage_box"][
        "access_settings"
    ]["ssh_enabled"]
    response = box_api.post(
        f"/v1/storage_boxes/{box['id']}/subaccounts",
        json={
            "password": "Secret-Pass-123",
            "home_directory": "sub",
            "access_settings": {},
        },
    )
    wait_action(box_api, response.json()["action"], prefix=BOX_ACTIONS_PREFIX)
    user = box_api.get(f"/v1/storage_boxes/{box['id']}/subaccounts").json()[
        "subaccounts"
    ][0]["username"]
    found = box_api.get(
        f"/v1/storage_boxes/{box['id']}/subaccounts?name={user}"
    ).json()["subaccounts"]
    assert [a["username"] for a in found] == [user]  # the default name is the username


def test_box_follows_hetzners_rules_for_names_actions_and_locks(box_api):
    box = create_box(box_api)
    url = f"/v1/storage_boxes/{box['id']}/subaccounts"
    body = {
        "password": "Secret-Pass-123",
        "home_directory": "one",
        "access_settings": {},
        "name": "backup",
    }
    assert box_api.post(url, json=body).status_code == 201
    response = box_api.post(url, json={**body, "home_directory": "two"})
    assert response.status_code == 409  # names are unique per box
    running = box_api.get("/v1/storage_boxes/actions?status=running").json()["actions"]
    assert all(a["status"] == "running" for a in running)
    other = box_api.on_loop(box_api.lab.new_action, "start_server", [(1, "server")])
    assert box_api.get(f"/v1/storage_boxes/actions/{other['id']}").status_code == 404
    box_record = state_of(box_api, "storage_boxes")[box["id"]]
    change_state(box_api, box_record.update, {"_deleting": True})
    assert (
        box_api.post(f"/v1/storage_boxes/{box['id']}/snapshots", json={}).status_code
        == 423
    )


def test_access_settings_are_cleaned_on_create_and_kept_when_not_applied(box_api):
    box = create_box(
        box_api,
        access_settings={"ssh_enabled": None, "bogus": True, "zfs_enabled": True},
    )
    acc = box["access_settings"]
    assert (
        acc["ssh_enabled"] is False
        and acc["zfs_enabled"] is True
        and "bogus" not in acc
    )
    box_api.sh.fail.add("set-access")  # not applied: not reported either
    # the API answers 500 (the test client passes the error on)
    with pytest.raises(RuntimeError):
        box_api.post(
            f"/v1/storage_boxes/{box['id']}/actions/update_access_settings",
            json={"ssh_enabled": True},
        )
    box_api.sh.fail.discard("set-access")
    assert (
        box_api.get(f"/v1/storage_boxes/{box['id']}").json()["storage_box"][
            "access_settings"
        ]["ssh_enabled"]
        is False
    )


def test_box_is_busy_while_rolled_back(box_api):
    box = create_box(box_api)
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/snapshots", json={})
    wait_action(box_api, response.json()["action"], prefix=BOX_ACTIONS_PREFIX)
    snap = response.json()["snapshot"]
    box_record = state_of(box_api, "storage_boxes")[box["id"]]
    change_state(box_api, box_record.update, {"_busy": True})  # a rollback runs
    assert (
        box_api.post(f"/v1/storage_boxes/{box['id']}/snapshots", json={}).status_code
        == 423
    )
    assert (
        box_api.delete(
            f"/v1/storage_boxes/{box['id']}/snapshots/{snap['id']}"
        ).status_code
        == 423
    )


def test_snapshot_still_being_copied_blocks_rollback_and_is_removed_on_restart(box_api):
    box = create_box(box_api)
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/snapshots", json={})
    wait_action(box_api, response.json()["action"], prefix=BOX_ACTIONS_PREFIX)
    snap = response.json()["snapshot"]
    snapshot = state_of(box_api, "box_snapshots")[snap["id"]]
    change_state(box_api, snapshot.update, {"_copying": True})
    response = box_api.post(
        f"/v1/storage_boxes/{box['id']}/actions/rollback_snapshot",
        json={"snapshot": snap["id"]},
    )
    assert response.status_code == 423
    # a lab start: the cut-off copy is removed
    box_api.on_loop(storagebox.restore, box_api.lab)
    assert snap["id"] not in state_of(box_api, "box_snapshots")


def test_rollback_frees_the_box(box_api):
    box = create_box(box_api)
    response = box_api.post(f"/v1/storage_boxes/{box['id']}/snapshots", json={})
    wait_action(box_api, response.json()["action"], prefix=BOX_ACTIONS_PREFIX)
    response = box_api.post(
        f"/v1/storage_boxes/{box['id']}/actions/rollback_snapshot",
        json={"snapshot": response.json()["snapshot"]["id"]},
    )
    assert (
        wait_action(box_api, response.json()["action"], prefix=BOX_ACTIONS_PREFIX)[
            "status"
        ]
        == "success"
    )
    assert not state_of(box_api, "storage_boxes")[box["id"]].get("_busy")


def test_a_subaccount_whose_account_fails_is_removed_and_frees_the_box(box_api):
    box = create_box(box_api)
    box_api.sh.fail.add("set-account")
    with pytest.raises(RuntimeError):
        box_api.post(
            f"/v1/storage_boxes/{box['id']}/subaccounts",
            json={"home_directory": "backups", "password": "Sub-Pass-123"},
        )
    assert state_of(box_api, "subaccounts") == {}
    box_api.sh.fail.clear()
    response = box_api.delete(f"/v1/storage_boxes/{box['id']}")
    assert response.status_code == 201  # no sub-account is left being set up
