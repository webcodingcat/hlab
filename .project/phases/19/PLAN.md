# Phase 19: Storage VM and SSH access

## Interfaces

```go
// internal/transit: two more fields of Addrs, derived in For.
type Addrs struct {
	// ... existing fields ...
	Storage      netip.Addr     // broadcast minus 2 of the range, 198.19.255.253 by default
	StorageAgent netip.AddrPort // Storage:9100
}

// internal/ipam: NextPublic never hands out transit.For(r).Storage.

// internal/agentapi (storage.go): the storage VM's document and jobs.
const StorageSchema = 1

type Storage struct {
	Schema, Generation int
	PublicRange        netip.Prefix // sources outside it, or inside HostNet, are external
	HostNet            netip.Prefix // the host's transit /30
	Boxes              []Box
}
type Box struct {
	ID          int64
	User        string     // "u100000"; the UID is the number
	IP          netip.Addr
	QuotaBytes  int64      // size of the type
	Hashes      Hashes
	SSHKeys     []string   // OpenSSH lines, written once into .ssh/authorized_keys at creation
	Access      Access     // SSH, Samba, WebDAV, ZFS, ReachableExternally
	Subaccounts []Subaccount
}
type Subaccount struct {
	User    string // "u100000-sub1"
	HomeDir string // relative to the box's data, no leading slash; created when missing
	Hashes  Hashes
	Access  Access // SSH, Samba, WebDAV, ReadOnly, ReachableExternally
}
type Hashes struct{ Crypt, NT string } // never logged
type Access struct{ SSH, Samba, WebDAV, ZFS, ReadOnly, ReachableExternally bool }

const (
	StorageJobFolders = "folders"
	StorageJobUsage   = "usage"
	// "snapshot", "rollback", "delete-snapshot" follow in phase 20.
)
type StorageJob struct {
	Kind     string
	Box      string // box user
	Snapshot string
	Path     string // folders: relative path, "." by default
}
type Usage struct{ Data, Snapshots int64 } // bytes, from the qgroups (E18)
type StorageJobResult struct {
	Folders       []string // names of direct subdirectories, sorted
	Usage         Usage
	SnapshotSizes map[string]int64
}
// Job errors: HTTP 404 with body {"error":"not_found"} for a missing
// folders path, 422 {"error":"invalid_path"} for an absolute or escaping one.

// agentapi.Config: two more fields for the appliance.
type Config struct {
	// ... existing fields ...
	Storage      StorageVM    // Addr invalid: no storage VM
	StorageBoxes []StorageBoxRoute
}
type StorageVM struct {
	Addr netip.Addr // transit Addrs.Storage
	MAC  string     // "02:48:4c:00:00:03"
}
type StorageBoxRoute struct {
	IP                  netip.Addr
	Names               []string // "u100000", "u100000-sub1": labels below storagebox.hetzner.internal
	ReachableExternally bool     // box or any of its subaccounts
}

// internal/agentclient (storage.go)
func NewStorage(opts Options) *StorageClient // Options.Addr = Addrs.StorageAgent
func (c *StorageClient) WaitReady(ctx context.Context) error
func (c *StorageClient) Push(ctx context.Context, doc agentapi.Storage) error // sets Generation, returns after the agent applied it
func (c *StorageClient) Job(ctx context.Context, job agentapi.StorageJob) (agentapi.StorageJobResult, error)
// A job error from the agent is returned as *agentclient.JobError{Status int, Code string}.

// internal/storageagent (role storage of hlab-agent)
func New(secret []byte, o Options) (*Agent, error) // Handler(): PUT /v1/storage, POST /v1/jobs
type Options struct {
	DataDisk string // block device of the data disk (SCSI LUN 1)
	Root     string // "/srv/sbox"
}
// Pure, OS independent (tested on Windows):
func Plan(have State, want agentapi.Storage) []Step // State: boxes and subvolumes found, filesystem present
func Port23(user string, readOnly bool, command string) (argv []string, err error) // SSH_ORIGINAL_COMMAND -> exec argv
func AuthorizedKeys(r io.Reader) []string // OpenSSH lines out of OpenSSH lines and RFC4716 blocks
// cmd/hlab-agent: --role appliance|storage (default appliance); subcommands
// `hlab-agent storage-shell` (ForceCommand of port 23) and
// `hlab-agent storage-keys <user>` (AuthorizedKeysCommand).

// internal/resources/storageboxes
type Deps struct {
	Catalog     *catalog.Catalog
	PublicRange netip.Prefix
	// Apply makes the storage VM match the store (all boxes without the
	// deleting mark); it returns only after the storage agent confirmed.
	Apply     func(ctx context.Context, boxID int64) error
	Job       func(ctx context.Context, job agentapi.StorageJob) (agentapi.StorageJobResult, error)
	FreeBytes func() (uint64, error) // free bytes of the host drive of the data folder
	Clock     scheduler.Clock        // stats job; nil: scheduler.RealClock()
}
const FreeSpaceReserve = 10 << 30 // below it create and a larger change_type answer 412 resource_unavailable
const StatsEvery = 5 * time.Minute
// HLab gets IP string `json:"ip,omitempty"` and Deleting bool `json:"deleting,omitempty"`.
func Boxes(tx store.Tx) ([]agentapi.Box, error) // all projects, without boxes marked deleting
// Module also implements resource.NetContributor (Config.Storage, Config.StorageBoxes),
// resource.Recoverer and resource.Scheduled.

// internal/catalog: own storage box types
func (c *Catalog) ReloadOwnStorageBoxTypes(file string) // reads storage-box-types.custom.json
func (c *Catalog) OwnStorageBoxTypesWarning() string    // "" when fine
// StorageBoxTypes() and the lookup by ID or name include the own types after the catalog's.

// internal/platform
// CompactDisk runs Optimize-VHD -Mode Full on the detached VHDX path.
CompactDisk(ctx context.Context, path string) error
```

