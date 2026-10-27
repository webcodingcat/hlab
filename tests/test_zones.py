"""DNS zones and RRSets, and the files the lab's nameserver serves them from."""

import pytest

from hlabd import config
from hlabd.api.zones import PublicSuffixes


def create_zone(api, name="example.com", **extra):
    """Create a zone in primary mode and return it."""
    response = api.post("/v1/zones", json={"name": name, "mode": "primary", **extra})
    assert response.status_code == 201, response.text
    return response.json()["zone"]


def rrset_action(api, path, cmd, body, status=201):
    """Run an RRSet action and return the answer."""
    response = api.post(f"/v1/zones/example.com/rrsets/{path}/actions/{cmd}", json=body)
    assert response.status_code == status, response.text
    return response.json()


def serial(api):
    """Return the SOA serial of example.com."""
    soa = api.get("/v1/zones/example.com/rrsets/@/SOA").json()["rrset"]
    return int(soa["records"][0]["value"].split()[2])


def test_new_zone_gets_hetzners_soa_and_nameservers_and_is_served(api, tmp_path):
    zone = create_zone(api)
    assert zone["record_count"] == 4 and zone["ttl"] == 3600
    assert (
        zone["authoritative_nameservers"]["assigned"][0] == "hydrogen.ns.hetzner.com."
    )
    rrsets = api.get("/v1/zones/example.com/rrsets").json()["rrsets"]
    assert sorted(r["id"] for r in rrsets) == ["@/NS", "@/SOA"]
    zone_file = (tmp_path / "dns" / "zones" / "example.com.zone").read_text()
    assert "@\tIN\tNS\thydrogen.ns.hetzner.com." in zone_file
    resolver = (tmp_path / "dns" / "resolver.conf").read_text()
    assert "server=/example.com/127.0.0.1#5353" in resolver
    assert api.sh.commands("start-nsd")


@pytest.mark.parametrize(
    "name", ["sub.example.com", "example.test", "Example.com", "example.com."]
)
def test_only_registrable_domains_in_lower_case_become_zones(api, name):
    # Hetzner: well-known public suffixes only, no subdomains
    response = api.post("/v1/zones", json={"name": name, "mode": "primary"})
    assert response.json()["error"]["code"] == "invalid_input"


def test_zones_below_a_longer_suffix_and_twice_and_secondary(api):
    create_zone(api, "example.co.uk")
    response = api.post("/v1/zones", json={"name": "example.co.uk", "mode": "primary"})
    assert response.json()["error"]["code"] == "uniqueness_error"
    response = api.post(
        "/v1/zones",
        json={
            "name": "example.org",
            "mode": "secondary",
            "primary_nameservers": [{"address": "198.51.100.1"}],
        },
    )
    assert response.json()["error"]["code"] == "unsupported"


def test_rrsets_are_created_checked_and_count_the_serial_up(api):
    create_zone(api)
    before = serial(api)
    response = api.post(
        "/v1/zones/example.com/rrsets",
        json={"name": "www", "type": "A", "records": [{"value": "203.0.113.10"}]},
    )
    assert response.status_code == 201
    assert response.json()["rrset"]["id"] == "www/A"
    assert serial(api) > before
    bad = api.post(
        "/v1/zones/example.com/rrsets",
        json={"name": "mail", "type": "A", "records": [{"value": "203.0.113"}]},
    )
    assert bad.json()["error"]["code"] == "invalid_input"
    # a CNAME allows no other records of its name
    cname = api.post(
        "/v1/zones/example.com/rrsets",
        json={"name": "www", "type": "CNAME", "records": [{"value": "example.com."}]},
    )
    assert cname.json()["error"]["code"] == "invalid_input"
    again = api.post(
        "/v1/zones/example.com/rrsets",
        json={"name": "www", "type": "A", "records": [{"value": "203.0.113.11"}]},
    )
    assert again.json()["error"]["code"] == "uniqueness_error"


def test_records_are_added_set_commented_and_removed(api):
    create_zone(api)
    # Hetzner: add_records creates a missing RRSet, an empty one goes
    challenge = {"value": '"token-1"'}
    rrset_action(api, "_acme-challenge/TXT", "add_records", {"records": [challenge]})
    rrset_action(
        api, "_acme-challenge/TXT", "add_records", {"records": [{"value": '"token-2"'}]}
    )
    rrset_action(
        api,
        "_acme-challenge/TXT",
        "update_records",
        {"records": [{"value": '"token-1"', "comment": "first"}]},
        status=200,  # Hetzner answers this one with 200
    )
    txt = api.get("/v1/zones/example.com/rrsets/_acme-challenge/TXT").json()["rrset"]
    assert txt["records"] == [
        {"value": '"token-1"', "comment": "first"},
        {"value": '"token-2"', "comment": ""},
    ]
    rrset_action(api, "_acme-challenge/TXT", "set_records", {"records": [challenge]})
    rrset_action(api, "_acme-challenge/TXT", "remove_records", {"records": [challenge]})
    response = api.get("/v1/zones/example.com/rrsets/_acme-challenge/TXT")
    assert response.status_code == 404


