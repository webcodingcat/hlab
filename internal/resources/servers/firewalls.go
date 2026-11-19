package servers

import (
	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/store"
)

// ServerFirewall is an entry of a server's public_net.firewalls.
type ServerFirewall struct {
	ID     int64  `json:"id"`
	Status string `json:"status"` // always "applied"
}

// Firewalls is the firewall hook of the servers module. Without it (nil)
// public_net.firewalls is [] and a create with firewalls is refused.
type Firewalls interface {
	// ForServer returns the firewalls applied to server r, directly or by
	// label selector, ordered by ID.
	ForServer(tx store.Tx, r Record) ([]ServerFirewall, error)
	// AttachAtCreate applies firewalls ids to new server serverID inside
	// the create transaction and returns the started apply_firewall
	// actions for next_actions.
	AttachAtCreate(tx store.Tx, acts *actions.Manager, projectID, serverID int64, hasIPv4 bool, ids []int64) ([]*actions.Action, error)
}

// SetFirewalls sets the firewall hook.
func (m *Module) SetFirewalls(h Firewalls) { m.firewalls = h }

// firewallRef is an entry of the firewalls of a server create.
type firewallRef struct {
	Firewall int64 `json:"firewall"`
}

// firewallsOf returns the public_net.firewalls of server r.
func (m *Module) firewallsOf(tx store.Tx, r Record) ([]ServerFirewall, error) {
	if m.firewalls == nil {
		return []ServerFirewall{}, nil
	}
	fws, err := m.firewalls.ForServer(tx, r)
	if err != nil {
		return nil, err
	}
	if fws == nil {
		fws = []ServerFirewall{}
	}
	return fws, nil
}

// attachFirewalls applies the firewalls of a create request to new server
// r through the hook and returns the started actions.
func (m *Module) attachFirewalls(tx store.Tx, acts *actions.Manager, r Record, refs []firewallRef) ([]*actions.Action, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if m.firewalls == nil {
		return nil, &api.FieldError{Field: "firewalls", Message: "firewalls are not available"}
	}
	ids := make([]int64, len(refs))
	for i, f := range refs {
		ids[i] = f.Firewall
	}
	return m.firewalls.AttachAtCreate(tx, acts, r.Hlab.ProjectID, r.ID, r.IPv4().IsValid(), ids)
}
