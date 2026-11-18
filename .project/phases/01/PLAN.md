# Phase 01 plan: walking skeleton

Context: CONTEXT.md. Research: RESEARCH.md. Tests: TESTS.md.

## Interfaces new in this phase

Test writers and implementers use these names; ARCHITECTURE.md gives the
rest. Constructors take a `Deps`/`Options` struct so tests pass fakes.

- `internal/profile`: `var Name = "dev"` (set by `-ldflags -X`), `Current()`
  and `Other()` return `Profile{Name, ServiceName, PipeName, DataDirName, Tag}`
  per ARCHITECTURE.md "Profiles".
- `internal/settings`: `Load(path string) (Settings, error)` with the
  defaults of ARCHITECTURE.md "Settings"; a missing file gives the defaults.
- `internal/api`: `api.New(api.Deps{Store, Actions}) *Router`,
  `(*Router).Mount(resource.Module)`, `http.Handler`; `api.Error`,
  `api.List` (one page, valid `meta.pagination` in this phase).
- `internal/apitest`: `apitest.New(t, mods ...resource.Module) *Env` with a
  temporary store, project `p` and a read/write token; `Env.Do(method, path,
  body) *http.Response` validates every response against the frozen OpenAPI
  description and fails the test with "OpenAPI description not cached: run
  `task openapi`" when it is missing.
- `internal/actions`: `actions.New(store) *Manager`, `Start`, `Run` as in
  ARCHITECTURE.md; module `internal/resources/actions` serves `/actions`,
  `/actions/{id}`.
- `internal/images`: `images.New(images.Options{Dir, Catalog, HTTP,
  Convert}) *Store`, `Ensure(ctx, name) (basePath string, err error)`.
- `internal/cidata`: `cidata.Build(cidata.Seed) ([]byte, error)`.
- `internal/platform/hyperv`: `hyperv.New(hyperv.Options{}) (platform.Platform, error)`.
- `internal/hostnet/windows`: `windows.New(windows.Options{Run Runner,
  OtherInstanceRunning func(ctx) (bool, error), StateDir string})`;
  `Runner` runs one PowerShell script and returns its JSON output, so tests
  record the scripts. `Up` returns the host DNS servers through
  `HostNet.DNSServers(ctx) []netip.Addr` (additive to the interface).
- `internal/ipam`: `ipam.NextPublic(tx store.Tx, r netip.Prefix) (netip.Addr, error)`, `ipam.Release`.
- `internal/mgmt`: `mgmt.New(mgmt.Deps{Store, API http.Handler, Status func() Status}) http.Handler`.
- `internal/cli`: `cli.Run(ctx, args []string, io cli.IO) int` with
  `IO{Stdin, Stdout, Stderr, Dial func(ctx) (net.Conn, error), Service sysint.ServiceControl, IsTerminal func() bool}`;
  `cli.Commands()` lists every command and flag (DOC-01).
- `internal/cloud`: `cloud.New(cloud.Deps{...}).Start(ctx) / Stop(ctx, reason)`.
- `internal/agent/...` (Linux only, `cmd/hlab-agent`): applies `agentapi.Config`.

## Plans

- [x] 01-01 Shared type packages
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: - | Tests: -
  - Size: ~350 lines in 6 files
  - [x] `internal/agentapi` (Config incl. `Upstream`, Job, Status), `internal/platform` (interface, types, `Error`, `SegmentFor`), `internal/hostnet` (interface incl. `DNSServers`, Config, Problem, WSLInfo)
  - [x] `internal/profile` with `Current`/`Other`; `Taskfile.yml` build passes `-X .../profile.Name=dev`

- [x] 01-02 Store
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: - | Tests: (used by all unit tests)
  - Size: ~350 lines in 3 files
  - [x] `internal/store`: schema v1 (ARCHITECTURE.md "Data schema"), `Open`, `Tx`, `Get/List/Put/Delete[T]`, `NextID`, kv, projects and tokens access (`TokenByHash`)