def test_protected_rrsets_and_the_soa_stay_as_they_are(api):
    create_zone(api)
    rrset_action(api, "www/A", "add_records", {"records": [{"value": "203.0.113.10"}]})
    rrset_action(api, "www/A", "change_protection", {"change": True})
    response = api.post(
        "/v1/zones/example.com/rrsets/www/A/actions/set_records",
        json={"records": [{"value": "203.0.113.11"}]},
    )
    assert response.json()["error"]["code"] == "protected"
    assert (
        api.delete("/v1/zones/example.com/rrsets/www/A").json()["error"]["code"]
        == "protected"
    )
    soa = api.delete("/v1/zones/example.com/rrsets/@/SOA")
    assert soa.json()["error"]["code"] == "invalid_input"


def test_the_soa_is_overwritten_but_its_serial_counts_on(api):
    # the hcloud provider sets the SOA with serial 0; Hetzner keeps counting
    create_zone(api)
    before = serial(api)
    soa = "hydrogen.ns.hetzner.com. dns.hetzner.com. 0 86400 10800 3600000 300"
    rrset_action(api, "@/SOA", "set_records", {"records": [{"value": soa}]})
    assert serial(api) > before
    value = api.get("/v1/zones/example.com/rrsets/@/SOA").json()["rrset"]["records"]
    assert value[0]["value"].endswith(" 3600000 300")
    response = api.post(
        "/v1/zones/example.com/rrsets/@/SOA/actions/add_records",
        json={"records": [{"value": soa.replace(" 0 ", " 1 ")}]},
    )
    assert response.json()["error"]["code"] == "invalid_input"


def test_zone_file_is_exported_and_imported(api):
    create_zone(api)
    rrset_action(api, "www/A", "add_records", {"records": [{"value": "203.0.113.10"}]})
    exported = api.get("/v1/zones/example.com/zonefile").json()["zonefile"]
    assert "$ORIGIN\texample.com." in exported
    assert "www\tIN\tA\t203.0.113.10" in exported
    zonefile = exported.replace("$TTL\t3600", "$TTL\t600").replace(
        "www\tIN\tA\t203.0.113.10", "api\tIN\t60\tA\t203.0.113.20"
    )
    rrset_action_answer = api.post(
        "/v1/zones/example.com/actions/import_zonefile", json={"zonefile": zonefile}
    )
    assert rrset_action_answer.status_code == 201, rrset_action_answer.text
    names = {r["id"] for r in api.get("/v1/zones/example.com/rrsets").json()["rrsets"]}
    assert names == {"@/SOA", "@/NS", "api/A"}
    assert api.get("/v1/zones/example.com").json()["zone"]["ttl"] == 600
    api_rrset = api.get("/v1/zones/example.com/rrsets/api/A").json()["rrset"]
    assert api_rrset["ttl"] == 60


def test_zone_file_needs_hetzners_nameservers_and_its_origin(api):
    create_zone(api)
    exported = api.get("/v1/zones/example.com/zonefile").json()["zonefile"]
    path = "/v1/zones/example.com/actions/import_zonefile"
    without_ns = "\n".join(
        line for line in exported.splitlines() if "\tNS\t" not in line
    )
    response = api.post(path, json={"zonefile": without_ns})
    assert response.json()["error"]["code"] == "invalid_input"
    other_origin = exported.replace("$ORIGIN\texample.com.", "$ORIGIN\texample.org.")
    response = api.post(path, json={"zonefile": other_origin})
    assert response.json()["error"]["code"] == "invalid_input"


def test_zone_is_protected_then_deleted_with_its_files(api, tmp_path):
    zone = create_zone(api)
    api.post("/v1/zones/example.com/actions/change_protection", json={"delete": True})
    assert api.delete("/v1/zones/example.com").json()["error"]["code"] == "protected"
    api.post("/v1/zones/example.com/actions/change_protection", json={"delete": False})
    assert api.delete(f"/v1/zones/{zone['id']}").status_code == 201
    assert not (tmp_path / "dns" / "zones" / "example.com.zone").exists()
    actions = api.get("/v1/zones/actions").json()["actions"]
    assert {a["command"] for a in actions} >= {"create_zone", "delete_zone"}


def test_actions_of_a_zone_are_found_by_its_name(api):
    zone = create_zone(api)
    actions = api.get("/v1/zones/example.com/actions").json()["actions"]
    assert actions[0]["resources"] == [{"id": zone["id"], "type": "zone"}]


def test_the_dns_setting_is_gone(tmp_path):
    config_file = tmp_path / "hlab.yaml"
    config_file.write_text("dns: {example.test: 203.0.113.10}\n")
    with pytest.raises(ValueError, match="create a DNS zone"):
        config.load(config_file)


def test_public_suffix_rules_with_wildcards_and_exceptions():
    suffixes = PublicSuffixes("// a comment\ncom\nco.uk\n*.ck\n!www.ck\n")
    assert suffixes.is_registrable("example.com")
    assert not suffixes.is_registrable("www.example.com")
    assert suffixes.is_registrable("example.co.uk")
    assert not suffixes.is_registrable("co.uk")
    assert suffixes.is_registrable("shop.example.ck")  # *.ck: example.ck is a suffix
    assert suffixes.is_registrable("www.ck")  # the exception
    # Hetzner takes well-known suffixes only, not the list's default rule
    assert not suffixes.is_registrable("example.unlisted")
