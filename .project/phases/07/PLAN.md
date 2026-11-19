# Phase 07: DNS zones and reverse DNS

Confirmed assumptions, user changes and answers: CONTEXT.md. Research:
RESEARCH.md ("R<n>" below means its section n). Where RESEARCH.md left
something UNCONFIRMED, the decisions D1 to D16 at the end settle it. Test
map: TESTS.md.

## Interfaces

New or changed in this phase; tests and implementers follow them.

```go
// internal/dnszone (07-01, 07-02): no build tag, no import of internal/api
// or internal/store; used by hlabd (zones module) and hlab-agent.
package dnszone

// RRSet is an RRSet in Hetzner's shape without API bookkeeping.
type RRSet struct {
	Name    string   // relative, lower case, "@" for the apex
	Type    string   // A, AAAA, CAA, CNAME, DS, HINFO, HTTPS, MX, NS, PTR, RP, SOA, SRV, SVCB, TLSA, TXT
	TTL     *int     // nil: the zone's default TTL
	Records []Record // order kept as given
}
type Record struct {
	Value   string // presentation format; names without trailing dot are relative to the zone
	Comment string
}

// FieldError is a validation error on one request field; the zones module
// turns it into api.FieldError (code "" means invalid_input).
type FieldError struct{ Code, Field, Message string }
func (e *FieldError) Error() string

// Nameservers are the assigned authoritative nameservers of every zone.
var Nameservers = []string{"hydrogen.ns.hetzner.com", "oxygen.ns.hetzner.com", "helium.ns.hetzner.de"}

// CheckZoneName accepts lower case names of at most 255 characters without
// trailing dot that are exactly one label below an ICANN public suffix, or
// exactly one label below internal, test or home.arpa; it refuses
// hetzner.internal, local and everything under arpa except home.arpa.
// Error field "name".
func CheckZoneName(name string) error
// PrivateUse reports whether a zone lies under internal, test or home.arpa.
func PrivateUse(zone string) bool

// CheckRRSet validates one RRSet of zone as at Hetzner: name, type, ttl
// (nil or 60..2147483647), 1..50 records with distinct values, each value
// parsed by miekg/dns for its type plus Hetzner's rules (TXT quoted, each
// string at most 255 characters). Fields "name", "type", "ttl", "records",
// "records[i].value".
func CheckRRSet(zone string, set RRSet) error
// CheckConflicts checks a zone's RRSets together: a CNAME is alone at its
// name and has one record. Field "type", message
// "(<name>, CNAME) conflicts with (<name>, <type>)".
func CheckConflicts(sets []RRSet) error
// ToRR returns the records of set as RRs with absolute names; zoneTTL
// applies when set.TTL is nil.
func ToRR(zone string, zoneTTL int, set RRSet) ([]dns.RR, error)
// DefaultSets are the SOA and NS RRSets of a new zone.
func DefaultSets(zone string, serial uint32) []RRSet
// NextSerial returns the next YYYYMMDDnn serial after old for day now (UTC).
func NextSerial(old uint32, now time.Time) uint32

// ParseZonefile parses a BIND zone file with Hetzner's import rules
// ($ORIGIN must be zone + ".", $TTL becomes ttl (0: none), class IN only,
// SOA present, the three Nameservers as apex NS, comments dropped). Errors
// are *FieldError on field "zonefile" naming the line.
func ParseZonefile(zone, text string) (ttl int, sets []RRSet, err error)
// FormatZonefile writes $ORIGIN, $TTL and one line per record
// ("<name>\tIN\t[<ttl>\t]<TYPE>\t<value>[ ; <comment>]"), SOA first, then
// NS, then by name and type; ParseZonefile reads it back unchanged.
func FormatZonefile(zone string, ttl int, sets []RRSet) string

// internal/rdns (07-03): reverse DNS shared by primary IPs, floating IPs, servers
package rdns
type Entry struct {
	IP     string `json:"ip"`
	DNSPtr string `json:"dns_ptr"`
}
// Default is Hetzner's default PTR of an IPv4 address,
// "static.<d>.<c>.<b>.<a>.clients.your-server.de"; "" for IPv6.
func Default(ip netip.Addr) string
// Change applies change_dns_ptr to the stored entries of a resource owning
// owner (an IPv4 /32 or an IPv6 /64): ip must be the exact IPv4 or lie in
// the /64; ptr nil removes the entry (IPv4 falls back to the default);
// ptr must be an FQDN without trailing dot; at most 100 IPv6 entries.
// Errors are *api.FieldError on "ip" or "dns_ptr".
func Change(entries []Entry, owner netip.Prefix, ip string, ptr *string) ([]Entry, error)
// Show returns entries as the API shows them: an IPv4 owner without entry
// gets its default entry.
func Show(entries []Entry, owner netip.Prefix) []Entry
// PTR returns the effective IPv4 PTR name (stored or default).
func PTR(entries []Entry, ip netip.Addr) string

// internal/resources/primaryips, floatingips (07-04, 07-05): DNSPtr stays the
// stored []rdns.Entry (type alias DNSPtr = rdns.Entry); both modules
// contribute their IPv4 PTRs to agentapi.Config.PTR (Contribute).

// internal/resources/servers (07-04)
type IPv6 struct { // DNSPtr changes from []any to []rdns.Entry (stored [] stays valid)
	ID      int64        `json:"id,omitempty"`
	IP      string       `json:"ip"`
	Blocked bool         `json:"blocked"`
	DNSPtr  []rdns.Entry `json:"dns_ptr"`
}
// IPv4.DNSPtr stored "" is shown as rdns.Default.
// DNSPtrs is the reverse DNS hook (set by the cloud; nil: change_dns_ptr
// answers 422 invalid_input on "ip").
type DNSPtrs interface {
	// ChangeServer applies change_dns_ptr on the primary IP of server r that
	// holds ip, updates r.PublicNet and returns the primary IP's ID.
	ChangeServer(tx store.Tx, r *Record, ip string, ptr *string) (int64, error)
}
func (m *Module) SetDNSPtrs(h DNSPtrs)
// POST /servers/{id}/actions/change_dns_ptr: 201 {action change_dns_ptr}
// naming server and primary IP; the action pushes the agent document.

// internal/resources/zones (07-07 to 07-10)
type ShadowWatch interface {
	Check(zone, project string) // starts a background check; a later call replaces the result
	Forget(zone string)
}
type Deps struct {
	Store     *store.Store
	PushAgent func(ctx context.Context) error
	// SetZones sets the NRPT rules to exactly these zone names.
	SetZones  func(ctx context.Context, zones []string) error
	Shadow    ShadowWatch // nil: none
	Now       func() time.Time // nil: time.Now
}
func New(d Deps) *Module // Type() "zones"; resource.NetContributor (primary zones only)
// Names returns the names of all zones of all projects, sorted.
func Names(tx store.Tx) ([]string, error)
// Primary returns the names and projects of all primary zones (for the
// shadow check at cloud start).
func Primary(tx store.Tx) ([]ZoneRef, error)
type ZoneRef struct{ Zone, Project string }

// Shadow (07-10) implements ShadowWatch.
type Lookup func(ctx context.Context, zone string) (bool, error)
func NewShadow(lookup Lookup) *Shadow
func (s *Shadow) List() []ZoneRef // sorted by zone
// PublicLookup asks servers (else 1.1.1.1:53, 9.9.9.9:53) in order for the
// SOA of zone with timeout per server; true when one answers NOERROR with
// an SOA or NS answer for zone itself, false on NXDOMAIN or NODATA, error
// when none answers.
func PublicLookup(servers []netip.AddrPort, timeout time.Duration) Lookup

// internal/mgmt (07-10)
type Status struct {
	State          string          `json:"state"`
	Version        string          `json:"version"`
	Problems       []Problem       `json:"problems"`
	ShadowingZones []ShadowingZone `json:"shadowing_zones"` // [] when none
}
type ShadowingZone struct {
	Zone    string `json:"zone"`
	Project string `json:"project"`
}
```

