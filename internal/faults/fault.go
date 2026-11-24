// Package faults holds the active faults of the cloud's projects in memory:
// it validates them, runs their effects and ends them on expiry, on request,
// when their target or project goes and at cloud stop.
package faults

import (
	"context"
	"errors"
	"time"

	"github.com/webcodingcat/hlab/internal/scheduler"
)

// Kinds.
const (
	KindServerCrash           = "server_crash"
	KindServerFreeze          = "server_freeze"
	KindNetwork               = "network"
	KindLatency               = "latency"
	KindAPIError              = "api_error"
	KindRateLimit             = "rate_limit"
	KindVolumeUnavailable     = "volume_unavailable"
	KindDiskSlow              = "disk_slow"
	KindStorageBoxUnavailable = "storage_box_unavailable"
)

// MaxChosenSeed is the largest seed hlab chooses, and the largest the MCP
// tool accepts (2^53-1).
const MaxChosenSeed = 1<<53 - 1

// Target types.
const (
	TargetServer     = "server"
	TargetVolume     = "volume"
	TargetStorageBox = "storage_box"
	TargetLocations  = "locations"
)

// Target is what a fault hits: a server, volume or storage box by ID, or
// the traffic between two locations.
type Target struct {
	Type string `json:"type"`
	ID   int64  `json:"id,omitempty"`   // server, volume, storage_box
	From string `json:"from,omitempty"` // locations
	To   string `json:"to,omitempty"`
}

// Fault is the fault object of the management API (params {} when a kind
// has none).
type Fault struct {
	ID      int64          `json:"id"`
	Project string         `json:"project"`
	Kind    string         `json:"kind"`
	Target  *Target        `json:"target"`
	Params  map[string]any `json:"params"`
	Seed    *uint64        `json:"seed"`
	Created time.Time      `json:"created"`
	Ends    *time.Time     `json:"ends"`
}

// Request is the body of POST /mgmt/v1/projects/{name}/faults.
type Request struct {
	Kind     string         `json:"kind"`
	Target   *Target        `json:"target"`
	Params   map[string]any `json:"params"`
	Seed     *uint64        `json:"seed"`
	Duration string         `json:"duration"` // Go duration, "" = none
}

// Spec is a request with its duration parsed; Duration 0 = none.
type Spec struct {
	Kind     string
	Target   *Target
	Params   map[string]any
	Seed     *uint64
	Duration time.Duration
}

// Spec parses Duration; an unparsable one or one outside 1 s to 24 h is an
// *InputError on "duration".
func (r Request) Spec() (Spec, error) {
	s := Spec{Kind: r.Kind, Target: r.Target, Params: r.Params, Seed: r.Seed}
	if r.Duration == "" {
		return s, nil
	}
	d, err := time.ParseDuration(r.Duration)
	if err != nil {
		return Spec{}, &InputError{Field: "duration", Message: "duration " + r.Duration + " is not a duration like 5m"}
	}
	if err := checkDuration(d); err != nil {
		return Spec{}, err
	}
	s.Duration = d
	return s, nil
}

// InputError is invalid input (management API: 400 invalid_input).
type InputError struct{ Field, Message string }

func (e *InputError) Error() string { return e.Field + ": " + e.Message }

var (
	// ErrNotFound is an unknown fault ID in the project or an unknown
	// target (wrapped with a message naming it).
	ErrNotFound = errors.New("not found")
	// ErrConflict is a second fault of the same kind on the same target
	// (for api_error and rate_limit: in the project).
	ErrConflict = errors.New("conflict")
)

// Effect makes a kind act; Apply runs at create (a failure refuses the
// fault), Undo when the fault ends, before it leaves the list.
type Effect interface {
	Apply(ctx context.Context, f Fault) error
	Undo(ctx context.Context, f Fault) error
}

// TargetLookup checks that t exists in project: ErrNotFound (wrapped) for
// an unknown server, volume, storage box or location, an *InputError for a
// volume that is not attached when kind is volume_unavailable or disk_slow.
type TargetLookup func(ctx context.Context, project, kind string, t Target) error

// Options configure a Manager.
type Options struct {
	Clock  scheduler.Clock // nil = scheduler.RealClock()
	Lookup TargetLookup    // nil = every target exists
	// Effects per kind. api_error and rate_limit are always available
	// (their effect is Middleware); any other kind without an entry is
	// refused with an *InputError "kind ... is not available yet".
	Effects map[string]Effect
	// Resources are the first path segments api_error's "resource" may
	// name (api.Router.Resources()); nil = any.
	Resources []string
}
