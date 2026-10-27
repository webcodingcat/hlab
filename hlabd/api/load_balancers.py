"""Load balancers: services, targets and their health, on HAProxy in the lab.

The API keeps services and targets in Hetzner's shape and fills in Hetzner's defaults
(`service_of`). The fabric runs each load balancer in a namespace of its own with the
HAProxy configuration that api/desired/ renders; a background job reads HAProxy's
checks into `lab.readings.health` for the targets' health status.

A load balancer joins one cloud network at most, like a server with a NIC (a veth
whose name starts with "prv"); with `use_private_ip` it reaches its targets there.
Without its public interface, nothing routes its public addresses to it.
"""

import asyncio
import copy
import logging
from typing import Any

from fastapi import FastAPI, Request

from ..errors import ApiError, check_unique_name, invalid, limit_exceeded, protected
from ..lab import ActionSpec, ActionTable, Lab, Plan, now
from ..records import BalancerNic, LoadBalancer
from ..state import CERTIFICATES, LOAD_BALANCERS, NETWORKS, SERVERS
from . import desired, hetzner, nics, primary_ips, system
from .catalogue import catalogue_name
from .resources import change_delete_protection, update_resource
from .web import filtered, paginate, read_body, ref_id

log = logging.getLogger(__name__)

HEALTH_SECONDS = 3  # how often the lab reads HAProxy's checks
BUSY = "load balancer is locked by another action"
# what a service listens on by default, per protocol (Hetzner's defaults)
DEFAULT_PORTS = {"http": 80, "https": 443}


def failed(code: str, message: str, status: int = 422) -> ApiError:
    """Return one of the load balancer's own errors (Hetzner's codes)."""
    return ApiError(status, code, message)


# --- services -------------------------------------------------------------------------


def merged(base: dict, change: dict) -> dict:
    """Return `base` with `change` laid over it, nested objects merged by key."""
    out = copy.deepcopy(base)
    for key, value in change.items():
        if isinstance(value, dict) and isinstance(out.get(key), dict):
            out[key] = merged(out[key], value)
        else:
            out[key] = copy.deepcopy(value)
    return out


def service_of(body: dict) -> dict:
    """Return a service as Hetzner shows it, with Hetzner's defaults filled in."""
    protocol = body.get("protocol") or "tcp"
    listen_port = body.get("listen_port") or DEFAULT_PORTS.get(protocol)
    if not listen_port:
        raise invalid("listen_port is required for tcp", "listen_port")
    destination_port = body.get("destination_port") or (
        80 if protocol in DEFAULT_PORTS else listen_port
    )
    given = body.get("health_check") or {}
    check = {
        "protocol": given.get("protocol") or ("tcp" if protocol == "tcp" else "http"),
        "port": given.get("port") or destination_port,
        "interval": given.get("interval") or 15,
        "timeout": given.get("timeout") or 10,
        "retries": given.get("retries") or 3,
    }
    if check["protocol"] == "http":
        http_check = given.get("http") or {}
        check["http"] = {
            "domain": http_check.get("domain"),
            "path": http_check.get("path") or "/",
            "response": http_check.get("response") or "",
            "status_codes": http_check.get("status_codes") or ["2??", "3??"],
            "tls": bool(http_check.get("tls", False)),
        }
    service: dict[str, Any] = {
        "protocol": protocol,
        "listen_port": listen_port,
        "destination_port": destination_port,
        "proxyprotocol": bool(body.get("proxyprotocol", False)),
        "health_check": check,
    }
    if protocol in DEFAULT_PORTS:
        http = body.get("http") or {}
        service["http"] = {
            "cookie_name": http.get("cookie_name") or "HCLBSTICKY",
            "cookie_lifetime": http.get("cookie_lifetime") or 300,
            "timeout_idle": http.get("timeout_idle") or 50,
            "sticky_sessions": bool(http.get("sticky_sessions", False)),
        }
        if protocol == "https":
            service["http"]["certificates"] = list(http.get("certificates") or [])
            service["http"]["redirect_http"] = bool(http.get("redirect_http", False))
    return service


