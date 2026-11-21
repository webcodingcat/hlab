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
	"github.com/webcodingcat/hlab/internal/rdns"
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
	m.assignment(w, req, id, "assign_floating_ip", func(tx store.Tx, r Record) (*int64, error) {
		srv, err := loadServer(tx, r.Hlab.ProjectID, body.Server)
		if err != nil {
			return nil, err
		}
		if err := m.checkAssign(r, srv); err != nil {
			return nil, err
		}
		return &srv.ID, nil
	})
}

// unassign takes the floating IP from its server; an unassigned floating
// IP stays as it is.
func (m *Module) unassign(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	m.assignment(w, req, id, "unassign_floating_ip", func(store.Tx, Record) (*int64, error) { return nil, nil })
}

// assignment runs an assign or unassign of floating IP id; target checks
// the action against floating IP r and returns the server r ends on (nil:
// none). The request checks target on the committed state and starts the
// action on the floating IP, refused while it has a running action, and
// on its current and its target server, queued behind their actions. The
// work waits its turn, checks target again, pushes the new assignment to
// the agent as pending and, at success, assigns the floating IP as it is
// then, so GET shows the change together with the action's success and a
// failed or interrupted action changes nothing.
func (m *Module) assignment(w http.ResponseWriter, req *http.Request, id int64, command string, target func(tx store.Tx, r Record) (*int64, error)) {
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	st := api.StoreFrom(req.Context())
	own := actions.Ref{ID: id, Type: refType}
	var action *actions.Action
	err := st.Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		to, err := target(tx, r)
		if err != nil {
			return err
		}
		refs := []actions.Ref{own}
		for _, sid := range []*int64{r.Server, to} {
			if sid != nil {
				refs = append(refs, actions.Ref{ID: *sid, Type: "server"})
			}
		}
		action, err = acts.StartQueued(tx, p.ID, command, refs, []actions.Ref{own})
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	actionID := action.ID
	acts.Run(actionID, func(ctx context.Context, _ func(int)) error {
		err := st.Tx(func(tx store.Tx) error {
			r, err := Load(tx, p.ID, id)
			if err != nil {
				return err
			}
			to, err := target(tx, r)
			if err != nil {
				return err
			}
			r.Hlab.Pending = &Pending{Action: actionID, Server: to}
			return put(tx, r)
		})
		if err != nil {
			return actionError(err)
		}
		pushErr := m.d.PushAgent(ctx)
		var changeErr error
		err = st.Tx(func(tx store.Tx) error {
			r, err := Load(tx, p.ID, id)
			if errors.Is(err, store.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if pd := r.Hlab.Pending; pd != nil && pd.Action == actionID {
				r.Hlab.Pending = nil
			}
			if pushErr == nil {
				// The check again, on the record as it is now.
				var to *int64
				if to, changeErr = target(tx, r); changeErr == nil {
					if err := m.move(tx, &r, to); err != nil {
						return err
					}
				}
			}
			return put(tx, r)
		})
		return errors.Join(pushErr, actionError(changeErr), err)
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// move assigns r to server to (nil: none), updating the servers'
// public_net.floating_ips.
func (m *Module) move(tx store.Tx, r *Record, to *int64) error {
	if to == nil {
		return detach(tx, r)
	}
	srv, err := loadServer(tx, r.Hlab.ProjectID, *to)
	if err != nil {
		return err
	}
	return m.assignTo(tx, r, srv)
}

// actionError turns an error of a check inside an action's work into the
// action's error object with the code the request would have answered.
func actionError(err error) error {
	var fe *api.FieldError
	if !errors.As(err, &fe) {
		return err
	}
	code := fe.Code
	if code == "" {
		code = "invalid_input"
	}
	return &actions.Error{Code: code, Message: fe.Message}
}

// Recover implements resource.Recoverer: an assign or unassign interrupted
// by a stop has been marked error, so its pending entry goes and the
// floating IP keeps its committed server.
func (m *Module) Recover(_ context.Context) error {
	return m.d.Store.Tx(func(tx store.Tx) error {
		recs, err := store.ListAll[Record](tx, Kind)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if r.Hlab.Pending == nil {
				continue
			}
			r.Hlab.Pending = nil
			if err := put(tx, r); err != nil {
				return err
			}
		}
		return nil
	})
}

// changeDNSPtr sets or resets a reverse DNS entry of the floating IP; the
// action pushes the agent document.
func (m *Module) changeDNSPtr(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	ip, ptr, ok := servers.DecodeDNSPtr(w, req)
	if !ok {
		return
	}
	m.change(w, req, id, "change_dns_ptr", func(_ store.Tx, r *Record) ([]actions.Ref, error) {
		entries, err := rdns.Change(r.DNSPtr, owner(*r), ip, ptr)
		if err != nil {
			return nil, err
		}
		r.DNSPtr = entries
		return nil, nil
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
	if err := m.checkAssign(*r, srv); err != nil {
		return err
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

// checkAssign checks the assign rules of r to srv: the server must be in
// the network zone of r's home location and not already hold r.
func (m *Module) checkAssign(r Record, srv servers.Record) error {
	if r.Server != nil && *r.Server == srv.ID {
		return &api.FieldError{Code: "floating_ip_assigned", Message: "the floating IP is already assigned to the server"}
	}
	home, _ := m.location(r.Hlab.HomeLocation)
	at, _ := m.location(srv.Hlab.Location)
	if home.NetworkZone != at.NetworkZone {
		return &api.FieldError{Field: "server", Message: "the server is outside the network zone of the floating IP's home location"}
	}
	return nil
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
// floating IP, targeting the public IPv4 of its server (the pending one
// while an assign or unassign runs), and its effective
// PTR name; the target is invalid when the floating IP is unassigned or its
// server has no public IPv4 or is being deleted. IPv6 floating IPs carry no
// traffic.
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
		srv := r.Server
		if p := r.Hlab.Pending; p != nil {
			srv = p.Server
		}
		if srv != nil {
			target = targets[*srv]
		}
		doc.FloatingIPs = append(doc.FloatingIPs, agentapi.FloatingIP{IP: ip, Target: target})
		if doc.PTR == nil {
			doc.PTR = map[netip.Addr]string{}
		}
		doc.PTR[ip] = rdns.PTR(r.DNSPtr, ip)
	}
	return nil
}
