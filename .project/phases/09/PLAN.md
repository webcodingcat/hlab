# Phase 09: Cloud lifecycle and persistence

## Interfaces

```go
// internal/resource: optional module interface, implemented by servers.
// internal/cloud calls Halt at stop (after API and jobs, before the
// appliance) and Restore at start (after the first PushAgent, before the
// management API and the jobs).
type Lifecycle interface {
	// Halt saves ("save") or shuts down ("shutdown": all guests asked at
	// once, hard off after the deadline) the running VMs and records in the
	// server status which ran. hostShutdown: Hyper-V has already applied
	// AutomaticStopAction; Halt changes neither VMs nor statuses.
	Halt(ctx context.Context, behaviour string, hostShutdown bool) error
	// Restore sets the VMs' AutomaticStopAction for behaviour, then resumes
	// or powers on every server with status running or starting whose VM is
	// Saved or Off. A server that fails is logged; Restore goes on.
	Restore(ctx context.Context, behaviour string) error
}

// internal/platform: Platform gains
SetStopAction(ctx context.Context, name string, a StopAction) error // AutomaticStopAction only
VMs(ctx context.Context, prefix string) ([]string, error)            // names of the VMs starting with prefix

// internal/store
type IncompatibleError struct {
	Found, Want int  // user_version of the file and of this binary
	NotDatabase bool // the file is no SQLite database (SQLITE_NOTADB/CORRUPT)
}
func (e *IncompatibleError) Error() string // names "newer hlab version" when Found > Want
func (e *IncompatibleError) Newer() bool
// Discard renames path, path-wal and path-shm to <name>.discarded-<UTC 20060102T150405Z>
// and returns the new name of path.
func Discard(path string, now time.Time) (string, error)
func KVKeys(tx Tx, prefix string) ([]string, error)

// internal/transit: every address derived from the public range.
type Addrs struct {
	Host      netip.Prefix   // range .2/30
	Appliance netip.Addr     // range .1
	APIListen netip.AddrPort // .2:8780
	APIProxy  netip.AddrPort // .1:80
	Agent     netip.AddrPort // .1:9100
}
func For(r netip.Prefix) Addrs

// internal/ipam
// Outside returns the public IPv4s in use outside r and the range recorded
// by the last RecordRange (invalid if none).
func Outside(tx store.Tx, r netip.Prefix) (used []netip.Addr, recorded netip.Prefix, err error)
func RecordRange(tx store.Tx, r netip.Prefix) error

// internal/logfile
const MaxBytes = 10 << 20; const Keep = 5
func NewWriter(dir, name string, maxBytes int64, keep int) (*Writer, error) // io.WriteCloser, safe for concurrent use; name.1 .. name.<keep>
// Tail emits the last n lines over the rotated files and the current one,
// oldest first; with follow it then emits new lines across rotations until
// ctx ends.
func Tail(ctx context.Context, dir, name string, n int, follow bool, emit func(line []byte) error) error

// internal/discard
type Options struct {
	StatePath, DataFolder, VMPrefix string // VMPrefix e.g. "hlab-dev-server-"
	Platform platform.Platform
	Now      time.Time
}
// Requested reports whether the start parameters ask for a discard and the
// open error is an *store.IncompatibleError; a compatible state is never discarded.
func Requested(params []string, openErr error) bool
// Run renames the state (store.Discard), deletes every VM with VMPrefix,
// the folders disks, volumes, vms and images\image-*.vhdx; the image and
// ISO cache, the appliance and the CA stay.
func Run(ctx context.Context, o Options) (renamed string, err error)

// internal/mgmt: Deps gains
LogDir string // GET /mgmt/v1/logs[?follow=1]: last 1000 lines as NDJSON; "" leaves the route out

// internal/cli: IO gains
LogDir string // read by `hlab logs` while the cloud is stopped
```

Status file problems at a refused start (code, fatal): `state_incompatible`
and `public_range_changed`, both naming `hlab cloud start --discard-state`.

