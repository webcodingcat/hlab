"""Servers.

Create, delete, power actions, rebuild, change_type, locks, restart of the lab.
"""

import pytest
from conftest import (
    KEY,
    change_state,
    create_server,
    dhcp,
    metadata_client,
    power_off,
    state_of,
    wait_action,
)

from hlabd.api import servers, watcher
from hlabd.api.restart import restore_lab
from hlabd.state import SERVERS


def test_server_is_created_powered_off_and_on_and_deleted(api):
    key = api.post("/v1/ssh_keys", json={"name": "k", "public_key": KEY}).json()[
        "ssh_key"
    ]
    out = create_server(
        api,
        ssh_keys=[key["id"]],
        labels={"role": "web"},
        user_data="#cloud-config\n{}\n",
    )
    sid = out["server"]["id"]
    assert out["next_actions"][0]["command"] == "start_server"
    server = api.get(f"/v1/servers/{sid}").json()["server"]
    assert server["status"] == "running"
    assert server["public_net"]["ipv4"]["ip"] == "198.51.100.11"
    assert server["server_type"]["name"] == "cx23"
    assert api.sh.commands("start-vm")
    assert (
        api.get("/v1/servers", params={"label_selector": "role=web"}).json()["servers"][
            0
        ]["id"]
        == sid
    )
    assert (
        api.get("/v1/servers", params={"label_selector": "role!=web"}).json()["servers"]
        == []
    )

    wait_action(api, api.post(f"/v1/servers/{sid}/actions/poweroff").json()["action"])
    assert api.get(f"/v1/servers/{sid}").json()["server"]["status"] == "off"
    assert not api.sh.running

    wait_action(api, api.post(f"/v1/servers/{sid}/actions/poweron").json()["action"])
    assert api.get(f"/v1/servers/{sid}").json()["server"]["status"] == "running"

    wait_action(api, api.delete(f"/v1/servers/{sid}").json()["action"])
    assert api.get(f"/v1/servers/{sid}").status_code == 404
    assert api.get("/v1/primary_ips").json()["primary_ips"] == []


def test_server_name_already_taken_is_a_conflict(api):
    create_server(api, "dup")
    response = api.post(
        "/v1/servers", json={"name": "dup", "server_type": "cx23", "image": "debian-13"}
    )
    assert response.status_code == 409


def test_dead_server_stays_off(api):
    sid = create_server(api, "d1")["server"]["id"]
    wait_action(api, api.post(f"/v1/servers/{sid}/actions/poweroff").json()["action"])
    api.post("/_lab/vm/d1/dead")
    action = wait_action(
        api, api.post(f"/v1/servers/{sid}/actions/poweron").json()["action"]
    )
    assert action["status"] == "success"
    assert api.get(f"/v1/servers/{sid}").json()["server"]["status"] == "off"


def test_refused_server_create_leaves_nothing_behind(api):
    volume = api.post(
        "/v1/volumes", json={"name": "v", "size": 10, "location": "fsn1"}
    ).json()["volume"]
    fw = api.post("/v1/firewalls", json={"name": "fw"}).json()["firewall"]
    for extra in (
        {"networks": [9999]},
        {"firewalls": [{"firewall": fw["id"]}, {"firewall": 9999}]},
        {"volumes": [volume["id"]], "networks": ["x"]},
        {"user_data": "x" * (32 * 1024 + 1)},
    ):
        response = api.post(
            "/v1/servers",
            json={
                "name": "a",
                "server_type": "cx23",
                "image": "debian-13",
                "location": "fsn1",
                "volumes": [volume["id"]],
                **extra,
            },
        )
        assert response.status_code == 422, (extra, response.text)
    assert api.get("/v1/servers").json()["servers"] == []
    assert api.get("/v1/primary_ips").json()["primary_ips"] == []
    assert api.get(f"/v1/volumes/{volume['id']}").json()["volume"]["server"] is None
    assert api.get(f"/v1/firewalls/{fw['id']}").json()["firewall"]["applied_to"] == []
    create_server(api, "a", volumes=[volume["id"]])  # the name is free, the volume too


