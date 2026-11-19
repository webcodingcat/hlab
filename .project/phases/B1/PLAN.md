# Phase B1: Backlog: restart recovery, cloud-init and dev-loop bugs

Taken in by the user with /wcc:change on 2026-11-19.

## Items

- [ ] bug: test/e2e harness: `task e2e` fails in `task build` when the dev cloud is already running, because the service holds `bin\hlabd.exe`; a developer who left the cloud running gets a build error instead of a test run (source: 01 verifier)
- [ ] bug: servers: after an hlabd restart during reset_password the action ends `server_error` but the pending reset stays in the record and keeps being served (nothing clears it at startup or on rebuild); the guest may later apply a password the user was told failed, and a server rebuilt afterwards may get its new root password overwritten by the stale one (internal/resources/servers/password.go, reconcile.go) (source: 03 03-13 review)
- [ ] bug: platform/hyperv: the metrics sample runs one full query of three `Win32_PerfRawData_*` classes per running server every 10 s through the single mutex-serialized PowerShell process; with many running servers it occupies the process for much of each interval and every user action waits behind it (internal/platform/hyperv/metrics.go, internal/resources/servers/metrics.go) (source: 03 verifier)
- [ ] bug: images/servers: if hlabd restarts while `create_image` runs, the action ends in error but the image record stays `creating`, and `DELETE /images/{id}` (423 for `creating`) then never lets the user remove it or its partial file in `<data>\images` (internal/resources/images/images.go, internal/resources/servers/snapshot.go) (source: 04 04-07 review)
- [ ] bug: cloud: nothing calls `resource.Reconciler.Reconcile` at cloud start, so a volume whose create or format is cut short by a cloud stop stays `creating` for the user forever, although ARCHITECTURE says Reconcile can run again after a cloud restart (internal/cloud, internal/resources/volumes) (source: 04 04-05 review)
- [ ] bug: servers: a server created with volumes and `automount` whose user_data is itself MIME multipart or gzip (e.g. from OpenTofu `cloudinit_config`) gets it wrapped in a base64 `text/plain` part that cloud-init does not unpack, so the user's configuration silently does not run (internal/resources/servers/vendordata.go, seedUserData) (source: 04 04-11 review)
- [ ] bug: servers: rebuilding a server from a snapshot or backup of that same server keeps instance-id = server ID while the restored disk already carries cloud-init state for it, so cloud-init skips its per-instance modules: the returned `root_password` does not work and new `user_data` is ignored - the normal way to restore a backup (internal/resources/servers/rebuild.go, reconcile.go) (source: 04 verifier)

## Interfaces

```go
// internal/resource/resource.go (DECISIONS Q6)
// Recoverer is implemented by modules whose records an interrupted action
// can leave half-made. Cloud start calls it after the action manager has
// marked interrupted actions error, before the API serves and before the
// first scheduled job run.
type Recoverer interface {
	Recover(ctx context.Context) error
}

// internal/cloud/recover.go
// recoverModules calls Recover of every module implementing
// resource.Recoverer, in module order, and joins their errors.
func recoverModules(ctx context.Context, mods []resource.Module) error

// internal/resources/images: Deps gains the store Recover works on.
type Deps struct {
	Catalog    *catalog.Catalog
	DataFolder string
	Store      *store.Store // used by Recover; nil leaves Recover a no-op
}

// Recover removes every image record with status creating and its file
// (Hlab.Path); a missing file is no error.
func (m *Module) Recover(ctx context.Context) error

// internal/resources/volumes
// Recover removes every volume record with status creating and its disk
// file (Hlab.Path); a missing file is no error.
func (m *Module) Recover(ctx context.Context) error

// internal/platform/platform.go: Platform gains
// MetricsAll reads the counters of all vms in one platform call. A VM that
// is not running (no counter instances) gets a zero sample; a VM that does
// not exist is left out of the map. Metrics(ctx, vm) stays, implemented
// through MetricsAll.
MetricsAll(ctx context.Context, vms []string) (map[string]Sample, error)

// internal/resources/servers/servers.go: Internal gains
// InstanceID is the NoCloud instance-id of the server's seed; empty means
// the server ID. Every rebuild sets "<server id>-<rebuild action id>".
InstanceID string `json:"instance_id,omitempty"`

// internal/resources/servers/vendordata.go (signature unchanged)
// seedUserData decodes user_data like Hetzner's datasource (strict base64
// without whitespace, raw when that fails); with automount volumes it then
// unpacks gzip and copies the leaf parts of a MIME document into its own
// multipart before its cloud-config part.
func seedUserData(userData string, volumes []int64) string
```

