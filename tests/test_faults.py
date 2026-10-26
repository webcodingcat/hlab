"""Faults and the packet path: API faults, cut links, netem, latency, anti-spoofing."""

from conftest import create_server, nft_set, wait_action


def test_injected_api_faults_answer_until_used_up(api):
    api.post("/_lab/api-fault", json={"status": 503, "count": 2})
    assert api.get("/v1/servers").status_code == 503
    assert api.get("/v1/servers").status_code == 503
    assert api.get("/v1/servers").status_code == 200


def test_cut_fault_needs_known_servers(api):
    create_server(api, "a")
    assert (
        api.post(
            "/_lab/faults", json={"type": "cut", "first": "a", "second": "nope"}
        ).status_code
        == 404
    )


def test_cut_fault_follows_the_server_not_its_name(api):
    create_server(api, "a")
    create_server(api, "b")
    assert (
        api.post(
            "/_lab/faults",
            json={"type": "cut", "first": "a", "second": "b", "net": "public"},
        ).status_code
        == 200
    )
    assert '"pub1" . "pub2"' in nft_set(api, "cuts")
    # faults follow the server, not its name: a rename keeps the cut
    sid = api.get("/v1/servers", params={"name": "a"}).json()["servers"][0]["id"]
    api.put(f"/v1/servers/{sid}", json={"name": "renamed"})
    api.post("/_lab/faults", json={"type": "isolate", "vm": "b"})
    assert '"pub1" . "pub2"' in nft_set(api, "cuts")


def test_netem_takes_only_text(api):
    create_server(api, "a")
    assert api.post("/_lab/netem", json={"vm": "a", "args": 5}).status_code == 422
    assert (
        api.post("/_lab/netem", json={"vm": "a", "args": "delay  50ms"}).status_code
        == 200
    )
    assert set(api.lab.state.data["netem"].values()) == {"delay 50ms"}
    # the stored text goes into a tc command line as it is: applying it must work
    api.lab.fabric.faults.apply_latency()


def test_latency_between_locations_is_split_over_both_sides(api):
    create_server(api, "a", location="fsn1")
    api.sh.calls.clear()
    create_server(api, "b", location="hel1")
    netem = [c for c in api.sh.calls if c[0] == "tc" and "netem" in c]
    # half of the 24 ms round trip, on each side
    assert any("12.0ms" in c for c in netem)


def test_latency_filters_ipv6(api):
    create_server(api, "a", location="fsn1")
    api.sh.calls.clear()
    create_server(api, "b", location="hel1")
    assert any(
        c[0] == "tc" and "ip6" in c and "2001:db8:1::/64" in c for c in api.sh.calls
    )


def test_antispoofing_sets_follow_the_addresses(api):
    server = create_server(api, "a")["server"]
    floating_ip = api.post(
        "/v1/floating_ips", json={"type": "ipv4", "home_location": "fsn1"}
    ).json()["floating_ip"]
    wait_action(
        api,
        api.post(
            f"/v1/floating_ips/{floating_ip['id']}/actions/assign",
            json={"server": server["id"]},
        ).json()["action"],
    )
    src4 = nft_set(api, "src4")
    assert '"pub1" . 198.51.100.11' in src4 and f'"pub1" . {floating_ip["ip"]}' in src4
    assert '"pub1" . 2001:db8:1::/64' in nft_set(api, "src6")
