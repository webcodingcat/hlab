"""DNS zones and their RRSets, served by the lab's nameserver.

As on Hetzner, a zone is named by a registrable domain (a public suffix and one label
more, from the Public Suffix List), gets an SOA and Hetzner's three NS records when it
is created, and counts its SOA serial up with every change (YYYYMMDDnn). The lab
serves the zones itself: its resolver answers for them inside the lab (fabric/dns.py).
Only zones in primary mode exist; a secondary zone is refused.

The API names an RRSet by name and type ("www/A"). Its actions run on the zone, and
the route adds the RRSet's name and type to the body as "rrset".
"""

import logging
import re
from datetime import datetime, timezone
from pathlib import Path

import dns.exception
import dns.name
import dns.rdata
import dns.rdataclass
import dns.rdatatype
import dns.zone
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from ..errors import (
    ApiError,
    invalid,
    not_found,
    not_unique,
    protected,
)
from ..lab import ActionSpec, ActionTable, Lab, Plan, now
from ..records import DnsRecord, RRSet, Zone
from ..state import RRSETS, ZONES
from . import desired, hetzner, system
from .web import change_delete_protection, filtered, paginate, read_body, update_fields

log = logging.getLogger(__name__)

# Debian's copy of the Public Suffix List (package publicsuffix)
PUBLIC_SUFFIX_LIST = Path("/usr/share/publicsuffix/public_suffix_list.dat")
DEFAULT_TTL = 3600
# Hetzner's SOA: primary nameserver, contact, and refresh, retry, expire, minimum
SOA_CONTACT = "dns.hetzner.com."
SOA_TIMERS = "86400 10800 3600000 3600"
LABEL = re.compile(r"^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$")


class PublicSuffixes:
    """The Public Suffix List: which names a registry hands out below."""

    def __init__(self, text: str):
        """Read the list's rules: plain ones, wildcards ("*.ck") and exceptions."""
        self.rules: set[str] = set()
        self.exceptions: set[str] = set()
        for line in text.splitlines():
            rule = line.strip().split(" ")[0]
            if not rule or rule.startswith("//"):
                continue
            try:  # the list writes names in Unicode; the API takes Punycode
                rule = rule.encode("idna").decode()
            except UnicodeError:
                pass
            if rule.startswith("!"):
                self.exceptions.add(rule[1:])
            else:
                self.rules.add(rule)

    def suffix_labels(self, labels: list[str]) -> int | None:
        """Return how many of a name's last labels are its public suffix.

        The list's algorithm: an exception wins, else the longest rule. A name that
        no rule matches has no well-known suffix: None (the list itself would take
        its last label).
        """
        for start in range(len(labels)):
            if ".".join(labels[start:]) in self.exceptions:
                return len(labels) - start - 1
        for start in range(len(labels)):
            wildcard = ".".join(["*", *labels[start + 1 :]])
            if ".".join(labels[start:]) in self.rules or wildcard in self.rules:
                return len(labels) - start
        return None

    def is_registrable(self, name: str) -> bool:
        """Tell whether a name is a well-known public suffix and one label more."""
        labels = name.split(".")
        suffix = self.suffix_labels(labels)
        return suffix is not None and len(labels) == suffix + 1


def load_public_suffixes() -> PublicSuffixes:
    """Read the Public Suffix List; the lab's start does it once."""
    return PublicSuffixes(PUBLIC_SUFFIX_LIST.read_text(encoding="utf-8"))


# --- checks ---------------------------------------------------------------------------


def check_zone_name(suffixes: PublicSuffixes, name: str) -> None:
    """Refuse a zone name that is not a registrable domain in lower case."""
    labels = name.split(".")
    if name != name.lower() or not all(LABEL.match(label) for label in labels):
        raise invalid(
            "must be a domain name in lower case, without a final dot", "name"
        )
    if not suffixes.is_registrable(name):
        raise invalid(
            "only domains right below a public suffix are supported, no subdomains",
            "name",
        )


