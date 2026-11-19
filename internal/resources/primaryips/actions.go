package primaryips

import (
	"context"
	"errors"
	"net/http"
	"net/netip"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/rdns"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// change runs one primary IP action: in a single transaction it loads
// primary IP id, applies f (which validates and changes the record and
// returns further action resources), starts the action command and stores
// the record; the action's work applies the change to the server among
// those resources (applyTo).
func (m *Module) change(w http.ResponseWriter, req *http.Request, id int64, command string, f func(tx store.Tx, r *Record) ([]actions.Ref, error)) {
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var action *actions.Action
	var refs []actions.Ref
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		more, err := f(tx, &r)
		if err != nil {
			return err
		}
		refs = append([]actions.Ref{{ID: id, Type: refType}}, more...)
		if action, err = acts.Start(tx, p.ID, command, refs); err != nil {
			return err
		}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	var serverID int64
	for _, ref := range refs {
		if ref.Type == "server" {
			serverID = ref.ID
		}
	}
	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error { return m.applyTo(ctx, serverID) })
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// applyTo brings an addressing change to server serverID: its seed is
// rebuilt and the agent document pushed; a server gone meanwhile needs
// nothing. Without a server (0) only the agent document is pushed.
func (m *Module) applyTo(ctx context.Context, serverID int64) error {
	if serverID == 0 {
		return m.d.PushAgent(ctx)
	}
	err := m.d.Servers.SyncAdapters(ctx, m.d.Store, serverID, nil)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

// decodeAction reads the path ID and the body of a primary IP action; an
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

// changeProtection sets the delete protection present in the body; a
// primary IP with auto_delete cannot be protected.
func (m *Module) changeProtection(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Delete *bool `json:"delete"`
	}
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "change_protection", func(_ store.Tx, r *Record) ([]actions.Ref, error) {
		if body.Delete == nil {
			return nil, nil
		}
		if *body.Delete && r.AutoDelete {
			return nil, &api.FieldError{Field: "delete", Message: "a primary IP with auto_delete cannot be delete protected"}
		}
		r.Protection.Delete = *body.Delete
		return nil, nil
	})
}

// assign assigns the primary IP to a stopped server of its location.
func (m *Module) assign(w http.ResponseWriter, req *http.Request) {
	var body struct {
		AssigneeID int64 `json:"assignee_id"`
	}
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "assign_primary_ip", func(tx store.Tx, r *Record) ([]actions.Ref, error) {
		srv, err := loadServer(tx, r.Hlab.ProjectID, body.AssigneeID)
		if err != nil {
			return nil, err
		}
		if err := assignTo(tx, r, srv); err != nil {
			return nil, err
		}
		return []actions.Ref{{ID: srv.ID, Type: "server"}}, nil
	})
}

// unassign takes the primary IP from its stopped server; an unassigned
// primary IP stays as it is.
func (m *Module) unassign(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	m.change(w, req, id, "unassign_primary_ip", func(tx store.Tx, r *Record) ([]actions.Ref, error) {
		if r.AssigneeID == nil {
			return nil, nil
		}
		if heldByLB(*r) {
			return nil, &api.FieldError{Message: "a primary IP held by a load balancer is released by deleting the load balancer"}
		}
		sid := *r.AssigneeID
		err := servers.Update(tx, sid, func(s *servers.Record) error {
			if s.Status != servers.StatusOff {
				return errNotStopped
			}
			if r.Type == typeIPv4 && s.PublicNet.IPv4 != nil && s.PublicNet.IPv4.ID == r.ID {
				s.PublicNet.IPv4 = nil
			}
			if r.Type == typeIPv6 && s.PublicNet.IPv6 != nil && s.PublicNet.IPv6.ID == r.ID {
				s.PublicNet.IPv6 = nil
			}
			return nil
		})
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		r.AssigneeID = nil
		return []actions.Ref{{ID: sid, Type: "server"}}, nil
	})
}

