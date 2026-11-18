# ARCHITECTURE: hlab

## Overview

hlab consists of a Windows service `hlabd` (Go, runs as SYSTEM only while the
cloud runs), a management command `hlab` (Go), a tray program `hlab-tray`
(Go, Wails v3, Svelte), an appliance VM (Debian 13) running the Go program
`hlab-agent`, and an installer. `hlabd` serves the Hetzner API, keeps the
state in SQLite, drives Hyper-V and the host network through a platform
interface, and pushes the complete network configuration (routes,
firewalls, load balancers, DNS, metadata) to `hlab-agent` as one declarative
document. Every guest packet passes the appliance. `hlab` and `hlab-tray`
talk to `hlabd` through a named pipe; when the cloud is stopped they only
read and write the settings file.

```
 host (Windows)                                     appliance VM (Debian 13)
 +----------------------------+                     +---------------------------+
 | hlab-tray --+              |   pipe \\.\pipe\hlab | hlab-agent                |
 | hlab (CLI) -+--> hlabd ----+---- HTTP (transit) -->|  netconf (routes, VRFs)  |
 |                 |  api     |                     |  nftables (fw, isolation) |
 |                 |  store   |                     |  haproxy per LB (netns)   |
 |                 |  platform|---Hyper-V----+      |  dns  metadata  api-proxy |
 |                 |  hostnet |              |      |  worker (images, mkfs)    |
 +----------------------------+              |      +-------------+-------------+
                                             v                    | vSwitch "hlab"
                                     guest VMs (cidata ISO) <-----+ (port ACLs)
```

## Profiles

Every binary is built for one profile, set at link time
(`internal/profile`, `-X ...profile.Name=release|dev`): `task build` builds
`dev`, the release build `release`. The profile gives every name that could
collide on one machine; this document writes the `release` names.

| | `release` | `dev` |
|---|---|---|
| Service | `hlab` | `hlab-dev` |
| Management pipe | `\\.\pipe\hlab` | `\\.\pipe\hlab-dev` |
| Data folder (`%ProgramData%\...`) | `hlab` | `hlab-dev` |
| Shipped catalogs | `%ProgramFiles%\hlab\catalog` | `catalog\` of the working copy (next to `bin\`) |
| Switch, VM name prefix, NAT name, NRPT comment, firewall rule group | `hlab` | `hlab-dev` |

Both profiles use the same public range default, addresses and
`api.hetzner.internal`, so only one cloud runs at a time: at start,
`hostnet.Check` reports `other_instance_running` (fatal) when the service of
the other profile runs. Both may be installed side by side.

## Components

### hlabd (Windows service)

- Purpose: the cloud. Runs as `LocalSystem`, start type manual, service name
  `hlab`, started and stopped only by `hlab-tray` or `hlab cloud start|stop`
  through the SCM.
- Interface:
  - Hetzner API on `198.19.0.2:8780` (transit address, see "Network"),
    reached by clients as `http://api.hetzner.internal/v1` through the
    agent's API proxy.
  - Management API: HTTP over the named pipe `\\.\pipe\hlab` (ACL: SYSTEM,
    Administrators, Interactive Users `IU`), see "Management API".
  - `hlabd install [--dev]` and `hlabd uninstall-cleanup [--keep-data]`:
    the one place that registers the service with its security descriptor
    (`IU`: query, start, stop; never change-config), creates
    `%ProgramData%\hlab` with its ACLs, and removes everything again. Used by
    the installer's custom actions and by `scripts/dev-setup.ps1`.
  - Files, see "Files and formats".
- Depends on: store, resources, actions, platform, hostnet, agent client,
  images, ca.

Inside `hlabd` (Go packages under `internal/`):

#### api

- Purpose: HTTP layer shared by all resource types.
- Interface: `api.Router` mounts every registered `resource.Module`.
  Middleware, in order: request log, token auth (`Authorization: Bearer`,
  SHA-256 hash lookup, sets `Project` and `ReadOnly` in the context), JSON
  decoding (`json_error`), read-only guard (`token_readonly` for every
  non-GET), handler. Shared helpers: `api.List(w, key, items)` writes a
  list with `meta.pagination` (phase 02 extends it with the request for
  `name`, `label_selector`, `sort`, `page` and `per_page`); modules reach
  store and action manager through `api.StoreFrom(ctx)`/`api.ActionsFrom(ctx)`; `api.Error(code, message, details)` writes Hetzner's
  error object; `labels.Validate`, `labels.Selector` (parser and matcher for
  all Hetzner operators).
- Depends on: store, resource, actions.

#### resource (one module per resource type)

- Purpose: one Go package per Hetzner resource type under
  `internal/resources/<type>` (servers, server_types, images, isos,
  locations, datacenters, ssh_keys, primary_ips, floating_ips, networks,
  firewalls, load_balancers, load_balancer_types, certificates, volumes,
  placement_groups, zones, pricing, actions). A new resource type is a new
  package plus one line in `internal/resources/all.go`; the core does not
  change.
- Interface:

  ```go
  type Module interface {
      Type() string                     // "servers"
      Routes(r resource.Registrar)      // registers its endpoints and actions (Handle(pattern, handler), ServeMux patterns under /v1)
  }
  // optional interfaces, detected by type assertion:
  type Reconciler interface {           // modules owning real resources
      Reconcile(ctx context.Context, id int64) error   // make reality match state
      Delete(ctx context.Context, id int64) error
  }
  type NetContributor interface {       // modules contributing to the agent document
      Contribute(tx store.Tx, doc *agentapi.Config) error
  }
  ```

  Handlers validate input, write the desired state in one transaction,
  create an action and enqueue the work; they never call Hyper-V directly.
