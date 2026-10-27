"""The JSON shapes of DNS zones and their RRSets."""

from ...lab import Lab
from ...records import (
    RRSet,
    Zone,
)
from ...state import (
    RRSETS,
)

# Hetzner's nameservers, assigned to every zone; the lab answers for them itself
NAMESERVERS = (
    "hydrogen.ns.hetzner.com.",
    "oxygen.ns.hetzner.com.",
    "helium.ns.hetzner.de.",
)


def zone_json(lab: Lab, zone: Zone) -> dict:
    """Return the API object of a DNS zone (always in primary mode in the lab)."""
    rrsets = [r for r in lab.state[RRSETS].values() if r["zone"] == zone["id"]]
    return {
        "id": zone["id"],
        "name": zone["name"],
        "created": zone["created"],
        "mode": "primary",
        "labels": zone["labels"],
        "protection": zone["protection"],
        "ttl": zone["ttl"],
        "status": "ok",
        "record_count": sum(len(r["records"]) for r in rrsets),
        "authoritative_nameservers": {
            "assigned": list(NAMESERVERS),
            # the lab has no parent zones that could delegate
            "delegated": [],
            "delegation_last_check": None,
            "delegation_status": "unknown",
        },
        "registrar": "other",
    }


def rrset_json(rrset: RRSet) -> dict:
    """Return the API object of an RRSet; its ID is "<name>/<type>"."""
    return {
        "id": f"{rrset['name']}/{rrset['type']}",
        "name": rrset["name"],
        "type": rrset["type"],
        "ttl": rrset["ttl"],
        "labels": rrset["labels"],
        "protection": rrset["protection"],
        "records": rrset["records"],
        "zone": rrset["zone"],
    }