Settled by the test writers (the tests rely on them):
- `windows.HostEntry` is the current `hostEntry` exported, with string fields `Kind`, `Alias`, `Prefix`.
- `store.Discard` renames side files to `state.db-wal.discarded-<time>` and `state.db-shm.discarded-<time>` (suffix after each file's full name).
- logfile's follow poll interval may stay unexported; the follow test uses the default interval with a 10 s deadline.

## Plans

- [x] 09-01 Platform: stop action and VM listing
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: LIFE-02, LIFE-03, LIFE-05, LIFE-08 | Tests: - (used by 09-07 and 09-11 tests)
  - Reads: ARCHITECTURE "platform (interface) and platform/hyperv"; RESEARCH.md topic 1; EXPERIMENTS E11
  - Size: ~150 lines in 3 files
  - [x] Add `SetStopAction` and `VMs` to `platform.Platform` (internal/platform/platform.go), Hyper-V implementation (internal/platform/hyperv/hyperv.go: `Set-VM -AutomaticStopAction`, `Get-VM` filtered by prefix in Go, never parsing localized text), fake (internal/platform/platformtest/fake.go: records the stop action per VM, lists VMs).
  - [x] Verify on a real server VM of the dev cloud (non-elevated, Hyper-V Administrators) whether `Set-VM -AutomaticStopAction` is accepted on a Running and on a Saved VM; report the result in the plan's summary (the user asked for it). `SetStopAction` returns `platform.Error{Kind: InvalidState}` where Hyper-V refuses.

- [x] 09-02 Store: incompatible state and discard
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: LIFE-08 | Tests: internal/store/store_test.go
  - Reads: ARCHITECTURE "Data schema", "hlab (management command)" (Incompatible state); RESEARCH.md topic 3; DECISIONS "Discarding the state cleans host objects and names downgrades"
  - Size: ~180 lines in 3 files
  - [x] `store.Open` pings and reads `user_version` and returns `*IncompatibleError` for a version other than 0 or 1 (`Newer` when higher) and for SQLITE_NOTADB/SQLITE_CORRUPT by code (`Code()&0xff`), never by message (internal/store/store.go).
  - [x] `store.Discard` (rename of the file and its `-wal`/`-shm`, missing side files skipped) and `store.KVKeys` (internal/store/store.go or a new internal/store/kv.go).

- [x] 09-03 Log file with rotation
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: LIFE-09 | Tests: internal/logfile/logfile_test.go
  - Reads: ARCHITECTURE "Files and formats" (hlabd.log); SPEC "Stack"
  - Size: ~260 lines in 3 files
  - [x] New package internal/logfile: `Writer` rotating at `MaxBytes` to `.1`..`.Keep` (oldest dropped, never splitting a line), `Tail` with last n lines and follow across rotation by polling (no fixed sleeps in tests: the poll interval is a package variable tests shorten).
  - [x] cmd/hlabd/main.go logs through `logfile.NewWriter(paths.Logs, "hlabd.log", logfile.MaxBytes, logfile.Keep)`.

- [x] 09-04 Public range: settings rules and transit addresses
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: HOST-02 | Tests: internal/settings/settings_test.go, internal/transit/transit_test.go
  - Reads: ARCHITECTURE "Network", "Files and formats" (Settings); DECISIONS "public_range excludes private and shared ranges", "Network addressing"
  - Size: ~200 lines in 4 files
  - [x] internal/settings/settings.go: `public_range` must be IPv4 /8../28, is normalised to its network address, and is refused (message naming `network.public_range` and the overlapped range) when it overlaps 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12 (this covers 172.31.1.1), 192.168.0.0/16, 224.0.0.0/3 (224/4 and up), 185.12.64.1 or 185.12.64.2.
  - [x] New package internal/transit with `For(r)`.

- [ ] 09-05 IPAM: changed range detection
  - Wave: 2 | Depends on: 09-02 | Review: alone
  - Requirements: HOST-02 | Tests: internal/ipam/ipam_test.go
  - Reads: DECISIONS "Changed public_range refuses the start", "Public IPv4 allocation continues after the last address"
  - Size: ~90 lines in 1 file
  - [ ] `ipam.Outside` over the `ipam.public.used.` keys (every public IPv4 of servers, primary IPs, floating IPs and LBs goes through ipam) and `ipam.RecordRange` (kv `ipam.public.range`) in internal/ipam/ipam.go.

- [ ] 09-06 Hostnet: range collision as a pure function
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: HOST-02 | Tests: internal/hostnet/windows/hostnet_test.go
  - Reads: ARCHITECTURE "hostnet (interface) and hostnet/windows"
  - Size: ~60 lines in 1 file
  - [ ] Move the overlap logic of `Check` into exported `RangeCollisions(entries []HostEntry, r netip.Prefix) []hostnet.Problem` (fatal `range_collision`, message naming the interface or route); `Check` calls it (internal/hostnet/windows/hostnet.go).

- [ ] 09-07 Servers: halt and restore
  - Wave: 2 | Depends on: 09-01 | Review: alone
  - Requirements: LIFE-02, LIFE-03, LIFE-05 | Tests: internal/resources/servers/lifecycle_test.go
  - Reads: ARCHITECTURE "resource", "Cloud start and stop"; EXPERIMENTS E11; DECISIONS "Server status kept until restore at cloud start", "Stop behaviour at a Windows shutdown", "VM settings and Windows shutdown"
  - Size: ~260 lines in 3 files
  - [ ] Add `resource.Lifecycle` (internal/resource/resource.go).
  - [ ] internal/resources/servers/lifecycle.go: `Halt` (save one VM after another, a failed save falls back to guest shutdown then hard off; shutdown asks all running guests, waits `Deps.HaltTimeout` (default 60 s, short in tests) in total, then hard off; Running VMs keep status `running`, Off VMs get `off`; hostShutdown changes nothing) and `Restore` (`SetStopAction` on every server VM that is Off or Saved, and on Running VMs too if 09-01 found Hyper-V accepts it, an InvalidState failure only logged; then Start for status running/starting with VM Saved or Off; failures logged).

- [x] 09-08 Load balancers: separate IPv6 prefix (Q14)
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: LB-01, LB-09 | Tests: internal/resources/loadbalancers/loadbalancers_test.go
  - Reads: DECISIONS "Q14: separate IPv6 prefix for load balancers", "Q11"; ARCHITECTURE "resource" (Recoverer)
  - Size: ~120 lines in 3 files
  - Adjusts: internal/resources/loadbalancers/loadbalancers_test.go (expected formal IPv6 of LB-01/LB-09 rows follows Q14)
  - [x] `loadbalancers.IPv6` uses `0xA0000000|id` (internal/resources/loadbalancers/loadbalancers.go).
  - [x] New internal/resources/loadbalancers/recover.go: `Recover` moves every `dns_ptr` entry of an LB without IPv6 primary IP from the /64 `2001:db8:<0x80000000|id>` to the new /64 with the same interface ID; idempotent; LBs with an IPv6 primary IP untouched.

- [ ] 09-09 Agent and appliance: transit addresses from the range
  - Wave: 2 | Depends on: 09-04 | Review: alone
  - Requirements: HOST-02 | Tests: - (default range covered by every e2e row)
  - Reads: ARCHITECTURE "hlab-agent (appliance)", "Network"; appliance/appliance.go
  - Size: ~180 lines in 5 files
  - Adjusts: internal/agent/dns_test.go (only if the DNS listen addresses become a parameter)
  - [ ] Appliance seed: new file `transit` (appliance address, one line) next to the secret, copied by hlab-agent.service; network-config, instance-id hash (over user-data and network-config) and addresses from `transit.For(c.set.Network.PublicRange)` (appliance/appliance.go, appliance/hlab-agent.service, internal/cloud/appliance.go).
  - [ ] hlab-agent listens on the address from `transit` (cmd/hlab-agent/main.go); its DNS server binds that address plus 185.12.64.1/2 and answers `api.hetzner.internal` with `Config.API.Listen.Addr()` (internal/agent/dns.go).

- [ ] 09-10 Discard of an incompatible state
  - Wave: 2 | Depends on: 09-01, 09-02 | Review: alone
  - Requirements: LIFE-08 | Tests: internal/discard/discard_test.go
  - Reads: ARCHITECTURE "hlab (management command)" (Incompatible state), "Files and formats"; DECISIONS "Discarding the state cleans host objects and names downgrades"
  - Size: ~170 lines in 2 files
  - [ ] New package internal/discard with `Requested` and `Run` as in Interfaces.

- [ ] 09-11 Management API and CLI: logs and discard-state
  - Wave: 2 | Depends on: 09-03 | Review: alone
  - Requirements: LIFE-08, LIFE-09 | Tests: internal/cli/cli_test.go
  - Reads: ARCHITECTURE "hlab (management command)", "Management API (named pipe)"; docs/user/commands.md
  - Size: ~280 lines in 4 files
  - [ ] `GET /mgmt/v1/logs[?follow=1]` via `logfile.Tail` (1000 lines, NDJSON, flushed per line) in internal/mgmt/mgmt.go.
  - [ ] CLI: `hlab logs [--follow]` (management API while running, `logfile.Tail` on `IO.LogDir` while stopped; human `time LEVEL msg key=value...`, `--output json` raw lines) and `hlab cloud start --discard-state` (start parameter `discard-state`); `cloud status` shows the refused message as today (internal/cli/cli.go, cmd/hlab/main.go sets `LogDir`); short entries in docs/user/commands.md so DOC-01 stays green.

- [ ] 09-12 Cloud: start and stop with persistence
  - Wave: 3 | Depends on: 09-01, 09-02, 09-03, 09-04, 09-05, 09-07, 09-08, 09-09, 09-10, 09-11 | Review: alone
  - Requirements: LIFE-02, LIFE-03, LIFE-04, LIFE-05, LIFE-07, LIFE-08, HOST-02 | Tests: test/e2e/life_test.go
  - Reads: ARCHITECTURE "Cloud start and stop", "hlab (management command)", "Network"; DECISIONS phase 09 entries; CONTEXT.md
  - Size: ~300 lines in 4 files
  - [ ] internal/cloud/cloud.go: addresses from `transit.For`; on `*store.IncompatibleError` refuse with `state_incompatible` unless `discard.Requested(params, err)`, then `hostnet.Down` (old tagged objects and zone NRPT rules), `discard.Run`, open the empty state; `ipam.Outside` non-empty refuses with `public_range_changed` (naming the recorded old range, or the first address outside when none is recorded, and `--discard-state`), and also discards when requested; `ipam.RecordRange` after the check; `Lifecycle.Restore` after the first `PushAgent`; at stop: jobs first, then re-read `stop_behaviour` (start value if the file is invalid), `Lifecycle.Halt`, appliance, `hostnet.Down`.
  - [ ] cmd/hlabd/main.go passes the start parameters to `Cloud.Start`.

- [ ] 09-13 Documentation
  - Wave: 4 | Depends on: 09-12 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06, DOC-07 | Tests: test/docs/docs_test.go
  - Reads: SPEC "Documentation"; REQUIREMENTS "Documentation (DOC)"; DECISIONS phase 09 entries
  - Size: ~200 lines in 5 files
  - [ ] docs/user/commands.md (`hlab logs`, `cloud start --discard-state`), docs/user/configuration.md (stop behaviour used at stop and re-read then, at a Windows shutdown the action set at the last start unless 09-01 found it applies to running VMs, `public_range` rules and refusals, log rotation), docs/user/deviations.md (LB IPv6 prefix of Q14), README.md (stop and start, discarding the state), docs/dev where components changed.
