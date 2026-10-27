"""The core of actions (lab.py): flags, `act()`, `run_action()`, stopping actions.

Each test runs its actions on its own event loop (`act()` is async), in the test's
thread, which then owns the state; `steps` wait for an event, so a test can look at the
flags while they run.
"""

import ast
import asyncio
import threading
import time
from pathlib import Path

import pytest

from hlabd import lab as lab_module
from hlabd.errors import ApiError, invalid
from hlabd.lab import SERVER_LOCKED, ActionSpec, ActionTable, Plan, RecordGoneError
from hlabd.state import SERVERS, VOLUMES


def make_server(lab, sid=1001, status="running"):
    """Put a bare server into the state and return it."""
    server = {
        "id": sid,
        "name": f"s{sid}",
        "status": status,
        "_lab": {"index": sid - 1000, "tap": f"pub{sid - 1000}", "nics": []},
    }
    with lab.state.change():
        lab.state[SERVERS][sid] = server
    return server


def make_volume(lab, vid=10001):
    """Put a bare volume into the state and return it."""
    volume = {"id": vid, "name": f"v{vid}", "status": "available", "server": None}
    with lab.state.change():
        lab.state[VOLUMES][vid] = volume
    return volume


def run(coro):
    """Run a test's coroutine on a fresh event loop."""
    return asyncio.run(coro)


async def settle(lab):
    """Wait until every running action has ended."""
    while lab.tasks:
        await asyncio.gather(*list(lab.tasks), return_exceptions=True)


def planning(plan):
    """Return a plan function for a table (`async def`, like all of them)."""

    async def plan_function(*_):
        return plan

    return plan_function


def table(function, resource_type="volume", flag="_busy"):
    """Return an action table with the command "go" (Hetzner's name "do_go")."""
    return ActionTable(
        resource_type,
        {
            "go": ActionSpec("do_go", function),
            "change_protection": ActionSpec("change_protection", function, flag=False),
        },
        flag=flag,
        busy="busy",
    )


# --- claim and release ----------------------------------------------------------------


def test_claim_sets_the_flag_and_refuses_a_second_claim_with_423(lab):
    volume = make_volume(lab)
    lab.claim(volume, "_busy", "busy")
    assert volume["_busy"] is True
    with pytest.raises(ApiError) as refused:
        lab.claim(volume, "_busy", "the volume is busy")
    assert (refused.value.status, refused.value.code) == (423, "locked")
    assert refused.value.message == "the volume is busy"


def test_release_takes_a_flag_off_and_leaves_a_server_unlocked(lab):
    volume, server = make_volume(lab), make_server(lab)
    lab.claim(volume, "_busy", "busy")
    lab.claim(server, "_locked", "locked")
    lab.release(volume, "_busy")
    lab.release(server, "_locked")
    assert "_busy" not in volume and "_locked" not in server
    lab.claim(volume, "_busy", "busy")  # free again


def test_a_restart_clears_every_flag(lab):
    volume, server = make_volume(lab), make_server(lab)
    lab.claim(volume, "_busy", "busy")
    lab.claim(server, "_locked", "locked")
    lab.clear_flags()
    assert "_busy" not in volume and "_locked" not in server


def test_holding_takes_off_what_it_holds_when_the_block_ends(lab):
    volume, server = make_volume(lab), make_server(lab)
    with lab.holding() as held:
        held.claim(volume, "_busy", "busy")
        held.claim(server, "_locked", SERVER_LOCKED)
        assert volume["_busy"] is True and server["_locked"] is True
    assert "_busy" not in volume and "_locked" not in server


def test_holding_takes_its_flags_off_when_the_block_fails(lab):
    volume, server = make_volume(lab), make_server(lab)
    lab.claim(server, "_locked", "locked")  # another action runs on the server
    with pytest.raises(ApiError), lab.holding() as held:
        held.claim(volume, "_busy", "busy")
        held.claim(server, "_locked", SERVER_LOCKED)  # 423
    assert "_busy" not in volume
    assert server["_locked"] is True  # still the other action's


def test_holding_leaves_kept_and_handed_over_flags(lab):
    volume, server = make_volume(lab), make_server(lab)
    with lab.holding() as held:
        held.claim(volume, "_busy", "busy")
        held.keep()
        held.claim(server, "_locked", SERVER_LOCKED)
        assert held.take() == [(server, "_locked")]
    assert volume["_busy"] is True and server["_locked"] is True


# --- act ------------------------------------------------------------------------------


def test_act_refuses_an_unknown_command_as_unsupported(lab):
    volume = make_volume(lab)
    with pytest.raises(ApiError) as refused:
        run(lab.act(table(planning(Plan())), "fly", volume, {}))
    assert (refused.value.status, refused.value.code) == (422, "unsupported_error")
    assert "_busy" not in volume


def test_act_takes_the_flag_off_when_the_plan_refuses(lab):
    volume = make_volume(lab)
    seen = []

    async def refuse(lab, item, body):
        seen.append(item.get("_busy"))  # the flag is set while the plan is made
        raise invalid("no")

    with pytest.raises(ApiError):
        run(lab.act(table(refuse), "go", volume, {}))
    assert seen == [True]
    assert "_busy" not in volume