- Depends on: api, store, actions, platform (through the action worker),
  agentapi.

#### store

- Purpose: the persistent state, one SQLite file.
- Interface: `store.Open(path) (*Store, error)`; `Store.Tx(func(tx Tx) error)`;
  generic typed access `store.Get[T]`, `store.List[T]`, `store.Put[T]`,
  `store.Delete` over the schema below; `store.NextID(kind)`.
- Schema (version 1, see "Data schema").
- Depends on: nothing.

#### actions

- Purpose: Hetzner actions, resource locks and the work queue.
- Interface: `actions.Start(tx, command string, resources []Ref) (*Action,
  error)` returns `locked` if a referenced resource has a running action
  that locks it; `actions.Run(id, func(ctx, progress func(int)) error)` runs
  the work in a worker pool and sets `success` or `error` with Hetzner's
  error object. Actions survive a stop: running actions are marked `error`
  with code `server_error` at the next start.
- Depends on: store.

#### platform (interface) and platform/hyperv

- Purpose: everything about VMs and disks that depends on the hypervisor.
  The only package that knows Hyper-V; a later `platform/libvirt`
  implements the same interface.
- Interface:

  ```go
  type Platform interface {
      EnsureSwitch(ctx, spec SwitchSpec) error
      CreateVM(ctx, spec VMSpec) error            // name "hlab-<kind>-<id>", Gen2, Secure Boot template from spec
      SetVM(ctx, name string, spec VMSpec) error  // cpu, ram (stopped only)
      DeleteVM(ctx, name string) error
      Power(ctx, name string, op PowerOp) error   // Start, Stop(hard), Shutdown and Reboot (Shutdown integration service), Reset, Save
      State(ctx, name string) (VMState, error)
      AttachNIC(ctx, vm string, nic NICSpec) error // MAC, segment (public PVLAN or private VLAN), MAC deny-list
      DetachNIC(ctx, vm, mac string) error
      CreateDisk(ctx, path string, sizeBytes int64, from string, page83 [16]byte) error // thin VHDX, optional copy of base
      ResizeDisk(ctx, path string, sizeBytes int64) error
      AttachDisk(ctx, vm, path string, lun int) error   // hot-add on SCSI controller 0
      DetachDisk(ctx, vm, path string) error
      SetDVD(ctx, vm string, slot int, iso string) error // DVD drives on SCSI controller 1: slot 0 cidata, slot 1 user ISO; "" ejects
      SetBootOrder(ctx, vm string, order []BootDevice) error
      Snapshot(ctx, vm, diskPath, target string) error  // crash-consistent copy of a running or stopped disk
      Metrics(ctx, vm string) (Sample, error)            // cpu %, disk bytes, net bytes
      HostResources(ctx) (Resources, error)              // RAM, logical cores, free disk in data folder
  }

  type SwitchSpec struct { Name string; PVLANPrimary, PVLANSecondary int } // internal switch; host adapter isolated
  type VMSpec struct {
      Name        string // "hlab-server-<id>", "hlab-appliance"
      CPUs        int
      MemoryMB    int64  // static
      Folder      string // VM configuration folder in the data folder
      StopAction  StopAction // StopSave | StopShutdown
  }
  type PowerOp int   // PowerStart, PowerStop (hard), PowerShutdown, PowerReboot, PowerReset, PowerSave
  type StopAction int
  type VMState int   // StateOff, StateStarting, StateRunning, StateStopping, StateSaved, StatePaused, StateOther
  type Segment struct { PublicPVLAN, Promiscuous bool; VLAN int } // public: PVLAN isolated port (Promiscuous: the appliance's port); private: access VLAN 2..4092
  type NICSpec struct {
      MAC     net.HardwareAddr
      Segment Segment
      DenyMACs []net.HardwareAddr // other members of the same private network
  }
  type BootDevice struct { Kind string; Slot int } // Kind "disk" (Slot = LUN) or "dvd" (Slot = 0|1)
  type Sample struct { Time time.Time; CPUPercent float64; DiskReadBytes, DiskWriteBytes, NetRxBytes, NetTxBytes uint64 } // counters cumulative
  type Resources struct { MemoryMB, FreeMemoryMB int64; LogicalCPUs int; FreeDiskBytes int64 }
  ```

  Errors are `platform.Error{Kind, Op, Err}` with Kind `NotFound`,
  `InvalidState`, `NoResources`, `Failed`; the action worker maps them to
  Hetzner codes. `SegmentFor(location string, networkID int64) Segment` is
  the one place deciding the segment of an adapter (`decisions/locations.md`):
  public adapters always the public PVLAN, private adapters the network's
  VLAN.
- Implementation: one long-running Windows PowerShell 5.1 process per
  hlabd, fed commands on stdin and answering JSON (`ConvertTo-Json`) on
  stdout, serialized by a mutex; restarted if it dies. VHDX files whose
  Page 83 identifier matters (volumes) are created through the
  `CreateVirtualDisk` API (`UniqueId`) via `github.com/Microsoft/go-winio/vhd`.
  Every disk copied or differenced from a base gets a new identifier
  (`Set-VHD -ResetDiskIdentifier`), since copies inherit the parent's.
  VMs are created Gen2, Secure Boot template
  `MicrosoftUEFICertificateAuthority`, static memory, automatic
  checkpoints off, `AutomaticStartAction Nothing`, `AutomaticStopAction`
  matching `stop_behaviour` (Save or ShutDown). The code never parses
  PowerShell text (messages and names are localized); it uses IDs, enums
  and JSON (Shutdown integration service ID
  `9F8233AC-BE49-4C79-8EE3-E7E1985B2077`).
