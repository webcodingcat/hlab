"""Cloud networks.

Subnets, routes, alias IPs, attach and detach, overlapping networks.
"""

import json

import pytest
from conftest import create_network, create_server, dhcp, nft_set, state_of, wait_action

from hlabd.api import desired, networks, nics
from hlabd.state import NETWORKS


def test_servers_in_a_network_get_their_address_by_dhcp(api, tmp_path):
    network = api.post(
        "/v1/networks",
        json={
            "name": "net",
            "ip_range": "10.0.0.0/16",
            "subnets": [
                {
                    "type": "cloud",
                    "ip_range": "10.0.1.0/24",
                    "network_zone": "eu-central",
                }
            ],
        },
    ).json()["network"]
    assert network["subnets"][0]["gateway"] == "10.0.0.1"
    s1 = create_server(api, "a", networks=[network["id"]])["server"]
    assert s1["private_net"][0]["ip"] == "10.0.1.1"
    s2 = create_server(api, "b")["server"]
    action = api.post(
        f"/v1/servers/{s2['id']}/actions/attach_to_network",
        json={"network": network["id"], "ip": "10.0.1.20"},
    )
    wait_action(api, action.json()["action"])
    s2 = api.get(f"/v1/servers/{s2['id']}").json()["server"]
    assert s2["private_net"][0]["ip"] == "10.0.1.20"
    assert api.sh.commands("add-nic")
    hosts = dhcp(tmp_path)
    assert "10.0.1.1" in hosts and "10.0.1.20" in hosts
    conf = dhcp(tmp_path)
    assert "121,10.0.0.1/32,0.0.0.0,10.0.0.0/16,10.0.0.1" in conf
    assert sorted(
        api.get(f"/v1/networks/{network['id']}").json()["network"]["servers"]
    ) == sorted([s1["id"], s2["id"]])


def test_overlapping_networks_stay_apart(api, tmp_path):
    """Keep two networks with the same range apart (allowed on Hetzner).

    The same private IP exists twice, each in its own table.
    """
    body = {
        "ip_range": "10.0.0.0/16",
        "subnets": [
            {"type": "cloud", "ip_range": "10.0.1.0/24", "network_zone": "eu-central"}
        ],
    }
    n1 = api.post("/v1/networks", json={"name": "one", **body}).json()["network"]
    n2 = api.post("/v1/networks", json={"name": "two", **body}).json()["network"]
    s1 = create_server(api, "a", networks=[n1["id"]])["server"]
    s2 = create_server(api, "b", networks=[n2["id"]])["server"]
    assert s1["private_net"][0]["ip"] == s2["private_net"][0]["ip"] == "10.0.1.1"
    ups = api.sh.commands("set-up-private-tap")
    assert (
        "set-up-private-tap",
        "prv1x1",
        "10.0.0.1/16",
        "10.0.1.1",
        str(n1["id"]),
        "10.0.0.0/16",
    ) in ups
    assert (
        "set-up-private-tap",
        "prv2x1",
        "10.0.0.1/16",
        "10.0.1.1",
        str(n2["id"]),
        "10.0.0.0/16",
    ) in ups
    # one dnsmasq per tap, so the address can be handed out on both
    for tap in ("prv1x1", "prv2x1"):
        conf = (tmp_path / "dhcp" / f"{tap}.conf").read_text()
        assert (
            f"interface={tap}\n" in conf
            and conf.count("dhcp-host=") == 1
            and ",10.0.1.1\n" in conf
        )
    # a server's private address counts for latency only on taps of the same network
    state = api.lab.state
    first = state_of(api, "servers")[s1["id"]]
    prefixes = desired.source_prefixes
    assert api.on_loop(prefixes, state, first, n2["id"]) == []
    assert api.on_loop(prefixes, state, first, n1["id"]) == ["10.0.1.1/32"]


def test_deleting_a_network_drops_its_table(api):
    network = api.post(
        "/v1/networks", json={"name": "gone", "ip_range": "10.5.0.0/16"}
    ).json()["network"]
    assert api.delete(f"/v1/networks/{network['id']}").status_code == 204
    assert ("flush-routes", str(network["id"])) in api.sh.calls


