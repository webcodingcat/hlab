"""Cloud networks: subnets, routes, alias IPs, attach and detach, overlapping networks."""

from conftest import create_server, dhcp, create_network, nft_set, wait_action


def test_networks_and_dhcp(api, tmp_path):
    network = api.post("/v1/networks", json={"name": "net", "ip_range": "10.0.0.0/16",
                                             "subnets": [{"type": "cloud", "ip_range": "10.0.1.0/24",
                                                          "network_zone": "eu-central"}]}).json()["network"]
    assert network["subnets"][0]["gateway"] == "10.0.0.1"
    s1 = create_server(api, "a", networks=[network["id"]])["server"]
    assert s1["private_net"][0]["ip"] == "10.0.1.1"
    s2 = create_server(api, "b")["server"]
    action = api.post(f"/v1/servers/{s2['id']}/actions/attach_to_network", json={"network": network["id"], "ip": "10.0.1.20"})
    wait_action(api, action.json()["action"])
    s2 = api.get(f"/v1/servers/{s2['id']}").json()["server"]
    assert s2["private_net"][0]["ip"] == "10.0.1.20"
    assert api.sh.commands("nic-add")
    hosts = dhcp(tmp_path)
    assert "10.0.1.1" in hosts and "10.0.1.20" in hosts
    conf = dhcp(tmp_path)
    assert "121,10.0.0.1/32,0.0.0.0,10.0.0.0/16,10.0.0.1" in conf
    assert sorted(api.get(f"/v1/networks/{network['id']}").json()["network"]["servers"]) == sorted([s1["id"], s2["id"]])


def test_overlapping_networks_stay_apart(api, tmp_path):
    """Two networks with the same range (allowed on Hetzner): the same private IP twice, each in its own table."""
    body = {"ip_range": "10.0.0.0/16", "subnets": [{"type": "cloud", "ip_range": "10.0.1.0/24",
                                                    "network_zone": "eu-central"}]}
    n1 = api.post("/v1/networks", json={"name": "one", **body}).json()["network"]
    n2 = api.post("/v1/networks", json={"name": "two", **body}).json()["network"]
    s1 = create_server(api, "a", networks=[n1["id"]])["server"]
    s2 = create_server(api, "b", networks=[n2["id"]])["server"]
    assert s1["private_net"][0]["ip"] == s2["private_net"][0]["ip"] == "10.0.1.1"
    ups = api.sh.commands("private-tap-up")
    assert ("private-tap-up", "prv1x1", "10.0.0.1/16", "10.0.1.1", str(n1["id"]), "10.0.0.0/16") in ups
    assert ("private-tap-up", "prv2x1", "10.0.0.1/16", "10.0.1.1", str(n2["id"]), "10.0.0.0/16") in ups
    # one dnsmasq per tap, so the address can be handed out on both
    for tap in ("prv1x1", "prv2x1"):
        conf = (tmp_path / "dhcp" / f"{tap}.conf").read_text()
        assert f"interface={tap}\n" in conf and conf.count("dhcp-host=") == 1 and ",10.0.1.1\n" in conf
    # a server's private address counts for latency only on taps of the same network
    lab = api.lab
    assert lab.fabric.source_prefixes(lab.state["servers"][s1["id"]], n2["id"]) == []
    assert lab.fabric.source_prefixes(lab.state["servers"][s1["id"]], n1["id"]) == ["10.0.1.1/32"]


def test_deleting_a_network_drops_its_table(api):
    network = api.post("/v1/networks", json={"name": "gone", "ip_range": "10.5.0.0/16"}).json()["network"]
    assert api.delete(f"/v1/networks/{network['id']}").status_code == 204
    assert ("route-flush", str(network["id"])) in api.sh.calls


def test_network_values_are_checked_like_hetzner(api):
    def net(**body):
        return api.post("/v1/networks", json={"name": "n", "ip_range": "10.0.0.0/16", **body})

    assert net(ip_range="198.51.100.0/24").status_code == 422          # not RFC 1918
    assert net(ip_range="10.0.0.0/25").status_code == 422              # smaller than /24
    assert net(subnets=[{"type": "cloud", "ip_range": "10.0.1.0/24", "network_zone": "eu-central"},
                        {"type": "cloud", "ip_range": "10.0.1.0/25", "network_zone": "eu-central"}]).status_code == 422
    assert not api.get("/v1/networks").json()["networks"]               # nothing created by the failures
    network = net(subnets=[{"type": "cloud", "ip_range": "10.0.1.0/24", "network_zone": "eu-central"}]).json()["network"]

    def act(cmd, **body):
        return api.post(f"/v1/networks/{network['id']}/actions/{cmd}", json=body)

    # a destination that would add a line to dnsmasq's configuration, other bad routes
    assert act("add_route", destination="10.9.0.0/16\ndhcp-script=/x", gateway="10.0.1.5").status_code == 422
    assert act("add_route", destination="8.8.8.0/24", gateway="10.0.1.5").status_code == 422
    assert act("add_route", destination="10.0.1.0/25", gateway="10.0.1.5").status_code == 422   # overlaps a subnet
    assert act("add_route", destination="10.9.0.0/16", gateway="10.0.0.1").status_code == 422   # the network's own gateway
    assert act("add_route", destination="10.9.0.0/16", gateway="10.0.1.5").status_code == 201
    assert act("change_ip_range", ip_range="10.0.0.0/17").status_code == 422   # shrinking
    assert act("change_ip_range", ip_range="10.1.0.0/15").status_code == 422   # another base address
    assert act("change_ip_range", ip_range="10.0.0.0/15").status_code == 201
    assert ("route-flush", str(network["id"])) in api.sh.calls                     # the table is set anew


