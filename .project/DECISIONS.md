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
- Why: The test proves the promise unaided; the product's hlab-guest timer does the rescan (see "Grown volumes picked up by the guest timer"), because an idle disk learns of a new size only on its next SCSI command.
- Cost of change: low

## 2026-11-18 SRV-15 uses the ubuntu-22.04 live-server ISO

- Type: decision
- Phase: 04
- What: the test boots that ISO; the indirect proof in the row is the fallback.
- Why: Smallest Ubuntu ISO; SSH into the live session is unconfirmed.
- Cost of change: low

## 2026-11-18 Snapshot merge deadline 10 minutes

- Type: decision
- Phase: 04 (04-01)
- What: Snapshot waits at most 10 minutes (`mergeTimeout`) for the checkpoint merge, polling every second.
- Why: No figure is documented; a short snapshot merges quickly.
- Cost of change: low

## 2026-11-18 Only hlab checkpoints are cleaned up

- Type: decision
- Phase: 04 (04-01)
- What: At the start of Snapshot only checkpoints named `hlab-snapshot-*` are removed, in every VM state; the merge wait runs only if one was removed.
- Why: A leftover checkpoint makes a direct copy miss data; foreign checkpoints are not touched.
- Cost of change: low

## 2026-11-18 Direct copy only for Off and Saved

- Type: decision
- Phase: 04 (04-01)
- What: Off and Saved (with Critical and FastSaved variants) are copied directly; every other state, Paused included, goes through a checkpoint.
- Why: The plan names off and saved as the direct cases.
- Cost of change: low

## 2026-11-18 Agent job responses

- Type: decision
- Phase: 04 (04-02)
- What: A malformed body, unknown kind, unsupported FS or negative LUN answer 400; a failing job answers 200 with `{ok:false, message}`; `Client.Job` turns both into an error.
- Why: Input errors stay apart from job failures.
- Cost of change: low

## 2026-11-18 Agent jobs bounded only by the caller's context

- Type: decision
- Phase: 04 (04-02)
- What: `Client.Job` uses its own http.Client without timeout.
- Why: mkfs of a large volume can exceed the 30 s of config and status requests.
- Cost of change: low

## 2026-11-18 Agent finds job disks by LUN in sysfs

- Type: decision
- Phase: 04 (04-02)
- What: `findDisk` matches L of H:C:T:L with SCSI type 0, ignores the host number, fails on two matches and waits up to 30 s for a hot-added disk.
- Why: E7: the host number is not stable; hot-added disks appear late.
- Cost of change: low

## 2026-11-18 Fixed labels for the rescue base

- Type: decision
- Phase: 04 (04-02)
- What: Labels present in the image become "hlab-rescue" (ext4) and "HLABRESCUE" (vfat); the ESP gets a new random volume ID; absent labels stay absent.
- Why: They differ from every image label; all rescue disks come from the one prepared base.
- Cost of change: low

## 2026-11-18 Only label references are rewritten

- Type: decision
- Phase: 04 (04-02)
- What: `LABEL=<old>` and `--label <old>` are rewritten, not the bare label text.
- Why: A short bare label would match unrelated text in grub.cfg.
- Cost of change: low

## 2026-11-18 ISO IDs 130001-130005 named by file name

- Type: decision
- Phase: 04 (04-04)
- What: Catalog ISOs get IDs 130001 to 130005; `name` is the ISO file name, also the cache file name.
- Why: Names follow the file names; the description defines no IDs.
- Cost of change: low

## 2026-11-18 Download fallback only on 404

- Type: decision
- Phase: 04 (04-04)
- What: The fallback URL pair is tried only when the primary URL or checksum URL answers 404, for system images and ISOs alike.
- Why: One shared download path; without a fallback nothing changes.
- Cost of change: low

## 2026-11-18 Image Deleter removes only the disk file

- Type: decision
- Phase: 04 (04-06)
- What: `Module.Delete` removes `<data>\images\image-<id>.vhdx`; project deletion removes the record.
- Why: `Deps` has no store; project deletion owns the rows.
- Cost of change: low

## 2026-11-18 Image PUT converts only to snapshot

- Type: decision
- Phase: 04 (04-06)
- What: A `type` other than `snapshot` answers 422 `invalid_input` on `type`.
- Why: The description allows only the conversion to snapshot.
- Cost of change: low

## 2026-11-18 Image write routes only with NewModule

- Type: decision
- Phase: 04 (04-06)
- What: PUT, DELETE, change_protection and actions are registered only by `NewModule`; `New(cat)` stays read-only.
- Why: Existing users of `New` stay unchanged.
- Cost of change: low

## 2026-11-18 Disk-size refusal field

- Type: decision
- Phase: 04 (04-07)
- What: An image larger than the type's disk gives 422 `invalid_input` on `server_type` at create and on `image` at rebuild.
- Why: At create the type is the wrong choice, at rebuild the image.
- Cost of change: low

## 2026-11-18 Default image description

- Type: decision
- Phase: 04 (04-07)
- What: `create_image` without description uses "<server name> <created RFC3339>".
- Why: Hetzner generates one without saying how.
- Cost of change: low

## 2026-11-18 Deleting a creating image is locked

- Type: decision
- Phase: 04 (04-07)
- What: `DELETE /images/{id}` answers 423 `locked` while the image is `creating`.
- Why: DELETE starts no action, so a ref lock would not be checked.
- Cost of change: low

## 2026-11-18 image_size unrounded

- Type: decision
- Phase: 04 (04-07)
- What: `image_size` is the snapshot file size in GB, not rounded.
- Why: Simplest.
- Cost of change: low

## 2026-11-18 Failed volume create is dropped

- Type: decision
- Phase: 04 (04-12)
- What: When create fails (CreateDisk or mkfs) the volume record and file are removed and the action ends in error.
- Why: Same pattern as create_image; nothing half-made stays.
- Cost of change: low

## 2026-11-18 Resize stores the new size at the start

- Type: decision
- Phase: 04 (04-12)
- What: The new size is written with the action and restored on failure; resize needs an integer 10–10240 above the current size.
- Why: The disk rule counts the growth at once.
- Cost of change: low

