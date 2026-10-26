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
