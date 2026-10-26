"""Cloud firewalls: rules, limits, label selectors, applying and removing."""

import pytest
from conftest import create_server, state_of, wait_action

from hlabd.api import desired
from hlabd.api.firewalls import check_port, checked_rules
from hlabd.errors import ApiError

ANY = ["0.0.0.0/0", "::/0"]


def ruleset(api) -> str:
    """Return the firewall ruleset the lab last wrote."""
    return (api.lab.fabric.root / "firewall.nft").read_text()


@pytest.mark.parametrize(
    "rule, message",
    [
        ({"direction": "in", "protocol": "tcp", "source_ips": ANY}, "port is required"),
        (
            {"direction": "in", "protocol": "icmp", "port": "80", "source_ips": ANY},
            "only allowed for tcp and udp",
        ),
        (
            {"direction": "in", "protocol": "tcp", "port": "80", "source_ips": []},
            "must not be empty",
        ),
        (
            {
                "direction": "in",
                "protocol": "tcp",
                "port": "80",
                "source_ips": ["10.0.0.1/8"],
            },
            "not a valid network",
        ),
        (
            {"direction": "up", "protocol": "tcp", "port": "80", "source_ips": ANY},
            "direction",
        ),
        ({"direction": "in", "protocol": "sctp", "source_ips": ANY}, "protocol"),
        (
            {"direction": "in", "protocol": "udp", "port": "90-80", "source_ips": ANY},
            "out of range",
        ),
        (
            {"direction": "out", "protocol": "tcp", "port": "443", "source_ips": ANY},
            "destination_ips",
        ),
    ],
)
def test_invalid_rules_are_refused_with_a_message(api, rule, message):
    response = api.post("/v1/firewalls", json={"name": "fw", "rules": [rule]})
    assert response.status_code == 422
    assert message in response.json()["error"]["message"]


def test_ports_are_numbers_ranges_or_any():
    for port in ("80", "80-85", "any"):
        check_port(port)
    with pytest.raises(ApiError):
        check_port("x")
    assert (
        checked_rules([{"direction": "in", "protocol": "gre", "source_ips": ANY}])[0][
            "port"
        ]
        is None
    )


def test_server_without_firewall_is_open(api):
    create_server(api, "open")
    assert "jump in_" not in ruleset(api)


def test_inbound_and_outbound_rules_become_nft_chains(api):
    server = create_server(api, "web", labels={"role": "web"})["server"]
    rules = [
        {"direction": "in", "protocol": "tcp", "port": "22", "source_ips": ANY},
        {"direction": "in", "protocol": "icmp", "source_ips": ["192.0.2.0/24"]},
        {
            "direction": "in",
            "protocol": "udp",
            "port": "9987-9990",
            "source_ips": ["2001:db8:ffff::/48"],
        },
        {"direction": "out", "protocol": "tcp", "port": "443", "destination_ips": ANY},
        {"direction": "in", "protocol": "gre", "source_ips": ANY},
    ]
    out = api.post(
        "/v1/firewalls",
        json={
            "name": "fw",
            "rules": rules,
            "apply_to": [
                {"type": "label_selector", "label_selector": {"selector": "role=web"}}
            ],
        },
    )
    assert out.status_code == 201, out.text
    rs = ruleset(api)
    sid = server["id"]
    assert f'oifname "pub1" jump in_{sid}' in rs
    assert f'iifname "pub1" jump out_{sid}' in rs
    assert "meta nfproto ipv4 tcp dport 22 accept" in rs
    assert "meta nfproto ipv6 tcp dport 22 accept" in rs
    assert "ip saddr { 192.0.2.0/24 } meta l4proto icmp accept" in rs
    assert "ip6 saddr { 2001:db8:ffff::/48 } udp dport 9987-9990 accept" in rs
    assert "tcp dport 443 return" in rs
    assert "meta nfproto ipv4 meta l4proto gre accept" in rs
    assert "meta nfproto ipv6 meta l4proto gre" not in rs  # GRE is IPv4 only
    assert "ip saddr 198.18.0.2 accept" in rs  # hlab ssh passes
    assert (
        api.get(f"/v1/servers/{sid}").json()["server"]["public_net"]["firewalls"][0][
            "status"
        ]
        == "applied"
    )

    # the label is removed, the firewall no longer applies
    api.put(f"/v1/servers/{sid}", json={"labels": {}})
    assert f"jump in_{sid}" not in ruleset(api)