## 2026-11-18 Volume delete refusals

- Type: decision
- Phase: 04 (04-12)
- What: Delete protection gives 423 `protected`, an attached volume 422 `invalid_input`, a `creating` volume 423 `locked`.
- Why: Follows the images module and the test map.
- Cost of change: low

## 2026-11-18 Volume names non-empty and unique per project

- Type: decision
- Phase: 04 (04-12)
- What: Names are checked like server names; Hetzner's character pattern is not.
- Why: Same rule as servers.
- Cost of change: low

## 2026-11-18 Missing appliance debs do not stop the cloud

- Type: decision
- Phase: 04 (04-05)
- What: If `appliance-debs.json` cannot be read or a download fails, hlabd logs a warning and writes the cidata without debs; a failing `dpkg -i` only logs.
- Why: ext4 and everything else work without xfsprogs; the xfs job fails with a clear message.
- Cost of change: low

## 2026-11-18 Appliance debs as `<name>.deb` at the cidata root

- Type: decision
- Phase: 04 (04-05)
- What: Debs are `xfsprogs.deb`, `libicu76.deb`, `libinih1.deb` on the ISO root and in `<data>\cache\debs\`; JSON `{"debs":[{"name","urls","sha256"}]}`.
- Why: cidata takes root files only; plain names avoid ISO9660 problems.
- Cost of change: low

## 2026-11-18 Rescue base prepared via a partial file

- Type: decision
- Phase: 04 (04-05)
- What: The base is copied to `<dst>.partial.vhdx`, prepared, then renamed; a failure or leftover partial file is removed.
- Why: A crash never leaves a half-prepared rescue.vhdx.
- Cost of change: low

## 2026-11-18 Appliance instance-id carries a user-data hash

- Type: decision
- Phase: 04 (04-05)
- What: The appliance instance-id is `applianceName()-<12 hex of sha256(user-data)>`; a changed script makes cloud-init rerun write_files and runcmd once.
- Why: An existing appliance picks up the new install script without recreating its disk.
- Cost of change: low

## 2026-11-18 Volume created with a server: locks

- Type: decision
- Phase: 04 (04-13)
- What: POST /volumes with a server starts `create_volume` on the volume and `attach_volume` on the server; the volume is recorded attached at the end of create, then the disk is added in the attach action.
- Why: `actions.Start` refuses a second action on the same volume in one transaction.
- Cost of change: low

## 2026-11-18 Attach state in the volume record

- Type: decision
- Phase: 04 (04-13)
- What: `_hlab` gets `lun` and `automount`; attach records before adding the disk and undoes on failure, detach records only after `DetachDisk` succeeds.
- Why: Concurrent attaches get distinct LUNs; a LUN is freed only when the disk is gone.
- Cost of change: low

## 2026-11-18 Detach from a missing VM succeeds

- Type: decision
- Phase: 04 (04-13)
- What: Detach and `DetachAll` treat a missing VM as success; detaching an unattached volume is a successful no-op action.
- Why: A half-created server must still be deletable.
- Cost of change: low

## 2026-11-18 Agent push only with automount

- Type: decision
- Phase: 04 (04-13)
- What: After attach or detach the agent document is pushed only when automount is involved.
- Why: Metadata.Volumes changes only then.
- Cost of change: low

## 2026-11-18 Router hands the action manager to modules

- Type: decision
- Phase: 04 (04-08)
- What: New optional `resource.ActionUser { UseActions(*actions.Manager) }`, called by `api.Router.Mount` before Routes.
- Why: The backup job must start real actions on the router's manager so locks and WaitIdle see them.
- Cost of change: low

## 2026-11-18 Daily backup done when an automatic backup exists for the UTC day

- Type: decision
- Phase: 04 (04-08)
- What: The job skips a server only if an image with `_hlab.automatic` was created that day; manual backups do not count.
- Why: A failed backup leaves no record, so the next hourly run retries.
- Cost of change: low

## 2026-11-18 Backup slots trimmed inside create_image

- Type: decision
- Phase: 04 (04-08)
- What: Right after a backup becomes available the action deletes the oldest records beyond 7 and their files.
- Why: Slots stay exact as soon as the action ends.
- Cost of change: low

## 2026-11-18 Volume hooks in the guest

- Type: decision
- Phase: 04 (04-11)
- What: A udev rule calls `hlab-volume-id` (last 6 bytes of the `vpd_pg83` designator holding `42414c480000`), links `scsi-0HC_Volume_<id>` and starts `hlab-volume@<id>.service`, which polls `GET /hlab/v1/volumes` every 2 s for up to 60 s and mounts only a volume with a filesystem, fstab `discard,nofail,defaults`.
- Why: The volume list is pushed after the hot-add, so the hook must wait for it.
- Cost of change: medium (guest script in vendor-data of existing servers)

## 2026-11-18 Volumes present at first boot are triggered

- Type: decision
- Phase: 04 (04-11)
- What: After writing the hooks the vendor script runs `udevadm control --reload`, `trigger --action=change` and `settle`.
- Why: Volumes attached at create exist before the rule.
- Cost of change: low

## 2026-11-18 Automount at create via runcmd

- Type: decision
- Phase: 04 (04-11)
- What: Seed user-data is multipart: the user's user_data unchanged plus a cloud-config with `merge_how` append and one `runcmd` per volume; runcmd and the hook share `/run/hlab-volume.lock`.
- Why: cloud-init's `mounts` runs before the vendor script creates the link; append keeps the user's runcmd.
- Cost of change: low

## 2026-11-18 Grown volumes picked up by the guest timer

- Type: decision
- Phase: 04 (04-11)
- What: The hlab-guest timer writes `1` to `/sys/block/<disk>/device/rescan` for each `scsi-0HC_Volume_*` disk every 5 s.
- Why: An idle disk learns of a new size only on its next SCSI command; the test itself still does not rescan.
- Cost of change: low

## 2026-11-18 Rescue seed held 90 s after the boot into rescue

- Type: decision
- Phase: 04 (04-09)
- What: After a poweron, reset or reboot into rescue the action waits `Deps.RescueSeedHold` (90 s) before restoring LUN 0 first and the server's seed.
- Why: The rescue system's cloud-init must read the rescue seed, not the server's.
- Cost of change: low

## 2026-11-18 One fixed rescue disk path per server

- Type: decision
- Phase: 04 (04-09)
- What: The rescue disk is `disks\server-<id>-rescue.vhdx`, the seed `server-<id>-rescue-cidata.iso`; enable_rescue first ends a previous rescue and deletes its disk.
- Why: LUN 63 holds one disk, and every rescue needs a fresh child disk.
- Cost of change: low

## 2026-11-18 Rescue disk detach errors after a boot are logged

- Type: decision
- Phase: 04 (04-09)
- What: A failed detach of a leftover rescue disk after a boot without rescue logs a warning; the status watch removes the disk once the VM is off.
- Why: A running or rebooting VM can refuse the hot-remove.
- Cost of change: low

## 2026-11-18 One boot-order helper for rescue and ISO

- Type: decision
- Phase: 04 (04-10)
- What: `bootOrder(rescue, iso)` puts LUN 63 first during rescue, then DVD slot 1 with an ISO, then LUN 0; ending rescue keeps an attached ISO in front of LUN 0.
- Why: Rescue keeps LUN 63 first and must not drop an attached ISO.
- Cost of change: low

## 2026-11-18 A failed ISO download leaves the server unchanged

- Type: decision
- Phase: 04 (04-10)
- What: `EnsureISO` runs before any VM change; on failure the action ends in error and a previously attached ISO stays.
- Why: Simplest form, matching the test row.
- Cost of change: low

## 2026-11-18 Reconcile restores an attached ISO

- Type: decision
- Phase: 04 (04-10)
- What: When reconcile recreates a VM with `_hlab.iso`, it re-inserts the ISO into slot 1 with the matching boot order.
- Why: The server's state stays consistent.
- Cost of change: low

## 2026-11-18 rescue-prepare waits for the partitions

- Type: decision
- Phase: 04 (04-09)
- What: The agent checks every 500 ms up to 30 s for the ext4 root and vfat ESP of the hot-added disk and runs `blockdev --rereadpt` while they are missing.
- Why: The kernel creates the block device before it reads the partition table.
- Cost of change: low

## 2026-11-18 Reboot out of a booted rescue via shutdown, detach, start

- Type: decision
- Phase: 04 (04-09)
- What: After a booted rescue ends, an hlab reboot shuts the guest down, waits up to 2 min for Off, removes the rescue disk and starts the VM.
- Why: Detaching the rescue disk while the rescue system runs from it hangs the guest.
- Cost of change: low

## 2026-11-19 Disk order fixed in the rescue initramfs

- Type: decision
- Phase: 04 (04-09)
- What: rescue-prepare installs an initramfs local-top script that, if LUN 0 is not sda, unbinds the sd disks and binds LUN 0 first, and rebuilds the initramfs in a chroot.
- Why: Debian probes sd disks in parallel; no kernel parameter makes the order stable.
- Cost of change: low

## 2026-11-19 Versioned rescue base file

- Type: decision
- Phase: 04 (04-09)
- What: The cached base is `<data>\images\rescue-v2.vhdx`; the name is bumped whenever the preparation changes.
- Why: Existing installs prepare the base again instead of keeping a stale one.
- Cost of change: low

## 2026-11-19 Guest flushes after cloud-init's final stage

- Type: decision
- Phase: 04 (04-10)
- What: The vendor script installs `hlab-sync.service` (After=cloud-final.service, runs sync once) and queues it with --no-block, replacing its own final `sync` (updates "Vendor script syncs before first boot ends").
- Why: user_data runcmd and scripts run after scripts_vendor; only a flush after cloud-final makes their files survive a hard reset soon after first boot.
- Cost of change: low

## 2026-11-19 Backlog phase B1 before phase 05

- Type: change
- What: The seven open `bug` items of BACKLOG.md become phase B1, inserted before phase 05; the `later` item for phase 09 and the `minor` test item stay in the backlog.
- Why: They cause stuck resources after a restart, wrong passwords and ignored user_data on normal paths, and a broken e2e build, which later phases would build on.

## 2026-11-19 Q6 Cloud start removes images and volumes left creating

- Type: decision
- Phase: B1
- What: an optional module interface `resource.Recoverer` runs at cloud start, before the API serves and before the first backup job run, and removes images and volumes left `creating` by an interrupted action, with their partial files; Reconcile is not called at cloud start (ARCHITECTURE corrected).
- Why: matches the `error` result of the interrupted action and the existing "failed create is dropped" handling, lets a client retry with the same name, and keeps the server path unchanged.
- Cost of change: low

## 2026-11-19 Stale password reset filtered by its action's status

- Type: decision
- Phase: B1
- What: `Contribute` serves a pending reset to the agent only while the reset_password action with the reset's ID is `running`; rebuild clears `Hlab.PasswordReset`.
- Why: `failInterrupted` marks the action `error` before the first agent push, so no start-up hook is needed and no path serves a stale reset.
- Cost of change: low

## 2026-11-19 NoCloud instance-id after a rebuild

- Type: decision
- Phase: B1
- What: Optional `Internal.InstanceID` in the server record (JSON `instance_id`, empty = server ID); every rebuild sets `<server id>-<rebuild action id>`, used by the server and rescue seed; metadata `/instance-id` stays the server ID.
- Why: A disk restored from the same server must count as a new instance so cloud-init reruns chpasswd and user_data.
- Cost of change: medium (guest-visible)

## 2026-11-19 Strict base64 decoding of user_data for the seed

- Type: decision
- Phase: B1
- What: user_data that is valid standard base64 without whitespace is decoded for the NoCloud seed, with and without volumes; anything else stays raw; metadata serves raw user_data.
- Why: Mirrors `maybe_b64decode(validate=True)` of the Hetzner datasource, which NoCloud lacks.
- Cost of change: low

## 2026-11-19 User MIME parts become leaf parts of hlab's multipart

- Type: decision
- Phase: B1
- What: With automount volumes, gzip is unpacked and each leaf part of a user MIME document is copied unchanged and in order before hlab's cloud-config part (boundary chosen to not collide); non-MIME content stays one base64 text/plain part.
- Why: cloud-init walks nested multiparts but does not unpack MIME or gzip inside text/plain, and `merge_how` needs hlab's part last.
- Cost of change: low

## 2026-11-19 `Platform.MetricsAll` with VM mapping in Go

- Type: decision
- Phase: B1
- What: A new interface method reads the three counter classes and VM disks once per call; Go maps rows to VMs by exact name (adapters: longest matching VM name); `Metrics(vm)` stays on top of it.
- Why: Extends the interface without breaking it and keeps the mapping unit-testable without Hyper-V.
- Cost of change: low

## 2026-11-19 Images module gets the store in its Deps

- Type: decision
- Phase: B1
- What: `imagesres.Deps.Store`, used only by `Recover`.
- Why: `Recover` runs outside a request and cannot take the store from the request context.
- Cost of change: low

## 2026-11-19 Start-up recovery runs before mounting

- Type: decision
- Phase: B1
- What: `recoverModules` (internal/cloud/recover.go) runs right after `c.modules()`, before mounting, the API and the scheduled jobs; an error fails cloud start.
- Why: Q6 requires it before the API serves and before the backup job.
- Cost of change: low

## 2026-11-19 E2E harness restores the cloud state it found

- Type: decision
- Phase: B1
- What: If the dev cloud runs, `setup()` stops it with the existing binary, builds, and starts it again without setting `startedCloud`, so it stays running.
- Why: A developer gets a test run instead of a build error and the cloud is left as found.
- Cost of change: low

## 2026-11-19 SRV-08 e2e row shares SRV-12's server and snapshot

- Type: decision
- Phase: B1
- What: A once-fixture provides the server and its snapshot to SRV-12 and SRV-08/own_snapshot.
- Why: No extra boot, as confirmed at the phase start.
- Cost of change: low

## 2026-11-19 A reset without its action is not served

- Type: decision
- Phase: B1
- What: a pending password reset whose action ID is no longer in the actions table counts as not running and is not served to the agent.
- Why: an action that no longer exists can never finish the reset, so serving it could only apply a password nobody was told about.
- Cost of change: low

## 2026-11-19 Seed multipart written with mime/multipart

- Type: decision
- Phase: B1
- What: With automount volumes the seed user-data is written by Go's `multipart.Writer` (CRLF, sorted part headers) instead of a hand-built LF document.
- Why: Standard MIME that cloud-init's email parser accepts, and copied user parts keep their exact bytes.
- Cost of change: low

## 2026-11-19 Base64 check for seed user_data

- Type: decision
- Phase: B1
- What: user_data is decoded only if the whole value matches `^[A-Za-z0-9+/]*={0,2}$` and decodes with correct padding; extra bits in the last character are accepted as in Python's validate=True.
- Why: Mirrors cloud-init's `maybe_b64decode(validate=True)`.
- Cost of change: low

## 2026-11-19 Seed boundary collision and unparsable MIME

- Type: decision
- Phase: B1
- What: If hlab's boundary occurs in copied content a number is appended until it does not; a MIME document that cannot be parsed falls back to one base64 text/plain part with the content after gunzip.
- Why: Keeps the outer document valid and never drops user content.
- Cost of change: low

## 2026-11-19 E2E harness on a fresh checkout

- Type: decision
- Phase: B1
- What: the harness skips the running check and the stop when bin\hlab.exe does not exist yet.
- Why: a fresh checkout has no binary to ask and no cloud of this working copy can run.
- Cost of change: low

## 2026-11-19 MetricsAll leaves out VMs not on the host

- Type: decision
- Phase: B1
- What: A VM counts as existing when `Get-VM` lists it; a requested VM missing there is left out of the map and `Metrics(vm)` returns NotFound as before.
- Why: Keeps the behaviour of `Metrics` and lets the sampler skip servers whose VM is gone.
- Cost of change: low

## 2026-11-19 Adapters map to the longest VM name on the host

- Type: decision
- Phase: B1
- What: Network adapter rows go to the longest matching VM name among all VMs on the host, not only the requested ones.
- Why: A VM not sampled must not have its adapters counted for a VM whose name is a prefix of its own.
- Cost of change: low

## 2026-11-19 Failed metrics tick stores nothing

- Type: decision
- Phase: B1
- What: If the MetricsAll call fails the tick stores nothing and logs a warning (not when the context was cancelled); with no running servers no platform call is made.
- Why: One failed read must not write zero samples for all servers.
- Cost of change: low

## 2026-11-19 Upgrade to wcc 0.32.4

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.3 to 0.32.4: CLAUDE.md rule on removing code the plan's own change left without callers
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-19 Q7: drop the unused per-VM Metrics call

- Type: change
- Phase: change
- What: `Metrics(ctx, vm)` is removed from `platform.Platform`, the hyperv implementation, the fake and ARCHITECTURE.md; `MetricsAll` stays, and phase 05 does the code removal (Q7).
- Why: since B1-03 nothing calls it, and every new platform would otherwise implement and maintain a method nothing uses.
- Cost of change: low

## 2026-11-19 Guest picks up private network changes live

- Type: decision
- Phase: 05
- What: The `hlab-guest` timer configures attached and detached private adapters (`02:48:4c:2x` only, primary private IP and the route to the network range, no alias IPs) from the metadata service via networkd files in `/run` or an `ip` fallback; the seed is rebuilt too, and a cloud-init drop-in `99-hlab-network.cfg` re-renders network config on every boot.
- Why: The user wants changes to act without a reboot, as at Hetzner (hc-utils with DHCP); hlab has no DHCP server and vendor-data `updates` acts only from the second boot.
- Cost of change: low

## 2026-11-19 Primary IP records for existing servers

- Type: decision
- Phase: 05
- What: The primary IP module's `Recover` creates missing primary IP records of existing servers at cloud start, idempotently, assigned and `auto_delete: true`; a primary IPv6 created with a server keeps `IPv6Net(serverID)`, standalone ones use `0x80000000|id`, floating ones `0xC0000000|id`.
- Why: The user keeps the dev cloud's servers; existing formal addresses stay.
- Cost of change: low

## 2026-11-19 Private adapter MACs

- Type: decision
- Phase: 05
- What: Private adapters get `02:48:4c` plus nibble `0x2` plus a 20-bit per-attachment counter; the appliance trunk adapter is `02:48:4c:00:00:02`; deviations.md notes Hetzner's `86:00:00:xx:xx:xx`.
- Why: Stays within the approved prefix and cannot collide with public MACs or the appliance.
- Cost of change: low

## 2026-11-19 One gateway per network

- Type: decision
- Phase: 05
- What: Each network's gateway is the first host address of its `ip_range`, shown as every subnet's `gateway` and reserved with each subnet's network and broadcast address and `172.31.1.1`; private IPs are the lowest free host address of the subnet.
- Why: Hetzner's FAQ and the spec examples say so (the spec text contradicts itself), and it fits one VRF per network.
- Cost of change: medium

## 2026-11-19 Network and IP refusals

- Type: decision
- Phase: 05
- What: `change_ip_range` keeps the start address and only grows (422 `service_error` with attached servers, 409 `conflict` during a change); deleting an assigned primary or floating IP gives 422 `must_be_unassigned`; delete protection on an `auto_delete` primary IP, more than 5 alias IPs and a create without IPv4, IPv6 and network give 422 `invalid_input`; a fourth network gives 403 `resource_limit_exceeded`; `delete_subnet` with attached servers gives 422 `service_error`.
- Why: The frozen spec where it says so; Hetzner documents no code for the rest.
- Cost of change: low

## 2026-11-19 Floating IP zone rule

- Type: decision
- Phase: 05
- What: A floating IP is assigned only to a server in the network zone of its home location (else 422 `invalid_input`); assigning to another server moves it, to the same server gives 422 `floating_ip_assigned`.
- Why: The stricter docs rule; FIP-02 needs a move without unassigning first.
- Cost of change: low

## 2026-11-19 Appliance private network layout

- Type: decision
- Phase: 05
- What: Per network a subinterface `t<vlan>` of the trunk adapter (found by MAC), VRF `vrf<vlan>`, table 1000+vlan, sysctls `proxy_arp`, `proxy_arp_pvlan`, no redirects, `rp_filter=0`; one conntrack zone per VLAN; stateless isolation that admits a gateway member's route destinations; one metadata listener per VRF with `SO_BINDTODEVICE`.
- Why: Members share one subinterface, ranges may overlap across VRFs, and research prefers per-VRF listeners.
- Cost of change: low

## 2026-11-19 Network membership on the server record

- Type: decision
- Phase: 05
- What: Membership lives in the server record (`private_net` plus `_hlab.networks`); the network's `servers` list is derived; servers own adapters, deny lists and seed (`SyncAdapters`), reached through the hooks `SetPublicIPs`, `SetNetworks`, `OnDelete`; a running VM's seed is swapped by ejecting and reinserting DVD slot 0.
- Why: Seed and adapters are built from the server record; hooks avoid import cycles as with volumes.
- Cost of change: medium

## 2026-11-19 Trunk adapter guards and deny-list sync

- Type: decision
- Phase: 05
- What: Trunk adapters have router guard and DHCP guard off like the appliance's promiscuous port; `SetNICDeny` keeps wanted deny entries present in both directions, removes partial or unwanted ones, adds missing ones, and leaves IP and non-deny ACLs alone.
- Why: The appliance routes on the trunk; Hyper-V refuses duplicate ACLs and lists `Both` as two entries.
- Cost of change: low

## 2026-11-19 Network validation details

- Type: decision
- Phase: 05
- What: Field errors name nested create fields `subnets[i].<field>`/`routes[i].<field>`; a `0.0.0.0/0` route skips the overlap checks with subnets and 172.31.1.1 but not with other routes; `change_ip_range` to the same size is a no-op; no free VLAN gives 403 `resource_limit_exceeded`; no free /24 and unknown subnet or route in delete actions give 422 `invalid_input`.
- Why: Hetzner documents none of these; the simplest answers that fit the frozen spec.
- Cost of change: low

## 2026-11-19 Server create and delete without public IPv4

- Type: decision
- Phase: 05
- What: cidata renders an interface without addresses as `optional: true`, so a server without IPv4 keeps eth0 and does not block wait-online; with the PublicIPs hook set, server delete leaves releasing the IPv4 to the primary IP's `ServerDeleted`; without hooks create follows `enable_ipv4/6`, ignores primary IP IDs and refuses `networks` with 422 `invalid_input`.
- Why: Keeps interface names stable and gives one owner for each IPv4.
- Cost of change: low

## 2026-11-19 VRF details on the appliance

- Type: decision
- Phase: 05
- What: Each VRF table holds an `unreachable default` route (kernel metric 4278198272); the gateway sits on `t<vlan>` as a `/32` without a connected route for the range; the trunk link is looked up only when the document has networks; stale `vrf<N>`/`t<N>` links and agent routes in tables 1002..5092 are always cleaned; the VRF applier runs after the Network applier.
- Why: Misses must not fall through to the main table (NET-02), only members, aliases and routes are reachable, and appliances without a trunk keep applying.
- Cost of change: low

## 2026-11-19 Private adapters of a server

- Type: decision
- Phase: 05
- What: The server record keeps the private MACs its VM has in `Internal.Adapters`; while rescue is on `SyncAdapters` writes the seed but leaves DVD slot 0; NotFound from `SetNICDeny`/`DetachNIC` counts as done; private adapters get `Segment{VLAN}` from the server record, `SegmentFor` decides only the public segment (ARCHITECTURE.md updated).
- Why: The platform cannot list adapters, a detach clears `private_net` before the sync, and the VLAN is server data.
- Cost of change: low

## 2026-11-19 Isolation on private networks

- Type: decision
- Phase: 05
- What: The metadata input rule on private networks matches the ingress subinterface with `meta sdifname` (key 34, defined locally; kernel 5.1+), since `iifname` is the VRF device there; a route accept rule exists only when the route's gateway is a member IP or alias IP; conntrack zones are set in prerouting only.
- Why: Keeps "Metadata service binds IP and MAC on its input path" on every segment; no rule uses `ct state`, and Hetzner firewalls do not filter private networks.
- Cost of change: low

## 2026-11-19 Primary IP details

- Type: decision
- Phase: 05
- What: Primary IPs have no `datacenter` field (the frozen schema has none), the location lives in `_hlab.location`; create with `assignee_id` returns an `assign_primary_ip` action, with `location` none; automatic and recovered ones are named `primary_ip-<id>`; given `public_net` IDs win over `enable_ipv4/6: false`; DELETE checks protection before assignment; assign and unassign rebuild the server's seed through `SyncAdapters`.
- Why: The frozen spec forbids extra properties; the seed must carry the new address for the next boot (PIP-02).
- Cost of change: low

## 2026-11-19 Guest hook for private adapters

- Type: decision
- Phase: 05
- What: `hlab-guest` takes over only private links that do not yet carry their private IP (hot-added after boot), records what it applied in `/run/hlab-guest/networks.json` and acts only on changes; without networkd it sets the route before the address; a failed network sync does not stop the password reset.
- Why: The seed's netplan config must stay in charge of links it set up, so user alias IPs and NAT routes survive (NET-03).
- Cost of change: low

## 2026-11-19 Floating IP details

- Type: decision
- Phase: 05
- What: A floating IP without name is named `floating_ip-<id>`; create with `server` returns an `assign_floating_ip` action, with `home_location` none; the list sorts by id and created and filters by name, label_selector and ip; the FIP-01 test reads the server's top-level `location` as the frozen schema has it.
- Why: The frozen spec says so; the old test path read a field the server shape does not have.
- Cost of change: low

## 2026-11-19 Metadata per VRF

- Type: decision
- Phase: 05
- What: The VRF applier gets the metadata service through the field `VRF.Metadata`, set by main.go before the first Apply; each listener passes its VLAN to the shared request router through the request context.
- Why: Keeps `NewVRF(trunkMAC)` as planned and registers every metadata path once.
- Cost of change: low

## 2026-11-19 Private metadata entries

- Type: decision
- Phase: 05
- What: `servers.Module.Metadata` builds a server's metadata entry without VLAN and IP and networks uses it for each member's private entry; `InterfaceNum` is the 1-based index in `private_net`; a member's `Subnet` is the subnet holding its IP.
- Why: A server without public IPv4 has no public entry to copy; the index matches the seed's interface order.
- Cost of change: low

## 2026-11-19 Attach and detach rules

- Type: decision
- Phase: 05
- What: `attach_to_network` checks network existence, `server_already_attached`, `networks_overlap`, then the 3-network limit; no free IP or no zone subnet gives `no_subnet_available`, a used or reserved `ip` or alias IP `ip_not_available`, an `ip`/`ip_range`/alias outside the subnets 422 `invalid_input`; alias IPs may come from any subnet of the network; `DELETE /networks` syncs the detached members' adapters after the commit before answering 204, logging failures.
- Why: Hetzner documents no order or code for these cases.
- Cost of change: low

## 2026-11-19 Cloud wiring of the network modules

- Type: decision
- Phase: 05
- What: Module order is servers, networks, primary_ips, floating_ips, volumes; cloud start detects a missing appliance trunk adapter by `SetNICDeny(appliance, 02:48:4c:00:00:02, nil)` returning NotFound and then attaches it; hyperv `SetDVD` with "" returns only once the drive is empty (at most 60 s).
- Why: volumes fill every metadata entry of a server only after networks made them; no new platform method is needed; Hyper-V finishes an eject about 5 s after returning, which broke every seed rewrite on running VMs.
- Cost of change: low

## 2026-11-19 Upgrade to wcc 0.32.6

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.4 to 0.32.6: the test rule on parallel end-to-end tests now takes shared limits in one step and logs each test's start and end with a time limit
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-19 Firewall create with unknown server answers 422

- Type: decision
- Phase: phase 06
- What: `POST /firewalls` with an `apply_to` server that does not exist answers `422 firewall_resource_not_found`; `apply_to_resources` and `remove_from_resources` answer 404 with that code.
- Why: the Hetzner OpenAPI documents 422 for create (RESEARCH.md), which overrides the 404 assumed at the phase start.
- Cost of change: low

## 2026-11-19 Firewall rule limits

- Type: decision
- Phase: phase 06
- What: a firewall takes at most 50 rules (validated); Hetzner's limit of 500 effective rules per firewall is not enforced and listed in `deviations.md`.
- Why: research shows the 500 limit is per firewall in effective rules, not per server as assumed at the phase start.
- Cost of change: low

## 2026-11-19 Outbound firewall rules never filter appliance services

- Type: decision
- Phase: phase 06
- What: resolvers, metadata service and API proxy on the appliance are never filtered by firewall rules; the firewall matches only forwarded traffic on the public interface.
- Why: Hetzner never filters resolver and metadata traffic (RESEARCH.md), so the user's answer to Q1 falls back to "never filter"; `hlab-guest` needs the metadata service.
- Cost of change: low

## 2026-11-19 Firewall rule input format

- Type: decision
- Phase: phase 06
- What: a CIDR with host bits set or without a mask gives 422 `invalid_input`; ports are `any`, `N` or `A-B` (1 <= A <= B <= 65535, digits only) echoed as sent; rules and IPs keep the order sent, IPv6 comes back in RFC 5952 form.
- Why: the spec wording suggests rejection, clients always send masked CIDRs, and the provider and Ansible compare rules order-sensitively.
- Cost of change: low

## 2026-11-19 Firewall apply and remove error cases

- Type: decision
- Phase: phase 06
- What: removing a server or selector that is not applied gives 404 `firewall_resource_not_found`; applying an applied selector gives 422 `firewall_already_applied`; a sixth direct firewall on a server gives 403 `resource_limit_exceeded`, selector matches beyond five are skipped; DELETE of a firewall applied only by selector gives 422 `resource_in_use` with message `firewall <id> is still in use`; server create with an unknown firewall gives 404 `not_found`, with firewalls and no public IPv4 422 `private_net_only_server`; `incompatible_network_type` is never returned.
- Why: `firewall_already_removed` is deprecated and the pinned clients tolerate the 404; Ansible retries on "is still in use"; Hetzner documents the rest only loosely.
- Cost of change: low

## 2026-11-19 Firewall actions

- Type: decision
- Phase: phase 06
- What: `apply_firewall`/`remove_firewall` for a server name server and firewall, for a selector one action naming only the firewall; `set_rules` returns `set_firewall_rules` plus one `apply_firewall` per directly applied server, create with rules and `apply_to` the same; the record changes in the request's transaction before the actions run; new `actions.Manager.StartAll(tx, projectID, lock, specs)` checks locks once on `lock` and never locks actions of one batch against each other.
- Why: follows the spec examples; GET and DELETE right after a remove see the new state (provider delete retry); `Start` would lock the apply actions against the running `create_server` and against each other.
- Cost of change: low

## 2026-11-19 Firewall traffic semantics

- Type: decision
- Phase: phase 06
- What: with several firewalls, outbound is restricted once any has an `out` rule and the union of `out` rules is allowed; traffic to and from a floating IP is filtered by the firewalls of its server (listed in `deviations.md`); not enforced and listed in `deviations.md`: 500 effective rules per firewall, 50 firewalls per project.
- Why: natural reading of "combined"; Hetzner does not document floating IP behaviour.
- Cost of change: low

## 2026-11-19 Firewall nftables table on the appliance

- Type: decision
- Phase: phase 06
- What: the firewall lives in its own table `inet hlab_fw`, forward chain at filter priority + 10, matching only the public interface and stateful through `ct state established,related`; table `inet hlab` stays unchanged.
- Why: keeps isolation untouched, gives FW-04 by construction, and an explicit priority avoids undefined order between base chains.
- Cost of change: medium

## 2026-11-19 Firewalls in server create

- Type: decision
- Phase: phase 06
- What: a firewall ID listed twice in a server create is applied once; a sixth firewall answers 403 `resource_limit_exceeded`; a selector's `applied_to_resources` also lists servers applied directly; each firewall action runs its own agent push.
- Why: Hetzner documents none of these cases; the simplest behaviour that keeps every client working.
- Cost of change: low

## 2026-11-19 Upgrade to wcc 0.32.8

- Type: approval
- Phase: upgrade
- What: project files brought from wcc 0.32.6 to 0.32.8: SPEC.md section "References" with the Hetzner OpenAPI description, version line in CLAUDE.md
- Why: the skills of the installed version search for these keys and rules
- Cost of change: low

## 2026-11-19 Dev cloud access for agents through hcloud context hlab-docs

- Type: change
- Phase: change
- What: agents run documentation examples against the dev cloud through a user-created hcloud context `hlab-docs` (endpoint `http://api.hetzner.internal/v1` set per context, token of project `docs`) and pass its token to OpenTofu and Ansible only inside the one command that needs it, never reading it.
- Why: the doc-writer has to check that the README examples work, and hcloud 1.70.2 stores the endpoint per context, so no environment variable or task is needed.
- Cost of change: low

