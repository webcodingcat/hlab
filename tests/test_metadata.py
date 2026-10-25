"""The metadata service: what cloud-init gets, and how the caller is found."""

from fastapi.testclient import TestClient

from conftest import create_server, KEY, metadata, create_network
from hlabd.api.metadata import create_metadata_app


def test_metadata_for_cloud_init(api):
    ssh_key = api.post("/v1/ssh_keys", json={"name": "k", "public_key": KEY}).json()["ssh_key"]
    server = create_server(api, "web", ssh_keys=[ssh_key["id"]], user_data="#cloud-config\nhostname: x\n")["server"]
    md = metadata(api, "198.51.100.11")
    assert md["instance-id"] == server["id"] and md["hostname"] == "web"
    assert KEY in md["public-keys"]
    assert md["network-config"]["config"][0]["mac_address"] == "96:00:00:00:00:01"
    assert "disable_root: false" in md["vendor_data"]
    with TestClient(create_metadata_app(api.lab), client=("198.51.100.11", 1)) as client:
        assert client.get("/hetzner/v1/userdata").text.startswith("#cloud-config")
        assert client.get("/hetzner/v1/metadata/hostname").text == "web"
        assert client.get("/hetzner/v1/metadata/private-networks").text.strip() == "[]"
    s2 = create_server(api, "empty")["server"]
    with TestClient(create_metadata_app(api.lab), client=(s2["public_net"]["ipv4"]["ip"], 1)) as client:
        assert client.get("/hetzner/v1/userdata").status_code == 204   # cloud-init would wait for content


def test_metadata_lists_cloud_networks_in_both_places(api):
    network = api.post("/v1/networks", json={"name": "n", "ip_range": "10.0.0.0/16", "subnets": [
        {"type": "cloud", "ip_range": "10.0.1.0/24", "network_zone": "eu-central"}]}).json()["network"]
    create_server(api, "p", networks=[network["id"]])
    md = metadata(api, "198.51.100.11")
    assert md["private-networks"][0]["ip"] == "10.0.1.1"
    assert metadata(api, "198.51.100.11", "/private-networks")[0]["mac_address"] == "86:00:00:00:01:01"


def test_metadata_by_private_ip_needs_a_single_tap(api):
    """Networks may overlap: a private caller is told apart by the tap it sits on, never guessed."""
    body = {"ip_range": "10.0.0.0/16", "subnets": [{"type": "cloud", "ip_range": "10.0.1.0/24",
                                                    "network_zone": "eu-central"}]}
    n1 = api.post("/v1/networks", json={"name": "one", **body}).json()["network"]
    n2 = api.post("/v1/networks", json={"name": "two", **body}).json()["network"]
    create_server(api, "a", networks=[n1["id"]])
    create_server(api, "b", networks=[n2["id"]])
    api.sh.neigh["10.0.1.1"] = "prv2x1"
    assert metadata(api, "10.0.1.1")["hostname"] == "b"
    api.sh.neigh["10.0.1.1"] = "prv1x1\nprv2x1"
    with TestClient(create_metadata_app(api.lab), client=("10.0.1.1", 40000)) as client:
        assert client.get("/hetzner/v1/metadata").status_code == 404


def test_metadata_by_private_ip(api):
    network = create_network(api, "10.0.1.0/24")
    server_a = create_server(api, "a", networks=[network["id"]])["server"]
    create_server(api, "b", networks=[network["id"]])
    # metadata by private IP: only a tap that holds the address counts (the neighbour table may know it elsewhere)
    api.sh.neigh["10.0.1.1"] = "prv1x1 prv2x1"
    with TestClient(create_metadata_app(api.lab), client=("10.0.1.1", 40000)) as client:
        assert client.get("/hetzner/v1/metadata/instance-id").text == str(server_a["id"])
    api.sh.neigh["10.0.1.1"] = "prv2x1"
    with TestClient(create_metadata_app(api.lab), client=("10.0.1.1", 40000)) as client:
        assert client.get("/hetzner/v1/metadata/instance-id").status_code == 404
