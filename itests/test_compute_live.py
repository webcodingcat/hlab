"""Live tests of servers and what surrounds them.

Boot like Hetzner, primary IPs, rebuild, snapshots in files, volumes, password reset,
other system images: these bring their own servers (x*) and leave a, b and c alone.
The power tests at the end power b off, reset a and delete c; conftest.py runs them
after all others.
"""

import subprocess
import time

import pytest
from livelab import A_PRIV, ALICE6, B6, B_PRIV, LiveLab, ping, probe


def test_server_boots_through_hetzners_datasource(lab: LiveLab):
    out = lab.check_output(
        "a",
        "cat /run/cloud-init/cloud-id; "
        "cat /sys/class/dmi/id/sys_vendor /sys/class/dmi/id/product_serial;"
        " ip -br link show eth0",
    )
    cloud_id, vendor, serial, link = out.splitlines()
    assert (
        cloud_id == "hetzner"
        and vendor == "Hetzner"
        and serial == str(lab.server("a")["id"])
    )
    assert "eth0" in link
    assert lab.check_output("a", "cat /sys/class/net/eth0/mtu") == "1500"
    assert (
        lab.check_output(
            "a", "cat /sys/class/net/$(ip -br a | awk '/^en/ {print $1; exit}')/mtu"
        )
        == "1450"
    )
    # metadata over IPv6 link-local, as cloud-init tries first
    assert (
        lab.check_output(
            "a", "curl -s -g http://[fe80::a9fe:a9fe%eth0]/hetzner/v1/metadata/hostname"
        )
        == "a"
    )


def test_primary_ip_moves_to_another_server(lab: LiveLab):
    x1, x2 = (
        lab.create("x1"),
        lab.create("x2", public_net={"enable_ipv4": True, "enable_ipv6": False}),
    )
    ip = x1["public_net"]["ipv4"]
    try:
        for x in (x1, x2):
            lab.wait_action(
                lab.api("POST", f"/servers/{x['id']}/actions/poweroff")["action"]
            )
        lab.wait_action(
            lab.api("POST", f"/primary_ips/{ip['id']}/actions/unassign")["action"]
        )
        own = x2["public_net"]["ipv4"]
        lab.wait_action(
            lab.api("POST", f"/primary_ips/{own['id']}/actions/unassign")["action"]
        )
        lab.wait_action(
            lab.api(
                "POST",
                f"/primary_ips/{ip['id']}/actions/assign",
                {"assignee_id": x2["id"], "assignee_type": "server"},
            )["action"]
        )
        lab.wait_action(
            lab.api("POST", f"/servers/{x2['id']}/actions/poweron")["action"]
        )
        lab.wait_ssh("x2")
        # DHCP hands out the moved address
        assert f"{ip['ip']}/32" in lab.check_output("x2", "ip -br a show eth0")
        assert lab.server("x1")["public_net"]["ipv4"] is None
    finally:
        lab.delete("x1")
        lab.delete("x2")
        for p in lab.api("GET", "/primary_ips")["primary_ips"]:
            if p["assignee_id"] is None:
                lab.api("DELETE", f"/primary_ips/{p['id']}")


def test_rebuild_gives_a_fresh_system(lab: LiveLab):
    server = lab.create("x3")
    try:
        lab.check_output("x3", "echo from-x3 > /root/marker; sync")
        lab.wait_action(
            lab.api(
                "POST",
                f"/servers/{server['id']}/actions/rebuild",
                {"image": "debian-13"},
            )["action"],
            timeout=300,
        )
        lab.wait_ssh("x3")
        assert lab.run("x3", "test -e /root/marker").returncode == 1  # a fresh system
    finally:
        lab.delete("x3")


