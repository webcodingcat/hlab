"""Live tests of cloud firewalls: inbound, outbound, Floating IPs."""

from livelab import A4, A6, A_PRIV, ALICE4, ALICE6, B4, B_PRIV, C6, LiveLab, ping, probe

ANY = ["0.0.0.0/0", "::/0"]


def set_labels(lab: LiveLab, name: str, labels: dict) -> None:
    """Set a server's labels, next to the label lab=live every live server has."""
    lab.api(
        "PUT",
        f"/servers/{lab.server(name)['id']}",
        {"labels": {"lab": "live", **labels}},
    )


def test_inbound_firewall_rules_filter_public_traffic_only(lab: LiveLab):
    fw = lab.api(
        "POST",
        "/firewalls",
        {
            "name": "web",
            "rules": [
                {
                    "direction": "in",
                    "protocol": "tcp",
                    "port": "7000",
                    "source_ips": ANY,
                },
                {"direction": "in", "protocol": "tcp", "port": "22", "source_ips": ANY},
            ],
            "apply_to": [
                {"type": "label_selector", "label_selector": {"selector": "role=web"}}
            ],
        },
    )["firewall"]
    try:
        assert probe(lab, "client:alice", "tcp", A4, 7001)  # open before the label
        set_labels(lab, "a", {"role": "web"})
        assert probe(lab, "client:alice", "tcp", A4) == f"a tcp {ALICE4}"
        assert probe(lab, "client:alice", "tcp", A6) == f"a tcp {ALICE6}"
        assert probe(lab, "client:alice", "tcp", A4, 7001) == ""
        assert probe(lab, "client:alice", "tcp", A6, 7001) == ""
        assert probe(lab, "client:alice", "udp", A4) == ""
        assert ping(lab, "client:alice", A4, count=1) is None
        # other servers come over the public network too
        assert probe(lab, "b", "tcp", A4, 7001) == ""
        # cloud networks are not filtered
        assert probe(lab, "b", "tcp", A_PRIV, 7001) == f"a tcp {B_PRIV}"
        assert lab.run("a", "true").returncode == 0  # the lab's own access passes
        # outbound stays open without outbound rules; answers to a's own connections
        # come back
        assert probe(lab, "a", "tcp", B4, 7001) == f"b tcp {A4}"
        # ICMP rule added: ping works, from IPv6 too
        lab.wait_action(
            lab.api(
                "POST",
                f"/firewalls/{fw['id']}/actions/set_rules",
                {
                    "rules": [
                        {
                            "direction": "in",
                            "protocol": "tcp",
                            "port": "7000",
                            "source_ips": ANY,
                        },
                        {"direction": "in", "protocol": "icmp", "source_ips": ANY},
                    ]
                },
            )["actions"][0]
        )
        assert ping(lab, "client:alice", A4, count=1) is not None
        assert ping(lab, "client:alice", A6, count=1) is not None
        # source restriction: only the client's /32
        lab.api(
            "POST",
            f"/firewalls/{fw['id']}/actions/set_rules",
            {
                "rules": [
                    {
                        "direction": "in",
                        "protocol": "tcp",
                        "port": "7001",
                        "source_ips": [f"{B4}/32"],
                    }
                ]
            },
        )
        assert probe(lab, "b", "tcp", A4, 7001) == f"a tcp {B4}"
        assert probe(lab, "client:alice", "tcp", A4, 7001) == ""
        assert probe(lab, "b", "tcp", A6, 7001) == ""  # the IPv6 of b is not listed
        set_labels(lab, "a", {})
        # label gone, firewall gone
        assert probe(lab, "client:alice", "tcp", A4, 7001) == f"a tcp {ALICE4}"
        # a firewall still applied (here by its label selector) cannot be deleted
        assert (
            lab.http.delete(f"/firewalls/{fw['id']}").json()["error"]["code"]
            == "resource_in_use"
        )
    finally:
        set_labels(lab, "a", {})
        lab.api(
            "POST",
            f"/firewalls/{fw['id']}/actions/remove_from_resources",
            {
                "remove_from": [
                    {
                        "type": "label_selector",
                        "label_selector": {"selector": "role=web"},
                    }
                ]
            },
        )
        lab.api("DELETE", f"/firewalls/{fw['id']}")


