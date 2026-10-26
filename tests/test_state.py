"""The state: its owner, changes as transactions, and the thread that saves it."""

import ast
import asyncio
import json
import threading
from pathlib import Path

import pytest

from hlabd.state import State, Writer


def in_thread(work):
    """Run `work` in another thread and return what it raised (or None)."""
    raised = []

    def run():
        try:
            work()
        except AssertionError as error:
            raised.append(error)

    other = threading.Thread(target=run)
    other.start()
    other.join()
    return raised[0] if raised else None


def test_the_state_is_used_only_on_its_owners_thread(tmp_path, state_owner_kept):
    state = State(tmp_path / "state.json")
    state.own()
    assert state["servers"] == {}  # the owner's thread
    assert in_thread(lambda: state["servers"]) is not None
    assert in_thread(lambda: state.data) is not None
    assert in_thread(lambda: state.new_id("servers")) is not None
    assert in_thread(state.save) is not None
    assert len(state_owner_kept) == 4
    state_owner_kept.clear()  # broken on purpose


def test_the_first_thread_that_uses_the_state_owns_it(tmp_path, state_owner_kept):
    state = State(tmp_path / "state.json")
    assert in_thread(lambda: state["servers"]) is None  # that thread owns it now
    assert in_thread(lambda: state["servers"]) is None
    with pytest.raises(AssertionError):
        state["servers"]
    state_owner_kept.clear()  # broken on purpose


def test_a_change_saves_when_it_succeeds_and_not_when_it_fails(tmp_path):
    path = tmp_path / "state.json"
    state = State(path)
    with pytest.raises(RuntimeError), state.change():
        state["clients"][1] = {"id": 1}
        raise RuntimeError("refused")
    state.flush()
    assert not path.exists()
    with state.change():
        state["clients"][2] = {"id": 2}
    state.flush()
    assert State(path)["clients"].keys() == {2}


def test_a_failed_change_is_undone_and_records_keep_their_identity(tmp_path):
    state = State(tmp_path / "state.json")
    server = {"id": 1, "name": "a", "_lab": {"nics": [{"tap": "prv1x1"}]}}
    with state.change():
        state["servers"][1] = server
    nics = server["_lab"]["nics"]
    with pytest.raises(RuntimeError), state.change():
        server["name"] = "b"
        nics.append({"tap": "prv1x2"})
        del state["servers"][1]
        state["servers"][2] = {"id": 2}
        state.new_id("servers")
        raise RuntimeError("refused")
    # the very records are back, as they were: code that holds them sees the old state
    assert state["servers"] == {1: server}
    assert state["servers"][1] is server and server["_lab"]["nics"] is nics
    assert server["name"] == "a" and nics == [{"tap": "prv1x1"}]
    assert state.data["next_id"]["servers"] == 1_000_000


def test_a_nested_change_saves_once_with_the_outer_one(tmp_path, monkeypatch):
    state = State(tmp_path / "state.json")
    saves = []
    monkeypatch.setattr(state, "save", lambda: saves.append(1))
    with state.change():
        with state.change():
            state["clients"][1] = {"id": 1}
        assert saves == []
    assert saves == [1]


def test_a_change_that_awaits_is_caught_when_another_task_uses_the_state(
    tmp_path, state_owner_kept
):
    state = State(tmp_path / "state.json")

    async def main():
        inside, checked = asyncio.Event(), asyncio.Event()

        async def bad_change():
            with state.change():  # awaits inside: breaks the rule on purpose
                state["clients"][1] = {"id": 1}
                inside.set()
                await checked.wait()

        async def other():
            await inside.wait()
            with pytest.raises(AssertionError):
                state["clients"]
            checked.set()

        await asyncio.gather(bad_change(), other())

    asyncio.run(main())
    assert len(state_owner_kept) == 1
    state_owner_kept.clear()


def test_the_writer_writes_only_the_newest_of_waiting_saves(tmp_path, monkeypatch):
    writer = Writer(tmp_path / "state.json")
    written, first_started, go_on = [], threading.Event(), threading.Event()
    write = writer.write

    def slow_write(data):
        first_started.set()
        go_on.wait()
        written.append(data)
        write(data)

    monkeypatch.setattr(writer, "write", slow_write)
    writer.put(b"1")
    first_started.wait()  # 1 is being written
    writer.put(b"2")
    writer.put(b"3")  # replaces 2, which was never written
    go_on.set()
    writer.flush()
    assert written == [b"1", b"3"]
    assert (tmp_path / "state.json").read_bytes() == b"3"
    assert not writer.running  # the thread has ended


def test_a_saved_state_is_on_disk_after_a_flush_and_private(tmp_path):
    path = tmp_path / "state.json"
    state = State(path)
    for number in range(1, 20):
        with state.change():
            state["clients"][number] = {"id": number}
    state.flush()
    assert len(json.loads(path.read_text())["clients"]) == 19
    assert path.stat().st_mode & 0o777 == 0o600  # Storage Box passwords are in it
    assert not path.with_suffix(".tmp").exists()


def test_a_failed_write_is_logged_and_the_next_save_writes(tmp_path, caplog):
    path = tmp_path / "missing" / "state.json"
    state = State(path)
    with state.change():
        state["clients"][1] = {"id": 1}
    state.flush()
    assert "saving the state" in caplog.text
    path.parent.mkdir()
    with state.change():
        state["clients"][2] = {"id": 2}
    state.flush()
    assert json.loads(path.read_text())["clients"].keys() == {"1", "2"}


def test_no_change_block_in_the_lab_awaits():
    # a `change()` block is a transaction on the loop: an await inside it would let
    # other tasks see (and save) its change half done
    awaits = []
    for path in (Path(__file__).parents[1] / "hlabd").rglob("*.py"):
        for node in ast.walk(ast.parse(path.read_text())):
            if isinstance(node, ast.With) and any(
                isinstance(item.context_expr, ast.Call)
                and isinstance(item.context_expr.func, ast.Attribute)
                and item.context_expr.func.attr == "change"
                for item in node.items
            ):
                awaits += [
                    f"{path.name}:{inner.lineno}"
                    for statement in node.body
                    for inner in ast.walk(statement)
                    if isinstance(inner, (ast.Await, ast.AsyncWith, ast.AsyncFor))
                ]
    assert awaits == []