def test_a_busy_server_is_locked_and_refused_actions_do_not_linger(api):
    server = create_server(api, "a")["server"]
    api.lab.scale = 0.05  # poweroff takes ~0.5 s now
    action = api.post(f"/v1/servers/{server['id']}/actions/poweroff").json()["action"]
    for path, body in (
        ("actions/attach_to_network", {"network": 1}),
        ("actions/reset_password", {}),
        ("actions/poweron", {}),
    ):
        response = api.post(f"/v1/servers/{server['id']}/{path}", json=body)
        assert (
            response.status_code == 423 and response.json()["error"]["code"] == "locked"
        ), path
    assert api.delete(f"/v1/servers/{server['id']}").status_code == 423
    wait_action(api, action)
    api.lab.scale = 0
    # a refused action does not stay "running"
    response = api.post(
        f"/v1/servers/{server['id']}/actions/change_type", json={"server_type": "nope"}
    )
    assert response.status_code == 422
    acts = api.get(f"/v1/servers/{server['id']}/actions").json()["actions"]
    running = [x for x in acts if x["status"] == "running"]
    assert running == []


def test_restart_finishes_or_drops_what_was_half_done(api):
    server_a = create_server(api, "a")["server"]
    server_b = create_server(api, "b")["server"]
    servers = state_of(api, "servers")
    # the lab stopped while a was being deleted
    change_state(api, servers[server_a["id"]].update, {"status": "deleting"})
    api.sh.running.clear()  # a new container: no VM runs
    api.sh.fail.add("start-vm")  # and b's VM does not come up
    api.on_loop(restore_lab, api.lab)  # must not raise: the lab comes up anyway
    assert server_a["id"] not in servers
    assert servers[server_b["id"]]["status"] == "off"
    actions = state_of(api, "actions").values()
    assert not [x for x in actions if x["status"] == "running"]


def test_failed_create_ends_its_next_actions_and_status(api):
    api.sh.fail.add("start-vm")
    response = api.post(
        "/v1/servers",
        json={
            "name": "a",
            "server_type": "cx23",
            "image": "debian-13",
            "location": "fsn1",
        },
    )
    out = response.json()
    assert wait_action(api, out["action"])["status"] == "error"
    assert wait_action(api, out["next_actions"][0])["status"] == "error"
    server = api.get(f"/v1/servers/{out['server']['id']}").json()["server"]
    assert server["status"] == "off" and not state_of(api, "servers")[server["id"]].get(
        "_locked"
    )


def test_change_type_grows_the_disk_only_when_asked(api, tmp_path):
    server = create_server(api, "a")["server"]
    power_off(api, server["id"])

    def change(type_name, upgrade):
        return api.post(
            f"/v1/servers/{server['id']}/actions/change_type",
            json={"server_type": type_name, "upgrade_disk": upgrade},
        )

    wait_action(api, change("cx33", False).json()["action"])  # the disk keeps its 40 GB
    assert (
        api.get(f"/v1/servers/{server['id']}").json()["server"]["primary_disk_size"]
        == 40
    )
    wait_action(api, change("cx23", False).json()["action"])  # and so it may go back
    wait_action(api, change("cx33", True).json()["action"])  # the disk grows to 80 GB
    assert (
        api.get(f"/v1/servers/{server['id']}").json()["server"]["primary_disk_size"]
        == 80
    )
    assert (
        "resize-vm-disk",
        str(tmp_path / "vm" / str(server["id"])),
        "80",
    ) in api.sh.calls
    response = change("cx23", False)  # no way back to 40 GB
    assert (
        response.status_code == 422
        and response.json()["error"]["code"] == "invalid_server_type"
    )


def test_shutdown_is_done_when_the_button_is_pressed(api):
    server = create_server(api, "a")["server"]
    action = wait_action(
        api, api.post(f"/v1/servers/{server['id']}/actions/shutdown").json()["action"]
    )
    assert action["status"] == "success"
    # the fake guest ignores the button: as on Hetzner it keeps running, nobody pulls
    # the plug
    assert (
        api.get(f"/v1/servers/{server['id']}").json()["server"]["status"] == "running"
    )
    assert not [c for c in api.sh.calls if c[0] == "kill-vm"]


def test_failed_rebuild_keeps_the_disk(api):
    server = create_server(api, "a")["server"]
    api.lab.images.pop("ubuntu-26.04")  # the new image could not be loaded
    action = api.post(
        f"/v1/servers/{server['id']}/actions/rebuild", json={"image": "ubuntu-26.04"}
    ).json()["action"]
    assert wait_action(api, action)["status"] == "error"
    # the old disk was never touched
    assert not [c for c in api.sh.calls if c[0] == "kill-vm"]
    assert (
        api.get(f"/v1/servers/{server['id']}").json()["server"]["image"]["name"]
        == "debian-13"
    )