def check_rrset_name(zone: Zone, name: str) -> None:
    """Refuse an RRSet name that is not "@" or a name relative to the zone."""
    if name == "@":
        return
    labels = name.split(".")
    if (
        name != name.lower()
        or name.endswith(zone["name"])
        or not all(LABEL.match(label) or label == "*" for label in labels)
        or "*" in labels[1:]
    ):
        raise invalid("must be @ or a name relative to the zone, in lower case", "name")


def check_records(zone: Zone, rr_type: str, records: list[dict]) -> list[DnsRecord]:
    """Return the records of a request, each value checked for its type.

    The values must be distinct and valid in zone file syntax (relative names are
    relative to the zone).
    """
    if not records:
        raise invalid("records must not be empty", "records")
    values = [r.get("value", "") for r in records]
    if len(set(values)) != len(values):
        raise invalid("record values must be distinct", "records")
    origin = dns.name.from_text(zone["name"])
    for value in values:
        try:
            dns.rdata.from_text(
                dns.rdataclass.IN, dns.rdatatype.from_text(rr_type), value, origin
            )
        except (dns.exception.DNSException, ValueError) as error:
            raise invalid(
                f"invalid {rr_type} value {value!r}: {error}", "records"
            ) from None
    return [{"value": r["value"], "comment": r.get("comment") or ""} for r in records]


def check_cname(lab: Lab, zone: Zone, name: str, rr_type: str) -> None:
    """Refuse a CNAME next to other records of its name, and at the zone's apex."""
    others = {r["type"] for r in rrsets_of(lab, zone) if r["name"] == name} - {rr_type}
    if rr_type == "CNAME" and (name == "@" or others):
        raise invalid("a CNAME cannot share its name with other records", "type")
    if rr_type != "CNAME" and "CNAME" in others:
        raise invalid("the name has a CNAME, which allows no other records", "type")


def check_changeable(rrset: RRSet) -> None:
    """Refuse a change of an RRSet that is change protected."""
    if rrset["protection"]["change"]:
        raise protected("RRSet is change protected")


def refuse_soa(rr_type: str) -> None:
    """Refuse to create, delete, add to or remove from the SOA.

    It comes and goes with its zone and has one record, which set_records may
    overwrite; the zone counts its serial on.
    """
    if rr_type == "SOA":
        raise invalid("the SOA record comes and goes with the zone", "type")


# --- lookups --------------------------------------------------------------------------


def zone_or_404(lab: Lab, id_or_name: str) -> Zone:
    """Return the zone that the path names by ID or by name, or raise 404."""
    for zone in lab.state[ZONES].values():
        if str(zone["id"]) == id_or_name or zone["name"] == id_or_name:
            return zone
    raise not_found("zone")


def rrsets_of(lab: Lab, zone: Zone) -> list[RRSet]:
    """Return the RRSets of a zone."""
    return [r for r in lab.state[RRSETS].values() if r["zone"] == zone["id"]]


def find_rrset(lab: Lab, zone: Zone, name: str, rr_type: str) -> RRSet | None:
    """Return the RRSet of this name and type in the zone, or None."""
    return next(
        (r for r in rrsets_of(lab, zone) if (r["name"], r["type"]) == (name, rr_type)),
        None,
    )


def rrset_or_404(lab: Lab, zone: Zone, name: str, rr_type: str) -> RRSet:
    """Return the RRSet of this name and type in the zone, or raise 404."""
    rrset = find_rrset(lab, zone, name, rr_type)
    if rrset is None:
        raise not_found("RRSet")
    return rrset


# --- changes (each inside the caller's state.change() block) --------------------------


def add_rrset(
    lab: Lab, zone: Zone, name: str, rr_type: str, ttl, records, labels=None
) -> RRSet:
    """Add an RRSet to the zone after checking it; it must not exist yet."""
    check_rrset_name(zone, name)
    if find_rrset(lab, zone, name, rr_type) is not None:
        raise not_unique(f"RRSet {name}/{rr_type} already exists", "name")
    check_cname(lab, zone, name, rr_type)
    rid = lab.state.new_id(RRSETS)
    rrset: RRSet = {
        "id": rid,
        "zone": zone["id"],
        "name": name,
        "type": rr_type,
        "ttl": ttl,
        "labels": labels or {},
        "protection": {"change": False},
        "records": check_records(zone, rr_type, records),
        "created": now(),
    }
    lab.state[RRSETS][rid] = rrset
    return rrset


