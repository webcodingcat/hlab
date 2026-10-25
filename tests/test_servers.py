"""Servers: create, delete, power actions, rebuild, change_type, locks, restart of the lab."""

import asyncio

from fastapi.testclient import TestClient

from conftest import create_server, dhcp, KEY, metadata, network, off, wait_action
from hlabd.api.metadata import create_metadata_app
from hlabd.api.restart import reconcile


def test_server_lifecycle(api):
    key = api.post("/v1/ssh_keys", json={"name": "k", "public_key": KEY}).json()["ssh_key"]
    out = create_server(api, ssh_keys=[key["id"]], labels={"role": "web"}, user_data="#cloud-config\n{}\n")
    sid = out["server"]["id"]
    assert out["next_actions"][0]["command"] == "start_server"
    s = api.get(f"/v1/servers/{sid}").json()["server"]
    assert s["status"] == "running"
    assert s["public_net"]["ipv4"]["ip"] == "198.51.100.11"
    assert s["server_type"]["name"] == "cx23"
    assert api.sh.commands("vm-start")
    assert api.get("/v1/servers", params={"label_selector": "role=web"}).json()["servers"][0]["id"] == sid
    assert api.get("/v1/servers", params={"label_selector": "role!=web"}).json()["servers"] == []

    wait_action(api, api.post(f"/v1/servers/{sid}/actions/poweroff").json()["action"])
    assert api.get(f"/v1/servers/{sid}").json()["server"]["status"] == "off"
    assert not api.sh.running

    wait_action(api, api.post(f"/v1/servers/{sid}/actions/poweron").json()["action"])
    assert api.get(f"/v1/servers/{sid}").json()["server"]["status"] == "running"

    wait_action(api, api.delete(f"/v1/servers/{sid}").json()["action"])
    assert api.get(f"/v1/servers/{sid}").status_code == 404
    assert api.get("/v1/primary_ips").json()["primary_ips"] == []


def test_duplicate_name(api):
    create_server(api, "dup")
    r = api.post("/v1/servers", json={"name": "dup", "server_type": "cx23", "image": "debian-13"})
    assert r.status_code == 409


def test_dead_server_stays_off(api):
    sid = create_server(api, "d1")["server"]["id"]
    wait_action(api, api.post(f"/v1/servers/{sid}/actions/poweroff").json()["action"])
    api.post("/_lab/vm/d1/dead")
    a = wait_action(api, api.post(f"/v1/servers/{sid}/actions/poweron").json()["action"])
    assert a["status"] == "success"
    assert api.get(f"/v1/servers/{sid}").json()["server"]["status"] == "off"


def test_refused_server_create_leaves_nothing_behind(api):
    v = api.post("/v1/volumes", json={"name": "v", "size": 10, "location": "fsn1"}).json()["volume"]
    fw = api.post("/v1/firewalls", json={"name": "fw"}).json()["firewall"]
    for extra in ({"networks": [9999]}, {"firewalls": [{"firewall": fw["id"]}, {"firewall": 9999}]},
                  {"volumes": [v["id"]], "networks": ["x"]}, {"user_data": "x" * (32 * 1024 + 1)}):
        r = api.post("/v1/servers", json={"name": "a", "server_type": "cx23", "image": "debian-13",
                                          "location": "fsn1", "volumes": [v["id"]], **extra})
        assert r.status_code == 422, (extra, r.text)
    assert api.get("/v1/servers").json()["servers"] == []
    assert api.get("/v1/primary_ips").json()["primary_ips"] == []
    assert api.get(f"/v1/volumes/{v['id']}").json()["volume"]["server"] is None
    assert api.get(f"/v1/firewalls/{fw['id']}").json()["firewall"]["applied_to"] == []
    create_server(api, "a", volumes=[v["id"]])                     # the name is free, the volume too


def test_one_action_at_a_time_and_no_phantom_actions(api):
    s = create_server(api, "a")["server"]
    api.lab.scale = 0.05                                            # poweroff takes ~0.5 s now
    a = api.post(f"/v1/servers/{s['id']}/actions/poweroff").json()["action"]
    for path, body in (("actions/attach_to_network", {"network": 1}), ("actions/reset_password", {}),
                       ("actions/poweron", {})):
        r = api.post(f"/v1/servers/{s['id']}/{path}", json=body)
        assert r.status_code == 423 and r.json()["error"]["code"] == "locked", path
    assert api.delete(f"/v1/servers/{s['id']}").status_code == 423
    wait_action(api, a)
    api.lab.scale = 0
    # a refused action does not stay "running"
    r = api.post(f"/v1/servers/{s['id']}/actions/change_type", json={"server_type": "nope"})
    assert r.status_code == 422
    acts = api.get(f"/v1/servers/{s['id']}/actions").json()["actions"]
    running = [x for x in acts if x["status"] == "running"]
    assert running == []


