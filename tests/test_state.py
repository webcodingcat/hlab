"""The state's lock and its check of the locking rule."""

import asyncio
import threading

import pytest

from hlabd.state import State


def test_the_lock_knows_whether_this_thread_holds_it(tmp_path):
    state = State(tmp_path / "state.json")
    assert not state.lock.held_by_me()
    with state.lock:
        with state.lock:  # re-entrant
            assert state.lock.held_by_me()
        assert state.lock.held_by_me()
        seen_by_other = []
        other = threading.Thread(
            target=lambda: seen_by_other.append(state.lock.held_by_me())
        )
        other.start()
        other.join()
        assert seen_by_other == [False]
    assert not state.lock.held_by_me()


def test_saving_without_the_lock_is_refused(tmp_path, locking_rule_kept):
    state = State(tmp_path / "state.json")
    with pytest.raises(AssertionError):
        state.save()
    with pytest.raises(AssertionError):
        state.new_id("servers")
    assert not (tmp_path / "state.json").exists()
    locking_rule_kept.clear()  # broken on purpose


def test_a_change_saves_when_it_succeeds_and_not_when_it_fails(tmp_path):
    path = tmp_path / "state.json"
    state = State(path)
    with pytest.raises(RuntimeError), state.change():
        state["clients"][1] = {"id": 1}
        raise RuntimeError("refused")
    assert not path.exists()
    with state.change():
        state["clients"][2] = {"id": 2}
    assert State(path)["clients"].keys() == {1, 2}
    assert not state.lock.held_by_me()


def test_releasing_a_lock_this_thread_does_not_hold_is_refused(tmp_path):
    state = State(tmp_path / "state.json")
    with pytest.raises(RuntimeError):
        state.lock.release()


def test_a_coroutine_does_not_hold_the_lock_another_task_holds(tmp_path):
    state = State(tmp_path / "state.json")

    async def main():
        holding, checked = asyncio.Event(), asyncio.Event()

        async def holder():
            state.lock.acquire()  # held across an await: breaks the rule on purpose
            holding.set()
            await checked.wait()
            state.lock.release()

        async def other():
            await holding.wait()
            held = state.lock.held_by_me()
            with pytest.raises(RuntimeError):  # the RLock would let this task in
                state.lock.acquire()
            checked.set()
            return held

        holder_task = asyncio.create_task(holder())
        held = await other()
        await holder_task
        assert state.lock.owner is None
        return held

    # the event loop's tasks share one thread, yet only the holder holds the lock
    assert asyncio.run(main()) is False