### Names the tests fix (from the test writers)

- storageagent (19-04): `type State struct{ Filesystem bool; Boxes, Subvolumes []string }` (box user names); `type Step struct{ Kind StepKind; Box string }`; constants `StepFormat`, `StepCreateBox`, `StepRemoveBox`; the format step comes first.
- agent (19-05): `func StorageBoxDropped(cfg agentapi.Config, src, dst netip.Addr) bool` in `internal/agent/storagebox.go`, the host's transit /30 from `cfg.PublicRange` via `transit.For`.
- Port23 (19-07): refusals contain "Command not allowed"; the sftp argv starts with a path ending in `sftp-server`; rrsync gets `/home` as its last argument; borg gets `--restrict-to-path /home`, a client's own `--restrict-to-path /` is refused.
- catalog (19-02): `Catalog.StorageBoxTypes()` (method), `ReloadOwnStorageBoxTypes`, `OwnStorageBoxTypesWarning`.

## Plans

- [ ] 19-01 Storage interfaces, addresses and client
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SBOX-03, SBOX-04 (groundwork) | Tests: -
  - Reads: ARCHITECTURE.md "Storage VM", "Network"; CONTEXT.md "Addresses", "Network"; this plan's "Interfaces"
  - Size: ~260 lines in 5 files
  - [ ] New `internal/agentapi/storage.go` with the storage types, jobs and job error codes; `Config.Storage` and `Config.StorageBoxes` in `internal/agentapi/agentapi.go`.
  - [ ] `internal/transit/transit.go`: `Storage` and `StorageAgent`; `internal/ipam/ipam.go`: `NextPublic` skips the storage address.
  - [ ] New `internal/agentclient/storage.go`: `StorageClient` with `WaitReady`, `Push` and `Job` on the existing request helper of `agentclient.go`.

- [x] 19-02 Own storage box types
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SBOX-21 | Tests: internal/catalog/storageboxtypes_custom_test.go, internal/catalog/catalog_test.go, internal/cloud/reload_test.go
  - Reads: REQUIREMENTS.md SBOX-21; ARCHITECTURE.md "Files and formats" (`storage-box-types.custom.json`); CONTEXT.md "SBOX-21"; `internal/catalog/custom.go` and `isos_custom.go` as the pattern
  - Size: ~230 lines in 4 files
  - Adjusts: field `Catalog.StorageBoxTypes` becomes the method `StorageBoxTypes()`: `testStorageBoxTypes` in internal/catalog/catalog_test.go, internal/resources/conventions_test.go:68, internal/resources/storageboxes/helpers_test.go:50 and :65 (what they use, e.g. through a setter)
  - [x] `internal/catalog/storageboxtypes.go` (and a new `storageboxtypes_custom.go`): read the own file, append its types after the catalog's, an invalid file or an ID or name colliding with a catalog type keeps only the catalog types and sets the warning.
  - [x] `internal/cloud/reload.go` and `internal/cloud/cloud.go`: create the file as `[]` with the settings.toml ACL at start, load it at start and on `/settings/reload`, report the warning as problem `custom_storage_box_types` in the status.

