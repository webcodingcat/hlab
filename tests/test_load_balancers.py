"""Load balancers: services and targets, HAProxy's configuration, health, limits."""

from conftest import create_network, create_server, state_of, wait_action
from test_certificates import upload

from hlabd import config
from hlabd.api import desired


def service(protocol="http", listen=80, destination=80, proxyprotocol=False, http=None):
    """Return a service as a create request must give it (the spec needs all of it)."""
    check = {"protocol": "tcp", "port": destination, "interval": 15, "timeout": 10}
    out = {
        "protocol": protocol,
        "listen_port": listen,
        "destination_port": destination,
        "proxyprotocol": proxyprotocol,
        "health_check": {**check, "retries": 3},
    }
    if protocol != "tcp":
        out["http"] = {
            "cookie_name": "HCLBSTICKY",
            "cookie_lifetime": 300,
            "timeout_idle": 50,
            "sticky_sessions": False,
            "certificates": [],
            "redirect_http": False,
            **(http or {}),
        }
    return out


def create_balancer(api, name="lb", **extra):
    """Create an lb11 in fsn1, wait for it and return it."""
    response = api.post(
        "/v1/load_balancers",
        json={"name": name, "load_balancer_type": "lb11", "location": "fsn1", **extra},
    )
    assert response.status_code == 201, response.text
    wait_action(api, response.json()["action"])
    return response.json()["load_balancer"]


def action(api, balancer, cmd, body):
    """Run a load balancer action and wait for it; return the response."""
    response = api.post(f"/v1/load_balancers/{balancer['id']}/actions/{cmd}", json=body)
    if response.status_code == 201:
        wait_action(api, response.json()["action"])
    return response


def haproxy_config(tmp_path, balancer) -> str:
    """Return the HAProxy configuration the lab wrote for a load balancer."""
    return (tmp_path / "lb" / str(balancer["id"]) / "haproxy.cfg").read_text()


def test_balancer_gets_hetzners_defaults_and_its_own_addresses(api, tmp_path):
    server = create_server(api, "web")["server"]
    balancer = create_balancer(
        api,
        services=[service()],
        targets=[{"type": "server", "server": {"id": server["id"]}}],
    )
    ipv4 = balancer["public_net"]["ipv4"]["ip"]
    assert (
        ipv4.startswith(config.PUBLIC_NET)
        and ipv4 != server["public_net"]["ipv4"]["ip"]
    )
    assert balancer["public_net"]["ipv6"]["ip"].endswith("::1")
    name = f"lb{balancer['id']}"
    assert any(
        call[:2] == ("add-namespace", name) and call[-1] == config.GATEWAY
        for call in api.sh.calls
    )
    text = haproxy_config(tmp_path, balancer)
    server_ip = server["public_net"]["ipv4"]["ip"]
    assert f"server s{server['id']} {server_ip}:80" in text
    # the client's address reaches the target
    assert "option forwardfor" in text


def test_proxy_protocol_label_selectors_and_algorithm_reach_haproxy(api, tmp_path):
    first = create_server(api, "a", labels={"role": "web"})["server"]
    balancer = create_balancer(
        api,
        services=[service("tcp", 25, 2525, proxyprotocol=True)],
        targets=[
            {"type": "label_selector", "label_selector": {"selector": "role=web"}}
        ],
    )
    second = create_server(api, "b", labels={"role": "web"})["server"]
    text = haproxy_config(tmp_path, balancer)
    assert f"server s{first['id']}" in text and f"server s{second['id']}" in text
    assert "send-proxy" in text
    action(api, balancer, "change_algorithm", {"type": "least_connections"})
    assert "balance leastconn" in haproxy_config(tmp_path, balancer)
    got = api.get(f"/v1/load_balancers/{balancer['id']}").json()["load_balancer"]
    members = got["targets"][0]["targets"]
    assert {m["server"]["id"] for m in members} == {first["id"], second["id"]}


