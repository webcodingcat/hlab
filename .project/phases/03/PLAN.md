# Phase 03: Server lifecycle

Confirmed assumptions and answers: CONTEXT.md. Research: RESEARCH.md
(sections R1 to R6 below mean its numbered sections). Test map: TESTS.md.

## Interfaces

New or changed in this phase; tests and implementers follow them.

```go
// internal/platform (03-03): additive counters. CPUPercent is computed by
// the platform between two Metrics calls for the same VM (per vCPU summed,
// 100 = one vCPU fully busy, as Hetzner's cpu series; 0 on the first call).
// All other counters are cumulative and restart at 0 when the VM restarts.
type Sample struct {
	Time                                               time.Time
	CPUPercent                                         float64
	DiskReadBytes, DiskWriteBytes, NetRxBytes, NetTxBytes uint64
	DiskReadOps, DiskWriteOps, NetRxPackets, NetTxPackets uint64 // new
}
// Power(PowerReboot) keeps its signature: InitiateReboot, and on any return
// value other than 0/4096 a guest shutdown, wait for off (deadline 120 s),
// Start-VM (R1).

// internal/platform/platformtest (03-01): in-memory platform for unit tests.
type Call struct {
	Op  string // method name: "CreateVM", "Power", "SetVM", "ResizeDisk", ...
	VM  string
	Arg any    // PowerOp, VMSpec, size, path ...
}
type Fake struct{ /* VMs with state, disks, calls; safe for concurrent use */ }
func NewFake() *Fake                                   // HostResources: 64 GiB RAM free, 16 cores, 1 TiB disk
func (f *Fake) SetResources(r platform.Resources)
func (f *Fake) SetState(vm string, s platform.VMState) // what the guest did by itself
func (f *Fake) SetSample(vm string, s platform.Sample)
func (f *Fake) FailNext(op string, err error)          // next call of op returns err
func (f *Fake) Block(op string) (release func())        // next call of op waits for release or ctx
func (f *Fake) Calls() []Call
// Power semantics: Start, Reboot, Reset -> Running; Stop -> Off; Shutdown ->
// Off unless HoldShutdown(vm) was called (the guest ignores the request).
func (f *Fake) HoldShutdown(vm string)

// internal/shacrypt (03-02): SHA-512 crypt per Drepper's specification.
func SHA512(password, setting string) (string, error) // setting "$6$[rounds=N$]salt"; clamps rounds, truncates salt to 16
func NewSHA512(password string) (string, error)       // random 16-char salt, default 5000 rounds

// internal/scheduler (03-05)
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}
func RealClock() Clock
type Job struct {
	Name  string
	Every time.Duration
	Run   func(ctx context.Context)
}
func Run(ctx context.Context, c Clock, jobs []Job) // one goroutine per job; returns when ctx ends and jobs returned
// internal/resource (03-05): optional, detected by type assertion in internal/cloud.
type Scheduled interface{ Jobs() []scheduler.Job }

// internal/agentapi (03-04): additive.
type Status struct {
	// ... existing fields
	PasswordResetsDone []int64 // IDs confirmed by guests, kept while the reset is still in the config
}
// Agent metadata service (03-04), 169.254.169.254:80, by source IPv4 on the
// public segment (Metadata.VLAN 0):
//   GET /hetzner/v1/metadata                 YAML: availability-zone, hostname, instance-id, public-ipv4, public-keys, region (sorted keys)
//   GET /hetzner/v1/metadata/<key>           text for hostname, instance-id, public-ipv4, region, availability-zone; YAML list for public-keys and private-networks (R3 field names, MAC quoted)
//   GET /hetzner/v1/userdata                 raw user_data
//   GET /hlab/v1/password-reset              204 | 200 {"id":n,"sha512_crypt":"$6$..."}
//   POST /hlab/v1/password-reset/{id}/done   204
// Unknown source address: 404.
func NewMetadata() *Metadata                           // an Applier (internal/agent) and an http.Handler
func (m *Metadata) Done() []int64                      // feeds Status.PasswordResetsDone

// internal/metrics (03-07): in-memory series per server.
type Store struct{ /* per server ring of platform.Sample, guarded */ }
func NewStore(retention time.Duration) *Store          // hlab: 24 h
func (s *Store) Add(serverID int64, smp platform.Sample)
func (s *Store) Forget(serverID int64)
type Query struct {
	Types      []string // "cpu", "disk", "network"
	Start, End time.Time
	Step       int // seconds; 0 = automatic
}
// ParseQuery reads type (repeated or comma separated), start, end (RFC 3339)
// and step; errors are *FieldError-like values naming the parameter.
func ParseQuery(v url.Values) (Query, error)
type QueryError struct{ Field, Message string }
func (e *QueryError) Error() string
// Series answers Hetzner's metrics object: step = max(sample interval,
// requested or ceil((end-start)/200)), raised so at most 500 points; rates
// from counter deltas, a decrease is a counter reset.
type Result struct {
	Start, End time.Time
	Step       float64
	TimeSeries map[string]struct{ Values [][2]any } // [unix seconds, "value"]
}
func (s *Store) Series(serverID int64, q Query) Result

// internal/resources/servers (03-06 and later)
type Images interface {
	Ensure(ctx context.Context, name string) (string, error) // *images.Store
}
type Settings struct {
	DataFolder    string
	StopBehaviour string // "save" | "shutdown"
	ReserveRAMMB  int64
	ReserveDiskGB int64
	VMPrefix      string // profile tag: "hlab" | "hlab-dev"
}
type Deps struct {
	Catalog     *catalog.Catalog
	PublicRange netip.Prefix
	Store       *store.Store
	Platform    platform.Platform                              // 03-06, replaces Machines
	Images      Images                                         // 03-06
	PushAgent   func(ctx context.Context) error                // 03-06
	Settings    Settings                                       // 03-06
	Clock       scheduler.Clock                                // 03-09; nil = scheduler.RealClock()
	AgentStatus func(ctx context.Context) (agentapi.Status, error) // 03-13
	ResetTimeout time.Duration                                 // 03-13; 0 = 120 s
	Metrics     *metrics.Store                                 // 03-14
}
func (m *Module) Reconcile(ctx context.Context, id int64) error // creates or matches VM, disk, seed, NIC; never changes power
func (m *Module) Delete(ctx context.Context, id int64) error
func (m *Module) Jobs() []scheduler.Job                         // 03-09 status watch (5 s), 03-14 metrics sample (10 s)
// Status values added: StatusStopping "stopping", StatusRebuilding
// "rebuilding", StatusMigrating "migrating".
// Internal gains: RootHash string (crypt of the create/rebuild password,
// never the clear text), PasswordReset *agentapi.PasswordReset (pending).

// internal/apitest (03-06): replaces NoopMachines and BlockingMachines.
func ServersDeps(t *testing.T, cat *catalog.Catalog, p platform.Platform) servers.Deps // temp data folder, fake images, no-op push
```

