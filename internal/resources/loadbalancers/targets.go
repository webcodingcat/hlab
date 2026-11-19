package loadbalancers

import (
	"encoding/json"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/store"
)

// Target is a stored target of a load balancer.
type Target struct {
	Type string `json:"type"`
}

// TargetView is a target as the API shows it.
type TargetView struct {
	Type string `json:"type"`
}

// targetRoutes registers add_target and remove_target.
func (m *Module) targetRoutes(resource.Registrar) {}

// createTargets adds the targets of a create request to r, within the
// limits of typ.
func (m *Module) createTargets(_ store.Tx, _ *Record, _ Type, in []json.RawMessage) error {
	if len(in) > 0 {
		return &api.FieldError{Field: "targets", Message: "targets are not supported"}
	}
	return nil
}

// viewTargets returns the targets of r as the API shows them.
func (m *Module) viewTargets(store.Tx, Record) ([]TargetView, error) { return []TargetView{}, nil }

// ServerDeleted is the servers.OnDelete function: it removes the targets
// of server serverID.
func (m *Module) ServerDeleted(store.Tx, int64) error { return nil }

// ForServer returns the load balancers with a server target on serverID.
func (m *Module) ForServer(store.Tx, int64) ([]int64, error) { return nil, nil }
