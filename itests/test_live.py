"""Live tests against real VMs; see conftest.py for the setup. Run in file order: the power tests come last."""

import subprocess
import time

import pytest

from livelab import Lab, ping, probe

A4, B4, C4 = "198.51.100.11", "198.51.100.12", "198.51.100.13"
A6, B6, C6 = "2001:db8:1::1", "2001:db8:2::1", "2001:db8:3::1"
A_PRIV, B_PRIV, C_PRIV = "10.0.1.1", "10.0.1.2", "10.1.1.1"
ALICE4, ALICE6 = "192.0.2.11", "2001:db8:ffff::b"
ANY = ["0.0.0.0/0", "::/0"]


def set_labels(lab: Lab, name: str, labels: dict) -> None:
    lab.api("PUT", f"/servers/{lab.server(name)['id']}", {"labels": {"lab": "live", **labels}})


# --- addressing and routing ----------------------------------------------------------------------------------------


def test_addresses_like_hetzner(lab: Lab):
    out = lab.sh("a", "ip -br a; ip r; ip -6 r")
    assert f"{A4}/32" in out and f"{A6}/64" in out and f"{A_PRIV}/32" in out
    assert "default via 172.31.1.1" in out and "default via fe80::1" in out
    assert "10.0.0.0/16 via 10.0.0.1" in out


@pytest.mark.parametrize("where, addr, seen", [
    ("client:alice", A4, ALICE4), ("client:alice", A6, ALICE6),       # Internet client keeps its address
    ("b", A4, B4), ("b", A6, B6),                                     # server to server, public
    ("b", A_PRIV, B_PRIV),                                            # cloud network
])
@pytest.mark.parametrize("proto", ["tcp", "udp"])
def test_echo_sees_real_source(lab: Lab, where, addr, seen, proto):
    assert probe(lab, where, proto, addr) == f"a {proto} {seen}"


def test_operator_reaches_servers_from_its_own_address(lab: Lab):
    # what tools in the lab's namespace (OpenTofu, Ansible) send comes from 192.0.2.2 / 2001:db8:fffe::2
    out = subprocess.run(["socat", "-T3", "-", f"TCP:{A4}:7000"], capture_output=True, text=True, timeout=10).stdout
    assert out.strip() == "a tcp 192.0.2.2"
    out = subprocess.run(["socat", "-T3", "-", f"TCP:[{A6}]:7000"], capture_output=True, text=True, timeout=10).stdout
    assert out.strip() == "a tcp 2001:db8:fffe::2"


def test_latency_between_locations(lab: Lab):
    same_zone = ping(lab, "a", B4)          # fsn1 – nbg1: 3 ms
    far = ping(lab, "a", C4)                # fsn1 – hel1: 24 ms
    far6 = ping(lab, "a", C6)
    assert same_zone is not None and 2.5 <= same_zone < 15, same_zone
    assert far is not None and 22 <= far < 45, far
    assert far6 is not None and 22 <= far6 < 45, far6
    private = ping(lab, "a", B_PRIV)
    assert private is not None and 2.5 <= private < 15, private


def test_cloud_networks_are_closed(lab: Lab):
    assert probe(lab, "c", "tcp", A_PRIV) == ""             # another network
    assert probe(lab, "client:alice", "tcp", A_PRIV) == ""  # the Internet
    assert ping(lab, "c", A_PRIV, count=1) is None
    # IPv4 only, and nothing leaves a network through the lab: not over IPv6, not to public addresses
    nic = lab.sh("a", "ip -br a | awk '/10.0.1.1\\// {print $1}'")
    assert lab.ssh("a", f"ping -6 -c1 -W2 -I {nic} {B6}").returncode != 0
    lab.sh("a", f"ip route add {B4}/32 via 10.0.0.1 dev {nic} onlink")
    try:
        assert ping(lab, "a", B4, count=1) is None
    finally:
        lab.sh("a", f"ip route del {B4}/32 via 10.0.0.1 dev {nic} onlink")
    assert ping(lab, "a", B4, count=1) is not None