def test_root_password_only_without_ssh_keys(api):
    response = api.post(
        "/v1/servers",
        json={
            "name": "a",
            "server_type": "cx23",
            "image": "debian-13",
            "location": "fsn1",
        },
    )
    pw = response.json()["root_password"]
    assert pw
    client = metadata_client(api, "198.51.100.11", 1)
    vendor = client.get("/hetzner/v1/metadata").text
    assert "ssh_pwauth: true" in vendor and pw in vendor
    api.post("/v1/ssh_keys", json={"name": "k", "public_key": KEY})
    response = api.post(
        "/v1/servers",
        json={
            "name": "b",
            "server_type": "cx23",
            "image": "debian-13",
            "location": "fsn1",
            "ssh_keys": ["k"],
        },
    )
    assert response.json()["root_password"] is None


def test_rebuild_takes_new_user_data(api):
    server = create_server(api, "a", user_data="#cloud-config\n")["server"]
    response = api.post(
        f"/v1/servers/{server['id']}/actions/rebuild",
        json={"image": "debian-13", "user_data": "#cloud-config\nx: 1\n"},
    )
    wait_action(api, response.json()["action"])
    assert (
        state_of(api, "servers")[server["id"]]["user_data"] == "#cloud-config\nx: 1\n"
    )


def test_a_vm_that_ends_by_itself_leaves_the_server_off(api):
    server = create_server(api, "a")["server"]
    api.sh.running.clear()  # the guest powered off
    api.on_loop(watcher.watch_vms, api.lab)
    assert api.get(f"/v1/servers/{server['id']}").json()["server"]["status"] == "off"
    response = api.post(f"/v1/servers/{server['id']}/actions/poweron")
    wait_action(api, response.json()["action"])
    assert (
        api.get(f"/v1/servers/{server['id']}").json()["server"]["status"] == "running"
    )


def test_the_vm_watcher_frees_its_server_and_leaves_a_busy_one_alone(api):
    first = create_server(api, "a")["server"]
    second = create_server(api, "b")["server"]
    servers = state_of(api, "servers")
    api.sh.running.clear()  # both guests powered off
    # an action runs on b
    api.on_loop(api.lab.claim, servers[second["id"]], "_locked", "locked")
    api.on_loop(watcher.stop_if_ended, api.lab, servers[first["id"]])
    api.on_loop(watcher.stop_if_ended, api.lab, servers[second["id"]])
    assert servers[first["id"]]["status"] == "off"
    assert "_locked" not in servers[first["id"]]
    # the action owns b: its status is the action's to change, and so is its flag
    assert servers[second["id"]]["status"] == "running"
    assert servers[second["id"]]["_locked"] is True


def test_rebuild_keeps_a_disk_kept_small(api):
    server = create_server(api, "a")["server"]
    power_off(api, server["id"])
    response = api.post(
        f"/v1/servers/{server['id']}/actions/change_type",
        json={"server_type": "cx33", "upgrade_disk": False},
    )
    wait_action(api, response.json()["action"])
    response = api.post(
        f"/v1/servers/{server['id']}/actions/rebuild", json={"image": "debian-13"}
    )
    wait_action(api, response.json()["action"])
    assert state_of(api, "servers")[server["id"]]["_lab"]["disk_gb"] == 40
    assert (
        api.get(f"/v1/servers/{server['id']}").json()["server"]["primary_disk_size"]
        == 40
    )


def test_power_on_a_running_server_changes_nothing(api):
    server = create_server(api, "a")["server"]
    starts = len(api.sh.commands("set-up-public-tap"))
    action = api.post(f"/v1/servers/{server['id']}/actions/poweron").json()["action"]
    assert wait_action(api, action)["status"] == "success"
    assert len(api.sh.commands("set-up-public-tap")) == starts


def test_new_server_is_locked_while_created(api):
    response = api.post(
        "/v1/servers",
        json={
            "name": "a",
            "server_type": "cx23",
            "image": "debian-13",
            "location": "fsn1",
        },
    )
    assert response.json()["server"]["locked"] is True
    wait_action(api, response.json()["action"])
    assert (
        api.get(f"/v1/servers/{response.json()['server']['id']}").json()["server"][
            "locked"
        ]
        is False
    )


def test_failed_rebuild_keeps_user_data(api):
    server = create_server(api, "a", user_data="#cloud-config\n")["server"]
    api.sh.fail.add("create-vm-disk")
    response = api.post(
        f"/v1/servers/{server['id']}/actions/rebuild",
        json={"image": "debian-13", "user_data": "#new\n"},
    )
    assert wait_action(api, response.json()["action"])["status"] == "error"
    assert state_of(api, "servers")[server["id"]]["user_data"] == "#cloud-config\n"


