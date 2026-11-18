# Phase 04: Images, backups, rescue, ISOs and volumes

Confirmed assumptions, corrections and answers: CONTEXT.md. Research:
RESEARCH.md (R1 to R7 below mean its numbered sections). Test map: TESTS.md.

## Interfaces

New or changed in this phase; tests and implementers follow them.

```go
// internal/platform (04-01): one method added. CreateChildDisk creates the
// differencing VHDX path on the VHDX parent and gives it a new disk
// identifier (E7). Snapshot keeps its signature: target becomes a standalone
// dynamic VHDX with a new disk identifier; a running VM is copied through a
// Standard checkpoint (copy the read-only parent, remove the checkpoint, wait
// until no drive path ends in .avhdx, deadline), an off or saved VM directly
// (R1). Every VM is created with CheckpointType Standard.
type Platform interface {
	// ... unchanged methods ...
	CreateChildDisk(ctx context.Context, path, parent string) error
}
// platformtest.Fake: CreateChildDisk records Call{Op: "CreateChildDisk",
// Arg: parent} and creates the file; Snapshot copies as before.

// internal/agentapi (04-03): automount volumes per server for the guest path
// GET /hlab/v1/volumes, answered as {"volumes":[<id>,...]} (sorted, [] when
// none) to the server identified like the other metadata paths.
type Metadata struct {
	// ... unchanged fields ...
	Volumes []int64 // attached volumes with automount
}

// internal/agentclient (04-02)
func (c *Client) Job(ctx context.Context, job agentapi.Job) (agentapi.JobResult, error) // POST /v1/jobs; OK false -> error carrying Message

// internal/agent (04-02): pure helper of rescue-prepare.
// RewriteIDs replaces every occurrence of each old identifier (map key) by
// its new one (map value) in text; nothing else changes.
func RewriteIDs(text string, ids map[string]string) string

// internal/images (04-04, 04-05)
type Source struct {
	URL, ChecksumURL, Format string
	// Tried in order when URL or ChecksumURL answers 404 (Debian's current/
	// directory moves to archive/ at the next point release, R6).
	FallbackURL, FallbackChecksumURL string
}
type Options struct {
	// ... unchanged fields ...
	ISOs   map[string]Source // ISO name -> source (Format "iso": no conversion)
	ISODir string            // <data>\isos
	// PrepareRescue writes the prepared rescue base dst from the cached
	// base of RescueImage (copy, appliance job rescue-prepare).
	RescueImage   string // "debian-13"
	PrepareRescue func(ctx context.Context, base, dst string) error
}
func (s *Store) EnsureISO(ctx context.Context, name string) (string, error) // download once, verify, cache <ISODir>\<name>
func (s *Store) EnsureRescue(ctx context.Context) (string, error)           // <Dir>\rescue.vhdx, prepared once
// Fetch downloads the first URL that answers 200 to dst and verifies its
// SHA-256 (hex); on error nothing stays at dst.
func Fetch(ctx context.Context, client *http.Client, urls []string, sha256hex, dst string) error

// internal/catalog (04-04): catalog/isos.json, {"isos":[...]} in Hetzner's
// ISO shape plus `_hlab.source` (url, checksum_url, fallback_url,
// fallback_checksum_url).
type ISO struct {
	ID           int64
	Name         string
	Architecture string
	Deprecated   bool
	Source       images.Source
	Raw          json.RawMessage // the object as served, without `_hlab`
}
// Catalog gains ISOs []ISO, ISO(ref string) (ISO, bool) by name or ID, and
// ISOSources() map[string]images.Source.

// internal/resources/images (04-06): project images (snapshots, backups) as
// store kind "images" in Hetzner's image shape plus `_hlab`.
const Kind = "images"
type Internal struct {
	ProjectID int64  `json:"project_id"`
	Path      string `json:"path"` // <data>\images\image-<id>.vhdx
	Automatic bool   `json:"automatic,omitempty"` // daily backup
}
type Record struct {
	Image                   // Hetzner's image object (type snapshot|backup, status, disk_size, image_size, created_from, bound_to, protection, labels ...)
	Hlab Internal `json:"_hlab"`
}
func New(cat *catalog.Catalog) *Module                 // unchanged: catalog images only (existing tests)
func NewModule(d Deps) *Module                         // Deps{Catalog, DataFolder}; catalog plus project images
func Put(tx store.Tx, r Record) error
func Load(tx store.Tx, projectID, id int64) (Record, error)
func Backups(tx store.Tx, serverID int64) ([]Record, error) // oldest first
func Remove(tx store.Tx, r Record) error                   // deletes the record; the caller removes r.Hlab.Path after the commit
// Module implements resource.Deleter (project deletion removes the files).

// internal/resources/servers (04-07): shared by 04-08 to 04-13.
type Images interface {
	Ensure(ctx context.Context, name string) (string, error)
	EnsureISO(ctx context.Context, name string) (string, error)
	EnsureRescue(ctx context.Context) (string, error)
}
type DiskUse struct {
	Path         string
	NominalBytes int64
}
type Deps struct {
	// ... unchanged fields ...
	// ExtraDisks lists further thin disks for the disk rule (volumes); nil = none.
	ExtraDisks func(tx store.Tx) ([]DiskUse, error)
}
type Internal struct {
	// ... unchanged fields ...
	ImageID          int64   `json:"image_id,omitempty"`          // project image the server was created or rebuilt from
	AutomountVolumes []int64 `json:"automount_volumes,omitempty"` // attached at create with automount (user-data part, 04-11)
	Rescue           *Rescue `json:"rescue,omitempty"`
	ISO              string  `json:"iso,omitempty"` // attached catalog ISO name
	BackupsEnabled   bool    `json:"backups_enabled,omitempty"`
}
type Rescue struct {
	Enabled  bool      `json:"enabled"`  // rescue_enabled
	Since    time.Time `json:"since"`    // enable time; auto-disable after 60 min without a boot into it
	Booted   bool      `json:"booted"`   // hlab started the VM into rescue
	DiskPath string    `json:"disk_path"` // LUN 63 child disk, removed at the next hlab-driven boot or when the VM is seen off
	RootHash string    `json:"root_hash"`
	SSHKeys  []string  `json:"ssh_keys"`
}
func VMName(prefix string, id int64) string
func (m *Module) CheckDisk(ctx context.Context, stor *store.Store, addBytes int64) error // 412 resource_unavailable (SRV-18 disk rule incl. ExtraDisks)
func SetVolumes(tx store.Tx, serverID int64, ids []int64) error                       // the server's `volumes` field
// VolumeHooks is set by the cloud after both modules exist (04-13).
type VolumeHooks interface {
	// CheckAttach validates the volume IDs of a server create (exist, same
	// project and location, not attached) with api field errors.
	CheckAttach(tx store.Tx, projectID int64, location string, ids []int64) error
	Attach(ctx context.Context, stor *store.Store, serverID int64, ids []int64, automount bool) error
	DetachAll(ctx context.Context, stor *store.Store, serverID int64) error // server deletion; files stay
}
func (m *Module) SetVolumes(h VolumeHooks)

// internal/resources/volumes (04-12, 04-13)
const Kind = "volumes"
type Deps struct {
	Catalog      *catalog.Catalog
	Store        *store.Store
	Platform     platform.Platform
	DataFolder   string
	VMPrefix     string
	CheckDisk    func(ctx context.Context, stor *store.Store, addBytes int64) error // nil = no check (until 04-13 wires it)
	ApplianceJob func(ctx context.Context, diskPath string, job agentapi.Job) error // job.LUN set by the cloud
	PushAgent    func(ctx context.Context) error
}
func New(d Deps) *Module                                   // Module, Reconciler, NetContributor, servers.VolumeHooks
type Disk struct {
	Path         string
	NominalBytes int64
}
func Disks(tx store.Tx) ([]Disk, error) // every volume disk; the cloud maps it to servers.Deps.ExtraDisks
func Page83(id int64) [16]byte                             // 484C4142-0000-4000-8000-<id, 12 hex digits>, text byte order

// internal/cloud (04-05): one appliance job at a time; the disk is
// hot-added at the appliance's LUN 1, the job runs, the disk is detached
// (also on error).
func (c *Cloud) applianceJob(ctx context.Context, diskPath string, job agentapi.Job) error
```

