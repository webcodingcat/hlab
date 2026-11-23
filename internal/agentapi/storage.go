package agentapi

import "net/netip"

// StorageSchema is the version of Storage this package describes.
const StorageSchema = 1

// Storage is the full desired state of the storage VM, applied
// idempotently by the storage agent (PUT /v1/storage).
type Storage struct {
	Schema, Generation int
	PublicRange        netip.Prefix // sources outside it, or inside HostNet, are external
	HostNet            netip.Prefix // the host's transit /30
	TLS                TLS          // the wildcard certificate of WebDAV and FTPS
	Boxes              []Box
}

// TLS is the certificate *.storagebox.hetzner.internal from the hlab CA:
// leaf plus CA chain, and the PKCS#8 key. Never logged.
type TLS struct{ CertPEM, KeyPEM string }

// Box is one storage box.
type Box struct {
	ID          int64
	User        string // "u100000"; the UID is the number
	IP          netip.Addr
	QuotaBytes  int64 // size of the type
	Hashes      Hashes
	SSHKeys     []string // OpenSSH lines, written once into .ssh/authorized_keys at creation
	Access      Access   // SSH, Samba, WebDAV, ZFS, ReachableExternally
	Subaccounts []Subaccount
	// Snapshots are the names of the box's snapshots in the store; the
	// agent removes snapshot subvolumes of the box that are not listed and
	// never one that is.
	Snapshots []string
}

// Subaccount is a subaccount of a box.
type Subaccount struct {
	User    string // "u100000-sub1"
	HomeDir string // relative to the box's data, no leading slash; created when missing
	Hashes  Hashes
	Access  Access // SSH, Samba, WebDAV, ReadOnly, ReachableExternally
}

// Hashes are an account's sha512-crypt and NT password hashes; never logged.
type Hashes struct{ Crypt, NT string }

// Access holds the switches of an account.
type Access struct{ SSH, Samba, WebDAV, ZFS, ReadOnly, ReachableExternally bool }

// Storage job kinds.
const (
	StorageJobFolders        = "folders"
	StorageJobUsage          = "usage"
	StorageJobSnapshot       = "snapshot"        // read-only snapshot <Snapshot> of box <Box>
	StorageJobDeleteSnapshot = "delete-snapshot" // removes snapshot <Snapshot>
	StorageJobRollback       = "rollback"        // box data back to <Snapshot>, then removes <Newer>
)

// StorageJob is work on the storage VM (POST /v1/jobs).
type StorageJob struct {
	Kind     string   // one of the StorageJob* kinds
	Box      string   // box user
	Snapshot string   // snapshot, delete-snapshot, rollback: the snapshot name
	Path     string   // folders: relative path, "." by default
	Newer    []string // rollback: the snapshots created after Snapshot, removed with it
}

// Usage is the space a box takes, in bytes, from its qgroups.
type Usage struct{ Data, Snapshots int64 }

// StorageJobResult is the outcome of a StorageJob.
type StorageJobResult struct {
	Folders       []string // names of direct subdirectories, sorted
	Usage         Usage
	SnapshotSizes map[string]int64 // name -> exclusive bytes (stats.size); usage and snapshot jobs
	SnapshotFS    map[string]int64 // name -> referenced bytes (stats.size_filesystem)
}

// Codes of a failed storage job, answered as StorageJobError: HTTP 404 with
// JobErrNotFound for a missing folders path, 422 with JobErrInvalidPath for
// an absolute or escaping one.
const (
	JobErrNotFound    = "not_found"
	JobErrInvalidPath = "invalid_path"
)

// StorageJobError is the body of a failed storage job.
type StorageJobError struct {
	Error string `json:"error"`
}

// StorageVM is the storage VM as the appliance sees it; Addr invalid: no
// storage VM.
type StorageVM struct {
	Addr netip.Addr // transit Addrs.Storage
	MAC  string     // "02:48:4c:00:00:03"
}

// StorageBoxRoute is a box IP the appliance routes to the storage VM.
type StorageBoxRoute struct {
	IP                  netip.Addr
	Names               []string // "u100000", "u100000-sub1": labels below storagebox.hetzner.internal
	ReachableExternally bool     // box or any of its subaccounts
}