def test_network_table_ends_without_its_routes(lab: Lab):
    """A default route through a gateway server replaces the table's end; gone again (route deleted, gateway off),
    the network's traffic must still end there instead of falling through to the uplink."""
    net = lab.nets["one"]
    lab.wait_action(lab.api("POST", f"/networks/{net}/actions/add_route",
                            {"destination": "0.0.0.0/0", "gateway": B_PRIV})["action"])
    lab.wait_action(lab.api("POST", f"/networks/{net}/actions/delete_route",
                            {"destination": "0.0.0.0/0", "gateway": B_PRIV})["action"])
    rules =subprocess.run(["ip", "rule"], capture_output=True, text=True).stdout
    taps = [line.split("iif ")[1].split()[0] for line in rules.splitlines() if "lookup" in line and "iif prv" in line]
    for tap in taps:
        got = subprocess.run(["ip", "route", "get", "8.8.8.8", "from", A_PRIV, "iif", tap],
                             capture_output=True, text=True)
        assert got.returncode != 0 or "unreachable" in got.stdout, (tap, got.stdout)


def test_dns_and_metadata(lab: Lab):
    out = lab.sh("a", "getent ahosts api.hlab | awk '{print $1}' | sort -u")
    assert "198.18.0.1" in out and "2001:db8:fffe::1" in out
    assert lab.sh("a", "curl -s http://169.254.169.254/hetzner/v1/metadata/hostname") == "a"
    assert lab.sh("a", "curl -s http://169.254.169.254/hetzner/v1/metadata/public-ipv4") == A4
    nets = lab.sh("a", "curl -s http://169.254.169.254/hetzner/v1/metadata/private-networks")
    assert f"ip: {A_PRIV}" in nets and "gateway: 10.0.0.1" in nets
    assert lab.sh("a", "curl -s http://169.254.169.254/hetzner/v1/metadata/availability-zone") == "fsn1-dc14"


def test_api_reachable_from_servers_over_both_families(lab: Lab):
    for host in ("198.18.0.1", "[2001:db8:fffe::1]"):
        code = lab.sh("a", f"curl -s -o /dev/null -w '%{{http_code}}' -H 'Authorization: Bearer {'hlab' * 16}' "
                           f"http://{host}/v1/servers")
        assert code == "200"


# --- firewalls --------------------------------------------------------------------------------------------------


def test_firewall_inbound(lab: Lab):
    fw = lab.api("POST", "/firewalls", {"name": "web", "rules": [
        {"direction": "in", "protocol": "tcp", "port": "7000", "source_ips": ANY},
        {"direction": "in", "protocol": "tcp", "port": "22", "source_ips": ANY},
    ], "apply_to": [{"type": "label_selector", "label_selector": {"selector": "role=web"}}]})["firewall"]
    try:
        assert probe(lab, "client:alice", "tcp", A4, 7001)   # open before the label
        set_labels(lab, "a", {"role": "web"})
        assert probe(lab, "client:alice", "tcp", A4) == f"a tcp {ALICE4}"
        assert probe(lab, "client:alice", "tcp", A6) == f"a tcp {ALICE6}"
        assert probe(lab, "client:alice", "tcp", A4, 7001) == ""
        assert probe(lab, "client:alice", "tcp", A6, 7001) == ""
        assert probe(lab, "client:alice", "udp", A4) == ""
        assert ping(lab, "client:alice", A4, count=1) is None
        assert probe(lab, "b", "tcp", A4, 7001) == ""          # other servers come over the public network too
        assert probe(lab, "b", "tcp", A_PRIV, 7001) == f"a tcp {B_PRIV}"   # cloud networks are not filtered
        assert lab.ssh("a", "true").returncode == 0             # the lab's own access passes
        # outbound stays open without outbound rules; answers to a's own connections come back
        assert probe(lab, "a", "tcp", B4, 7001) == f"b tcp {A4}"
        # ICMP rule added: ping works, from IPv6 too
        lab.wait_action(lab.api("POST", f"/firewalls/{fw['id']}/actions/set_rules", {"rules": [
            {"direction": "in", "protocol": "tcp", "port": "7000", "source_ips": ANY},
            {"direction": "in", "protocol": "icmp", "source_ips": ANY}]})["actions"][0])
        assert ping(lab, "client:alice", A4, count=1) is not None
        assert ping(lab, "client:alice", A6, count=1) is not None
        # source restriction: only the client's /32
        lab.api("POST", f"/firewalls/{fw['id']}/actions/set_rules", {"rules": [
            {"direction": "in", "protocol": "tcp", "port": "7001", "source_ips": [f"{B4}/32"]}]})
        assert probe(lab, "b", "tcp", A4, 7001) == f"a tcp {B4}"
        assert probe(lab, "client:alice", "tcp", A4, 7001) == ""
        assert probe(lab, "b", "tcp", A6, 7001) == ""            # the IPv6 of b is not listed
        set_labels(lab, "a", {})
        assert probe(lab, "client:alice", "tcp", A4, 7001) == f"a tcp {ALICE4}"   # label gone, firewall gone
        # a firewall still applied (here by its label selector) cannot be deleted
        assert lab.http.delete(f"/firewalls/{fw['id']}").json()["error"]["code"] == "resource_in_use"
    finally:
        set_labels(lab, "a", {})
        lab.api("POST", f"/firewalls/{fw['id']}/actions/remove_from_resources", {"remove_from": [
            {"type": "label_selector", "label_selector": {"selector": "role=web"}}]})
        lab.api("DELETE", f"/firewalls/{fw['id']}")