def test_network_values_are_checked_like_hetzner(api):
    def net(**body):
        return api.post(
            "/v1/networks", json={"name": "n", "ip_range": "10.0.0.0/16", **body}
        )

    assert net(ip_range="198.51.100.0/24").status_code == 422  # not RFC 1918
    assert net(ip_range="10.0.0.0/25").status_code == 422  # smaller than /24
    assert (
        net(
            subnets=[
                {
                    "type": "cloud",
                    "ip_range": "10.0.1.0/24",
                    "network_zone": "eu-central",
                },
                {
                    "type": "cloud",
                    "ip_range": "10.0.1.0/25",
                    "network_zone": "eu-central",
                },
            ]
        ).status_code
        == 422
    )
    # nothing created by the failures
    assert not api.get("/v1/networks").json()["networks"]
    network = net(
        subnets=[
            {"type": "cloud", "ip_range": "10.0.1.0/24", "network_zone": "eu-central"}
        ]
    ).json()["network"]

    def act(cmd, **body):
        return api.post(f"/v1/networks/{network['id']}/actions/{cmd}", json=body)

    # a destination that would add a line to dnsmasq's configuration, other bad routes
    assert (
        act(
            "add_route",
            destination="10.9.0.0/16\ndhcp-script=/x",
            gateway="10.0.1.5",
        ).status_code
        == 422
    )
    assert (
        act("add_route", destination="8.8.8.0/24", gateway="10.0.1.5").status_code
        == 422
    )
    # overlaps a subnet
    assert (
        act("add_route", destination="10.0.1.0/25", gateway="10.0.1.5").status_code
        == 422
    )
    # the network's own gateway
    assert (
        act("add_route", destination="10.9.0.0/16", gateway="10.0.0.1").status_code
        == 422
    )
    assert (
        act("add_route", destination="10.9.0.0/16", gateway="10.0.1.5").status_code
        == 201
    )
    # shrinking
    assert act("change_ip_range", ip_range="10.0.0.0/17").status_code == 422
    # another base address
    assert act("change_ip_range", ip_range="10.1.0.0/15").status_code == 422
    assert act("change_ip_range", ip_range="10.0.0.0/15").status_code == 201
    assert ("flush-routes", str(network["id"])) in api.sh.calls  # the table is set anew


def test_deleting_a_network_detaches_its_servers(api):
    network = api.post(
        "/v1/networks",
        json={
            "name": "n",
            "ip_range": "10.0.0.0/16",
            "subnets": [
                {
                    "type": "cloud",
                    "ip_range": "10.0.1.0/24",
                    "network_zone": "eu-central",
                }
            ],
        },
    ).json()["network"]
    server = create_server(api, "a", networks=[network["id"]])["server"]
    assert (
        api.post(
            f"/v1/networks/{network['id']}/actions/delete_subnet",
            json={"ip_range": "10.0.1.0/24"},
        ).status_code
        == 422
    )
    assert api.delete(f"/v1/networks/{network['id']}").status_code == 204
    assert api.get(f"/v1/servers/{server['id']}").json()["server"]["private_net"] == []
    assert ("tear-down-tap", "prv1x1") in api.sh.calls


def test_overlapping_networks_on_one_server_are_refused_and_members_listed(api):
    body = {
        "ip_range": "10.0.0.0/16",
        "subnets": [
            {"type": "cloud", "ip_range": "10.0.1.0/24", "network_zone": "eu-central"}
        ],
    }
    n1 = api.post("/v1/networks", json={"name": "one", **body}).json()["network"]
    n2 = api.post("/v1/networks", json={"name": "two", **body}).json()["network"]
    server = create_server(api, "a", networks=[n1["id"]])["server"]
    response = api.post(
        f"/v1/servers/{server['id']}/actions/attach_to_network",
        json={"network": n2["id"]},
    )
    assert response.json()["error"]["code"] == "networks_overlap"
    members = api.get(f"/v1/networks/{n1['id']}/members").json()["members"]
    assert members == [
        {
            "type": "server",
            "id": server["id"],
            "ip": "10.0.1.1",
            "status": "ok",
            "alias_ips": [],
            "subnet": "10.0.1.0/24",
        }
    ]


def test_alias_ips_are_checked_and_reserved(api):
    network = api.post(
        "/v1/networks",
        json={
            "name": "n",
            "ip_range": "10.0.0.0/16",
            "subnets": [
                {
                    "type": "cloud",
                    "ip_range": "10.0.1.0/24",
                    "network_zone": "eu-central",
                }
            ],
        },
    ).json()["network"]
    server_a = create_server(api, "a", networks=[network["id"]])["server"]
    response = api.post(
        f"/v1/servers/{server_a['id']}/actions/change_alias_ips",
        json={"network": network["id"], "alias_ips": ["nope"]},
    )
    assert response.status_code == 422
    wait_action(
        api,
        api.post(
            f"/v1/servers/{server_a['id']}/actions/change_alias_ips",
            json={"network": network["id"], "alias_ips": ["10.0.1.2"]},
        ).json()["action"],
    )
    server_b = create_server(api, "b", networks=[network["id"]])["server"]
    assert server_b["private_net"][0]["ip"] == "10.0.1.3"  # 10.0.1.2 is a's alias
    tap_ups = [c for c in api.sh.calls if c[:2] == ("set-up-private-tap", "prv1x1")]
    assert tap_ups[-1][-1] == "10.0.1.2"  # routed to a


