"""Internet clients: their addresses, and their faults going with them."""

from conftest import state_of


def test_client_has_ipv6(api):
    client = api.post("/_lab/clients", json={"name": "alice"}).json()["client"]
    assert client["ip6"] == "2001:db8:ffff::b"
    assert (
        "add-client",
        "alice",
        "cl1",
        "192.0.2.11",
        "2001:db8:ffff::b",
    ) in api.sh.calls


def test_client_goes_with_its_faults(api):
    api.post("/_lab/clients", json={"name": "c1"})
    api.post("/_lab/netem", json={"vm": "c1", "args": "delay 50ms"})
    assert state_of(api, "netem") == {"cl1": "delay 50ms"}
    assert (
        "tc",
        "qdisc",
        "replace",
        "dev",
        "cl1",
        "root",
        "netem",
        "delay",
        "50ms",
    ) in api.sh.calls
    api.delete("/_lab/clients/c1")
    assert state_of(api, "netem") == {}


def test_client_names_are_plain_ascii_namespace_names(api):
    for name in ("bob-2", "Alice_1", "9lives"):
        assert api.post("/_lab/clients", json={"name": name}).status_code == 200
    for name in ("-opt", "_x", "a b", "ä", "../etc", "", "x" * 13, 7):
        response = api.post("/_lab/clients", json={"name": name})
        assert response.status_code == 422, name
        assert "client name" in response.json()["error"]["message"]