## Plans

- [x] 04-01 Platform: child disks and snapshots
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SRV-12, SRV-11 | Tests: - (covered by test/e2e/image_test.go, rescue_test.go)
  - Reads: ARCHITECTURE "platform", "Guest integration" (rescue); RESEARCH R1, R3 (last two bullets); EXPERIMENTS E7
  - Size: ~220 lines in 5 files
  - Adjusts: -
  - [x] `CreateChildDisk` in the interface, hyperv (`New-VHD -Differencing`, `Set-VHD -ResetDiskIdentifier`) and the fake (internal/platform/platform.go, hyperv/disks.go, platformtest/fake.go)
  - [x] `Snapshot` in hyperv: off/saved VM direct copy; running VM `Set-VM -CheckpointType Standard`, `Checkpoint-VM -Passthru` with an hlab-prefixed name, copy the parent, reset the identifier, `Remove-VMCheckpoint` by object, wait for the merge (no `.avhdx` path) with a deadline; leftover hlab checkpoints are removed first; `CreateVM` sets CheckpointType Standard; remove the stub (hyperv/disks.go, hyperv/hyperv.go, hyperv/pending.go)

- [x] 04-02 Agent jobs
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SRV-11, VOL-03 | Tests: internal/agent/jobs_test.go
  - Reads: ARCHITECTURE "hlab-agent" (jobs); RESEARCH R3, R4; EXPERIMENTS E7
  - Size: ~330 lines in 5 files
  - Adjusts: -
  - [x] `POST /v1/jobs` (same bearer auth, synchronous, `JobResult`), `RewriteIDs`, job dispatch with unknown kind refused (internal/agent/agent.go, agent/jobs.go, agent/jobs_other.go)
  - [x] Linux jobs: find the SCSI disk (type 0) with the LUN in sysfs, never by host number; `mkfs` whole device `mkfs.ext4 -F` / `mkfs.xfs -f`, clear error when `mkfs.xfs` is missing; `rescue-prepare`: read old PARTUUIDs and root FS UUID with blkid, `sgdisk -G`, re-read, `e2fsck -f -p`, `tune2fs -U` with a UUID made in Go, mount root and ESP, `RewriteIDs` on fstab and every grub.cfg found under `/boot/grub` and the ESP, unmount (internal/agent/jobs_linux.go)
  - [x] `Client.Job` (internal/agentclient/agentclient.go)

