package state

import "time"

// Project is a project of the lab, as on Hetzner: it holds resources, and its
// tokens reach only them.
type Project struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	Limits   Limits    `json:"limits"`
	Internet bool      `json:"internet"` // whether its servers reach the Internet
}

// RecordID returns the project's ID.
func (p Project) RecordID() int64 { return p.ID }

// Limits are a project's resource limits, as Hetzner sets them per project.
// Zero means no limit: a field added later loads from older state files as
// zero, so it needs no migration.
type Limits struct {
	Servers       int `json:"servers"`
	Cores         int `json:"cores"`
	PrimaryIPs    int `json:"primary_ips"`
	FloatingIPs   int `json:"floating_ips"`
	VolumeGB      int `json:"volume_gb"`
	Networks      int `json:"networks"`
	LoadBalancers int `json:"load_balancers"`
}

// Permission is what a token may do.
type Permission string

// The permissions of a token, as on Hetzner.
const (
	PermissionRead      Permission = "read"
	PermissionReadWrite Permission = "read_write"
)

// Token is an API token of a project. Only its hash is kept: the token itself
// is shown once, when it is created.
type Token struct {
	ID         int64      `json:"id"`
	ProjectID  int64      `json:"project_id"`
	Name       string     `json:"name"`
	Created    time.Time  `json:"created"`
	Permission Permission `json:"permission"`
	Hash       string     `json:"hash"`   // SHA-256 of the token, hex
	Prefix     string     `json:"prefix"` // its first characters, to tell tokens apart
}

// RecordID returns the token's ID.
func (t Token) RecordID() int64 { return t.ID }

// SSHKey is an SSH key of a project.
type SSHKey struct {
	ID          int64             `json:"id"`
	ProjectID   int64             `json:"project_id"`
	Name        string            `json:"name"`
	Labels      map[string]string `json:"labels"`
	Created     time.Time         `json:"created"`
	Fingerprint string            `json:"fingerprint"` // MD5, unique in the project
	PublicKey   string            `json:"public_key"`
}

// RecordID returns the key's ID.
func (k SSHKey) RecordID() int64 { return k.ID }

// ActionStatus is how far an action is.
type ActionStatus string

// The statuses of an action, as on Hetzner.
const (
	ActionRunning ActionStatus = "running"
	ActionSuccess ActionStatus = "success"
	ActionError   ActionStatus = "error"
)

// Action is an action of a project: the API returns one for every change, and
// clients poll it while they wait.
type Action struct {
	ID        int64         `json:"id"`
	ProjectID int64         `json:"project_id"`
	Command   string        `json:"command"` // Hetzner's name, such as "stop_server"
	Status    ActionStatus  `json:"status"`
	Progress  int           `json:"progress"`
	Started   time.Time     `json:"started"`
	Finished  *time.Time    `json:"finished"`
	Error     *Failure      `json:"error"`
	Resources []ResourceRef `json:"resources"`
}

// RecordID returns the action's ID.
func (a Action) RecordID() int64 { return a.ID }

// Failure is why an action failed: Hetzner's "error" of an action.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ResourceRef names a resource by its type, as actions name it ("server"), and
// its ID.
type ResourceRef struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
}

// Lab is the lab's own records, which the API does not show. Later phases add
// the appliance, the address pools, the image cache, the Internet clients and
// the ACME keys.
type Lab struct {
	Faults map[int64]ProjectFaults `json:"faults"` // by project ID
}

// ProjectFaults are the faults injected into a project.
type ProjectFaults struct {
	API APIFault `json:"api"`
}

// APIFault makes the project's next API calls fail: Count calls answer with
// Status, each after DelayMS.
type APIFault struct {
	Status  int `json:"status"`
	Count   int `json:"count"`
	DelayMS int `json:"delay_ms"`
}
