# Phase 07 research: DNS zones and reverse DNS

Date of research: 2026-11-19. No live API calls were possible in this run (no shell, no token). Everything marked UNCONFIRMED must be verified against the real Hetzner API (hcloud context `hlab-docs`) before the plan relies on it. A cheap probe list is at the end.

Sources (abbreviated below):
- OPENAPI = https://docs.hetzner.cloud/cloud.spec.json (zones section, character offset ~3.0M to 3.5M)
- HCLOUD-GO = https://github.com/hetznercloud/hcloud-go (hcloud/zone.go, zone_rrset.go, error.go, schema/*)
- CLI = https://github.com/hetznercloud/cli/tree/v1.70.2/internal/cmd/zone
- DOCS = https://docs.hetzner.com/networking/dns/...

## 1. RRSet and record validation

Confirmed (OPENAPI):
- RRSet types (enum): A, AAAA, CAA, CNAME, DS, HINFO, HTTPS, MX, NS, PTR, RP, SOA, SRV, SVCB, TLSA, TXT.
- RRSet `name`: lowercase, must NOT end with a dot or with the zone name, apex is `@`, non-ASCII must be Punycode (ACE). Relative names only. A third-party client reports that the API appends the zone name to whatever it receives, so a full FQDN yields `host.zone.zone` (https://github.com/chbister/com.mybister.apiservices/pull/7).
- RRSet id is `<name>/<type>` (e.g. `www/A`); paths are `/zones/{id_or_name}/rrsets/{rr_name}/{rr_type}`.
- `records`: required on create, "must not be empty and must only contain distinct record values", order not guaranteed in responses. Each record has `value` (required) and `comment` (optional). At most 50 records per RRSet (stated on set_records).
- `ttl`: 60..2147483647; null means zone default (zone default 3600). TTL 0 is invalid.
- Record `value` format: the OpenAPI only points to the DNS record types docs. DOCS (https://docs.hetzner.com/networking/dns/record-types/overview and /txt-record/) give only examples:
  - TXT: value must be quoted; may consist of several quoted substrings (`"hello " "world"`); each substring max 255 characters.
  - CNAME, MX, NS, SRV, HTTPS, SVCB targets: a trailing dot makes a name absolute; without it the zone name is appended (docs.hetzner.com/networking/dns/faq/records/: "especially important with CNAME, HTTPS, MX, NS, SRV, SVCB"). For HTTPS the example target is `.`. Relative targets are therefore legal but resolve inside the zone.
  - HTTPS/SVCB example params: `ipv4hint=203.0.113.1 alpn="h3,h2"`. CAA example: `0 issue "letsencrypt.org"` (Ansible zonefile sample). HINFO example `"Intel" "Ubuntu"`. SOA example: `hydrogen.ns.hetzner.com. dns.hetzner.com. 2024010100 86400 10800 3600000 3600`.
  - The docs give no field structure for TLSA, DS, RP (the RP example `admin@example.com` looks like an email form; whether the API wants DNS mbox form `admin.example.com.` is UNCONFIRMED).
- Migration docs (search summary of docs.hetzner.com/networking/dns/migration-to-hetzner-console/process, page itself returned 404 on direct fetch): TXT values must be fully quoted (error field `zonefile.records[0].value`), duplicate values and uppercase names are rejected, TTL below 60 rejected, "Zone has missing or invalid nameserver sets" (fix: remove NS records not assigned to the zone), 400 "Invalid delegation of parent zone".
- Error envelope (OPENAPI, HCLOUD-GO error.go): `{"error":{"code","message","details"}}`. For `invalid_input`, `details.fields` is a list of `{name, messages[]}` (ErrorDetailsInvalidInput). Zone-specific code documented: `incorrect_zone_mode` (HTTP 422, "This operation is not supported for this Zone's mode"). Other relevant generic codes: `not_found`, `invalid_input`, `uniqueness_error`, `protected`, `conflict`, `locked`, `resource_limit_exceeded`, `rate_limit_exceeded`. hcloud-go also has `dns_zone_not_found`, `dns_zone_is_secondary_zone` (certificate-related).
- CNAME conflict: a third-party client documents HTTP 422 with message `(name, CNAME) conflicts with (name, A)` (https://github.com/guarnacciaa/hetzner-ddns). Field name and code (presumably `invalid_input`) UNCONFIRMED.
- Creating an RRSet that already exists: a third-party client treats HTTP 409 as "already exists" and falls back to set_records. Code string (`uniqueness_error` vs `conflict`) UNCONFIRMED.
- `add_records` creates the RRSet implicitly if missing; `remove_records` deletes the RRSet when the last record is removed. `set_records` overwrites an existing RRSet (empty list not allowed). `update_records` only changes comments (each item needs both `value` and `comment`; returns 200, others 201).
- Change protection on an RRSet (`protection.change`) makes modification/deletion fail; zone import fails if existing RRSets are change-protected (OPENAPI). Error code for protection is likely `protected` (UNCONFIRMED).
- Apex SOA/NS: DOCS say "The SOA record and three NS records are always created automatically" and new zones have no other default records. The API enum allows SOA and NS RRSets, but which changes are refused (editing SOA values, removing/replacing the apex NS set, TTL changes) is UNCONFIRMED. Import requires the assigned Hetzner NS records and a SOA record (OPENAPI). Hetzner increments the SOA serial itself in primary mode.

## 2. Zone names

- OPENAPI: name must be lowercase, no trailing dot, max 255 characters, Punycode for IDN. "Well-known public suffixes are supported"; "Subdomains are not supported". FAQ (docs.hetzner.com/networking/dns/faq/zones/): "Why can the domain <sub.domain.tld> not be created? (unknown TLD)" answer "Subzones are not supported."
- Whether only the ICANN section of the Public Suffix List counts or also the private section (e.g. `github.io`, `example.dev` style private suffixes): UNCONFIRMED. Whether `.test`, `.internal`, `.lan`, `.example` or other non-delegated TLDs are accepted: UNCONFIRMED (the FAQ wording "unknown TLD" suggests refused).
- `in-addr.arpa` / `ip6.arpa` zones: UNCONFIRMED. DOCS say PTR records live in reverse zones owned by the IP owner and "you should not create PTR records for IP addresses with Hetzner". Likely refused; treat as unsupported until probed.
- Error code and field for refused names: UNCONFIRMED; expect HTTP 422 `invalid_input` with `details.fields[0].name == "name"`. Existing-zone-elsewhere case: "Zone already claimed by somebody else" (FAQ, wording only).
- hcloud CLI converts the zone name to ASCII client side (`util.ParseZoneIDOrName`), error "failed to convert Zone name to ascii".

## 3. Reverse DNS

- Default name for IPv4: `static.<d>.<c>.<b>.<a>.clients.your-server.de` for address a.b.c.d (reversed octets, dots). Confirmed by docs example `static.1.113.0.203.clients.your-server.de` (docs.hetzner.com/dns-console/dns/general/reverse-dns/) and by a real hostname `49.13.4.8 -> static.8.4.13.49.clients.your-server.de` (https://worldip.io/49.13.4.8). Ansible hrobot doc: deleting the main IPv4 rDNS "actually sets it to a default hostname like static.X.Y.Z.W.clients.your-server.de". hcloud-go: passing a nil dns_ptr to change_dns_ptr resets to default.
- Server `public_net.ipv4.dns_ptr` is a single string (HCLOUD-GO schema/server.go). Server `public_net.ipv6.dns_ptr` is an ARRAY of `{ip, dns_ptr}`; `ipv6.ip` is the /64 network (e.g. `2001:db8::/64`), and entries only exist for addresses that have been set explicitly (assumption from the schema, UNCONFIRMED whether a default entry is present). Load balancer `public_net.ipv4.dns_ptr` and `ipv6.dns_ptr` are single strings or null.
- Primary IP and Floating IP: `dns_ptr` is an array of `{ip, dns_ptr}` (HCLOUD-GO schema/primary_ip.go, floating_ip.go). Change request `{ip, dns_ptr}` with `dns_ptr: null` to reset. Docs say Primary IPs and Floating IPs can be edited.
- Whether a fresh Floating IP has a default PTR at all, and the exact default for Primary IPs (assumed same `static....` pattern as the server IP): UNCONFIRMED.
- Primary IPv4 of a server: deleting the PTR is not possible; the API puts the default back (Ansible hrobot note refers to Robot, hcloud rdns module says omitting the value resets to default).

## 4. Zone file import and export

- Export: `GET /zones/{id_or_name}/zonefile` -> `{"zonefile": "<text>"}`; BIND (RFC 1034/1035) format; only primary zones; secondary returns 422 `incorrect_zone_mode`. CLI `hcloud zone export-zonefile <zone>` prints the raw text; with `--json` prints `{"zonefile": "..."}`.
- Exact text (record order, TTL column, SOA form) of a real export: UNCONFIRMED. The only fixture in hand is the CLI test file (testdata/example.zone), which is hand-made and probably mimics the real thing:
  ```
  $ORIGIN	example.com.
  $TTL	3600
  @	IN	SOA	hydrogen.ns.hetzner.com. dns.hetzner.com. 2024010100 86400 10800 3600000 3600
  @	IN	10800	NS	hydrogen.ns.hetzner.com. ; Some comment.
  @	IN	10800	NS	oxygen.ns.hetzner.com.
  @	IN	10800	NS	helium.ns.hetzner.de.
  ```
  Note the unusual `IN <ttl>` order (TTL after class); comments after `;` carry the record comment. A parser for export must handle `$ORIGIN`, `$TTL`, TTL either before or after the class, relative names and `@`.
- Import: `POST /zones/{id}/actions/import_zonefile` body `{"zonefile": "..."}`; replaces ALL RRSets; fails if existing RRSets are change-protected; primary zones only (else 422 `incorrect_zone_mode`); only class IN; zonefile must contain a SOA and the NS records of the assigned Hetzner nameservers. Returns action with command `import_zonefile`. Create with zone file: `POST /zones` with `zonefile`, ignored for secondary zones, and `rrsets` must be empty when `zonefile` is set (OPENAPI); CLI refuses `--zonefile` with secondary mode.
- Error response for a bad zonefile (code, field name such as `zonefile` or `zonefile.records[0].value`, line numbers) is UNCONFIRMED; migration docs show `zonefile.records[0].value` for an unquoted TXT. Parsing may also fail asynchronously via the action (`error.code: action_failed`) rather than synchronously: UNCONFIRMED.
- `record_count`: "Number of resource records (RR) within the Zone" (OPENAPI). Whether SOA and the three NS RRs are counted: UNCONFIRMED (hcloud-go fixture shows 4 for a sample, consistent with SOA+3NS but not proof).
- Action command names (OPENAPI example responses): zone `create_zone`, `delete_zone`, `import_zonefile`, `change_primary_nameservers`, `change_protection`, `change_ttl`; RRSet `create_rrset`, `delete_rrset`, `change_rrset_protection`, `change_rrset_ttl`, `set_rrset_records`, `add_rrset_records`, `remove_rrset_records`, `update_rrset_records`. (Use the examples as names; the spec has no formal enum.) Actions status: running/success/error; failed action carries `error.code` (`action_failed`) and message. Zone `status`: ok, updating, error. Zone and RRSet writes return `{action}` (create zone also `{zone, action}`); poll `GET /zones/actions/{id}` (zone actions) because `GET /zones/{id}/actions/{action_id}` is deprecated; `GET /actions/{id}` may not cover zone actions (UNCONFIRMED, spec lists a separate `/zones/actions` family).
- Zone update (PUT) only changes labels. Zone endpoints: GET/POST `/zones`, GET/PUT/DELETE `/zones/{id_or_name}`, `/zones/{id_or_name}/rrsets...`, actions `change_protection`, `change_ttl`, `change_primary_nameservers`, `import_zonefile`.
- Zone fields: id, name, created, mode, ttl, labels, protection{delete}, record_count, status, registrar (hetzner|other|unknown), primary_nameservers, authoritative_nameservers{assigned[], delegated[], delegation_last_check, delegation_status (valid, partially-valid, invalid, lame, unregistered, unknown)}. The assigned nameservers are known only after the create action finished (CLI reloads the zone after the action for this reason). Delegation: assigned NS are of the form hydrogen.ns.hetzner.com, oxygen.ns.hetzner.com, helium.ns.hetzner.de (sample output in docs; check per zone, do not hardcode).

## 5. miekg/dns v1.1.73

- v1.1.73 is the latest release (2026-08-19; previous v1.1.72 2026-01-22). Maintained. Note: no GitHub "releases" page, versions come from tags/pkg.go.dev.
- All wanted types exist as RR types and parse in ZoneParser/NewRR: HTTPS/SVCB (key names mandatory, alpn, no-default-alpn, port, ipv4hint, ech, ipv6hint, dohpath, ohttp; unknown `keyN`), CAA (flag, tag, one quoted value), TLSA (certificate hex, spaces dropped), DS (algorithm numeric or mnemonic), RP (both names made absolute against origin), HINFO (cpu and os, quoted), TXT (>255-byte tokens split into 255-byte chunks). Source: scan_rr.go, svcb.go at tag v1.1.73. Not executed here; verify with a round-trip test.
- `RR.String()` output: `name.<TAB>ttl<TAB>IN<TAB>TYPE<TAB>rdata`, absolute names with trailing dots, TXT as space-separated quoted strings with escapes (`\"`, `\\`, `\DDD` for non-printable). SVCB/HTTPS String() always double-quotes parameter values (`alpn="h3,h2"`), `no-default-alpn` prints `key=""` (check this: Hetzner may reject `no-default-alpn=""`; UNCONFIRMED), invalid IP hints print `<nil>`. Whether Hetzner accepts these lines in an import: UNCONFIRMED but they are standard BIND presentation format and Hetzner states BIND compatibility. Safer for import: build `$ORIGIN <zone>.`, `$TTL`, then `RR.String()` lines with absolute names.
- Library does not validate SVCB AliasMode rules (priority 0 with params) or `mandatory` consistency; own checks needed. The library is lenient where Hetzner may be strict (uppercase names are kept as given, TTL 0 allowed). Hetzner lowercase and TTL >= 60 rules must be checked by the product.
- For per-RRSet API calls (`records[].value`) use only the rdata part: `strings.TrimPrefix(rr.String(), rr.Header().String())` gives rdata (Header().String() ends with the tab before rdata).

## 6. hcloud CLI v1.70.2 (pinned, version matches SPEC)

- `hcloud zone create --name <name> [--mode primary|secondary] [--ttl N] [--label k=v]... [--enable-protection delete] [--primary-nameservers-file <json|->] [--zonefile <file|->]`. Only `--name` required; default mode primary; `--ttl` sent only if given; zonefile with secondary mode is refused client side.
- Other zone commands: `list [--mode]`, `describe`, `delete`, `change-protection`, `change-ttl --ttl N`, `change-primary-nameservers --primary-nameservers-file`, `import-zonefile --zonefile <file|->`, `export-zonefile` (raw text; `--json` gives `{"zonefile": ...}`), `labels` add/remove.
- RRSet: `hcloud zone rrset` (aliases `record`, `records`): `create --name <n> --type <T> (--record <v>... | --records-file <json|->) [--ttl] [--label] <zone>`, `list`, `describe`, `delete`, `change-ttl`, `enable-protection`/`disable-protection`, `add-records`, `set-records`, `remove-records` with `(--record <value>... | --records-file <file>) <zone> <name> <type>`, add/set take `--ttl`. `--record` has value only (no comment); `--records-file` is a JSON array of `{value, comment?}`. `set-records` with no records deletes the RRSet; with a missing RRSet creates it. For TXT the CLI auto-quotes unquoted values (warning) and expects "one or many quoted strings of 255 characters".
- Names in the CLI are relative, `@` for apex (same as API).

## 7. Get-DnsClientNrptRule without elevation

UNCONFIRMED. Microsoft's reference page (https://learn.microsoft.com/en-us/powershell/module/dnsclient/get-dnsclientnrptrule) states no privilege requirement. Without `-GpoName` it reads the local client rules (CIM class root/Microsoft/Windows/DNS/DnsClientNrptRule; properties Name, Namespace, NameServers, DirectAccess*, DnsSec*, DisplayName, Comment). From memory only (not verified): reading works unelevated while Add/Remove/Set need admin. Probe on the dev machine as a non-admin user before relying on it.

## 8. Secondary zones

- Create: `POST /zones` with `mode: "secondary"`, `name`, optional `ttl`, `labels`, `primary_nameservers: [{address, port?, tsig_algorithm?, tsig_key?}]`. `address` public IPv4 or IPv6 (unique per zone), `port` default 53, `tsig_key` base64, `tsig_algorithm` hmac-md5 | hmac-sha1 | hmac-sha256. `zonefile` and `rrsets` ignored for secondary zones.
- Response: zone object with `mode: "secondary"`, `primary_nameservers` populated (only in secondary mode, else empty array), plus action `create_zone`. `change_primary_nameservers` (body `{primary_nameservers: [...]}`) overwrites the list; command `change_primary_nameservers`; only secondary zones (else 422 `incorrect_zone_mode`).
- Mode cannot be changed after creation. RRSet writes, import, export, change_ttl on a secondary zone return 422 `incorrect_zone_mode`.
- Whether creation validates reachability or AXFR immediately (action error, zone `status: error`) or just stores the entries: UNCONFIRMED. Docs say primaries must allow queries from Hetzner's secondaries and that TSIG covers AXFR and NOTIFY. The `private/non-public address` rule for `address` (RFC1918 refused?) is UNCONFIRMED; the spec says "Public IPv4 or IPv6", so loopback/private addresses may be rejected with `invalid_input`.
- Primary-mode Hetzner as primary for external secondaries is not supported (FAQ).

## Probe list (live, cheap, with context hlab-docs)

1. Create zone with a subdomain (`a.example.com`), a private-suffix name (`x.github.io`), `foo.test`, `1.0.10.in-addr.arpa`: record code, `details.fields[].name/messages`.
2. Create apex RRSets: edit SOA, delete/replace NS, add CNAME at `@`, CNAME next to A: record status, code, field, message.
3. Create each type with relative and absolute targets; TXT unquoted/quoted/over 255; TLSA, DS, RP, CAA, HINFO, HTTPS with `no-default-alpn=""`.
4. Export a zone with one RRSet of each type and save the text as a test fixture; check `record_count`.
5. Bad import (unquoted TXT, missing SOA, wrong NS, non-IN class): sync error or action error.
6. Read fresh server IPv4 PTR, Primary IP and Floating IP `dns_ptr`.
7. Secondary zone with an unreachable/private primary address.
8. `Get-DnsClientNrptRule` in a non-elevated PowerShell.
