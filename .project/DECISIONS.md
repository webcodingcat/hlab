# DECISIONS

Every decision Claude takes without asking, and every assumption, is recorded
here. Newest entries first.

Format:

## <date> <short title>

- Type: decision | assumption | change | approval
- Phase: idea | spec | phase <id> | change | feedback | upgrade
- What: <one sentence>
- Why: <one sentence>
- Cost of change: low | medium | high

The cost is high when a later change needs a migration or breaks something
others already use: data schema, file and save formats, public URLs and
interfaces, authentication. Claude never takes such decisions alone. They are
taken with the approval of the spec or by an answer from the user.

Keep entries short and their titles telling: phases find the entries
they need by their titles and do not read this file in full. Details belong
in ARCHITECTURE.md or the plan.

## 2026-11-18 Server public_net carries primary IP IDs

- Type: decision
- Phase: phase 02
- What: new servers get `public_net.ipv4.id` and `public_net.ipv6.id` from the `primary_ips` ID sequence (`omitempty`, so older stored servers stay valid).
- Why: hcloud-python reads these IDs and the Ansible `server_info` module failed without them; phase 05 can turn them into real primary IP records.
- Cost of change: low

## 2026-11-18 Project deletion waits for running actions

- Type: decision
- Phase: phase 02
- What: the project-deletion action first waits (at most 20 minutes, woken when an action ends) until the project has no running action, else it ends in error and the project stays listed.
- Why: a server create still running could otherwise leave an orphaned VM after the delete (02-14 review).
- Cost of change: low

## 2026-11-18 Project deletion details

- Type: decision
- Phase: phase 02
- What: a running deletion locks the project (action resource type `project`), so a second delete gets 409 `conflict`; `hlab project delete` polls every 200 ms for at most 30 minutes; a failed agent push after the deletion only logs a warning; a server being deleted is `deleting` and goes back to `off` if the VM cannot be deleted.
- Why: reuses the existing lock check, and the next push rebuilds the whole agent document.
- Cost of change: low

## 2026-11-18 CAT-03 live check leaves the dev catalog updated

- Type: decision
- Phase: phase 02
- What: the CAT-03 e2e live check does not restore the dev profile's `catalog\server-types.json`; after it, the dev cloud serves Hetzner's current server types.
- Why: the non-elevated test user may only read the protected config root, and a restore endpoint or extra write permission would weaken the product for a test.
- Cost of change: low

## 2026-11-18 Catalog update CLI details

- Type: decision
- Phase: phase 02
- What: the hidden prompt uses `golang.org/x/term` `ReadPassword` in cmd/hlab; `UpdateOptions.DataDir` gets the config root; a usage error raised while a command runs (missing token) also exits with 2.
- Why: x/term already sets the console mode through x/sys/windows, and ARCHITECTURE puts the updated catalog in the config root.
- Cost of change: low

## 2026-11-18 Placement group membership stored on the group

- Type: decision
- Phase: phase 02
- What: only the group's `servers` list records membership and a server's `placement_group` is derived when read, so deleting a group clears it; an unknown group answers `invalid_input` on `placement_group`; a server in any group gets `already_in_placement_group`; removing a server from no group succeeds.
- Why: one source of truth, and Hetzner allows one group per server.
- Cost of change: low

## 2026-11-18 Server protection and console details

- Type: decision
- Phase: phase 02
- What: `change_protection` writes the flags in the transaction that starts its action; delete checks protection before the lock (protected and locked answers `protected`); resource action lists accept `id` and `status`; `request_console` answers `unsupported_error` for any server ID.
- Why: the simplest order that the description allows; the console is unsupported regardless of the server.
- Cost of change: low

## 2026-11-18 Catalog lists through ListRequest

- Type: decision
- Phase: phase 02
- What: `/server_types`, `/locations`, `/images` and `/actions` are sorted by ID and paged by `ListRequest`; `/actions` ignores its old `sort`; `name` takes one exact value.
- Why: the clients follow `meta.pagination`, and the frozen description gives these lists only these parameters.
- Cost of change: low

## 2026-11-18 Catalog loading and pricing source

- Type: decision
- Phase: phase 02
- What: an unreadable updated `server-types.json` stops loading with an error (a missing one falls back to the shipped file); `/pricing` takes server type prices from each type's own `prices`; catalog problems show in `/status` as `custom_server_types`.
- Why: updated types are then priced correctly, and a broken file is not silently replaced by older data.
- Cost of change: low

## 2026-11-18 Placement group membership errors

- Type: decision
- Phase: phase 02
- What: adding a server already in the group answers 422 `already_in_placement_group`; a full group answers 422 `placement_error` without `details.fields`; group names are unique per project.
- Why: keeps the member list free of duplicates; the full-group error concerns no single field.
- Cost of change: low

## 2026-11-18 List details: sort without keys, last_page, repeats

- Type: decision
- Phase: phase 02
- What: endpoints without sort keys ignore `sort`; `last_page` is always a number (at least 1); a repeated sort field is ignored; bad `sort` or `label_selector` answers `invalid_input` on that field.
- Why: the parameter does not exist on those endpoints, the description allows a number, and Hetzner ignores contrary orders.
- Cost of change: low

## 2026-11-18 Reading .wslconfig: missing user, unreadable file, defaults

- Type: decision
- Phase: phase 02
- What: no console user token means no WSL problem; an unreadable profile or `.wslconfig` counts as an unknown mode and gives no problem (hostnet has no logger); unknown `networkingMode` counts as NAT and only `dnsTunneling=false` as off.
- Why: WSL falls back to its defaults for unknown values, and `wsl_mode_unsupported` is reserved for bridged and none.
- Cost of change: low