- [ ] 19-03 Uninstall removes the storage VM
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SBOX-08 | Tests: internal/uninstall/uninstall_test.go
  - Reads: REQUIREMENTS.md SBOX-08, INST-04, INST-05; `internal/uninstall/uninstall.go`
  - Size: ~20 lines in 1 file
  - [ ] `internal/uninstall/uninstall.go`: `ProfileVM` matches exactly `<tag>-storage`; `hlabDirs` gets `storage`.

- [ ] 19-04 Storage agent: disk, boxes and jobs
  - Wave: 2 | Depends on: 19-01 | Review: alone
  - Requirements: SBOX-03, SBOX-06, SBOX-11, SBOX-12 | Tests: internal/storageagent/plan_test.go
  - Reads: ARCHITECTURE.md "Storage VM"; CONTEXT.md "Data disk", "Storage agent"; EXPERIMENTS.md E17, E18, E19; RESEARCH.md 4, 6; `internal/agent/agent.go` (handler and secret pattern), `internal/agent/jobs_linux.go` (disk by LUN, `run`)
  - Size: ~400 lines in 5 files
  - [ ] New `internal/storageagent`: `agent.go` (handler `PUT /v1/storage`, `POST /v1/jobs`, bearer secret), `plan.go` (pure `Plan`: make the filesystem only when the disk holds none, never touch a box present in the document, remove only boxes absent from it), `apply_linux.go` plus `apply_other.go`:
    - `mkfs.btrfs -K`; mount with `discard=async`; `quota enable`.
    - Per box: the user with UID `n`, the subvolume and the level-1 qgroup `1/<n>` limited to `QuotaBytes`.
    - Hashes through `usermod -p`, the chroots of E17, and a read-only bind of the home for read-only subaccounts.
    - The `.ssh/authorized_keys` of a new box from `SSHKeys`; missing subaccount homes are created.
    - After each apply, `qgroup show` is checked for "inconsistent", followed by `quota rescan -w` when needed.
  - [ ] `jobs_linux.go`: `folders` (direct subdirectories, hidden ones included, sorted; `not_found` and `invalid_path` errors), `usage` from `btrfs qgroup show --raw`.
  - [ ] `cmd/hlab-agent/main.go`: `--role storage` starts the storage agent on the address of the transit file, data disk at SCSI LUN 1.

- [ ] 19-05 Appliance: routes, names and reachability of the boxes
  - Wave: 2 | Depends on: 19-01 | Review: alone
  - Requirements: SBOX-04 | Tests: internal/agent/storagebox_test.go
  - Reads: ARCHITECTURE.md "Storage VM" (Network), "Network"; CONTEXT.md "Network"; EXPERIMENTS.md E3, E4, E17 (passive FTP note for phase 20); `internal/agent/network_linux.go`, `isolation_linux.go`, `firewall.go`, `dns.go`
  - Size: ~300 lines in 5 files
  - [ ] `internal/agent/network_linux.go`: a `/32` route to `Storage.Addr` and onlink `/32` routes of the box IPs via it.
  - [ ] `internal/agent/dns.go`: answers each name of `StorageBoxes` under `storagebox.hetzner.internal` with the box IP; `isolation_linux.go`: the storage MAC with `Storage.Addr` and the box IPs as its sources.
  - [ ] New `internal/agent/storagebox.go` (pure: which box IPs drop external sources) and its use in `firewall_linux.go`: forwarded traffic from sources outside the public range or inside the host's transit `/30` to a box IP is dropped unless `ReachableExternally`; TCP 22 and 23 to box IPs pass otherwise.

- [ ] 19-06 Storage boxes: stored IP, user and the documents
  - Wave: 2 | Depends on: 19-01 | Review: alone
  - Requirements: SBOX-03, SBOX-04 | Tests: -
  - Reads: CONTEXT.md "Create", "Delete", "Network"; DECISIONS.md "Storage box stored layout", "Storage box Apply hook"; `internal/resources/storageboxes/*.go`
  - Size: ~250 lines in 4 files
  - Note: storage_test.go also uses `FreeSpaceReserve` and `StatsEvery` of 19-10; 19-06 declares both (constant and interval) so the package's tests compile; 19-10 gives them their behaviour.
  - [ ] `boxes.go`: in the create transaction assign `_hlab.n` (kv counter) and `_hlab.ip` (`ipam.NextPublic`); the API keeps `username`, `server`, `system` null until active; `storageboxes.go`: the new `Deps` fields and `HLab.IP`, `HLab.Deleting`.
  - [ ] New `document.go`: `Boxes(tx)` (all projects, quota from the stored type, hashes, keys, access, subaccounts; boxes marked deleting left out) and `Contribute` (`Config.Storage` when any box exists, `Config.StorageBoxes` with names and reachability).