## Plans

- [x] 07-01 DNS zone data: names and records
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: DNS-01 | Tests: internal/dnszone/dnszone_test.go
  - Reads: REQUIREMENTS DNS-01; CONTEXT "Changes by the user"; RESEARCH R1, R2, R5; DECISIONS "Zones under private-use suffixes"; decisions D1 to D5 below; OpenAPI `/zones` POST (name, rrsets)
  - Size: ~350 lines in 3 files
  - Adjusts: -
  - [x] `RRSet`, `Record`, `FieldError`, `Nameservers`, `CheckZoneName`, `PrivateUse` (golang.org/x/net/publicsuffix, ICANN section only; go.mod: x/net direct) (internal/dnszone/names.go, go.mod)
  - [x] `CheckRRSet`, `CheckConflicts`, `ToRR` (rdata parsed with `dns.NewRR` under `$ORIGIN zone.`; TXT quoting and 255-character strings; SOA records only through `DefaultSets`), `DefaultSets`, `NextSerial` (internal/dnszone/records.go)
  - [x] Rows of TESTS.md for 07-01 (internal/dnszone/dnszone_test.go, group `TestDNSZone`)

- [x] 07-02 DNS zone data: zone files
  - Wave: 2 | Depends on: 07-01 | Review: alone
  - Requirements: DNS-01 | Tests: internal/dnszone/zonefile_test.go
  - Reads: RESEARCH R4, R5; OpenAPI "Zone file import" (tag Zones); decisions D7, D8 below
  - Size: ~250 lines in 2 files
  - Adjusts: -
  - [x] `ParseZonefile` (miekg `ZoneParser` with origin `zone.`, TTL before or after class, rdata of each RR made relative where it lies in the zone, records grouped into RRSets in file order, each set through `CheckRRSet`, then `CheckConflicts`), `FormatZonefile` (internal/dnszone/zonefile.go)
  - [x] Rows of TESTS.md for 07-02 (internal/dnszone/zonefile_test.go, in `TestDNSZone`)

