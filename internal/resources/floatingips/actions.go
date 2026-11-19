package floatingips

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"slices"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// change runs one floating IP action: in a single transaction it loads
// floating IP id, applies f (which validates and changes the record and
// returns further action resources), starts the action command and stores
// the record; the action's work pushes the agent document.
func (m *Module) change(w http.ResponseWriter, req *http.Request, id int64, command string, f func(tx store.Tx, r *Record) ([]actions.Ref, error)) {
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		more, err := f(tx, &r)
		if err != nil {
			return err
		}
		refs := append([]actions.Ref{{ID: id, Type: refType}}, more...)
		if action, err = acts.Start(tx, p.ID, command, refs); err != nil {
			return err
		}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error { return m.d.PushAgent(ctx) })
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// decodeAction reads the path ID and the body of a floating IP action; an
// empty body is no error.
func decodeAction(w http.ResponseWriter, req *http.Request, body any) (int64, bool) {
	id, ok := api.PathID(w, req)
	if !ok {
		return 0, false
	}
	if req.ContentLength == 0 {
		return id, true
	}
	return id, api.Decode(w, req, body)
}

// changeProtection sets the delete protection present in the body.
func (m *Module) changeProtection(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Delete *bool `json:"delete"`
	}
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "change_protection", func(_ store.Tx, r *Record) ([]actions.Ref, error) {
		if body.Delete != nil {
			r.Protection.Delete = *body.Delete
		}
		return nil, nil
	})
}

// assign assigns the floating IP to a server in the network zone of its
// home location; one assigned elsewhere moves.
func (m *Module) assign(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Server int64 `json:"server"`
	}
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "assign_floating_ip", func(tx store.Tx, r *Record) ([]actions.Ref, error) {
		srv, err := loadServer(tx, r.Hlab.ProjectID, body.Server)
		if err != nil {
			return nil, err
		}
		if err := m.assignTo(tx, r, srv); err != nil {
			return nil, err
		}
		return []actions.Ref{{ID: srv.ID, Type: "server"}}, nil
	})
}

// unassign takes the floating IP from its server; an unassigned floating
// IP stays as it is.
func (m *Module) unassign(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	m.change(w, req, id, "unassign_floating_ip", func(tx store.Tx, r *Record) ([]actions.Ref, error) {
		if r.Server == nil {
			return nil, nil
		}
		sid := *r.Server
		if err := detach(tx, r); err != nil {
			return nil, err
		}
		return []actions.Ref{{ID: sid, Type: "server"}}, nil
	})
}

// loadServer returns server id of projectID; a missing one is an input
// error of server.
func loadServer(tx store.Tx, projectID, id int64) (servers.Record, error) {
	s, err := store.Get[servers.Record](tx, servers.Kind, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		return s, &api.FieldError{Field: "server", Message: "server not found"}
	}
	return s, err
}

// assignTo checks the assign rules and assigns r to srv: the server must
// be in the network zone of r's home location and not already hold r. An
// r assigned to another server is taken from it first.
func (m *Module) assignTo(tx store.Tx, r *Record, srv servers.Record) error {
	if r.Server != nil && *r.Server == srv.ID {
		return &api.FieldError{Code: "floating_ip_assigned", Message: "the floating IP is already assigned to the server"}
	}
	home, _ := m.location(r.Hlab.HomeLocation)
	at, _ := m.location(srv.Hlab.Location)
	if home.NetworkZone != at.NetworkZone {
		return &api.FieldError{Field: "server", Message: "the server is outside the network zone of the floating IP's home location"}
	}
	if err := detach(tx, r); err != nil {
		return err
	}
	sid := srv.ID
	r.Server = &sid
	return servers.Update(tx, sid, func(s *servers.Record) error {
		if !slices.Contains(s.PublicNet.FloatingIPs, r.ID) {
			s.PublicNet.FloatingIPs = append(s.PublicNet.FloatingIPs, r.ID)
		}
		return nil
	})
}

// detach takes r from its server's public_net.floating_ips and clears its
// server; a server gone meanwhile is no error.
func detach(tx store.Tx, r *Record) error {
	if r.Server == nil {
		return nil
	}
	sid := *r.Server
	r.Server = nil
	err := servers.Update(tx, sid, func(s *servers.Record) error {
		kept := make([]int64, 0, len(s.PublicNet.FloatingIPs))
		for _, id := range s.PublicNet.FloatingIPs {
			if id != r.ID {
				kept = append(kept, id)
			}
		}
		s.PublicNet.FloatingIPs = kept
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

// ServerDeleted is the servers.OnDelete function: the floating IPs of
// server serverID become unassigned.
func (m *Module) ServerDeleted(tx store.Tx, serverID int64) error {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.Server == nil || *r.Server != serverID {
			continue
		}
		r.Server = nil
		if err := put(tx, r); err != nil {
			return err
		}
	}
	return nil
}

// Contribute implements resource.NetContributor: one entry per IPv4
// floating IP, targeting the public IPv4 of its server; the target is
// invalid when the floating IP is unassigned or its server has no public
// IPv4 or is being deleted. IPv6 floating IPs carry no traffic.
func (m *Module) Contribute(tx store.Tx, doc *agentapi.Config) error {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	srvs, err := store.ListAll[servers.Record](tx, servers.Kind)
	if err != nil {
		return err
	}
	targets := make(map[int64]netip.Addr, len(srvs))
	for _, s := range srvs {
		if s.Status != servers.StatusDeleting {
			targets[s.ID] = s.IPv4()
		}
	}
	for _, r := range recs {
		if r.Type != typeIPv4 {
			continue
		}
		ip, err := netip.ParseAddr(r.IP)
		if err != nil {
			continue
		}
		var target netip.Addr
		if r.Server != nil {
			target = targets[*r.Server]
		}
		doc.FloatingIPs = append(doc.FloatingIPs, agentapi.FloatingIP{IP: ip, Target: target})
	}
	return nil
}