## 2026-11-19 Zones under private-use suffixes

- Type: change
- Phase: phase 07
- What: besides a registrable domain of the Public Suffix List, a zone name may lie under `.internal`, `.test` or `.home.arpa` (except `hetzner.internal` and below, used by hlab, and never `.local`, used by mDNS); such zones never shadow a public name, so DNS-04 skips them; DNS-01 changed and the deviation goes to docs/user/deviations.md.
- Why: a local lab needs zone names that cannot collide with real domains.
- Cost of change: low

## 2026-11-19 Q8: secondary zones are stored and forwarded

- Type: change
- Phase: phase 07
- What: zones with `mode: secondary` are accepted and stored with their `primary_nameservers`, have no records in hlab, and the appliance forwards their queries upstream; they get their NRPT rule (DNS-03) but never count as `shadowing_zones` (DNS-04); listed in docs/user/deviations.md.
- Why: clients and OpenTofu configurations with secondary zones keep working without a zone transfer component.
- Cost of change: low

## 2026-11-19 Zone modes and synchronous updates

- Type: decision
- Phase: phase 07
- What: on a secondary zone the RRSet calls, `change_ttl` and `import_zonefile` answer 422 `incorrect_zone_mode`, as does `change_primary_nameservers` on a primary zone; `PUT /zones/{id}` and `PUT .../rrsets/{name}/{type}` answer 200 synchronously without an action, all other writes run as actions; where the phase 07 research finds Hetzner behaving differently, the research wins.
- Why: the user's answer at the start of phase 07, matching Hetzner's documented behaviour as far as known.
- Cost of change: low