def test_act_refuses_a_flagged_resource_and_leaves_its_flag(lab):
    volume = make_volume(lab)
    lab.claim(volume, "_busy", "busy")
    with pytest.raises(ApiError) as refused:
        run(lab.act(table(planning(Plan())), "go", volume, {}))
    assert refused.value.status == 423
    assert volume["_busy"] is True  # still the other action's


def test_act_without_steps_finishes_at_once_and_frees_the_resource(lab):
    volume = make_volume(lab)
    answer = run(
        lab.act(
            table(planning(Plan(extra={"note": 1}))),
            "go",
            volume,
            {},
        )
    )
    assert answer["action"]["status"] == "success"
    assert answer["action"]["command"] == "do_go"
    assert answer["action"]["resources"] == [{"id": 10001, "type": "volume"}]
    assert answer["note"] == 1
    assert "_busy" not in volume
    lab.state.flush()
    assert lab.state.path.is_file()  # saved


def test_act_holds_the_flags_until_the_steps_succeed(lab):
    volume, server = make_volume(lab), make_server(lab)

    async def main():
        go = asyncio.Event()

        async def steps():
            await go.wait()

        answer = await lab.act(
            table(planning(Plan(steps, server=server))),
            "go",
            volume,
            {},
        )
        await asyncio.sleep(0)
        assert volume["_busy"] is True and server["_locked"] is True
        assert answer["action"]["status"] == "running"
        go.set()
        await settle(lab)
        return answer["action"]

    action = run(main())
    assert action["status"] == "success"
    assert "_busy" not in volume and "_locked" not in server


def test_act_releases_the_flags_when_the_steps_fail(lab):
    volume, server = make_volume(lab), make_server(lab)

    async def main():
        async def steps():
            raise RuntimeError("the disk broke")

        answer = await lab.act(
            table(planning(Plan(steps, server=server))),
            "go",
            volume,
            {},
        )
        await settle(lab)
        return answer["action"]

    action = run(main())
    assert action["status"] == "error"
    assert action["error"] == {"code": "action_failed", "message": "the disk broke"}
    assert "_busy" not in volume and "_locked" not in server


def test_act_refuses_a_locked_plan_server_and_frees_the_resource(lab):
    volume, server = make_volume(lab), make_server(lab)
    lab.claim(server, "_locked", "locked")

    async def steps():
        pass

    with pytest.raises(ApiError) as refused:
        run(
            lab.act(
                table(planning(Plan(steps, server=server))),
                "go",
                volume,
                {},
            )
        )
    assert refused.value.status == 423
    assert "_busy" not in volume
    assert server["_locked"] is True  # still the other action's


def test_act_locks_a_server_that_is_the_resource_only_once(lab):
    server = make_server(lab)

    async def main():
        async def steps():
            assert server["_locked"] is True

        await lab.act(
            table(planning(Plan(steps, server=server)), "server", "_locked"),
            "go",
            server,
            {},
        )
        await settle(lab)

    run(main())
    assert "_locked" not in server


def test_record_only_actions_take_no_flag(lab):
    volume = make_volume(lab)
    lab.claim(volume, "_busy", "busy")  # another action runs
    answer = run(lab.act(table(planning(Plan())), "change_protection", volume, {}))
    assert answer["action"]["status"] == "success"
    assert volume["_busy"] is True  # the running action keeps its flag


# --- run_action -----------------------------------------------------------------------


def test_failed_steps_fail_the_next_actions_too(lab):
    server = make_server(lab)

    async def main():
        action = lab.new_action("create_server", [(1001, "server")])
        next_action = lab.new_action("start_server", [(1001, "server")])

        async def steps():
            raise RuntimeError("no disk")

        with lab.holding() as held:
            held.claim(server, "_locked", SERVER_LOCKED)
            lab.run_action(action, steps, server, [next_action], held)
        await settle(lab)
        return action, next_action

    action, next_action = run(main())
    assert action["status"] == next_action["status"] == "error"
    assert next_action["error"]["message"] == "no disk"


@pytest.mark.parametrize(("vm_runs", "status"), [(False, "off"), (True, "running")])
def test_a_failed_server_leaves_its_in_between_status_for_what_its_vm_does(
    lab, vm_runs, status
):
    server = make_server(lab, status="starting")
    if vm_runs:
        lab.fabric.shell.running.add(str(lab.fabric.vms.directory(server["id"])))

    async def main():
        action = lab.new_action("start_server", [(1001, "server")])

        async def steps():
            raise RuntimeError("QEMU failed")

        with lab.holding() as held:
            held.claim(server, "_locked", SERVER_LOCKED)
            lab.run_action(action, steps, server, holding=held)
        await settle(lab)

    run(main())
    assert server["status"] == status


def test_a_cancelled_action_ends_with_an_error_and_frees_its_flags(lab):
    volume = make_volume(lab)

    async def main():
        action = lab.new_action("resize_volume", [(10001, "volume")])

        async def steps():
            await asyncio.Event().wait()  # forever

        with lab.holding() as held:
            held.claim(volume, "_busy", "busy")
            lab.run_action(action, steps, holding=held)
        await asyncio.sleep(0)
        await lab.stop_actions()
        return action

    action = run(main())
    assert action["status"] == "error"
    assert "cancelled" in action["error"]["message"]
    assert "_busy" not in volume