- Depends on: Hyper-V PowerShell module.

#### hostnet (interface) and hostnet/windows

- Purpose: everything on the Windows host outside Hyper-V: host IP on the
  hlab adapter, routes, IP forwarding, outbound NAT, NRPT rules, WSL2
  detection and its forwarding, collision check, CA trust store.
- Interface:

  ```go
  type HostNet interface {
      Check(ctx, cfg Config) []Problem      // range collision, WSL mode, dnsTunneling
      Up(ctx, cfg Config) error             // adapter IP, route, forwarding, NAT, base NRPT rules
      SetZones(ctx, zones []string) error   // exactly one NRPT rule per zone, idempotent
      Down(ctx) error                        // removes everything Up and SetZones added
      WatchWSL(ctx, onChange func(WSLInfo)) // re-applies forwarding when WSL restarts
      TrustCA(ctx, pem []byte, on bool) error // Windows store and WSL2 distros
  }

  type Config struct {
      PublicRange   netip.Prefix   // from settings
      HostAddr      netip.Prefix   // 198.19.0.2/30
      ApplianceAddr netip.Addr     // 198.19.0.1
      SwitchName    string         // "hlab"
      BaseZones     []string       // "hetzner.internal", reverse zones of PublicRange
  }
  type Problem struct {
      Code    string // "range_collision", "winnat_exists", "other_instance_running", "wsl_mirrored", "wsl_virtioproxy", "wsl_dns_tunneling_off", "hyperv_missing"
      Message string // English, shown as is
      Fatal   bool   // true: the cloud does not start
  }
  type WSLInfo struct { Present bool; Mode string; DNSTunneling bool; AdapterAlias string; Subnet netip.Prefix }
  ```

  Every object hostnet creates is tagged (NRPT rule `Comment` = `hlab`, NAT
  name `hlab`, firewall rule group `hlab`, route via the hlab adapter) so
  `Down` and the uninstaller find all of them without the state file.
  Before changing a host setting it does not own (IPv4 forwarding on the
  WSL and hlab adapters), hostnet writes the previous value to
  `<data folder root>\hostnet-restore.json`; `Up`, `Down` and
  `hlabd uninstall-cleanup` first restore what that file records and then
  delete it, so a crash leaves nothing changed after the next start.
  `Up` also returns the host's DNS servers (those of adapters with a
  default route, without the hlab, WSL and loopback adapters) for the agent
  document.
- Depends on: Windows networking (see SPEC.md).

#### agent client

- Purpose: build the agent document and push it.
- Interface: `agentclient.Push(ctx, cfg agentapi.Config) error` (PUT, full
  document, monotonically increasing `generation`), `Status(ctx)` (applied
  generation, LB target health, LB metrics), `Job(ctx, job agentapi.Job)`
  for worker jobs. Every state change that touches the network triggers a
  rebuild of the full document from the store and a push; the agent is
  stateless across reboots.
- Depends on: agentapi, store, resource modules (`NetContributor`).

#### images

- Purpose: system images, ISOs, the rescue disk and the disk store.
- Interface: `images.Ensure(ctx, imageID) (basePath string, err error)`
  downloads on the host, verifies the checksum, converts once (qcow2 via
  `go-qcow2reader` or raw, to a fixed VHD by appending the footer, then to
  a dynamic VHDX through the platform) and caches; `images.EnsureISO(ctx,
  isoID)`; `images.EnsureRescue(ctx)`. Snapshots, backups and system
  images live as VHDX files in the data folder. The same pipeline creates
  the appliance disk from the Debian 13 `generic` image.
- Depends on: platform.

#### cidata

- Purpose: per-server NoCloud seed ISO (`meta-data`, `user-data`,
  `vendor-data`, `network-config` v2) written in Go (`go-diskfs`, ISO 9660
  with Rock Ridge, volume label `cidata`).
- Interface: `cidata.Build(server, keys, networks, rootPassword) ([]byte,
  error)`. `network-config`: public adapter `/32` with on-link default route
  via `172.31.1.1`, nameservers `185.12.64.1` and `185.12.64.2`; one adapter
  per private network with its `/32` and a route to the network's range via
  the network gateway, like Hetzner. `vendor-data` installs the guest hooks
  (see "Guest integration").
- Depends on: nothing.

#### scheduler

- Purpose: time-driven work while the cloud runs: daily backups (one per
  server per day, 7 kept), managed certificate renewal (30 days before
  expiry), LB metrics sampling, server metrics sampling.
- Interface: internal; uses a clock interface so tests use a fake clock.
- Depends on: actions, store.

#### ca

- Purpose: the hlab CA for managed certificates
  (`decisions/managed-certificates.md`).
- Interface: `ca.Issue(domains []string) (certPEM, keyPEM, error)`,
  `ca.Certificate() []byte`. Key at `%ProgramData%\hlab\ca\ca.key`, ACL
  SYSTEM only.

#### system integration (interface `sysint`, implementation `sysint/windows`)

- Purpose: everything about the host operating system that is not
  networking or the hypervisor, behind one interface, so a Linux host
  replaces only the implementation.