## 2026-11-19 Zone name and record validation

- Type: decision
- Phase: phase 07
- What: zone names count only the ICANN section of the Public Suffix List (everything under `arpa` except `home.arpa` refused) and private-use names are exactly one label below `internal`, `test` or `home.arpa`, all refusals `invalid_input` on `name`; RP values only in DNS mailbox form; a CNAME conflict (beside another type, twice, at the apex) is 422 `invalid_input` on `type` with `(<name>, CNAME) conflicts with (<name>, <type>)`; an existing RRSet on create is 409 `uniqueness_error`, a write on a protected RRSet `protected`; the apex SOA is read-only except its TTL, apex NS editable.
- Why: the research could not confirm these details; the closest of Hetzner's documented rules and the project's existing codes.
- Cost of change: low

## 2026-11-19 Zone files, counts and actions

- Type: decision
- Phase: phase 07
- What: `record_count` counts SOA and NS (4 for a new zone); a bad zone file fails synchronously with 422 `invalid_input` on `zonefile` naming the line; the export writes `$ORIGIN`, `$TTL`, SOA, NS, then the rest sorted by name and type, a TTL only where the RRSet has its own; zone and RRSet actions are readable under `/zones/actions/{id}`, the per-zone action path and `/actions/{id}`; zone `status` is always `ok`.
- Why: follows the hcloud-go and CLI fixtures; the simplest behaviour where Hetzner is unconfirmed.
- Cost of change: low