## Plans

- [x] 03-01 Fake platform for unit tests
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: - (base of SRV-18, SRV-20 unit rows) | Tests: -
  - Reads: ARCHITECTURE "platform (interface) and platform/hyperv"
  - Size: ~220 lines in 1 file
  - Adjusts: -
  - [x] `platformtest.Fake` implementing every `platform.Platform` method in memory with the power semantics, call log, `FailNext`, `Block`, `HoldShutdown`, `SetState`, `SetSample`, `SetResources` (internal/platform/platformtest/fake.go)

- [x] 03-02 SHA-512 crypt
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SRV-02, SRV-10 (hash) | Tests: internal/shacrypt/shacrypt_test.go
  - Reads: RESEARCH R6 (re-check every vector against https://www.akkadia.org/drepper/SHA-crypt.txt or `openssl passwd -6` before the test freezes it)
  - Size: ~160 lines in 1 file
  - Adjusts: -
  - [x] `SHA512` (rounds clamp 1000..999999999, `rounds=` echoed only when given, salt truncated to 16, Drepper's byte order and alphabet) and `NewSHA512` with a salt from crypto/rand (internal/shacrypt/shacrypt.go)

- [ ] 03-03 Hyper-V reboot and metrics
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SRV-07 (reboot), SRV-21 (source) | Tests: - (proved by the SRV-07 and SRV-21 e2e rows)
  - Reads: ARCHITECTURE "platform"; RESEARCH R1, R2 ("Probes to run first" 1 and 2); BACKLOG reboot item; E11
  - Size: ~260 lines in 4 files
  - Adjusts: -
  - [ ] Probe first on the dev host: `Get-CimClass` and `Get-CimInstance` of the three `Win32_PerfRawData_*` classes with a running VM (property names, instance names), and `InitiateReboot` on a running Ubuntu 24.04 and Debian 13 server (return value, time until SSH answers); record the result in the plan's summary
  - [ ] `Sample` counters (internal/platform/platform.go); `Power(PowerReboot)` falls back to InitiateShutdown, wait for Off (120 s), Start-VM on any return value other than 0 and 4096 (internal/platform/hyperv/hyperv.go)
  - [ ] `Metrics` from the probed raw classes, matched by exact delimiter on the VM name, CPU percent from the previous call's ticks kept per VM, all of the VM's disks summed, `_Total` skipped; drop it from pending.go (internal/platform/hyperv/metrics.go, internal/platform/hyperv/pending.go)

- [ ] 03-04 Agent metadata service
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: META-01, SRV-10 (guest side) | Tests: internal/agent/metadata_test.go
  - Reads: ARCHITECTURE "hlab-agent" (metadata, password reset), "Network"; RESEARCH R3; CONTEXT.md META-01
  - Size: ~350 lines in 5 files
  - Adjusts: -
  - [ ] `Metadata` Applier and handler: lookup by source IPv4 among `Config.Metadata` with VLAN 0, Hetzner paths with hand-written YAML (sorted keys, MAC quoted), `/hetzner/v1/userdata`, password-reset GET and done; confirmed IDs kept while the reset is still in the config (internal/agent/metadata.go)
  - [ ] `Status.PasswordResetsDone` filled from `Metadata.Done` (internal/agentapi/agentapi.go, internal/agent/agent.go); second listener on `169.254.169.254:80` (cmd/hlab-agent/main.go)
  - [ ] Input-path binding: TCP to 169.254.169.254:80 from a known guest MAC with a source address not its own is dropped, in the same table as the forward backstop (internal/agent/isolation_linux.go)

- [x] 03-05 Scheduler
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: - (runs SRV-20 watch and SRV-21 sampling) | Tests: -
  - Reads: ARCHITECTURE "scheduler", "resource"
  - Size: ~120 lines in 3 files
  - Adjusts: -
  - [x] `scheduler.Clock`, `RealClock`, `Job`, `Run` (internal/scheduler/scheduler.go); `resource.Scheduled` (internal/resource/resource.go)
  - [x] Cloud start runs the jobs of every module implementing `Scheduled` after the API is up and cancels them first at stop (internal/cloud/cloud.go)

- [ ] 03-06 Servers module reconciles its VMs
  - Wave: 2 | Depends on: 03-01, 03-05 | Review: alone | e2e
  - Requirements: SRV-01, SRV-16 (run again) | Tests: test/e2e/server_test.go (SRV-01, SRV-16 unchanged)
  - Reads: decisions/resource-reconciler.md; ARCHITECTURE "resource", "Guest integration"; CONTEXT.md first line
  - Size: ~400 lines in 10 files (code moves; tests change only their constructor)
  - Adjusts: internal/resources/servers/servers_test.go, internal/mgmt/mgmt_test.go, internal/resources/conventions_test.go, internal/resources/openapi_test.go, internal/resources/placementgroups/placementgroups_test.go (NoopMachines/BlockingMachines become the fake platform through `apitest.ServersDeps`; a blocked create uses `Fake.Block("CreateVM")`)
  - [ ] Move create/delete from internal/cloud/servers.go into `Reconcile`/`Delete` with paths, seed and vendor-data in the module; new `Deps`; create action calls `Reconcile` then powers on; delete `internal/cloud/servers.go`; cloud wires `Deps` (internal/resources/servers/reconcile.go, servers.go, internal/cloud/servers.go, internal/cloud/cloud.go)
  - [ ] `apitest.ServersDeps` replacing `NoopMachines`/`BlockingMachines`; servers and mgmt tests use it (internal/apitest/apitest.go, internal/resources/servers/servers_test.go, internal/mgmt/mgmt_test.go)
  - [ ] Resource tests use it (internal/resources/conventions_test.go, internal/resources/openapi_test.go, internal/resources/placementgroups/placementgroups_test.go); `task e2e -- -run 'TestE2E/SRV-0[1-9]|TestE2E/SRV-16'` passes

- [ ] 03-07 Metrics series
  - Wave: 2 | Depends on: 03-03 | Review: alone
  - Requirements: SRV-21 | Tests: internal/metrics/metrics_test.go
  - Reads: RESEARCH R2 ("How to use"), R4 (metrics endpoint); CONTEXT.md SRV-21
  - Size: ~280 lines in 1 file
  - Adjusts: -
  - [ ] `Store` with retention, `ParseQuery`, `Series` with the nine series names, step rules (200 default, 500 max), rates from deltas and counter resets (internal/metrics/metrics.go)

- [ ] 03-08 Resource limits
  - Wave: 3 | Depends on: 03-06 | Review: alone
  - Requirements: SRV-18 | Tests: internal/resources/servers/servers_test.go (SRV-18/create)
  - Reads: REQUIREMENTS SRV-18; CONTEXT.md "Q3 answer"; DECISIONS "Resource limits"; ARCHITECTURE "Files and formats" (resources settings)
  - Size: ~180 lines in 2 files
  - Adjusts: -
  - [ ] `checkResources(ctx, cores, ramMB, addDiskBytes)`: cores > logical CPUs, RAM > free RAM minus `reserve_ram_mb`, remaining growth of all existing server disks (nominal minus file size) plus the new disk > free disk minus `reserve_disk_gb`, each `resource_unavailable` with a message naming the resource (internal/resources/servers/limits.go)
  - [ ] Create checks synchronously before its transaction (412) and again at the start of its action (internal/resources/servers/servers.go)

- [ ] 03-09 Power actions and status watch
  - Wave: 4 | Depends on: 03-03, 03-08 | Review: alone | e2e
  - Requirements: SRV-07, SRV-20 | Tests: internal/resources/servers/servers_test.go (SRV-20/power), test/e2e/server_test.go (SRV-07)
  - Reads: REQUIREMENTS SRV-07, SRV-20; CONTEXT.md power and status lines; RESEARCH R1, R4 (command names); ARCHITECTURE "actions"
  - Size: ~350 lines in 4 files
  - Adjusts: -
  - [ ] `poweron` (resource check, starting, running), `poweroff` (stopping, off), `shutdown` (stopping; action success once sent), `reboot`, `reset` as locking actions with Hetzner's command names; routes (internal/resources/servers/power.go, servers.go)
  - [ ] Status watch job every 5 s through `Deps.Clock`: servers without a running action take the platform state (Off, Starting, Running, Stopping; Saved, Paused and Other leave it); `Jobs()` (internal/resources/servers/watch.go)
  - [ ] SRV-07 e2e row registered (test/e2e/e2e_test.go); the BACKLOG reboot item is answered in the summary with the path the probe chose

- [ ] 03-10 Root password and guest hooks
  - Wave: 5 | Depends on: 03-02, 03-09 | Review: alone | e2e
  - Requirements: SRV-02 | Tests: internal/resources/servers/servers_test.go (SRV-02), test/e2e/server_test.go (SRV-02)
  - Reads: REQUIREMENTS SRV-02; RESEARCH R5 (chpasswd `expire: false`, `ssh_pwauth`, PermitRootLogin drop-in, merge rules, "Probes" 3); ARCHITECTURE "Guest integration"
  - Size: ~330 lines in 4 files
  - Adjusts: -
  - [ ] Vendor-data builder: `disable_root: false`, `ssh_pwauth: true`, `chpasswd` with `expire: false` and the root hash when there is one, `/etc/ssh/sshd_config.d/00-hlab-root.conf` with `PermitRootLogin yes`, the `hlab-guest` python3 hook with service and 5 s timer (asks the password reset, applies it with `chpasswd -e`, confirms, remembers the last ID) (internal/resources/servers/vendordata.go, reconcile.go)
  - [ ] Create without SSH keys generates a password, stores only `NewSHA512` of it, returns it once as `root_password` (internal/resources/servers/servers.go)
  - [ ] Harness helper for an SSH password login (OpenSSH with `SSH_ASKPASS_REQUIRE=force` and a temporary askpass script, deleted at once; no new module) (test/e2e/main_test.go)

- [ ] 03-11 Rebuild
  - Wave: 6 | Depends on: 03-10 | Review: alone | e2e
  - Requirements: SRV-08, SRV-20, API-12 (rebuild) | Tests: internal/resources/servers/servers_test.go (SRV-20/rebuild, API-12/rebuild), test/e2e/server_test.go (SRV-08)
  - Reads: REQUIREMENTS SRV-08, API-12; CONTEXT.md rebuild line; RESEARCH R4 (rebuild)
  - Size: ~260 lines in 2 files
  - Adjusts: -
  - [ ] `rebuild`: `protected` with rebuild protection, image by name (system images; others `invalid_input`), `user_data` stored or the previous one kept, disk check of 03-08, status rebuilding, hard off, new disk from the image with a new identifier, new seed with same instance-id, MAC and IP, power on, running; `root_password` when the server has no SSH keys (internal/resources/servers/rebuild.go, servers.go)

- [ ] 03-12 Change type
  - Wave: 7 | Depends on: 03-11 | Review: alone | e2e
  - Requirements: SRV-09, SRV-20, SRV-18 (wired) | Tests: internal/resources/servers/servers_test.go (SRV-09, SRV-20/change_type, SRV-18/actions), test/e2e/server_test.go (SRV-09)
  - Reads: REQUIREMENTS SRV-09; CONTEXT.md change_type line; RESEARCH R4 (change_type), R5 (growpart)
  - Size: ~240 lines in 2 files
  - Adjusts: -
  - [ ] `change_type`: `server_not_stopped` unless off; `invalid_server_type` for an unknown, deprecated or arm type or a disk below `primary_disk_size`; resource check (cores, RAM, disk growth with `upgrade_disk`); status migrating, `SetVM`, `ResizeDisk` and new `primary_disk_size` with `upgrade_disk`, server type stored, power on, running (internal/resources/servers/changetype.go, servers.go)

- [ ] 03-13 Metadata document and reset password
  - Wave: 8 | Depends on: 03-04, 03-12 | Review: alone | e2e
  - Requirements: META-01, SRV-10 | Tests: internal/resources/servers/servers_test.go (SRV-10), test/e2e/metadata_test.go (META-01, META-01/spoof), test/e2e/server_test.go (SRV-10)
  - Reads: REQUIREMENTS META-01, SRV-10; ARCHITECTURE "hlab-agent" (Metadata, PasswordReset); RESEARCH R3, R4 (reset_password); CONTEXT.md reset_password line
  - Size: ~300 lines in 4 files
  - Adjusts: -
  - [ ] `Contribute` adds one `Metadata` per server with a public IPv4 (hostname, region = network zone, availability zone `<location>-dc<n>` from the catalog, keys, user_data, pending reset) (internal/resources/servers/servers.go)
  - [ ] `reset_password`: 409 `conflict` unless running; new password, hash into `_hlab.PasswordReset`, push, poll `Deps.AgentStatus` each second until its ID is confirmed (success, reset cleared, push) or `ResetTimeout` (`server_error`, reset cleared); returns `root_password` (internal/resources/servers/password.go); cloud wires `AgentStatus` (internal/cloud/cloud.go)

- [ ] 03-14 Metrics endpoint and sampling
  - Wave: 9 | Depends on: 03-07, 03-13 | Review: alone | e2e
  - Requirements: SRV-21 | Tests: test/e2e/server_test.go (SRV-21)
  - Reads: REQUIREMENTS SRV-21; RESEARCH R4 (metrics); CONTEXT.md SRV-21
  - Size: ~150 lines in 3 files
  - Adjusts: -
  - [ ] `GET /servers/{id}/metrics` through `metrics.ParseQuery` (`invalid_input` on the named field) and `Series`; sample job every 10 s for running servers; delete forgets the series (internal/resources/servers/metrics.go, servers.go); cloud creates the 24 h store (internal/cloud/cloud.go)

- [ ] 03-15 Documentation
  - Wave: 10 | Depends on: 03-14 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06 | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC "Documentation"; REQUIREMENTS DOC; CONTEXT.md "Documentation"; the summaries of 03-03 and 03-09
  - Size: ~120 lines in 3 files
  - Adjusts: -
  - [ ] docs/user/deviations.md: reset_password through the hlab guest hook (not qemu-guest-agent; servers not created from hlab images lack it; 409 `conflict` when not running), metrics kept 24 h in memory at 10 s and lost at cloud stop, disk.0 sums the VM's disks, shutdown reports only the sent request, root password not expired, reboot fallback if 03-03 chose it, metadata service without EC2 routes and with `public-keys`/`userdata` in cloud-init's shape
  - [ ] README.md server section (power actions, rebuild, change type, root password, metadata); docs/dev/components.md (scheduler, metrics, platformtest)
