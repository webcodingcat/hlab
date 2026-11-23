# Phase 20: SMB, WebDAV, FTP and snapshots

## Interfaces

```go
// internal/agentapi (storage.go): additions to the storage VM's document
// and jobs; existing fields stay.
const (
	StorageJobFolders        = "folders"
	StorageJobUsage          = "usage"
	StorageJobSnapshot       = "snapshot"        // read-only snapshot <Snapshot> of box <Box>
	StorageJobDeleteSnapshot = "delete-snapshot" // removes snapshot <Snapshot>
	StorageJobRollback       = "rollback"        // box data back to <Snapshot>, then removes <Newer>
)

type Storage struct {
	Schema, Generation int
	PublicRange        netip.Prefix
	HostNet            netip.Prefix
	TLS                TLS // the wildcard certificate of WebDAV and FTPS
	Boxes              []Box
}

// TLS is the certificate *.storagebox.hetzner.internal from the hlab CA:
// leaf plus CA chain, and the PKCS#8 key. Never logged.
type TLS struct{ CertPEM, KeyPEM string }

type Box struct {
	// ... existing fields ...
	// Snapshots are the names of the box's snapshots in the store; the
	// agent removes snapshot subvolumes of the box that are not listed and
	// never one that is.
	Snapshots []string
}

type StorageJob struct {
	Kind     string
	Box      string   // box user
	Snapshot string   // snapshot, delete-snapshot, rollback: the snapshot name
	Path     string   // folders
	Newer    []string // rollback: the snapshots created after Snapshot, removed with it
}

type StorageJobResult struct {
	Folders       []string
	Usage         Usage
	SnapshotSizes map[string]int64 // name -> exclusive bytes (stats.size); usage and snapshot jobs
	SnapshotFS    map[string]int64 // name -> referenced bytes (stats.size_filesystem)
}

// internal/platform (platform.go): VMSpec gets dynamic memory.
type VMSpec struct {
	// ... existing fields ...
	MemoryMB      int64 // startup memory; with DynamicMemory also the maximum
	DynamicMemory bool  // dynamic memory, minimum 512 MB; false: static
}

// internal/resources/storageboxes (snapshots.go)
const SnapshotKind = "storage_box_snapshots" // store kind; action resource type "storage_box_snapshot"

type SnapshotStats struct {
	Size           int64 `json:"size"`
	SizeFilesystem int64 `json:"size_filesystem"`
}
type Snapshot struct {
	ID          int64             `json:"id"`
	StorageBox  int64             `json:"storage_box"`
	Name        string            `json:"name"`        // manual "2006-01-02T15-04-05" UTC, automatic "snapshot-daily--" + same
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	Stats       SnapshotStats     `json:"stats"`
	IsAutomatic bool              `json:"is_automatic"`
	Created     time.Time         `json:"created"`
}
type SnapshotHLab struct {
	Creating bool `json:"creating,omitempty"` // create_snapshot not yet confirmed; Recover deletes it
}
type SnapshotRecord struct {
	Snapshot
	HLab SnapshotHLab `json:"_hlab"`
}

// internal/resources/storageboxes (snapplan.go), pure
// Due reports whether plan p runs at a minute in (from, to], in UTC;
// day_of_week (1 Monday .. 7 Sunday) and day_of_month both must match
// when both are set (AND).
func Due(p SnapshotPlan, from, to time.Time) bool
// Prune returns the automatic snapshots of snaps beyond the newest max,
// oldest first; manual snapshots are never returned.
func Prune(snaps []Snapshot, max int) []Snapshot
// PlanEvery is the interval of the snapshot plan job.
const PlanEvery = time.Minute

// internal/storageagent: OS-independent renderers (tested on Windows) and
// the service frame (linux).
type SambaFiles struct {
	Global   string               // /etc/samba/smb.conf
	PerIP    map[netip.Addr]string // /etc/samba/ip-<addr>.conf, shares of that box IP
	Accounts []string             // accounts that get a tdbsam entry (samba on, not skipped)
}
func SambaConfig(doc agentapi.Storage, skipped map[string]bool) SambaFiles

type WebDAVFiles struct {
	Site   string            // the one Apache site: Listen and vhosts of every box IP
	Passwd map[string]string // account -> htpasswd line content file (crypt hash)
}
func WebDAVConfig(doc agentapi.Storage, skipped map[string]bool, tlsDir string) WebDAVFiles

type FTPFiles struct {
	Conf   string            // /etc/vsftpd.conf
	Users  map[string]string // user_config_dir file per account
	Access string            // /etc/security/vsftpd-access.conf (pam_access)
}
func FTPConfig(doc agentapi.Storage, skipped map[string]bool, tlsDir string) FTPFiles

// services_linux.go: a protocol daemon configured at every apply after the
// accounts and passwords; each service file registers itself in init.
type service interface {
	Name() string
	Apply(ctx context.Context, doc agentapi.Storage, skipped map[string]bool) error
}
func registerService(s service)
const tlsDir = "/etc/hlab-agent/tls" // storagebox.crt, storagebox.key (0600)

// plan.go: State gets the snapshot subvolumes found per box, Plan a step
// that removes an unlisted one.
type State struct {
	Filesystem bool
	Boxes      []string
	Subvolumes []string
	Snapshots  map[string][]string // box user -> snapshot names found
}
const StepRemoveSnapshot StepKind = ... // Box, Snapshot set
type Step struct {
	Kind     StepKind
	Box      string
	Snapshot string
}
```

