# Phase 06: Firewalls

Confirmed assumptions and answers: CONTEXT.md. Research: RESEARCH.md
("C<n>" below means its correction n, "R<n>" its numbered section). Where
RESEARCH.md contradicts CONTEXT.md, the research is followed. Test map:
TESTS.md.

## Interfaces

New or changed in this phase; tests and implementers follow them.

```go
// internal/actions (06-01)
type Spec struct {
	Command   string
	Resources []Ref
}
// StartAll creates one running action per spec in tx. It returns ErrLocked
// if a running action outside the batch references one of lock (nil: no
// check); the actions of one batch never lock each other. Used where one
// request starts several actions on the same resources (firewall create,
// set_rules, apply_to_resources, server create with firewalls).
func (m *Manager) StartAll(tx store.Tx, projectID int64, lock []Ref, specs []Spec) ([]*Action, error)

// internal/resources/firewalls (06-02): rules in Hetzner's shape
type Rule struct {
	Description    *string  `json:"description"`
	Direction      string   `json:"direction"`       // "in" | "out"
	Protocol       string   `json:"protocol"`        // tcp, udp, icmp, esp, gre
	Port           *string  `json:"port"`            // "80", "1000-2000", "any"; null for icmp/esp/gre
	SourceIPs      []string `json:"source_ips"`      // "in"; [] for "out"
	DestinationIPs []string `json:"destination_ips"` // "out"; [] for "in"
}
// CheckRules validates rules as at Hetzner and returns them normalised:
// order kept (rules and IPs), the other direction's list [], IPv6 prefixes
// in RFC 5952 form, port and description echoed as given. Errors are
// *api.FieldError with field "rules[i].<field>" (or "rules" for the count).
func CheckRules(rules []Rule) ([]Rule, error)
// AgentRules converts stored rules to agentapi rules.
func AgentRules(rules []Rule) []agentapi.Rule

// internal/resources/servers (06-03)
type ServerFirewall struct {
	ID     int64  `json:"id"`
	Status string `json:"status"` // always "applied"
}
// PublicNet.Firewalls becomes []ServerFirewall (stored [] stays valid).
// Firewalls is the firewall hook of the servers module (set by the cloud;
// nil: public_net.firewalls is [] and create with `firewalls` answers 422
// invalid_input on `firewalls`).
type Firewalls interface {
	// ForServer returns the firewalls applied to server r, directly or by
	// label selector, ordered by ID.
	ForServer(tx store.Tx, r Record) ([]ServerFirewall, error)
	// AttachAtCreate applies firewalls ids to new server serverID inside
	// the create transaction (same checks as apply_to_resources) and
	// returns the started apply_firewall actions for next_actions.
	AttachAtCreate(tx store.Tx, acts *actions.Manager, projectID, serverID int64, hasIPv4 bool, ids []int64) ([]*actions.Action, error)
}
func (m *Module) SetFirewalls(h Firewalls)
// PUT /servers/{id} pushes the agent document after the commit when the
// labels changed; a failed push is logged, the answer stays 200.

// internal/resources/firewalls (06-05, 06-06, 06-07)
type Deps struct {
	Store     *store.Store
	PushAgent func(ctx context.Context) error
}
func New(d Deps) *Module // Type() "firewalls"; implements servers.Firewalls and resource.NetContributor
func (m *Module) ServerDeleted(tx store.Tx, serverID int64) error // removes the server from every applied_to
// Constructors in tests: srv.SetFirewalls(fw); srv.OnDelete(fw.ServerDeleted).

// internal/agent (06-04), no build tag: the pure part
type FilterRule struct {
	Protocol         uint8          // IPPROTO_TCP, _UDP, _ICMP, _ESP, _GRE
	FromPort, ToPort uint16         // tcp/udp; 0,0 = any port
	Prefixes         []netip.Prefix // IPv4 only, masked; a rule whose prefixes were all IPv6 is dropped
}
type ServerFilter struct {
	ServerID    int64
	Addrs       []netip.Addr // public IPv4 plus the floating IPs routed to it
	In, Out     []FilterRule // union of the rules of all its firewalls
	RestrictOut bool         // some applied firewall has an "out" rule
}
// CompileFirewalls returns one filter per server with a public IPv4 and at
// least one firewall, ordered by server ID. Networks never contribute.
func CompileFirewalls(cfg agentapi.Config) ([]ServerFilter, error)
// Firewall is the Applier "firewall": table inet hlab_fw, forward chain at
// filter priority + 10, replaced atomically; matches only packets with
// iifname/oifname the public interface (linux only; a no-op elsewhere).
func NewFirewall(iface string) *Firewall
```