def test_failed_attach_takes_its_nic_back(api):
    network = api.post(
        "/v1/networks",
        json={
            "name": "n",
            "ip_range": "10.0.0.0/16",
            "subnets": [
                {
                    "type": "cloud",
                    "ip_range": "10.0.1.0/24",
                    "network_zone": "eu-central",
                }
            ],
        },
    ).json()["network"]
    server = create_server(api, "a")["server"]
    api.sh.fail.add("add-nic")
    action = api.post(
        f"/v1/servers/{server['id']}/actions/attach_to_network",
        json={"network": network["id"]},
    ).json()["action"]
    assert wait_action(api, action)["status"] == "error"
    assert api.get(f"/v1/servers/{server['id']}").json()["server"]["private_net"] == []


def test_a_server_joins_at_most_three_networks(api):
    server = create_server(api, "a")["server"]
    for i in range(4):
        network = api.post(
            "/v1/networks",
            json={
                "name": f"n{i}",
                "ip_range": f"10.{i}.0.0/16",
                "subnets": [
                    {
                        "type": "cloud",
                        "ip_range": f"10.{i}.1.0/24",
                        "network_zone": "eu-central",
                    }
                ],
            },
        ).json()["network"]
        response = api.post(
            f"/v1/servers/{server['id']}/actions/attach_to_network",
            json={"network": network["id"]},
        )
        if i < 3:
            wait_action(api, response.json()["action"])
        else:
            assert (
                response.status_code == 403
                and response.json()["error"]["code"] == "resource_limit_exceeded"
            )


def test_attach_takes_an_address_from_the_chosen_subnet(api):
    network = create_network(api, "10.0.1.0/24", "10.0.2.0/24")
    server = create_server(api, "a")["server"]
    response = api.post(
        f"/v1/servers/{server['id']}/actions/attach_to_network",
        json={"network": network["id"], "ip_range": "10.0.1.0/24", "ip": "10.0.2.5"},
    )
    assert response.status_code == 422  # the IP is not in ip_range
    response = api.post(
        f"/v1/servers/{server['id']}/actions/attach_to_network",
        json={"network": network["id"], "ip_range": "10.0.2.0/24"},
    )
    wait_action(api, response.json()["action"])
    assert (
        api.get(f"/v1/servers/{server['id']}")
        .json()["server"]["private_net"][0]["ip"]
        .startswith("10.0.2.")
    )
    assert (
        api.post(
            f"/v1/servers/{server['id']}/actions/attach_to_network",
            json={"network": network["id"], "ip": "not-an-ip"},
        ).status_code
        == 422
    )


def test_a_new_network_does_not_take_an_address_in_use(api):
    network = create_network(api, "10.0.1.0/24")
    create_server(api, "a", networks=[network["id"]])
    # its first address is a's
    response = api.post("/v1/networks", json={"name": "m", "ip_range": "10.0.1.0/24"})
    assert response.status_code == 422
    api.sh.run = (
        lambda run: (
            lambda *a, **k: "10.0.0.1\n" if a[0] == "list-local-addrs" else run(*a, **k)
        )
    )(api.sh.run)
    api.lab.fabric.network._local_addresses_cache = None
    # n's gateway: shared, fine
    response = api.post("/v1/networks", json={"name": "o", "ip_range": "10.0.0.0/16"})
    assert response.status_code == 201, response.text


def test_detached_nic_takes_its_netem_along(api):
    n1, n2 = (
        create_network(api, "10.0.1.0/24"),
        create_network(api, "10.0.1.0/24", name="m"),
    )
    server = create_server(api, "a", networks=[n1["id"]])["server"]
    api.post("/_lab/netem", json={"vm": "a", "net": "private", "args": "delay 100ms"})
    assert "prv1x1" in state_of(api, "netem")
    response = api.post(
        f"/v1/servers/{server['id']}/actions/detach_from_network",
        json={"network": n1["id"]},
    )
    wait_action(api, response.json()["action"])
    assert "prv1x1" not in state_of(api, "netem")
    response = api.post(
        f"/v1/servers/{server['id']}/actions/attach_to_network",
        json={"network": n2["id"]},
    )
    wait_action(api, response.json()["action"])


