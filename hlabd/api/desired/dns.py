"""The zone files of the DNS zones."""

from ...fabric.dns import ZoneFile
from ...records import (
    Zone,
)
from ...state import (
    RRSETS,
    ZONES,
    State,
)


def zone_text(state: State, zone: Zone) -> str:
    """Return a zone in BIND's zone file format, as Hetzner's /zonefile gives it.

    The SOA comes first; each record's comment follows it after a semicolon.
    """
    rrsets = sorted(
        (r for r in state[RRSETS].values() if r["zone"] == zone["id"]),
        key=lambda r: (r["type"] != "SOA", r["name"] != "@", r["name"], r["type"]),
    )
    lines = [f"$ORIGIN\t{zone['name']}.", f"$TTL\t{zone['ttl']}", ""]
    for rrset in rrsets:
        ttl = "" if rrset["ttl"] is None else f"\t{rrset['ttl']}"
        for record in rrset["records"]:
            comment = f" ; {record['comment']}" if record["comment"] else ""
            lines.append(
                f"{rrset['name']}\tIN{ttl}\t{rrset['type']}\t{record['value']}{comment}"
            )
    return "\n".join(lines) + "\n"


def zone_files(state: State) -> list[ZoneFile]:
    """Return every zone with its zone file, for the lab's nameserver."""
    return [
        ZoneFile(zone["name"], zone_text(state, zone))
        for zone in sorted(state[ZONES].values(), key=lambda z: z["name"])
    ]