def test_an_action_whose_steps_never_start_fails_and_frees_its_flags(lab, monkeypatch):
    volume = make_volume(lab)

    def no_task(coro):
        raise RuntimeError("no task")

    monkeypatch.setattr(lab, "spawn", no_task)

    async def main():
        action = lab.new_action("resize_volume", [(10001, "volume")])

        async def steps():
            pass

        with pytest.raises(RuntimeError), lab.holding() as held:
            held.claim(volume, "_busy", "busy")
            lab.run_action(action, steps, holding=held)
        return action

    action = run(main())
    assert action["status"] == "error"
    assert "_busy" not in volume


def test_stopping_actions_waits_for_their_threads(lab):
    returned = threading.Event()

    def slow_script():
        time.sleep(0.2)
        returned.set()

    async def main():
        action = lab.new_action("poweron", [(1001, "server")])

        async def steps():
            await lab.blocking(slow_script)

        lab.run_action(action, steps)
        await asyncio.sleep(0.05)  # the thread runs
        await lab.stop_actions()
        # a thread cannot be cancelled: the reset waited until it returned
        assert returned.is_set()
        assert not lab.threads
        return action

    assert run(main())["status"] == "error"


def test_act_holds_the_flag_while_a_plan_function_awaits(lab):
    volume = make_volume(lab)

    async def plan(lab, item, body):
        await asyncio.sleep(0)  # asks the system first (a script, in a thread)
        assert item["_busy"] is True  # the flag is held meanwhile
        return Plan(extra={"asked": True})

    answer = run(lab.act(table(plan), "go", volume, {}))
    assert answer["asked"] is True
    assert answer["action"]["status"] == "success"
    assert "_busy" not in volume


def test_act_answers_a_list_of_actions_with_the_finished_ones(lab):
    volume = make_volume(lab)
    plan = Plan(finished=[("do_more", [(10001, "volume"), (1001, "server")])])
    actions = ActionTable(
        "volume", {"go": ActionSpec("do_go", planning(plan), answer="actions")}
    )
    answer = run(lab.act(actions, "go", volume, {}))
    assert [action["command"] for action in answer["actions"]] == ["do_go", "do_more"]
    assert all(action["status"] == "success" for action in answer["actions"])
    assert answer["actions"][1]["resources"][1] == {"id": 1001, "type": "server"}


def test_stopping_actions_gives_up_on_a_hung_thread_and_names_it(
    lab, monkeypatch, caplog
):
    monkeypatch.setattr(lab_module, "THREADS_WAIT_SECONDS", 0.05)
    release = threading.Event()

    def hung_script():
        release.wait(5)

    async def main():
        lab.spawn(lab.blocking(hung_script))
        await asyncio.sleep(0.02)  # the thread runs
        await lab.stop_actions()  # returns although the thread still runs
        assert len(lab.threads) == 1
        release.set()

    run(main())
    assert "hung_script" in caplog.text


def test_the_core_imports_nothing_from_the_api():
    # api/ builds on lab.py; the other way round would be a cycle
    tree = ast.parse((Path(lab_module.__file__)).read_text())
    imported = [
        node.module or ""
        for node in ast.walk(tree)
        if isinstance(node, ast.ImportFrom) and node.level
    ]
    assert not [module for module in imported if module.startswith("api")]


def test_current_returns_a_record_by_id_or_raises_when_it_is_gone(lab):
    server = make_server(lab)
    assert lab.current(SERVERS, 1001) is server
    with lab.state.change():
        del lab.state[SERVERS][1001]
    with pytest.raises(RecordGoneError, match="the server 1001 was deleted"):
        lab.current(SERVERS, 1001)


def test_a_failed_action_leaves_a_deleted_server_alone(lab):
    server = make_server(lab, status="starting")

    async def steps():
        with lab.state.change():
            del lab.state[SERVERS][1001]
        lab.current(SERVERS, 1001)

    async def main():
        action = lab.new_action("start_server", [(1001, "server")])
        lab.run_action(action, steps, server)
        await settle(lab)
        return action

    action = run(main())
    assert action["status"] == "error" and "deleted" in action["error"]["message"]
    assert server["status"] == "starting"  # gone: not changed, nothing asked
    assert lab.fabric.shell.calls == []


def test_the_picture_lock_refuses_a_nested_take_but_lets_other_tasks_wait(lab):
    # asyncio.Lock is not re-entrant: taken twice by one task, it would wait forever
    order = []

    async def other():
        async with lab.picture_lock:
            order.append("other")

    async def main():
        async with lab.picture_lock:
            waiting = asyncio.create_task(other())
            await asyncio.sleep(0)  # the other task waits for the lock
            with pytest.raises(RuntimeError, match="already held by this task"):
                async with lab.picture_lock:
                    pass
            order.append("main")
        await waiting

    run(main())
    assert order == ["main", "other"]