var errNotStopped = &api.FieldError{Code: "server_not_stopped", Message: "the server must be powered off"}

// loadServer returns server id of projectID; a missing one is an input
// error of assignee_id.
func loadServer(tx store.Tx, projectID, id int64) (servers.Record, error) {
	s, err := store.Get[servers.Record](tx, servers.Kind, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		return s, &api.FieldError{Field: "assignee_id", Message: "server not found"}
	}
	return s, err
}

// assignTo checks Hetzner's assign rules and assigns r to srv, setting the
// server's public_net entry of r's family.
func assignTo(tx store.Tx, r *Record, srv servers.Record) error {
	switch {
	case r.AssigneeID != nil:
		return &api.FieldError{Code: "primary_ip_already_assigned", Message: "the primary IP is already assigned"}
	case srv.Status != servers.StatusOff:
		return errNotStopped
	case srv.Hlab.Location != r.Hlab.Location:
		return &api.FieldError{Code: "primary_ip_datacenter_mismatch", Message: "the primary IP and the server are in different locations"}
	case r.Type == typeIPv4 && srv.PublicNet.IPv4 != nil:
		return &api.FieldError{Code: "server_has_ipv4", Message: "the server already has a public IPv4"}
	case r.Type == typeIPv6 && srv.PublicNet.IPv6 != nil:
		return &api.FieldError{Code: "server_has_ipv6", Message: "the server already has a public IPv6"}
	}
	sid := srv.ID
	r.AssigneeID = &sid
	return servers.Update(tx, sid, func(s *servers.Record) error {
		setPublic(&s.PublicNet, *r)
		return nil
	})
}

// setPublic puts r with its reverse DNS entries into the public_net entry
// of its family; an IPv4 without entry stores "" (shown as the default).
func setPublic(pn *servers.PublicNet, r Record) {
	if r.Type == typeIPv4 {
		ptr := ""
		for _, e := range r.DNSPtr {
			if e.IP == r.IP {
				ptr = e.DNSPtr
			}
		}
		pn.IPv4 = &servers.IPv4{ID: r.ID, IP: r.IP, DNSPtr: ptr}
		return
	}
	pn.IPv6 = &servers.IPv6{ID: r.ID, IP: r.IP, DNSPtr: append([]rdns.Entry{}, r.DNSPtr...)}
}

// changeDNSPtr sets or resets a reverse DNS entry of the primary IP and
// mirrors it into its server's public_net; the action pushes the agent
// document.
func (m *Module) changeDNSPtr(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	ip, ptr, ok := servers.DecodeDNSPtr(w, req)
	if !ok {
		return
	}
	m.change(w, req, id, "change_dns_ptr", func(tx store.Tx, r *Record) ([]actions.Ref, error) {
		if err := applyDNSPtr(r, ip, ptr); err != nil {
			return nil, err
		}
		if r.AssigneeID == nil || heldByLB(*r) {
			return nil, nil
		}
		err := servers.Update(tx, *r.AssigneeID, func(s *servers.Record) error {
			mirror(&s.PublicNet, *r)
			return nil
		})
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	})
}

// applyDNSPtr changes the reverse DNS entries of r through rdns.Change.
func applyDNSPtr(r *Record, ip string, ptr *string) error {
	entries, err := rdns.Change(r.DNSPtr, owner(*r), ip, ptr)
	if err != nil {
		return err
	}
	r.DNSPtr = entries
	return nil
}

// mirror updates the public_net entry that holds r, if any.
func mirror(pn *servers.PublicNet, r Record) {
	if (r.Type == typeIPv4 && pn.IPv4 != nil && pn.IPv4.ID == r.ID) ||
		(r.Type == typeIPv6 && pn.IPv6 != nil && pn.IPv6.ID == r.ID) {
		setPublic(pn, r)
	}
}