## Plans

- [x] 06-01 Actions: batch start
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: FW-01 | Tests: internal/resources/firewalls/actions_test.go (through 06-06)
  - Reads: ARCHITECTURE "actions"; this file "Interfaces"
  - Size: ~60 lines in 1 file
  - Adjusts: -
  - [x] `Spec`, `Manager.StartAll` (lock check once on `lock` against running actions, then one action per spec in the same tx; `Start` unchanged) (internal/actions/actions.go)

- [x] 06-02 Firewalls: rule validation
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: FW-01 | Tests: internal/resources/firewalls/rules_test.go
  - Reads: REQUIREMENTS FW-01; RESEARCH R1 (rule shape, limits, port, CIDR); CONTEXT; decisions D1, D2 below
  - Size: ~300 lines in 2 files
  - Adjusts: -
  - [x] `Rule`, `CheckRules` (direction in/out, protocol enum; port required for tcp/udp and forbidden for icmp/esp/gre, `any`, `N` or `A-B` with 1 <= A <= B <= 65535, digits only; `source_ips` required and non-empty for `in`, `destination_ips` for `out`; each IP a CIDR with a mask and no host bits, at most 100 per list; at most 50 rules; description at most 255 characters; order kept; IPv6 canonical), `AgentRules` (internal/resources/firewalls/rules.go)
  - [x] Rows of TESTS.md for 06-02 (internal/resources/firewalls/rules_test.go)

- [x] 06-03 Servers: firewall hook
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: FW-01, FW-05 | Tests: internal/resources/firewalls/actions_test.go, internal/resources/firewalls/contribute_test.go (through 06-06, 06-07)
  - Reads: ARCHITECTURE "resource"; RESEARCH R1 (server `public_net.firewalls`, create `firewalls`), R4 (provider `firewall_ids`); DECISIONS "Network membership on the server record"
  - Size: ~150 lines in 2 files
  - Adjusts: -
  - [x] `ServerFirewall`, `Firewalls`, `SetFirewalls`; `public_net.firewalls` filled through `ForServer` wherever a server is shown (get, list, create, update, actions); create accepts `firewalls: [{firewall: id}]` and appends the hook's actions to `next_actions` (without hook: 422 `invalid_input` on `firewalls`) (internal/resources/servers/firewalls.go, internal/resources/servers/servers.go)
  - [x] `update` pushes the agent document after the commit when `labels` changed, logging a failed push (internal/resources/servers/servers.go)

- [x] 06-04 Agent: firewall plan and nftables applier
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: FW-02, FW-03, FW-04 | Tests: internal/agent/firewall_test.go
  - Reads: ARCHITECTURE "hlab-agent", "Network" (firewalls); RESEARCH R2, R3; DECISIONS "Isolation on private networks", "Appliance private network layout"; internal/agent/isolation_linux.go
  - Size: ~400 lines in 5 files
  - Adjusts: -
  - [x] `FilterRule`, `ServerFilter`, `CompileFirewalls` (addresses: public IPv4 plus `FloatingIPs` whose target is it; In/Out the union of the rules of `FirewallIDs`; ports parsed from `Port`; IPv6 prefixes dropped, a rule left without prefixes dropped; `RestrictOut` if any of its firewalls has an `out` rule; unknown firewall ID ignored) (internal/agent/firewall.go)
  - [x] Applier `Firewall` on Linux: table `inet hlab_fw` replaced atomically (add, delete, add); forward chain policy accept at filter priority + 10; `ct state established,related accept` for packets to or from a filtered address; per filter: `oifname <pub> ip daddr <addr>` jumps to the server's in-chain (rules accept, then drop), `iifname <pub> ip saddr <addr>` to its out-chain (rules accept, then drop) only when `RestrictOut`; prefixes per rule as an interval set or per-prefix mask compare; port ranges with `expr.Range`; `firewall_other.go` no-op; registered after `NewIsolation` in main (internal/agent/firewall_linux.go, internal/agent/firewall_other.go, cmd/hlab-agent/main.go)
  - [x] Rows of TESTS.md for 06-04 (internal/agent/firewall_test.go, registered in `TestAgent`)