## Plans

- [x] 20-01 Storage contract for snapshots and TLS
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SBOX-05, SBOX-15, SBOX-16, SBOX-18 (contract only) | Tests: -
  - Reads: ARCHITECTURE "Storage VM"; PLAN.md "Interfaces"; internal/agentapi/storage.go
  - Size: ~60 lines in 1 file
  - [x] Add the job kinds, `Storage.TLS`, `TLS`, `Box.Snapshots`, `StorageJob.Newer` and `StorageJobResult.SnapshotFS` in internal/agentapi/storage.go, with doc comments as in "Interfaces"; update the comment of the job kinds.

- [x] 20-02 Storage VM packages and memory
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SBOX-14, SBOX-15, SBOX-16 (prerequisites) | Tests: -
  - Reads: SPEC "Stack" (storage VM); RESEARCH.md 1.1, 1.2, 1.3, 3 (first point); CONTEXT.md; catalog/storage-debs.json; appliance/hlab-agent-install.sh; appliance/storage-user-data.yaml; internal/platform/platform.go VMSpec; internal/platform/hyperv/hyperv.go CreateVM, SetVM; internal/cloud/storage.go
  - Size: ~300 lines in 5 files (most of it catalog entries)
  - [x] Add the 32 packages of RESEARCH.md 1.2 to catalog/storage-debs.json (format of the existing entries, `lsb-base` with the values of appliance-debs.json). Resolve each URL as a snapshot.debian.org URL through `/mr/binary/<pkg>/<version>/binfiles`, download each file and recompute its SHA-256 and size; never commit a hash from RESEARCH.md unchecked.
  - [x] In appliance/hlab-agent-install.sh install the Samba, Apache and vsftpd groups each while its tool is missing (`smbd`, `apache2`, `vsftpd`), independently of btrfs, so storage VMs of phase 19 get them at their next boot. Use a `policy-rc.d` exiting 101 during `dpkg -i`; afterwards disable `nmbd`, `smbd`, `apache2` and `vsftpd` until the agent configures them; remove Apache's `ports.conf` Listen lines and `sites-enabled/000-default.conf`; `a2dismod -f mpm_event` and `a2enmod mpm_prefork mpm_itk ssl dav dav_fs auth_basic authn_file authz_user authz_host`. Keep the rule that a failure does not keep the agent down.
  - [x] Add `VMSpec.DynamicMemory` (platform.go) and use it in hyperv.go CreateVM and SetVM (`-DynamicMemory`, minimum 512 MB, startup and maximum MemoryMB); the storage VM in internal/cloud/storage.go gets 2048 MB dynamic (SetVM already runs on an off VM at start, so existing VMs change too). Other VMs stay static.

- [x] 20-03 Appliance lets the protocol ports through to box IPs
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SBOX-14, SBOX-15, SBOX-16 (network) | Tests: -
  - Reads: ARCHITECTURE "Storage VM" (Network); DECISIONS "Storage box firewall chains only drop or return", "Only SSH ports reach box IPs in phase 19"; internal/agent/firewall_linux.go addStorageBoxRules
  - Size: ~40 lines in 1 file
  - [x] Extend `storageBoxPorts` in internal/agent/firewall_linux.go to TCP 21, 22, 23, 443, 445 and add one range rule for TCP 50000-50099 (passive FTP); the chain still only drops or returns, and 9100 stays dropped.