- [x] 04-03 Agent metadata: automount volumes
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: VOL-03 | Tests: internal/agent/metadata_test.go
  - Reads: ARCHITECTURE "hlab-agent" (metadata), "Guest integration" (volumes); DECISIONS "Q4 Automount on later attach via a metadata path", "Metadata service binds IP and MAC on its input path"
  - Size: ~60 lines in 2 files
  - Adjusts: -
  - [x] `Metadata.Volumes` and `GET /hlab/v1/volumes` on the existing server lookup (internal/agentapi/agentapi.go, internal/agent/metadata.go)

- [x] 04-04 ISO catalog and ISO cache
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: CAT-08 | Tests: internal/images/images_test.go, internal/resources/isos/isos_test.go
  - Reads: REQUIREMENTS CAT-07, CAT-08; ARCHITECTURE "images", "Files and formats"; RESEARCH R5 (size warning), R6
  - Size: ~380 lines in 5 files
  - Adjusts: -
  - [x] `catalog/isos.json` with the five ISOs of R6 (IDs, names as the file names, descriptions, `type: public`, `architecture: x86`, sources; debian-13 with the archive fallback); `Catalog.ISOs`, `ISO`, `ISOSources` (internal/catalog/catalog.go)
  - [x] `Source` fallback fields, `EnsureISO` (no conversion, same lock and temp handling), `Fetch` (internal/images/images.go, internal/images/download.go)
  - [x] Module `isos` with `isos.New(cat *catalog.Catalog) *Module` like locations: `GET /isos` (name, architecture, include_architecture_wildcard, page, per_page through `api.ListRequest`) and `GET /isos/{id}` (internal/resources/isos/isos.go)