def check_services(lab: Lab, balancer_type: str, services: list[dict]) -> None:
    """Refuse services that clash, exceed the type, or lack their certificates."""
    entry = lab.cfg["load_balancer_types"][balancer_type]
    if len(services) > entry["max_services"]:
        raise limit_exceeded(
            f"{balancer_type} has at most {entry['max_services']} services", "services"
        )
    ports = [s["listen_port"] for s in services]
    ports += [80 for s in services if (s.get("http") or {}).get("redirect_http")]
    if len(set(ports)) != len(ports):
        raise failed(
            "source_port_already_used", "the listen port is already in use", 412
        )
    certificates = set()
    for service in services:
        if service["protocol"] != "https":
            continue
        cids = service["http"]["certificates"]
        if not cids:
            raise invalid("an https service needs a certificate", "certificates")
        for cid in cids:
            if cid not in lab.state[CERTIFICATES]:
                raise invalid(f"certificate {cid} not found", "certificates")
        certificates |= set(cids)
    if len(certificates) > entry["max_certificates"]:
        raise limit_exceeded(
            f"{balancer_type} has at most {entry['max_certificates']} certificates",
            "certificates",
        )


# --- targets --------------------------------------------------------------------------


def target_of(lab: Lab, body: dict, network: int | None) -> dict:
    """Return a target as Hetzner shows it (without its health), after checks.

    `network` is the load balancer's cloud network, which `use_private_ip` needs.
    """
    private = bool(body.get("use_private_ip"))
    if private and network is None:
        raise failed(
            "load_balancer_not_attached_to_network",
            "the load balancer is not attached to a network",
        )
    kind = body.get("type")
    if kind == "server":
        sid = (body.get("server") or {}).get("id")
        if lab.state.get(SERVERS, sid) is None:
            raise invalid(f"server {sid} not found", "server")
        target = {"type": "server", "server": {"id": sid}, "use_private_ip": private}
        if not desired.target_members(lab.state, target, network):
            if private:
                raise failed(
                    "resolve_cloud_private_targets_error",
                    "the server is not in the load balancer's network",
                )
            raise failed(
                "resolve_cloud_public_targets_error",
                "the server has no public IPv4 address",
            )
        return target
    if kind == "label_selector":
        selector = (body.get("label_selector") or {}).get("selector") or ""
        return {
            "type": "label_selector",
            "label_selector": {"selector": selector},
            "use_private_ip": private,
        }
    if kind == "ip":
        ip = (body.get("ip") or {}).get("ip") or ""
        if primary_ips.is_cloud_address(lab, ip):
            raise failed(
                "cloud_resource_ip_not_allowed",
                "an IP target cannot be an address of the cloud",
            )
        return {"type": "ip", "ip": {"ip": ip}}
    raise invalid("type must be server, label_selector or ip", "type")


def same_target(first: dict, second: dict) -> bool:
    """Tell whether two targets are the same (Hetzner compares them so)."""
    return {k: v for k, v in first.items() if k != "use_private_ip"} == {
        k: v for k, v in second.items() if k != "use_private_ip"
    }


def check_targets(
    lab: Lab, balancer_type: str, targets: list[dict], network: int | None
) -> None:
    """Refuse a target given twice, and more members than the type allows."""
    for index, target in enumerate(targets):
        if any(same_target(target, other) for other in targets[:index]):
            raise failed("target_already_defined", "the target is already defined")
    members = sum(len(desired.target_members(lab.state, t, network)) for t in targets)
    limit = lab.cfg["load_balancer_types"][balancer_type]["max_targets"]
    if members > limit:
        raise limit_exceeded(f"{balancer_type} has at most {limit} targets", "targets")


# --- networks -------------------------------------------------------------------------


def private_link(
    lab: Lab, body: dict, location: str, local_addresses: set[str]
) -> BalancerNic:
    """Return a load balancer's NIC in the network a request names, with a free IP.

    `local_addresses` are the container's own addresses, which no NIC may get.
    """
    nid = ref_id(body.get("network"), "network")
    network = lab.state.get(NETWORKS, nid)
    if network is None or network.get("_deleting"):
        raise invalid(f"network {nid} not found", "network")
    zone = lab.cfg["locations"][location].get("network_zone", "eu-central")
    ip = nics.network_ip(
        lab, network, body.get("ip"), local_addresses, zone, body.get("ip_range")
    )
    return {"network": nid, "ip": ip}


