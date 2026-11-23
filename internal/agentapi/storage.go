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
	Boxes              []Box
}

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

// Storage job kinds; "snapshot", "rollback" and "delete-snapshot" follow.
const (
	StorageJobFolders = "folders"
	StorageJobUsage   = "usage"
)

// StorageJob is work on the storage VM (POST /v1/jobs).
type StorageJob struct {
	Kind     string // StorageJobFolders | StorageJobUsage
	Box      string // box user
	Snapshot string
	Path     string // folders: relative path, "." by default
}

// Usage is the space a box takes, in bytes, from its qgroups.
type Usage struct{ Data, Snapshots int64 }

// StorageJobResult is the outcome of a StorageJob.
type StorageJobResult struct {
	Folders       []string // names of direct subdirectories, sorted
	Usage         Usage
	SnapshotSizes map[string]int64
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