def test_firewall_without_rules_blocks_inbound_only(api):
    server = create_server(api, "closed")["server"]
    api.post(
        "/v1/firewalls",
        json={
            "name": "deny",
            "apply_to": [{"type": "server", "server": {"id": server["id"]}}],
        },
    )
    rs = ruleset(api)
    chain = rs.split(f"chain in_{server['id']} {{")[1].split("}")[0]
    assert chain.split()[-1] == "drop"
    assert "jump out_" not in rs


def test_rules_of_several_firewalls_add_up(api):
    server = create_server(api, "multi")["server"]
    target = [{"type": "server", "server": {"id": server["id"]}}]
    api.post(
        "/v1/firewalls",
        json={
            "name": "a",
            "apply_to": target,
            "rules": [
                {"direction": "in", "protocol": "tcp", "port": "80", "source_ips": ANY}
            ],
        },
    )
    api.post(
        "/v1/firewalls",
        json={
            "name": "b",
            "apply_to": target,
            "rules": [
                {"direction": "in", "protocol": "tcp", "port": "443", "source_ips": ANY}
            ],
        },
    )
    rs = ruleset(api)
    assert "tcp dport 80 accept" in rs and "tcp dport 443 accept" in rs
    assert rs.count(f"chain in_{server['id']} ") == 1


def test_firewall_limits_and_errors_are_hetzners(api):
    server = create_server(api, "lim")["server"]
    target = [{"type": "server", "server": {"id": server["id"]}}]
    for i in range(5):
        assert (
            api.post(
                "/v1/firewalls", json={"name": f"f{i}", "apply_to": target}
            ).status_code
            == 201
        )
    response = api.post("/v1/firewalls", json={"name": "f5", "apply_to": target})
    assert (
        response.status_code == 403
        and response.json()["error"]["code"] == "resource_limit_exceeded"
    )
    assert api.post("/v1/firewalls", json={"name": "f0"}).status_code == 409
    fid = api.post("/v1/firewalls", json={"name": "free"}).json()["firewall"]["id"]
    response = api.post(
        f"/v1/firewalls/{fid}/actions/apply_to_resources",
        json={"apply_to": [{"type": "server", "server": {"id": 999}}]},
    )
    assert response.json()["error"]["code"] == "firewall_resource_not_found"
    first = api.get("/v1/firewalls", params={"name": "f0"}).json()["firewalls"][0]["id"]
    response = api.post(
        f"/v1/firewalls/{first}/actions/apply_to_resources", json={"apply_to": target}
    )
    assert response.json()["error"]["code"] == "firewall_already_applied"


def test_server_created_with_a_firewall_gets_and_releases_it(api):
    fid = api.post("/v1/firewalls", json={"name": "fw"}).json()["firewall"]["id"]
    server = create_server(api, "fwd", firewalls=[{"firewall": fid}])["server"]
    assert f"jump in_{server['id']}" in ruleset(api)
    wait_action(api, api.delete(f"/v1/servers/{server['id']}").json()["action"])
    assert "jump in_" not in ruleset(api)
    assert api.get(f"/v1/firewalls/{fid}").json()["firewall"]["applied_to"] == []


def test_firewall_applies_to_servers_by_label_selector(api):
    server = create_server(api, "w", labels={"cluster": "x"})["server"]
    out = api.post(
        "/v1/firewalls",
        json={
            "name": "fw",
            "rules": [],
            "apply_to": [
                {"type": "label_selector", "label_selector": {"selector": "cluster=x"}}
            ],
        },
    ).json()
    applied = out["firewall"]["applied_to"][0]["applied_to_resources"]
    assert applied == [{"type": "server", "server": {"id": server["id"]}}]
    assert (
        api.get(f"/v1/servers/{server['id']}").json()["server"]["public_net"][
            "firewalls"
        ][0]["id"]
        == out["firewall"]["id"]
    )


def test_firewall_removal_accepts_null_fields(api):
    sel = {"type": "label_selector", "label_selector": {"selector": "cluster=x"}}
    fid = api.post("/v1/firewalls", json={"name": "fw", "apply_to": [sel]}).json()[
        "firewall"
    ]["id"]
    assert api.delete(f"/v1/firewalls/{fid}").status_code == 422
    api.post(
        f"/v1/firewalls/{fid}/actions/remove_from_resources",
        json={"remove_from": [{**sel, "server": None}]},
    )
    assert api.delete(f"/v1/firewalls/{fid}").status_code == 204


