# Components

```
 host (Windows)                                  appliance VM (Debian 13)
 hlab (CLI) --pipe--> hlabd ---- HTTP ---------> hlab-agent
 hlab-tray  --pipe-->   |
                        |  Hetzner API            routes, DNS, API proxy,
                        |  state (SQLite)         nftables isolation,
                        |  HLab CA                firewalls, HAProxy per
                        |                         load balancer
                        |  Hyper-V, host network
                        +---- Hyper-V ----> server VMs (cloud-init seed ISO)
```

A client calls `http://api.hetzner.internal/v1`. The name resolves (NRPT
rule on the host) to the appliance at the first address of `public_range`
(`198.19.0.1` by default), whose agent proxies
port 80 to hlabd on the second address, port 8780 (`198.19.0.2:8780`);
[internal/transit](../../internal/transit) derives these addresses from
the range. hlabd keeps the state, drives
Hyper-V and the host network, and pushes the complete network
configuration to the agent as one document after every change. Every
packet of a server passes the appliance.

The appliance has two adapters on the virtual switch. The first is the
promiscuous port of the public segment, a private VLAN in which every
server adapter is an isolated port. The second, the trunk adapter (MAC
`02:48:4c:00:00:02`), carries the VLANs 2 to 4092 tagged; each private
network gets one of them, and each server adapter in that network is an
access port of its VLAN. Each private adapter denies the MACs of the
other members of its network, so servers of a network also talk only
through the appliance. On the appliance every network is a VRF of its
own, so networks with overlapping ranges stay apart.

## Programs