- Set by the B1-03 tests (internal/platform/hyperv/metrics_test.go): package-internal `mapCounters(rows counterRows, vms []string) map[string]X` with `counterRows{VPs []vpRow; Disks []diskRow; NICs []nicRow; DiskPaths map[string][]string}`, `vpRow{Name; RunTime, Timestamp uint64}`, `diskRow{Name; ReadBytes, WriteBytes, ReadOps, WriteOps uint64}`, `nicRow{Name; RxBytes, TxBytes, RxPackets, TxPackets uint64}`; X has fields `ticks`, `cpuTicks` and `sample platform.Sample`. `platformtest.Fake.MetricsAll` records `Call{Op: "MetricsAll", Arg: []string}`.
- VOL-03 case "gzip" sends base64(gzip(text)), since raw gzip cannot pass the JSON API.

## Plans

- [x] B1-01 Servers: stale password resets and the instance-id after a rebuild
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SRV-10, SRV-08 | Tests: internal/resources/servers/servers_test.go (TestServers/SRV-10/interrupted_reset, TestServers/SRV-08/instance_id), test/e2e/image_test.go and test/e2e/e2e_test.go (TestE2E/SRV-08/own_snapshot)
  - Reads: REQUIREMENTS SRV-08, SRV-10, SRV-11, META-01; ARCHITECTURE "cidata", "Guest integration", "hlab-agent (appliance)" (metadata service, password reset); RESEARCH.md Q2; DECISIONS "Rescue seed reuses the server's instance-id and network-config", "Root password without expiry, root SSH login by drop-in"
  - Size: ~150 lines in 4 files
  - Adjusts: -
  - [x] `Contribute` (servers.go) puts `Hlab.PasswordReset` into the server's metadata only while the action with the reset's ID has status `running` (`store.GetAction` in the same tx); `rebuild` (rebuild.go) sets `Hlab.PasswordReset = nil` in its record update
  - [x] `Internal.InstanceID` (servers.go); `rebuild` sets it to `<id>-<action.ID>` in the transaction that starts the action; `seed` (reconcile.go) uses it and falls back to the server ID, so the server seed and the rescue seed (rescue.go builds on `m.seed`) carry the current one; the metadata document keeps `ServerID`