def bump_serial(lab: Lab, zone: Zone) -> None:
    """Count the zone's SOA serial up, as Hetzner does: YYYYMMDDnn."""
    soa = find_rrset(lab, zone, "@", "SOA")
    if soa is None:
        return
    fields = soa["records"][0]["value"].split()
    today = int(datetime.now(timezone.utc).strftime("%Y%m%d") + "01")
    fields[2] = str(max(int(fields[2]) + 1, today))
    soa["records"][0]["value"] = " ".join(fields)


def default_rrsets(lab: Lab, zone: Zone) -> None:
    """Give a new zone Hetzner's SOA and its three NS records."""
    serial = datetime.now(timezone.utc).strftime("%Y%m%d") + "00"
    soa = f"{hetzner.NAMESERVERS[0]} {SOA_CONTACT} {serial} {SOA_TIMERS}"
    add_rrset(lab, zone, "@", "SOA", None, [{"value": soa}])
    add_rrset(lab, zone, "@", "NS", None, [{"value": n} for n in hetzner.NAMESERVERS])


def parsed_zonefile(zone: Zone, text: str) -> tuple[int | None, list[tuple]]:
    """Return a zone file's $TTL and its RRSets as (name, type, ttl, values).

    Hetzner's rules: $ORIGIN, if given, is the zone; only IN records; an SOA and
    Hetzner's NS records are there. An RRSet whose TTL is the default gets none.
    """
    origin = dns.name.from_text(zone["name"])
    given_origin = re.search(r"^\$ORIGIN\s+(\S+)", text, re.MULTILINE)
    if given_origin and given_origin.group(1).lower() != f"{zone['name']}.":
        raise invalid("$ORIGIN must be the zone's name with a final dot", "zonefile")
    given_ttl = re.search(r"^\$TTL\s+(\d+)", text, re.MULTILINE)
    default_ttl = int(given_ttl.group(1)) if given_ttl else zone["ttl"]
    try:
        parsed = dns.zone.from_text(text, origin=origin, relativize=True)
    except dns.exception.DNSException as error:
        raise invalid(f"invalid zone file: {error}", "zonefile") from None
    out = []
    for name, node in parsed.nodes.items():
        for rdataset in node.rdatasets:
            if rdataset.rdclass != dns.rdataclass.IN:
                raise invalid("only IN records are allowed", "zonefile")
            values = [r.to_text(origin=origin, relativize=False) for r in rdataset]
            ttl = None if rdataset.ttl == default_ttl else rdataset.ttl
            out.append(
                (name.to_text(), dns.rdatatype.to_text(rdataset.rdtype), ttl, values)
            )
    ns = next((v for n, t, _, v in out if (n, t) == ("@", "NS")), [])
    if not set(hetzner.NAMESERVERS) <= set(ns):
        raise invalid(
            "Hetzner's nameservers must be NS records of the zone", "zonefile"
        )
    return (int(given_ttl.group(1)) if given_ttl else None), out


def replace_rrsets(lab: Lab, zone: Zone, text: str) -> None:
    """Replace all RRSets of a zone with a zone file's; refused if one is protected."""
    if any(r["protection"]["change"] for r in rrsets_of(lab, zone)):
        raise protected("an RRSet of the zone is change protected")
    ttl, rrsets = parsed_zonefile(zone, text)
    for rrset in rrsets_of(lab, zone):
        del lab.state[RRSETS][rrset["id"]]
    if ttl is not None:
        zone["ttl"] = ttl
    for name, rr_type, rr_ttl, values in rrsets:
        add_rrset(lab, zone, name, rr_type, rr_ttl, [{"value": v} for v in values])
    bump_serial(lab, zone)


# --- zones ----------------------------------------------------------------------------