## 2026-11-19 Reverse DNS defaults and zone serving

- Type: decision
- Phase: phase 07
- What: fresh primary and floating IPv4 addresses get the server default `static.<d>.<c>.<b>.<a>.clients.your-server.de`, IPv6 starts empty; a server's `change_dns_ptr` for an IP it does not hold is `invalid_input` on `ip`; `servers.IPv6.DNSPtr` becomes `[]rdns.Entry` with compatible JSON; the agent follows CNAME chains at most 8 steps and wildcards per RFC 4592; secondary zones stay out of the agent document but get their NRPT rule; secondary `primary_nameservers` are checked for syntax only; a zone name used in another project is 409 `uniqueness_error` without naming it.
- Why: consistent behaviour across resources without a migration or an `agentapi` change, and no leak across projects.
- Cost of change: low

## 2026-11-19 Reading NRPT rules in the DNS-03 test

- Type: decision
- Phase: phase 07
- What: the DNS-03 e2e test reads NRPT rules with `Get-DnsClientNrptRule` filtered by the profile tag and falls back to the `DnsPolicyConfig` registry key if that needs elevation; it never changes rules.
- Why: whether reading needs elevation is unconfirmed.
- Cost of change: low

## 2026-11-19 Reverse DNS entry rules

- Type: decision
- Phase: phase 07
- What: a new IPv6 entry beyond 100 per /64 is `invalid_input` on `ip`; a `dns_ptr` needs at least two labels of letters, digits, `-` or `_`, no trailing dot, at most 253 characters; entries are keyed by the canonical IP form.
- Why: Hetzner's exact rules are unconfirmed; the simplest check that refuses obvious mistakes.
- Cost of change: low

