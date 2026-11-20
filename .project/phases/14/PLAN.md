# Phase 14: Tray console: remaining resources

## Interfaces

The pure modules below import neither `lib/bridge.ts` nor `@wailsio/runtime`,
so Playwright's `unit` project loads them without a browser (DECISIONS
"Unit-tested UI modules stay free of the Wails runtime"). `lib/serverActions.ts`
of phase 11 stays as it is.

```ts
// lib/resourceActions.ts: one entry of a resource's actions menu or detail tab.
export type Input = Record<string, unknown>;
export type Confirm = "none" | "plain" | "typed-name";
export type ResourceAction = {
  id: string;                         // "resize", "assign", "add_subnet", "create", ...
  label: string;                      // menu or button label
  method: "GET" | "POST" | "PUT" | "DELETE"; // GET only for the zone file export
  path: (id: number, input: Input) => string; // id is the resource's ID (ignored by create)
  body?: (input: Input, id: number) => unknown;
  confirm: Confirm;
  needsInput: boolean;                // opens a dialog before it runs
  disabledHint?: string;              // set: shown disabled, never sent
};
// request builds method, path and body; undefined for a disabled entry.
export function request(a: ResourceAction, id: number, input?: Input): { method: string; path: string; body?: unknown } | undefined;
// postAction: POST /<base>/{id}/actions/<act>, confirm "none" and needsInput false unless given.
export function postAction(base: string, id: string, act: string,
  o: { label: string; body?: ResourceAction["body"]; confirm?: Confirm; needsInput?: boolean }): ResourceAction;
export function update(base: string, id: string, label: string, body: ResourceAction["body"], o?: { confirm?: Confirm; needsInput?: boolean }): ResourceAction; // PUT /<base>/{id}
export function remove(base: string, label: string, confirm: Confirm): ResourceAction;   // DELETE /<base>/{id}, id "delete"
export function createAt(base: string, label: string, body: ResourceAction["body"]): ResourceAction; // POST /<base>, id "create"
export function changeProtection(base: string): ResourceAction; // body: the boolean flags of input (delete, rebuild)
export function changeDnsPtr(base: string): ResourceAction;     // body {ip, dns_ptr}; an empty dns_ptr sends null
export function pick(input: Input, keys: string[]): Input;      // copies the named fields that are set

// lib/resources/<resource>.ts, one module per resource with actions:
// volumes, loadBalancers, networks, firewalls, floatingIps, primaryIps,
// zones, certificates, placementGroups, images. Each exports:
export const create: ResourceAction;                 // the create form's request (not in images)
export const menu: ResourceAction[];                 // the detail page's actions menu, in order; Delete last
export const tabs: Record<string, ResourceAction>;   // actions of the detail tabs and list row menus, by id

// lib/resources/zones.ts: RRSet entries take input.rr_name and input.rr_type,
// path /zones/{id}/rrsets/{rr_name}/{rr_type}[/actions/<act>].

// lib/resources/firewallRules.ts
export type RuleRow = { direction: "in" | "out"; protocol: "tcp" | "udp" | "icmp" | "esp" | "gre";
  port: string; ips: string; description: string };   // ips: addresses separated by commas or line breaks
export type Rule = { direction: "in" | "out"; protocol: string; port?: string;
  source_ips: string[]; destination_ips: string[]; description?: string };
export function toRules(rows: RuleRow[]): Rule[];   // in -> source_ips, out -> destination_ips; port only for tcp/udp; empty description left out
export function toRuleRows(rules: Rule[]): RuleRow[];

// lib/format.ts (labelText and createdText move here from SshKeys.svelte,
// which re-exports them)
export function labelText(labels: Record<string, string>): string; // "k=v, ..." sorted by key
export function createdText(created: string): string;              // "YYYY-MM-DD hh:mm"
```

Svelte building blocks (props):

```ts
// lib/ResourceActionsMenu.svelte: ActionsMenu from ResourceAction entries; a
// confirmation by `confirm` (typed-name asks for `name`), the `input` snippet
// for needsInput entries, then the request through runApi under a toast
// "<label> <name>"; the dialog closes at once (DECISIONS "Server dialogs
// close before the action ends").
{ project: string; id: number; name: string; actions: ResourceAction[];
  input?: Snippet<[ResourceAction, (i: Input) => void, () => void]>; // entry, submit, cancel
  ondone: (a: ResourceAction) => void }
// lib/ReverseDnsDialog.svelte: IP select and PTR field; empty resets to the default.
{ open: boolean (bindable); ips: string[]; current: Record<string, string | null>;
  onsubmit: (input: Input) => void }
```

Section convention: each new section lives in `ui/frontend/src/views/<view>/`
with its entry component `Section.svelte` (`{ project: string; page: Page }`:
title row with HelpButton, CloudStopped while the cloud is stopped, then
list, create form or detail by `page`). `views/ProjectConsole.svelte` finds
them with `import.meta.glob("./*/Section.svelte", { eager: true })` keyed by
folder name; the sidebar lists a new section only once its component exists.