def test_firewall_outbound(lab: Lab):
    fw = lab.api("POST", "/firewalls", {"name": "egress", "rules": [
        {"direction": "out", "protocol": "tcp", "port": "7001", "destination_ips": [f"{B4}/32"]},
    ], "apply_to": [{"type": "server", "server": {"id": lab.server("a")["id"]}}]})["firewall"]
    try:
        assert probe(lab, "a", "tcp", B4, 7001) == f"b tcp {A4}"
        assert probe(lab, "a", "tcp", B4, 7000) == ""
        assert probe(lab, "a", "tcp", C6, 7001) == ""
        api = lab.ssh("a", "curl -s -m3 -o /dev/null -w '%{http_code}' http://198.18.0.1/v1/servers").stdout
        assert api in ("", "000")                                  # the API is on the Internet
        assert lab.sh("a", "curl -s -m3 http://169.254.169.254/hetzner/v1/metadata/hostname") == "a"  # metadata
        assert lab.sh("a", "curl -s -m3 -g http://[fe80::a9fe:a9fe%eth0]/hetzner/v1/metadata/hostname") == "a"
        assert probe(lab, "a", "tcp", B_PRIV, 7000) == f"b tcp {A_PRIV}"    # cloud network
        # inbound is closed too (the firewall has no inbound rules)
        assert probe(lab, "client:alice", "tcp", A4) == ""
    finally:
        lab.api("POST", f"/firewalls/{fw['id']}/actions/remove_from_resources",
                {"remove_from": [{"type": "server", "server": {"id": lab.server("a")["id"]}}]})
        lab.api("DELETE", f"/firewalls/{fw['id']}")
    assert probe(lab, "a", "tcp", B4, 7000) == f"b tcp {A4}"


def test_firewall_covers_floating_ip(lab: Lab):
    fip = lab.api("POST", "/floating_ips", {"type": "ipv4", "home_location": "fsn1", "server": lab.server("a")["id"]})
    f = fip["floating_ip"]
    lab.wait_action(fip["action"])
    lab.sh("a", f"ip addr add {f['ip']}/32 dev eth0")
    lab.sh("b", f"ip addr add {f['ip']}/32 dev eth0")
    fw = lab.api("POST", "/firewalls", {"name": "fipfw", "rules": [
        {"direction": "in", "protocol": "tcp", "port": "7000", "source_ips": ANY}],
        "apply_to": [{"type": "server", "server": {"id": lab.server("a")["id"]}}]})["firewall"]
    try:
        assert probe(lab, "client:alice", "tcp", f["ip"]) == f"a tcp {ALICE4}"
        assert probe(lab, "client:alice", "tcp", f["ip"], 7001) == ""
        lab.wait_action(lab.api("POST", f"/floating_ips/{f['id']}/actions/assign", {"server": lab.server("b")["id"]})["action"])
        assert probe(lab, "client:alice", "tcp", f["ip"], 7001) == f"b tcp {ALICE4}"   # b has no firewall
    finally:
        lab.api("POST", f"/firewalls/{fw['id']}/actions/remove_from_resources",
                {"remove_from": [{"type": "server", "server": {"id": lab.server("a")["id"]}}]})
        lab.api("DELETE", f"/firewalls/{fw['id']}")
        lab.sh("a", f"ip addr del {f['ip']}/32 dev eth0")
        lab.sh("b", f"ip addr del {f['ip']}/32 dev eth0")
        lab.wait_action(lab.api("POST", f"/floating_ips/{f['id']}/actions/unassign")["action"])
        lab.api("DELETE", f"/floating_ips/{f['id']}")


