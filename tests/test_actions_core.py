"""The core of actions (lab.py): flags, `act()`, `run_action()`, stopping actions.

Each test runs its actions on its own event loop; `steps` wait for an event, so a test
can look at the flags while they run.
"""

import asyncio
import threading
import time

import pytest

from hlabd.errors import ApiError, invalid
from hlabd.lab import SERVER_LOCKED, Plan


def make_server(lab, sid=1001, status="running"):
    """Put a bare server into the state and return it."""
    server = {
        "id": sid,
        "name": f"s{sid}",
        "status": status,
        "locked": False,
        "_lab": {"index": sid - 1000, "tap": f"pub{sid - 1000}", "nics": []},
    }
    lab.state["servers"][sid] = server
    return server


def make_volume(lab, vid=10001):
    """Put a bare volume into the state and return it."""
    volume = {"id": vid, "name": f"v{vid}", "status": "available", "server": None}
    lab.state["volumes"][vid] = volume
    return volume


def run(coro):
    """Run a test's coroutine on a fresh event loop."""
    return asyncio.run(coro)


async def settle(lab):
    """Wait until every running action has ended."""
    while lab.tasks:
        await asyncio.gather(*list(lab.tasks), return_exceptions=True)


def table(function):
    """Return an action table with the command "go" (Hetzner's name "do_go")."""
    return {
        "go": ("do_go", function),
        "change_protection": ("change_protection", function),
    }


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
    lab.claim(server, "locked", "locked")
    lab.release(volume, "_busy")
    lab.release(server)
    assert "_busy" not in volume
    # the API shows a server's flag, so it stays as False
    assert server["locked"] is False
    lab.claim(volume, "_busy", "busy")  # free again


def test_holding_takes_off_what_it_holds_when_the_block_ends(lab):
    volume, server = make_volume(lab), make_server(lab)
    with lab.holding() as held:
        held.claim(volume, "_busy", "busy")
        held.claim(server, "locked", SERVER_LOCKED)
        assert volume["_busy"] is True and server["locked"] is True
    assert "_busy" not in volume and server["locked"] is False


def test_holding_takes_its_flags_off_when_the_block_fails(lab):
    volume, server = make_volume(lab), make_server(lab)
    lab.claim(server, "locked", "locked")  # another action runs on the server
    with pytest.raises(ApiError), lab.holding() as held:
        held.claim(volume, "_busy", "busy")
        held.claim(server, "locked", SERVER_LOCKED)  # 423
    assert "_busy" not in volume
    assert server["locked"] is True  # still the other action's


def test_holding_leaves_kept_and_handed_over_flags(lab):
    volume, server = make_volume(lab), make_server(lab)
    with lab.holding() as held:
        held.claim(volume, "_busy", "busy")
        held.keep()
        held.claim(server, "locked", SERVER_LOCKED)
        assert held.take() == [(server, "locked")]
    assert volume["_busy"] is True and server["locked"] is True


# --- act ------------------------------------------------------------------------------


def test_act_refuses_an_unknown_command_as_unsupported(lab):
    volume = make_volume(lab)
    with pytest.raises(ApiError) as refused:
        lab.act(table(lambda *_: Plan()), "fly", volume, "volume", {}, "_busy")
    assert (refused.value.status, refused.value.code) == (422, "unsupported_error")
    assert "_busy" not in volume


def test_act_takes_the_flag_off_when_the_plan_refuses(lab):
    volume = make_volume(lab)
    seen = []

    def refuse(lab, item, body):
        seen.append(item.get("_busy"))  # the flag is set while the plan is made
        raise invalid("no")

    with pytest.raises(ApiError):
        lab.act(table(refuse), "go", volume, "volume", {}, "_busy")
    assert seen == [True]
    assert "_busy" not in volume


def test_act_refuses_a_flagged_resource_and_leaves_its_flag(lab):
    volume = make_volume(lab)
    lab.claim(volume, "_busy", "busy")
    with pytest.raises(ApiError) as refused:
        lab.act(table(lambda *_: Plan()), "go", volume, "volume", {}, "_busy", "busy")
    assert refused.value.status == 423
    assert volume["_busy"] is True  # still the other action's


def test_act_without_steps_finishes_at_once_and_frees_the_resource(lab):
    volume = make_volume(lab)
    answer = lab.act(
        table(lambda *_: Plan(extra={"note": 1})), "go", volume, "volume", {}, "_busy"
    )
    assert answer["action"]["status"] == "success"
    assert answer["action"]["command"] == "do_go"
    assert answer["action"]["resources"] == [{"id": 10001, "type": "volume"}]
    assert answer["note"] == 1
    assert "_busy" not in volume
    assert lab.state.path.is_file()  # saved


def test_act_holds_the_flags_until_the_steps_succeed(lab):
    volume, server = make_volume(lab), make_server(lab)

    async def main():
        go = asyncio.Event()

        async def steps():
            await go.wait()

        answer = lab.act(
            table(lambda *_: Plan(steps, server=server)),
            "go",
            volume,
            "volume",
            {},
            "_busy",
        )
        await asyncio.sleep(0)
        assert volume["_busy"] is True and server["locked"] is True
        assert answer["action"]["status"] == "running"
        go.set()
        await settle(lab)
        return answer["action"]

    action = run(main())
    assert action["status"] == "success"
    assert "_busy" not in volume and server["locked"] is False


def test_act_releases_the_flags_when_the_steps_fail(lab):
    volume, server = make_volume(lab), make_server(lab)

    async def main():
        async def steps():
            raise RuntimeError("the disk broke")

        answer = lab.act(
            table(lambda *_: Plan(steps, server=server)),
            "go",
            volume,
            "volume",
            {},
            "_busy",
        )
        await settle(lab)
        return answer["action"]

    action = run(main())
    assert action["status"] == "error"
    assert action["error"] == {"code": "action_failed", "message": "the disk broke"}
    assert "_busy" not in volume and server["locked"] is False


def test_act_refuses_a_locked_plan_server_and_frees_the_resource(lab):
    volume, server = make_volume(lab), make_server(lab)
    lab.claim(server, "locked", "locked")

    async def steps():
        pass

    with pytest.raises(ApiError) as refused:
        lab.act(
            table(lambda *_: Plan(steps, server=server)),
            "go",
            volume,
            "volume",
            {},
            "_busy",
        )
    assert refused.value.status == 423
    assert "_busy" not in volume
    assert server["locked"] is True  # still the other action's


def test_act_locks_a_server_that_is_the_resource_only_once(lab):
    server = make_server(lab)

    async def main():
        async def steps():
            assert server["locked"] is True

        lab.act(
            table(lambda *_: Plan(steps, server=server)),
            "go",
            server,
            "server",
            {},
            "locked",
        )
        await settle(lab)

    run(main())
    assert server["locked"] is False


def test_record_only_actions_take_no_flag(lab):
    volume = make_volume(lab)
    lab.claim(volume, "_busy", "busy")  # another action runs
    answer = lab.act(
        table(lambda *_: Plan()), "change_protection", volume, "volume", {}, "_busy"
    )
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
            held.claim(server, "locked", SERVER_LOCKED)
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
        lab.fabric.shell.running.add(str(lab.fabric.vms.directory(server)))

    async def main():
        action = lab.new_action("start_server", [(1001, "server")])

        async def steps():
            raise RuntimeError("QEMU failed")

        with lab.holding() as held:
            held.claim(server, "locked", SERVER_LOCKED)
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