- [x] B1-02 Servers: user_data layers in the seed
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SRV-01, VOL-03 | Tests: internal/resources/servers/vendordata_test.go (TestServers/SRV-01/user_data_layers, TestServers/VOL-03/user_data_layers)
  - Reads: REQUIREMENTS SRV-01, VOL-03; ARCHITECTURE "cidata", "Guest integration"; RESEARCH.md Q1, Q3; DECISIONS "Automount at create via runcmd"
  - Size: ~180 lines in 1 file
  - Adjusts: -
  - [x] `seedUserData` (vendordata.go): decode user_data as standard base64 only when it consists of the base64 alphabet with correct padding and no whitespace (Go's decoder skips newlines, so check that first), as cloud-init's `maybe_b64decode(validate=True)`; otherwise keep it raw; without volumes return the decoded bytes (cloud-init gunzips a top-level gzip and parses top-level MIME itself)
  - [x] with volumes: gunzip a result starting with 1f 8b (keep the bytes on error); if the result parses as a MIME message with a `multipart/*` Content-Type (net/mail, mime/multipart, nested multiparts walked), copy every leaf part with its headers and body unchanged, in order, into hlab's outer multipart before hlab's cloud-config part, and make sure hlab's boundary does not occur in the copied content (pick another boundary if it does); anything else keeps today's base64 `text/plain` part

- [x] B1-03 Metrics: one counter read per sample for all VMs
  - Wave: 2 | Depends on: B1-01 | Review: alone
  - Requirements: SRV-21 | Tests: internal/platform/hyperv/metrics_test.go (TestHyperV/SRV-21), internal/resources/servers/servers_test.go (TestServers/SRV-21/one_call)
  - Reads: REQUIREMENTS SRV-21; ARCHITECTURE "platform (interface) and platform/hyperv"; RESEARCH.md Q4; DECISIONS "platform.Sample only gains counters", "Minimal scheduler for status watch and metrics"
  - Size: ~250 lines in 5 files
  - Adjusts: -
  - [x] `Platform.MetricsAll` (platform.go); hyperv (metrics.go): one script reads each of the three `Win32_PerfRawData_*` classes once and `Get-VM | Get-VMHardDiskDrive` once and returns the raw rows (instance name, counters, timestamp) plus each VM's disk paths; a Go function maps rows to the requested VMs by exact name (VP `<vm>:Hv VP <n>`, disk by its path with `\` as `-`, NIC `<vm>_...` with the longest matching VM name winning) and sums them; `cpuPercent` per VM as today; `Metrics(vm)` calls `MetricsAll`
  - [x] `platformtest.Fake.MetricsAll` (fake.go): returns the samples set with `SetSample` and records the call like the other methods
  - [x] `servers.sample` (servers/metrics.go): one `MetricsAll` call per tick for the VMs of all running servers; a VM missing from the result is skipped

- [x] B1-04 Start-up recovery of images and volumes left creating
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SRV-12, VOL-01 | Tests: internal/resources/images/images_test.go (TestImages/SRV-12/recover), internal/resources/volumes/volumes_test.go (TestVolumes/VOL-01/recover), internal/cloud/recover_test.go (TestCloud/VOL-01)
  - Reads: REQUIREMENTS SRV-12, VOL-01; ARCHITECTURE "resource (one module per resource type)", "actions", "Cloud start and stop"; DECISIONS Q6, "Deleting a creating image is locked", "Failed volume create is dropped", "Image Deleter removes only the disk file"
  - Size: ~220 lines in 6 files
  - Adjusts: -
  - [x] `resource.Recoverer` (resource.go); images (images.go): `Deps.Store`, `Recover` removes `creating` records through `Remove` and their files
  - [x] volumes (disk.go): `Recover` removes `creating` records and their disk files
  - [x] cloud: `recoverModules` (new recover.go); `start` (cloud.go) passes `Store` to `imagesres.NewModule` and calls `recoverModules` right after `c.modules()`, before the router mounts the modules and before `serveAPI` and `startJobs`; an error fails the start

- [x] B1-05 E2E harness: build with the dev cloud running
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: - (backlog item 1) | Tests: none (existing e2e rows stay green; measured at the close)
  - Reads: SPEC "Machine prerequisites"; CLAUDE.md "Build and test commands"
  - Size: ~40 lines in 1 file
  - Adjusts: -
  - [x] `setup()` (test/e2e/main_test.go): read `cloudState()` before `buildAll`; if it is `running`, run `hlab cloud stop` with the existing binary and wait for `stopped`, then build, guard the profiles and start the cloud as today without setting `harness.startedCloud`, so teardown leaves it running as found. Measurement at the close: with the dev cloud running, `task e2e -- -run 'TestE2E/API-01'` builds and runs instead of failing in `task build` (before: build error); noted under "Close"

- [ ] B1-06 Documentation
  - Wave: 3 | Depends on: B1-01, B1-02, B1-03, B1-04, B1-05 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06 | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC "Documentation"; REQUIREMENTS "Documentation (`DOC`)"
  - Size: ~40 lines in 2 files
  - Adjusts: -
  - [ ] docs/dev/build-and-test.md: `task e2e` stops a running dev cloud for the build and starts it again
  - [ ] docs/user/deviations.md: after a rebuild the NoCloud instance-id is `<server id>-<n>` while the metadata `/instance-id` stays the server ID; images and volumes cut short by a cloud stop are removed at the next start

## Close

- B1-05 measurement (2026-11-19, on 7055e87): with the dev cloud started (`starting`), `task e2e -- -run 'TestE2E/API-01'` stopped it, built, passed API-01 and left the cloud `running`.