- Interface:

  ```go
  type Paths struct { Config, State, Logs, CA, Catalog, Programs string } // Windows: %ProgramData%\hlab\..., %ProgramFiles%\hlab
  type ServiceControl interface {           // used by hlab and hlab-tray
      Start(ctx context.Context, params ...string) error // minimal rights (E10)
      Stop(ctx context.Context) error
      State(ctx context.Context) (string, error)         // "stopped" | "starting" | "running" | "stopping"
  }
  type ServiceHost interface {              // used by hlabd
      Run(start func(ctx context.Context, params []string) error, stop func(ctx context.Context, reason StopReason) error) error
  }                                          // StopReason: StopRequested | StopHostShutdown
  type Installer interface {                // hlabd install / uninstall-cleanup
      Install(dev bool) error
      Uninstall(keepData bool) error
  }
  // these two live in the implementation package (sysint/windows):
  func ListenManagement() (net.Listener, error) // Windows: \\.\pipe\hlab with ACL
  func DialManagement(ctx context.Context) (net.Conn, error)
  ```

  Windows: SCM with the descriptor from DECISIONS.md, preshutdown,
  named pipe via go-winio. Linux later: systemd unit, polkit rule or group
  for start/stop, Unix socket, `/etc/hlab` and `/var/lib/hlab`.

### hlab (management command)

- Purpose: projects, tokens, cloud start and stop, status, catalog update
  for users and scripts.
- Interface (stable, scripts use it):

  ```
  hlab cloud start | stop | status
  hlab project create <name> | list | delete <name>
  hlab token create --project <name> --name <name> [--read-only]   # prints the token once
  hlab token list --project <name> | revoke --project <name> <name>
  hlab catalog update [--hetzner-token-env <VAR>]
  hlab logs [--follow]
  hlab ca export <file>
  hlab version
  ```

  Output is human-readable; `--output json` gives JSON for every command.
  Exit code 0 on success, 1 on error, 2 on usage error. `cloud start|stop`
  use the service controller (see "System integration"); all other commands
  use the management API and fail with "the cloud is not running" while it
  is stopped, except `logs`, which then reads the log file.
- `catalog update`: the token comes from the environment variable named by
  `--hetzner-token-env`, else from a hidden prompt on a terminal; with
  neither, the command fails with exit code 2 and says how to pass it. It
  is sent once in `POST /mgmt/v1/catalog/update`; hlabd fetches the server
  types from `https://api.hetzner.cloud/v1` with it, writes the catalog and
  discards the token. No component stores or logs it, and no component of
  the product calls hcloud.
- Incompatible state (LIFE-08): hlabd refuses to start, writes the status
  file `%ProgramData%\hlab\status.json` (`{"state":"refused","problem":
  {"code":"state_incompatible","message":...}}`) and stops. `hlab cloud
  status` and the tray show the message; `hlab cloud start --discard-state`
  (tray: confirm dialog) renames `state.db` to `state.db.discarded-<time>`
  through the start parameter `discard-state` and starts with an empty
  state; VMs and disks of the discarded state are deleted by the tagged
  cleanup of `hlabd uninstall-cleanup --vms-only`.
- Depends on: management API.

### hlab-tray (tray program)

- Purpose: tray icon and console window like Docker Desktop.
- Interface: Wails v3 application. Go side: `tray` (icon, menu, start/stop
  through SCM), `bridge` (Wails-bound service with one method
  `Call(method, path string, body []byte) (status int, body []byte)` that
  forwards to the management API, plus `Settings()`/`SaveSettings()` for
  the file while the cloud is stopped). Frontend: Svelte SPA in
  `ui/frontend`, which uses only the bridge; the Hetzner resources are
  managed through the management API's project proxy
  (`/mgmt/v1/projects/{name}/api/v1/...`), so the UI exercises the same
  handlers as the clients. Views per UI-SPEC.md.
- Start and stop through the SCM open the manager with `SC_MANAGER_CONNECT`
  and the service with `SERVICE_START|SERVICE_STOP|SERVICE_QUERY_STATUS`
  only (raw `windows.OpenSCManager`/`OpenService`; `svc/mgr.Connect`
  requests full access and fails for non-admins).
- UI tests: Playwright Test connects over CDP to the real tray window of a
  build with tag `e2e`, which passes `--remote-debugging-port` (127.0.0.1)
  and a per-run user data folder from the harness's environment variables
  to Wails' `AdditionalBrowserArgs`/`WebviewUserDataPath` (Wails clears the
  WebView2 environment variables, E9). Release builds never carry the tag
  (DECISIONS.md "Tray UI tests through a CDP port in an e2e-only build").
- Depends on: management API, settings file.

### Management API (named pipe)

HTTP/1.1 over `\\.\pipe\hlab`, JSON. All paths under `/mgmt/v1`:

| Method and path | Purpose |
|---|---|
| `GET /status` | `{"state":"running","version":"...","problems":[Problem],"shadowing_zones":[{"zone":"example.com","project":"p"}],"appliance_generation":n}` |
| `GET /projects` | `{"projects":[{"name":"p","created":"RFC3339","resources":n}]}` |
| `POST /projects` | body `{"name":"p"}` (1-63 chars, `[a-z0-9-]`); `201` with the project; `409` if it exists |
| `DELETE /projects/{name}` | `202 {"action_id":n}`; deletes all resources, then the project |
| `GET /projects/{name}/tokens` | `{"tokens":[{"name":"t","read_only":false,"created":"RFC3339"}]}` (never the value) |
| `POST /projects/{name}/tokens` | body `{"name":"t","read_only":false}`; `201 {"name":"t","read_only":false,"token":"<64 chars [A-Za-z0-9]>"}`, the only time the value is returned |
| `DELETE /projects/{name}/tokens/{token-name}` | `204` |