## 2026-11-19 Default zone sets and record value checks

- Type: decision
- Phase: phase 07
- What: the default SOA (`hydrogen.ns.hetzner.com. dns.hetzner.com. <serial> 86400 10800 3600000 3600`) and NS sets carry no TTL of their own and use the zone TTL; record values with a line break or a trailing `;` comment are refused; owner names may contain `_` and a `*` label.
- Why: follows Hetzner's defaults where known and keeps zone file export unambiguous.
- Cost of change: low

## 2026-11-19 change_dns_ptr body and side effects

- Type: decision
- Phase: phase 07
- What: `change_dns_ptr` requires the `dns_ptr` key (missing is `invalid_input` on `dns_ptr`, `null` resets); a primary IP's action names only the primary IP, pushes only the agent document and copies the PTR into the assigned server's `public_net` without rebuilding its seed.
- Why: a PTR is not part of the seed, and an explicit key avoids resetting a PTR by an empty body.
- Cost of change: low

## 2026-11-19 Zone file parsing details

- Type: decision
- Phase: phase 07
- What: an RRSet has its own TTL only when its line names one; `$TTL` and record TTLs accept BIND units (`1h30m`); `$INCLUDE` and `$GENERATE` are refused with the line; owner names are lower-cased on import; the apex NS must contain the three Hetzner nameservers and may hold more.
- Why: ordinary BIND zone files import unchanged and export and import round-trip exactly.
- Cost of change: low