- [x] 07-03 Reverse DNS entries
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: RDNS-01 | Tests: internal/rdns/rdns_test.go
  - Reads: REQUIREMENTS RDNS-01; RESEARCH R3; OpenAPI `change_dns_ptr` of servers, primary IPs, floating IPs; decisions D9, D10 below
  - Size: ~150 lines in 2 files
  - Adjusts: -
  - [x] `Entry`, `Default`, `Change`, `Show`, `PTR` (internal/rdns/rdns.go)
  - [x] Rows of TESTS.md for 07-03 (internal/rdns/rdns_test.go, group `TestRDNS`)

- [x] 07-04 Primary IPs and servers: change_dns_ptr
  - Wave: 2 | Depends on: 07-03 | Review: alone
  - Requirements: RDNS-01 | Tests: internal/resources/primaryips/primaryips_test.go
  - Reads: RESEARCH R3; this file "Interfaces"; internal/resources/primaryips/actions.go (assign writes the server's public_net), internal/resources/servers/network.go (hooks)
  - Size: ~300 lines in 5 files
  - Adjusts: tests in internal/resources/servers that compare `ipv6.dns_ptr` with `[]any{}` (type only)
  - [x] Servers: `IPv6.DNSPtr []rdns.Entry`, IPv4 `dns_ptr` shown as `rdns.Default` when stored empty, `DNSPtrs`, `SetDNSPtrs`, route `POST /servers/{id}/actions/change_dns_ptr` (body `ip` and `dns_ptr` required, `dns_ptr` null allowed; action `change_dns_ptr` on server and primary IP; push) (internal/resources/servers/servers.go, internal/resources/servers/network.go, internal/resources/servers/dnsptr.go)
  - [x] Primary IPs: `change_dns_ptr` action through `rdns.Change` (records shown through `rdns.Show`), `ChangeServer` (finds the server's primary IP holding `ip`; an IP the server does not hold: 422 `invalid_input` on `ip`; mirrors the change into the server's `public_net`, also on assign), `Contribute` adds the effective PTR of every primary IPv4 to `doc.PTR` (internal/resources/primaryips/actions.go, internal/resources/primaryips/primaryips.go)
  - [x] Rows of TESTS.md for 07-04 (internal/resources/primaryips/primaryips_test.go)

- [x] 07-05 Floating IPs: change_dns_ptr
  - Wave: 2 | Depends on: 07-03 | Review: alone
  - Requirements: RDNS-01 | Tests: internal/resources/floatingips/floatingips_test.go
  - Reads: RESEARCH R3; internal/resources/floatingips/actions.go (`change`, `Contribute`)
  - Size: ~120 lines in 3 files
  - Adjusts: -
  - [x] `change_dns_ptr` action through `rdns.Change`, `dns_ptr` shown through `rdns.Show`, `Contribute` adds the effective PTR of every floating IPv4 to `doc.PTR` (internal/resources/floatingips/actions.go, internal/resources/floatingips/floatingips.go)
  - [x] Rows of TESTS.md for 07-05 (internal/resources/floatingips/floatingips_test.go)

- [x] 07-06 Agent: answering hlab zones
  - Wave: 2 | Depends on: 07-01 | Review: alone
  - Requirements: DNS-02 | Tests: internal/agent/dns_test.go
  - Reads: ARCHITECTURE "hlab-agent" (Zone, RRSet), "Network" (DNS); CONTEXT (wildcards, CNAME, DS); internal/agent/dns.go; decision D11 below
  - Size: ~250 lines in 2 files
  - Adjusts: -
  - [x] `buildDNSData` adds every `cfg.Zones` entry as an authoritative zone (RRs through `dnszone.ToRR`, SOA from its SOA RRSet); `zoneFor` keeps the most specific zone; `answer` serves wildcard owners (`*.<name>`) for names without own records, and follows a CNAME inside hlab zones and, for a target outside them, through the upstream servers, appending the chain (at most 8 steps) (internal/agent/dns.go)
  - [x] Rows of TESTS.md for 07-06 (internal/agent/dns_test.go, registered in `TestAgent`; fake upstream through a local miekg server on 127.0.0.1)

- [x] 07-07 Zones: zones resource
  - Wave: 2 | Depends on: 07-01 | Review: alone
  - Requirements: DNS-01 | Tests: internal/resources/zones/zones_test.go
  - Reads: REQUIREMENTS DNS-01; CONTEXT; RESEARCH R1, R4, R8; DECISIONS "Q8: secondary zones are stored and forwarded", "Zone modes and synchronous updates"; OpenAPI `/zones`, `/zones/{id_or_name}`, `/zones/actions`, zone actions; internal/resources/firewalls/firewalls.go as pattern; decisions D6, D12 to D15 below
  - Size: ~400 lines in 3 files
  - Adjusts: -
  - [x] Module, `Record` (Hetzner zone shape; RRSets stored in the document with labels and `protection.change`; `_hlab.project_id`), `Deps`, `Names`, `Primary`, path lookup by ID or name, `GET /zones` (filter name, mode, label_selector; sort id, name, created), `GET /zones/{id_or_name}`, `PUT` (labels only, 200 without action), `POST /zones` (primary with `rrsets` or `zonefile` placeholder left to 07-09: 422 `invalid_input` on `zonefile` until then; secondary with `primary_nameservers`), `DELETE` (`delete_zone`; delete protection: `protected`), actions `change_protection`, `change_ttl`, `change_primary_nameservers`, mode checks (`incorrect_zone_mode`), `/zones/actions` and per-zone action routes through `resactions.RoutesFor` (internal/resources/zones/zones.go, internal/resources/zones/actions.go)
  - [x] Publishing: every write that changes zones or records runs `PushAgent` then `SetZones(Names)` inside its action (failure: action `error`, zone `status` stays `ok`); `Contribute` adds each primary zone with its RRSets (TTL resolved) to `doc.Zones`; `Shadow.Check` on create of a primary zone outside `PrivateUse`, `Forget` on delete (internal/resources/zones/publish.go)
  - [x] Rows of TESTS.md for 07-07 (internal/resources/zones/zones_test.go, group `TestZones`)

- [x] 07-08 Zones: RRSets
  - Wave: 3 | Depends on: 07-07 | Review: alone
  - Requirements: DNS-01 | Tests: internal/resources/zones/rrsets_test.go
  - Reads: RESEARCH R1, R6; OpenAPI `/zones/{id_or_name}/rrsets...`; decisions D3 to D5, D13 below
  - Size: ~380 lines in 2 files
  - Adjusts: -
  - [x] `GET .../rrsets` (filter name, type, label_selector; sort id, name, created), `GET`/`PUT` (labels, 200 without action)/`DELETE` of `.../rrsets/{rr_name}/{rr_type}`, `POST .../rrsets` (`create_rrset`; existing: 409 `uniqueness_error`), actions `change_protection` (`change_rrset_protection`), `change_ttl`, `set_records`, `add_records` (creates a missing RRSet), `remove_records` (removing the last record deletes the RRSet), `update_records` (comments only); every change through `CheckRRSet` and `CheckConflicts`, SOA serial through `NextSerial`, change protection answers `protected`, the apex SOA is read-only (D5), secondary zones answer `incorrect_zone_mode` (internal/resources/zones/rrsets.go)
  - [x] Rows of TESTS.md for 07-08 (internal/resources/zones/rrsets_test.go)

- [x] 07-09 Zones: zone file import and export
  - Wave: 4 | Depends on: 07-02, 07-08 | Review: alone
  - Requirements: DNS-01 | Tests: internal/resources/zones/zonefile_test.go
  - Reads: RESEARCH R4; OpenAPI `zonefile`, `import_zonefile`, `POST /zones` (`zonefile`); decisions D7, D8 below
  - Size: ~180 lines in 3 files
  - Adjusts: the 07-07 row that expects `invalid_input` on `zonefile` at create (replaced by the 07-09 row)
  - [x] `GET /zones/{id_or_name}/zonefile` (`{"zonefile": FormatZonefile}`), action `import_zonefile` (replaces all RRSets; `$TTL` sets the zone TTL; a change-protected RRSet: `protected`), `POST /zones` with `zonefile` (with `rrsets` not empty: 422 `invalid_input` on `zonefile`); secondary: `incorrect_zone_mode` (internal/resources/zones/zonefile.go, internal/resources/zones/zones.go)
  - [x] Rows of TESTS.md for 07-09 (internal/resources/zones/zonefile_test.go)

- [x] 07-10 Shadowing zones in the status
  - Wave: 3 | Depends on: 07-07 | Review: alone
  - Requirements: DNS-04 | Tests: internal/resources/zones/shadow_test.go
  - Reads: REQUIREMENTS DNS-04; ARCHITECTURE "Management API" (`/status`); CONTEXT (DNS-04 line, user changes, Q8); internal/mgmt/mgmt.go, internal/cli/cli.go (`cloudStatus`)
  - Size: ~250 lines in 4 files
  - Adjusts: -
  - [x] `Shadow` (`NewShadow`, `Check` runs the lookup in a goroutine with the lookup's own timeouts, `Forget`, `List`), `PublicLookup` (miekg client, UDP, 2 s per server) (internal/resources/zones/shadow.go)
  - [x] `mgmt.Status.ShadowingZones`, `ShadowingZone`; `hlab cloud status` prints `warning: zone <zone> (project <project>) shadows a public domain` per entry (internal/mgmt/mgmt.go, internal/cli/cli.go)
  - [x] Rows of TESTS.md for 07-10 (internal/resources/zones/shadow_test.go)

- [x] 07-11 Cloud wiring and end-to-end
  - Wave: 5 | Depends on: 07-04, 07-05, 07-06, 07-09, 07-10 | Review: alone
  - Requirements: DNS-02, DNS-03, RDNS-02 | Tests: test/e2e/dns_test.go, internal/resources/conventions_test.go
  - Reads: ARCHITECTURE "Cloud start and stop", "hostnet", "Network" (NRPT); EXPERIMENTS E4, E5; TESTS.md header; test/e2e/ip_test.go, test/e2e/wsl_test.go (helpers); decision D16 below
  - Size: ~350 lines in 5 files
  - Adjusts: -
  - [x] Module `zones` after `firewalls` in `modules()` with `SetZones` = `c.net.SetZones`, a `Shadow` from `PublicLookup(host DNS servers)`; `srv.SetDNSPtrs(pips)`; at start after `hostnet.Up`, `SetZones(Names)` and `Shadow.Check` for every `Primary` zone outside `PrivateUse`; `mgmtStatus` fills `ShadowingZones` from `Shadow.List()`; zones in `newFullEnv` plus the API-06, API-07 and API-11 cases (internal/cloud/cloud.go, internal/resources/conventions_test.go)
  - [x] End-to-end rows DNS-02, DNS-03, RDNS-02 (test/e2e/dns_test.go, test/e2e/e2e_test.go)

- [ ] 07-12 Documentation
  - Wave: 6 | Depends on: 07-11 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06 | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC "Documentation", "Accounts and secrets" (`hlab-docs`); REQUIREMENTS DOC; CONTEXT; RESEARCH R6; decisions of this phase
  - Size: ~200 lines in 5 files
  - Adjusts: -
  - [ ] Deviations, each in internal/api/deviations.go AND docs/user/deviations.md (DOC-03 checks only the Go list): private-use zone suffixes; secondary zones stored and forwarded, never transferred; `authoritative_nameservers.delegated` `[]`, `delegation_status`/`registrar` `unknown`, `status` always `ok`; no DNSSEC, DS served as data; IPv6 PTRs stored but not resolvable; zone names unique across projects; the codes and rules chosen where Hetzner is unconfirmed (D3, D5, D7, D12, D13) (internal/api/deviations.go, docs/user/deviations.md)
  - [ ] README.md (zones resolve on host, WSL2 and servers; one NRPT rule per zone while the cloud runs; shadowing warning; reverse DNS), docs/user/commands.md (`hlab cloud status` warning line), docs/dev/components.md (zones module, dnszone, rdns, agent DNS); examples run through `hcloud --context hlab-docs` as SPEC.md says

## Decisions taken in planning

- D1 Zone names: only the ICANN section of the Public Suffix List counts. `foo.github.io` and unknown TLDs are refused, and so is everything under `arpa` except `home.arpa`, which also excludes reverse zones. Under `internal`, `test` and `home.arpa` the name must be exactly one label below the suffix (`lab.internal`; `a.lab.internal` is refused, as are subdomains elsewhere). Every refusal is 422 `invalid_input` on `name`.
- D2 RP values are accepted in DNS mailbox form as miekg/dns parses them (`admin.example.com. txt.example.com.`); `admin@example.com` is refused.
- D3 A CNAME beside another type at the same name, or a CNAME RRSet with more than one record, answers 422 `invalid_input` on `type` with the message `(<name>, CNAME) conflicts with (<name>, <type>)`. This also refuses a CNAME at the apex.
- D4 Creating an RRSet that exists answers 409 `uniqueness_error`. Any write to a change-protected RRSet answers `protected`; that covers records, TTL, delete and an import that would replace it, but not labels or protection itself.
- D5 The apex SOA RRSet is managed by hlab: creating, deleting or changing its records answers 422 `invalid_input` on `type`. Its TTL may be changed. Apex NS RRSets are freely editable.
- D6 `record_count` counts every record, SOA and NS included (4 for a new zone).
- D7 Bad zone files fail synchronously: 422 `invalid_input` on `zonefile`, with the line number in the message; no action is started.
- D8 Export writes `$ORIGIN`, `$TTL`, then `<name>\tIN\t[<ttl>\t]<TYPE>\t<value>[ ; comment]` lines, SOA first, then NS, then by name and type. A TTL appears only when the RRSet has one. Comments are written on export and dropped on import, as Hetzner does.
- D9 Fresh primary IPv4 and floating IPv4 addresses show the default PTR `static.<d>.<c>.<b>.<a>.clients.your-server.de`, as servers do. IPv6 starts with no entries.
- D10 `change_dns_ptr` on a server for an IP it does not hold (or one without a public IP) answers 422 `invalid_input` on `ip`. The action names the server and the primary IP.
- D11 The agent follows a CNAME chain at most 8 steps; if the upstream fails it answers with the chain so far. Wildcards match only names without records of their own (RFC 4592).
- D12 Secondary zones: `address` must be an IP address (private ones allowed in a lab), unique per zone; `port` 1..65535 (default 53); `tsig_algorithm` in the enum; `tsig_key` valid base64. Each refusal is 422 `invalid_input` on `primary_nameservers[i].<field>`. Nothing is checked for reachability.
- D13 Zone and RRSet actions are readable at `/zones/actions/{id}`, `/zones/{id}/actions/{id}` and `/actions/{id}`. The command names are those of the OpenAPI examples (R4).
- D14 A zone name used in another project answers 409 `uniqueness_error`. The message is `zone name is already used`, without naming the project.
- D15 Zone `status` is always `ok`, and `authoritative_nameservers.assigned` is `Nameservers` from the create answer on.
- D16 DNS-03 reads the rules with `Get-DnsClientNrptRule` filtered by the profile's tag in `Comment`. If that is refused without elevated rights, the test reads the same rules from `HKLM\SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig` (`Name`, `Comment`). The test never changes rules.