# --- create and delete ----------------------------------------------------------------


async def create_load_balancer(lab: Lab, body: dict) -> dict:
    """Create a load balancer with its services, targets and network, and start it."""
    public = body.get("public_interface") is not False
    if not public and not body.get("network"):
        raise invalid("without a public interface it needs a network", "network")
    local_addresses = (  # runs a script: the addresses that no NIC may get
        await lab.blocking(lab.fabric.network.read_local_addresses)
        if body.get("network")
        else set()
    )
    network = ref_id(body["network"], "network") if body.get("network") else None
    balancer_type = catalogue_name(
        lab, "load_balancer_type", body.get("load_balancer_type")
    )
    location = body.get("location")
    if not location:
        zone = body.get("network_zone")
        location = next(
            (
                n
                for n, loc in lab.cfg["locations"].items()
                if loc.get("network_zone") == zone
            ),
            None,
        )
    location = catalogue_name(lab, "location", location)
    services = [service_of(s) for s in body.get("services") or []]
    targets = [target_of(lab, t, network) for t in body.get("targets") or []]
    check_services(lab, balancer_type, services)
    check_targets(lab, balancer_type, targets, network)
    state = lab.state
    with state.change():
        check_unique_name(
            state[LOAD_BALANCERS].values(), body.get("name"), None, "load balancer"
        )
        lid = state.new_id(LOAD_BALANCERS)
        balancer: LoadBalancer = {
            "id": lid,
            "name": body.get("name") or "",
            "type": balancer_type,
            "location": location,
            "algorithm": (body.get("algorithm") or {}).get("type") or "round_robin",
            "labels": body.get("labels") or {},
            "protection": {"delete": False},
            "created": now(),
            "ipv4": primary_ips.free_ipv4(lab),
            "ipv6_net": primary_ips.free_ipv6(lab),
            "dns_ptr": {},
            "services": services,
            "targets": targets,
            "private_net": [private_link(lab, body, location, local_addresses)]
            if network
            else [],
            "public_interface": public,
        }
        state[LOAD_BALANCERS][lid] = balancer
        action = lab.new_action("create_load_balancer", [(lid, "load_balancer")])

    async def steps():
        await system.apply_load_balancers(lab)

    lab.run_action(action, steps)
    return {
        "load_balancer": hetzner.load_balancer_json(lab, balancer),
        "action": action,
    }


def delete_load_balancer(lab: Lab, lid: int) -> dict:
    """Delete a load balancer unless it is delete protected; stop it."""
    balancer = lab.get_or_404(LOAD_BALANCERS, lid, "load balancer")
    if balancer["protection"]["delete"]:
        raise protected("load balancer is delete protected")
    if balancer.get("_busy"):
        raise ApiError(423, "locked", BUSY)
    with lab.state.change():
        del lab.state[LOAD_BALANCERS][lid]
        action = lab.new_action("delete_load_balancer", [(lid, "load_balancer")])

    async def steps():
        await system.apply_load_balancers(lab)

    lab.run_action(action, steps)
    return {"action": action}


# --- actions --------------------------------------------------------------------------


def apply_plan(lab: Lab, resources: list[tuple[int, str]] | None = None) -> Plan:
    """Return the plan of an action whose change is in the record: run it anew."""

    async def steps():
        await system.apply_load_balancers(lab)

    return Plan(steps, resources=resources or [])


async def add_service(lab: Lab, balancer: LoadBalancer, body: dict) -> Plan:
    """Add a service."""
    services = [*balancer["services"], service_of(body)]
    check_services(lab, balancer["type"], services)
    with lab.state.change():
        balancer["services"] = services
    return apply_plan(lab)


async def update_service(lab: Lab, balancer: LoadBalancer, body: dict) -> Plan:
    """Change a service, named by its listen port; what the body leaves out stays."""
    port = body.get("listen_port")
    old = next((s for s in balancer["services"] if s["listen_port"] == port), None)
    if old is None:
        raise invalid(f"no service listens on port {port}", "listen_port")
    new = service_of(merged(old, body))
    services = [new if s is old else s for s in balancer["services"]]
    check_services(lab, balancer["type"], services)
    with lab.state.change():
        balancer["services"] = services
    return apply_plan(lab)