## 2026-11-19 Agent zone answers

- Type: decision
- Phase: phase 07
- What: empty non-terminals count as existing (wildcards answer below them, a direct query gets NODATA); an RRSet with TTL 0 uses the zone TTL; unparsable values are skipped with a warning and a zone without apex SOA is not served; for an outside CNAME target an upstream NXDOMAIN is passed on with the chain, any other upstream failure or the 8-step limit returns the chain so far.
- Why: RFC 4592 behaviour and an agent that keeps answering when one record is bad.
- Cost of change: low

## 2026-11-19 Zone create and publish details

- Type: decision
- Phase: phase 07
- What: `rrsets` errors on zone create name `rrsets[<i>].<field>`; a given SOA set is refused, a given apex NS set replaces the default; a secondary zone needs `primary_nameservers`; every zone action publishes (agent push, then `SetZones`) under one module lock, `change_ttl` raises the SOA serial; `GET /zones/actions/{id}` is served through the `/zones/{id}/{sub}` route because `http.ServeMux` refuses both patterns side by side.
- Why: concurrent zone actions must never leave a zone without its NRPT rule (DNS-03).
- Cost of change: low

## 2026-11-19 Public lookup for shadowing zones

- Type: decision
- Phase: phase 07
- What: `PublicLookup` treats SERVFAIL or REFUSED as no answer and tries the next server; a later `Check` or `Forget` discards the result of an older lookup still running; `hlab cloud status` prints `warning: zone <zone> (project <project>) shadows a public domain`.
- Why: the simplest reading of "error when no server answers" and "a later call replaces the result".
- Cost of change: low

## 2026-11-19 RRSet list and record actions

- Type: decision
- Phase: phase 07
- What: `sort=created` on RRSets sorts by creation order in the zone document; `remove_records` and `update_records` refuse a value not in the RRSet on `records[<i>].value`; `add_records` with a `ttl` other than the RRSet's TTL is `invalid_input` on `ttl`; RRSet list and get on a secondary zone also answer `incorrect_zone_mode`.
- Why: RRSets have no `created` field, and the OpenAPI text and the user's answer at the phase start set the rest.
- Cost of change: low

## 2026-11-19 Zone file import rules

- Type: decision
- Phase: phase 07
- What: an import is refused with `protected` when any RRSet of the zone is change-protected; the imported SOA keeps its values except the serial, which hlab continues; a `$TTL` below 60 is a zone file error on its line; imported RRSets have no labels and no protection; `POST /zones` with both `zonefile` and `rrsets` is `invalid_input` on `zonefile`; create and import both run the shadow check.
- Why: the simplest reading of D4, D5 and DNS-04.
- Cost of change: low
