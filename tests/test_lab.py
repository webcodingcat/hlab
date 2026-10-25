"""Lab control: faults, latency, netem, Internet clients, anti-spoofing, reset, hlab env."""

from conftest import create_server, nft_set, wait_action
from hlabd import cli


def test_cut_fault_sets_nft_elements(api):
    create_server(api, "a")
    create_server(api, "b")
    assert api.post("/_lab/faults", json={"type": "cut", "a": "a", "b": "b", "net": "public"}).status_code == 200
    assert '"pub1" . "pub2"' in nft_set(api, "cuts")
    assert api.post("/_lab/faults", json={"type": "cut", "a": "a", "b": "nope"}).status_code == 404
    # faults follow the server, not its name: a rename keeps the cut
    sid = api.get("/v1/servers", params={"name": "a"}).json()["servers"][0]["id"]
    api.put(f"/v1/servers/{sid}", json={"name": "renamed"})
    api.post("/_lab/faults", json={"type": "isolate", "vm": "b"})
    assert '"pub1" . "pub2"' in nft_set(api, "cuts")


def test_latency_between_locations(api):
    create_server(api, "a", location="fsn1")
    api.sh.calls.clear()
    create_server(api, "b", location="hel1")
    netem = [c for c in api.sh.calls if c[0] == "tc" and "netem" in c]
    assert any("12.0ms" in c for c in netem)  # half of the 24 ms round trip, on each side


def test_reset(api):
    create_server(api, "a")
    api.post("/v1/networks", json={"name": "n", "ip_range": "10.0.0.0/16"})
    assert api.post("/_lab/reset").status_code == 200
    assert api.get("/v1/servers").json()["servers"] == []
    assert api.get("/v1/networks").json()["networks"] == []
    assert create_server(api, "again")["server"]["id"] == 1002   # IDs go on counting, as in the cloud


def test_client_goes_with_its_faults(api):
    api.post("/_lab/clients", json={"name": "c1"})
    api.post("/_lab/netem", json={"vm": "c1", "args": "delay 50ms"})
    assert api.lab.state.data["netem"] == {"cl1": "delay 50ms"}
    assert ("tc", "qdisc", "replace", "dev", "cl1", "root", "netem", "delay", "50ms") in api.sh.calls
    api.delete("/_lab/clients/c1")
    assert api.lab.state.data["netem"] == {}


def test_netem_takes_only_text(api):
    create_server(api, "a")
    assert api.post("/_lab/netem", json={"vm": "a", "args": 5}).status_code == 422
    assert api.post("/_lab/netem", json={"vm": "a", "args": "delay  50ms"}).status_code == 200
    assert set(api.lab.state.data["netem"].values()) == {"delay 50ms"}
    api.lab.fabric.apply_latency()   # stored values always split


def test_env_quotes_its_values(api, capsys, monkeypatch):
    monkeypatch.setattr(cli.C, "load", lambda _: {"name": "demo", "api": {"token": "a b;$(x)"}})
    cli.env()
    assert "export HCLOUD_TOKEN='a b;$(x)'" in capsys.readouterr().out


def test_latency_filters_ipv6(api):
    create_server(api, "a", location="fsn1")
    api.sh.calls.clear()
    create_server(api, "b", location="hel1")
    assert any(c[0] == "tc" and "ip6" in c and "2001:db8:1::/64" in c for c in api.sh.calls)


def test_client_has_ipv6(api):
    client = api.post("/_lab/clients", json={"name": "alice"}).json()["client"]
    assert client["ip6"] == "2001:db8:ffff::b"
    assert ("client-add", "alice", "cl1", "192.0.2.11", "2001:db8:ffff::b") in api.sh.calls


def test_antispoofing_sets_follow_the_addresses(api):
    server = create_server(api, "a")["server"]
    floating_ip = api.post("/v1/floating_ips", json={"type": "ipv4", "home_location": "fsn1"}).json()["floating_ip"]
    wait_action(api, api.post(f"/v1/floating_ips/{floating_ip['id']}/actions/assign", json={"server": server["id"]}).json()["action"])
    src4 = nft_set(api, "src4")
    assert '"pub1" . 198.51.100.11' in src4 and f'"pub1" . {floating_ip["ip"]}' in src4
    assert '"pub1" . 2001:db8:1::/64' in nft_set(api, "src6")