async def delete_service(lab: Lab, balancer: LoadBalancer, body: dict) -> Plan:
    """Remove the service listening on a port."""
    port = body.get("listen_port")
    services = [s for s in balancer["services"] if s["listen_port"] != port]
    if len(services) == len(balancer["services"]):
        raise invalid(f"no service listens on port {port}", "listen_port")
    with lab.state.change():
        balancer["services"] = services
    return apply_plan(lab)


async def add_target(lab: Lab, balancer: LoadBalancer, body: dict) -> Plan:
    """Add a target: a server, the servers of a label selector, or an address."""
    network = desired.balancer_network(balancer)
    targets = [*balancer["targets"], target_of(lab, body, network)]
    check_targets(lab, balancer["type"], targets, network)
    with lab.state.change():
        balancer["targets"] = targets
    return apply_plan(lab)


async def remove_target(lab: Lab, balancer: LoadBalancer, body: dict) -> Plan:
    """Remove a target, named as it was added."""
    given = {
        k: v for k, v in body.items() if k in ("type", "server", "label_selector", "ip")
    }
    targets = [t for t in balancer["targets"] if not same_target(t, {**t, **given})]
    if len(targets) == len(balancer["targets"]):
        raise invalid("the target is not defined", "type")
    with lab.state.change():
        balancer["targets"] = targets
    return apply_plan(lab)


async def change_algorithm(lab: Lab, balancer: LoadBalancer, body: dict) -> Plan:
    """Change how connections are spread over the targets."""
    with lab.state.change():
        balancer["algorithm"] = body["type"]
    return apply_plan(lab)


async def change_type(lab: Lab, balancer: LoadBalancer, body: dict) -> Plan:
    """Change the type, if the services and targets fit into the new one."""
    new_type = catalogue_name(lab, "load_balancer_type", body.get("load_balancer_type"))
    check_services(lab, new_type, balancer["services"])
    network = desired.balancer_network(balancer)
    check_targets(lab, new_type, balancer["targets"], network)
    with lab.state.change():
        balancer["type"] = new_type
    return apply_plan(lab)


async def change_dns_ptr(lab: Lab, balancer: LoadBalancer, body: dict) -> Plan:
    """Set the reverse DNS entry of one of the load balancer's addresses."""
    ip = body.get("ip")
    if ip not in (balancer["ipv4"], desired.balancer_ipv6(balancer)):
        raise invalid("ip is not an address of the load balancer", "ip")
    with lab.state.change():
        if body.get("dns_ptr"):
            balancer["dns_ptr"][ip] = body["dns_ptr"]
        else:
            balancer["dns_ptr"].pop(ip, None)
    return Plan()


async def attach_to_network(lab: Lab, balancer: LoadBalancer, body: dict) -> Plan:
    """Put the load balancer into a cloud network, with a free or the given IP."""
    if balancer["private_net"]:
        raise invalid("the load balancer is in a network already", "network")
    lid = balancer["id"]
    # runs a script: the addresses that no NIC may get
    local_addresses = await lab.blocking(lab.fabric.network.read_local_addresses)
    balancer = lab.get_or_404(LOAD_BALANCERS, lid, "load balancer")  # looked up again
    link = private_link(lab, body, balancer["location"], local_addresses)
    with lab.state.change():
        balancer["private_net"] = [link]
    return apply_plan(lab, [(link["network"], "network")])


async def detach_from_network(lab: Lab, balancer: LoadBalancer, body: dict) -> Plan:
    """Take the load balancer out of its network, if it does without it."""
    nid = ref_id(body.get("network"), "network")
    if desired.balancer_network(balancer) != nid:
        raise invalid("the load balancer is not in this network", "network")
    if not balancer["public_interface"]:
        raise invalid("without its public interface it needs its network", "network")
    if any(t.get("use_private_ip") for t in balancer["targets"]):
        raise invalid("targets use the private network", "network")
    with lab.state.change():
        balancer["private_net"] = []
    return apply_plan(lab, [(nid, "network")])