def test_floating_ipv6_moves(lab: Lab):
    fip = lab.api("POST", "/floating_ips", {"type": "ipv6", "home_location": "fsn1", "server": lab.server("a")["id"]})
    f = fip["floating_ip"]
    lab.wait_action(fip["action"])
    addr = f["ip"].split("/")[0] + "1"
    for n in ("a", "b"):
        lab.sh(n, f"ip -6 addr add {addr}/64 dev eth0 nodad")
    try:
        assert probe(lab, "client:alice", "tcp", addr) == f"a tcp {ALICE6}"
        lab.wait_action(lab.api("POST", f"/floating_ips/{f['id']}/actions/assign", {"server": lab.server("b")["id"]})["action"])
        assert probe(lab, "client:alice", "tcp", addr) == f"b tcp {ALICE6}"
    finally:
        for n in ("a", "b"):
            lab.ssh(n, f"ip -6 addr del {addr}/64 dev eth0")
        lab.wait_action(lab.api("POST", f"/floating_ips/{f['id']}/actions/unassign")["action"])
        lab.api("DELETE", f"/floating_ips/{f['id']}")


# --- faults ---------------------------------------------------------------------------------------------------


def test_cut_public_only(lab: Lab):
    lab.control("POST", "/faults", {"type": "cut", "a": "a", "b": "b", "net": "public"})
    try:
        assert probe(lab, "a", "tcp", B4) == ""
        assert probe(lab, "a", "tcp", B6) == ""
        assert probe(lab, "a", "tcp", B_PRIV) == f"b tcp {A_PRIV}"
        assert probe(lab, "a", "tcp", C4) == f"c tcp {A4}"
    finally:
        lab.control("POST", "/faults/remove", {"type": "cut", "a": "a", "b": "b", "net": "public"})
    assert probe(lab, "a", "tcp", B4) == f"b tcp {A4}"


def test_cut_private_only(lab: Lab):
    lab.control("POST", "/faults", {"type": "cut", "a": "a", "b": "b", "net": "private"})
    try:
        assert probe(lab, "a", "tcp", B_PRIV) == ""
        assert probe(lab, "a", "tcp", B4) == f"b tcp {A4}"
    finally:
        lab.control("POST", "/faults/remove", {"type": "cut", "a": "a", "b": "b", "net": "private"})
    assert probe(lab, "a", "tcp", B_PRIV) == f"b tcp {A_PRIV}"


def test_isolate(lab: Lab):
    lab.control("POST", "/faults", {"type": "isolate", "vm": "b"})
    try:
        assert probe(lab, "client:alice", "tcp", B4) == ""
        assert probe(lab, "a", "tcp", B_PRIV) == ""
        assert lab.ssh("b", "true", timeout=15).returncode != 0
    finally:
        lab.control("POST", "/faults/remove", {"type": "isolate", "vm": "b"})
    assert probe(lab, "a", "tcp", B_PRIV) == f"b tcp {A_PRIV}"


@pytest.mark.parametrize("what", ["api", "box", "internet"])
def test_block(lab: Lab, what):
    api = "curl -s -m3 -o /dev/null -w '%{http_code}' http://198.18.0.1/v1/servers"
    box = "timeout 3 bash -c '</dev/tcp/198.18.0.1/23' && echo open"
    inet = "curl -s -m5 -o /dev/null -w '%{http_code}' http://deb.debian.org/debian/"
    before = {"api": lab.ssh("a", api).stdout, "box": lab.ssh("a", box).stdout, "internet": lab.ssh("a", inet).stdout}
    assert before["api"] == "401" and before["box"].strip() == "open"
    if before["internet"] in ("", "000"):
        pytest.skip("the Docker host has no Internet access")
    lab.control("POST", "/faults", {"type": "block", "vm": "a", "what": what})
    try:
        now = {"api": lab.ssh("a", api).stdout, "box": lab.ssh("a", box).stdout, "internet": lab.ssh("a", inet).stdout}
        for k in now:
            if k == what:
                assert now[k] in ("", "000"), (k, now[k])
            else:
                assert now[k] == before[k], (k, now[k])
    finally:
        lab.control("POST", "/faults/remove", {"type": "block", "vm": "a", "what": what})


