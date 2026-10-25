"""Floating IPs, primary IPs, IPv6 and reverse DNS."""

from conftest import create_server, dhcp, metadata, network, off, wait_action


def test_floating_ip_follows_server(api):
    s1 = create_server(api, "a")["server"]
    s2 = create_server(api, "b")["server"]
    out = api.post("/v1/floating_ips", json={"type": "ipv4", "home_location": "fsn1", "name": "entry"}).json()
    f = out["floating_ip"]
    assert f["ip"] == "203.0.113.11"
    wait_action(api, api.post(f"/v1/floating_ips/{f['id']}/actions/assign", json={"server": s1["id"]}).json()["action"])
    assert ("route", "203.0.113.11/32", "pub1") in api.sh.calls
    wait_action(api, api.post(f"/v1/floating_ips/{f['id']}/actions/assign", json={"server": s2["id"]}).json()["action"])
    assert ("route", "203.0.113.11/32", "pub2") in api.sh.calls
    assert api.get(f"/v1/servers/{s2['id']}").json()["server"]["public_net"]["floating_ips"] == [f["id"]]
    # a power cycle keeps the route
    api.sh.calls.clear()
    wait_action(api, api.post(f"/v1/servers/{s2['id']}/actions/poweroff").json()["action"])
    wait_action(api, api.post(f"/v1/servers/{s2['id']}/actions/poweron").json()["action"])
    assert ("route", "203.0.113.11/32", "pub2") in api.sh.calls


def test_hetzner_rules_for_ips_firewalls_and_actions(api):
    s = create_server(api, "a", labels={"web": "1"})["server"]
    # a Floating IP is unassigned before it is deleted (Hetzner since 1 May 2026)
    f = api.post("/v1/floating_ips", json={"type": "ipv6", "server": s["id"]}).json()
    wait_action(api, f["action"])
    fid = f["floating_ip"]["id"]
    r = api.delete(f"/v1/floating_ips/{fid}")
    assert r.status_code == 422 and r.json()["error"]["code"] == "must_be_unassigned"
    # reverse DNS per address of an IPv6 /64
    net = f["floating_ip"]["ip"].split("/")[0]
    for host in ("1", "2"):
        api.post(f"/v1/floating_ips/{fid}/actions/change_dns_ptr", json={"ip": net + host, "dns_ptr": f"h{host}.test"})
    ptrs = api.get(f"/v1/floating_ips/{fid}").json()["floating_ip"]["dns_ptr"]
    assert sorted(p["dns_ptr"] for p in ptrs) == ["h1.test", "h2.test"]
    # firewalls: a server reached through a label selector cannot be removed by hand
    fw = api.post("/v1/firewalls", json={"name": "fw", "apply_to": [
        {"type": "label_selector", "label_selector": {"selector": "web=1"}}]}).json()["firewall"]
    r = api.post(f"/v1/firewalls/{fw['id']}/actions/remove_from_resources",
                 json={"remove_from": [{"type": "server", "server": {"id": s["id"]}}]})
    assert r.json()["error"]["code"] == "firewall_managed_by_label_selector"
    r = api.post(f"/v1/firewalls/{fw['id']}/actions/apply_to_resources",
                 json={"apply_to": [{"type": "server", "server": {"id": 9999}}]})
    assert r.status_code == 404 and r.json()["error"]["code"] == "firewall_resource_not_found"
    r = api.post("/v1/firewalls", json={"name": "twice", "apply_to": [{"type": "server", "server": {"id": s["id"]}}] * 2})
    assert r.json()["error"]["code"] == "server_already_added"
    # protection: delete and rebuild together
    r = api.post(f"/v1/servers/{s['id']}/actions/change_protection", json={"delete": True, "rebuild": False})
    assert r.status_code == 422
    # action lists per type, not for the whole project
    assert api.get("/v1/actions").status_code == 422
    acts = api.get("/v1/servers/actions").json()["actions"]
    assert acts and all(any(x["type"] == "server" for x in a["resources"]) for a in acts)
    assert api.get("/v1/servers/9999/actions").status_code == 404
    assert api.get("/v1/servers?page=abc&per_page=x").json()["meta"]["pagination"]["per_page"] == 25


def test_primary_ip_lifecycle(api, tmp_path):
    s = create_server(api, "a")["server"]
    sid, ip4 = s["id"], s["public_net"]["ipv4"]
    r = api.post("/v1/primary_ips", json={"name": "spare", "type": "ipv4", "assignee_type": "server",
                                          "location": "fsn1", "auto_delete": False})
    assert r.status_code == 201, r.text
    spare = r.json()["primary_ip"]
    assert spare["assignee_id"] is None and spare["ip"] == "198.51.100.12"
    # assigning needs the server off and without an IPv4
    assert api.post(f"/v1/primary_ips/{spare['id']}/actions/assign",
                    json={"assignee_id": sid, "assignee_type": "server"}).status_code == 422   # server_not_stopped
    off(api, sid)
    r = api.post(f"/v1/primary_ips/{spare['id']}/actions/assign", json={"assignee_id": sid, "assignee_type": "server"})
    assert r.json()["error"]["code"] == "server_has_ipv4"
    api.post(f"/v1/primary_ips/{ip4['id']}/actions/unassign")
    assert api.get(f"/v1/servers/{sid}").json()["server"]["public_net"]["ipv4"] is None
    assert api.delete(f"/v1/primary_ips/{spare['id']}").status_code == 204
    # the old one moves back; DHCP hands it out again
    api.post(f"/v1/primary_ips/{ip4['id']}/actions/assign", json={"assignee_id": sid, "assignee_type": "server"})
    assert api.get(f"/v1/servers/{sid}").json()["server"]["public_net"]["ipv4"]["ip"] == ip4["ip"]
    assert f",{ip4['ip']}" in dhcp(tmp_path)
    api.post(f"/v1/primary_ips/{ip4['id']}/actions/change_dns_ptr", json={"ip": ip4["ip"], "dns_ptr": "web.example.com"})
    assert api.get(f"/v1/servers/{sid}").json()["server"]["public_net"]["ipv4"]["dns_ptr"] == "web.example.com"


