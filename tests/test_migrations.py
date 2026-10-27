"""The saved state's version: older files are migrated, newer ones refuse to load."""

import json

import pytest

from hlabd import migrations
from hlabd.migrations import (
    MIGRATIONS,
    STATE_VERSION,
    StateVersionError,
    migrate,
    migrate_0_to_1,
    migrate_1_to_2,
    migrate_2_to_3,
    migrate_3_to_4,
    migrate_4_to_5,
)
from hlabd.state import BOX_SNAPSHOTS, SERVERS, State


def old_server() -> dict:
    """Return a server as the oldest supported files hold it (version 0)."""
    return {
        "id": 1000001,
        "name": "a",
        "status": "running",
        "locked": False,
        "_lab": {"index": 1, "tap": "pub1", "nics": [], "disk_gb": 40},
    }


def old_state() -> dict:
    """Return a state file of version 0 (no version) with every old shape."""
    return {
        "servers": {"1000001": old_server()},
        "box_snapshots": {"16000001": {"id": 16000001, "box": 14000001}},
        "faults": [
            {
                "type": "cut",
                "first": "a",
                "second": "c1",
                "net": "all",
                "first_ref": "s:1000001",
                "second_ref": "c:c1",
            },
            {"type": "isolate", "vm": "c1", "target_ref": {"client": "c1"}},
        ],
        "next_id": {"servers": 1000001},
    }


def test_the_steps_are_numbered_in_order_and_give_the_current_version():
    names = [step.__name__ for step in MIGRATIONS]
    assert names == [f"migrate_{n}_to_{n + 1}" for n in range(len(MIGRATIONS))]
    assert STATE_VERSION == len(MIGRATIONS)


def test_0_to_1_drops_the_old_locked_field():
    data = {"servers": {"1": old_server()}}
    migrate_0_to_1(data)
    assert "locked" not in data["servers"]["1"]


def test_1_to_2_turns_text_references_of_faults_into_typed_ones():
    data = old_state()
    migrate_1_to_2(data)
    cut, isolate = data["faults"]
    assert cut["first_ref"] == {"server": 1000001}
    assert cut["second_ref"] == {"client": "c1"}
    assert isolate["target_ref"] == {"client": "c1"}  # already typed: unchanged


def test_2_to_3_gives_old_servers_the_target_running():
    data = {"servers": {"1": old_server(), "2": old_server()}}
    data["servers"]["2"]["_lab"]["target"] = "off"
    migrate_2_to_3(data)
    assert data["servers"]["1"]["_lab"]["target"] == "running"
    assert data["servers"]["2"]["_lab"]["target"] == "off"  # kept


def test_3_to_4_fills_in_public_keys_and_snapshot_sizes():
    data = old_state()
    data["servers"]["2"] = {**old_server(), "public_keys": ["ssh-ed25519 AAAA"]}
    migrate_3_to_4(data)
    assert data["servers"]["1000001"]["public_keys"] == []
    assert data["servers"]["2"]["public_keys"] == ["ssh-ed25519 AAAA"]
    assert data["box_snapshots"]["16000001"]["size"] == 0


def test_4_to_5_puts_old_servers_in_no_placement_group():
    data = old_state()
    migrate_4_to_5(data)
    assert data["servers"]["1000001"]["placement_group"] is None


def test_the_steps_take_a_file_without_the_kinds_they_change():
    data: dict = {}
    for step in MIGRATIONS:
        step(data)
    assert data == {}


def test_a_file_without_a_version_is_migrated_from_0_to_now(tmp_path):
    path = tmp_path / "state.json"
    path.write_text(json.dumps(old_state()))
    state = State(path)
    server = state[SERVERS][1000001]
    assert "locked" not in server
    assert server["_lab"]["target"] == "running"
    assert server["public_keys"] == []
    assert server["placement_group"] is None
    assert state[BOX_SNAPSHOTS][16000001]["size"] == 0
    assert state.data["faults"][0]["first_ref"] == {"server": 1000001}
    assert state.data["version"] == STATE_VERSION
    # the next save writes the current version
    with state.change():
        state.new_id(SERVERS)
    state.flush()
    assert json.loads(path.read_text())["version"] == STATE_VERSION


def test_a_file_of_the_current_version_runs_no_migration(tmp_path, monkeypatch):
    path = tmp_path / "state.json"
    state = State(path)
    with state.change():
        pass
    state.flush()

    def fail(data):
        raise AssertionError("a migration ran")

    monkeypatch.setattr(migrations, "MIGRATIONS", (fail,) * STATE_VERSION)
    assert State(path).data["version"] == STATE_VERSION


def test_a_new_state_and_a_reset_keep_the_current_version(tmp_path):
    state = State(tmp_path / "state.json")
    assert state.data["version"] == STATE_VERSION
    state.clear()
    assert state.data["version"] == STATE_VERSION


def test_a_file_of_a_newer_version_refuses_to_load(tmp_path):
    path = tmp_path / "state.json"
    path.write_text(json.dumps({"version": STATE_VERSION + 1, "servers": {}}))
    with pytest.raises(StateVersionError, match=f"version {STATE_VERSION + 1}"):
        State(path)
    assert json.loads(path.read_text())["version"] == STATE_VERSION + 1  # untouched


@pytest.mark.parametrize("version", [-1, "2", True, 1.5])
def test_a_broken_version_refuses_to_load(version):
    with pytest.raises(StateVersionError, match="no valid version"):
        migrate({"version": version})
