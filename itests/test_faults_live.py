"""Live tests of fault injection: cuts, isolation, blocks, netem, API faults, pause.

Every test removes its fault again.
"""

import subprocess
import time

import pytest
from livelab import A4, A_PRIV, B4, B6, B_PRIV, C4, LiveLab, ping, probe


def test_public_cut_leaves_the_cloud_network_open(lab: LiveLab):
    lab.control(
        "POST", "/faults", {"type": "cut", "first": "a", "second": "b", "net": "public"}
    )
    try:
        assert probe(lab, "a", "tcp", B4) == ""
        assert probe(lab, "a", "tcp", B6) == ""
        assert probe(lab, "a", "tcp", B_PRIV) == f"b tcp {A_PRIV}"
        assert probe(lab, "a", "tcp", C4) == f"c tcp {A4}"
    finally:
        lab.control(
            "POST",
            "/faults/remove",
            {"type": "cut", "first": "a", "second": "b", "net": "public"},
        )
    assert probe(lab, "a", "tcp", B4) == f"b tcp {A4}"


def test_private_cut_leaves_the_public_network_open(lab: LiveLab):
    lab.control(
        "POST",
        "/faults",
        {"type": "cut", "first": "a", "second": "b", "net": "private"},
    )
    try:
        assert probe(lab, "a", "tcp", B_PRIV) == ""
        assert probe(lab, "a", "tcp", B4) == f"b tcp {A4}"
    finally:
        lab.control(
            "POST",
            "/faults/remove",
            {"type": "cut", "first": "a", "second": "b", "net": "private"},
        )
    assert probe(lab, "a", "tcp", B_PRIV) == f"b tcp {A_PRIV}"


def test_isolated_server_is_unreachable(lab: LiveLab):
    lab.control("POST", "/faults", {"type": "isolate", "vm": "b"})
    try:
        assert probe(lab, "client:alice", "tcp", B4) == ""
        assert probe(lab, "a", "tcp", B_PRIV) == ""
        assert lab.run("b", "true", timeout=15).returncode != 0
    finally:
        lab.control("POST", "/faults/remove", {"type": "isolate", "vm": "b"})
    assert probe(lab, "a", "tcp", B_PRIV) == f"b tcp {A_PRIV}"


@pytest.mark.parametrize("what", ["api", "box", "internet"])
def test_block_closes_only_what_it_names(lab: LiveLab, what):
    api = "curl -s -m3 -o /dev/null -w '%{http_code}' http://198.18.0.1/v1/servers"
    box = "timeout 3 bash -c '</dev/tcp/198.18.0.1/23' && echo open"
    inet = "curl -s -m5 -o /dev/null -w '%{http_code}' http://deb.debian.org/debian/"
    before = {
        "api": lab.run("a", api).stdout,
        "box": lab.run("a", box).stdout,
        "internet": lab.run("a", inet).stdout,
    }
    assert before["api"] == "401" and before["box"].strip() == "open"
    if before["internet"] in ("", "000"):
        pytest.skip("the Docker host has no Internet access")
    lab.control("POST", "/faults", {"type": "block", "vm": "a", "what": what})
    try:
        now = {
            "api": lab.run("a", api).stdout,
            "box": lab.run("a", box).stdout,
            "internet": lab.run("a", inet).stdout,
        }
        for k in now:
            if k == what:
                assert now[k] in ("", "000"), (k, now[k])
            else:
                assert now[k] == before[k], (k, now[k])
    finally:
        lab.control(
            "POST", "/faults/remove", {"type": "block", "vm": "a", "what": what}
        )


def test_netem_delays_only_the_public_tap(lab: LiveLab):
    lab.control("POST", "/netem", {"vm": "a", "net": "public", "args": "delay 100ms"})
    try:
        rtt = ping(lab, "client:alice", A4)
        assert rtt is not None and rtt >= 100, rtt
        assert ping(lab, "b", A_PRIV) < 50  # only the public tap is delayed
    finally:
        lab.control("POST", "/netem", {"vm": "a", "net": "public", "args": ""})
    assert ping(lab, "client:alice", A4) < 50
    assert 2.5 <= ping(lab, "a", B4) < 15  # location latency is back


def test_api_faults_answer_errors_and_delays(lab: LiveLab):
    lab.control("POST", "/api-fault", {"status": 503, "count": 2})
    codes = [lab.http.get("/servers").status_code for _ in range(3)]
    assert codes == [503, 503, 200]
    lab.control("POST", "/api-fault", {"delay_ms": 800})
    t0 = time.time()
    lab.http.get("/servers")
    assert time.time() - t0 >= 0.8
    lab.control("POST", "/api-fault", {})


def test_pause_freezes_the_guest(lab: LiveLab):
    up0 = float(lab.check_output("c", "cut -d' ' -f1 /proc/uptime"))
    t0 = time.time()
    lab.control("POST", "/vm/c/pause")
    try:
        time.sleep(8)
        try:
            # ssh gives up after its connect timeout
            assert lab.run("c", "true", timeout=8).returncode != 0
        except subprocess.TimeoutExpired:
            pass
    finally:
        lab.control("POST", "/vm/c/cont")
    lab.wait_ssh("c")
    up1 = float(lab.check_output("c", "cut -d' ' -f1 /proc/uptime"))
    wall = time.time() - t0
    assert up1 - up0 < wall - 5, (up1 - up0, wall)  # the guest missed the pause
    # the cloud does not notice a frozen guest
    assert lab.server("c")["status"] == "running"