def test_health_comes_from_haproxys_checks(api):
    server = create_server(api, "web")["server"]
    balancer = create_balancer(
        api,
        services=[service("tcp", 22, 22)],
        targets=[{"type": "server", "server": {"id": server["id"]}}],
    )

    def checked():
        api.lab.balancer_health = {balancer["id"]: {("b22", f"s{server['id']}"): "UP"}}

    api.on_loop(checked)
    got = api.get(f"/v1/load_balancers/{balancer['id']}").json()["load_balancer"]
    assert got["targets"][0]["health_status"] == [
        {"listen_port": 22, "status": "healthy"}
    ]


def test_services_and_targets_are_checked_like_on_hetzner(api):
    server = create_server(api, "web")["server"]
    balancer = create_balancer(api, services=[service()])
    errors = {
        "source_port_already_used": ("add_service", service()),
        "invalid_input": ("add_service", service("https", 443)),  # no certificate
        "load_balancer_not_attached_to_network": (
            "add_target",
            {"type": "server", "server": {"id": server["id"]}, "use_private_ip": True},
        ),
        "cloud_resource_ip_not_allowed": (
            "add_target",
            {"type": "ip", "ip": {"ip": server["public_net"]["ipv4"]["ip"]}},
        ),
    }
    for code, (cmd, body) in errors.items():
        response = action(api, balancer, cmd, body)
        assert response.json()["error"]["code"] == code, (code, response.text)
    target = {"type": "server", "server": {"id": server["id"]}}
    action(api, balancer, "add_target", target)
    response = action(api, balancer, "add_target", target)
    assert response.json()["error"]["code"] == "target_already_defined"


def test_services_are_changed_and_removed(api, tmp_path):
    balancer = create_balancer(api, services=[service()])
    response = action(
        api,
        balancer,
        "update_service",
        {
            "listen_port": 80,
            "health_check": {
                "protocol": "http",
                "port": 80,
                "interval": 5,
                "timeout": 3,
                "retries": 2,
                "http": {"path": "/health"},
            },
        },
    )
    assert response.status_code == 201, response.text
    got = api.get(f"/v1/load_balancers/{balancer['id']}").json()["load_balancer"]
    check = got["services"][0]["health_check"]
    assert check["http"]["path"] == "/health" and check["interval"] == 5
    # what the update leaves out stays
    assert got["services"][0]["destination_port"] == 80
    assert "uri /health" in haproxy_config(tmp_path, balancer)
    action(api, balancer, "delete_service", {"listen_port": 80})
    got = api.get(f"/v1/load_balancers/{balancer['id']}").json()["load_balancer"]
    assert got["services"] == []


def test_https_uses_its_certificate_which_is_then_in_use(api, tmp_path):
    certificate = upload(api)
    balancer = create_balancer(
        api,
        services=[
            service(
                "https",
                443,
                80,
                http={"certificates": [certificate["id"]], "redirect_http": True},
            )
        ],
    )
    text = haproxy_config(tmp_path, balancer)
    assert f"crt certs/{certificate['id']}.pem" in text
    assert "redirect scheme https" in text
    pem = tmp_path / "lb" / str(balancer["id"]) / "certs" / f"{certificate['id']}.pem"
    assert "PRIVATE KEY" in pem.read_text()
    got = api.get(f"/v1/certificates/{certificate['id']}").json()["certificate"]
    assert got["used_by"] == [{"id": balancer["id"], "type": "load_balancer"}]
    response = api.delete(f"/v1/certificates/{certificate['id']}")
    assert response.json()["error"]["code"] == "resource_in_use"


def test_protected_balancer_stays_then_goes_with_its_namespace(api, tmp_path):
    balancer = create_balancer(api)
    action(api, balancer, "change_protection", {"delete": True})
    path = f"/v1/load_balancers/{balancer['id']}"
    assert api.delete(path).json()["error"]["code"] == "protected"
    action(api, balancer, "change_protection", {"delete": False})
    wait_action(api, api.delete(path).json()["action"])
    name = f"lb{balancer['id']}"
    assert ("delete-namespace", name, name) in api.sh.calls
    assert not (tmp_path / "lb" / str(balancer["id"])).exists()