def test_netem(lab: Lab):
    lab.control("POST", "/netem", {"vm": "a", "net": "public", "args": "delay 100ms"})
    try:
        rtt = ping(lab, "client:alice", A4)
        assert rtt is not None and rtt >= 100, rtt
        assert ping(lab, "b", A_PRIV) < 50   # only the public tap is delayed
    finally:
        lab.control("POST", "/netem", {"vm": "a", "net": "public", "args": ""})
    assert ping(lab, "client:alice", A4) < 50
    assert 2.5 <= ping(lab, "a", B4) < 15   # location latency is back


def test_api_faults(lab: Lab):
    lab.control("POST", "/api-fault", {"status": 503, "count": 2})
    codes = [lab.http.get("/servers").status_code for _ in range(3)]
    assert codes == [503, 503, 200]
    lab.control("POST", "/api-fault", {"delay_ms": 800})
    t0 = time.time()
    lab.http.get("/servers")
    assert time.time() - t0 >= 0.8
    lab.control("POST", "/api-fault", {})


def test_pause_freezes_the_guest(lab: Lab):
    up0 = float(lab.sh("c", "cut -d' ' -f1 /proc/uptime"))
    t0 = time.time()
    lab.control("POST", "/vm/c/pause")
    try:
        time.sleep(8)
        try:
            assert lab.ssh("c", "true", timeout=8).returncode != 0   # ssh gives up after its connect timeout
        except subprocess.TimeoutExpired:
            pass
    finally:
        lab.control("POST", "/vm/c/cont")
    lab.wait_ssh("c")
    up1 = float(lab.sh("c", "cut -d' ' -f1 /proc/uptime"))
    wall = time.time() - t0
    assert up1 - up0 < wall - 5, (up1 - up0, wall)       # the guest missed the pause
    assert lab.server("c")["status"] == "running"        # the cloud does not notice a frozen guest


# --- cloud network hot-plug --------------------------------------------------------------------------------------


def test_attach_and_detach_network(lab: Lab):
    cid = lab.server("c")["id"]
    lab.wait_action(lab.api("POST", f"/servers/{cid}/actions/attach_to_network", {"network": lab.nets["one"]})["action"])
    srv = lab.server("c")
    ip = next(p["ip"] for p in srv["private_net"] if p["network"] == lab.nets["one"])
    for _ in range(20):
        if probe(lab, "c", "tcp", A_PRIV) == f"a tcp {ip}":
            break
        time.sleep(1)
    assert probe(lab, "c", "tcp", A_PRIV) == f"a tcp {ip}"
    lab.wait_action(lab.api("POST", f"/servers/{cid}/actions/detach_from_network", {"network": lab.nets["one"]})["action"])
    assert probe(lab, "c", "tcp", A_PRIV) == ""
    assert C_PRIV in lab.sh("c", "ip -br a")   # its own network stays


# --- Storage Box ------------------------------------------------------------------------------------------------