// ChangeServer implements servers.DNSPtrs: it applies change_dns_ptr on the
// primary IP of server s that holds ip and mirrors it into s.PublicNet.
func (m *Module) ChangeServer(tx store.Tx, s *servers.Record, ip string, ptr *string) (int64, error) {
	notHeld := &api.FieldError{Field: "ip", Message: "the server does not hold this IP"}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return 0, &api.FieldError{Field: "ip", Message: "invalid IP address"}
	}
	var pipID int64
	switch {
	case addr.Is4() && s.PublicNet.IPv4 != nil && s.PublicNet.IPv4.IP == addr.String():
		pipID = s.PublicNet.IPv4.ID
	case addr.Is6() && s.PublicNet.IPv6 != nil:
		n, _ := netip.ParsePrefix(s.PublicNet.IPv6.IP)
		if !n.Contains(addr) {
			return 0, notHeld
		}
		pipID = s.PublicNet.IPv6.ID
	default:
		return 0, notHeld
	}
	r, err := Load(tx, s.Hlab.ProjectID, pipID)
	if errors.Is(err, store.ErrNotFound) {
		return 0, notHeld
	}
	if err != nil {
		return 0, err
	}
	if err := applyDNSPtr(&r, ip, ptr); err != nil {
		return 0, err
	}
	if err := put(tx, r); err != nil {
		return 0, err
	}
	mirror(&s.PublicNet, r)
	return pipID, nil
}

// Allocate implements servers.PublicIPs: it takes the given primary IPs
// (unassigned, of the family and location) or makes new ones with
// auto_delete, assigned to the new server.
func (m *Module) Allocate(tx store.Tx, projectID, serverID int64, location string, req servers.PublicNetRequest) (servers.PublicNet, error) {
	var pn servers.PublicNet
	loc, ok := m.location(location)
	if !ok {
		return pn, &api.FieldError{Field: "location", Message: "location " + location + " not found"}
	}
	for _, fam := range []struct {
		typ     string
		id      int64
		enabled bool
		field   string
	}{
		{typeIPv4, req.IPv4, req.EnableIPv4, "public_net.ipv4"},
		{typeIPv6, req.IPv6, req.EnableIPv6, "public_net.ipv6"},
	} {
		var r Record
		switch {
		case fam.id != 0:
			var err error
			if r, err = Load(tx, projectID, fam.id); errors.Is(err, store.ErrNotFound) {
				return pn, &api.FieldError{Field: fam.field, Message: "primary IP not found"}
			} else if err != nil {
				return pn, err
			}
			switch {
			case r.Type != fam.typ:
				return pn, &api.FieldError{Code: "primary_ip_version_mismatch", Message: "the primary IP is not of type " + fam.typ}
			case r.AssigneeID != nil:
				return pn, &api.FieldError{Code: "primary_ip_assigned", Message: "the primary IP is already assigned"}
			case r.Hlab.Location != loc.Name:
				return pn, &api.FieldError{Code: "primary_ip_datacenter_mismatch", Message: "the primary IP is in another location"}
			}
		case fam.enabled:
			var err error
			if r, err = m.newRecord(tx, projectID, "", fam.typ, loc, servers.IPv6Net(serverID)); err != nil {
				return pn, err
			}
			r.AutoDelete = true
		default:
			continue
		}
		sid := serverID
		r.AssigneeID = &sid
		if err := put(tx, r); err != nil {
			return pn, err
		}
		setPublic(&pn, r)
	}
	return pn, nil
}

// ServerDeleted is the servers.OnDelete function: the auto_delete primary
// IPs of server serverID go, its other primary IPs become unassigned.
func (m *Module) ServerDeleted(tx store.Tx, serverID int64) error {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if !heldByServer(r, serverID) {
			continue
		}
		if r.AutoDelete {
			if err := remove(tx, r); err != nil {
				return err
			}
			continue
		}
		r.AssigneeID = nil
		if err := put(tx, r); err != nil {
			return err
		}
	}
	return nil
}