def test_rebuild_answers_the_new_root_password_and_stores_it_only_when_it_runs(api):
    server = create_server(api, "a")["server"]
    old = state_of(api, "servers")[server["id"]]["_lab"]["root_password"]
    url = f"/v1/servers/{server['id']}/actions/rebuild"
    # a refused rebuild (the image is unknown) keeps the old password
    assert api.post(url, json={"image": "nope"}).status_code == 422
    assert state_of(api, "servers")[server["id"]]["_lab"]["root_password"] == old
    response = api.post(url, json={"image": "debian-13"}).json()
    assert response["root_password"] not in (None, old)
    wait_action(api, response["action"])
    stored = state_of(api, "servers")[server["id"]]["_lab"]["root_password"]
    assert stored == response["root_password"]


def test_vm_has_hetzner_smbios_and_dhcp(api, tmp_path):
    server = create_server(api, "a")["server"]
    start = [c for c in api.sh.calls if c[0] == "start-vm"][-1]
    # SMBIOS serial = instance ID (cloud-init checks it)
    assert start[5] == str(server["id"])
    assert "dhcp-host=96:00:00:00:00:01,198.51.100.11" in dhcp(tmp_path)
    assert ("set-up-public-tap", "pub1", "198.51.100.11") in api.sh.calls


def test_reset_password_needs_the_guest_agent(api):
    server = create_server(api, "c")["server"]
    response = api.post(f"/v1/servers/{server['id']}/actions/reset_password").json()
    assert response["root_password"]
    action = api.get(f"/v1/actions/{response['action']['id']}").json()["action"]
    for _ in range(100):
        if action["status"] != "running":
            break
        action = api.get(f"/v1/actions/{response['action']['id']}").json()["action"]
    # the fake shell has no agent
    assert action["status"] == "error" and "guest agent" in action["error"]["message"]


def test_ssh_keys_have_a_fingerprint_and_are_unique(api):
    ssh_key = api.post("/v1/ssh_keys", json={"name": "k", "public_key": KEY}).json()[
        "ssh_key"
    ]
    assert len(ssh_key["fingerprint"].split(":")) == 16
    assert (
        api.post("/v1/ssh_keys", json={"name": "k2", "public_key": KEY}).status_code
        == 409
    )
    assert (
        api.get("/v1/ssh_keys", params={"fingerprint": ssh_key["fingerprint"]}).json()[
            "ssh_keys"
        ][0]["id"]
        == ssh_key["id"]
    )


def test_ssh_keys_are_taken_only_as_hetzner_takes_them(api):
    assert (
        api.post(
            "/v1/ssh_keys", json={"name": "a", "public_key": " " + KEY}
        ).status_code
        == 422
    )
    assert (
        api.post(
            "/v1/ssh_keys",
            json={"name": "b", "public_key": "ssh-ed25519 aGVsbG8gd29ybGQ="},
        ).status_code
        == 422
    )
    assert (
        api.post("/v1/ssh_keys", json={"name": "c", "public_key": KEY}).status_code
        == 201
    )


def test_delete_and_rebuild_protection_change_together(api):
    server = create_server(api, "a")["server"]
    response = api.post(
        f"/v1/servers/{server['id']}/actions/change_protection",
        json={"delete": True, "rebuild": False},
    )
    assert response.status_code == 422


def test_a_delete_that_cannot_start_leaves_the_server_as_it_was(api, monkeypatch):
    server = state_of(api, "servers")[create_server(api, "a")["server"]["id"]]

    def no_background(*_args, **_kwargs):
        raise RuntimeError("no event loop")

    monkeypatch.setattr(api.lab, "run_action", no_background)
    with pytest.raises(RuntimeError):
        api.on_loop(servers.delete_server, api.lab, server["id"])
    assert server["status"] == "running"
    assert "_locked" not in server


@pytest.mark.parametrize("command", ["poweroff", "poweron", "reboot", "rebuild"])
def test_a_server_deleted_while_its_action_waits_ends_the_action_with_an_error(
    api, monkeypatch, command
):
    server = create_server(api, "a")["server"]
    sid, lab = server["id"], api.lab
    if command == "poweron":
        power_off(api, sid)

    async def sleep_and_delete(seconds):
        # someone else removes the record while the steps wait (a lab reset, say)
        with lab.state.change():
            lab.state[SERVERS].pop(sid, None)

    monkeypatch.setattr(lab, "sleep", sleep_and_delete)
    body = {"image": "debian-13"} if command == "rebuild" else {}
    answer = api.post(f"/v1/servers/{sid}/actions/{command}", json=body)
    assert answer.status_code == 201, answer.text
    done = wait_action(api, answer.json()["action"])
    assert done["status"] == "error"
    assert done["error"]["message"] == (
        f"the server {sid} was deleted while the action ran"
    )
    assert sid not in state_of(api, "servers")  # nothing brought it back