- [x] 06-05 Firewalls: resource
  - Wave: 2 | Depends on: 06-02 | Review: alone
  - Requirements: FW-01 | Tests: internal/resources/firewalls/firewalls_test.go
  - Reads: REQUIREMENTS FW-01; RESEARCH R1 (endpoints, errors, limits); CONTEXT "Corrections by the user"; internal/resources/floatingips/floatingips.go as pattern
  - Size: ~350 lines in 2 files
  - Adjusts: -
  - [x] Module, `Record` (Hetzner shape: id, name, labels, created, rules, applied_to; `_hlab.project_id`; applied entries stored as `{type, server}` or `{type, label_selector}`), `GET /firewalls` (filter name, label_selector; sort id, name, created), `GET /firewalls/{id}`, `PUT` (name, labels; name 1..128, unique per project: 409 `uniqueness_error`), `POST /firewalls` without `apply_to` (rules through `CheckRules`; 201 `{firewall, actions}`, with rules one `set_firewall_rules` action naming the firewall that ends after the agent push), `DELETE` (applied anywhere, also by selector: 422 `resource_in_use`, message `firewall <id> is still in use`; else 204), action endpoints through `resactions.RoutesFor` (internal/resources/firewalls/firewalls.go)
  - [x] Rows of TESTS.md for 06-05 (internal/resources/firewalls/firewalls_test.go, group `TestFirewalls`)