def create_zone(lab: Lab, suffixes: PublicSuffixes, body: dict) -> Zone:
    """Create a zone with its default records, and the RRSets or zone file given."""
    if body.get("mode") != "primary":
        raise ApiError(
            422, "unsupported", "the lab supports zones in primary mode only"
        )
    name = body.get("name") or ""
    check_zone_name(suffixes, name)
    state = lab.state
    with state.change():
        if any(z["name"] == name for z in state[ZONES].values()):
            raise not_unique("zone already exists", "name")
        zid = state.new_id(ZONES)
        zone: Zone = {
            "id": zid,
            "name": name,
            "created": now(),
            "ttl": body.get("ttl") or DEFAULT_TTL,
            "labels": body.get("labels") or {},
            "protection": {"delete": False},
        }
        state[ZONES][zid] = zone
        default_rrsets(lab, zone)
        if body.get("zonefile"):
            replace_rrsets(lab, zone, body["zonefile"])
        for given in body.get("rrsets") or []:
            add_rrset(
                lab,
                zone,
                given["name"],
                given["type"],
                given.get("ttl"),
                given["records"],
                given.get("labels"),
            )
    return zone


def delete_zone(lab: Lab, zone: Zone) -> None:
    """Delete a zone and its RRSets unless it is delete protected."""
    if zone["protection"]["delete"]:
        raise protected("zone is delete protected")
    with lab.state.change():
        for rrset in rrsets_of(lab, zone):
            del lab.state[RRSETS][rrset["id"]]
        del lab.state[ZONES][zone["id"]]


async def change_ttl(lab: Lab, zone: Zone, body: dict) -> Plan:
    """Set the zone's default TTL."""
    with lab.state.change():
        zone["ttl"] = int(body["ttl"])
        bump_serial(lab, zone)
    return Plan()


async def import_zonefile(lab: Lab, zone: Zone, body: dict) -> Plan:
    """Replace the zone's RRSets with a zone file's."""
    with lab.state.change():
        replace_rrsets(lab, zone, body.get("zonefile") or "")
    return Plan()


async def change_primary_nameservers(lab: Lab, zone: Zone, body: dict) -> Plan:
    """Refuse: the lab's zones are all in primary mode."""
    raise ApiError(
        422, "incorrect_zone_mode", "only zones in secondary mode have primaries"
    )


# the zone actions: Hetzner's name of the action and the function that plans it
ZONE_ACTIONS: ActionTable[Zone] = {
    "change_protection": ActionSpec("change_protection", change_delete_protection),
    "change_ttl": ActionSpec("change_ttl", change_ttl),
    "import_zonefile": ActionSpec("import_zonefile", import_zonefile),
    "change_primary_nameservers": ActionSpec(
        "change_primary_nameservers", change_primary_nameservers
    ),
}


# --- RRSet actions: the route puts the RRSet's (name, type) into body["rrset"] -------


def target(lab: Lab, zone: Zone, body: dict) -> RRSet:
    """Return the RRSet an action is about, if it may be changed."""
    rrset = rrset_or_404(lab, zone, *body["rrset"])
    check_changeable(rrset)
    return rrset


async def rrset_change_protection(lab: Lab, zone: Zone, body: dict) -> Plan:
    """Set or clear the RRSet's change protection."""
    rrset = rrset_or_404(lab, zone, *body["rrset"])
    with lab.state.change():
        rrset["protection"]["change"] = bool(body["change"])
    return Plan()


async def rrset_change_ttl(lab: Lab, zone: Zone, body: dict) -> Plan:
    """Set the RRSet's TTL; None makes it use the zone's."""
    rrset = target(lab, zone, body)
    with lab.state.change():
        rrset["ttl"] = body.get("ttl")
        bump_serial(lab, zone)
    return Plan()


async def set_records(lab: Lab, zone: Zone, body: dict) -> Plan:
    """Replace the RRSet's records (the SOA's one record, too)."""
    rrset = target(lab, zone, body)
    records = check_records(zone, rrset["type"], body.get("records") or [])
    if rrset["type"] == "SOA" and len(records) != 1:
        raise invalid("the SOA has exactly one record", "records")
    with lab.state.change():
        rrset["records"] = records
        bump_serial(lab, zone)
    return Plan()