View IDs (routes.ts `ProjectView`, help.ts `ViewId`, views.json) in sidebar
order: `servers`, `volumes`, `load-balancers`, `networks`, `firewalls`,
`floating-ips`, `primary-ips`, `dns`, `certificates`, `ssh-keys`,
`placement-groups`, `images`, `isos`, separator, `tokens`. `isos` has a
list and a detail page but no `new`.

e2e (tests/e2e/console.ts): `resourceTest = test.extend<{}, { shared: TestProject }>`
with a worker-scoped `shared` project `e2e-ui02-res-<time>` and its read/write
token, made through `hlab` at first use and deleted at worker teardown. All
phase 14 specs use it; each spec deletes what it made.

UI texts the e2e specs rely on: sidebar titles "Volumes", "Load Balancers",
"Networks", "Firewalls", "Floating IPs", "Primary IPs", "DNS",
"Certificates", "Placement Groups", "Images", "ISOs"; create buttons
"Create volume", "Create load balancer", "Create network", "Create firewall",
"Create floating IP", "Create primary IP", "Add zone", "Add certificate",
"Create placement group"; form fields labelled "Name", "Location", "Size",
"IP range", "Domain", "Domain names", "Protocol", "Type"; the detail tab
"Labels" with "Add label" and "Save labels" (LabelsTab); delete through the
row menu "Delete" and the dialog button "Delete".
Also: form submit buttons named "Create <resource>" (certificates "Add
certificate", DNS "Add zone", the same name as the opening button); the
firewall rules editor has an "Add inbound rule" button, per row a "Protocol"
select with option value `tcp`, fields "Port" and "Source IPs", inbound
first; the certificate "Type" select has an option value `managed`; the
floating and primary IP "Protocol" control has an "IPv4" entry; the ISOs list
shows all ISOs on one page (no paging).

## Plans

- [x] 14-01 Console registration of the new sections
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/unit/routes.test.ts
  - Reads: UI-SPEC "Views" (Project console), "Foundation" (icons); ARCHITECTURE "hlab-tray (tray program)"; DECISIONS "Console routes in a pure module, project console starts on Servers"; PLAN "Interfaces" (section convention, view IDs); ui/frontend/src/lib/routes.ts, help.ts, views.json, views/ProjectConsole.svelte, lib/SectionSidebar.svelte
  - Size: ~200 lines in 16 files (11 of them one-heading help stubs)
  - [x] ui/frontend/src/lib/routes.ts (`ProjectView` with the eleven views, `isos` without `new`), help.ts `ViewId`, views.json in sidebar order; tests/unit/routes.test.ts gets the new views.
  - [x] views/ProjectConsole.svelte: sidebar in UI-SPEC order with Lucide icons (Server, HardDrive, Scale, Network, Shield, Globe, MapPin, Book/Globe for DNS, ShieldCheck, Key, Group, Images/Camera, Disc), separator, API tokens; sections found by `import.meta.glob("./*/Section.svelte")`, listed only when present.
  - [x] help/volumes.md, load-balancers.md, networks.md, firewalls.md, floating-ips.md, primary-ips.md, dns.md, certificates.md, placement-groups.md, images.md, isos.md: one heading and one sentence each (the section plans write the text).

- [x] 14-02 Shared resource action building blocks
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-02 | Tests: - (proven through the resource modules' tables)
  - Reads: PLAN "Interfaces"; CONTEXT; ui/frontend/src/lib/serverActions.ts, views/servers/ServerActions.svelte, lib/ActionsMenu.svelte, ConfirmDialog.svelte, Dialog.svelte, api.ts, views/servers/tabs/Networking.svelte (reverse DNS), views/sshkeys/SshKeys.svelte, tests/e2e/console.ts, fixtures.ts
  - Size: ~330 lines in 6 files
  - [x] ui/frontend/src/lib/resourceActions.ts and lib/format.ts as in "Interfaces"; views/sshkeys/SshKeys.svelte re-exports `labelText`/`createdText` from lib/format.ts.
  - [x] lib/ResourceActionsMenu.svelte and lib/ReverseDnsDialog.svelte as in "Interfaces".
  - [x] tests/e2e/console.ts: `resourceTest` with the worker-scoped `shared` project.

- [x] 14-03 Volumes: actions, list and create
  - Wave: 2 | Depends on: 14-01, 14-02 | Review: alone
  - Requirements: UI-02 | Tests: ui/frontend/tests/unit/volumes.test.ts
  - Reads: CONTEXT "Corrections" (volume create); phases/11/RESEARCH.md "Volume"; REQUIREMENTS VOL-01, VOL-03, VOL-05; OpenAPI `/volumes`, `/volumes/{id}`, `/volumes/{id}/actions/*` in the reference; docs/development/api-coverage.md "Volumes"; views/sshkeys/SshKeys.svelte and SshKeyCreate.svelte as the pattern
  - Size: ~400 lines in 5 files
  - [x] ui/frontend/src/lib/resources/volumes.ts: `create` (size, name, labels; `location` without a server, `server` plus `automount` and `format` with one), `menu` (Resize, Attach, Detach plain, Change protection, Delete typed-name), `tabs`; tests/unit/volumes.test.ts.
  - [x] views/volumes/Section.svelte (list: Name, Size, Server, Location, Labels, Created; refresh 5 s; row menu Detach and Delete) and views/volumes/VolumeCreate.svelte (Size 10–10240 GB, Name, Location after Name, hidden once a Server is chosen; Server optional; Mount Automatic/Manual and Filesystem ext4/xfs only with a server; Labels); help/volumes.md.

- [x] 14-04 Volumes: detail
  - Wave: 3 | Depends on: 14-03 | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/e2e/volumes.spec.ts (through test/e2e/ui_test.go TestE2E/UI-02)
  - Reads: UI-SPEC "Building blocks" (Detail page, Confirm dialog); OpenAPI `volume` schema in the reference; docs/development/api-coverage.md "Volumes"; lib/DetailPage.svelte, LabelsTab.svelte, ProtectionTab.svelte
  - Size: ~300 lines in 3 files
  - [x] ui/frontend/src/views/volumes/VolumeDetail.svelte: tabs Overview (size, location, server, status, Linux device `/dev/disk/by-id/scsi-0HC_Volume_<id>` in a CopyField, created), Labels, Protection (delete); ResourceActionsMenu with dialogs Resize (larger size only), Attach (servers of the same location, automount); after Delete back to the list.
  - [x] tests/e2e/volumes.spec.ts.

- [x] 14-05 Load balancers: actions module
  - Wave: 2 | Depends on: 14-02 | Review: alone
  - Requirements: UI-02 | Tests: ui/frontend/tests/unit/loadBalancers.test.ts
  - Reads: CONTEXT "Corrections" (load balancer menu); REQUIREMENTS LB-01, LB-03, LB-04, LB-06, LB-07, LB-09; OpenAPI `POST /load_balancers` and `/load_balancers/{id}/actions/*` request bodies in the reference; docs/development/api-coverage.md "Load balancers"
  - Size: ~300 lines in 2 files
  - [x] ui/frontend/src/lib/resources/loadBalancers.ts: `create` (location, type, network, algorithm, targets, services, labels, name, public interface); service body from form input (tcp without `http`; http/https with sticky sessions, cookie, `redirect_http` and `certificates` only for https; health check; proxy protocol); targets server, label selector and IP with `use_private_ip`; `menu` (Change type plain, Change algorithm, Enable public interface, Disable public interface plain, Reverse DNS, Change protection, Delete plain); `tabs` add/update/delete service (delete plain), add/remove target (remove plain), attach/detach network (detach plain).
  - [x] tests/unit/loadBalancers.test.ts.

- [x] 14-06 Load balancers: list and create
  - Wave: 3 | Depends on: 14-01, 14-05 | Review: alone
  - Requirements: UI-02 | Tests: - (the UI-02 load balancer row of 14-07 creates through this form)
  - Reads: phases/11/RESEARCH.md "Load Balancer"; UI-SPEC "Building blocks" (Form); OpenAPI `/load_balancer_types`, `/locations`, `/networks`, `/servers`, `/certificates` lists in the reference
  - Size: ~400 lines in 4 files
  - [x] ui/frontend/src/views/load-balancers/Section.svelte (list: Name, Public IPv4, Type, Location, Targets, Services, Labels, Created; row menu Delete) and views/load-balancers/LoadBalancerCreate.svelte (Location, Type, Network, Targets, Services, Algorithm, Labels, Name).
  - [x] views/load-balancers/ServiceEditor.svelte and TargetEditor.svelte, reused by the detail tabs.

- [x] 14-07 Load balancers: detail, actions menu, networking and graphs
  - Wave: 4 | Depends on: 14-06 | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/e2e/loadbalancers.spec.ts (through TestE2E/UI-02)
  - Reads: OpenAPI `load_balancer` schema and `GET /load_balancers/{id}/metrics` in the reference; REQUIREMENTS LB-08, LB-09; lib/LineChart.svelte, views/servers/tabs/Graphs.svelte; CONTEXT "Corrections"
  - Size: ~380 lines in 4 files
  - [x] ui/frontend/src/views/load-balancers/LoadBalancerDetail.svelte: tabs Overview (IPs in CopyFields with reverse DNS, type, algorithm, location, public interface, health summary), Networking (attach, detach), Graphs (open connections, connections and requests per second, bandwidth over 1 h, 24 h, 7 d), Labels, Protection; ResourceActionsMenu with dialogs for Change type, Change algorithm and ReverseDnsDialog; help/load-balancers.md.
  - [x] tests/e2e/loadbalancers.spec.ts.

- [x] 14-08 Load balancers: targets and services tabs
  - Wave: 5 | Depends on: 14-07 | Review: alone
  - Requirements: UI-02 | Tests: - (requests proven by loadBalancers.test.ts)
  - Reads: OpenAPI `load_balancer.targets`, `services`, `health_status` in the reference; docs/development/api-coverage.md "Load balancers", "Certificates"
  - Size: ~300 lines in 3 files
  - [x] views/load-balancers/tabs/Targets.svelte (targets with health per service, add through TargetEditor, remove after confirmation) and tabs/Services.svelte (services, add and edit through ServiceEditor, delete after confirmation); LoadBalancerDetail.svelte adds both tabs.

- [x] 14-09 Networks: actions, list and create
  - Wave: 2 | Depends on: 14-01, 14-02 | Review: alone
  - Requirements: UI-02 | Tests: ui/frontend/tests/unit/networks.test.ts
  - Reads: CONTEXT "Corrections" (network); phases/11/RESEARCH.md "Network"; REQUIREMENTS NET-01, NET-05, NET-06; OpenAPI `/networks` and `/networks/{id}/actions/*` in the reference; docs/development/api-coverage.md "Networks"
  - Size: ~350 lines in 5 files
  - [x] ui/frontend/src/lib/resources/networks.ts: `create` (name, ip_range, labels), `menu` (Change IP range plain, Change protection, Delete typed-name), `tabs` add subnet (type `cloud` always, network zone, ip_range), delete subnet plain, add route, delete route plain; no `expose_routes_to_vswitch`; tests/unit/networks.test.ts.
  - [x] views/networks/Section.svelte (list: Name, IP range, Subnets, Servers, Labels, Created; row menu Delete) and views/networks/NetworkCreate.svelte (Name, IP range, Labels); help/networks.md (Change IP range only grows and not while servers are attached).

- [x] 14-10 Networks: detail
  - Wave: 3 | Depends on: 14-09 | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/e2e/networks.spec.ts (through TestE2E/UI-02)
  - Reads: OpenAPI `network` schema (subnets, routes, servers, load_balancers) in the reference; /locations network zones
  - Size: ~350 lines in 2 files
  - [x] ui/frontend/src/views/networks/NetworkDetail.svelte: tabs Overview (IP range, change IP range dialog), Subnets (add with network zone and range, delete), Routes (add destination and gateway, delete), Resources (attached servers and load balancers, read only, linking to their pages), Labels, Protection.
  - [x] tests/e2e/networks.spec.ts.

- [x] 14-11 Firewalls: actions module and rules
  - Wave: 2 | Depends on: 14-02 | Review: alone
  - Requirements: UI-02 | Tests: ui/frontend/tests/unit/firewalls.test.ts
  - Reads: CONTEXT "Corrections" (firewall); REQUIREMENTS FW-01, FW-05; OpenAPI `POST /firewalls`, `/firewalls/{id}/actions/set_rules`, `apply_to_resources`, `remove_from_resources` in the reference; docs/development/api-coverage.md "Firewalls"; DECISIONS "Firewalls applied by label selector cannot be removed from the server tab"
  - Size: ~250 lines in 3 files
  - [x] ui/frontend/src/lib/resources/firewallRules.ts (`toRules`, `toRuleRows`) and lib/resources/firewalls.ts: `create` (name, labels, rules, `apply_to` with servers and label selectors), `menu` (Delete plain), `tabs` set rules, apply to server, apply to label selector, remove server plain, remove label selector plain.
  - [x] tests/unit/firewalls.test.ts.

- [x] 14-12 Firewalls: list and create
  - Wave: 3 | Depends on: 14-01, 14-11 | Review: alone
  - Requirements: UI-02 | Tests: - (the UI-02 firewall row of 14-13 creates through this form)
  - Reads: phases/11/RESEARCH.md "Firewall"; UI-SPEC "Building blocks" (Form); OpenAPI `firewall` schema in the reference
  - Size: ~380 lines in 4 files
  - [x] ui/frontend/src/views/firewalls/Section.svelte (list: Name, Rules in/out, Applied to, Labels, Created; row menu Delete) and views/firewalls/FirewallCreate.svelte (Rules, Apply to, Labels, Name).
  - [x] views/firewalls/RulesEditor.svelte (inbound and outbound tables of RuleRows: protocol, port, IPs, description, add and remove) and views/firewalls/ApplyEditor.svelte (servers and label selectors), reused by the detail.

- [x] 14-13 Firewalls: detail
  - Wave: 4 | Depends on: 14-12 | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/e2e/firewalls.spec.ts (through TestE2E/UI-02)
  - Reads: OpenAPI `firewall.applied_to` in the reference; docs/development/api-coverage.md "Firewalls"
  - Size: ~300 lines in 3 files
  - [x] ui/frontend/src/views/firewalls/FirewallDetail.svelte: tabs Rules (RulesEditor, "Save rules" through set_rules), Resources (servers and label selectors with apply and remove, servers matched by a selector shown under it), Labels; actions menu Delete; help/firewalls.md.
  - [x] tests/e2e/firewalls.spec.ts.

- [x] 14-14 Floating IPs
  - Wave: 2 | Depends on: 14-01, 14-02 | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/unit/floatingIps.test.ts, ui/frontend/tests/e2e/floatingips.spec.ts (through TestE2E/UI-02)
  - Reads: CONTEXT "Corrections" (IPs); phases/11/RESEARCH.md "Floating IP"; REQUIREMENTS FIP-01, RDNS-01; OpenAPI `/floating_ips` and actions in the reference; docs/development/api-coverage.md "Floating IPs", "Reverse DNS"
  - Size: ~420 lines in 6 files
  - [x] ui/frontend/src/lib/resources/floatingIps.ts: `create` (type, `home_location` or `server`, name, description, labels), `menu` (Assign, Unassign plain, Reverse DNS, Change protection, Delete plain), `tabs`; tests/unit/floatingIps.test.ts.
  - [x] views/floating-ips/Section.svelte (list: Name, IP, Protocol, Home location, Server, Labels, Created; row menu Unassign and Delete), FloatingIpCreate.svelte (Location or Server, Protocol, Name, Labels), FloatingIpDetail.svelte (Overview with IP in a CopyField, description edit, server, reverse DNS; Labels; Protection; Assign dialog with servers); help/floating-ips.md.
  - [x] tests/e2e/floatingips.spec.ts.

- [x] 14-15 Primary IPs
  - Wave: 2 | Depends on: 14-01, 14-02 | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/unit/primaryIps.test.ts, ui/frontend/tests/e2e/primaryips.spec.ts (through TestE2E/UI-02)
  - Reads: CONTEXT "Corrections" (IPs); phases/11/RESEARCH.md "Primary IP"; REQUIREMENTS PIP-01, RDNS-01; OpenAPI `/primary_ips` and actions in the reference; docs/development/api-coverage.md "Primary IPs"
  - Size: ~420 lines in 6 files
  - [x] ui/frontend/src/lib/resources/primaryIps.ts: `create` (name, type, location, without a server `location`, with a server `assignee_type: "server"` and `assignee_id` and no `location`, `auto_delete`, labels; no datacenter), `menu` (Assign, Unassign plain, Reverse DNS, Change protection, Delete plain), `tabs` auto_delete through PUT; tests/unit/primaryIps.test.ts.
  - [x] views/primary-ips/Section.svelte (list: Name, IP, Protocol, Location, Assignee, Auto delete, Labels, Created), PrimaryIpCreate.svelte (Name, Location, Protocol, Server, Auto delete, Labels), PrimaryIpDetail.svelte (Overview with auto_delete switch, assignee, reverse DNS; Labels; Protection; Assign dialog offering servers of the location that are off); help/primary-ips.md.
  - [x] tests/e2e/primaryips.spec.ts.

- [x] 14-16 DNS: actions, list and create
  - Wave: 2 | Depends on: 14-01, 14-02 | Review: alone
  - Requirements: UI-02 | Tests: ui/frontend/tests/unit/zones.test.ts
  - Reads: CONTEXT "Corrections" (DNS); phases/11/RESEARCH.md "DNS zone"; REQUIREMENTS DNS-01; OpenAPI `/zones`, `/zones/{id}/actions/*`, `/zones/{id}/rrsets*`, `/zones/{id}/zonefile` in the reference; docs/development/api-coverage.md "Zones", "Records", "Zone files"
  - Size: ~400 lines in 4 files
  - [x] ui/frontend/src/lib/resources/zones.ts: `create` (name, mode primary with optional `zonefile` or secondary with `primary_nameservers`, ttl, labels), `menu` (Change default TTL, Import zone file plain, Change protection, Delete typed-name), `tabs` change primary nameservers, export zone file, RRSet create, change TTL, add records, remove records plain, labels, change protection, delete plain; tests/unit/zones.test.ts.
  - [x] views/dns/Section.svelte (list: Name, Mode, Records, TTL, Labels, Created; row menu Delete) and views/dns/ZoneCreate.svelte (Domain, Type empty / import zone file / secondary with primary nameservers, TTL, Labels; "Add zone").

- [x] 14-17 DNS: detail and records
  - Wave: 3 | Depends on: 14-16 | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/e2e/dns.spec.ts (through TestE2E/UI-02)
  - Reads: OpenAPI `zone` and `rrset` schemas in the reference; docs/development/api-coverage.md "Records"; DECISIONS on DNS of phase 07 that name the SOA
  - Size: ~400 lines in 4 files
  - [x] ui/frontend/src/views/dns/ZoneDetail.svelte (tabs Overview with nameservers, default TTL, record count; Records; Labels; Protection; ResourceActionsMenu with Change default TTL) and views/dns/tabs/Records.svelte (RRSets with name, type, TTL, values; add RRSet with records; per RRSet change TTL, add and remove records, labels, protection, delete; the SOA row shows only Change TTL); help/dns.md.
  - [x] tests/e2e/dns.spec.ts.

- [x] 14-18 DNS: zone file and primary nameservers tabs
  - Wave: 4 | Depends on: 14-17 | Review: alone
  - Requirements: UI-02 | Tests: - (requests proven by zones.test.ts)
  - Reads: docs/development/api-coverage.md "Zone files", "Zones" (secondary); OpenAPI `GET /zones/{id}/zonefile`, `import_zonefile`, `change_primary_nameservers` in the reference
  - Size: ~250 lines in 3 files
  - [x] views/dns/tabs/ZoneFile.svelte (export in a monospace read-only text area with copy; import from pasted text after a plain confirmation, the API's line error under the field) and views/dns/tabs/PrimaryNameservers.svelte (address, port, TSIG for secondary zones); ZoneDetail.svelte shows Records and Zone file for primary zones and Primary nameservers for secondary ones.

- [x] 14-19 Certificates
  - Wave: 2 | Depends on: 14-01, 14-02 | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/unit/certificates.test.ts, ui/frontend/tests/e2e/certificates.spec.ts (through TestE2E/UI-02)
  - Reads: CONTEXT "Corrections" (managed certificate); phases/11/RESEARCH.md "Certificate"; REQUIREMENTS CERT-01, CERT-02; OpenAPI `/certificates` and `retry` in the reference; docs/development/api-coverage.md "Certificates"
  - Size: ~420 lines in 6 files
  - [x] ui/frontend/src/lib/resources/certificates.ts: `create` (uploaded: name, certificate, private_key; managed: name, domain_names; labels), `menu` (Retry, shown only for a failed managed certificate; Delete plain); tests/unit/certificates.test.ts.
  - [x] views/certificates/Section.svelte (list: Name, Type, Domains, Status, Valid until, Labels, Created; row menu Delete), CertificateCreate.svelte (Type, then Name, Certificate and Private key, or Name and Domain names; Labels; "Add certificate"), CertificateDetail.svelte (Overview: type, status with the issuance error, domains, fingerprint, validity, used by; Labels); help/certificates.md (managed: domains under a primary zone of this project, issued by HLab's CA; `dns_zone_not_found` otherwise).
  - [x] tests/e2e/certificates.spec.ts.

- [x] 14-20 Placement groups
  - Wave: 2 | Depends on: 14-01, 14-02 | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/e2e/placementgroups.spec.ts (through TestE2E/UI-02)
  - Reads: phases/11/RESEARCH.md "Placement Group"; REQUIREMENTS SRV-22; OpenAPI `/placement_groups` in the reference; docs/development/api-coverage.md "Placement groups"
  - Size: ~300 lines in 5 files
  - [x] ui/frontend/src/lib/resources/placementGroups.ts (`create` with type spread, `menu` Rename and Delete plain) and views/placement-groups/Section.svelte (list: Name, Type, Servers, Labels, Created), PlacementGroupCreate.svelte (Name, Type spread shown fixed, Labels), PlacementGroupDetail.svelte (Overview with its servers linking to them; Labels); help/placement-groups.md.
  - [x] tests/e2e/placementgroups.spec.ts.

- [x] 14-21 Images
  - Wave: 2 | Depends on: 14-01, 14-02 | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/unit/images.test.ts
  - Reads: CONTEXT "Corrections" (images), "Answers"; OpenAPI `/images`, `PUT /images/{id}`, `change_protection` in the reference; docs/development/api-coverage.md "Snapshots and backups"; views/servers/tabs/ImageTable.svelte
  - Size: ~330 lines in 5 files
  - [x] ui/frontend/src/lib/resources/images.ts: `menu` (Edit description, Convert to snapshot only for backups, no confirmation, `PUT /images/{id}` with `type: snapshot`; Change protection; Delete plain); tests/unit/images.test.ts.
  - [x] views/images/Section.svelte (the project's snapshots and backups, no system images: Name/description, Type, Size, Created from, Labels, Created; empty state saying images are made from a server's Backups and Snapshots tabs) and views/images/ImageDetail.svelte (Overview, Labels, Protection); help/images.md.

- [x] 14-22 ISOs
  - Wave: 2 | Depends on: 14-01, 14-02 | Review: alone
  - Requirements: UI-02, UI-07 | Tests: ui/frontend/tests/e2e/isos.spec.ts (through TestE2E/UI-02)
  - Reads: OpenAPI `/isos` in the reference; docs/development/api-coverage.md "ISOs"
  - Size: ~180 lines in 4 files
  - [x] ui/frontend/src/views/isos/Section.svelte (read-only list: Name, Description, Architecture, Deprecated) and views/isos/IsoDetail.svelte (Overview; a note that ISOs attach in a server's ISO tab); help/isos.md.
  - [x] tests/e2e/isos.spec.ts.

- [x] 14-23 UI-02 full run and time limit
  - Wave: 6 | Depends on: 14-04, 14-08, 14-10, 14-13, 14-14, 14-15, 14-18, 14-19, 14-20, 14-21, 14-22 | Review: alone
  - Requirements: UI-02 | Tests: test/e2e/ui_test.go TestE2E/UI-02 (all specs)
  - Reads: CONTEXT "Answers"; CLAUDE.md "Tests"; test/e2e/ui_test.go `runSpec`, `testUI02`
  - Size: ~15 lines in 1 file
  - [x] Run `task e2e -- -run 'TestE2E/UI-02$' -v` once in full, then set the UI-02 limit in test/e2e/ui_test.go to about twice that run and update `testUI02`'s comment to name all sections.

- [x] 14-24 Documentation
  - Wave: 7 | Depends on: 14-23 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06, DOC-07 | Tests: test/docs/docs_test.go, ui/frontend/tests/unit/help.test.ts
  - Reads: SPEC "Documentation"; REQUIREMENTS "Documentation (DOC)"; CONTEXT; DECISIONS phase 14 entries
  - Size: ~250 lines in 5 files
  - [x] Check the eleven help texts against the built views (only existing views and buttons).
  - [x] README.md (tray program: every resource section), docs/guide.md (the console for each resource, at the matching sections), docs/development/components.md (resourceActions, lib/resources, section convention); regenerate the console screenshots with `task docs:screenshots`.

## Findings from manual testing

- change (2026-11-20, /wcc:report during UI-09, then /wcc:change): console
  navigation like the Hetzner Cloud Console, and own ISOs. Build it on this
  branch before UI-09 is checked; plan it as new plans 14-25 onward.
  - Sidebar: UI-SPEC.md "Views > Project console" (collapsible groups
    "Cloud" and "Networking", single entry "Security", collapsed groups
    remembered) and "Building blocks > Section tabs". Reference screenshot
    of the user (Hetzner Console, dark): group headings CLOUD, NETWORKING,
    STORAGE in uppercase muted small print, each with a down chevron at the
    right edge, a thin horizontal line between the groups, entries with an
    outline icon and the name, and at the bottom below a line a single
    entry "Security" with a key icon. HLab has no Storage group.
  - Tabs per entry: Servers (Servers, Snapshots, Backups, Placement
    Groups), Volumes, Firewalls, Load Balancers, Networks, IPs (Primary
    IPs, Floating IPs), DNS (DNS Zones), Security (SSH Keys, API tokens,
    Certificates). The `images` view splits into `snapshots` and `backups`
    (images of that type; system images are not listed); the project
    console has no ISOs section any more. The user may describe the tab
    design further in a later report; until then UI-SPEC.md "Section
    tabs" holds.
  - Own ISOs: CAT-10 (`isos.custom.json`, `/isos` as type `private`,
    `custom_isos` problem, copy of local files on start and
    `/settings/reload`) and UI-10 (Administration view ISOs with add,
    edit, remove; the ISOs view moves from the project console to
    `#/admin/isos`); ARCHITECTURE.md "images", "hlab-tray", "Management
    API" and "Files and formats".
  - Tests: CAT-10 unit, UI-10 e2e (new rows in TESTS.md); UI-02 e2e specs
    that open sections through the sidebar adjust to the entries and tabs
    (Adjusts: what they use, not what they check).
  - Documentation: README.md (tray program: project console and
    Administration), docs/guide.md (console sections, ISOs),
    docs/development/components.md (sidebar, section convention), the help
    texts of the moved and new views; regenerate the screenshots.
- change (2026-11-20, /wcc:report during UI-09, then /wcc:change):
  Create volume like the Hetzner Console's volume dialog. Build it on this
  branch with the sidebar change, before UI-09 is checked, as one more
  plan after 14-25 onward.
  - What the user did: opened Create volume in the console and compared it
    with the Hetzner Console. hlab shows size as a plain number input,
    then Name, a Location select (preselected), an optional Server ("No
    server"); Mount and Filesystem appear only after a server is chosen,
    so the user did not find them.
  - Reference screenshot of the user (Hetzner Console "Volume erstellen",
    dark): hexagon volume icon at the top left; at the right a large
    number field "10" with stacked + / − buttons and "GB" beside it; a
    full-width slider below with "10 GB" at the left end and "1.024 GB" at
    the right end; field "Name *" prefilled "volume-1" with a clear ×;
    required select "Server wählen *" with an info icon line "the volume
    is bound to the location of the server"; "Mount-Optionen wählen:" with
    a segmented control Automatisch | Manuell and the line "your volume is
    formatted automatically and mounted on your server"; "Dateisystem
    wählen:" with inline radios EXT4 (selected) and XFS; price and the
    buttons Cancel / Create at the bottom.
  - Expected (UI-SPEC.md "Views > Create per type", Create volume): size
    field with +/− stepper (1 GB) and a slider over the full range 10 to
    10240 GB of VOL-01 (not Hetzner's 1024), bound to one value; Name
    (prefill "volume-1" only if trivial, otherwise empty); Server
    required, no Location field, hint "The volume is bound to the server's
    location."; Mount options as a segmented control Automatic | Manual
    with a one-line explanation, default Automatic; Filesystem inline
    radios ext4 / xfs, default ext4; both always visible; Labels; no
    price. The request always sends `server`, `automount` and `format`;
    the API keeps accepting `location` without a server (VOL-01
    unchanged). With no server in the project the select is empty and
    submit shows the required-field error under Server.
  - Requirements: UI-02, UI-09. Code: ui/frontend/src/views/volumes/VolumeCreate.svelte,
    ui/frontend/src/lib/resources/volumes.ts.
  - Tests: tests/unit/volumes.test.ts keeps its rows; the console form row
    uses a server. Adjusts: tests/e2e/volumes.spec.ts, which creates a
    volume without a booted server (CONTEXT Q1): it now needs a server in
    the shared project, created through the API in the spec setup (what it
    uses, not what it checks).
  - Documentation: help/volumes.md, docs/guide.md (volumes in the
    console), README screenshots if the volume form is among them.
- minor (2026-11-20, /wcc:report, wanted in this phase by the user): action
  toasts do not name the resource type. The user deleted the network
  "Test" from the Networks list; the toast read "Delete Test" / "Done"
  (screenshot `phases/14/feedback/toast-without-type.png`: dark toast,
  green bar on the left, check icon, title "Delete Test", line "Done",
  close button). Expected: the title names action and type like the
  UI-SPEC.md wording "Delete volume", followed by the name, e.g. "Delete
  network Test", "Assign floating IP fip-1", "Add record www A". Cause:
  every caller builds the title as `${label} ${name}` (all `runApi(project,
  ...)` calls in `ui/frontend/src/views/**` and
  `lib/ResourceActionsMenu.svelte`, servers of phase 11 included). Fix it
  once in a shared place (the resource's type name passed to the actions
  menu / runApi helper), not per view; one unit test for the title
  builder, plus the existing UI-02 specs adjusted where they match toast
  titles.
- minor (2026-11-20, /wcc:report during UI-09; the user chose to fix it
  on this branch with the changes above, before UI-09 is checked):
  Create placement group, field Type.
  - What the user did: opened Create placement group. The form shows Name,
    then "Type" as a text input holding "spread" (readonly, looks editable)
    with the hint "Spread: each server of the group runs on its own host
    (in HLab, the group only records its servers).", then Labels with
    "+ Add label", and the buttons Cancel / Create placement group.
  - Expected: Type is a select (dropdown) with the single option "spread",
    preselected, same hint below it; the request still sends
    `type: "spread"`.
  - Requirement: UI-02. Code:
    ui/frontend/src/views/placement-groups/PlacementGroupCreate.svelte:64.
  - Tests: none new (cosmetic); existing unit and e2e rows stay.
- change (2026-11-20, /wcc:report, wish built in this phase by the user):
  labels as chips like the Hetzner Console. Today `lib/LabelsField.svelte`
  shows one row per label (key input, value input, remove button) and an
  "Add label" button; `lib/LabelsTab.svelte` the same on detail pages.
  UI-SPEC.md "Building blocks > Labels field" holds the new behaviour;
  build it once in `lib/` and use it in every create form (the twelve
  users of LabelsField) and in LabelsTab; `lib/labels.ts` keeps the
  conversion to the API's labels object (parsing `key=value` and the
  rule check go there, with unit tests). Screenshots of the user
  (Hetzner Console, dark, German text): `phases/14/feedback/hetzner-labels.png`
  shows the heading "Labels", the rule text, a dark card with a large
  resizable text box holding one chip "Label | value" (key part on a grey
  background, value part plain, rounded border) and the text cursor
  after it; `phases/14/feedback/hetzner-labels-selected.png` shows the
  same chip selected: key part filled red (Hetzner's accent; HLab uses its
  own accent colour), red border. The step markers of the Hetzner form
  (check in a circle, dashed line) are not part of this change. Adjust
  the UI-02 specs that fill label rows (Adjusts: what they use).

## Close

- e2e: `task e2e -- -run 'TestE2E/UI-0[1-6]$' -v` green on 691eb1a (UI-02 12 of 12 specs; the other UI rows because `runSpec` changed in 14-23).
- Unit suite, frontend unit tests and linter green on 691eb1a.
- Verifier blockers fixed: Rename for the seven resources without it (0804ea2), components.md sidebar order and `noNew` sentence (691eb1a).
- Manual check UI-09: open (CHECKS.md); the phase stops for it unmerged.