| Program | Code | Role |
|---|---|---|
| hlabd | [cmd/hlabd](../../cmd/hlabd) | Windows service; runs the cloud; `install` and `uninstall-cleanup` register and remove it. |
| hlab | [cmd/hlab](../../cmd/hlab), [internal/cli](../../internal/cli) | Management command. `cloud start/stop` use the service controller, the rest the management pipe. |
| hlab-agent | [cmd/hlab-agent](../../cmd/hlab-agent), [internal/agent](../../internal/agent) | Static Linux program in the appliance; it listens on the appliance address that the file `transit` of its seed ISO names: `/32` routes to the servers, API proxy, DNS (see [internal/agent/dns.go](../../internal/agent/dns.go)): authoritative for `hetzner.internal`, the IPv4 reverse zones of the public range (PTRs from the document) and the users' primary zones, the most specific zone winning, with wildcards per RFC 4592 and CNAME chains followed up to 8 steps; other names, secondary zones included, forwarded to the host's DNS servers, nftables isolation, and the metadata service on `169.254.169.254:80`, which also hands pending root password resets to the guests, collects their confirmations and lists each server's automount volumes at `/hlab/v1/volumes`. Per private network it creates a VLAN subinterface `t<vlan>` of the trunk adapter in the VRF `vrf<vlan>` (table 1000+VLAN) with the network gateway, `/32` routes to the members and their alias IPs, the network's routes, an unreachable default and a metadata listener bound to the VRF; it routes floating IPs to their server's public IPv4. The nftables rules drop forwarded traffic and metadata requests from a server whose source is not one of its addresses: on the public segment its public IPv4 and floating IPs, on each `t<vlan>` its private IP, alias IPs and the destinations of the network routes it is gateway for; each `t<vlan>` has its own conntrack zone; this isolation lives in table `inet hlab`. The server firewalls live in a table of their own, `inet hlab_fw`, replaced as a whole at every push: `CompileFirewalls` turns the document into one filter per server (its public IPv4 and the floating IPs routed to it, the union of the rules of its firewalls, IPv4 prefixes only), and the forward chain, at filter priority + 10, lets packets of established and related connections pass and sends every other packet that leaves the public interface to a filtered address through that server's inbound allow rules, and every packet that arrives on it from a server with outbound rules through its outbound allow rules. It matches only the public interface, so private networks and the appliance's own services (DNS, metadata, API proxy) are never filtered. It also runs jobs that need Linux tools on a disk hlabd hot-adds to the appliance (`POST /v1/jobs`): `mkfs` formats a volume, `rescue-prepare` gives a copy of the debian-13 image new partition and filesystem identifiers for the rescue base. Each load balancer of the document gets the network namespace `lb<ID>` ([internal/agent/lb_linux.go](../../internal/agent/lb_linux.go)): a veth to the root namespace for its public IPv4, routed there so the firewalls apply, and while it is in a network a veth into that network's VRF; the root ends all carry `169.254.0.1/32` with onlink routes. In the namespace runs one HAProxy master-worker, whose `haproxy.cfg` [internal/agent/haproxy.go](../../internal/agent/haproxy.go) renders from the document (frontend `fe<port>`, backend `be<port>`, server names are the target keys such as `server:4` or `ip:198.19.0.9`); a changed configuration is checked with `haproxy -c` and loaded through the master CLI. hlabd reads target health and counters every 10 s through the agent's `GET /v1/status`, which answers from HAProxy's `show stat` ([internal/agent/haproxy_stat.go](../../internal/agent/haproxy_stat.go)). |
| hlab-tray | [cmd/hlab-tray](../../cmd/hlab-tray), [internal/bridge](../../internal/bridge), [ui/frontend](../../ui/frontend) | Tray program on Wails v3: tray icon (`icons/*.ico`, the application icon with a status dot) with its menu and a WebView2 window with the Svelte frontend, one instance per user and profile. See [The tray program](#the-tray-program). |

## The tray program

[cmd/hlab-tray](../../cmd/hlab-tray) wires the window, the tray icon and
three services of [internal/bridge](../../internal/bridge) that the
frontend calls by name. The bridge does not import Wails, so its tests run
without a window:

- `Service` forwards `Call(method, path, body)` to the management API
  through [internal/mgmtclient](../../internal/mgmtclient), and edits the
  settings and own server types: it validates with the packages hlabd
  uses ([internal/settings](../../internal/settings),
  [internal/catalog](../../internal/catalog)), writes `settings.toml` and
  `server-types.custom.json` itself and then calls
  `POST /mgmt/v1/settings/reload` while the cloud runs. `Locations` lists
  the catalog's locations for the location checkboxes of an own server
  type, also while the cloud is stopped.
- `Cloud` polls every 2 s: the service manager's state, `/mgmt/v1/status`
  while the cloud runs, and `status.json` through
  [internal/cloudstatus](../../internal/cloudstatus) while it is stopped.
  `Tray` maps the state to icon, tooltip and menu; a change goes to the
  window as the event `cloud`. Start and stop go through the service
  manager, like `hlab cloud start/stop`.
- `Logs` follows `GET /mgmt/v1/logs?follow=1` while the cloud runs and the
  log files while it is stopped, and sends the lines to the window in
  100 ms batches as the event `logs`. `Search` and `Older` read the log
  files directly through `logfile.Search` and `logfile.Before` (at most
  1000 lines each), so they work whether the cloud runs or not.

The frontend in [ui/frontend](../../ui/frontend) is Svelte 5 with Vite;
[ui/embed.go](../../ui/embed.go) embeds its build. The views are in
`src/views` and listed in `src/views.json`; the shell has three areas
(project list `#/projects`, project console, Administration
`#/admin/<view>`), routed by `location.hash`.
[src/lib/routes.ts](../../ui/frontend/src/lib/routes.ts) turns routes into
hashes and back; a project console page is
`#/projects/<name>/<section>` for a list, `.../new` for a create form and
`.../<id>/<tab>` for a detail page. `src/state.ts` keeps the current route,
the cloud state and the view titles. The building blocks (top bar, section
sidebar, table, form field, dialogs, toast, help button) are in `src/lib`,
the bridge calls in `src/lib/bridge.ts`.

The project console
([src/views/ProjectConsole.svelte](../../ui/frontend/src/views/ProjectConsole.svelte))
shows its sections in its sidebar: Servers, the other resource sections,
SSH Keys and, after a separator, API tokens. Servers, SSH Keys and API
tokens are wired in by hand. Every other section lives in
`src/views/<view>/` with the entry component `Section.svelte`
(props `project` and `page`), which renders the title row with the help
button, the "start the cloud" notice while the cloud is stopped, and then
the list, the create form or the detail page by `page`. The console finds
these components with `import.meta.glob("./*/Section.svelte")`, keyed by
folder name, and lists a section in the sidebar only once its component
exists. The resource sections reach the project's Hetzner API through the
management API's project proxy and are built from these parts of
`src/lib`:

- `api.ts`: `api` sends one request, `listAll` reads every page of a list,
  `runApi` runs a request under an action toast.
- `actions.ts`: `runAction` follows the returned action and its next
  actions to the end and shows their progress in a toast (`toast.ts`,
  `Toast.svelte`).
- `DetailPage.svelte` (title row with status, actions menu and tabs),
  `ActionsMenu.svelte`, `FormPage.svelte`, `LabelsField.svelte`,
  `LabelsTab.svelte`, `ProtectionTab.svelte`, `SecretDialog.svelte` (a
  one-time root password) and `LineChart.svelte`; `formErrors.ts` puts an
  API error's field messages under the form fields, `labels.ts` converts
  label rows.
- `serverActions.ts` describes every server action of the actions menu and
  the detail tabs (label, method, path, body, confirmation, whether the
  answer carries a root password); the views in `src/views/servers` only
  render it, so its unit test covers every request.
- [resourceActions.ts](../../ui/frontend/src/lib/resourceActions.ts) does
  the same for the other resources: a `ResourceAction` holds an action's
  label, method, path, body, confirmation (`none`, `plain` or
  `typed-name`), whether it opens a dialog for input first, and an
  optional hint that shows it disabled. `request` turns an entry and its
  input into the request; builders make the common entries (`postAction`,
  `update`, `remove`, `createAt`, `changeProtection`, `changeDnsPtr`).
- `src/lib/resources/<resource>.ts` (volumes, load balancers, networks,
  firewalls, floating IPs, primary IPs, zones, certificates, placement
  groups, images) exports per resource the create form's request
  `create` (images have none), the detail page's actions menu `menu`
  with Delete last, and `tabs`, the actions of the detail tabs and row
  menus by ID. `firewallRules.ts` converts between the rows of the rules
  editor and the API's rules. These modules import neither the bridge nor
  the Wails runtime, so their unit tests in `tests/unit` check every
  request without a browser.
- `ResourceActionsMenu.svelte` renders a `menu`: it asks for confirmation
  as the entry says (typing the name for `typed-name`), shows the
  section's input dialog for entries that need input, and runs the request
  under a toast. `ReverseDnsDialog.svelte` is the shared reverse DNS
  dialog, and `format.ts` formats labels and creation times for the
  tables.

A new view needs its ID in `views.json` and in `ViewId` of `src/help.ts`,
its title and area in `src/state.ts` and a help text
`src/help/<view>.md`, which the `?` button shows. An Administration view
also needs its ID in `AdminView` of `routes.ts` and an icon and a
branch in `src/App.svelte`; a project section needs its ID in
`ProjectView` and `projectViews` of `routes.ts` (and in `listOnly` if it
has no create form or detail pages, in `noNew` if it has detail pages but
no create form), an entry with title and icon in the `sections` list of
`ProjectConsole.svelte`, and its `src/views/<view>/Section.svelte`; for
its requests, add a module under `src/lib/resources` with a unit test. The build tag `e2e` adds the test hooks of
[cmd/hlab-tray/e2e.go](../../cmd/hlab-tray/e2e.go) (see
[Build and test](build-and-test.md#end-to-end-tests)).

The application icon (a white "H" on a rounded square in the accent
blue), the four tray state icons, the `rsrc_windows_amd64.syso` of each
Windows program that embeds the icon in the EXE, the frontend's
`src/assets/app-icon.svg` and the README banner `docs/images/banner.svg`
are drawn by [tools/icons](../../tools/icons) from one geometry. The
generated files are committed; `task icons` renews them, and `task build`
only links the `.syso` files.

## Packages of hlabd

| Package | Role |
|---|---|
| [internal/cloud](../../internal/cloud) | Start and stop of one cloud: settings, host checks, store, switch, appliance (with its trunk adapter, added at start to an appliance made without one), host network, API, management pipe; the list of resource modules. After the modules recover it sets one NRPT rule per stored zone and starts the shadow check of every public primary zone, whose result the status of the management pipe reports as `shadowing_zones`. Opens the HLab CA (`openCA`, [internal/cloud/ca.go](../../internal/cloud/ca.go)), renews its CRL, serves it without a token at `/hlab/v1/ca.crl` on the API listener and adds the CA to the trust stores while `trust_ca` is on; a failed trust is the status problem `trust_ca`, not a failed start. Runs appliance jobs one at a time (the disk at LUN 1 of the appliance, detached again on every path) and downloads the packages of `catalog/appliance-debs.json` into `<data>\cache\debs` for the appliance's seed ISO. Opening the state ([internal/cloud/lifecycle.go](../../internal/cloud/lifecycle.go)) refuses an incompatible state (`state_incompatible`) or one with public IPv4s outside a changed range (`public_range_changed`) unless the start parameter `discard-state` asks for a discard, and records the range of every start. After the first agent push it calls `Restore` of the `Lifecycle` modules; at stop it stops the API and the jobs, reads `stop_behaviour` again, calls `Halt`, then stops the appliance and the host network. At start it creates a missing `server-types.custom.json` with the permissions of settings.toml; `reload` ([internal/cloud/reload.go](../../internal/cloud/reload.go)) applies `logging.level` and the own server types while the cloud runs. |
| [internal/api](../../internal/api) | HTTP layer of the Hetzner API: routing under `/v1`, token authentication, read-only guard, JSON checks, error and list helpers. |
| [internal/resource](../../internal/resource) | The `Module` interface every resource type implements, and the optional `NetContributor`, `Deleter`, `Reconciler` (make the VM, disk and seed match the stored state), `Scheduled` (time-driven jobs), `Recoverer` (cleans up records an interrupted action left half-made, at cloud start) `ActionUser` (receives the action manager to start actions outside a request, as the servers' daily backups do) and `Lifecycle` (`Halt` saves or shuts down the running VMs at cloud stop, `Restore` sets their Hyper-V stop action and resumes or powers on at start what ran; implemented by the servers, whose stored status records which ran). |
| [internal/resources](../../internal/resources) | One package per resource type: actions, locations, datacenters, server types, pricing, images (catalog images and the projects' snapshots and backups), ISOs, SSH keys, placement groups, servers (with snapshots, backups, rescue and ISOs), networks (with the server actions attach, detach and alias IPs), primary IPs, floating IPs, firewalls, volumes, zones, load balancer types, load balancers, certificates. Zones keep their RRSets inside the zone's document; every zone action publishes all zones (agent push, then `hostnet` `SetZones` for the NRPT rules) under one lock, and `Shadow` asks the public DNS whether a primary zone outside the private-use suffixes hides a real domain. Servers, primary IPs and floating IPs share `change_dns_ptr` through [internal/rdns](../../internal/rdns); a server's action is applied by the primary IP that holds the address (`SetDNSPtrs`). The other packages reach the servers through hooks of the servers package, which the cloud wires: `VolumeHooks`, `SetPublicIPs` (primary IPs of a new server), `SetNetworks` (networks of a new server), `SetFirewalls` (the server's `public_net.firewalls` and the firewalls of a new server) and `OnDelete` (primary IPs, floating IPs and firewall entries of a deleted server). Firewalls resolve their label selectors at every read and every agent push, so a server whose labels change gets the matching firewalls with the push that follows `PUT /servers/{id}`. The server record holds its network membership; `SyncAdapters` makes the VM's private adapters, their deny lists and the seed match it. [loadbalancers](../../internal/resources/loadbalancers) is split by feature (create, actions, network, services, targets, status and metrics, reverse DNS, `Contribute` for the agent document); label selector targets resolve when the document is built, and LB-held primary IPs go through `AssignLoadBalancer` of the primary IPs package. [certificates](../../internal/resources/certificates) stores uploaded certificates, issues managed ones through the HLab CA when every domain lies in a zone of the project, and renews them in an hourly job; `UsedBy` (the load balancers) blocks deletion. |
| [internal/actions](../../internal/actions) | Hetzner actions and the worker pool that runs their work; `StartAll` starts several actions of one request without them locking each other. |
| [internal/scheduler](../../internal/scheduler) | Runs the jobs of `Scheduled` modules while the cloud runs, with a clock tests can replace; the servers use it for the status watch (every 5 s, which also ends an unused rescue), the metrics sampling (every 10 s) and the daily backups (checked every hour); the load balancers read health and counters every 10 s, and managed certificates are checked for renewal every hour. |
| [internal/metrics](../../internal/metrics) | Keeps the samples of each server's VM in memory for 24 hours and derives Hetzner's cpu, disk and network series from them. |
| [internal/ca](../../internal/ca) | The HLab CA: an ECDSA P-256 root, created at the first start in `%ProgramData%\hlab-dev\ca` (`ca.pem`, `ca.key`, `ca.crl`), that signs 90-day leaf certificates for managed certificates. Every leaf names the CRL URL `http://api.hetzner.internal/hlab/v1/ca.crl`; `RenewCRL` writes the empty CRL, renewed at every start and weekly by the cloud job `crl`. |
| [internal/shacrypt](../../internal/shacrypt) | SHA-512 crypt of root passwords; only the hash leaves hlabd. |
| [internal/store](../../internal/store) | SQLite state: projects, tokens, resources as JSON documents, actions, ID counters. `Open` returns `IncompatibleError` for a schema version it does not know or a file that is no SQLite database; `Discard` renames the file and its `-wal`/`-shm` to `.discarded-<UTC time>`. |
| [internal/discard](../../internal/discard) | `Requested` decides whether a start discards an incompatible state (only with `discard-state`); `Run` does the discard, also for a changed range: renames the state, deletes the profile's server VMs and the folders `disks`, `volumes`, `vms` and `images\image-*.vhdx`. |
| [internal/logfile](../../internal/logfile) | Writer of `hlabd.log` rotating at 10 MB to `.1`..`.5`, and `Tail`, which reads the last lines across the rotated files and follows new ones; used by hlabd, the management API and `hlab logs`. `Search` (case-insensitive text and level filter, newest first, capped) and `Before` (the lines before a given one) read the files backwards in 64 KiB chunks and keep reading a file that is rotated away under its new name; used by the tray's Logs view. |
| [internal/transit](../../internal/transit) | The addresses of host, appliance, API listener, API proxy and agent, derived from `public_range`. |
| [internal/mgmt](../../internal/mgmt) | Management API on the named pipe: status, projects (deletion as an action with its resources), tokens, catalog update, the CA certificate (`GET /mgmt/v1/ca.pem`) and the trust switch (`PUT /mgmt/v1/trust`, which stores `trust_ca` once the trust stores changed), the log (`GET /mgmt/v1/logs[?follow=1]`, the last 1000 lines as NDJSON; followers end when the cloud stops), and `POST /mgmt/v1/settings/reload`. |
| [internal/catalog](../../internal/catalog) | Loads the shipped catalogs from [catalog/](../../catalog), an updated server type catalog and the own server types; updates the server types from the real Hetzner API. `AddOwn`, `UpdateOwn`, `RemoveOwn` and `ReloadOwn` add, change, remove and re-read own types. |
| [internal/labels](../../internal/labels) | Hetzner's label rules and label selectors. |
| [internal/dnszone](../../internal/dnszone) | Hetzner's DNS data rules without API bookkeeping: zone names (Public Suffix List and the private-use suffixes), RRSets and record values (parsed with miekg/dns), SOA serials, and BIND zone file import and export. |
| [internal/rdns](../../internal/rdns) | Reverse DNS entries of an IPv4 or IPv6 /64 owner: Hetzner's default IPv4 PTR, the rules of `change_dns_ptr`, and the effective PTR for the agent document. |
| [internal/images](../../internal/images) | Downloads, verifies and converts system images into cached base disks; downloads and verifies catalog ISOs unchanged; prepares the rescue base `rescue-v2.vhdx` once through an appliance job. |
| [internal/ipam](../../internal/ipam) | Public IPv4 allocation for servers, primary IPs, floating IPs and load balancers; `Outside` finds addresses outside a changed range, `RecordRange` keeps the range of the last start. |
| [internal/cidata](../../internal/cidata) | Cloud-init seed ISOs for servers and the appliance. A server's vendor-data is multipart MIME, built in [internal/resources/servers](../../internal/resources/servers): a cloud-config part (root login, root password without expiry) and a shell script part that installs the sshd drop-in, the `hlab-guest` timer for password resets, grown volumes and private adapters attached while the server runs, a cloud-init drop-in that renders the seed's network configuration at every boot, and the udev rule and `hlab-volume@.service` that name volume disks `scsi-0HC_Volume_<id>` and mount automount volumes, so user data with its own `runcmd` or `write_files` cannot drop them. |
| [internal/platform](../../internal/platform), [internal/platform/hyperv](../../internal/platform/hyperv) | VMs, disks (copies, differencing child disks, snapshots of running VMs through a Standard checkpoint), adapters (isolated or promiscuous port of the public segment, access port of a private VLAN, trunk; hot add and remove; MAC deny ACLs), DVD drives, boot order, power, the automatic stop action (`SetStopAction`, which Hyper-V refuses on a running VM), listing VMs by name prefix, VM samples and host resources; Hyper-V implementation through one PowerShell process. Every VM gets COM1 on a named pipe that hlabd itself serves and drains before each start, with an ACL for SYSTEM, administrators and the VM worker processes only, because the console carries user data output and a pipe Hyper-V creates would be readable by everyone. |
| [internal/hostnet](../../internal/hostnet), [internal/hostnet/windows](../../internal/hostnet/windows) | Host IP, route, NAT, NRPT, firewall rule, WSL2 forwarding, collision checks, check of the console user's `.wslconfig`. `TrustCA` adds or removes the HLab CA in LocalMachine\Root through the Cert* calls ([trust.go](../../internal/hostnet/windows/trust.go)) and in every WSL2 distribution by running `wsl.exe` with the console user's token ([trust_wsl.go](../../internal/hostnet/windows/trust_wsl.go)); with nobody logged on the WSL part is retried every 30 s. |
| [internal/agentapi](../../internal/agentapi), [internal/agentclient](../../internal/agentclient) | The document hlabd pushes to hlab-agent, and the client that pushes it. |
| [internal/sysint](../../internal/sysint) | Service, paths, pipe and installer of the operating system. |
| [internal/settings](../../internal/settings) | Reads and validates `settings.toml` (including the rules for `public_range`); `Save` rewrites it for the trust switch; `Write` writes the tray's form in place and keeps `trust_ca` from the file. |
| [internal/mgmtclient](../../internal/mgmtclient), [internal/cloudstatus](../../internal/cloudstatus) | Client of the management pipe, used by `hlab` and the tray; reader of the status file `status.json` that reports a refused or failed start. |
| [internal/profile](../../internal/profile) | Names of the `release` and `dev` profiles. |

Other folders:

- [appliance/](../../appliance): boot files of the appliance VM (cloud-init
  user data, systemd unit that installs the current agent at every boot and
  the packages of `appliance-debs.json`: the xfs packages while
  `mkfs.xfs` is missing, HAProxy while `haproxy` is missing, with Debian's
  own HAProxy service disabled).
- [tools/](../../tools): `openapi`, `catalog`, `icons` and `screenshots`,
  run by `task openapi`, `task catalog`, `task icons` and
  `task docs:screenshots`.
- [internal/apitest](../../internal/apitest): test helper that runs API
  handlers in process and validates every response against Hetzner's
  OpenAPI description; `apitest.ServersDeps` wires the servers module to
  a fake platform.
- [internal/platform/platformtest](../../internal/platform/platformtest):
  in-memory fake of the platform for unit tests, with VM states, host
  resources, samples, injected failures and blocked calls.
- [test/e2e](../../test/e2e), [test/docs](../../test/docs): end-to-end and
  documentation tests. [test/traylaunch](../../test/traylaunch) starts and stops the e2e build of
  the tray program for the UI end-to-end tests and the README screenshots
  and reports its CDP port.

To add an API resource, see [Adding a resource](adding-a-resource.md).
