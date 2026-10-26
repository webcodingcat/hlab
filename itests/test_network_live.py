"""Live tests of addresses, routing, DNS, metadata and cloud networks."""

import subprocess
import time

import pytest
from livelab import (
    A4,
    A6,
    A_PRIV,
    ALICE4,
    ALICE6,
    B4,
    B6,
    B_PRIV,
    C4,
    C6,
    C_PRIV,
    LiveLab,
    ping,
    probe,
)


def test_servers_have_hetzners_addresses_and_routes(lab: LiveLab):
    out = lab.check_output("a", "ip -br a; ip r; ip -6 r")
    assert f"{A4}/32" in out and f"{A6}/64" in out and f"{A_PRIV}/32" in out
    assert "default via 172.31.1.1" in out and "default via fe80::1" in out
    assert "10.0.0.0/16 via 10.0.0.1" in out


@pytest.mark.parametrize(
    "where, addr, seen",
    [
        ("client:alice", A4, ALICE4),
        ("client:alice", A6, ALICE6),  # Internet client keeps its address
        ("b", A4, B4),
        ("b", A6, B6),  # server to server, public
        ("b", A_PRIV, B_PRIV),  # cloud network
    ],
)
@pytest.mark.parametrize("proto", ["tcp", "udp"])
def test_echo_sees_the_real_source_address(lab: LiveLab, where, addr, seen, proto):
    assert probe(lab, where, proto, addr) == f"a {proto} {seen}"


def test_operator_reaches_servers_from_its_own_address(lab: LiveLab):
    # what tools in the lab's namespace (OpenTofu, Ansible) send comes from 192.0.2.2 /
    # 2001:db8:fffe::2
    out = subprocess.run(
        ["socat", "-T3", "-", f"TCP:{A4}:7000"],
        capture_output=True,
        text=True,
        timeout=10,
    ).stdout
    assert out.strip() == "a tcp 192.0.2.2"
    out = subprocess.run(
        ["socat", "-T3", "-", f"TCP:[{A6}]:7000"],
        capture_output=True,
        text=True,
        timeout=10,
    ).stdout
    assert out.strip() == "a tcp 2001:db8:fffe::2"


def test_latency_between_locations_is_like_hetzners(lab: LiveLab):
    same_zone = ping(lab, "a", B4)  # fsn1 – nbg1: 3 ms
    far = ping(lab, "a", C4)  # fsn1 – hel1: 24 ms
    far6 = ping(lab, "a", C6)
    assert same_zone is not None and 2.5 <= same_zone < 15, same_zone
    assert far is not None and 22 <= far < 45, far
    assert far6 is not None and 22 <= far6 < 45, far6
    private = ping(lab, "a", B_PRIV)
    assert private is not None and 2.5 <= private < 15, private


def test_cloud_networks_are_closed(lab: LiveLab):
    assert probe(lab, "c", "tcp", A_PRIV) == ""  # another network
    assert probe(lab, "client:alice", "tcp", A_PRIV) == ""  # the Internet
    assert ping(lab, "c", A_PRIV, count=1) is None
    # IPv4 only, and nothing leaves a network through the lab: not over IPv6, not to
    # public addresses
    nic = lab.check_output("a", "ip -br a | awk '/10.0.1.1\\// {print $1}'")
    assert lab.run("a", f"ping -6 -c1 -W2 -I {nic} {B6}").returncode != 0
    lab.check_output("a", f"ip route add {B4}/32 via 10.0.0.1 dev {nic} onlink")
    try:
        assert ping(lab, "a", B4, count=1) is None
    finally:
        lab.check_output("a", f"ip route del {B4}/32 via 10.0.0.1 dev {nic} onlink")
    assert ping(lab, "a", B4, count=1) is not None


def test_network_table_ends_without_its_routes(lab: LiveLab):
    """Check that a network's traffic still ends in its table once its routes are gone.

    A default route through a gateway server replaces the table's end; gone again (route
    deleted, gateway off), the network's traffic must still end there instead of falling
    through to the uplink.
    """
    net = lab.nets["one"]
    lab.wait_action(
        lab.api(
            "POST",
            f"/networks/{net}/actions/add_route",
            {"destination": "0.0.0.0/0", "gateway": B_PRIV},
        )["action"]
    )
    lab.wait_action(
        lab.api(
            "POST",
            f"/networks/{net}/actions/delete_route",
            {"destination": "0.0.0.0/0", "gateway": B_PRIV},
        )["action"]
    )
    rules = subprocess.run(["ip", "rule"], capture_output=True, text=True).stdout
    taps = [
        line.split("iif ")[1].split()[0]
        for line in rules.splitlines()
        if "lookup" in line and "iif prv" in line
    ]
    for tap in taps:
        got = subprocess.run(
            ["ip", "route", "get", "8.8.8.8", "from", A_PRIV, "iif", tap],
            capture_output=True,
            text=True,
        )
        assert got.returncode != 0 or "unreachable" in got.stdout, (tap, got.stdout)


def test_servers_resolve_the_api_and_read_their_metadata(lab: LiveLab):
    out = lab.check_output("a", "getent ahosts api.hlab | awk '{print $1}' | sort -u")
    assert "198.18.0.1" in out and "2001:db8:fffe::1" in out
    assert (
        lab.check_output(
            "a", "curl -s http://169.254.169.254/hetzner/v1/metadata/hostname"
        )
        == "a"
    )
    assert (
        lab.check_output(
            "a", "curl -s http://169.254.169.254/hetzner/v1/metadata/public-ipv4"
        )
        == A4
    )
    nets = lab.check_output(
        "a", "curl -s http://169.254.169.254/hetzner/v1/metadata/private-networks"
    )
    assert f"ip: {A_PRIV}" in nets and "gateway: 10.0.0.1" in nets
    assert (
        lab.check_output(
            "a", "curl -s http://169.254.169.254/hetzner/v1/metadata/availability-zone"
        )
        == "fsn1-dc14"
    )


