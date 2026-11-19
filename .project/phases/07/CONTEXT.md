# Phase 07 context

Confirmed by the user at the start of the phase, one line each.

## Assumptions confirmed

- New module `internal/resources/zones` serves every `/zones` path of the frozen spec (zone CRUD, zonefile export, `/zones/actions`, all zone and RRSet actions).
- RRSets are stored inside the zone's resource document, identified by `name/type`; the RRSet list goes through `api.ListRequest`.
- Zone names: lower case, no trailing dot, at most 255 characters, Punycode, exactly a registrable domain (eTLD+1) by `golang.org/x/net/publicsuffix` (direct dependency).
- Zone names are unique across all projects (`uniqueness_error`).
- A new zone gets the default SOA and three NS RRSets with `hydrogen.ns.hetzner.com`, `oxygen.ns.hetzner.com`, `helium.ns.hetzner.de`.
- The SOA serial increments in `YYYYMMDDnn` form on every change.
- `authoritative_nameservers.delegated` is `[]`, `delegation_status` and `registrar` are `unknown`, `status` is `ok` once pushed; listed as a deviation.
- Action writes succeed only after the agent push and `hostnet.SetZones`; the module gets `PushAgent` and `SetZones` in its Deps.
- At cloud start, wiring calls `SetZones` with all zones after `hostnet.Up`; `Down` already removes every tagged rule.
- Zone file import and export in BIND format through miekg/dns with Hetzner's import rules.
- Record values are checked by parsing an RR with miekg/dns plus Hetzner's extra rules from the research.
- The agent serves user zones authoritatively next to `hetzner.internal` and the reverse zones; the most specific zone wins.
- Wildcards are answered; CNAMEs are followed inside hlab zones and through upstream for outside names; no DNSSEC, DS served as plain data (deviation).
- DNS-04: SOA query per zone directly to the host DNS servers (fallback 1.1.1.1 / 9.9.9.9), 2 s timeout, on create, import and cloud start, cached in memory; unreachable upstream means not listed.
- `mgmt.Status` gains `shadowing_zones: [{"zone","project"}]`; `hlab cloud status` prints a warning line per zone; tray in phase 10.
- RDNS-01: PTRs live on primary IP and floating IP records; the server's `change_dns_ptr` writes to its primary IP.
- Empty IPv4 `dns_ptr` means Hetzner's default; `dns_ptr: null` resets IPv4 and removes an IPv6 entry.
- `change_dns_ptr` rules: IPv4 exact, IPv6 within the /64 (at most 100 entries), FQDN without trailing dot, `invalid_input` on `ip` or `dns_ptr`; the action pushes the agent.
- The agent's PTR map serves only IPv4; IPv6 PTRs are stored but not resolvable (deviation).
- Load balancer `change_dns_ptr` (LB-09) stays in phase 08.
- E2E: DNS-02 and RDNS-02 share the environment and one server; DNS-03 counts tagged NRPT rules before and after zone delete; the stop case stays with phase 09.
- DOC: every new deviation goes to `internal/api/deviations.go` and `docs/user/deviations.md` (the DOC-03 test only checks that list); `docs/user/commands.md` if `hlab cloud status` output changes.

## Changes by the user

- Zone names may also lie under the private-use suffixes `.internal`, `.test` and `.home.arpa` (e.g. `lab.internal`), except `hetzner.internal` and below; `.local` is refused. Deviation for deviations.md; DNS-01 changed. Such zones never shadow anything; DNS-04 skips them.
- On a secondary zone the RRSet calls, `change_ttl` and `import_zonefile` answer 422 `incorrect_zone_mode`; `change_primary_nameservers` on a primary zone answers the same.
- `PUT /zones/{id}` and `PUT .../rrsets/{name}/{type}` answer 200 synchronously without an action; all other writes run as actions.
- Where the research finds Hetzner behaving differently from these answers, the research wins (DECISIONS.md).

## Answers

- Q8 (planner's Q1) secondary zones: accept and store with `primary_nameservers`, the appliance forwards their queries upstream; no own records, never in `shadowing_zones`, but they get their NRPT rule (DNS-03); deviation in deviations.md.
