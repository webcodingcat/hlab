"""What the JSON shapes share: error bodies, prices, reverse DNS."""

from decimal import Decimal

from ...lab import Lab
from ...records import (
    DnsPtr,
    FloatingIP,
    PrimaryIP,
)

CREATED = "2026-01-01T00:00:00+00:00"  # catalogue entries


TB = 1024**4  # Hetzner counts traffic in binary terabytes


# Hetzner's hourly price is the monthly one spread over 624 hours (26 days): a
# server costs its monthly price at most
HOURS_PER_MONTH = Decimal(624)


def error(code: str, message: str, details: dict | None = None) -> dict:
    """Return the API's error body."""
    return {"error": {"code": code, "message": message, "details": details or {}}}


def price(lab: Lab, net: float | Decimal) -> dict:
    """Return a price as the API gives it: net and with VAT, as decimal strings.

    `net` is a number of the configuration (EUR without VAT).
    """
    amount = Decimal(str(net))
    vat = Decimal(str(lab.cfg["pricing"]["vat_rate"])) / 100
    return {"net": f"{amount:.10f}", "gross": f"{amount * (1 + vat):.10f}"}


def hourly(monthly: float | Decimal) -> Decimal:
    """Return Hetzner's hourly price for a monthly price, to four decimals."""
    return (Decimal(str(monthly)) / HOURS_PER_MONTH).quantize(Decimal("0.0001"))


def default_ptr(ip: str) -> str:
    """Return Hetzner's reverse DNS for an IPv4 without its own entry.

    The octets are reversed (static.4.3.2.1.clients...).
    """
    return f"static.{'.'.join(reversed(ip.split('.')))}.clients.your-server.de"


def dns_ptrs(record: PrimaryIP | FloatingIP) -> list[DnsPtr]:
    """Return the reverse DNS entries of a primary or Floating IP.

    There is one entry per address, so an IPv6 /64 may have several. An IPv4 without
    its own entry gets Hetzner's default.
    """
    if record["type"] == "ipv4" and not record["dns_ptr"]:
        return [{"ip": record["ip"], "dns_ptr": default_ptr(record["ip"])}]
    return record["dns_ptr"]