def test_api_is_reachable_from_servers_over_both_families(lab: LiveLab):
    for host in ("198.18.0.1", "[2001:db8:fffe::1]"):
        code = lab.check_output(
            "a",
            "curl -s -o /dev/null -w '%{http_code}' "
            f"-H 'Authorization: Bearer {'hlab' * 16}' "
            f"http://{host}/v1/servers",
        )
        assert code == "200"


def test_floating_ipv6_moves(lab: LiveLab):
    fip = lab.api(
        "POST",
        "/floating_ips",
        {"type": "ipv6", "home_location": "fsn1", "server": lab.server("a")["id"]},
    )
    floating_ip = fip["floating_ip"]
    lab.wait_action(fip["action"])
    addr = floating_ip["ip"].split("/")[0] + "1"
    for n in ("a", "b"):
        lab.check_output(n, f"ip -6 addr add {addr}/64 dev eth0 nodad")
    try:
        assert probe(lab, "client:alice", "tcp", addr) == f"a tcp {ALICE6}"
        lab.wait_action(
            lab.api(
                "POST",
                f"/floating_ips/{floating_ip['id']}/actions/assign",
                {"server": lab.server("b")["id"]},
            )["action"]
        )
        assert probe(lab, "client:alice", "tcp", addr) == f"b tcp {ALICE6}"
    finally:
        for n in ("a", "b"):
            lab.run(n, f"ip -6 addr del {addr}/64 dev eth0")
        lab.wait_action(
            lab.api("POST", f"/floating_ips/{floating_ip['id']}/actions/unassign")[
                "action"
            ]
        )
        lab.api("DELETE", f"/floating_ips/{floating_ip['id']}")


def test_network_hot_plugs_into_a_running_server(lab: LiveLab):
    cid = lab.server("c")["id"]
    lab.wait_action(
        lab.api(
            "POST",
            f"/servers/{cid}/actions/attach_to_network",
            {"network": lab.nets["one"]},
        )["action"]
    )
    srv = lab.server("c")
    ip = next(p["ip"] for p in srv["private_net"] if p["network"] == lab.nets["one"])
    for _ in range(20):
        if probe(lab, "c", "tcp", A_PRIV) == f"a tcp {ip}":
            break
        time.sleep(1)
    assert probe(lab, "c", "tcp", A_PRIV) == f"a tcp {ip}"
    lab.wait_action(
        lab.api(
            "POST",
            f"/servers/{cid}/actions/detach_from_network",
            {"network": lab.nets["one"]},
        )["action"]
    )
    assert probe(lab, "c", "tcp", A_PRIV) == ""
    assert C_PRIV in lab.check_output("c", "ip -br a")  # its own network stays


def test_network_route_goes_through_a_server(lab: LiveLab):
    net = lab.nets["one"]
    b_priv = next(
        p["ip"] for p in lab.server("b")["private_net"] if p["network"] == net
    )
    lab.check_output("b", "ip addr add 10.100.0.1/32 dev lo")
    lab.wait_action(
        lab.api(
            "POST",
            f"/networks/{net}/actions/add_route",
            {"destination": "10.100.0.0/24", "gateway": b_priv},
        )["action"]
    )
    try:
        iface = lab.check_output("a", "ip -br a | awk '/10.0.1.1\\// {print $1}'")
        lab.check_output(
            "a", f"networkctl renew {iface}; sleep 3"
        )  # the route arrives by DHCP
        assert "10.100.0.0/24 via 10.0.0.1" in lab.check_output("a", "ip r")
        assert ping(lab, "a", "10.100.0.1") is not None
    finally:
        lab.api(
            "POST",
            f"/networks/{net}/actions/delete_route",
            {"destination": "10.100.0.0/24", "gateway": b_priv},
        )
        lab.run("b", "ip addr del 10.100.0.1/32 dev lo")


def test_overlapping_networks_work_side_by_side(lab: LiveLab):
    """Check a second network with the range of "one" (allowed on Hetzner).

    Its servers get a's and b's private IPs, both networks work side by side and stay
    apart, and removing the twins leaves a and b alone.
    """
    nid = lab.api(
        "POST",
        "/networks",
        {
            "name": "one-again",
            "ip_range": "10.0.0.0/16",
            "subnets": [
                {
                    "type": "cloud",
                    "ip_range": "10.0.1.0/24",
                    "network_zone": "eu-central",
                }
            ],
        },
    )["network"]["id"]
    try:
        x9, x10 = (
            lab.create("x9", networks=[nid]),
            lab.create("x10", networks=[nid], location="nbg1"),
        )
        assert [x["private_net"][0]["ip"] for x in (x9, x10)] == [
            "10.0.1.1",
            "10.0.1.2",
        ]
        for n, ip in (("x9", "10.0.1.1"), ("x10", "10.0.1.2")):
            for _ in range(30):
                if f"{ip}/32" in lab.run(n, "ip -br a").stdout:
                    break
                time.sleep(1)
            # DHCP hands out the same address in both networks
            assert f"{ip}/32" in lab.check_output(n, "ip -br a")
        lab.start_echo("x9")
        assert probe(lab, "x10", "tcp", "10.0.1.1") == "x9 tcp 10.0.1.2"
        assert probe(lab, "b", "tcp", "10.0.1.1") == "a tcp 10.0.1.2"
    finally:
        lab.delete("x9")
        lab.delete("x10")
        lab.api("DELETE", f"/networks/{nid}")
    # a's and b's routes survived the twins
    assert probe(lab, "b", "tcp", "10.0.1.1") == "a tcp 10.0.1.2"