def test_restart_finishes_or_drops_what_was_half_done(api):
    import asyncio
    a = create_server(api, "a")["server"]
    b = create_server(api, "b")["server"]
    st = api.lab.state
    st["servers"][a["id"]]["status"] = "deleting"       # the lab stopped while a was being deleted
    api.sh.running.clear()                              # a new container: no VM runs
    api.sh.fail.add("vm-start")                         # and b's VM does not come up
    asyncio.run(reconcile(api.lab))                     # must not raise: the lab comes up anyway
    assert a["id"] not in st["servers"]
    assert st["servers"][b["id"]]["status"] == "off"
    assert not [x for x in st["actions"].values() if x["status"] == "running"]


def test_failed_create_ends_its_next_actions_and_status(api):
    api.sh.fail.add("vm-start")
    r = api.post("/v1/servers", json={"name": "a", "server_type": "cx23", "image": "debian-13", "location": "fsn1"})
    out = r.json()
    assert wait_action(api, out["action"])["status"] == "error"
    assert wait_action(api, out["next_actions"][0])["status"] == "error"
    s = api.get(f"/v1/servers/{out['server']['id']}").json()["server"]
    assert s["status"] == "off" and not api.lab.state["servers"][s["id"]]["locked"]


def test_change_type_and_the_disk(api, tmp_path):
    s = create_server(api, "a")["server"]
    off(api, s["id"])

    def change(t, upgrade):
        return api.post(f"/v1/servers/{s['id']}/actions/change_type", json={"server_type": t, "upgrade_disk": upgrade})

    wait_action(api, change("cx33", False).json()["action"])         # the disk keeps its 40 GB
    assert api.get(f"/v1/servers/{s['id']}").json()["server"]["primary_disk_size"] == 40
    wait_action(api, change("cx23", False).json()["action"])         # and so it may go back
    wait_action(api, change("cx33", True).json()["action"])          # the disk grows to 80 GB
    assert api.get(f"/v1/servers/{s['id']}").json()["server"]["primary_disk_size"] == 80
    assert ("vm-disk-resize", str(tmp_path / "vm" / str(s["id"])), "80") in api.sh.calls
    r = change("cx23", False)                                         # no way back to 40 GB
    assert r.status_code == 422 and r.json()["error"]["code"] == "invalid_server_type"


def test_shutdown_is_done_when_the_button_is_pressed(api):
    s = create_server(api, "a")["server"]
    a = wait_action(api, api.post(f"/v1/servers/{s['id']}/actions/shutdown").json()["action"])
    assert a["status"] == "success"
    # the fake guest ignores the button: as on Hetzner it keeps running, nobody pulls the plug
    assert api.get(f"/v1/servers/{s['id']}").json()["server"]["status"] == "running"
    assert not [c for c in api.sh.calls if c[0] == "vm-kill"]


def test_failed_rebuild_keeps_the_disk(api):
    s = create_server(api, "a")["server"]
    api.lab.images.pop("ubuntu-26.04")   # the new image could not be loaded
    a = api.post(f"/v1/servers/{s['id']}/actions/rebuild", json={"image": "ubuntu-26.04"}).json()["action"]
    assert wait_action(api, a)["status"] == "error"
    assert not [c for c in api.sh.calls if c[0] == "vm-kill"]           # the old disk was never touched
    assert api.get(f"/v1/servers/{s['id']}").json()["server"]["image"]["name"] == "debian-13"


def test_root_password_without_ssh_keys(api):
    r = api.post("/v1/servers", json={"name": "a", "server_type": "cx23", "image": "debian-13", "location": "fsn1"})
    pw = r.json()["root_password"]
    assert pw
    with TestClient(create_metadata_app(api.lab), client=("198.51.100.11", 1)) as c:
        vendor = c.get("/hetzner/v1/metadata").text
    assert "ssh_pwauth: true" in vendor and pw in vendor
    api.post("/v1/ssh_keys", json={"name": "k", "public_key": KEY})
    r = api.post("/v1/servers", json={"name": "b", "server_type": "cx23", "image": "debian-13", "location": "fsn1",
                                      "ssh_keys": ["k"]})
    assert r.json()["root_password"] is None