## 2026-11-18 Status of codes outside the frozen error table

- Type: decision
- Phase: phase 02
- What: the status table also lists `token_readonly` 403 and the operation codes `placement_error`, `server_not_stopped`, `already_in_placement_group` 422.
- Why: API-03 requires 403 `token_readonly`, the description answers the operation codes with 422, and an unknown code would answer 500.
- Cost of change: low

## 2026-11-18 Negative selectors match absent keys

- Type: decision
- Phase: phase 02
- What: `k!=v` and `k notin (...)` also match resources without the key (Kubernetes semantics); selector keys and values are checked only for allowed characters, label prefixes must be lowercase RFC 1123 subdomains.
- Why: Hetzner's text does not settle these cases and its selectors follow Kubernetes.
- Cost of change: low

## 2026-11-18 Hyper-V VM group in the data folder DACL

- Type: decision
- Phase: phase 02
- What: the protected data folder DACL grants "NT VIRTUAL MACHINE\Virtual Machines" (S-1-5-83-0) read and execute on folders and read and write on files, inherited.
- Why: hlabd creates disks and ISOs itself, so they get only inherited entries, and the appliance could not open cidata.iso.
- Cost of change: low

## 2026-11-18 Primary IP prices only for ipv4

- Type: decision
- Phase: phase 02
- What: `/pricing` returns the primary IP prices of the shipped catalog, which has only `ipv4`; no `ipv6` entry is invented.
- Why: the catalog is generated from the real Hetzner API, which lists only ipv4 under `primary_ips`.
- Cost of change: low

## 2026-11-18 HTTP status of error codes from the OpenAPI error table

- Type: decision
- Phase: phase 02
- What: `api.Status(code)` is the one table of HTTP statuses, copied from the error table of the frozen OpenAPI description (`invalid_input` 422, `protected` 423), and `api.Error` takes no status.
- Why: the user asked for one sourced table; a read-only call against the real API answered `invalid_input` with 422.
- Cost of change: low

## 2026-11-18 Eleventh server in a spread group gives placement_error

- Type: decision
- Phase: phase 02
- What: adding an eleventh server to a spread placement group answers 422 `placement_error`.
- Why: it is the only related code Hetzner documents; the real one could not be confirmed.
- Cost of change: low

## 2026-11-18 resource.Deleter embedded in Reconciler

- Type: decision
- Phase: phase 02
- What: new interface `resource.Deleter{Delete}`, embedded in `Reconciler`; servers implement only `Deleter` until phase 03 adds `Reconcile`.
- Why: project deletion needs `Delete` now, and a fake `Reconcile` would lie.
- Cost of change: low

## 2026-11-18 WSL mode from the console user's .wslconfig

- Type: decision
- Phase: phase 02
- What: hostnet reads `networkingMode` and `dnsTunneling` from the console user's `.wslconfig` (injectable for tests), maps consomme/virtioproxy to `wsl_virtioproxy` and bridged/none to the new code `wsl_mode_unsupported`; no console user means no WSL problem.
- Why: the user took this answer; a LocalSystem service cannot see the user's distros.
- Cost of change: low

## 2026-11-18 CAT-03 live check as an e2e row

- Type: decision
- Phase: phase 02
- What: besides its unit test, CAT-03 gets `TestE2E/CAT-03` using the hcloud context, skipped with a reason without one.
- Why: SPEC.md "Accounts and secrets" prescribes the live check and success criterion 3 needs it.
- Cost of change: low

## 2026-11-18 Success criteria 1 and 4 tested as CLI-02 and CLI-03 rows

- Type: decision
- Phase: phase 02
- What: the tofu tests for ssh_keys and placement_groups and the Ansible `server_info` test are rows of CLI-02 and CLI-03; both requirements stay open until phase 13.
- Why: the user took this answer at the start of the phase.
- Cost of change: low

## 2026-11-18 PUT changes only the fields present

- Type: decision
- Phase: phase 02
- What: every PUT changes only the fields in the body; `labels`, when present, replaces the whole set.
- Why: the OpenTofu provider sends name and labels in separate PUTs.
- Cost of change: low

## 2026-11-18 Placement group create returns no action

- Type: decision
- Phase: phase 02
- What: `POST /placement_groups` answers `action: null`.
- Why: the description allows it and the clients then have nothing to poll.
- Cost of change: low

## 2026-11-18 Server types behind an accessor

- Type: decision
- Phase: phase 02
- What: `Catalog.ServerTypes` becomes unexported behind `Types()` and `ReplaceServerTypes`.
- Why: `hlab catalog update` swaps the types while requests run.
- Cost of change: low

## 2026-11-18 Hidden token prompt through x/sys/windows

- Type: decision
- Phase: phase 02
- What: the hidden prompt of `hlab catalog update` uses the console mode of `x/sys/windows`, with new `cli.IO.ReadSecret` and `IO.Getenv`.
- Why: no new module.
- Cost of change: low

## 2026-11-18 WSL distro of the e2e harness

- Type: decision
- Phase: phase 02
- What: the harness uses `HLAB_E2E_WSL_DISTRO`, else the single registered distro whose name starts with `Ubuntu`, always through `wsl.exe -d`, and fails with none or several; the test key goes to a temporary directory deleted at the end.
- Why: the user asked for the SPEC.md Ubuntu distro instead of the default distro.
- Cost of change: low

## 2026-11-18 Page beyond the last is empty

- Type: decision
- Phase: phase 02
- What: a page beyond the last returns an empty list with `next_page` null.
- Why: the description does not say, and the clients' paging loops expect it.
- Cost of change: low

## 2026-11-18 Pricing built in the catalog plan