def test_snapshot_survives_export_delete_and_import(lab: LiveLab, tmp_path):
    lab.create("x4")
    file = str(tmp_path / "x4.qcow2")
    try:
        lab.check_output("x4", "echo from-x4 > /root/marker; sync")
        answer = lab.api(
            "POST",
            f"/servers/{lab.server('x4')['id']}/actions/create_image",
            {"description": "x4"},
        )
        lab.wait_action(answer["action"], timeout=300)
        snapshot_id = answer["image"]["id"]
        subprocess.run(["hlab", "image", "export", str(snapshot_id), file], check=True)
        lab.api("DELETE", f"/images/{snapshot_id}")
        imported = subprocess.run(
            ["hlab", "image", "import", file, "--label", "role=prepared"],
            check=True,
            capture_output=True,
            text=True,
        )
        image_id = int(imported.stdout)
        listed = lab.api("GET", "/images?type=snapshot&label_selector=role=prepared")
        assert [image["id"] for image in listed["images"]] == [image_id]
        lab.create("x5", image=image_id)
        assert lab.check_output("x5", "cat /root/marker") == "from-x4"
        lab.delete("x5")
        lab.api("DELETE", f"/images/{image_id}")
    finally:
        lab.delete("x4")


def test_volume_automounts_hot_plugs_moves_and_grows(lab: LiveLab):
    x6, x7 = lab.create("x6"), lab.create("x7")
    response = lab.api(
        "POST",
        "/volumes",
        {
            "name": "data",
            "size": 10,
            "server": x6["id"],
            "automount": True,
            "format": "ext4",
        },
    )
    volume = response["volume"]
    lab.wait_action(response["action"], timeout=120)
    try:
        path = f"/mnt/HC_Volume_{volume['id']}"
        for _ in range(30):
            if lab.run("x6", f"mountpoint -q {path}").returncode == 0:
                break
            time.sleep(1)
        assert lab.check_output(
            "x6",
            f"ls -l {volume['linux_device']} && "
            f"echo hi > {path}/f && sync && cat {path}/f",
        ).endswith("hi")
        lab.check_output("x6", f"umount {path}")
        lab.wait_action(
            lab.api("POST", f"/volumes/{volume['id']}/actions/detach")["action"]
        )
        assert lab.run("x6", f"test -e {volume['linux_device']}").returncode == 1
        lab.wait_action(
            lab.api(
                "POST",
                f"/volumes/{volume['id']}/actions/attach",
                {"server": x7["id"], "automount": True},
            )["action"]
        )
        for _ in range(30):
            if lab.run("x7", f"cat {path}/f").stdout.strip() == "hi":
                break
            time.sleep(1)
        assert lab.check_output("x7", f"cat {path}/f") == "hi"
        lab.wait_action(
            lab.api("POST", f"/volumes/{volume['id']}/actions/resize", {"size": 20})[
                "action"
            ]
        )
        size = lab.check_output("x7", f"blockdev --getsize64 {volume['linux_device']}")
        assert int(size) == 20 * 2**30
    finally:
        lab.run("x7", f"umount /mnt/HC_Volume_{volume['id']}")
        lab.wait_action(
            lab.api("POST", f"/volumes/{volume['id']}/actions/detach")["action"]
        )
        lab.api("DELETE", f"/volumes/{volume['id']}")
        lab.delete("x6")
        lab.delete("x7")


def test_reset_password_changes_the_root_password(lab: LiveLab):
    lab.check_output(
        "a",
        "for i in $(seq 1 120); do "
        "systemctl is-active --quiet qemu-guest-agent && exit 0; sleep 2; "
        "done; exit 1",
        timeout=300,
    )
    before = lab.check_output("a", "grep ^root: /etc/shadow | cut -d: -f2")
    response = lab.api(
        "POST", f"/servers/{lab.server('a')['id']}/actions/reset_password"
    )
    lab.wait_action(response["action"])
    assert lab.check_output("a", "grep ^root: /etc/shadow | cut -d: -f2") != before