def test_outbound_firewall_rules_leave_metadata_and_cloud_networks_open(lab: LiveLab):
    fw = lab.api(
        "POST",
        "/firewalls",
        {
            "name": "egress",
            "rules": [
                {
                    "direction": "out",
                    "protocol": "tcp",
                    "port": "7001",
                    "destination_ips": [f"{B4}/32"],
                },
            ],
            "apply_to": [{"type": "server", "server": {"id": lab.server("a")["id"]}}],
        },
    )["firewall"]
    try:
        assert probe(lab, "a", "tcp", B4, 7001) == f"b tcp {A4}"
        assert probe(lab, "a", "tcp", B4, 7000) == ""
        assert probe(lab, "a", "tcp", C6, 7001) == ""
        api = lab.run(
            "a",
            "curl -s -m3 -o /dev/null -w '%{http_code}' http://198.18.0.1/v1/servers",
        ).stdout
        assert api in ("", "000")  # the API is on the Internet
        # metadata
        assert (
            lab.check_output(
                "a", "curl -s -m3 http://169.254.169.254/hetzner/v1/metadata/hostname"
            )
            == "a"
        )
        assert (
            lab.check_output(
                "a",
                "curl -s -m3 -g http://[fe80::a9fe:a9fe%eth0]/hetzner/v1/metadata/hostname",
            )
            == "a"
        )
        # cloud network
        assert probe(lab, "a", "tcp", B_PRIV, 7000) == f"b tcp {A_PRIV}"
        # inbound is closed too (the firewall has no inbound rules)
        assert probe(lab, "client:alice", "tcp", A4) == ""
    finally:
        lab.api(
            "POST",
            f"/firewalls/{fw['id']}/actions/remove_from_resources",
            {
                "remove_from": [
                    {"type": "server", "server": {"id": lab.server("a")["id"]}}
                ]
            },
        )
        lab.api("DELETE", f"/firewalls/{fw['id']}")
    assert probe(lab, "a", "tcp", B4, 7000) == f"b tcp {A4}"


def test_firewall_covers_floating_ip(lab: LiveLab):
    fip = lab.api(
        "POST",
        "/floating_ips",
        {"type": "ipv4", "home_location": "fsn1", "server": lab.server("a")["id"]},
    )
    floating_ip = fip["floating_ip"]
    lab.wait_action(fip["action"])
    lab.check_output("a", f"ip addr add {floating_ip['ip']}/32 dev eth0")
    lab.check_output("b", f"ip addr add {floating_ip['ip']}/32 dev eth0")
    fw = lab.api(
        "POST",
        "/firewalls",
        {
            "name": "fipfw",
            "rules": [
                {
                    "direction": "in",
                    "protocol": "tcp",
                    "port": "7000",
                    "source_ips": ANY,
                }
            ],
            "apply_to": [{"type": "server", "server": {"id": lab.server("a")["id"]}}],
        },
    )["firewall"]
    try:
        assert probe(lab, "client:alice", "tcp", floating_ip["ip"]) == f"a tcp {ALICE4}"
        assert probe(lab, "client:alice", "tcp", floating_ip["ip"], 7001) == ""
        lab.wait_action(
            lab.api(
                "POST",
                f"/floating_ips/{floating_ip['id']}/actions/assign",
                {"server": lab.server("b")["id"]},
            )["action"]
        )
        # b has no firewall
        assert (
            probe(lab, "client:alice", "tcp", floating_ip["ip"], 7001)
            == f"b tcp {ALICE4}"
        )
    finally:
        lab.api(
            "POST",
            f"/firewalls/{fw['id']}/actions/remove_from_resources",
            {
                "remove_from": [
                    {"type": "server", "server": {"id": lab.server("a")["id"]}}
                ]
            },
        )
        lab.api("DELETE", f"/firewalls/{fw['id']}")
        lab.check_output("a", f"ip addr del {floating_ip['ip']}/32 dev eth0")
        lab.check_output("b", f"ip addr del {floating_ip['ip']}/32 dev eth0")
        lab.wait_action(
            lab.api("POST", f"/floating_ips/{floating_ip['id']}/actions/unassign")[
                "action"
            ]
        )
        lab.api("DELETE", f"/floating_ips/{floating_ip['id']}")
