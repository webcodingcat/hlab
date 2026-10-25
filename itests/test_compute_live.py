"""Live tests of what surrounds a server: boot like Hetzner, primary IPs, rebuild, volumes, routes, password reset,
other system images. They bring their own servers (x*) and leave a, b and c alone."""

import time

from livelab import Lab, ping, probe


def test_boots_through_hetzners_datasource(lab: Lab):
    out = lab.sh("a", "cat /run/cloud-init/cloud-id; cat /sys/class/dmi/id/sys_vendor /sys/class/dmi/id/product_serial;"
                      " ip -br link show eth0")
    cloud_id, vendor, serial, link = out.splitlines()
    assert cloud_id == "hetzner" and vendor == "Hetzner" and serial == str(lab.server("a")["id"])
    assert "eth0" in link
    assert lab.sh("a", "cat /sys/class/net/eth0/mtu") == "1500"
    assert lab.sh("a", "cat /sys/class/net/$(ip -br a | awk '/^en/ {print $1; exit}')/mtu") == "1450"
    # metadata over IPv6 link-local, as cloud-init tries first
    assert lab.sh("a", "curl -s -g http://[fe80::a9fe:a9fe%eth0]/hetzner/v1/metadata/hostname") == "a"


def test_primary_ip_moves_to_another_server(lab: Lab):
    x1, x2 = lab.create("x1"), lab.create("x2", public_net={"enable_ipv4": True, "enable_ipv6": False})
    ip = x1["public_net"]["ipv4"]
    try:
        for x in (x1, x2):
            lab.wait_action(lab.api("POST", f"/servers/{x['id']}/actions/poweroff")["action"])
        lab.wait_action(lab.api("POST", f"/primary_ips/{ip['id']}/actions/unassign")["action"])
        own = x2["public_net"]["ipv4"]
        lab.wait_action(lab.api("POST", f"/primary_ips/{own['id']}/actions/unassign")["action"])
        lab.wait_action(lab.api("POST", f"/primary_ips/{ip['id']}/actions/assign",
                                {"assignee_id": x2["id"], "assignee_type": "server"})["action"])
        lab.wait_action(lab.api("POST", f"/servers/{x2['id']}/actions/poweron")["action"])
        lab.wait_ssh("x2")
        assert f"{ip['ip']}/32" in lab.sh("x2", "ip -br a show eth0")   # DHCP hands out the moved address
        assert lab.server("x1")["public_net"]["ipv4"] is None
    finally:
        lab.delete("x1")
        lab.delete("x2")
        for p in lab.api("GET", "/primary_ips")["primary_ips"]:
            if p["assignee_id"] is None:
                lab.api("DELETE", f"/primary_ips/{p['id']}")


def test_rebuild(lab: Lab):
    x = lab.create("x3")
    try:
        lab.sh("x3", "echo from-x3 > /root/marker; sync")
        lab.wait_action(lab.api("POST", f"/servers/{x['id']}/actions/rebuild", {"image": "debian-13"})["action"],
                        timeout=300)
        lab.wait_ssh("x3")
        assert lab.ssh("x3", "test -e /root/marker").returncode == 1   # a fresh system
    finally:
        lab.delete("x3")


def test_volume_automount_hotplug_and_move(lab: Lab):
    x6, x7 = lab.create("x6"), lab.create("x7")
    r = lab.api("POST", "/volumes", {"name": "data", "size": 10, "server": x6["id"], "automount": True,
                                     "format": "ext4"})
    v = r["volume"]
    lab.wait_action(r["action"], timeout=120)
    try:
        path = f"/mnt/HC_Volume_{v['id']}"
        for _ in range(30):
            if lab.ssh("x6", f"mountpoint -q {path}").returncode == 0:
                break
            time.sleep(1)
        assert lab.sh("x6", f"ls -l {v['linux_device']} && echo hi > {path}/f && sync && cat {path}/f").endswith("hi")
        lab.sh("x6", f"umount {path}")
        lab.wait_action(lab.api("POST", f"/volumes/{v['id']}/actions/detach")["action"])
        assert lab.ssh("x6", f"test -e {v['linux_device']}").returncode == 1
        lab.wait_action(lab.api("POST", f"/volumes/{v['id']}/actions/attach",
                                {"server": x7["id"], "automount": True})["action"])
        for _ in range(30):
            if lab.ssh("x7", f"cat {path}/f").stdout.strip() == "hi":
                break
            time.sleep(1)
        assert lab.sh("x7", f"cat {path}/f") == "hi"
        lab.wait_action(lab.api("POST", f"/volumes/{v['id']}/actions/resize", {"size": 20})["action"])
        size = lab.sh("x7", f"blockdev --getsize64 {v['linux_device']}")
        assert int(size) == 20 * 2**30
    finally:
        lab.ssh("x7", f"umount /mnt/HC_Volume_{v['id']}")
        lab.wait_action(lab.api("POST", f"/volumes/{v['id']}/actions/detach")["action"])
        lab.api("DELETE", f"/volumes/{v['id']}")
        lab.delete("x6")
        lab.delete("x7")