def test_deleting_a_network_detaches_its_servers(api):
    network = api.post("/v1/networks", json={"name": "n", "ip_range": "10.0.0.0/16", "subnets": [
        {"type": "cloud", "ip_range": "10.0.1.0/24", "network_zone": "eu-central"}]}).json()["network"]
    server = create_server(api, "a", networks=[network["id"]])["server"]
    assert api.post(f"/v1/networks/{network['id']}/actions/delete_subnet", json={"ip_range": "10.0.1.0/24"}).status_code == 422
    assert api.delete(f"/v1/networks/{network['id']}").status_code == 204
    assert api.get(f"/v1/servers/{server['id']}").json()["server"]["private_net"] == []
    assert ("tap-down", "prv1x1") in api.sh.calls


def test_network_members_and_overlap_on_one_server(api):
    body = {"ip_range": "10.0.0.0/16", "subnets": [{"type": "cloud", "ip_range": "10.0.1.0/24",
                                                    "network_zone": "eu-central"}]}
    n1 = api.post("/v1/networks", json={"name": "one", **body}).json()["network"]
    n2 = api.post("/v1/networks", json={"name": "two", **body}).json()["network"]
    server = create_server(api, "a", networks=[n1["id"]])["server"]
    response = api.post(f"/v1/servers/{server['id']}/actions/attach_to_network", json={"network": n2["id"]})
    assert response.json()["error"]["code"] == "networks_overlap"
    members = api.get(f"/v1/networks/{n1['id']}/members").json()["members"]
    assert members == [{"type": "server", "id": server["id"], "ip": "10.0.1.1", "status": "ok", "alias_ips": [],
                        "subnet": "10.0.1.0/24"}]


def test_alias_ips_are_checked_and_reserved(api):
    network = api.post("/v1/networks", json={"name": "n", "ip_range": "10.0.0.0/16", "subnets": [
        {"type": "cloud", "ip_range": "10.0.1.0/24", "network_zone": "eu-central"}]}).json()["network"]
    server_a = create_server(api, "a", networks=[network["id"]])["server"]
    response = api.post(f"/v1/servers/{server_a['id']}/actions/change_alias_ips", json={"network": network["id"], "alias_ips": ["nope"]})
    assert response.status_code == 422
    wait_action(api, api.post(f"/v1/servers/{server_a['id']}/actions/change_alias_ips",
                              json={"network": network["id"], "alias_ips": ["10.0.1.2"]}).json()["action"])
    server_b = create_server(api, "b", networks=[network["id"]])["server"]
    assert server_b["private_net"][0]["ip"] == "10.0.1.3"                    # 10.0.1.2 is a's alias
    tap_ups = [c for c in api.sh.calls if c[:2] == ("private-tap-up", "prv1x1")]
    assert tap_ups[-1][-1] == "10.0.1.2"                               # routed to a


def test_failed_attach_takes_its_nic_back(api):
    network = api.post("/v1/networks", json={"name": "n", "ip_range": "10.0.0.0/16", "subnets": [
        {"type": "cloud", "ip_range": "10.0.1.0/24", "network_zone": "eu-central"}]}).json()["network"]
    server = create_server(api, "a")["server"]
    api.sh.fail.add("nic-add")
    action = api.post(f"/v1/servers/{server['id']}/actions/attach_to_network", json={"network": network["id"]}).json()["action"]
    assert wait_action(api, action)["status"] == "error"
    assert api.get(f"/v1/servers/{server['id']}").json()["server"]["private_net"] == []


def test_networks_per_server(api):
    server = create_server(api, "a")["server"]
    for i in range(4):
        network = api.post("/v1/networks", json={"name": f"n{i}", "ip_range": f"10.{i}.0.0/16", "subnets": [
            {"type": "cloud", "ip_range": f"10.{i}.1.0/24", "network_zone": "eu-central"}]}).json()["network"]
        response = api.post(f"/v1/servers/{server['id']}/actions/attach_to_network", json={"network": network["id"]})
        if i < 3:
            wait_action(api, response.json()["action"])
        else:
            assert response.status_code == 403 and response.json()["error"]["code"] == "resource_limit_exceeded"