def test_reverse_dns_and_type_change(api):
    balancer = create_balancer(api)
    ipv4 = balancer["public_net"]["ipv4"]["ip"]
    action(api, balancer, "change_dns_ptr", {"ip": ipv4, "dns_ptr": "lb.example.com"})
    action(api, balancer, "change_type", {"load_balancer_type": "lb21"})
    got = api.get(f"/v1/load_balancers/{balancer['id']}").json()["load_balancer"]
    assert got["public_net"]["ipv4"]["dns_ptr"] == "lb.example.com"
    assert got["load_balancer_type"]["name"] == "lb21"


def test_balancer_in_a_network_reaches_targets_at_their_private_ip(api, tmp_path):
    network = create_network(api, "10.0.1.0/24")
    inside = create_server(api, "in", networks=[network["id"]])["server"]
    outside = create_server(api, "out")["server"]
    balancer = create_balancer(
        api,
        network=network["id"],
        services=[service("tcp", 22, 22)],
        targets=[
            {"type": "server", "server": {"id": inside["id"]}, "use_private_ip": True}
        ],
    )
    link = balancer["private_net"][0]
    assert link["network"] == network["id"] and link["ip"].startswith("10.0.1.")
    veth = f"prvl{balancer['id']}"
    assert any(
        call[:3] == ("add-namespace-nic", f"lb{balancer['id']}", veth)
        for call in api.sh.calls
    )
    private_ip = inside["private_net"][0]["ip"]
    assert f"server s{inside['id']} {private_ip}:22" in haproxy_config(
        tmp_path, balancer
    )
    # Hetzner: a private target must be in the load balancer's network
    response = action(
        api,
        balancer,
        "add_target",
        {"type": "server", "server": {"id": outside["id"]}, "use_private_ip": True},
    )
    assert response.json()["error"]["code"] == "resolve_cloud_private_targets_error"
    # the cloud network carries the balancer like a server's NIC
    sets = api.on_loop(lambda: desired.fault_sets(api.lab.state))
    inside_tap = state_of(api, "servers")[inside["id"]]["_lab"]["nics"][0]["tap"]
    assert f'"{veth}" . "{inside_tap}"' in sets["private_pairs"]


def test_public_interface_is_switched_off_only_in_a_network(api):
    network = create_network(api, "10.0.1.0/24")
    balancer = create_balancer(api)
    response = action(api, balancer, "disable_public_interface", {})
    assert response.json()["error"]["code"] == "invalid_input"
    assert (
        action(
            api, balancer, "attach_to_network", {"network": network["id"]}
        ).status_code
        == 201
    )
    assert action(api, balancer, "disable_public_interface", {}).status_code == 201
    got = api.get(f"/v1/load_balancers/{balancer['id']}").json()["load_balancer"]
    assert got["public_net"]["enabled"] is False
    ipv4 = got["public_net"]["ipv4"]["ip"]
    assert ("set-route", f"{ipv4}/32") in api.sh.calls
    # without its public interface it keeps its network
    response = action(api, balancer, "detach_from_network", {"network": network["id"]})
    assert response.json()["error"]["code"] == "invalid_input"
    action(api, balancer, "enable_public_interface", {})
    assert (
        action(
            api, balancer, "detach_from_network", {"network": network["id"]}
        ).status_code
        == 201
    )
    assert ("delete-namespace-nic", f"prvl{balancer['id']}") in api.sh.calls


def test_deleting_the_network_takes_the_balancer_out(api):
    network = create_network(api, "10.0.1.0/24")
    balancer = create_balancer(api, network=network["id"])
    assert api.delete(f"/v1/networks/{network['id']}").status_code == 204
    got = api.get(f"/v1/load_balancers/{balancer['id']}").json()["load_balancer"]
    assert got["private_net"] == []