def test_network_route_through_a_server(lab: Lab):
    net = lab.nets["one"]
    b_priv = next(p["ip"] for p in lab.server("b")["private_net"] if p["network"] == net)
    lab.sh("b", "ip addr add 10.100.0.1/32 dev lo")
    lab.wait_action(lab.api("POST", f"/networks/{net}/actions/add_route",
                            {"destination": "10.100.0.0/24", "gateway": b_priv})["action"])
    try:
        iface = lab.sh("a", "ip -br a | awk '/10.0.1.1\\// {print $1}'")
        lab.sh("a", f"networkctl renew {iface}; sleep 3")          # the route arrives by DHCP
        assert "10.100.0.0/24 via 10.0.0.1" in lab.sh("a", "ip r")
        assert ping(lab, "a", "10.100.0.1") is not None
    finally:
        lab.api("POST", f"/networks/{net}/actions/delete_route", {"destination": "10.100.0.0/24", "gateway": b_priv})
        lab.ssh("b", "ip addr del 10.100.0.1/32 dev lo")


def test_reset_password(lab: Lab):
    lab.sh("a", "for i in $(seq 1 120); do systemctl is-active --quiet qemu-guest-agent && exit 0; sleep 2; done; exit 1",
           timeout=300)
    before = lab.sh("a", "grep ^root: /etc/shadow | cut -d: -f2")
    r = lab.api("POST", f"/servers/{lab.server('a')['id']}/actions/reset_password")
    lab.wait_action(r["action"])
    assert lab.sh("a", "grep ^root: /etc/shadow | cut -d: -f2") != before


def test_ubuntu_image(lab: Lab):
    x = lab.create("x8", image="ubuntu-26.04", networks=[lab.nets["one"]])
    try:
        assert "Ubuntu 26.04" in lab.sh("x8", "cat /etc/os-release")
        assert lab.sh("x8", "cat /run/cloud-init/cloud-id") == "hetzner"
        priv = x["private_net"][0]["ip"]
        for _ in range(30):
            if priv in lab.ssh("x8", "ip -br a").stdout:
                break
            time.sleep(1)
        assert ping(lab, "x8", "10.0.1.1") is not None
    finally:
        lab.delete("x8")


def test_overlapping_networks(lab: Lab):
    """A second network with the range of "one" (allowed on Hetzner): its servers get a's and b's private IPs, both
    networks work side by side and stay apart, and removing the twins leaves a and b alone."""
    nid = lab.api("POST", "/networks", {"name": "one-again", "ip_range": "10.0.0.0/16", "subnets": [
        {"type": "cloud", "ip_range": "10.0.1.0/24", "network_zone": "eu-central"}]})["network"]["id"]
    try:
        x9, x10 = lab.create("x9", networks=[nid]), lab.create("x10", networks=[nid], location="nbg1")
        assert [x["private_net"][0]["ip"] for x in (x9, x10)] == ["10.0.1.1", "10.0.1.2"]
        for n, ip in (("x9", "10.0.1.1"), ("x10", "10.0.1.2")):
            for _ in range(30):
                if f"{ip}/32" in lab.ssh(n, "ip -br a").stdout:
                    break
                time.sleep(1)
            assert f"{ip}/32" in lab.sh(n, "ip -br a")   # DHCP hands out the same address in both networks
        lab.start_echo("x9")
        assert probe(lab, "x10", "tcp", "10.0.1.1") == "x9 tcp 10.0.1.2"
        assert probe(lab, "b", "tcp", "10.0.1.1") == "a tcp 10.0.1.2"
    finally:
        lab.delete("x9")
        lab.delete("x10")
        lab.api("DELETE", f"/networks/{nid}")
    assert probe(lab, "b", "tcp", "10.0.1.1") == "a tcp 10.0.1.2"   # a's and b's routes survived the twins