- [ ] 19-07 Storage agent: SSH on ports 22 and 23
  - Wave: 3 | Depends on: 19-04 | Review: alone
  - Requirements: SBOX-03, SBOX-12, SBOX-13 | Tests: internal/storageagent/port23_test.go, internal/storageagent/keys_test.go
  - Reads: CONTEXT.md "sshd", "SSH keys", "Network" (Match Address); EXPERIMENTS.md E17; RESEARCH.md 2
  - Size: ~380 lines in 5 files
  - [ ] New `internal/storageagent/sshd.go`: one sshd config:
    - port 22: internal-sftp chrooted to `/home`, `-R` only for read-only subaccounts;
    - port 23 only for accounts with ssh_enabled, with `ForceCommand hlab-agent storage-shell`;
    - `AuthorizedKeysCommand hlab-agent storage-keys %u`;
    - `Match Address` that refuses external sources for accounts that are not reachable externally;
    - reloaded after each apply that changes it.
  - [ ] New `port23.go` (pure `Port23`) and `keys.go` (pure `AuthorizedKeys`, reading `<home>/.ssh/authorized_keys` of the account):
    - Port23 allows `internal-sftp`/sftp-server, strictly parsed `scp -t|-f <path>`, `rsync --server` through `rrsync [-ro] /home`, and `borg serve --restrict-to-path /home` (accepting `--remote-path` borg-1.1, borg-1.2, borg-1.4);
    - it refuses everything else with "Command not allowed".
  - [ ] `cmd/hlab-agent/main.go`: subcommands `storage-shell` (exec of the argv, never `sh -c`) and `storage-keys`.

- [ ] 19-08 Storage boxes: action outcomes
  - Wave: 3 | Depends on: 19-06 | Review: alone
  - Requirements: SBOX-03 (box lifecycle with the storage VM) | Tests: internal/resources/storageboxes/storage_test.go, internal/resources/storageboxes/storageboxes_test.go, internal/resources/storageboxes/helpers_test.go
  - Reads: CONTEXT.md "Failed create", "Delete", "Not active"; BACKLOG.md item on `createSubaccount`; `internal/resources/storageboxes/*.go`
  - Size: ~250 lines in 4 files
  - Adjusts: internal/resources/storageboxes/helpers_test.go (the Deps it builds, if the gated Apply needs the new fields)
  - [ ] `storageboxes.go`, `boxes.go`:
    - A failed `create_storage_box` deletes the box and releases its IP; the action carries the error.
    - Delete sets `_hlab.deleting` in the begin transaction and runs Apply; records go and the IP is released only after Apply succeeded; on failure the mark is cleared.
  - [ ] `subaccounts.go`, `subaccount_actions.go`, `actions.go`: subaccount create and every action but delete answer 423 `locked` on a box that is not `active`; new `recover.go`: `Recover` deletes boxes whose create was interrupted.