def test_storage_box(lab: Lab, tmp_path):
    import paramiko

    key = paramiko.Ed25519Key.from_private_key_file(_keypair(tmp_path))
    lab.control("POST", "/box/users", {"name": "backup1", "public_key": (tmp_path / "k.pub").read_text()})
    for host in ("198.18.0.1", "2001:db8:fffe::1"):
        t = paramiko.Transport((host, 23))
        t.connect(username="backup1", pkey=key)
        sftp = paramiko.SFTPClient.from_transport(t)
        d = f"t-{time.time_ns()}"
        sftp.mkdir(d)
        for name in ("one", "two"):
            with sftp.open(f"{d}/tmp-{name}", "w") as f:
                f.write(name)
        sftp.rename(f"{d}/tmp-one", f"{d}/lease")
        with pytest.raises(IOError):
            sftp.rename(f"{d}/tmp-two", f"{d}/lease")    # a plain rename never overwrites: exactly one winner
        with sftp.open(f"{d}/lease") as f:
            assert f.read() == b"one"
        sftp.posix_rename(f"{d}/tmp-two", f"{d}/lease")  # posix-rename does overwrite
        with sftp.open(f"{d}/lease") as f:
            assert f.read() == b"two"
        sftp.remove(f"{d}/lease")
        sftp.rmdir(d)
        sftp.close()
        t.close()
    out = subprocess.run(["ssh", "-p", "23", "-i", str(tmp_path / "k"), "-o", "StrictHostKeyChecking=no",
                          "-o", "UserKnownHostsFile=/dev/null", "backup1@198.18.0.1", "df"],
                         capture_output=True, text=True, timeout=20)
    assert out.stdout.splitlines()[-1].endswith("/home")       # the account sees only its own directory
    bad = subprocess.run(["ssh", "-p", "23", "-i", str(tmp_path / "k"), "-o", "StrictHostKeyChecking=no",
                          "-o", "UserKnownHostsFile=/dev/null", "backup1@198.18.0.1", "cat /etc/passwd"],
                         capture_output=True, text=True, timeout=20)
    # the account's root is all it sees: its /etc/passwd knows only the account itself
    assert [line.split(":")[0] for line in bad.stdout.splitlines()] == ["backup1"]


def _keypair(tmp_path) -> str:
    subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(tmp_path / "k")], check=True)
    return str(tmp_path / "k")


# --- power (last: these disturb the servers) ---------------------------------------------------------------------


def test_poweroff_and_on(lab: Lab):
    t0 = time.time()
    lab.action("b", "poweroff")
    assert 8 <= time.time() - t0 < 20
    assert lab.server("b")["status"] == "off"
    assert probe(lab, "a", "tcp", B_PRIV) == ""
    lab.action("b", "poweron")
    assert lab.server("b")["status"] == "running"
    lab.wait_ssh("b")
    lab.start_echo("b")
    assert probe(lab, "client:alice", "tcp", B6) == f"b tcp {ALICE6}"   # IPv6 route survives the power cycle
    assert probe(lab, "a", "tcp", B_PRIV) == f"b tcp {A_PRIV}"


def test_server_locked_while_action_runs(lab: Lab):
    sid = lab.server("b")["id"]
    a = lab.api("POST", f"/servers/{sid}/actions/poweroff")["action"]
    r = lab.http.post(f"/servers/{sid}/actions/poweron")
    assert r.status_code == 423
    lab.wait_action(a)
    lab.action("b", "poweron")
    lab.wait_ssh("b")


def test_dead_hardware(lab: Lab):
    lab.action("b", "poweroff")
    lab.control("POST", "/vm/b/dead")
    lab.action("b", "poweron")
    assert lab.server("b")["status"] == "off"
    lab.control("POST", "/vm/b/alive")
    lab.action("b", "poweron")
    lab.wait_ssh("b")


def test_shutdown_is_graceful(lab: Lab):
    lab.sh("b", "cloud-init status --wait >/dev/null; sync")
    lab.action("b", "shutdown")   # done once the button is pressed; off follows when the guest is down
    for _ in range(60):
        if lab.server("b")["status"] == "off":
            break
        time.sleep(1)
    assert lab.server("b")["status"] == "off"
    console = subprocess.run(["sh", "-c", f"tail -40 /hlab/vm/{lab.server('b')['id']}/console.log"],
                             capture_output=True, text=True).stdout
    assert "reboot: Power down" in console or "Power-Off" in console
    lab.action("b", "poweron")
    lab.wait_ssh("b")


def test_reset_reboots(lab: Lab):
    boot0 = lab.sh("a", "cat /proc/sys/kernel/random/boot_id")
    lab.action("a", "reset")
    lab.wait_ssh("a")
    assert lab.sh("a", "cat /proc/sys/kernel/random/boot_id") != boot0


def test_delete_cleans_up(lab: Lab):
    sid = lab.server("c")["id"]
    lab.wait_action(lab.api("DELETE", f"/servers/{sid}")["action"])
    links = subprocess.run(["ip", "-br", "link"], capture_output=True, text=True).stdout
    routes = subprocess.run(["ip", "-6", "route"], capture_output=True, text=True).stdout
    assert "pub3 " not in links and "prv3x1" not in links
    assert "2001:db8:3::/64" not in routes
    assert lab.http.get(f"/servers/{sid}").status_code == 404
