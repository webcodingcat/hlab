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
- What: hostnet adds one inbound Windows Firewall rule (group `hlab`): TCP 8780 on `vEthernet (hlab)` from 198.19.0.1 only; `Down` and the uninstaller remove it.
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