- [x] 20-04 Storage agent service frame and TLS files
  - Wave: 2 | Depends on: 20-01 | Review: alone
  - Requirements: SBOX-14, SBOX-15, SBOX-16 (frame) | Tests: -
  - Reads: PLAN.md "Interfaces"; DECISIONS "One failing box or subaccount does not stop the storage apply", "Storage agent logic as pure functions"; internal/storageagent/apply_linux.go apply
  - Size: ~120 lines in 2 files
  - [x] New internal/storageagent/services_linux.go: `service`, `registerService`, and `applyServices` that writes `doc.TLS` to `tlsDir` (key 0600, root only; written only when changed, never logged) and runs every registered service; a failing service is logged and joined into the apply error, the others still run.
  - [x] Call `applyServices` in apply (internal/storageagent/apply_linux.go) after `applySSHD`.

- [x] 20-05 Storage TLS certificate in hlabd
  - Wave: 2 | Depends on: 20-01 | Review: alone
  - Requirements: SBOX-15, SBOX-16 (certificate) | Tests: -
  - Reads: CONTEXT.md (Q1); ARCHITECTURE "ca", "Storage VM"; internal/cloud/storage_life.go applyStorage; internal/cloud/ca.go; internal/ca/ca.go Issue
  - Size: ~90 lines in 2 files
  - [x] New internal/cloud/storage_tls.go: `storageTLS()` returns the certificate `*.storagebox.hetzner.internal` kept in kv (`storage.tls_cert`, `storage.tls_key`), issued by `ca.Issue` when missing, not parseable, or expiring within 30 days.
  - [x] Put it into `agentapi.Storage.TLS` in applyStorage (internal/cloud/storage_life.go).

