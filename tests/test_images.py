"""Images of the project: snapshots and backups, servers made from them."""

from conftest import change_state, create_server, state_of, wait_action

from hlabd.api import images, servers
from hlabd.state import IMAGES


def server_action(api, sid, cmd, body=None):
    """Run a server action and wait for it; return the answer."""
    response = api.post(f"/v1/servers/{sid}/actions/{cmd}", json=body or {})
    assert response.status_code == 201, response.text
    wait_action(api, response.json()["action"])
    return response.json()


def take_image(api, sid, **body):
    """Take an image of a server, wait for it and return it."""
    image = server_action(api, sid, "create_image", body)["image"]
    return api.get(f"/v1/images/{image['id']}").json()["image"]


def test_snapshot_outlives_its_server_and_makes_new_servers(api, tmp_path):
    server = create_server(api, "a")["server"]
    answer = api.post(
        f"/v1/servers/{server['id']}/actions/create_image",
        json={"description": "base", "labels": {"role": "web"}},
    ).json()
    # Hetzner: the image exists at once, still being created
    assert answer["image"]["status"] == "creating" and answer["image"]["name"] is None
    wait_action(api, answer["action"])
    iid = answer["image"]["id"]
    image = api.get(f"/v1/images/{iid}").json()["image"]
    assert (image["status"], image["type"], image["bound_to"]) == (
        "available",
        "snapshot",
        None,
    )
    assert image["image_size"] == 3.0 and image["disk_size"] == 40
    assert image["os_flavor"] == "debian"
    assert image["created_from"] == {"id": server["id"], "name": "a"}
    disk = str(tmp_path / "vm" / str(server["id"]) / "disk.qcow2")
    file = str(tmp_path / "images" / "own" / f"{iid}.qcow2")
    assert ("create-image", disk, file) in api.sh.calls
    listed = api.get(
        "/v1/images", params={"type": "snapshot", "label_selector": "role=web"}
    ).json()["images"]
    assert [i["id"] for i in listed] == [iid]

    wait_action(api, api.delete(f"/v1/servers/{server['id']}").json()["action"])
    copy = create_server(api, "b", image=iid)["server"]
    assert copy["image"]["id"] == iid
    assert api.sh.commands("create-vm-disk-from-image")[-1][1:] == (
        str(tmp_path / "vm" / str(copy["id"])),
        file,
        "40",
    )
    assert api.delete(f"/v1/images/{iid}").status_code == 204
    # the server keeps its disk; the image it came from is gone
    assert api.get(f"/v1/servers/{copy['id']}").json()["server"]["image"] is None


def test_server_is_rebuilt_from_a_snapshot(api):
    sid = create_server(api, "a")["server"]["id"]
    iid = take_image(api, sid)["id"]
    server_action(api, sid, "rebuild", {"image": iid})
    assert api.get(f"/v1/servers/{sid}").json()["server"]["image"]["id"] == iid


def test_snapshot_of_a_larger_disk_does_not_fit_a_smaller_type(api):
    sid = create_server(api, "a", server_type="cx33")["server"]["id"]
    iid = take_image(api, sid)["id"]
    response = api.post(
        "/v1/servers", json={"name": "b", "server_type": "cx23", "image": iid}
    )
    assert response.json()["error"]["code"] == "invalid_input"


def test_backups_need_enabling_keep_seven_and_go_with_their_server(api):
    sid = create_server(api, "a")["server"]["id"]
    response = api.post(
        f"/v1/servers/{sid}/actions/create_image", json={"type": "backup"}
    )
    assert response.json()["error"]["code"] == "invalid_input"
    server_action(api, sid, "enable_backup")
    assert api.get(f"/v1/servers/{sid}").json()["server"]["backup_window"] == "22-02"
    taken = [take_image(api, sid, type="backup")["id"] for _ in range(8)]
    backups = api.get("/v1/images", params={"type": "backup", "bound_to": sid})
    # Hetzner keeps seven backups: the oldest one went
    assert [i["id"] for i in backups.json()["images"]] == taken[1:]
    kept = taken[-1]
    api.put(f"/v1/images/{kept}", json={"type": "snapshot"})
    wait_action(api, api.delete(f"/v1/servers/{sid}").json()["action"])
    assert set(state_of(api, "images")) == {kept}  # the converted one stays


def test_disabling_backups_deletes_them(api):
    sid = create_server(api, "a")["server"]["id"]
    server_action(api, sid, "enable_backup")
    take_image(api, sid, type="backup")
    server_action(api, sid, "disable_backup")
    assert state_of(api, "images") == {}
    assert api.get(f"/v1/servers/{sid}").json()["server"]["backup_window"] is None


def test_daily_backups_are_taken_for_servers_with_backups(api):
    with_backups = create_server(api, "a")["server"]["id"]
    create_server(api, "b")
    server_action(api, with_backups, "enable_backup")
    api.on_loop(servers.take_daily_backups, api.lab)
    taken = api.get("/v1/images", params={"type": "backup"}).json()["images"]
    wait_action(
        api,
        api.get(f"/v1/servers/{with_backups}/actions").json()["actions"][-1],
    )
    assert [i["bound_to"] for i in taken] == [with_backups]


def test_only_snapshots_are_protected_and_protection_keeps_them(api):
    sid = create_server(api, "a")["server"]["id"]
    server_action(api, sid, "enable_backup")
    backup = take_image(api, sid, type="backup")
    response = api.post(
        f"/v1/images/{backup['id']}/actions/change_protection", json={"delete": True}
    )
    assert response.json()["error"]["code"] == "invalid_input"
    snapshot = take_image(api, sid)
    path = f"/v1/images/{snapshot['id']}"
    api.post(f"{path}/actions/change_protection", json={"delete": True})
    assert api.delete(path).json()["error"]["code"] == "protected"


def test_system_images_cannot_be_changed_or_deleted(api):
    iid = api.get("/v1/images", params={"name": "debian-13"}).json()["images"][0]["id"]
    assert api.delete(f"/v1/images/{iid}").json()["error"]["code"] == "invalid_input"
    response = api.put(f"/v1/images/{iid}", json={"description": "x"})
    assert response.json()["error"]["code"] == "invalid_input"


def test_a_restart_drops_images_whose_copy_was_cut_off(api):
    sid = create_server(api, "a")["server"]["id"]
    iid = take_image(api, sid)["id"]

    image = api.on_loop(lambda: api.lab.state[IMAGES][iid])
    change_state(api, image.update, {"status": "creating"})
    api.on_loop(images.drop_half_created_images, api.lab)
    assert state_of(api, "images") == {}
