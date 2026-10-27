"""The load balancers as the fabric runs them: members and HAProxy's configuration."""

from ...config import Config
from ...fabric.balancers import BalancerLink, BalancerSpec
from ...labels import selector_servers
from ...records import (
    LoadBalancer,
    Server,
)
from ...state import (
    CERTIFICATES,
    LOAD_BALANCERS,
    NETWORKS,
    SERVERS,
    State,
    primary_address_of,
)

# HAProxy's balance algorithm for Hetzner's
ALGORITHMS = {"round_robin": "roundrobin", "least_connections": "leastconn"}


def member_name(target: dict, sid: int | None = None) -> str:
    """Return HAProxy's name of a target's member: s<server ID> or i<address>.

    A label selector target has one member per matching server (`sid`).
    """
    if target["type"] == "ip":
        return "i" + target["ip"]["ip"].replace(".", "-").replace(":", "-")
    return f"s{sid if sid is not None else target['server']['id']}"


def balancer_network(balancer: LoadBalancer) -> int | None:
    """Return the ID of the cloud network a load balancer is in, or None."""
    return balancer["private_net"][0]["network"] if balancer["private_net"] else None


def server_target_address(
    state: State, server: Server, network: int | None, private: bool
) -> str | None:
    """Return where a load balancer reaches a server, or None if it cannot.

    As on Hetzner: at its primary IPv4, or with `use_private_ip` at its address in
    the load balancer's network.
    """
    if not private:
        return primary_address_of(state, server, "ipv4")
    nic = next((n for n in server["_lab"]["nics"] if n["network"] == network), None)
    return nic["ip"] if nic else None


def target_members(
    state: State, target: dict, network: int | None
) -> list[tuple[str, str]]:
    """Return a target's members as (HAProxy name, address).

    `network` is the load balancer's cloud network. A server that cannot be reached
    (no public IPv4, or not in the network) is left out.
    """
    if target["type"] == "ip":
        return [(member_name(target), target["ip"]["ip"])]
    if target["type"] == "server":
        servers = [s for s in [state.get(SERVERS, target["server"]["id"])] if s]
    else:
        servers = selector_servers(state, target["label_selector"]["selector"])
    private = target["use_private_ip"]
    return [
        (member_name(target, server["id"]), ip)
        for server in servers
        if (ip := server_target_address(state, server, network, private))
    ]


def health_check_lines(check: dict) -> list[str]:
    """Return a backend's lines for a service's HTTP health check (none for TCP)."""
    if check["protocol"] != "http":
        return []
    http = check["http"]
    send = f"    http-check send meth GET uri {http['path']}"
    if http.get("domain"):
        send += f" hdr Host {http['domain']}"
    # Hetzner's status codes are patterns like "2??"
    codes = "|".join(c.replace("?", ".") for c in http["status_codes"])
    lines = ["    option httpchk", send, f"    http-check expect rstatus ^({codes})$"]
    if http.get("response"):
        lines.append(f"    http-check expect string {http['response']}")
    return lines


def bind_lines(port: int, tls: str) -> list[str]:
    """Return a frontend's listeners: IPv4 and IPv6 apart.

    One dual-stack socket would show IPv4 clients as ::ffff:a.b.c.d to the targets;
    Hetzner hands over the plain address.
    """
    return [f"    bind 0.0.0.0:{port}{tls}", f"    bind :::{port} v6only{tls}"]


def haproxy_config(state: State, cfg: Config, balancer: LoadBalancer) -> str:
    """Return HAProxy's configuration of a load balancer, one frontend per service.

    The client's address reaches the targets: through the PROXY protocol where the
    service has it, and in X-Forwarded-For (with -Proto and -Port) for HTTP(S).
    """
    entry = cfg["load_balancer_types"][balancer["type"]]
    lines = [
        "global",
        f"    maxconn {entry['max_connections']}",
        "    stats socket unix@haproxy.sock mode 600 level admin",
        "defaults",
        "    timeout connect 5s",
        "    timeout client 50s",
        "    timeout server 50s",
    ]
    for service in balancer["services"]:
        port, protocol = service["listen_port"], service["protocol"]
        http = service.get("http") or {}
        mode = "tcp" if protocol == "tcp" else "http"
        tls = ""
        if protocol == "https":
            certificates = [
                f"crt certs/{cid}.pem"
                for cid in http["certificates"]
                if (c := state.get(CERTIFICATES, cid)) and c["certificate"]
            ]
            tls = " ssl " + " ".join(certificates)
        lines += ["frontend f" + str(port), *bind_lines(port, tls), f"    mode {mode}"]
        if mode == "http":
            lines += [
                "    option forwardfor",
                f"    http-request set-header X-Forwarded-Proto {protocol}",
                f"    http-request set-header X-Forwarded-Port {port}",
                f"    timeout http-keep-alive {http['timeout_idle']}s",
            ]
        lines.append(f"    default_backend b{port}")
        if protocol == "https" and http.get("redirect_http"):
            lines += [
                "frontend redirect",
                *bind_lines(80, ""),
                "    mode http",
                "    http-request redirect scheme https code 301",
            ]
        check = service["health_check"]
        lines += [
            f"backend b{port}",
            f"    mode {mode}",
            f"    balance {ALGORITHMS[balancer['algorithm']]}",
            f"    timeout check {check['timeout']}s",
            *health_check_lines(check),
        ]
        sticky = mode == "http" and http.get("sticky_sessions")
        if sticky:
            lines.append(
                f"    cookie {http['cookie_name']} insert indirect nocache "
                f"maxlife {http['cookie_lifetime']}s"
            )
        options = (
            f"check port {check['port']} inter {check['interval']}s "
            f"fall {check['retries']} rise {check['retries']}"
        )
        if check["protocol"] == "http" and check["http"].get("tls"):
            options += " check-ssl verify none"
        if service["proxyprotocol"]:
            options += " send-proxy"
        for target in balancer["targets"]:
            for name, ip in target_members(state, target, balancer_network(balancer)):
                address = f"[{ip}]" if ":" in ip else ip
                cookie = f" cookie {name}" if sticky else ""
                lines.append(
                    f"    server {name} {address}:{service['destination_port']} "
                    f"{options}{cookie}"
                )
    return "\n".join(lines) + "\n"


def balancer_specs(state: State, cfg: Config) -> list[BalancerSpec]:
    """Return every load balancer as the fabric runs it, with its certificates."""
    specs = []
    for balancer in sorted(state[LOAD_BALANCERS].values(), key=lambda b: b["id"]):
        cids = {
            cid
            for service in balancer["services"]
            for cid in (service.get("http") or {}).get("certificates", [])
        }
        certificates = {
            f"{cid}.pem": c["certificate"] + (c["_key"] or "")
            for cid in sorted(cids)
            if (c := state.get(CERTIFICATES, cid)) and c["certificate"]
        }
        specs.append(
            BalancerSpec(
                balancer["id"],
                balancer["ipv4"],
                balancer_ipv6(balancer),
                haproxy_config(state, cfg, balancer),
                certificates,
                balancer["public_interface"],
                [
                    BalancerLink(
                        link["ip"],
                        link["network"],
                        state[NETWORKS][link["network"]]["ip_range"],
                    )
                    for link in balancer["private_net"]
                    if link["network"] in state[NETWORKS]
                ],
            )
        )
    return specs


def balancer_ipv6(balancer: LoadBalancer) -> str:
    """Return a load balancer's IPv6 address: the ::1 of its /64."""
    return balancer["ipv6_net"].split("::")[0] + "::1"