def test_ubuntu_image_boots_and_joins_a_network(lab: LiveLab):
    server = lab.create("x8", image="ubuntu-26.04", networks=[lab.nets["one"]])
    try:
        assert "Ubuntu 26.04" in lab.check_output("x8", "cat /etc/os-release")
        assert lab.check_output("x8", "cat /run/cloud-init/cloud-id") == "hetzner"
        priv = server["private_net"][0]["ip"]
        for _ in range(30):
            if priv in lab.run("x8", "ip -br a").stdout:
                break
            time.sleep(1)
        assert ping(lab, "x8", "10.0.1.1") is not None
    finally:
        lab.delete("x8")


# --- power (run last, see conftest.py: these disturb a, b and c) ----------------------


@pytest.mark.disturbs_servers
def test_poweroff_and_poweron_keep_the_routes(lab: LiveLab):
    t0 = time.time()
    lab.server_action("b", "poweroff")
    assert 8 <= time.time() - t0 < 20
    assert lab.server("b")["status"] == "off"
    assert probe(lab, "a", "tcp", B_PRIV) == ""
    lab.server_action("b", "poweron")
    assert lab.server("b")["status"] == "running"
    lab.wait_ssh("b")
    lab.start_echo("b")
    # IPv6 route survives the power cycle
    assert probe(lab, "client:alice", "tcp", B6) == f"b tcp {ALICE6}"
    assert probe(lab, "a", "tcp", B_PRIV) == f"b tcp {A_PRIV}"


@pytest.mark.disturbs_servers
def test_server_is_locked_while_an_action_runs(lab: LiveLab):
    sid = lab.server("b")["id"]
    action = lab.api("POST", f"/servers/{sid}/actions/poweroff")["action"]
    response = lab.http.post(f"/servers/{sid}/actions/poweron")
    assert response.status_code == 423
    lab.wait_action(action)
    lab.server_action("b", "poweron")
    lab.wait_ssh("b")


@pytest.mark.disturbs_servers
def test_dead_hardware_keeps_the_server_off(lab: LiveLab):
    lab.server_action("b", "poweroff")
    lab.control("POST", "/vm/b/dead")
    lab.server_action("b", "poweron")
    assert lab.server("b")["status"] == "off"
    lab.control("POST", "/vm/b/alive")
    lab.server_action("b", "poweron")
    lab.wait_ssh("b")


@pytest.mark.disturbs_servers
def test_shutdown_is_graceful(lab: LiveLab):
    lab.check_output("b", "cloud-init status --wait >/dev/null; sync")
    # done once the button is pressed; off follows when the guest is down
    lab.server_action("b", "shutdown")
    for _ in range(60):
        if lab.server("b")["status"] == "off":
            break
        time.sleep(1)
    assert lab.server("b")["status"] == "off"
    console = subprocess.run(
        ["sh", "-c", f"tail -40 /hlab/vm/{lab.server('b')['id']}/console.log"],
        capture_output=True,
        text=True,
    ).stdout
    assert "reboot: Power down" in console or "Power-Off" in console
    lab.server_action("b", "poweron")
    lab.wait_ssh("b")


@pytest.mark.disturbs_servers
def test_reset_reboots(lab: LiveLab):
    boot0 = lab.check_output("a", "cat /proc/sys/kernel/random/boot_id")
    lab.server_action("a", "reset")
    lab.wait_ssh("a")
    assert lab.check_output("a", "cat /proc/sys/kernel/random/boot_id") != boot0


@pytest.mark.disturbs_servers
def test_delete_cleans_up(lab: LiveLab):
    sid = lab.server("c")["id"]
    lab.wait_action(lab.api("DELETE", f"/servers/{sid}")["action"])
    links = subprocess.run(["ip", "-br", "link"], capture_output=True, text=True).stdout
    routes = subprocess.run(
        ["ip", "-6", "route"], capture_output=True, text=True
    ).stdout
    assert "pub3 " not in links and "prv3x1" not in links
    assert "2001:db8:3::/64" not in routes
    assert lab.http.get(f"/servers/{sid}").status_code == 404