def test_firewall_has_at_most_50_rules(api):
    rule = {
        "direction": "in",
        "protocol": "tcp",
        "port": "22",
        "source_ips": ["0.0.0.0/0"],
    }
    assert (
        api.post(
            "/v1/firewalls", json={"name": "big", "rules": [rule] * 51}
        ).status_code
        == 422
    )
    assert (
        api.post("/v1/firewalls", json={"name": "ok", "rules": [rule] * 50}).status_code
        == 201
    )


def test_firewall_needs_a_public_interface(api):
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
    server = create_server(
        api,
        "a",
        networks=[network["id"]],
        public_net={"enable_ipv4": False, "enable_ipv6": False},
    )["server"]
    fw = api.post("/v1/firewalls", json={"name": "fw"}).json()["firewall"]
    response = api.post(
        f"/v1/firewalls/{fw['id']}/actions/apply_to_resources",
        json={"apply_to": [{"type": "server", "server": {"id": server["id"]}}]},
    )
    assert response.json()["error"]["code"] == "private_net_only_server"


def test_set_rules_sets_and_applies(api):
    server = create_server(api, "a")["server"]
    firewall = api.post(
        "/v1/firewalls",
        json={
            "name": "f",
            "apply_to": [{"type": "server", "server": {"id": server["id"]}}],
        },
    )
    fid = firewall.json()["firewall"]["id"]
    acts = api.post(
        f"/v1/firewalls/{fid}/actions/set_rules", json={"rules": []}
    ).json()["actions"]
    assert [a["command"] for a in acts] == ["set_firewall_rules", "apply_firewall"]


def test_remove_firewall_names_its_servers(api):
    server = create_server(api, "a")["server"]
    fid = api.post(
        "/v1/firewalls",
        json={
            "name": "f",
            "apply_to": [{"type": "server", "server": {"id": server["id"]}}],
        },
    ).json()["firewall"]["id"]
    acts = api.post(
        f"/v1/firewalls/{fid}/actions/remove_from_resources",
        json={"remove_from": [{"type": "server", "server": {"id": server["id"]}}]},
    ).json()["actions"]
    assert {"id": server["id"], "type": "server"} in acts[0]["resources"]


def test_server_reached_through_a_label_selector_cannot_be_removed_by_hand(api):
    server = create_server(api, "a", labels={"web": "1"})["server"]
    fw = api.post(
        "/v1/firewalls",
        json={
            "name": "fw",
            "apply_to": [
                {"type": "label_selector", "label_selector": {"selector": "web=1"}}
            ],
        },
    ).json()["firewall"]
    response = api.post(
        f"/v1/firewalls/{fw['id']}/actions/remove_from_resources",
        json={"remove_from": [{"type": "server", "server": {"id": server["id"]}}]},
    )
    assert response.json()["error"]["code"] == "firewall_managed_by_label_selector"


def test_firewall_cannot_name_the_same_server_twice(api):
    server = create_server(api, "a")["server"]
    response = api.post(
        "/v1/firewalls",
        json={
            "name": "twice",
            "apply_to": [{"type": "server", "server": {"id": server["id"]}}] * 2,
        },
    )
    assert response.json()["error"]["code"] == "server_already_added"


def test_the_firewall_rules_are_gathered_per_server_by_id(api):
    first = create_server(api, "a")["server"]
    create_server(api, "b")
    third = create_server(api, "c")["server"]
    rule = {"direction": "in", "protocol": "tcp", "port": "22", "source_ips": ANY}
    for server, rules in ((third, [rule]), (first, [])):
        response = api.post(
            "/v1/firewalls",
            json={
                "name": f"fw{server['id']}",
                "rules": rules,
                "apply_to": [{"type": "server", "server": {"id": server["id"]}}],
            },
        )
        assert response.status_code == 201, response.text
    servers = api.on_loop(desired.firewall_rules, api.lab.state)
    # a server whose firewall has no rules is listed: everything inbound is dropped
    assert [(x.sid, x.tap, len(x.rules)) for x in servers] == [
        (first["id"], "pub1", 0),
        (third["id"], "pub3", 1),
    ]
    servers[1].rules[0]["port"] = "23"  # a copy: the firewall keeps its rule
    assert any(
        r["port"] == "22"
        for f in state_of(api, "firewalls").values()
        for r in f["rules"]
    )