- [x] 01-03 System integration and settings
  - Wave: 2 | Depends on: 01-01 | Review: alone
  - Requirements: LIFE-01, LIFE-06 | Tests: test/e2e (LIFE-01)
  - Size: ~400 lines in 5 files
  - [x] `internal/sysint` (Paths, ServiceControl, ServiceHost, Installer interfaces) and `internal/sysint/windows`: Paths per profile, ServiceControl with minimal SCM rights (E10), ServiceHost with preshutdown, pipe listen/dial with ACL (go-winio)
  - [x] Installer: `hlabd install [--dev]` (service with descriptor, preshutdown 180 s, protected data folder ACLs; both profiles may be installed side by side) and `uninstall-cleanup` skeleton; `internal/settings`

- [x] 01-04 API core and OpenAPI tooling
  - Wave: 2 | Depends on: 01-02 | Review: alone
  - Requirements: API-02 | Tests: internal/api/auth_test.go
  - Size: ~400 lines in 5 files
  - [x] `task openapi`, `api/openapi.lock`, cache in the user cache folder (`internal/apitest` is written by the test writer)
  - [x] `internal/api`: router, request log, token auth, JSON decoding, read-only guard, `Error`, `List`; `internal/resource` (Module, Registrar)

- [x] 01-05 Actions
  - Wave: 3 | Depends on: 01-04 | Review: alone
  - Requirements: - | Tests: (API-10 rows of /actions)
  - Size: ~300 lines in 3 files
  - [x] `internal/actions`: complete the first manager from 01-04 (project_id, locks, `locked`); `internal/resources/actions` (`GET /actions` with repeated `id` and `sort` as hcloud polls, `/actions/{id}`)

- [x] 01-06 Catalog and catalog resources
  - Wave: 3 | Depends on: 01-04 | Review: alone
  - Requirements: CAT-01, CAT-06 | Tests: internal/resources/locations/locations_test.go, internal/resources/images/images_test.go
  - Size: ~400 lines in 8 files (generated JSON not counted)
  - [x] `task catalog` (tool in `tools/catalog`, hcloud context or hidden prompt, SPEC.md "Accounts and secrets"); generate `catalog/{locations,server-types,load-balancer-types,pricing}.json`; write `catalog/images.json` by hand
  - [x] modules `locations`, `datacenters` (410 as the frozen description), `server_types`, `images` (system images from the catalog)

- [x] 01-07 cidata
  - Wave: 2 | Depends on: 01-01 | Review: alone
  - Requirements: - | Tests: (SRV-01, SRV-03 e2e)
  - Size: ~200 lines in 2 files
  - [x] `internal/cidata`: meta-data, user-data, vendor-data (empty hooks for now), network-config v2 (/32, on-link 172.31.1.1, resolvers), extra files (agent binary for the appliance)

- [x] 01-08 Image pipeline
  - Wave: 2 | Depends on: 01-01 | Review: alone
  - Requirements: CAT-07 | Tests: internal/images/images_test.go
  - Size: ~350 lines in 4 files
  - [x] `internal/images`: download with checksum file from the source, verify, qcow2/raw to fixed VHD (E2), convert via callback, cache, single conversion under concurrent use

- [x] 01-09 Hyper-V platform: runner, switch, VMs
  - Wave: 2 | Depends on: 01-01 | Review: alone
  - Requirements: - | Tests: (e2e)
  - Size: ~400 lines in 4 files
  - [x] persistent PowerShell 5.1 runner (stdin commands, JSON out, mutex, restart)
  - [x] EnsureSwitch (internal, host adapter isolated PVLAN), CreateVM (E2 defaults), SetVM, DeleteVM, Power, State, HostResources

- [x] 01-10 Hyper-V platform: disks, NICs, DVD, boot
  - Wave: 3 | Depends on: 01-09 | Review: alone
  - Requirements: SRV-19 | Tests: (SRV-19 e2e)
  - Size: ~350 lines in 3 files
  - [x] CreateDisk (dynamic VHDX, copy of base, new identifier), ResizeDisk, AttachDisk, DetachDisk, AttachNIC (PVLAN, guards, MAC with dashes), DetachNIC, SetDVD, SetBootOrder