- [x] 04-06 Project images
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SRV-12, SRV-14 | Tests: internal/resources/images/images_test.go
  - Reads: ARCHITECTURE "resource", "Data schema"; RESEARCH R7 (create_image, image fields); DECISIONS "PUT changes only the fields present"
  - Size: ~330 lines in 2 files
  - Adjusts: -
  - [x] Record, `NewModule`, `Put`/`Load`/`Backups`/`Remove`; list and get merge catalog images with the project's images (filters type, status, bound_to, architecture, include_deprecated; sorts id, name, created) (internal/resources/images/images.go, records.go)
  - [x] `PUT /images/{id}` (description, labels, `type` backup→snapshot clears bound_to), `DELETE /images/{id}` (`protected` with delete protection, removes the file), `POST /images/{id}/actions/change_protection`, action endpoints, `Deleter` (internal/resources/images/images.go, records.go)

- [ ] 04-12 Volumes: create, resize, delete
  - Wave: 3 | Depends on: 04-07 | Review: alone
  - Requirements: VOL-01 | Tests: internal/resources/volumes/volumes_test.go (the test package uses servers interfaces of 04-07; VOL-01's case for deleting an attached volume passes only after 04-13)
  - Reads: REQUIREMENTS VOL-01; ARCHITECTURE "resource", "Guest integration"; RESEARCH R2, R7 (volume create); DECISIONS "Guest-visible identifiers", "Volume identifier byte order"
  - Size: ~400 lines in 2 files
  - Adjusts: -
  - [ ] Module, Record (`linux_device`, `format`, `location`, `server`, `assignee`, `status`, protection, labels), `POST /volumes` validation (size 10–10240, location by name or ID or from the server, automount without server, format ext4/xfs, a location differing from the server's) with `CheckDisk`; list, get, PUT, `DELETE` (attached: 422 `invalid_input`; removes the file), `change_protection`, `resize` (grow only, `ResizeDisk`, `CheckDisk` on the growth), `Page83`, `Disks` (internal/resources/volumes/volumes.go)
  - [ ] Reconcile/Delete: create the disk with `Page83`, run `mkfs` through `ApplianceJob` when `format` is set, then status `available` (internal/resources/volumes/disk.go)

- [ ] 04-05 Appliance jobs, rescue base and wiring
  - Wave: 4 | Depends on: 04-02, 04-04, 04-06, 04-12 | Review: alone
  - Requirements: SRV-11, VOL-03, CAT-08 | Tests: - (covered by test/e2e/rescue_test.go, volume_test.go)
  - Reads: ARCHITECTURE "hlab-agent", "images", "Cloud start and stop"; RESEARCH R3, R4 (option B); CONTEXT "Corrections by the user"
  - Size: ~330 lines in 5 files
  - Adjusts: -
  - [ ] `catalog/appliance-debs.json` (xfsprogs, libicu76, libinih1: pool URL, snapshot.debian.org URL, sha256); hlabd fetches them once into `<data>\cache\debs\` and puts them on the appliance cidata; the install script runs `dpkg -i` from the seed only when `mkfs.xfs` is missing (internal/cloud/appliance.go, appliance/hlab-agent-install.sh)
  - [ ] `applianceJob` (mutex, LUN 1, detach on every path) and `PrepareRescue` (copy the debian-13 base with `CreateDisk`, job `rescue-prepare`, rename into place); `EnsureRescue` in the store (internal/cloud/jobs.go, internal/images/rescue.go)
  - [ ] Wiring: images store with ISOs and rescue, `isos` module, `imagesres.NewModule`, `volumes.New` with `ApplianceJob` and `PushAgent` (internal/cloud/cloud.go)

- [ ] 04-07 Servers: snapshots and images as source
  - Wave: 2 | Depends on: 04-01, 04-06 | Review: alone
  - Requirements: SRV-12, SRV-18 | Tests: internal/resources/servers/snapshot_test.go, test/e2e/image_test.go
  - Reads: REQUIREMENTS SRV-12; RESEARCH R1, R7 (create_image, rebuild); DECISIONS "Rebuild from system images only in phase 03", "Q3 SRV-18 counts live RAM and remaining disk growth"
  - Size: ~380 lines in 5 files
  - Adjusts: -
  - [ ] Shared changes of the Interfaces section: `Images` interface, `Internal` fields, `Rescue`, `DiskUse`, `ExtraDisks`, `VMName`, `SetVolumes`, `VolumeHooks`/`SetVolumes` (no behaviour yet), `CheckDisk` including `ExtraDisks`; apitest fake images gain `EnsureISO`/`EnsureRescue` (internal/resources/servers/servers.go, limits.go, internal/apitest/apitest.go)
  - [ ] `create_image` (type snapshot here; description default, labels): image record `creating`, server locked, `CheckDisk` on the disk file size, `Platform.Snapshot` into `<data>\images\image-<id>.vhdx`, `image_size`, then `available`; a failure removes record and file (internal/resources/servers/snapshot.go)
  - [ ] Server create and rebuild accept a snapshot or backup of the project by ID (base = its file, `ImageID`, server `image` = the image object); a type whose disk is below the image's `disk_size` gives 422 `invalid_input` on field `image`/`server_type` (internal/resources/servers/servers.go, rebuild.go, reconcile.go)

- [ ] 04-13 Volumes: attach and detach
  - Wave: 5 | Depends on: 04-03, 04-05, 04-07 | Review: alone
  - Requirements: VOL-05, VOL-02, SRV-18 | Tests: internal/resources/volumes/volumes_test.go
  - Reads: REQUIREMENTS VOL-02, VOL-05; RESEARCH R7 (attach, create with server); CONTEXT "Corrections by the user" (assignee)
  - Size: ~380 lines in 5 files
  - Adjusts: -
  - [ ] `attach` (`assignee {id, type: server}` or deprecated `server`, `automount`), `detach`; actions lock volume and server; lowest free LUN 1..16 (17th: 422 `invalid_input`); 422 `volume_already_attached` (added to the status table) and `invalid_input` for another location; `servers.SetVolumes`; `POST /volumes` with a server attaches and returns `next_actions`; `Contribute` fills `Metadata.Volumes` (internal/resources/volumes/attach.go, volumes.go, internal/api/errors.go)
  - [ ] `VolumeHooks` implementation; servers create takes `volumes` and `automount` (checked in the handler, attached after the VM exists, `AutomountVolumes` recorded); server delete calls `DetachAll` before removing the VM; cloud sets the hooks, volumes' `CheckDisk` to the servers module's and `servers.Deps.ExtraDisks` from `volumes.Disks` (internal/resources/servers/servers.go, internal/cloud/cloud.go)

- [ ] 04-08 Servers: backups
  - Wave: 6 | Depends on: 04-07, 04-13 | Review: alone
  - Requirements: SRV-13, SRV-14 | Tests: internal/resources/servers/backup_test.go
  - Reads: REQUIREMENTS SRV-13, SRV-14; ARCHITECTURE "scheduler"; RESEARCH R7 (backups); DECISIONS "Automatic daily backups", "Minimal scheduler for status watch and metrics"
  - Size: ~300 lines in 3 files
  - Adjusts: -
  - [ ] `enable_backup` (`backup_window` "22-02"), `disable_backup` (deletes every backup), `create_image type=backup` (needs backups enabled, else 422 `invalid_input` on `type`; `bound_to`), 7 slots shared, oldest beyond 7 deleted with its file; server delete deletes its backups and keeps snapshots (internal/resources/servers/backup.go, snapshot.go, servers.go)
  - [ ] Job `backups` every hour on the module's clock: one backup per server with backups enabled per UTC day as action `create_image`, a locked server waits for the next run (internal/resources/servers/backup.go, watch.go)

- [ ] 04-11 Guest volume hooks and automount
  - Wave: 6 | Depends on: 04-03, 04-13 | Review: alone
  - Requirements: VOL-02, VOL-03, VOL-04 | Tests: internal/resources/servers/vendordata_test.go, test/e2e/volume_test.go
  - Reads: ARCHITECTURE "Guest integration"; DECISIONS "Q4 Automount on later attach via a metadata path", "Vendor-data as multipart MIME", "Guest password hook in python3", "Volume identifier byte order"; EXPERIMENTS E6; RESEARCH R2
  - Size: ~260 lines in 2 files
  - Adjusts: -
  - [ ] vendor-data script part installs the udev rule (`scsi-0HC_Volume_<id>` from the last 6 bytes of the page 83 identifier, E6) and `hlab-volume@.service` with a python3 script: wait for the metadata service with a deadline, mount only volumes listed by `GET /hlab/v1/volumes` that carry a filesystem, at `/mnt/HC_Volume_<id>` with an fstab line `discard,nofail,defaults` (internal/resources/servers/vendordata.go)
  - [ ] Seed user-data: with `AutomountVolumes` a MIME multipart of the user's user_data unchanged plus a cloud-config part mounting each volume; without, user_data as before; metadata keeps the original (internal/resources/servers/vendordata.go, reconcile.go)

- [ ] 04-09 Servers: rescue
  - Wave: 7 | Depends on: 04-01, 04-05, 04-08 | Review: alone
  - Requirements: SRV-11 | Tests: internal/resources/servers/rescue_test.go, test/e2e/rescue_test.go
  - Reads: REQUIREMENTS SRV-11; ARCHITECTURE "Guest integration" (rescue); CONTEXT "Corrections by the user"; RESEARCH R3, R7 (enable_rescue); DECISIONS "Rescue system from a separate rescue disk"; EXPERIMENTS E7
  - Size: ~380 lines in 5 files
  - Adjusts: -
  - [ ] `enable_rescue` (`type` linux64, `ssh_keys` IDs) returns `root_password`, no reboot: `EnsureRescue`, `CreateChildDisk` at LUN 63, rescue seed in DVD slot 0 (the server's instance-id and network-config, the rescue keys and password hash, no user_data), LUN 63 first in the boot order; `disable_rescue` restores boot order and the server's seed at once (internal/resources/servers/rescue.go, servers.go)
  - [ ] Consumption: a poweron, reset or reboot hlab drives while rescue is enabled boots it and then sets `rescue_enabled` false, boot order LUN 0 first and the server's seed back; the job of the status watch disables a rescue unused for 60 min; the rescue disk is detached and deleted at the next hlab-driven boot without rescue or when the VM is seen off; delete and rebuild remove it (internal/resources/servers/rescue.go, power.go, watch.go, rebuild.go)

- [ ] 04-10 Servers: ISOs
  - Wave: 8 | Depends on: 04-04, 04-05, 04-09 | Review: alone
  - Requirements: SRV-15, CAT-08 | Tests: internal/resources/servers/iso_test.go, test/e2e/iso_test.go
  - Reads: REQUIREMENTS SRV-15, CAT-08; RESEARCH R5, R6 (attach_iso); DECISIONS "Q5 SRV-15 proved by SSH into the live ISO"
  - Size: ~180 lines in 3 files
  - Adjusts: -
  - [ ] `attach_iso` (`iso` by name or ID; unknown or deprecated: 422 `invalid_input`): `EnsureISO` inside the action, `SetDVD` slot 1, boot order DVD slot 1 then LUN 0, server `iso` object; replaces an attached ISO; `detach_iso` ejects and restores disk-first; a server without ISO answers `detach_iso` with success and no change; rescue boot order keeps LUN 63 first (internal/resources/servers/iso.go, servers.go, reconcile.go)

- [ ] 04-14 Documentation
  - Wave: 9 | Depends on: 04-10 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06 | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC "Documentation"; REQUIREMENTS DOC; CONTEXT.md "Documentation"; the summaries of 04-01, 04-05, 04-09, 04-10
  - Size: ~160 lines in 4 files
  - Adjusts: -
  - [ ] docs/user/deviations.md: rescue runs from its own disk (not RAM), auto-disable is noticed only for boots hlab drives (a guest-initiated reboot into rescue is ended by the 60 min timeout), ISO catalog instead of Hetzner's ISOs, udev/automount hooks only on hlab-created disks, snapshots of running servers crash-consistent via a checkpoint, backups only while the cloud runs and not made up, the chosen error codes (`volume_already_attached`, `invalid_input` for location, backup and disk-size cases, deleting an attached volume)
  - [ ] docs/user/configuration.md (`isos.json`, `appliance-debs.json`, data folders `images`, `isos`, `volumes`, `cache\debs`), README.md (snapshots, backups, rescue, ISOs, volumes), docs/dev/components.md (agent jobs, appliance debs)
