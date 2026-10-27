"""Faults and the packet path: API faults, cut links, netem, latency, anti-spoofing."""

import asyncio
import threading

from conftest import create_server, nft_set, state_of, wait_action

from hlabd.api import desired, system


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
    assert set(state_of(api, "netem").values()) == {"delay 50ms"}
    # the stored text goes into a tc command line as it is: applying it must work
    api.on_loop(system.apply_latency, api.lab)


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


def test_a_tap_with_netem_gets_no_delays_the_others_by_location(api):
    first = create_server(api, "a", location="fsn1")["server"]
    second = create_server(api, "b", location="hel1")["server"]
    response = api.post("/_lab/netem", json={"vm": "a", "args": "loss 5%"})
    assert response.status_code == 200, response.text
    latency = api.on_loop(desired.latency, api.lab.state, api.lab.cfg)
    shapings = dict(latency.servers)
    (first_tap,) = shapings[first["id"]]
    (second_tap,) = shapings[second["id"]]
    assert (first_tap.netem, first_tap.delays) == ("loss 5%", {})
    # the 24 ms round trip fsn1-hel1, from a's public addresses
    public = first["public_net"]
    assert (second_tap.netem, second_tap.delays) == (
        None,
        {24: [f"{public['ipv4']['ip']}/32", public["ipv6"]["ip"]]},
    )


def test_pictures_are_put_in_place_in_the_order_they_were_computed(lab, monkeypatch):
    applied, first_runs, release_first = [], threading.Event(), threading.Event()

    def slow_first_apply(sets, tap_networks):
        if not applied and not first_runs.is_set():
            first_runs.set()
            release_first.wait(5)  # the older picture takes long to put in place
        applied.append(sets["faults"])

    # a picture: how many faults the state had when it was computed
    monkeypatch.setattr(
        desired, "fault_sets", lambda state: {"faults": len(state.data["faults"])}
    )
    monkeypatch.setattr(lab.fabric.faults, "apply", slow_first_apply)

    async def two_pictures():
        older = asyncio.create_task(system.apply_faults(lab))
        while not first_runs.is_set():
            await asyncio.sleep(0.01)
        lab.state.data["faults"].append(
            {"type": "cut", "first": "a", "second": "b", "net": "all"}
        )
        newer = asyncio.create_task(system.apply_faults(lab))
        await asyncio.sleep(0.05)  # the newer picture waits for the older one
        release_first.set()
        await asyncio.gather(older, newer)

    asyncio.run(two_pictures())
    # the newest picture is in place last: an older one never overwrites it
    assert applied == [0, 1]


def test_faults_show_their_targets_as_text_and_store_typed_references(api):
    sid = create_server(api, "a")["server"]["id"]
    assert api.post("/_lab/clients", json={"name": "c1"}).status_code == 200
    answer = api.post(
        "/_lab/faults", json={"type": "cut", "first": "c1", "second": "a"}
    )
    shown = {
        "type": "cut",
        "first": "a",
        "second": "c1",
        "net": "all",
        "first_ref": f"s:{sid}",
        "second_ref": "c:c1",
    }
    assert answer.json()["faults"] == [shown]
    assert api.get("/_lab/status").json()["faults"] == [shown]
    assert state_of(api, "faults")[0]["first_ref"] == {"server": sid}
    remove = {"type": "cut", "first_ref": f"s:{sid}"}  # matched as the API shows it
    assert api.post("/_lab/faults/remove", json=remove).json()["faults"] == []