def test_alias_ip_must_not_be_a_network_gateway(api):
    network = api.post(
        "/v1/networks",
        json={
            "name": "n",
            "ip_range": "10.0.0.0/16",
            "subnets": [
                {
                    "type": "cloud",
                    "ip_range": "10.0.4.0/24",
                    "network_zone": "eu-central",
                }
            ],
        },
    ).json()["network"]
    # its gateway: 10.0.4.1
    response = api.post("/v1/networks", json={"name": "m", "ip_range": "10.0.4.0/24"})
    assert response.status_code == 201, response.text
    server = create_server(api, "a", networks=[network["id"]])["server"]
    response = api.post(
        f"/v1/servers/{server['id']}/actions/change_alias_ips",
        json={"network": network["id"], "alias_ips": ["10.0.4.1"]},
    )
    assert response.status_code == 422


def test_network_routes_go_to_the_gateway_server(api, tmp_path):
    network = api.post(
        "/v1/networks",
        json={
            "name": "n",
            "ip_range": "10.0.0.0/16",
            "subnets": [
                {
                    "type": "cloud",
                    "ip_range": "10.0.1.0/24",
                    "network_zone": "eu-central",
                }
            ],
        },
    ).json()["network"]
    create_server(api, "gw", networks=[network["id"]])
    wait_action(
        api,
        api.post(
            f"/v1/networks/{network['id']}/actions/add_route",
            json={"destination": "10.100.0.0/24", "gateway": "10.0.1.1"},
        ).json()["action"],
    )
    assert (
        "set-route-via",
        "10.100.0.0/24",
        "10.0.1.1",
        "prv1x1",
        str(network["id"]),
    ) in api.sh.calls
    assert "10.100.0.0/24,10.0.0.1" in dhcp(tmp_path)
    api.post(
        f"/v1/networks/{network['id']}/actions/delete_route",
        json={"destination": "10.100.0.0/24", "gateway": "10.0.1.1"},
    )
    assert ("delete-route", "10.100.0.0/24", str(network["id"])) in api.sh.calls


def test_private_networks_are_isolated_from_each_other(api):
    n1 = api.post(
        "/v1/networks",
        json={
            "name": "n1",
            "ip_range": "10.0.0.0/16",
            "subnets": [{"type": "cloud", "network_zone": "eu-central"}],
        },
    ).json()["network"]
    n2 = api.post(
        "/v1/networks",
        json={
            "name": "n2",
            "ip_range": "10.1.0.0/16",
            "subnets": [{"type": "cloud", "network_zone": "eu-central"}],
        },
    ).json()["network"]
    create_server(api, "a", networks=[n1["id"]])
    create_server(api, "b", networks=[n1["id"]])
    api.sh.calls.clear()
    create_server(api, "c", networks=[n2["id"]])
    pairs = nft_set(api, "private_pairs")
    assert '"prv1x1" . "prv2x1"' in pairs
    assert "prv3x1" not in pairs


def test_a_failed_detach_leaves_the_network_with_the_nics_not_yet_detached(
    api, monkeypatch
):
    network = create_network(api, "10.0.1.0/24")
    nid = network["id"]
    first = create_server(api, "a", networks=[nid])["server"]
    second = create_server(api, "b", networks=[nid])["server"]
    run = api.sh.run

    def tap_down_fails_for_b(*args, **kwargs):
        if args == ("tear-down-tap", "prv2x1"):
            raise RuntimeError("tap-down failed")
        return run(*args, **kwargs)

    monkeypatch.setattr(api.sh, "run", tap_down_fails_for_b)
    with pytest.raises(RuntimeError):
        api.on_loop(networks.delete_network, api.lab, nid)
    # the first NIC is gone, also from the saved state; the second stays with its
    # network, and every flag is off again
    servers = state_of(api, "servers")
    assert servers[first["id"]]["_lab"]["nics"] == []
    assert [nic["network"] for nic in servers[second["id"]]["_lab"]["nics"]] == [nid]
    api.lab.state.flush()
    saved = json.loads(api.lab.state.path.read_text())
    assert saved["servers"][str(first["id"])]["_lab"]["nics"] == []
    assert api.get(f"/v1/networks/{nid}").json()["network"]["servers"] == [second["id"]]
    assert not servers[first["id"]].get("_locked") and not servers[second["id"]].get(
        "_locked"
    )
    assert "_deleting" not in state_of(api, "networks")[nid]
    # deleting it again goes on from there
    monkeypatch.setattr(api.sh, "run", run)
    assert api.delete(f"/v1/networks/{nid}").status_code == 204
    assert servers[second["id"]]["_lab"]["nics"] == []