- [x] 20-06 Snapshot resource in the Hetzner API
  - Wave: 2 | Depends on: 20-01 | Review: alone
  - Requirements: SBOX-05 | Tests: internal/resources/storageboxes/snapshots_test.go
  - Reads: REQUIREMENTS SBOX-05; hetzner.spec.json paths `/storage_boxes/{id}/snapshots` and `/storage_boxes/{id}/snapshots/{snapshot_id}` (parameters, bodies, responses, examples); RESEARCH.md 5 (names, limit); DECISIONS "Subaccounts stored at create, listed without paging", "Every storage box action locks the box", "Storage box not-active guard and Recover", "Storage box coverage rows in one plan"; internal/resources/storageboxes/{storageboxes.go,subaccounts.go,document.go,recover.go}; internal/api/deviations.go
  - Size: ~400 lines in 6 files
  - [x] New internal/resources/storageboxes/snapshots.go: kind `storage_box_snapshots`; routes GET (list with `name`, `label_selector`, `is_automatic`, sort id/name/created through `api.ListAllRequest`, no paging) and POST on `/storage_boxes/{id}/snapshots`, GET, PUT (description and labels, synchronous; labels replace) and DELETE on `/{snapshot_id}`. A snapshot of another box is 404. Create: box active, description checked (max 1000, the description's pattern, 422 `invalid_input`), manual snapshots below the type's `snapshot_limit` (null: no limit) else 403 `resource_limit_exceeded` with `details.limits [{"name":"snapshot_limit"}]`; record with the UTC name (the next free second when the name is taken in the box) and `_hlab.creating`, action `create_snapshot` on box and snapshot, work = job `snapshot`, then clear `creating` and set stats from the job result; on failure the record is deleted. Delete: action `delete_snapshot`, job `delete-snapshot`, then the record goes. Register the routes in storageboxes.go `Routes`.
  - [x] `removeBox` (storageboxes.go) also deletes the box's snapshot records; `docBox` (document.go) fills `Box.Snapshots` with the names of all its snapshot records; `Recover` (recover.go) deletes snapshot records still `creating`.
  - [x] Add `api.Deviations()` entries (internal/api/deviations.go: the limit answer, the name taken within one second) and move the snapshot endpoints in docs/development/api-coverage.md from "Not served yet" to served, with those entries.

- [ ] 20-07 Storage agent snapshot jobs and `.zfs`
  - Wave: 3 | Depends on: 20-04, 20-06 | Review: alone
  - Requirements: SBOX-05, SBOX-19, SBOX-07 | Tests: internal/storageagent/plan_test.go; test/e2e/storagebox_snapshot_test.go (SBOX-05, SBOX-19); test/e2e/life_test.go (SBOX-07)
  - Reads: E18; ARCHITECTURE "Storage VM"; DECISIONS "Storage VM on-disk layout", "Storage box quota with full qgroups"; RESEARCH.md 5 (`.zfs`), 6 (layout); internal/storageagent/{plan.go,agent.go,jobs_linux.go,apply_linux.go}
  - Size: ~380 lines in 5 files
  - [ ] Plan (plan.go): `State.Snapshots`, `StepRemoveSnapshot` for each snapshot subvolume of a box in the document that `Box.Snapshots` lacks; boxes absent from the document lose their snapshots with the box; a listed snapshot is never removed; the snapshot folder is never taken for a box. state and removeBox in apply_linux.go read and remove `<mount>/.snapshots/<user>/<name>`.
  - [ ] New internal/storageagent/snapshots_linux.go: jobs `snapshot` (`btrfs subvolume snapshot -r -i 1/<n>`, name checked against `^[A-Za-z0-9][A-Za-z0-9._-]*$`, then the E18 accounting check; result with the snapshot's sizes) and `delete-snapshot` (`not_found` when missing); register both in postJob (agent.go); the `usage` job (jobs_linux.go) also returns `SnapshotSizes` and `SnapshotFS` of all snapshots of the box from `qgroup show --raw`.
  - [ ] `.zfs`: with `Access.ZFS` the box account's chroot home gets `.zfs/snapshot` as a read-only bind of the box's snapshot folder (bind plus read-only remount) at every apply, removed when ZFS is off; subaccounts never get it (apply_linux.go, snapshots_linux.go).

- [x] 20-08 Samba
  - Wave: 3 | Depends on: 20-02, 20-03, 20-04 | Review: alone
  - Requirements: SBOX-14, SBOX-12, SBOX-04 | Tests: internal/storageagent/smb_test.go; test/e2e/storagebox_protocols_test.go (SBOX-14, SBOX-12/smb, SBOX-04/smb)
  - Reads: E16, E17, E19; RESEARCH.md 2; DECISIONS "Storage agent sets hashes through chpasswd" (no secret in argv); internal/storageagent/sshd.go (how accounts are rendered)
  - Size: ~300 lines in 2 files
  - [x] New internal/storageagent/smb.go: `SambaConfig`. Global: standalone server, `server min protocol = SMB2_10`, `smb ports = 445`, `disable netbios = yes`, `map to guest = never`, `restrict anonymous = 2`, `durable handles = no`, `bind interfaces only = yes` on the box IPs, `include = /etc/samba/ip-%i.conf`. Per box IP: share `backup` (box data, owned by the box) when the box has Samba on, one share per subaccount with Samba on named after it (its home, `read only = yes` for read-only ones), `valid users` the one account, `hosts allow = <PublicRange> EXCEPT <HostNet>` for an account without reachable_externally; skipped accounts and boxes left out; `Accounts` lists the accounts with Samba on.
  - [x] New internal/storageagent/smb_linux.go: a registered service that writes the files atomically (plus an empty `ip-0.0.0.0.conf`, removes includes of gone IPs), runs `testparm -s`, sets the NT hashes of `Accounts` through `pdbedit` with the hash on stdin or a 0600 temp file (never argv), removes tdbsam entries of other accounts, enables smbd at the first apply and reloads with `smbcontrol smbd reload-config`.

- [x] 20-09 WebDAV over HTTPS
  - Wave: 3 | Depends on: 20-02, 20-03, 20-04, 20-05 | Review: alone
  - Requirements: SBOX-15, SBOX-12, SBOX-04 | Tests: internal/storageagent/webdav_test.go; test/e2e/storagebox_protocols_test.go (SBOX-15, SBOX-12/webdav, SBOX-04/webdav)
  - Reads: E17, E19; RESEARCH.md 1.3 (prefork limits), 3; CONTEXT.md (Q1)
  - Size: ~300 lines in 2 files
  - [x] New internal/storageagent/webdav.go: `WebDAVConfig`. Prefork limits (StartServers 2, MinSpareServers 2, MaxSpareServers 4, MaxRequestWorkers 20); per box IP `Listen <ip>:443`, first a catch-all vhost (`AssignUserID nobody nogroup`, `Require all denied`), then one vhost per account with WebDAV on: `ServerName` its name under `storagebox.hetzner.internal`, the wildcard certificate from `tlsDir`, `AssignUserID` the box user, DocumentRoot its data (chroot home path), `Dav On`, per-account `DavLockDB` in a folder of the box user, basic auth from its passwd file; read-only accounts `<RequireAll> Require valid-user, Require method GET HEAD OPTIONS PROPFIND`; accounts without reachable_externally also `Require ip <PublicRange>` and `Require not ip <HostNet>`.
  - [x] New internal/storageagent/webdav_linux.go: a registered service that writes the site and passwd files (crypt hashes, 0640 root:www-data), runs `apachectl configtest`, enables apache2 at the first apply and reloads it gracefully, then checks with `ss -ltn` that every box IP listens on 443.

- [ ] 20-10 FTP and explicit FTPS
  - Wave: 3 | Depends on: 20-02, 20-03, 20-04, 20-05 | Review: alone
  - Requirements: SBOX-16, SBOX-12, SBOX-04 | Tests: internal/storageagent/ftp_test.go; test/e2e/storagebox_protocols_test.go (SBOX-16, SBOX-12/ftp, SBOX-04/ftp)
  - Reads: E17; RESEARCH.md 4 (the `pam_access` rules after the wrong-direction example); CONTEXT.md (Q2)
  - Size: ~250 lines in 2 files
  - [ ] New internal/storageagent/ftp.go: `FTPConfig`. vsftpd.conf: `listen=YES`, `listen_ipv6=NO`, no `listen_address` or `pasv_address`, passive 50000-50099, local users chrooted to `/srv/chroot/$USER/home`, `ssl_enable=YES` with the wildcard certificate, TLS 1.2 and 1.3 only, modern `ssl_ciphers`, `require_ssl_reuse=NO`, `force_local_logins_ssl=NO`, `force_local_data_ssl=NO`, `reverse_lookup_enable=NO`, `user_config_dir`; per account a file with `write_enable=NO` for read-only ones; `Access`: per subaccount without reachable_externally `+:<user>:<PublicRange> EXCEPT <HostNet>` then `-:<user>:ALL`, ending with `+:ALL:ALL`.
  - [ ] New internal/storageagent/ftp_linux.go: a registered service that writes the files, adds `account required pam_access.so accessfile=/etc/security/vsftpd-access.conf` to `/etc/pam.d/vsftpd` before the common-account include (once), enables vsftpd at the first apply and sends SIGHUP only when vsftpd.conf or the certificate changed. If the e2e row SBOX-04/ftp still fails after three repair attempts, the plan stops (CONTEXT.md Q2).

- [ ] 20-11 Rollback in the storage agent
  - Wave: 4 | Depends on: 20-07 | Review: alone
  - Requirements: SBOX-18 | Tests: - (its e2e row is in 20-13)
  - Reads: E17, E18; RESEARCH.md 6; CONTEXT.md (subaccount sessions); internal/storageagent/{agent.go,snapshots_linux.go,apply_linux.go} (bind, remount, ensureChroot)
  - Size: ~300 lines in 2 files
  - [ ] New internal/storageagent/rollback_linux.go, job `rollback` in the order of RESEARCH.md 6: fence the box IP's ports 21, 22, 23, 443, 445, 50000-50099 with a reject rule through `github.com/google/nftables` (own table, removed at the end also on failure); end the sessions of the box and all its subaccounts (`ss -K` on the box IP's sockets, kill of the PIDs `ss -tnp` shows when `ss -K` is not supported, `pkill -KILL -U <box uid>`), delete the box's DavLockDB files, wait with a deadline until `fuser -m` on the box's mounts is empty; unmount `.zfs/snapshot`, the subaccount homes and the box home; swap the data with a writable snapshot `-i 1/<n>` of the target, remove the `Newer` snapshots and the old data, `subvolume sync` with a deadline, `qgroup clear-stale`, the E18 accounting check; recreate missing subaccount homes with the box UID; bind everything again. A failure before the swap leaves the data untouched.
  - [ ] Register `rollback` in postJob (agent.go); a missing target snapshot is `not_found`.

- [x] 20-12 Snapshot plan runs
  - Wave: 3 | Depends on: 20-06 | Review: alone
  - Requirements: SBOX-17 | Tests: internal/resources/storageboxes/snapshots_test.go (SBOX-17)
  - Reads: REQUIREMENTS SBOX-17, SBOX-20; hetzner.spec.json `enable_snapshot_plan` description; RESEARCH.md 5; CONTEXT.md (AND); ARCHITECTURE "scheduler"; internal/resources/storageboxes/{stats.go,snapshots.go}
  - Size: ~250 lines in 5 files
  - [x] New internal/resources/storageboxes/snapplan.go: `Due`, `Prune`, and the job `storage box snapshot plans` every `PlanEvery` on `Deps.Clock`: for each active box with a plan whose `Due` falls in (`_hlab.plan_checked`, now], it stores `plan_checked`, starts `create_snapshot` through `StartQueued` (it waits behind running actions of the box) for an automatic snapshot `snapshot-daily--<UTC time>`, and after its success deletes the snapshots `Prune` returns (jobs `delete-snapshot`, then records). Times missed while the cloud was stopped are not caught up (`plan_checked` starts at the cloud start when older).
  - [x] Add the job to `Jobs()` and fill the snapshot stats from `SnapshotSizes` and `SnapshotFS` in the stats refresh (stats.go); add the `HLab.PlanChecked` field (storageboxes.go).
  - [x] `api.Deviations()` entry and api-coverage.md line: missed plan times are skipped (internal/api/deviations.go, docs/development/api-coverage.md).

- [ ] 20-13 rollback_snapshot action
  - Wave: 5 | Depends on: 20-11, 20-12 | Review: alone
  - Requirements: SBOX-18 | Tests: internal/resources/storageboxes/snapshots_test.go (SBOX-18); test/e2e/storagebox_snapshot_test.go (SBOX-18)
  - Reads: hetzner.spec.json `/storage_boxes/{id}/actions/rollback_snapshot`; RESEARCH.md 5 (rollback); internal/resources/storageboxes/{actions.go,snapshots.go}
  - Size: ~150 lines in 4 files
  - [ ] New internal/resources/storageboxes/rollback.go: `POST /storage_boxes/{id}/actions/rollback_snapshot` with `snapshot` as ID or name (a missing one 404 `not_found`), action `rollback_snapshot` on box and snapshot, job `rollback` with `Newer` = all snapshots of the box created after the target (manual and automatic), then their records go and the stats refresh; register the route in `actionRoutes` (actions.go).
  - [ ] `api.Deviations()` entry if any, and the endpoint in docs/development/api-coverage.md as served (internal/api/deviations.go, docs/development/api-coverage.md).

- [ ] 20-14 Documentation
  - Wave: 6 | Depends on: 20-01 to 20-13 | Review: alone
  - Requirements: DOC-01 to DOC-06 (kept green), DOC-07 (manual) | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC "Documentation"; REQUIREMENTS DOC; docs/guide.md "Storage boxes"; docs/deviations.md "Storage boxes"; docs/configuration.md (catalog files); docs/development/api-coverage.md storage box part
  - Size: ~250 lines in 4 files
  - [ ] docs/guide.md: SMB (share `backup`, subaccount shares, Windows `net use` and smbclient), WebDAV (URLs per account, trusting the hlab CA), FTP and FTPS (passive only, lftp example), snapshots (create, list, rollback, snapshot plan in UTC, `.zfs/snapshot`), each example run through `hlab-docs` where it is an hcloud command.
  - [ ] docs/deviations.md (about 250 lines at most): remove "snapshot plan is stored only"; add missed plan times skipped, NTLM risk from Windows, certificate from the hlab CA, credentials of one box at another box's address over FTP, passive FTP only, limit answer for manual snapshots, rollback ends open sessions of the box and its subaccounts.
  - [ ] docs/configuration.md (storage VM packages) and docs/development/api-coverage.md (wording of the served snapshot endpoints and the protocols).
