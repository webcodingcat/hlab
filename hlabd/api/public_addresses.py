"""What primary IPs and Floating IPs share: free addresses and reverse DNS."""

import ipaddress

from ..errors import ApiError, invalid
from ..lab import Lab, Plan
from ..records import FloatingIP, PrimaryIP
from .spec import hostname_ok


def first_free(candidates, used: set[str], error: ApiError) -> str:
    """Return the first candidate address that is not used, or raise the error."""
    address = next((c for c in candidates if c not in used), None)
    if address is None:
        raise error
    return address


def contains(prefix: str, ip: str) -> bool:
    """Return whether an address belongs to an IP (a single address, or an IPv6 /64)."""
    try:
        return ipaddress.ip_address(ip) in ipaddress.ip_network(prefix)
    except ValueError:
        return False


def set_dns_ptr(
    lab: Lab, record: PrimaryIP | FloatingIP, ip: str, ptr: str | None
) -> None:
    """Set the reverse DNS entry of one address of a primary or Floating IP.

    None removes the entry.
    """
    if ptr is not None and not (isinstance(ptr, str) and hostname_ok(ptr)):
        raise invalid("dns_ptr must be a host name (without trailing dot)", "dns_ptr")
    with lab.state.change():
        record["dns_ptr"] = [x for x in record.get("dns_ptr", []) if x["ip"] != ip]
        if ptr:
            record["dns_ptr"].append({"ip": ip, "dns_ptr": ptr})


def record_dns_ptr(
    lab: Lab, record: PrimaryIP | FloatingIP, body: dict, default_ip: str
) -> Plan:
    """Set the reverse DNS of one of a primary or Floating IP's addresses.

    The address is the body's `ip`, else `default_ip`. If `default_ip` is empty, the
    body must name the address.
    """
    ip = str(body.get("ip") or default_ip)
    if not contains(record["ip"], ip):
        raise invalid("ip must be an address of the IP", "ip")
    set_dns_ptr(lab, record, str(ipaddress.ip_address(ip)), body.get("dns_ptr"))
    return Plan()
