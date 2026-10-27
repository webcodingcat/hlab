"""Lab control: reset."""

from conftest import create_server


def test_reset_empties_the_lab_but_ids_go_on_counting(api):
    first = create_server(api, "a")["server"]
    api.post("/v1/networks", json={"name": "n", "ip_range": "10.0.0.0/16"})
    assert api.post("/_lab/reset").status_code == 200
    assert api.get("/v1/servers").json()["servers"] == []
    assert api.get("/v1/networks").json()["networks"] == []
    # IDs go on counting, as in the cloud
    assert create_server(api, "again")["server"]["id"] == first["id"] + 1


def test_reset_shuts_the_apis_and_refuses_a_second_one(api):
    # while a reset runs, the APIs answer 503 and a second reset 409
    api.on_loop(lambda: setattr(api.lab, "resetting", True))
    assert api.get("/v1/servers").json()["error"]["code"] == "unavailable"
    assert api.post("/_lab/reset").json()["error"]["code"] == "conflict"
    api.on_loop(lambda: setattr(api.lab, "resetting", False))
    assert api.get("/v1/servers").status_code == 200


def test_reset_goes_on_past_a_failed_step(api):
    create_server(api, "a")
    api.sh.fail.add("kill-vm")
    answer = api.post("/_lab/reset").json()
    assert answer["ok"] is False and answer["failed"]
    # the rest of the lab is gone anyway
    assert api.get("/v1/servers").json()["servers"] == []