Errors: `{"error":{"code":"...","message":"..."}}` with codes
`invalid_input`, `not_found`, `conflict`, `cloud_not_running`,
`server_error`.
| `ANY /projects/{name}/api/v1/{rest}` | forwards to the Hetzner API handlers with that project and read/write rights |
| `POST /catalog/update` | body `{"hetzner_token": "..."}` (required, used once, never stored or logged) |
| `GET /logs?follow=1` | log lines as newline-delimited JSON |
| `GET /ca.pem` | the hlab CA certificate |
| `PUT /trust` | `{"on": bool}` |
| `POST /settings/reload` | re-read the settings file |

### hlab-agent (appliance)

- Purpose: everything that needs a Linux network stack: routing, VRFs for
  private networks, nftables (port isolation backstop, firewalls, floating
  IP routes), HAProxy per load balancer, DNS server, metadata service, API
  proxy, and the worker for jobs that need Linux tools.
- Interface: HTTP on `198.19.0.1:9100` (transit only), header
  `Authorization: Bearer <agent secret>` (random, generated by hlabd at
  appliance creation, passed in the appliance's cidata):

  | Method and path | Purpose |
  |---|---|
  | `PUT /v1/config` | full `agentapi.Config`, applied idempotently; returns the applied generation |
  | `GET /v1/status` | applied generation, per LB target health, LB metrics counters |
  | `POST /v1/jobs` | `agentapi.Job`: `mkfs` (LUN, ext4/xfs; formats a new volume attached to the appliance), `rescue-prepare` (LUN; gives the attached copy of the Debian image new partition and filesystem UUIDs and labels and points its boot configuration at them), returns when done |

  `agentapi.Config` (Go package `internal/agentapi`, shared by both sides,
  versioned by `schema: 1`):

  ```go
  type Config struct {
      Schema, Generation int
      PublicRange  netip.Prefix          // e.g. 198.19.0.0/16
      Servers      []Server              // public IPv4, MAC, location, firewall IDs
      FloatingIPs  []FloatingIP          // IP -> server public IPv4
      Networks     []Network             // ID, VLAN, range, gateway, subnets, routes, members (IP, aliases, MAC)
      Firewalls    []Firewall            // ID, rules in Hetzner's shape
      LoadBalancers []LoadBalancer       // ID, public IP, private IPs, services, targets resolved to IPs
      Zones        []Zone                // name, TTL, RRSets in Hetzner's shape
      PTR          map[netip.Addr]string
      Metadata     []Metadata
      API          APIProxy              // listen 198.19.0.1:80, backend 198.19.0.2:8780
      Upstream     []netip.AddrPort      // DNS forwarders: the host's DNS servers, else 1.1.1.1:53, 9.9.9.9:53
  }

  type Server struct {
      ID          int64
      Location    string               // "fsn1"
      MAC         string               // public adapter, "02:48:4c:..."
      PublicIPv4  netip.Addr           // invalid if the server has no public IPv4
      FirewallIDs []int64              // applied firewalls, label selectors already resolved
  }
  type FloatingIP struct { IP netip.Addr; Target netip.Addr } // Target: the server's public IPv4; unassigned: invalid
  type Network struct {
      ID       int64
      VLAN     int
      IPRange  netip.Prefix
      Gateway  netip.Addr                 // first address of IPRange
      Routes   []Route
      Members  []Member
  }
  type Route struct { Destination netip.Prefix; Gateway netip.Addr }
  type Member struct { ServerID int64; MAC string; IP netip.Addr; AliasIPs []netip.Addr }
  type Firewall struct { ID int64; Rules []Rule }
  type Rule struct {                          // Hetzner's firewall rule
      Direction       string                // "in" | "out"
      Protocol        string                // "tcp" | "udp" | "icmp" | "esp" | "gre"
      Port            string                // "80", "1000-2000", "any", "" for icmp/esp/gre
      SourceIPs       []netip.Prefix        // direction in
      DestinationIPs  []netip.Prefix        // direction out
  }
  type LoadBalancer struct {
      ID         int64
      PublicIPv4 netip.Addr                 // invalid if the public interface is disabled
      Private    []LBNetwork                // one per attached network
      Algorithm  string                     // "round_robin" | "least_connections"
      Services   []LBService
      Targets    []LBTarget                 // resolved from servers, label selectors, IPs
  }
  type LBNetwork struct { NetworkID int64; VLAN int; IP netip.Addr }
  type LBService struct {
      Protocol        string                // "tcp" | "http" | "https"
      ListenPort, DestinationPort int
      ProxyProtocol   bool
      HTTP            *LBHTTP               // nil for tcp
      HealthCheck     LBHealthCheck
  }
  type LBHTTP struct { CookieName string; CookieLifetime int; StickySessions, RedirectHTTP bool; CertPEM, KeyPEM []string }
  type LBHealthCheck struct { Protocol string; Port, Interval, Timeout, Retries int; Path, Domain, Response string; StatusCodes []string; TLS bool }
  type LBTarget struct { Key string; IP netip.Addr; ViaNetworkID int64 } // Key: "server:<id>" or "ip:<addr>"; ViaNetworkID 0 = public
  type Zone struct { Name string; TTL int; RRSets []RRSet }
  type RRSet struct { Name string; Type string; TTL int; Records []string } // Hetzner's shape, Name relative ("@", "www")
  type Metadata struct {
      VLAN      int                         // 0 = public segment
      IP        netip.Addr                  // source address of the asking server
      ServerID  int64
      Hostname, Region, AvailabilityZone, PublicIPv4 string
      PublicKeys []string
      UserData  string
      PrivateNetworks []MetadataNetwork
      PasswordReset *PasswordReset           // pending root password reset, or nil
  }
  type MetadataNetwork struct { IP netip.Addr; AliasIPs []netip.Addr; MAC string; NetworkID int64; NetworkName string; Subnet netip.Prefix; Gateway netip.Addr }
  type PasswordReset struct { ID int64; SHA512Crypt string } // crypt(3) hash, never the clear text
  type APIProxy struct { Listen netip.AddrPort; Backend netip.AddrPort }

  type Job struct {
      Kind string // "mkfs" | "rescue-prepare"
      LUN  int    // disk attached to the appliance's SCSI controller 0
      FS   string // "ext4" | "xfs" (mkfs)
  }
  type JobResult struct { OK bool; Message string }
  type Status struct {
      Generation int
      Targets    map[int64][]TargetHealth   // per load balancer
      LBMetrics  map[int64]LBCounters
  }
  type TargetHealth struct { Key string; ListenPort int; Status string } // "healthy" | "unhealthy" | "unknown"
  type LBCounters struct { OpenConnections, ConnectionsTotal, RequestsTotal, BytesIn, BytesOut uint64 }
  ```

  Metadata service (agent, on `169.254.169.254:80`, answers by source
  address and segment): Hetzner's paths under `/hetzner/v1/metadata`
  (`/hostname`, `/instance-id`, `/public-ipv4`, `/private-networks`,
  `/region`, `/availability-zone`, `/public-keys`, `/userdata`, and the full
  YAML document at `/hetzner/v1/metadata`). hlab's own paths for the guest
  hook: `GET /hlab/v1/password-reset` returns `204` or `200
  {"id":n,"sha512_crypt":"$6$..."}`; `POST /hlab/v1/password-reset/{id}/done`
  confirms, after which hlabd ends the action with `success` (no
  confirmation within 120 s: `error`, code `server_error`).
- Depends on: nothing on the host; the Debian packages listed in SPEC.md.

### Guest integration

- cidata ISO in DVD slot 0 and user ISO in DVD slot 1 (SCSI controller 1), server disk at SCSI controller 0 LUN 0,
  volumes from LUN 1 upward.
- `vendor-data` installs two hooks into every server created from an hlab
  image: a udev rule that creates `/dev/disk/by-id/scsi-0HC_Volume_<id>`
  from the disk's Page 83 identifier (hlab creates volume disks with the identifier `484C4142-0000-4000-8000-<volume ID as 12 hex digits>`; the last 6 bytes survive in both the T10 and the NAA designator the guest sees), and
  `hlab-guest.timer`, which asks the metadata service every 5 seconds for a
  pending root password reset and applies it with `chpasswd`. Servers whose
  disks were not created by hlab (ISO installs) lack the hooks; listed as a
  deviation.
- Volumes with `automount` are mounted by a `user-data` part merged by
  cloud-init at creation, or by the udev rule plus a systemd mount unit when
  attached later.
- Rescue: hlab keeps one rescue base disk (Debian 13 `generic`, prepared
  once by the agent job `rescue-prepare` so that no UUID or label matches
  any image). Enable rescue creates a differencing disk from it at SCSI
  LUN 63 with its own cidata (root password, SSH keys) and puts it first in
  the boot order; the server disk stays at LUN 0 (`/dev/sda`). The rescue
  disk is discarded when rescue ends (the next boot after
  `disable_rescue`, or a reset).

### Installer

- Purpose: install, update and uninstall.
- Interface: WiX bundle `hlab-setup-<version>.exe` chaining the WebView2
  Evergreen bootstrapper (detected by its `pv` registry value) and
  `hlab-<version>.msi`. The MSI installs to `%ProgramFiles%\hlab`, runs
  `hlabd install` as a deferred elevated custom action (service, security
  descriptor, `%ProgramData%\hlab` and ACLs), checks Hyper-V first (a custom
  action names what is missing), and creates the shortcuts. Uninstall runs `hlabd uninstall-cleanup
  [--keep-data]` as a deferred custom action, which removes every
  Hyper-V and host object tagged `hlab` (see hostnet) even without a
  readable state, then the files.

## Network

All addresses below derive from the configured public range
(`network.public_range`, default `198.19.0.0/16`).

- Virtual switch `hlab` (internal). The host adapter `vEthernet (hlab)` has
  `198.19.0.2/30`; the appliance's first adapter has `198.19.0.1/30` plus
  `172.31.1.1/32` (the guests' gateway, as at Hetzner), `169.254.169.254/32`
  (metadata) and `185.12.64.1/32`, `185.12.64.2/32` (resolvers, as at
  Hetzner).
- Host route: public range via `198.19.0.1`. NRPT: `.hetzner.internal`, the
  reverse zone(s) of the public range and every user zone point to
  `198.19.0.1`. `api.hetzner.internal` resolves to `198.19.0.1`; the agent
  proxies port 80 to `198.19.0.2:8780`, so hlabd never needs port 80 on the
  host.
- Servers get public IPv4 addresses from `.4` upward as `/32` with on-link
  gateway `172.31.1.1`. Allocation continues after the last address handed
  out (counter in `kv`) and wraps to `.4` only at the end of the range,
  skipping addresses in use, so a freed address is not reused at once (SSH
  host key conflicts).
- hlabd listens on `198.19.0.2:8780` only; one inbound Windows Firewall rule
  (group `hlab`) allows TCP 8780 for all firewall profiles (the internal
  adapter is an unidentified network in profile Public), restricted to
  interface `vEthernet (hlab)`, local address `198.19.0.2`, remote address
  `198.19.0.1` and the program `hlabd.exe` of the running profile. `Up`
  removes any rule of the group first and creates it again, so a crash
  leaves no duplicate or stale rule. Nothing listens on the host's port 80.
- The agent's DNS server answers hlab zones itself and forwards everything
  else to `Config.Upstream`. The appliance routes each `/32` to the server's
  adapter. Floating IPs are `/32` routes to the server's public IPv4.
- Port isolation on the public segment by private VLAN: the appliance's
  public adapter is promiscuous (primary VLAN 4094, secondary 4093), the
  host adapter (`-ManagementOS`) and every guest public adapter are
  isolated (primary 4094, secondary 4093). Guests reach only the appliance
  and the host only the appliance (E1). Private adapters get MAC deny ACLs
  for the other members of their network, updated when members change (an
  allow-appliance-deny-`ANY` ACL drops legitimate traffic, E1). All guest
  adapters: `-MacAddressSpoofing Off`, `-RouterGuard On`, `-DhcpGuard On`.
- Private networks: each network has a VLAN ID (allocated from 2..4092) and
  a VRF on the appliance (a VLAN subinterface on the appliance's second,
  trunk adapter, gateway = first address of the network range). Ranges of
  different projects may overlap. A server's private adapter is an access
  port in that VLAN.
- Load balancers: one network namespace and one HAProxy process per load
  balancer on the appliance, with its public IPv4 (routed `/32`) and one
  interface per attached network's VRF.
- Firewalls: nftables in the appliance's forward chain, per server public
  IPv4 and its floating IPs; private-network traffic is not filtered.
- Outbound: the appliance's default route is `198.19.0.2`; the host NATs the
  public range with WinNAT (`New-NetNat -Name hlab
  -InternalIPInterfaceAddressPrefix <public range>`); forwarding on the hlab
  adapter is not needed for it (E3). Windows allows one WinNAT instance; if
  another exists, `hostnet.Check` reports it. The host route is created in
  `ActiveStore` (gone after a reboot); NAT and NRPT rules persist and are
  removed by `Down` and the uninstaller.
- WSL2 (NAT mode): hostnet enables IPv4 forwarding on `vEthernet (WSL*)`
  and `vEthernet (hlab)`; traffic from the WSL subnet reaches the servers
  with its WSL source address, replies return via the appliance's default
  route; NRPT applies inside WSL with dnsTunneling (E4). hostnet records the
  previous forwarding state and restores it in `Down`, and re-applies
  forwarding when WSL recreates its adapter. WSL's fallback mode
  (VirtioProxy) and mirrored mode are detected with `wslinfo
  --networking-mode` inside the default distro.
- NRPT: one suffix rule per zone (`.example.net`) also covers the zone
  apex (E5).

## Data schema

SQLite file `%ProgramData%\hlab\state.db`, `PRAGMA user_version` = schema
version (1). Resources are stored as JSON documents so that adding a field is
no migration:

```sql
CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT UNIQUE NOT NULL, created TEXT NOT NULL);
CREATE TABLE tokens   (id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL REFERENCES projects ON DELETE CASCADE,
                       name TEXT NOT NULL, sha256 BLOB UNIQUE NOT NULL, read_only INTEGER NOT NULL, created TEXT NOT NULL,
                       UNIQUE(project_id, name));
CREATE TABLE resources (kind TEXT NOT NULL, id INTEGER NOT NULL, project_id INTEGER REFERENCES projects ON DELETE CASCADE,
                       name TEXT, labels TEXT NOT NULL DEFAULT '{}', created TEXT NOT NULL, doc TEXT NOT NULL,
                       PRIMARY KEY(kind, id));
CREATE INDEX resources_project ON resources(project_id, kind);
CREATE TABLE actions  (id INTEGER PRIMARY KEY, project_id INTEGER, command TEXT NOT NULL, status TEXT NOT NULL,
                       progress INTEGER NOT NULL, started TEXT NOT NULL, finished TEXT, error TEXT, resources TEXT NOT NULL);
CREATE TABLE ids      (kind TEXT PRIMARY KEY, next INTEGER NOT NULL);
CREATE TABLE kv       (key TEXT PRIMARY KEY, value TEXT NOT NULL);   -- allocations: IPs, VLANs, appliance secret
```

- `doc` holds the resource in Hetzner's API shape plus an `_hlab` object for
  internal fields (VM name, disk paths, VLAN, MACs). IDs are global per
  kind across projects, like Hetzner. Catalog kinds (locations, server
  types, images of type system, ISOs) have `project_id` NULL.
- An incompatible `user_version` (newer than the binary, or older without a
  migration) leads to the warning of LIFE-08; until a 1.0 the user may
  discard the state.

## Files and formats

| Path | Format | Writer |
|---|---|---|
| `%ProgramData%\hlab\settings.toml` | TOML, keys in "Settings" below; ACL: Interactive Users modify | tray, user |
| `%ProgramData%\hlab\server-types.custom.json` | JSON array of Hetzner `server_type` objects (IDs from 100000) | tray, user |
| `%ProgramFiles%\hlab\catalog\server-types.json` | `{"server_types": [...]}` as returned by Hetzner's API | `task catalog` (release) |
| `%ProgramData%\hlab\catalog\server-types.json` | same; written by `hlab catalog update`, preferred when present | hlabd |
| `%ProgramFiles%\hlab\catalog\{images,isos,locations,load-balancer-types,pricing}.json` | Hetzner API shapes plus `_hlab.source` (URL, checksum URL, format); locations, load balancer types and pricing from `task catalog`, images and ISOs by hand | release |
| `%ProgramData%\hlab\state.db` | SQLite, see "Data schema" | hlabd |
| `%ProgramData%\hlab\logs\hlabd.log` | JSON lines (`time`, `level`, `msg`, fields), 10 MB x 5 rotated | hlabd |
| `%ProgramData%\hlab\ca\` | `ca.pem`, `ca.key` (SYSTEM only) | hlabd |
| `<data folder>\{disks,volumes,images,isos,cache,appliance}\` | VHDX, ISO | hlabd |

Settings (`settings.toml`), all optional with defaults:

```toml
[cloud]
stop_behaviour = "save"          # "save" | "shutdown"
[network]
public_range = "198.19.0.0/16"
[storage]
data_folder = 'C:\ProgramData\hlab\data'
[resources]
reserve_ram_mb = 4096
reserve_disk_gb = 50
[certificates]
trust_ca = false
[logging]
level = "info"
```

## Cloud start and stop

- Start (`hlabd` service start): read settings, `hostnet.Check` (abort on a
  problem with a status message), open store, create switch and appliance
  if missing, start appliance, `hostnet.Up`, push agent document, restore
  servers (resume saved ones; power on those marked running), start API and
  scheduler, set status `running`.
- Stop (SCM stop or `SERVICE_CONTROL_PRESHUTDOWN` at Windows shutdown):
  stop API, record which servers ran, then per `stop_behaviour` save or
  shut down every running VM (Shutdown integration service, 60 s, then
  hard off), stop the appliance, `hostnet.Down`, close store. At Windows
  shutdown `vmms` runs before hlabd in `PreshutdownOrder` and already
  applies each VM's `AutomaticStopAction` (set to match `stop_behaviour`);
  hlabd's preshutdown handler records the state and runs `hostnet.Down`.
  hlabd sets its preshutdown timeout to 180 s
  (`SERVICE_CONFIG_PRESHUTDOWN_INFO`).

## Extension to the target vision

- Fault injection: a new resource-like module `faults` in the management API
  that adds entries to `agentapi.Config` (netem and drop rules on the
  appliance, which already sees all traffic), an API middleware for API
  errors and rate limits, and a decorator around `platform.Platform` for
  server and storage failures. No core change.
- Linux host with KVM/libvirt: implement `platform.Platform` with libvirt,
  `hostnet.HostNet` with Linux routes and systemd-resolved instead of NRPT,
  `sysint` with systemd, a Unix socket and Linux paths, and build the tray
  with Wails on Linux (its start/stop goes through `sysint.ServiceControl`);
  the API, store, resource modules, cidata, images (VHDX becomes qcow2 inside
  `platform`) and the appliance stay unchanged. Every `%ProgramData%` path in
  this document is `sysint.Paths` on Windows.
- Storage Box (v2): a second HTTP listener for the Hetzner API base address
  under `.internal` with its own module set; the agent's API proxy gets a
  second name. The Storage Box itself can be a VM or a Samba/SFTP service on
  the appliance.
- Server console: a new component (RFB-over-WebSocket bridge) behind
  `request_console`, using `Platform` extensions for framebuffer and input;
  the endpoint already exists and returns `unsupported_error` until then.
- WSL2 mirrored mode: a second implementation path inside `hostnet` chosen
  by `WatchWSL`; the appliance and the API do not change.
- More images: new entries in `catalog/images.json` with their sources; the
  image pipeline (download on the host, convert, cache) is generic.

## Verified assumptions

- Guests isolated from each other on the public segment, host and WSL2 still reach the appliance: confirmed with a private VLAN, refuted with allow/deny-ANY MAC ACLs (E1, E3).
- Ubuntu cloud image (qcow2) and Debian 13 generic (raw) boot on Gen2 with Secure Boot (Microsoft UEFI CA) after conversion in Go plus `Convert-VHD`; NoCloud from a go-diskfs ISO; netplan renders /32 with on-link gateway on both (E2).
- Host route, WinNAT for the public range, NRPT for hlab names, zone apex and reverse zone from host and WSL2: confirmed (E3, E4, E5).
- A chosen VHDX UniqueId appears in the guest's VPD page 0x83; hot-added disks appear without rescan (E6).
- Server disk at LUN 0 stays `sda` when booting a rescue disk at LUN 63 (E7).
- libopenapi-validator validates responses against Hetzner's 3.1.2 description in about 1 ms (E8).
- Wails v3.1.0 ignores WebView2 environment variables; CDP works through `AdditionalBrowserArgs` (E9).
- A non-admin interactive user starts and stops a service whose DACL grants IU RP/WP, using minimal SCM rights (E10).
- Save/resume keeps the guest running; vmms precedes services at Windows shutdown (E11).