def test_attach_to_a_chosen_subnet(api):
    network = create_network(api, "10.0.1.0/24", "10.0.2.0/24")
    server = create_server(api, "a")["server"]
    response = api.post(f"/v1/servers/{server['id']}/actions/attach_to_network",
                        json={"network": network["id"], "ip_range": "10.0.1.0/24", "ip": "10.0.2.5"})
    assert response.status_code == 422   # the IP is not in ip_range
    response = api.post(f"/v1/servers/{server['id']}/actions/attach_to_network", json={"network": network["id"], "ip_range": "10.0.2.0/24"})
    wait_action(api, response.json()["action"])
    assert api.get(f"/v1/servers/{server['id']}").json()["server"]["private_net"][0]["ip"].startswith("10.0.2.")
    assert api.post(f"/v1/servers/{server['id']}/actions/attach_to_network",
                    json={"network": network["id"], "ip": "not-an-ip"}).status_code == 422


def test_a_new_network_does_not_take_an_address_in_use(api):
    network = create_network(api, "10.0.1.0/24")
    create_server(api, "a", networks=[network["id"]])
    response = api.post("/v1/networks", json={"name": "m", "ip_range": "10.0.1.0/24"})   # its first address is a's
    assert response.status_code == 422
    api.sh.run = (lambda run: lambda *a, **k: "10.0.0.1\n" if a[0] == "local-addrs" else run(*a, **k))(api.sh.run)
    api.lab.fabric._local = None
    response = api.post("/v1/networks", json={"name": "o", "ip_range": "10.0.0.0/16"})   # n's gateway: shared, fine
    assert response.status_code == 201, response.text


def test_detached_nic_takes_its_netem_along(api):
    n1, n2 = create_network(api, "10.0.1.0/24"), create_network(api, "10.0.1.0/24", name="m")
    server = create_server(api, "a", networks=[n1["id"]])["server"]
    api.post("/_lab/netem", json={"vm": "a", "net": "private", "args": "delay 100ms"})
    assert "prv1x1" in api.lab.state.data["netem"]
    response = api.post(f"/v1/servers/{server['id']}/actions/detach_from_network", json={"network": n1["id"]})
    wait_action(api, response.json()["action"])
    assert "prv1x1" not in api.lab.state.data["netem"]
    response = api.post(f"/v1/servers/{server['id']}/actions/attach_to_network", json={"network": n2["id"]})
    wait_action(api, response.json()["action"])


def test_alias_ip_not_a_network_gateway(api):
    network = api.post("/v1/networks", json={"name": "n", "ip_range": "10.0.0.0/16", "subnets": [
        {"type": "cloud", "ip_range": "10.0.4.0/24", "network_zone": "eu-central"}]}).json()["network"]
    response = api.post("/v1/networks", json={"name": "m", "ip_range": "10.0.4.0/24"})   # its gateway: 10.0.4.1
    assert response.status_code == 201, response.text
    server = create_server(api, "a", networks=[network["id"]])["server"]
    response = api.post(f"/v1/servers/{server['id']}/actions/change_alias_ips", json={"network": network["id"], "alias_ips": ["10.0.4.1"]})
    assert response.status_code == 422


def test_network_routes_go_to_the_gateway_server(api, tmp_path):
    network = api.post("/v1/networks", json={"name": "n", "ip_range": "10.0.0.0/16", "subnets": [
        {"type": "cloud", "ip_range": "10.0.1.0/24", "network_zone": "eu-central"}]}).json()["network"]
    create_server(api, "gw", networks=[network["id"]])
    wait_action(api, api.post(f"/v1/networks/{network['id']}/actions/add_route",
                              json={"destination": "10.100.0.0/24", "gateway": "10.0.1.1"}).json()["action"])
    assert ("route-via", "10.100.0.0/24", "10.0.1.1", "prv1x1", str(network["id"])) in api.sh.calls
    assert "10.100.0.0/24,10.0.0.1" in dhcp(tmp_path)
    api.post(f"/v1/networks/{network['id']}/actions/delete_route", json={"destination": "10.100.0.0/24",
                                                                         "gateway": "10.0.1.1"})
    assert ("route-del", "10.100.0.0/24", str(network["id"])) in api.sh.calls


def test_private_networks_are_isolated_from_each_other(api):
    n1 = api.post("/v1/networks", json={"name": "n1", "ip_range": "10.0.0.0/16",
                                        "subnets": [{"type": "cloud", "network_zone": "eu-central"}]}).json()["network"]
    n2 = api.post("/v1/networks", json={"name": "n2", "ip_range": "10.1.0.0/16",
                                        "subnets": [{"type": "cloud", "network_zone": "eu-central"}]}).json()["network"]
    create_server(api, "a", networks=[n1["id"]])
    create_server(api, "b", networks=[n1["id"]])
    api.sh.calls.clear()
    create_server(api, "c", networks=[n2["id"]])
    pairs = nft_set(api, "private_pairs")
    assert '"prv1x1" . "prv2x1"' in pairs
    assert "prv3x1" not in pairs