- [ ] 19-09 Storage VM: creation and removal
  - Wave: 3 | Depends on: 19-04 | Review: alone
  - Requirements: SBOX-03 | Tests: -
  - Reads: ARCHITECTURE.md "Storage VM", "Files and formats"; CONTEXT.md "Storage VM", "Data disk", "storage-debs.json"; RESEARCH.md 1, 4, 9; `internal/cloud/appliance.go`, `appliance/*`
  - Size: ~380 lines in 5 files
  - [ ] New `catalog/storage-debs.json` with the seven packages of RESEARCH.md 1.2 (pool and snapshot URL; every SHA-256 recomputed from the downloaded file before committing).
  - [ ] New `appliance/storage-user-data.yaml` and the storage part of `appliance/appliance.go` (install the debs with one `dpkg -i`, disable `rsync.service`, start `hlab-agent --role storage`).
  - [ ] New `internal/cloud/storage.go`: `ensureStorageVM` and `deleteStorageVM`:
    - `<tag>-storage` with 2 vCPU and 1024 MB; a 10 GB system disk copied from debian-13; a 64 TiB dynamic `storage\data.vhdx` at LUN 1; MAC `02:48:4c:00:00:03` on the public PVLAN as an isolated port.
    - The cidata holds the agent, kv `storage.secret`, the address `Addrs.Storage` and the debs; the stop action follows `stop_behaviour`.
    - A half-created VM is removed again.
    - Delete removes the VM and the folder `storage\`.

- [ ] 19-10 Storage boxes: folders, change_type, free space and stats
  - Wave: 4 | Depends on: 19-08 | Review: alone
  - Requirements: SBOX-06, SBOX-11 | Tests: internal/resources/storageboxes/storage_test.go
  - Reads: CONTEXT.md "Folders", "Stats", "Free space"; RESEARCH.md 3, 8; the frozen `hetzner.spec.json` operation `list_storage_box_folders`; `internal/scheduler/scheduler.go`
  - Size: ~300 lines in 4 files
  - [ ] New `folders.go`: `GET /storage_boxes/{id}/folders?path=` (default `.`): 422 `invalid_input` (field `path`) for an absolute or escaping path before the agent is asked, 404 `not_found` for a missing one, 423 on a box that is not active; validated against the description.
  - [ ] `actions.go`:
    - change_type asks `Job` for usage and answers 422 `invalid_input` (field `storage_box_type`) when data plus snapshots exceed the new size.
    - Create (`boxes.go`) and a larger change_type answer 412 `resource_unavailable` when `FreeBytes` is below `FreeSpaceReserve`.
  - [ ] New `stats.go`: `Jobs()` refreshes `stats` of every active box every `StatsEvery` on `Deps.Clock`, and after each box action.

- [ ] 19-11 Storage VM in the cloud's life
  - Wave: 5 | Depends on: 19-02, 19-05, 19-07, 19-09, 19-10 | Review: alone
  - Requirements: SBOX-03, SBOX-04, SBOX-06, SBOX-11, SBOX-12, SBOX-13 | Tests: test/e2e/storagebox_test.go, test/e2e/storagebox_access_test.go
  - Reads: ARCHITECTURE.md "Cloud start and stop", "Storage VM"; CONTEXT.md "Lifecycle", "Compaction", "Free space"; RESEARCH.md 5, 7, 8, 9; `internal/cloud/cloud.go`, `lifecycle.go`
  - Size: ~350 lines in 5 files
  - [ ] New `internal/cloud/storage_life.go`:
    - Apply builds `storageboxes.Boxes`. Without boxes it deletes the VM, the folder and the storage document; otherwise it ensures the VM, waits for the agent (deadline about 5 min), pushes the document and then `PushAgent`.
    - `Job` goes through the storage client; `FreeBytes` uses `windows.GetDiskFreeSpaceEx` on the data folder.
    - Start: when boxes exist, power the VM on and push the document in the background with retries.
    - Stop: stop the storage VM before the appliance. When it is off afterwards and the stop is no Windows shutdown, run `CompactDisk` on `data.vhdx`: a failure is logged and the stop continues; the skip is logged with stop_behaviour `save`.
  - [ ] `internal/cloud/cloud.go`: the new `storageboxes.Deps`, the start and teardown hooks.
  - [ ] `internal/platform/platform.go`, `internal/platform/hyperv/disks.go`, `internal/platform/platformtest/fake.go`: `CompactDisk` (`Optimize-VHD -Path <p> -Mode Full`).
  - [ ] Write `phases/19/CHECKS.md`: write 2 GB into a box, delete the box, stop the cloud, compare the size of `data.vhdx` before and after.

- [ ] 19-12 Documentation
  - Wave: 6 | Depends on: 19-01 to 19-11 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06 | Tests: test/docs/docs_test.go
  - Reads: SPEC.md "Documentation" (D4 now about 250 lines); CONTEXT.md "Documentation"; `docs/guide.md` "Storage boxes", `docs/deviations.md` "Storage boxes", `docs/configuration.md`, `docs/development/api-coverage.md`, `internal/api/deviations.go`
  - Size: ~250 lines in 6 files
  - [ ] `docs/guide.md`: SSH access on ports 22 and 23 (sftp, scp, rsync, borg), keys, subaccounts, reachability, folders, own storage box types; examples run through context `hlab-docs`.
  - [ ] `docs/deviations.md`:
    - credentials of one box log in at another box's address over SSH;
    - names under `storagebox.hetzner.internal`;
    - a failed create deletes the box;
    - 423 on boxes that are not active;
    - the quota check of change_type;
    - the free-space reserve;
    - the port 23 command set and key formats;
    - the folders answers.
  - [ ] `docs/configuration.md`: `storage-debs.json`, `storage-box-types.custom.json`, the folders `storage` and `cache\debs`.
  - [ ] `docs/development/api-coverage.md` and `internal/api/deviations.go`: `folders` served, snapshots and rollback still unserved until phase 20.
  - [ ] `docs/development/components.md`: the storage agent.