- [x] 06-06 Firewalls: apply, remove, set rules, server hook
  - Wave: 3 | Depends on: 06-01, 06-03, 06-05 | Review: alone
  - Requirements: FW-01 | Tests: internal/resources/firewalls/actions_test.go
  - Reads: RESEARCH R1 (errors, actions, applied_to, precedence, 5 per server), R4 (what clients wait for); CONTEXT "Corrections by the user"; decisions D3 to D9 below
  - Size: ~400 lines in 3 files
  - Adjusts: -
  - Note from 06-03: servers runs only `create_server`; `AttachAtCreate` runs its `apply_firewall` actions itself (`acts.Run` after its last fallible step; the single store connection makes the push wait for the commit) and starts them through `StartAll` with lock on the firewall only, never the server (held by `create_server`).
  - [x] Resolution: the servers a firewall reaches (direct entries, then selectors over the project's servers that have a public IPv4 and are not deleting), skipping a selector match that would give a server a sixth firewall; `applied_to_resources` on selector entries; `ForServer`; `ServerDeleted` (internal/resources/firewalls/applied.go)
  - [x] `POST /firewalls` with `apply_to`; `apply_to_resources` (`apply_to`), `remove_from_resources` (`remove_from`), `set_rules` (`rules`), all 201 `{actions}` through `StartAll` with lock on the firewall; checks and codes as in TESTS.md; the record changes in the request's transaction, each action ends `success` after the agent push and `error` when it fails; `AttachAtCreate` (internal/resources/firewalls/actions.go, internal/resources/firewalls/firewalls.go)
  - [x] Rows of TESTS.md for 06-06 (internal/resources/firewalls/actions_test.go)

- [x] 06-07 Firewalls: agent document
  - Wave: 4 | Depends on: 06-06 | Review: alone
  - Requirements: FW-05 | Tests: internal/resources/firewalls/contribute_test.go
  - Reads: ARCHITECTURE "hlab-agent" (Server.FirewallIDs, Firewall, Rule); RESEARCH R1 (selector skips)
  - Size: ~150 lines in 2 files
  - Adjusts: -
  - [x] `Contribute` (runs after servers): sets `FirewallIDs` of each `doc.Servers` entry from the resolution, sorted; appends one `agentapi.Firewall` with `AgentRules` per firewall that reaches a server (internal/resources/firewalls/contribute.go)
  - [x] Rows of TESTS.md for 06-07 (internal/resources/firewalls/contribute_test.go)

- [x] 06-08 Cloud wiring and end-to-end
  - Wave: 5 | Depends on: 06-04, 06-07 | Review: alone
  - Requirements: FW-02, FW-03 | Tests: test/e2e/firewall_test.go, internal/resources/conventions_test.go
  - Reads: ARCHITECTURE "resource", "Network" (firewalls); TESTS.md header; test/e2e/ip_test.go, test/e2e/image_test.go (helpers `ownServerImage`, `runAPIAction`, `post`)
  - Size: ~300 lines in 5 files
  - Adjusts: -
  - [x] Module `firewalls` after `floating_ips` in `modules()` (Contribute after servers and floating IPs), `srv.SetFirewalls(fw)`, `srv.OnDelete(fw.ServerDeleted)`; the module in `newFullEnv` plus the API-06 and API-11 cases (internal/cloud/cloud.go, internal/resources/conventions_test.go)
  - [x] End-to-end rows FW-02 and FW-03 (test/e2e/firewall_test.go, test/e2e/e2e_test.go)

- [x] 06-09 Documentation
  - Wave: 6 | Depends on: 06-08 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06 | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC "Documentation"; REQUIREMENTS DOC; CONTEXT; RESEARCH C1, R1, R2; the decisions of this phase
  - Size: ~120 lines in 3 files
  - Adjusts: -
  - [x] docs/user/deviations.md: server firewall status always `applied` (no `pending`), IPv6 rules stored without effect, 500 effective rules per firewall and 50 firewalls per project not enforced, floating IP traffic filtered by the assigned server's firewalls (Hetzner unverified), `incompatible_network_type` never returned, the codes chosen where Hetzner documents none (D3 to D7)
  - [x] README.md (firewalls: what is filtered, stateful, private networks not filtered, appliance services never filtered), docs/dev/components.md (table `inet hlab_fw` beside `inet hlab`, CompileFirewalls)

## Decisions taken in planning

- D1 A CIDR with host bits set, or an address without a mask, answers 422 `invalid_input` on `rules[i].source_ips` / `destination_ips` (clients always send masked CIDRs).
- D2 Ports: `any`, `N` or `A-B` with 1 <= A <= B <= 65535, digits only, echoed exactly as sent; tcp/udp without port answers 422 `invalid_input`.
- D3 `remove_from_resources` for a server or selector not applied answers 404 `firewall_resource_not_found` (both pinned clients tolerate it).
- D4 Applying a selector already applied answers 422 `firewall_already_applied`; a sixth firewall applied directly to a server answers 403 `resource_limit_exceeded`; selector matches beyond five are skipped silently.
- D5 `apply_firewall`/`remove_firewall` for a server name the server and the firewall; for a label selector one action naming only the firewall.
- D6 `set_rules` returns `set_firewall_rules` plus one `apply_firewall` per directly applied server; create with rules and `apply_to` returns `set_firewall_rules` plus the apply actions.
- D7 `DELETE` of a firewall applied only through a selector also answers 422 `resource_in_use`.
- D8 Outbound with several firewalls: restricted as soon as any applied firewall has an `out` rule, allowed are the union of `out` rules.
- D9 Traffic to and from a floating IP is filtered by the firewalls of the server it is assigned to.
- D10 The record changes in the request's transaction, before the action runs, so `GET` and `DELETE` right after a remove see the new state.
- D12 `POST /servers` with an unknown firewall in `firewalls` answers 404 `not_found` and creates no server; a server create with firewalls and no public IPv4 answers 422 `private_net_only_server`.
- D11 New `actions.Manager.StartAll` instead of naming only one resource per action: the server create action and several apply actions on one firewall would otherwise lock each other.

## Close

- E2E: `task e2e -- -v -run 'TestE2E/FW-0[23]$'` (the phase's e2e rows; no other e2e test function changed). Green on cf1ac92.
- Unit suite `task test` and `task lint` green on cf1ac92.