def test_a_network_with_a_busy_member_is_not_deleted_and_flags_come_off(api):
    network = create_network(api, "10.0.1.0/24")
    server = create_server(api, "a", networks=[network["id"]])["server"]
    record = state_of(api, "servers")[server["id"]]
    api.on_loop(api.lab.claim, record, "_locked", "locked")  # an action runs on it
    response = api.delete(f"/v1/networks/{network['id']}")
    assert response.status_code == 423
    assert "_deleting" not in state_of(api, "networks")[network["id"]]
    assert record["_locked"] is True  # still the other action's


def test_a_failed_detach_keeps_the_network_and_frees_its_servers(api, monkeypatch):
    network = create_network(api, "10.0.1.0/24")
    server = create_server(api, "a", networks=[network["id"]])["server"]

    def failing_detach(*_):
        raise RuntimeError("tear-down-tap failed")

    monkeypatch.setattr(api.lab.fabric.network, "detach_nic", failing_detach)
    with pytest.raises(RuntimeError):
        api.on_loop(networks.delete_network, api.lab, network["id"])
    assert "_deleting" not in state_of(api, "networks")[network["id"]]
    assert "_locked" not in state_of(api, "servers")[server["id"]]


def test_a_route_is_wanted_with_the_nics_that_hold_its_gateway(api):
    network = create_network(api, "10.0.1.0/24")
    gateway = create_server(api, "gw", networks=[network["id"]])["server"]
    create_server(api, "other", networks=[network["id"]])
    route = {"destination": "10.100.0.0/24", "gateway": "10.0.1.1"}
    action = api.post(
        f"/v1/networks/{network['id']}/actions/add_route", json=route
    ).json()["action"]
    wait_action(api, action)
    (wanted,) = api.on_loop(desired.wanted_routes, api.lab.state)
    assert (wanted.destination, wanted.gateway, wanted.network) == (
        "10.100.0.0/24",
        "10.0.1.1",
        network["id"],
    )
    assert wanted.holders == [(gateway["id"], "prv1x1")]
    # the routes put in are kept, to be removed before the next ones go in
    assert state_of(api, "net_routes") == [["10.100.0.0/24", str(network["id"])]]


def test_a_hot_plugged_nic_takes_the_lowest_free_port():
    server = {"_lab": {"nics": [{"port": "hp1"}, {"port": None}, {"port": "hp3"}]}}
    assert desired.free_hot_plug_port(server) == "hp2"
    server["_lab"]["nics"] = [{"port": port} for port in desired.HOT_PLUG_PORTS]
    assert desired.free_hot_plug_port(server) is None


def test_no_nic_gets_an_address_of_the_container(api):
    # the container's addresses are read in a thread and handed down explicitly
    api.sh.run = (
        lambda run: (
            lambda *a, **k: "10.0.1.1\n" if a[0] == "list-local-addrs" else run(*a, **k)
        )
    )(api.sh.run)
    network = create_network(api, "10.0.1.0/24")
    server = create_server(api, "a", networks=[network["id"]])["server"]
    assert server["private_net"][0]["ip"] == "10.0.1.2"  # 10.0.1.1 is the container's
    response = api.post(
        f"/v1/servers/{server['id']}/actions/change_alias_ips",
        json={"network": network["id"], "alias_ips": ["10.0.1.1"]},
    )
    assert response.status_code == 422
    assert response.json()["error"]["code"] == "ip_not_available"


def test_a_free_address_skips_the_local_addresses_it_is_given(api):
    network = create_network(api, "10.0.1.0/24")

    def pick(local_addresses):
        record = api.lab.state[NETWORKS][network["id"]]
        return nics.network_ip(api.lab, record, None, local_addresses)

    assert api.on_loop(pick, set()) == "10.0.1.1"
    assert api.on_loop(pick, {"10.0.1.1", "10.0.1.2"}) == "10.0.1.3"


def test_failed_alias_change_keeps_the_old_alias_ips(api):
    network = create_network(api, "10.0.1.0/24")
    server = create_server(api, "a", networks=[network["id"]])["server"]
    api.sh.fail.add("set-up-private-tap")
    action = api.post(
        f"/v1/servers/{server['id']}/actions/change_alias_ips",
        json={"network": network["id"], "alias_ips": ["10.0.1.9"]},
    ).json()["action"]
    assert wait_action(api, action)["status"] == "error"
    got = api.get(f"/v1/servers/{server['id']}").json()["server"]
    assert got["private_net"][0]["alias_ips"] == []