async def add_records(lab: Lab, zone: Zone, body: dict) -> Plan:
    """Add records to the RRSet, which is created if it does not exist.

    A TTL given for an RRSet that has one must be the same.
    """
    name, rr_type = body["rrset"]
    refuse_soa(rr_type)
    rrset = find_rrset(lab, zone, name, rr_type)
    ttl = body.get("ttl")
    with lab.state.change():
        if rrset is None:
            add_rrset(lab, zone, name, rr_type, ttl, body.get("records") or [])
        else:
            check_changeable(rrset)
            if ttl is not None and rrset["ttl"] not in (None, ttl):
                raise invalid("the TTL differs from the RRSet's", "ttl")
            old = {r["value"] for r in rrset["records"]}
            new = check_records(zone, rr_type, body.get("records") or [])
            if old & {r["value"] for r in new}:
                raise invalid("a record is in the RRSet already", "records")
            rrset["records"] += new
            if ttl is not None:
                rrset["ttl"] = ttl
        bump_serial(lab, zone)
    return Plan()


async def remove_records(lab: Lab, zone: Zone, body: dict) -> Plan:
    """Remove records from the RRSet; an RRSet left empty is deleted."""
    refuse_soa(body["rrset"][1])
    rrset = target(lab, zone, body)
    values = {r.get("value") for r in body.get("records") or []}
    if not values <= {r["value"] for r in rrset["records"]}:
        raise invalid("a record is not in the RRSet", "records")
    with lab.state.change():
        rrset["records"] = [r for r in rrset["records"] if r["value"] not in values]
        if not rrset["records"]:
            del lab.state[RRSETS][rrset["id"]]
        bump_serial(lab, zone)
    return Plan()


async def update_records(lab: Lab, zone: Zone, body: dict) -> Plan:
    """Change the comments of records of the RRSet (the values name them)."""
    rrset = target(lab, zone, body)
    comments = {
        r.get("value"): r.get("comment") or "" for r in body.get("records") or []
    }
    if not set(comments) <= {r["value"] for r in rrset["records"]}:
        raise invalid("a record is not in the RRSet", "records")
    with lab.state.change():
        for record in rrset["records"]:
            record["comment"] = comments.get(record["value"], record["comment"])
    return Plan()