- Type: decision
- Phase: phase 02
- What: `/pricing` is built in plan 02-03 from the merged server types.
- Why: it derives from them and needs only one registration line.
- Cost of change: low

## 2026-11-18 Project deletion waits through a management action endpoint

- Type: decision
- Phase: phase 02
- What: `GET /mgmt/v1/actions/{id}` is added and `hlab project delete` waits for it; a second deletion while one runs answers 409 `conflict`.
- Why: the user took this answer; the CLI must report success or failure.
- Cost of change: medium

## 2026-11-18 Q1: real resources reconciled by their module

- Type: change
- Phase: change
- What: modules owning real resources implement `resource.Reconciler` with platform, image store, agent push and settings in their `Deps`, replacing `servers.Deps.Machines` and `internal/cloud/servers.go` as the first plan of phase 03 (decisions/resource-reconciler.md).
- Why: a new resource type stays one package plus one line before phases 03, 04, 05 and 08 add more such modules, and phase 09 gets one way to restore every resource.
- Cost of change: medium

## 2026-11-18 Q2: guest-visible identifiers approved

- Type: approval
- Phase: change
- What: the user approves server MAC, formal IPv6, root login via `disable_root: false` and the volume identifier byte order exactly as recorded in phase 01 (decisions/guest-identifiers.md).
- Why: the values follow the spec's identifiers and the documentation range, and the MAC wrap-around after 2^20 servers is out of reach for a local lab.
- Cost of change: high

## 2026-11-18 Server MACs, IPv6 and root login

- Type: decision
- Phase: 01 (01-16)
- What: a server's public MAC is `02:48:4c` followed by `0x1` and the low 20 bits of its ID (the appliance has `02:48:4c:00:00:01`), kept in `_hlab`; its formal IPv6 is `2001:db8:<ID as 32 bits>::/64`; server vendor-data sets `disable_root: false` so key logins as root work like at Hetzner.
- Why: unique MACs without colliding with the appliance; Ubuntu cloud images block root logins by default.
- Cost of change: high

## 2026-11-18 Store timestamps and access scoping

- Type: decision
- Phase: 01 (01-02)
- What: timestamps are stored as RFC3339Nano text in UTC; resource access functions take the project ID (0 = catalog, `project_id IS ?`), so one project cannot address another's rows.
- Why: sortable text that SQLite and Go both read; isolation enforced in the one place every module uses.
- Cost of change: medium

## 2026-11-18 Volume identifier byte order

- Type: decision
- Phase: 01 (01-10)
- What: `CreateDisk` writes `page83` in GUID text byte order, so `484C4142-0000-4000-8000-<id>` reaches the disk with the volume ID in the last 6 bytes.
- Why: those bytes survive in both designators the guest sees (E6).
- Cost of change: high

## 2026-11-18 Management API details in phase 01

- Type: decision
- Phase: 01 (01-14)
- What: token names are 1-63 characters; `/status` carries state, version and problems until later phases fill `shadowing_zones` and `appliance_generation`; deleting a project with resources answers 409 until PROJ-04 (phase 02).
- Why: ARCHITECTURE.md sets no rule for token names; the other fields have no data yet.
- Cost of change: low

## 2026-11-18 Agent document without JSON tags

- Type: decision
- Phase: 01 (01-01)
- What: `agentapi` types carry no JSON tags and use Go's field names on the wire, with `const Schema = 1`; `SegmentFor` returns the public PVLAN for network 0 and leaves private VLANs to its caller until phase 05.
- Why: hlabd and hlab-agent are built from the same package and the agent is replaced at every cloud start, so the two sides never differ in version.
- Cost of change: low

## 2026-11-18 Release and dev profile side by side

- Type: change
- Phase: 01 (discuss)
- What: Binaries carry a link-time profile `release` or `dev` that sets service, pipe, data folder, catalog folder, switch, VM prefix and host-object tags (`hlab` / `hlab-dev`); both may be installed, only one cloud runs (`other_instance_running`, LIFE-10); the e2e harness refuses to run while the release cloud runs.
- Why: the user develops and uses hlab on the same host; a separate running dev cloud would need a second range, API name and a shared WinNAT, which Windows limits to one.
- Cost of change: high

## 2026-11-18 Public IPv4 allocation continues after the last address

- Type: decision
- Phase: 01 (discuss)
- What: Public IPv4s are handed out after the last one given (counter in `kv`), wrapping to `.4` only at the end of the range and skipping used ones.
- Why: reusing a freed address at once causes SSH host key conflicts when servers are deleted and recreated (user's choice).
- Cost of change: low

## 2026-11-18 Appliance DNS forwards to the host's DNS servers

- Type: decision
- Phase: 01 (discuss)
- What: hlabd collects the host's DNS servers at cloud start and passes them as `Upstream` in the agent document; without any, the agent forwards to 1.1.1.1 and 9.9.9.9.
- Why: Windows has no resolver on port 53 the appliance could forward to; the host's servers keep LAN and VPN names working (user's choice).
- Cost of change: low

## 2026-11-18 Firewall rule for the API port

- Type: decision
- Phase: 01 (discuss)
- What: hostnet adds one inbound Windows Firewall rule (group per profile): TCP 8780, all firewall profiles, interface `vEthernet (hlab)`, local 198.19.0.2, remote 198.19.0.1, program `hlabd.exe`; `Up` replaces an existing rule of the group, `Down` and the uninstaller remove it; hlabd listens on 198.19.0.2:8780 only.
- Why: the appliance's API proxy must reach hlabd, and the host must expose nothing else.
- Cost of change: low

## 2026-11-18 Host settings restored after a crash