- [x] 01-11 Host networking (Windows)
  - Wave: 2 | Depends on: 01-01 | Review: alone
  - Requirements: SRV-06, LIFE-10 | Tests: internal/hostnet/windows/hostnet_test.go
  - Size: ~400 lines in 4 files
  - [x] Check (range collision, WinNAT exists, other_instance_running), Up (adapter IP, route ActiveStore, WinNAT, forwarding hlab and WSL, firewall rule, base NRPT), Down, restore file, DNSServers, SetZones

- [x] 01-12 hlab-agent: config server, routes, API proxy
  - Wave: 2 | Depends on: 01-01 | Review: alone
  - Requirements: - | Tests: (API-01, SRV-03 e2e)
  - Size: ~400 lines in 5 files
  - [x] `cmd/hlab-agent`, `internal/agent`: HTTP on 198.19.0.1:9100 with bearer secret, `PUT /v1/config`, `GET /v1/status`; addresses on eth0, /32 routes to the public adapter, IP forwarding; API reverse proxy 198.19.0.1:80
  - [x] appliance boot files: systemd unit that installs the agent from the cidata ISO at every boot

- [x] 01-13 hlab-agent: DNS and isolation backstop
  - Wave: 3 | Depends on: 01-12 | Review: alone
  - Requirements: - | Tests: (API-01, SRV-05 e2e)
  - Size: ~300 lines in 3 files
  - [x] DNS server (miekg/dns) on 198.19.0.1 and the resolver addresses: `hetzner.internal` (api -> 198.19.0.1), reverse zone of the public range, forward to `Upstream`
  - [x] nftables forward chain: no guest-to-guest on the public segment except via routing rules of the document

- [x] 01-14 Management API and hlab command
  - Wave: 3 | Depends on: 01-02, 01-03, 01-04 | Review: alone
  - Requirements: PROJ-01, PROJ-02, PROJ-05 | Tests: internal/cli/cli_test.go, test/e2e (PROJ-05)
  - Size: ~400 lines in 5 files
  - [x] `internal/mgmt`: status, projects, tokens, project API proxy
  - [x] `internal/cli` and `cmd/hlab`: `cloud start|stop|status`, `project`, `token`, `version`, `--output json`, exit codes

- [x] 01-15 Cloud lifecycle in hlabd
  - Wave: 4 | Depends on: 01-05, 01-06, 01-07, 01-08, 01-10, 01-11, 01-13, 01-14 | Review: alone
  - Requirements: LIFE-01 | Tests: test/e2e (LIFE-01)
  - Size: ~400 lines in 5 files
  - [x] `internal/cloud`: start (settings, Check, store, switch, appliance image/disk/cidata with agent and secret, VM, hostnet.Up, agent push with DNS upstream, API listener on 198.19.0.2:8780, pipe), stop (API, appliance, hostnet.Down, store)
  - [x] `internal/agentclient`; `cmd/hlabd` service entry, `install`, `uninstall-cleanup`; status file; `scripts/dev-setup.ps1` calls `bin\hlabd.exe install --dev` instead of sc.exe/icacls

- [x] 01-16 SSH keys, servers and IPAM
  - Wave: 5 | Depends on: 01-15 | Review: alone
  - Requirements: API-01, API-10, SRV-01, SRV-03, SRV-04, SRV-05, SRV-16, SRV-19 | Tests: internal/ipam/ipam_test.go, internal/resources/servers/servers_test.go, internal/resources/openapi_test.go, test/e2e
  - Size: ~400 lines in 5 files
  - [x] `internal/ipam`; module `ssh_keys` (create, get, list, delete)
  - [x] module `servers` (create, get, list, delete; IPv4 /32, IPv6 /64; work: image, disk, cidata, VM, NIC, DVD, start, agent push), API-10 table over all endpoints of the phase

- [ ] 01-17 Documentation
  - Wave: 6 | Depends on: all | Review: alone
  - Requirements: DOC-01..DOC-06 | Tests: test/docs/docs_test.go
  - Size: doc-writer
  - [ ] README, commands, configuration, deviations, developer guide (setup with profiles, build-and-test, components, adding-a-resource)