async def enable_public_interface(lab: Lab, balancer: LoadBalancer, body: dict) -> Plan:
    """Route the load balancer's public addresses to it again."""
    with lab.state.change():
        balancer["public_interface"] = True
    return apply_plan(lab)


async def disable_public_interface(
    lab: Lab, balancer: LoadBalancer, body: dict
) -> Plan:
    """Stop routing the public addresses to it; it needs a network for that."""
    if not balancer["private_net"]:
        raise invalid("without a network it needs its public interface", "network")
    with lab.state.change():
        balancer["public_interface"] = False
    return apply_plan(lab)


# the load balancer actions: Hetzner's name of the action and the function that plans it
LOAD_BALANCER_ACTIONS = ActionTable[LoadBalancer](
    "load_balancer",
    {
        "add_service": ActionSpec("add_service", add_service),
        "update_service": ActionSpec("update_service", update_service),
        "delete_service": ActionSpec("delete_service", delete_service),
        "add_target": ActionSpec("add_target", add_target),
        "remove_target": ActionSpec("remove_target", remove_target),
        "change_algorithm": ActionSpec("change_algorithm", change_algorithm),
        "change_type": ActionSpec("change_load_balancer_type", change_type),
        "change_protection": ActionSpec(
            "change_protection", change_delete_protection, flag=False
        ),
        "change_dns_ptr": ActionSpec("change_dns_ptr", change_dns_ptr, flag=False),
        "attach_to_network": ActionSpec("attach_to_network", attach_to_network),
        "detach_from_network": ActionSpec("detach_from_network", detach_from_network),
        "enable_public_interface": ActionSpec(
            "enable_public_interface", enable_public_interface
        ),
        "disable_public_interface": ActionSpec(
            "disable_public_interface", disable_public_interface
        ),
    },
    flag="_busy",
    busy=BUSY,
)


# --- health ---------------------------------------------------------------------------


async def health_watcher(lab: Lab) -> None:
    """Read the checks of every load balancer's HAProxy, as long as the lab runs."""
    while True:
        await asyncio.sleep(HEALTH_SECONDS)
        if lab.resetting:
            continue
        try:
            await read_health(lab)
        except Exception:  # the watcher keeps going; the error is in the log
            log.exception("load balancer health")


async def read_health(lab: Lab) -> None:
    """Read HAProxy's checks of each load balancer; forget deleted ones."""
    lids = list(lab.state[LOAD_BALANCERS])
    health = {}
    for lid in lids:
        health[lid] = await lab.blocking(lab.fabric.balancers.health, lid)
    lab.readings.health = health


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the load balancer routes to the app."""
    state = lab.state

    @app.get("/v1/load_balancers")
    async def load_balancer_list(request: Request):
        items = filtered(request, list(state[LOAD_BALANCERS].values()))
        return paginate(
            request,
            [hetzner.load_balancer_json(lab, b) for b in items],
            "load_balancers",
        )

    @app.post("/v1/load_balancers", status_code=201)
    async def load_balancer_create(request: Request):
        return await create_load_balancer(lab, await read_body(request))

    @app.get("/v1/load_balancers/{lid}")
    async def load_balancer_get(lid: int):
        balancer = lab.get_or_404(LOAD_BALANCERS, lid, "load balancer")
        return {"load_balancer": hetzner.load_balancer_json(lab, balancer)}

    @app.put("/v1/load_balancers/{lid}")
    async def load_balancer_update(lid: int, request: Request):
        body = await read_body(request)
        balancer = lab.get_or_404(LOAD_BALANCERS, lid, "load balancer")
        keys = ("name", "labels")
        update_resource(lab, LOAD_BALANCERS, balancer, body, keys, "load balancer")
        return {"load_balancer": hetzner.load_balancer_json(lab, balancer)}

    @app.delete("/v1/load_balancers/{lid}", status_code=201)
    async def load_balancer_delete(lid: int):
        return delete_load_balancer(lab, lid)

    @app.post("/v1/load_balancers/{lid}/actions/{cmd}", status_code=201)
    async def load_balancer_action(lid: int, cmd: str, request: Request):
        body = await read_body(request)
        balancer = lab.get_or_404(LOAD_BALANCERS, lid, "load balancer")
        return await lab.act(LOAD_BALANCER_ACTIONS, cmd, balancer, body)