def test_server_with_given_primary_ip_keeps_it_after_delete(api):
    p = api.post("/v1/primary_ips", json={"name": "keep", "type": "ipv4", "assignee_type": "server",
                                          "location": "nbg1"}).json()["primary_ip"]
    r = api.post("/v1/servers", json={"name": "x", "server_type": "cx23", "image": "debian-13", "location": "fsn1",
                                      "public_net": {"ipv4": p["id"]}})
    assert r.json()["error"]["code"] == "primary_ip_datacenter_mismatch"
    s = create_server(api, "y", location="nbg1", public_net={"ipv4": p["id"], "enable_ipv6": False})["server"]
    assert s["public_net"]["ipv4"]["id"] == p["id"] and s["public_net"]["ipv6"] is None
    wait_action(api, api.delete(f"/v1/servers/{s['id']}").json()["action"])
    left = api.get("/v1/primary_ips").json()["primary_ips"]
    assert [(x["id"], x["assignee_id"]) for x in left] == [(p["id"], None)]  # auto_delete IPs went with the server


def test_primary_ip_and_name_rules(api):
    p = api.post("/v1/primary_ips", json={"name": "p", "type": "ipv4", "assignee_type": "server",
                                          "location": "fsn1", "auto_delete": True}).json()["primary_ip"]
    assert api.post(f"/v1/primary_ips/{p['id']}/actions/change_protection", json={"delete": True}).status_code == 422
    q = api.post("/v1/primary_ips", json={"name": "q", "type": "ipv4", "assignee_type": "server",
                                          "location": "fsn1"}).json()["primary_ip"]
    assert api.put(f"/v1/primary_ips/{q['id']}", json={"name": "p"}).status_code == 409
    api.post("/v1/floating_ips", json={"type": "ipv4", "home_location": "fsn1", "name": "f"})
    assert api.post("/v1/floating_ips", json={"type": "ipv4", "home_location": "fsn1", "name": "f"}).status_code == 409
    api.post("/v1/firewalls", json={"name": "fw1"})
    fw2 = api.post("/v1/firewalls", json={"name": "fw2"}).json()["firewall"]
    assert api.put(f"/v1/firewalls/{fw2['id']}", json={"name": "fw1"}).status_code == 409


def test_unassigned_primary_ip(api):
    p = api.post("/v1/primary_ips", json={"name": "p", "type": "ipv4", "assignee_type": "server",
                                          "location": "fsn1", "auto_delete": False}).json()["primary_ip"]
    assert p["assignee_type"] == "unassigned" and p["assignee_id"] is None
    assert p["dns_ptr"][0]["dns_ptr"] == "static." + ".".join(reversed(p["ip"].split("."))) + ".clients.your-server.de"


def test_reverse_dns_and_primary_ips(api):
    s = create_server(api, "a")["server"]
    r = api.post(f"/v1/servers/{s['id']}/actions/change_dns_ptr",
                 json={"ip": s["public_net"]["ipv4"]["ip"], "dns_ptr": "a.example.com."})
    assert r.status_code == 422
    r = api.post("/v1/primary_ips", json={"name": "p", "type": "ipv4", "assignee_type": "server",
                                          "location": "fsn1", "assignee_id": s["id"]})
    assert r.status_code == 422   # location and assignee_id exclude each other
    r = api.post("/v1/primary_ips", json={"name": "p", "type": "ipv4", "assignee_type": "server", "location": "fsn1"})
    assert r.status_code == 201 and r.json()["action"] is None


def test_ipv6_server_and_routes(api, tmp_path):
    s = create_server(api, "v6")["server"]
    assert s["public_net"]["ipv6"]["ip"] == "2001:db8:1::/64"
    assert ("route6", "2001:db8:1::/64", "pub1", "fe80::9400:ff:fe00:1") in api.sh.calls   # via its link-local address
    net = metadata(api, "198.51.100.11")["network-config"]
    subnets = net["config"][0]["subnets"]
    assert {"type": "static", "ipv6": True, "address": "2001:db8:1::1/64", "gateway": "fe80::1"} in subnets
    assert {"type": "dhcp", "ipv4": True} in subnets

    s2 = create_server(api, "v4only", public_net={"enable_ipv4": True, "enable_ipv6": False})["server"]
    assert s2["public_net"]["ipv6"] is None
    subnets = metadata(api, s2["public_net"]["ipv4"]["ip"])["network-config"]["config"][0]["subnets"]
    assert subnets == [{"type": "dhcp", "ipv4": True}]


def test_floating_ipv6_is_routed(api):
    s = create_server(api, "f6")["server"]
    f = api.post("/v1/floating_ips", json={"type": "ipv6", "home_location": "fsn1"}).json()["floating_ip"]
    assert f["ip"] == "2001:db8:ff01::/64"
    wait_action(api, api.post(f"/v1/floating_ips/{f['id']}/actions/assign", json={"server": s["id"]}).json()["action"])
    assert ("route6", "2001:db8:ff01::/64", "pub1", "fe80::9400:ff:fe00:1") in api.sh.calls
    wait_action(api, api.post(f"/v1/floating_ips/{f['id']}/actions/unassign").json()["action"])
    assert ("route", "2001:db8:ff01::/64") in api.sh.calls