def test_rebuild_with_new_user_data(api):
    s = create_server(api, "a", user_data="#cloud-config\n")["server"]
    r = api.post(f"/v1/servers/{s['id']}/actions/rebuild", json={"image": "debian-13", "user_data": "#cloud-config\nx: 1\n"})
    wait_action(api, r.json()["action"])
    assert api.lab.state["servers"][s["id"]]["user_data"] == "#cloud-config\nx: 1\n"


def test_a_vm_that_ends_by_itself(api):
    s = create_server(api, "a")["server"]
    api.sh.running.clear()   # the guest powered off
    api.lab.watch_vms()
    assert api.get(f"/v1/servers/{s['id']}").json()["server"]["status"] == "off"
    r = api.post(f"/v1/servers/{s['id']}/actions/poweron")
    wait_action(api, r.json()["action"])
    assert api.get(f"/v1/servers/{s['id']}").json()["server"]["status"] == "running"


def test_rebuild_keeps_a_disk_kept_small(api):
    s = create_server(api, "a")["server"]
    off(api, s["id"])
    r = api.post(f"/v1/servers/{s['id']}/actions/change_type", json={"server_type": "cx33", "upgrade_disk": False})
    wait_action(api, r.json()["action"])
    r = api.post(f"/v1/servers/{s['id']}/actions/rebuild", json={"image": "debian-13"})
    wait_action(api, r.json()["action"])
    assert api.lab.state["servers"][s["id"]]["_lab"]["disk_gb"] == 40
    assert api.get(f"/v1/servers/{s['id']}").json()["server"]["primary_disk_size"] == 40


def test_power_on_a_running_server_changes_nothing(api):
    s = create_server(api, "a")["server"]
    starts = len(api.sh.commands("public-tap-up"))
    a = api.post(f"/v1/servers/{s['id']}/actions/poweron").json()["action"]
    assert wait_action(api, a)["status"] == "success"
    assert len(api.sh.commands("public-tap-up")) == starts


def test_new_server_is_locked_while_created(api):
    r = api.post("/v1/servers", json={"name": "a", "server_type": "cx23", "image": "debian-13", "location": "fsn1"})
    assert r.json()["server"]["locked"] is True
    wait_action(api, r.json()["action"])
    assert api.get(f"/v1/servers/{r.json()['server']['id']}").json()["server"]["locked"] is False


def test_failed_rebuild_keeps_user_data(api):
    s = create_server(api, "a", user_data="#cloud-config\n")["server"]
    api.sh.fail.add("vm-disk")
    r = api.post(f"/v1/servers/{s['id']}/actions/rebuild", json={"image": "debian-13", "user_data": "#new\n"})
    assert wait_action(api, r.json()["action"])["status"] == "error"
    assert api.lab.state["servers"][s["id"]]["user_data"] == "#cloud-config\n"


def test_vm_has_hetzner_smbios_and_dhcp(api, tmp_path):
    s = create_server(api, "a")["server"]
    start = [c for c in api.sh.calls if c[0] == "vm-start"][-1]
    assert start[5] == str(s["id"])  # SMBIOS serial = instance ID (cloud-init checks it)
    assert "dhcp-host=96:00:00:00:00:01,198.51.100.11" in dhcp(tmp_path)
    assert ("public-tap-up", "pub1", "198.51.100.11") in api.sh.calls


def test_reset_password_needs_the_guest_agent(api):
    s = create_server(api, "c")["server"]
    r = api.post(f"/v1/servers/{s['id']}/actions/reset_password").json()
    assert r["root_password"]
    a = api.get(f"/v1/actions/{r['action']['id']}").json()["action"]
    for _ in range(100):
        if a["status"] != "running":
            break
        a = api.get(f"/v1/actions/{r['action']['id']}").json()["action"]
    assert a["status"] == "error" and "guest agent" in a["error"]["message"]  # the fake shell has no agent


def test_ssh_key_fingerprint_and_uniqueness(api):
    k = api.post("/v1/ssh_keys", json={"name": "k", "public_key": KEY}).json()["ssh_key"]
    assert len(k["fingerprint"].split(":")) == 16
    assert api.post("/v1/ssh_keys", json={"name": "k2", "public_key": KEY}).status_code == 409
    assert api.get("/v1/ssh_keys", params={"fingerprint": k["fingerprint"]}).json()["ssh_keys"][0]["id"] == k["id"]


def test_ssh_keys_as_hetzner_takes_them(api):
    assert api.post("/v1/ssh_keys", json={"name": "a", "public_key": " " + KEY}).status_code == 422
    assert api.post("/v1/ssh_keys", json={"name": "b", "public_key": "ssh-ed25519 aGVsbG8gd29ybGQ="}).status_code == 422
    assert api.post("/v1/ssh_keys", json={"name": "c", "public_key": KEY}).status_code == 201