# the RRSet actions, run on their zone
RRSET_ACTIONS: ActionTable[Zone] = {
    "change_protection": ActionSpec("change_rrset_protection", rrset_change_protection),
    "change_ttl": ActionSpec("change_rrset_ttl", rrset_change_ttl),
    "set_records": ActionSpec("set_rrset_records", set_records),
    "add_records": ActionSpec("add_rrset_records", add_records),
    "remove_records": ActionSpec("remove_rrset_records", remove_records),
    "update_records": ActionSpec("update_rrset_records", update_records),
}


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the routes of zones and RRSets to the app."""
    state = lab.state
    suffixes = load_public_suffixes()  # at the start: no file reads on the loop later

    @app.get("/v1/zones")
    async def zone_list(request: Request):
        items = filtered(
            request,
            list(state[ZONES].values()),
            {"mode": lambda zone, value: "primary" in value.split(",")},
        )
        return paginate(request, [hetzner.zone_json(lab, z) for z in items], "zones")

    @app.post("/v1/zones", status_code=201)
    async def zone_create(request: Request):
        zone = create_zone(lab, suffixes, await read_body(request))
        answer = {
            "zone": hetzner.zone_json(lab, zone),
            "action": lab.finished_action("create_zone", [(zone["id"], "zone")]),
        }
        await system.apply_dns(lab)
        return answer

    @app.get("/v1/zones/{id_or_name}")
    async def zone_get(id_or_name: str):
        return {"zone": hetzner.zone_json(lab, zone_or_404(lab, id_or_name))}

    @app.put("/v1/zones/{id_or_name}")
    async def zone_update(id_or_name: str, request: Request):
        body = await read_body(request)
        zone = zone_or_404(lab, id_or_name)
        with state.change():
            update_fields(zone, body, ("labels",))
        return {"zone": hetzner.zone_json(lab, zone)}

    @app.delete("/v1/zones/{id_or_name}", status_code=201)
    async def zone_delete(id_or_name: str):
        zone = zone_or_404(lab, id_or_name)
        zid = zone["id"]
        delete_zone(lab, zone)
        answer = {"action": lab.finished_action("delete_zone", [(zid, "zone")])}
        await system.apply_dns(lab)
        return answer

    @app.get("/v1/zones/{id_or_name}/zonefile")
    async def zone_file(id_or_name: str):
        zone = zone_or_404(lab, id_or_name)
        return {"zonefile": desired.zone_text(state, zone)}

    @app.post("/v1/zones/{id_or_name}/actions/{cmd}", status_code=201)
    async def zone_action(id_or_name: str, cmd: str, request: Request):
        body = await read_body(request)
        zone = zone_or_404(lab, id_or_name)
        answer = await lab.act(ZONE_ACTIONS, cmd, zone, "zone", body)
        await system.apply_dns(lab)
        return answer

    @app.get("/v1/zones/{id_or_name}/rrsets")
    async def rrset_list(id_or_name: str, request: Request):
        zone = zone_or_404(lab, id_or_name)
        items = filtered(
            request,
            rrsets_of(lab, zone),
            {"type": lambda rrset, value: rrset["type"] in value.split(",")},
        )
        return paginate(request, [hetzner.rrset_json(r) for r in items], "rrsets")

    @app.post("/v1/zones/{id_or_name}/rrsets", status_code=201)
    async def rrset_create(id_or_name: str, request: Request):
        body = await read_body(request)
        zone = zone_or_404(lab, id_or_name)
        refuse_soa(body.get("type") or "")
        with state.change():
            rrset = add_rrset(
                lab,
                zone,
                body.get("name") or "",
                body.get("type") or "",
                body.get("ttl"),
                body.get("records") or [],
                body.get("labels"),
            )
            bump_serial(lab, zone)
        answer = {
            "rrset": hetzner.rrset_json(rrset),
            "action": lab.finished_action("create_rrset", [(zone["id"], "zone")]),
        }
        await system.apply_dns(lab)
        return answer

    @app.get("/v1/zones/{id_or_name}/rrsets/{rr_name}/{rr_type}")
    async def rrset_get(id_or_name: str, rr_name: str, rr_type: str):
        zone = zone_or_404(lab, id_or_name)
        return {"rrset": hetzner.rrset_json(rrset_or_404(lab, zone, rr_name, rr_type))}

    @app.put("/v1/zones/{id_or_name}/rrsets/{rr_name}/{rr_type}")
    async def rrset_update(
        id_or_name: str, rr_name: str, rr_type: str, request: Request
    ):
        body = await read_body(request)
        rrset = rrset_or_404(lab, zone_or_404(lab, id_or_name), rr_name, rr_type)
        with state.change():
            update_fields(rrset, body, ("labels",))
        return {"rrset": hetzner.rrset_json(rrset)}

    @app.delete("/v1/zones/{id_or_name}/rrsets/{rr_name}/{rr_type}", status_code=201)
    async def rrset_delete(id_or_name: str, rr_name: str, rr_type: str):
        zone = zone_or_404(lab, id_or_name)
        rrset = rrset_or_404(lab, zone, rr_name, rr_type)
        refuse_soa(rr_type)
        check_changeable(rrset)
        with state.change():
            del state[RRSETS][rrset["id"]]
            bump_serial(lab, zone)
        answer = {"action": lab.finished_action("delete_rrset", [(zone["id"], "zone")])}
        await system.apply_dns(lab)
        return answer

    @app.post(
        "/v1/zones/{id_or_name}/rrsets/{rr_name}/{rr_type}/actions/{cmd}",
        status_code=201,
    )
    async def rrset_action(
        id_or_name: str, rr_name: str, rr_type: str, cmd: str, request: Request
    ):
        body = {**await read_body(request), "rrset": (rr_name, rr_type)}
        zone = zone_or_404(lab, id_or_name)
        answer = await lab.act(RRSET_ACTIONS, cmd, zone, "zone", body)
        await system.apply_dns(lab)
        # Hetzner answers update_records with 200, the other actions with 201
        return JSONResponse(answer, status_code=200 if cmd == "update_records" else 201)