- Type: decision
- Phase: 01 (discuss)
- What: hostnet records previous values of settings it does not own (IPv4 forwarding on the WSL and hlab adapters) in `hostnet-restore.json` before changing them, and restores them at the next `Up`, `Down` or uninstall-cleanup.
- Why: a crash of hlabd must not leave the WSL adapter forwarding (user's wish).
- Cost of change: low

## 2026-11-18 Unit tests fail without the cached OpenAPI description

- Type: decision
- Phase: 01 (discuss)
- What: when the frozen OpenAPI description is missing from the cache, the API tests fail with "run `task openapi`" instead of skipping the validation.
- Why: a silently skipped API-10 check would pass without proving anything (user's choice).
- Cost of change: low

## 2026-11-18 Hetzner token: hcloud context in development, prompt in operation

- Type: change
- Phase: change
- What: Development and release reach the real API through a named hcloud context (default name in "Default hcloud context name HLab", `HLAB_HCLOUD_CONTEXT`; `task catalog` via `hcloud api`, the CAT-03 live check gets the token through hcloud and skips with a reason without a valid context); in operation `hlab catalog update` and the tray ask for the token (`--hetzner-token-env`, hidden prompt, password field), use it once and never store it; `HLAB_HETZNER_TOKEN` and `.env.example` are gone.
- Why: the user's hcloud login already holds the token for development, while the product must not depend on hcloud and must not keep a real Hetzner secret.
- Cost of change: low

## 2026-11-18 Default hcloud context name HLab, token prompt without hcloud

- Type: change
- Phase: change
- What: The hcloud context for the real API is named `HLab` unless `HLAB_HCLOUD_CONTEXT` says otherwise; without hcloud or a usable context `task catalog` asks for the token with a hidden prompt, and the unattended live check is skipped.
- Why: `HLab` is the user's existing context; development must not be tied to hcloud.
- Cost of change: low

## 2026-11-18 Module path and repository in SPEC.md

- Type: change
- Phase: change
- What: SPEC.md "Stack" names the Go module path `github.com/webcodingcat/hlab` and the repository `https://github.com/webcodingcat/hlab`, confirmed by the user.
- Why: the module path was only recorded in DECISIONS.md.
- Cost of change: low

## 2026-11-18 Upgrade to wcc 0.31.2

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.31.0 to 0.31.2: project CLAUDE.md gains the rule to work from this repository only and the rule that tool login credentials stay with their tool.
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-18 Go module path confirmed

- Type: approval
- Phase: upgrade
- What: The user confirmed the Go module path `github.com/webcodingcat/hlab`, which /wcc:prepare had taken from a sibling folder before the rule "work from this repository only" existed.
- Why: user's choice.
- Cost of change: low

## 2026-11-18 Go module path and repository layout

- Type: decision
- Phase: prepare
- What: Go module `github.com/webcodingcat/hlab` (confirmed by the user, see "Go module path confirmed"); programs in `cmd/<name>`, tray frontend in `ui/frontend` (Vite, svelte-check as its linter, Playwright as dev dependency), tasks `build`, `lint`, `test`, `e2e` in `Taskfile.yml`.
- Why: SPEC.md names no module path; this matches the owner's GitHub account.
- Cost of change: low

## 2026-11-18 Frontend audit finding accepted

- Type: assumption
- Phase: prepare
- What: `npm audit` reports a ReDoS advisory in picomatch (via vite, svelte-check); it is accepted because it affects only the local build tools and the fix would downgrade vite below SPEC.md.
- Why: no untrusted glob patterns reach the build.
- Cost of change: low

## 2026-11-18 Spec approved

- Type: approval
- Phase: spec
- What: The user approved REQUIREMENTS, SPEC, ARCHITECTURE, UI-SPEC and ROADMAP with all decisions of cost high listed in the summary, the test levels and the design changes from E1 (private VLAN) and E9 (CDP port in the e2e build).
- Why: approval step of /wcc:spec.
- Cost of change: low

## 2026-11-18 Guest-visible identifiers

- Type: decision
- Phase: spec
- What: Volume disks carry the identifier `484C4142-0000-4000-8000-<volume ID, 12 hex digits>`; the rescue disk sits at SCSI LUN 63, server disk at LUN 0, volumes from LUN 1, DVDs on SCSI controller 1; public PVLAN 4094/4093, private-network VLANs 2..4092; metadata paths `/hlab/v1/password-reset`; MACs `02:48:4c:...`.
- Why: guests and their scripts see these values; changing them later breaks existing servers' udev rules and hooks (E6, E7).
- Cost of change: high
## 2026-11-18 Tray UI tests through a CDP port in an e2e-only build

- Type: approval
- Phase: spec
- What: The user decided that the tray UI is tested in the real tray window: a build with tag `e2e` sets Wails' `AdditionalBrowserArgs` to `--remote-debugging-port=<port>` (bound to 127.0.0.1) and a per-run `WebviewUserDataPath`, both taken from test-harness environment variables; Playwright Test connects over CDP. Release builds never use the tag; `task release` fails if the tag is set, and a unit test checks that the default build has no remote-debugging argument.
- Why: Wails v3.1.0 clears `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS` (E9); the user preferred testing the real window over a Wails server build. This is a user-approved exception to the project rule against debug ports, limited to the `e2e` build.
- Cost of change: low

## 2026-11-18 Service name hlab

- Type: approval
- Phase: spec
- What: The Windows service keeps the name `hlab`; the user removes the old prototype service of the same name on the development machine.
- Why: user's choice.
- Cost of change: high
## 2026-11-18 Port isolation by private VLAN

- Type: change
- Phase: spec
- What: The public segment is isolated with a private VLAN (appliance promiscuous, host adapter and guest adapters isolated, VLANs 4094/4093) instead of MAC port ACLs; private-network adapters get MAC deny-lists of the other members.
- Why: allow-appliance-deny-ANY MAC ACLs dropped legitimate traffic intermittently (E1); PVLAN kept all legitimate paths and blocked direct ones, including host and WSL2 traffic (E1, E3).
- Cost of change: medium

## 2026-11-18 VM settings and Windows shutdown

- Type: change
- Phase: spec
- What: VMs get `AutomaticStartAction Nothing` and `AutomaticStopAction` matching `stop_behaviour`; shutdown and reboot use the Shutdown integration service; disks copied from a base get a new disk identifier.
- Why: vmms runs before hlabd at Windows shutdown, the default start action would start VMs with Windows (LIFE-06), Hyper-V has no ACPI button, and copies inherit the parent's identifier (E7, E11).
- Cost of change: low
## 2026-11-18 Stack

- Type: decision
- Phase: spec
- What: Go 1.27.4; Wails v3.1.0 with Svelte 5, Vite 8, TypeScript 5.9; SQLite via modernc.org/sqlite; Hyper-V through a persistent Windows PowerShell 5.1 process; appliance Debian 13 with nftables, HAProxy 3.0 and a Go agent (miekg/dns); WiX v7 MSI plus Burn bundle; Task; Go testing and Playwright Test; versions in SPEC.md.
- Why: Go and Wails are fixed by IDEA.md. Svelte needs the least frontend code for a Go maintainer (React rejected: more boilerplate). PowerShell is what minikube and Packer use for Hyper-V; WMI via github.com/microsoft/wmi rejected: no releases, Msvm method calls need embedded CIM. modernc SQLite needs no cgo; bbolt rejected: no queries, hand-written indexes. CoreDNS rejected: a second daemon to configure, while the agent already holds the zone data. WiX is the maintained free MSI/Burn toolset; Inno Setup rejected: no MSI. TypeScript 7 postponed until svelte-check supports it. Sources: go.dev/dl, github.com/wailsapp/wails/releases (v3.0.0 stable 2026-10-26), minikube Hyper-V driver docs, Packer Hyper-V driver source, proxy.golang.org, docs.firegiant.com/wix/osmf, registry.npmjs.org.
- Cost of change: high

## 2026-11-18 Data schema: SQLite with JSON documents per resource

- Type: decision
- Phase: spec
- What: State in `%ProgramData%\hlab\state.db` with tables projects, tokens (SHA-256 only), resources (kind, id, project, name, labels, doc JSON in Hetzner's shape plus `_hlab`), actions, ids, kv; schema version in `PRAGMA user_version` (ARCHITECTURE.md "Data schema").
- Why: one generic table keeps every resource module alike and makes new fields no migration; IDs global per kind like Hetzner.
- Cost of change: high

## 2026-11-18 Files and formats

- Type: decision
- Phase: spec
- What: settings in `%ProgramData%\hlab\settings.toml` (TOML, keys in ARCHITECTURE.md); catalogs as JSON in Hetzner's API shape; own server types in `server-types.custom.json` with IDs from 100000; logs as JSON lines with rotation.
- Why: TOML allows comments for a file users edit; Hetzner's own JSON shape lets `hlab catalog update` store the API response unchanged.
- Cost of change: high

## 2026-11-18 Network addressing

- Type: decision
- Phase: spec
- What: Default public range `198.19.0.0/16` (inside the benchmark range 198.18.0.0/15, not the commonly used 198.18.0.0/16 of proxy fake-IP tools, not CGNAT used by Tailscale); transit /30 at its start (appliance .1, host .2); servers from .4; guests see gateway 172.31.1.1 and resolvers 185.12.64.1/.2 like at Hetzner; private networks as VLAN plus VRF on the appliance so ranges of different projects may overlap.
- Why: a range that home LANs, VPNs and Docker/WSL (172.16/12) do not use; addresses inside guests match Hetzner, so scripts run unchanged.
- Cost of change: high

## 2026-11-18 API reached through the appliance

- Type: decision
- Phase: spec
- What: `api.hetzner.internal` resolves to the appliance (198.19.0.1); the agent proxies port 80 to hlabd on 198.19.0.2:8780.
- Why: hlabd never binds port 80 on the host, where http.sys (IIS and others) claims it on all addresses by default; servers, host and WSL2 use the same path.
- Cost of change: high

## 2026-11-18 Start and stop for Interactive Users instead of a group

- Type: decision
- Phase: spec
- What: The service security descriptor, the management pipe and the settings file grant Interactive Users (`IU`) query/start/stop, connect and modify; no `hlab-users` group.
- Why: group membership needs a sign-out after installation, and security for multi-user use is a non-goal; a well-known SID needs nothing created or removed.
- Cost of change: medium

## 2026-11-18 Management interface

- Type: decision
- Phase: spec
- What: `hlab` command set and the management API over `\\.\pipe\hlab` as in ARCHITECTURE.md; the tray uses the same API, Hetzner resources through `/mgmt/v1/projects/{name}/api/v1/...`.
- Why: one interface for CLI, tray and scripts; the tray tests the same handlers as the clients.
- Cost of change: high

## 2026-11-18 Appliance as declarative network document

- Type: decision
- Phase: spec
- What: hlabd pushes the complete network configuration as one versioned JSON document (`agentapi.Config`, schema 1) to hlab-agent, which applies it idempotently; firewalls in nftables, one HAProxy per load balancer in its own namespace, DNS and metadata in the agent.
- Why: answers "where LBs, firewalls and floating IPs live" with one place that sees all traffic; a full document makes restarts and stop/start trivial to reconcile.
- Cost of change: high

## 2026-11-18 Guests configured by a NoCloud seed ISO

- Type: decision
- Phase: spec
- What: Each server gets a cidata ISO (meta-data, user-data, vendor-data, network-config v2 with /32 and on-link gateway); vendor-data installs a udev rule for `scsi-0HC_Volume_<id>` and a timer that applies root password resets from the metadata service.
- Why: cloud-init's Hetzner datasource only runs when DMI says "Hetzner", which Hyper-V cannot fake; Hetzner's password reset needs a guest agent, which official images lack on Hyper-V.
- Cost of change: medium

## 2026-11-18 Rescue system from a separate rescue disk

- Type: decision
- Phase: spec
- What: Rescue boots a differencing disk of a prepared Debian 13 rescue base (new UUIDs and labels) at SCSI LUN 63; the server disk stays at LUN 0 (`/dev/sda`); the rescue system runs from its own disk, not from RAM.
- Why: live ISOs (GRML, Debian live) cannot take SSH keys without remastering; a cloud image takes keys and password through cidata. Running from RAM is not observable for scripts, the disk name is.
- Cost of change: low

## 2026-11-18 Datacenters follow Hetzner's current API

- Type: decision
- Phase: spec
- What: hlab serves datacenter fields and endpoints exactly as the frozen OpenAPI description does; `/datacenters` answers 410 like Hetzner since 2026-10-01.
- Why: IDEA.md lists datacenters, but Hetzner removed the endpoint; goal 1 (unmodified clients) is served by matching the real API.
- Cost of change: low

## 2026-11-18 OpenAPI copy not committed

- Type: decision
- Phase: spec
- What: The frozen Hetzner OpenAPI description is downloaded by `task openapi` from docs.hetzner.cloud, checked against the SHA-256 in `api/openapi.lock` and cached outside the repository; changing the pin is a deliberate commit.
- Why: the file carries no licence, so committing it to an Apache-2.0 repository is unclear; the pin keeps it frozen.
- Cost of change: low

## 2026-11-18 request_console and vswitch subnets

- Type: decision
- Phase: spec
- What: request_console returns `unsupported_error` ("server console is not supported by hlab yet"); vswitch subnets return `invalid_input` on field `type`.
- Why: neither may pretend to work (decisions/server-console.md); vSwitches couple to dedicated servers that hlab has no equivalent for.
- Cost of change: low

## 2026-11-18 Phases run without elevated rights

- Type: decision
- Phase: spec
- What: A one-time elevated `scripts/dev-setup.ps1` registers the dev service with the installer's descriptor; phases then build, start and test as a normal user; only the installer suite needs an elevated terminal.
- Why: a Claude Code run with elevated rights gives every agent those rights.
- Cost of change: low
## 2026-11-18 IPv6 assigned formally, not routed

- Type: assumption
- Phase: idea
- What: The API assigns IPv6 addresses to servers and primary IPs as clients expect, but they carry no traffic; listed as a known deviation.
- Why: v1 is IPv4 only, while clients create servers with IPv6 enabled by default.
- Cost of change: medium

## 2026-11-18 Public IPv4 range configurable

- Type: assumption
- Phase: idea
- What: Public IPv4 addresses come from a configurable range whose default avoids common networks (VPNs, Tailscale, home LANs); hlab checks for collisions at start.
- Why: a fixed range could collide with networks on the user's machine.
- Cost of change: medium

## 2026-11-18 Stop behaviour is a setting

- Type: assumption
- Phase: idea
- What: A setting chooses whether stopping the cloud saves VM state or shuts servers down cleanly (default: save state); a Windows shutdown with a running cloud follows the same setting and the cloud is stopped after reboot.
- Why: the user wants to choose per installation, not at every stop.
- Cost of change: low

## 2026-11-18 Automatic daily backups

- Type: assumption
- Phase: idea
- What: Servers with backups enabled get a backup daily while the cloud runs, keeping 7 like Hetzner, plus manual backups.
- Why: matches Hetzner's behaviour.
- Cost of change: low

## 2026-11-18 Rescue system is a real Linux

- Type: assumption
- Phase: idea
- What: Enabling rescue boots a Linux rescue system with root password and SSH keys.
- Why: "everything really works".
- Cost of change: low

## 2026-11-18 ISO catalog from public sources

- Type: assumption
- Phase: idea
- What: ISOs are a catalog of publicly downloadable ISOs (e.g. Ubuntu and Debian installers), downloaded on demand; Hetzner's private ISOs are not available.
- Why: Hetzner's ISO files cannot be downloaded.
- Cost of change: low

## 2026-11-18 Server type catalog update needs optional real token

- Type: assumption
- Phase: idea
- What: Every release ships the server type catalog; updating it from the real Hetzner API optionally uses a real Hetzner token provided by the user.
- Why: the real API needs authentication; hlab itself needs no Hetzner account.
- Cost of change: low

## 2026-11-18 Resource limits: RAM and disk hard, vCPUs overcommitted

- Type: assumption
- Phase: idea
- What: RAM and disk are hard limits including the Windows reserve; total vCPUs may exceed host cores, but a single VM gets at most the host's logical cores, otherwise resource_unavailable; disks are thin-provisioned in a configurable location.
- Why: RAM is fixed per VM, CPU is time-shared like Hetzner's shared vCPUs.
- Cost of change: low

## 2026-11-18 Tokens read and read/write, projects isolated

- Type: assumption
- Phase: idea
- What: Tokens have read or read/write permission like Hetzner; several projects run at the same time, isolated from each other.
- Why: matches Hetzner's behaviour.
- Cost of change: medium

## 2026-11-18 UI structure like Hetzner Console, own design

- Type: assumption
- Phase: idea
- What: The tray window follows the structure and workflows of the Hetzner Cloud Console with its own visual design, without Hetzner logos or branding.
- Why: familiar workflows without copying a third party's brand.
- Cost of change: low

## 2026-11-18 Reverse DNS resolves, metadata at 169.254.169.254

- Type: assumption
- Phase: idea
- What: Reverse DNS entries really resolve (PTR) via the appliance; the metadata service is reachable at 169.254.169.254 like at Hetzner.
- Why: "everything really works".
- Cost of change: low

## 2026-11-18 Clients installed by the user, no auto-update, no telemetry

- Type: assumption
- Phase: idea
- What: The user installs hcloud, OpenTofu and Ansible (in WSL2) themselves; updates come via a new installer without automatic update check; hlab sends no telemetry.
- Why: keeps v1 small; hlab must not depend on outside services.
- Cost of change: low

## 2026-11-18 Upgrade to wcc 0.32.2

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.0 to 0.32.2: CLAUDE.md test rule on shared behaviour takes the template wording; version line updated
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-18 Q3 SRV-18 counts live RAM and remaining disk growth

- Type: decision
- Phase: 03
- What: A server fits when its type RAM is at most the host's free physical RAM minus `reserve_ram_mb`, and the remaining growth of all thin disks plus the new disk is at most the free data-folder space minus `reserve_disk_gb`.
- Why: RAM matches success criterion 4 literally, and disk stays a hard limit although disks are thin.
- Cost of change: low

## 2026-11-18 reset_password on a stopped server answers 409 conflict

- Type: decision
- Phase: 03
- What: `reset_password` on a server that is not running answers 409 `conflict`.
- Why: the frozen description lists no code for this case.
- Cost of change: low

## 2026-11-18 platform.Sample only gains counters

- Type: decision
- Phase: 03
- What: `platform.Sample` gains disk-op and packet counters; `CPUPercent` stays, computed in hyperv from two reads, and `disk.0` is the sum of all disks of a VM.
- Why: extends the interface without removing anything.
- Cost of change: low

## 2026-11-18 Minimal scheduler for status watch and metrics

- Type: decision
- Phase: 03
- What: a minimal `internal/scheduler` (Clock, Job, Run) and an optional `resource.Scheduled` interface run the status watch (5 s) and metrics sampling (10 s).
- Why: the scheduler is already in ARCHITECTURE.md and phase 04 extends it for backups.
- Cost of change: low

## 2026-11-18 Fake platform replaces NoopMachines in tests

- Type: decision
- Phase: 03
- What: `internal/platform/platformtest` and `apitest.ServersDeps` replace NoopMachines/BlockingMachines; five test files change only their constructor.
- Why: `servers.Machines` goes away with the Reconciler refactor.
- Cost of change: low

## 2026-11-18 Root password without expiry, root SSH login by drop-in

- Type: decision
- Phase: 03
- What: the root password is set with `expire: false`, and a `00-hlab-root.conf` sshd drop-in with `PermitRootLogin yes` plus `ssh_pwauth: true` allows root password login; documented as a deviation.
- Why: SRV-02's SSH login must work without a forced change; cloud-init alone does not allow root login.
- Cost of change: low

## 2026-11-18 Guest password hook in python3

- Type: decision
- Phase: 03
- What: the guest password hook is a python3 stdlib script run by a 5 s systemd timer.
- Why: cloud-init guarantees python3, curl is not certain on the Debian images.
- Cost of change: low

## 2026-11-18 e2e password login through SSH_ASKPASS

- Type: decision
- Phase: 03
- What: the e2e harness logs in with a password through OpenSSH `SSH_ASKPASS_REQUIRE=force` and a temporary askpass script; if that fails on Windows OpenSSH, plan 03-10 stops instead of adding `x/crypto/ssh`.
- Why: no new Go module for a test-only need.
- Cost of change: low

## 2026-11-18 Metadata service binds IP and MAC on its input path

- Type: decision
- Phase: 03
- What: the agent's metadata service on 169.254.169.254:80 applies the IP/MAC binding to its input path, proven by the e2e row META-01/spoof.
- Why: the metadata carries user_data and password hashes; a spoofing guest must not read them.
- Cost of change: low

## 2026-11-18 Rebuild from system images only in phase 03

- Type: decision
- Phase: 03
- What: rebuild accepts only system images; snapshot and backup images follow in phase 04.
- Why: images other than system images arrive in phase 04.
- Cost of change: low

## 2026-11-18 Availability zone in metadata

- Type: decision
- Phase: 03
- What: the metadata availability zone is `<location>-dc<n>` from the catalog.
- Why: matches Hetzner's datacenter naming.
- Cost of change: low

## 2026-11-18 Vendor-data as multipart MIME

- Type: decision
- Phase: 03
- What: vendor-data is multipart MIME: a minimal cloud-config part and a shell-script part that writes and enables the guest hooks and the root sshd drop-in.
- Why: user cloud-config lists (`runcmd`, `write_files`) replace vendor lists and would silently drop the hooks.
- Cost of change: low

## 2026-11-18 COM1 pipe served by hlabd with a protected ACL

- Type: decision
- Phase: 03
- What: every VM gets COM1 on a named pipe that hlabd creates before each start with a SYSTEM/Administrators/VMs-only SD and drains; starts fail if the pipe cannot be served.
- Why: Debian 12 panics with `console=ttyS0` when no serial port exists, and a pipe Hyper-V creates is readable by Everyone (console carries user_data output).
- Cost of change: low

## 2026-11-18 Vendor script syncs before first boot ends

- Type: decision
- Phase: 03
- What: the vendor shell part ends with `sync`; poweroff stays a hard cut.
- Why: a hard poweroff within about 30 s of the first boot left zero-length SSH host keys and hook files, so sshd never came back.
- Cost of change: low

## 2026-11-18 Upgrade to wcc 0.32.3

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.2 to 0.32.3: version line updated; CLAUDE.md rules and planning documents already matched the templates
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-18 Q4 Automount on later attach via a metadata path

- Type: decision
- Phase: 04
- What: the agent document and metadata serve a per-server list of automount volumes at `GET /hlab/v1/volumes`; a udev-triggered unit reads it and mounts at `/mnt/HC_Volume_<id>` with an fstab entry.
- Why: a volume attached after server creation with `automount: true` must be mounted like at Hetzner, and only then.
- Cost of change: medium (guest-visible path baked into vendor-data of existing servers)

## 2026-11-18 Q5 SRV-15 proved by SSH into the live ISO

- Type: decision
- Phase: 04
- What: the SRV-15 e2e test boots an Ubuntu live-server ISO that reads the cidata in DVD slot 0 and checks over SSH that `/` is the live system; the indirect proof (installed system silent, DVD first) is the fallback if research refutes it.
- Why: a direct login shows the boot source; the indirect check cannot tell a hung boot from an ISO boot.
- Cost of change: low

## 2026-11-18 xfsprogs from cached .debs on the appliance seed

- Type: decision
- Phase: 04
- What: `catalog/appliance-debs.json` lists xfsprogs, libicu76, libinih1 (pool URL, snapshot.debian.org URL, sha256); hlabd caches them in `<data>\cache\debs\`, puts them on the appliance cidata, and the script runs `dpkg -i` only when `mkfs.xfs` is missing.
- Why: The user ruled out installing at every boot; building into the image needs a Linux builder.
- Cost of change: low

## 2026-11-18 Rescue counts as booted when hlab starts the server

- Type: decision
- Phase: 04
- What: a poweron, reset or reboot hlab drives while rescue is enabled ends rescue (`rescue_enabled` false, LUN 0 first, own seed back); a guest-initiated reboot is ended only by the 60-minute timeout (deviation).
- Why: Hyper-V gives no signal for a boot the guest starts itself.
- Cost of change: low

## 2026-11-18 Rescue seed reuses the server's instance-id and network-config

- Type: decision
- Phase: 04
- What: the rescue seed carries the server's instance-id and network-config, the rescue keys and password hash, and no user_data.
- Why: The installed system sees no new instance if it boots before the seed is restored, and rescue never runs user_data.
- Cost of change: medium (guest-visible)

## 2026-11-18 Rescue disk is a differencing disk via Platform.CreateChildDisk

- Type: decision
- Phase: 04
- What: `Platform` gains `CreateChildDisk`; the rescue disk is a differencing disk at LUN 63.
- Why: ARCHITECTURE names a differencing disk; adding a method extends the interface.
- Cost of change: low

## 2026-11-18 Checkpoint type Standard on every VM

- Type: decision
- Phase: 04
- What: VMs get CheckpointType Standard; a running server's snapshot takes a Standard checkpoint, copies the parent, removes the checkpoint and waits for the merge; leftover hlab checkpoints are removed first.
- Why: The guest images have no VSS daemon, so Production falls back to Standard anyway.
- Cost of change: low

## 2026-11-18 Error codes the frozen description leaves open

- Type: decision
- Phase: 04
- What: a volume attached elsewhere gives 422 `volume_already_attached`; another location, a backup without backups enabled, an image larger than the type's disk, deleting an attached volume and a 17th volume give 422 `invalid_input`.
- Why: The description names no code for these; hcloud-go knows `volume_already_attached`.
- Cost of change: low

## 2026-11-18 Appliance jobs run one at a time at LUN 1

- Type: decision
- Phase: 04
- What: appliance jobs are serialized and the job disk always sits at the appliance's LUN 1.
- Why: One job at a time needs no LUN bookkeeping.
- Cost of change: low

## 2026-11-18 Volume LUNs 1 to 16

- Type: decision
- Phase: 04
- What: a volume gets the lowest free LUN from 1 to 16; LUN 63 stays reserved for rescue.
- Why: Hetzner allows 16 volumes per server.
- Cost of change: low

## 2026-11-18 backup_window fixed and daily backups as create_image

- Type: decision
- Phase: 04
- What: `backup_window` is "22-02" while backups are enabled, null otherwise; a daily backup is a `create_image` action and a locked server waits for the next hourly run.
- Why: Hetzner's field shape, and locking without a second mechanism.
- Cost of change: low

## 2026-11-18 Images module keeps its old constructor

- Type: decision
- Phase: 04
- What: `imagesres.New(cat)` stays; `NewModule(Deps)` serves catalog plus project images.
- Why: Existing conventions and OpenAPI tests stay unchanged.
- Cost of change: low

## 2026-11-18 Volume disks join the disk rule through the cloud

- Type: decision
- Phase: 04
- What: `servers.Deps.ExtraDisks` gets volume disks from `volumes.Disks` (wired in internal/cloud); volumes call the servers module's `CheckDisk`.
- Why: servers cannot import volumes; wiring stays in the cloud.
- Cost of change: low

## 2026-11-18 VOL-04 tested without a guest rescan

- Type: decision
- Phase: 04
- What: the e2e test waits for the new size without writing to the rescan file.
- Why: The default images have the storvsc fix, so the test proves the promise unaided.
- Cost of change: low

## 2026-11-18 SRV-15 uses the ubuntu-22.04 live-server ISO

- Type: decision
- Phase: 04
- What: the test boots that ISO; the indirect proof in the row is the fallback.
- Why: Smallest Ubuntu ISO; SSH into the live session is unconfirmed.
- Cost of change: low
