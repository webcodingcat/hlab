package loadbalancers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/labels"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// Target types.
const (
	targetServer   = "server"
	targetSelector = "label_selector"
	targetIP       = "ip"
)

// Target is a stored target of a load balancer, in the shape of Hetzner's
// add_target request (use_private_ip is read by network.go from the stored
// JSON). It is also the body of add_target and remove_target.
type Target struct {
	Type          string          `json:"type"`
	Server        *TargetServer   `json:"server,omitempty"`
	LabelSelector *TargetSelector `json:"label_selector,omitempty"`
	IP            *TargetIP       `json:"ip,omitempty"`
	UsePrivateIP  bool            `json:"use_private_ip"`
}

// TargetServer names the server of a server target.
type TargetServer struct {
	ID int64 `json:"id"`
}

// TargetSelector is the label selector of a label_selector target.
type TargetSelector struct {
	Selector string `json:"selector"`
}

// TargetIP is the address of an ip target.
type TargetIP struct {
	IP string `json:"ip"`
}

// TargetView is a target as the API shows it.
type TargetView struct {
	Type          string            `json:"type"`
	Server        *TargetServerView `json:"server,omitempty"`
	LabelSelector *TargetSelector   `json:"label_selector,omitempty"`
	IP            *TargetIP         `json:"ip,omitempty"`
	UsePrivateIP  *bool             `json:"use_private_ip,omitempty"`
	HealthStatus  *[]HealthStatus   `json:"health_status,omitempty"`
	Targets       *[]TargetView     `json:"targets,omitempty"`
}

// TargetServerView is the server of a shown server target with the IP the
// load balancer reaches it on ("" when it has none).
type TargetServerView struct {
	ID int64  `json:"id"`
	IP string `json:"ip"`
}

// HealthStatus is the health of a target on one listen port.
type HealthStatus struct {
	ListenPort int    `json:"listen_port"`
	Status     string `json:"status"`
}

// resolved is a target as the agent reaches it: Key is "server:<id>" or
// "ip:<addr>", ViaNetworkID 0 is the public path.
type resolved struct { //nolint:unused // used by Contribute (plan 08-19)
	Key          string
	IP           netip.Addr
	ViaNetworkID int64
}

// targetRoutes registers add_target and remove_target.
func (m *Module) targetRoutes(r resource.Registrar) {
	r.Handle("POST /load_balancers/{id}/actions/add_target", http.HandlerFunc(m.addTarget))
	r.Handle("POST /load_balancers/{id}/actions/remove_target", http.HandlerFunc(m.removeTarget))
}

// addTarget adds one target.
func (m *Module) addTarget(w http.ResponseWriter, req *http.Request) {
	var body Target
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "add_target", func(tx store.Tx, r *Record) ([]actions.Ref, error) {
		typ, _ := m.lbType(r.Hlab.Type)
		return nil, m.addTo(tx, r, typ, body)
	})
}

// removeTarget removes the target the body names.
func (m *Module) removeTarget(w http.ResponseWriter, req *http.Request) {
	var body Target
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "remove_target", func(_ store.Tx, r *Record) ([]actions.Ref, error) {
		for i, t := range r.Targets {
			if sameTarget(t, body) {
				r.Targets = append(r.Targets[:i:i], r.Targets[i+1:]...)
				return nil, nil
			}
		}
		return nil, &api.FieldError{Code: "not_found", Message: "the load balancer has no such target"}
	})
}

// createTargets adds the targets of a create request to r, within the
// limits of typ.
func (m *Module) createTargets(tx store.Tx, r *Record, typ Type, in []json.RawMessage) error {
	for _, raw := range in {
		var t Target
		if err := json.Unmarshal(raw, &t); err != nil {
			return &api.FieldError{Field: "targets", Message: "invalid target: " + err.Error()}
		}
		if err := m.addTo(tx, r, typ, t); err != nil {
			return err
		}
	}
	return nil
}

// addTo checks target t against r and typ and appends it to r's targets.
func (m *Module) addTo(tx store.Tx, r *Record, typ Type, t Target) error {
	if len(r.Targets) >= typ.MaxTargets {
		return &api.FieldError{Code: "resource_limit_exceeded", Message: fmt.Sprintf("the load balancer type allows %d targets", typ.MaxTargets)}
	}
	var n Target
	var err error
	switch t.Type {
	case targetServer:
		n, err = m.checkServerTarget(tx, *r, t)
	case targetSelector:
		n, err = checkSelectorTarget(t)
	case targetIP:
		n, err = m.checkIPTarget(*r, t)
	default:
		err = &api.FieldError{Field: "type", Message: "type must be server, label_selector or ip"}
	}
	if err != nil {
		return err
	}
	if !n.UsePrivateIP && !r.PublicInterface {
		return &api.FieldError{Code: "load_balancer_public_interface_disabled", Message: "a target without use_private_ip needs the public interface"}
	}
	for _, o := range r.Targets {
		if sameTarget(o, n) {
			return &api.FieldError{Code: "target_already_defined", Message: "the target is already defined"}
		}
	}
	r.Targets = append(r.Targets, n)
	return nil
}

// checkServerTarget checks a server target: the server belongs to the
// project, lies in the load balancer's network zone and is reachable the
// way use_private_ip asks.
func (m *Module) checkServerTarget(tx store.Tx, r Record, t Target) (Target, error) {
	if t.Server == nil {
		return t, &api.FieldError{Field: "server", Message: "server is required"}
	}
	s, err := servers.Load(tx, t.Server.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && s.Hlab.ProjectID != r.Hlab.ProjectID) {
		return t, &api.FieldError{Field: "server", Message: "server " + strconv.FormatInt(t.Server.ID, 10) + " not found"}
	}
	if err != nil {
		return t, err
	}
	if !m.sameZone(r, s) {
		return t, &api.FieldError{Field: "server", Message: "the server is in another network zone than the load balancer"}
	}
	if _, _, ok := reach(r, s, t.UsePrivateIP); !ok {
		if t.UsePrivateIP {
			return t, &api.FieldError{Code: "resolve_cloud_private_targets_error", Message: "the server is not in the load balancer's network"}
		}
		if r.PublicInterface {
			return t, &api.FieldError{Code: "resolve_cloud_public_targets_error", Message: "the server has no public IPv4"}
		}
	}
	return Target{Type: targetServer, Server: &TargetServer{ID: s.ID}, UsePrivateIP: t.UsePrivateIP}, nil
}

// checkSelectorTarget checks the label selector of a label_selector target.
func checkSelectorTarget(t Target) (Target, error) {
	if t.LabelSelector == nil || t.LabelSelector.Selector == "" {
		return t, &api.FieldError{Field: "label_selector", Message: "label_selector.selector is required"}
	}
	if _, err := labels.ParseSelector(t.LabelSelector.Selector); err != nil {
		return t, &api.FieldError{Field: "label_selector", Message: err.Error()}
	}
	return Target{Type: targetSelector, LabelSelector: &TargetSelector{Selector: t.LabelSelector.Selector}, UsePrivateIP: t.UsePrivateIP}, nil
}

// checkIPTarget checks an ip target: a unicast IPv4 outside the transit
// /30 at the start of the public range and not the load balancer's own
// address.
func (m *Module) checkIPTarget(r Record, t Target) (Target, error) {
	if t.IP == nil {
		return t, &api.FieldError{Field: "ip", Message: "ip is required"}
	}
	a, err := netip.ParseAddr(t.IP.IP)
	if err != nil || !a.Is4() || !a.IsGlobalUnicast() {
		return t, &api.FieldError{Field: "ip", Message: "ip must be a unicast IPv4 address"}
	}
	transit := netip.PrefixFrom(m.d.PublicRange.Masked().Addr(), 30)
	if transit.Contains(a) {
		return t, &api.FieldError{Field: "ip", Message: "ip is in hlab's transit network"}
	}
	if a == r.IPv4() {
		return t, &api.FieldError{Field: "ip", Message: "ip is the load balancer's own address"}
	}
	return Target{Type: targetIP, IP: &TargetIP{IP: a.String()}}, nil
}

// sameTarget reports whether a and b name the same server, selector or IP.
func sameTarget(a, b Target) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case targetServer:
		return a.Server != nil && b.Server != nil && a.Server.ID == b.Server.ID
	case targetSelector:
		return a.LabelSelector != nil && b.LabelSelector != nil && a.LabelSelector.Selector == b.LabelSelector.Selector
	case targetIP:
		if a.IP == nil || b.IP == nil {
			return false
		}
		x, errA := netip.ParseAddr(a.IP.IP)
		y, errB := netip.ParseAddr(b.IP.IP)
		return errA == nil && errB == nil && x == y
	}
	return false
}

// sameZone reports whether server s lies in the network zone of r.
func (m *Module) sameZone(r Record, s servers.Record) bool {
	a, _ := m.location(r.Hlab.Location)
	b, _ := m.location(s.Hlab.Location)
	return a.NetworkZone == b.NetworkZone
}

// reach is the address r reaches server s on: its IP in r's network with
// private, its public IPv4 otherwise; via is the network ID or 0.
func reach(r Record, s servers.Record, private bool) (netip.Addr, int64, bool) {
	if !private {
		a := s.IPv4()
		return a, 0, a.IsValid()
	}
	if len(r.PrivateNet) == 0 {
		return netip.Addr{}, 0, false
	}
	netID := r.PrivateNet[0].Network
	for _, p := range s.PrivateNet {
		if a, err := netip.ParseAddr(p.IP); err == nil && p.Network == netID {
			return a, netID, true
		}
	}
	return netip.Addr{}, 0, false
}

func serverKey(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

func ipKey(a string) string { return "ip:" + a }

// selected returns the servers of r's project in r's network zone that
// selector sel matches.
func (m *Module) selected(tx store.Tx, r Record, sel string) ([]servers.Record, error) {
	s, err := labels.ParseSelector(sel)
	if err != nil {
		return nil, nil // stored selectors were checked when added
	}
	all, err := store.List[servers.Record](tx, servers.Kind, r.Hlab.ProjectID)
	if err != nil {
		return nil, err
	}
	var out []servers.Record
	for _, srv := range all {
		if srv.Status != servers.StatusDeleting && s.Matches(srv.Labels) && m.sameZone(r, srv) {
			out = append(out, srv)
		}
	}
	return out, nil
}

// resolve returns the targets of r as the agent reaches them: a server
// target as its public or private IP, a label selector as the matching
// servers of the project, an ip target as its address. A server listed
// twice keeps its first entry; servers the load balancer cannot reach are
// left out.
func (m *Module) resolve(tx store.Tx, r Record) ([]resolved, error) { //nolint:unused // used by Contribute (plan 08-19)
	var out []resolved
	seen := map[string]bool{}
	add := func(x resolved) {
		if !seen[x.Key] {
			seen[x.Key] = true
			out = append(out, x)
		}
	}
	addServer := func(s servers.Record, private bool) {
		if a, via, ok := reach(r, s, private); ok {
			add(resolved{Key: serverKey(s.ID), IP: a, ViaNetworkID: via})
		}
	}
	for _, t := range r.Targets {
		switch {
		case t.Type == targetServer && t.Server != nil:
			s, err := servers.Load(tx, t.Server.ID)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			addServer(s, t.UsePrivateIP)
		case t.Type == targetSelector && t.LabelSelector != nil:
			ss, err := m.selected(tx, r, t.LabelSelector.Selector)
			if err != nil {
				return nil, err
			}
			for _, s := range ss {
				addServer(s, t.UsePrivateIP)
			}
		case t.Type == targetIP && t.IP != nil:
			if a, err := netip.ParseAddr(t.IP.IP); err == nil {
				add(resolved{Key: ipKey(a.String()), IP: a})
			}
		}
	}
	return out, nil
}

// healthOf is the health of target key on every service of r.
func (m *Module) healthOf(r Record, key string) *[]HealthStatus {
	hs := make([]HealthStatus, 0, len(r.Services))
	for _, s := range r.Services {
		hs = append(hs, HealthStatus{ListenPort: s.ListenPort, Status: m.health(r.ID, key, s.ListenPort)})
	}
	return &hs
}

// serverView is server s as a target of r.
func (m *Module) serverView(r Record, s servers.Record, private bool) TargetView {
	ip := ""
	if a, _, ok := reach(r, s, private); ok {
		ip = a.String()
	}
	return TargetView{
		Type:         targetServer,
		Server:       &TargetServerView{ID: s.ID, IP: ip},
		UsePrivateIP: &private,
		HealthStatus: m.healthOf(r, serverKey(s.ID)),
	}
}

// viewTargets returns the targets of r as the API shows them.
func (m *Module) viewTargets(tx store.Tx, r Record) ([]TargetView, error) {
	out := make([]TargetView, 0, len(r.Targets))
	for _, t := range r.Targets {
		switch {
		case t.Type == targetServer && t.Server != nil:
			s, err := servers.Load(tx, t.Server.ID)
			if errors.Is(err, store.ErrNotFound) {
				s = servers.Record{}
				s.ID = t.Server.ID
			} else if err != nil {
				return nil, err
			}
			out = append(out, m.serverView(r, s, t.UsePrivateIP))
		case t.Type == targetSelector && t.LabelSelector != nil:
			ss, err := m.selected(tx, r, t.LabelSelector.Selector)
			if err != nil {
				return nil, err
			}
			nested := make([]TargetView, 0, len(ss))
			for _, s := range ss {
				nested = append(nested, m.serverView(r, s, t.UsePrivateIP))
			}
			private := t.UsePrivateIP
			out = append(out, TargetView{Type: targetSelector, LabelSelector: t.LabelSelector, UsePrivateIP: &private, Targets: &nested})
		case t.Type == targetIP && t.IP != nil:
			out = append(out, TargetView{Type: targetIP, IP: t.IP, HealthStatus: m.healthOf(r, ipKey(t.IP.IP))})
		}
	}
	return out, nil
}

// ServerDeleted is the servers.OnDelete function: it removes the targets
// of server serverID.
func (m *Module) ServerDeleted(tx store.Tx, serverID int64) error {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	for _, r := range recs {
		kept := make([]Target, 0, len(r.Targets))
		for _, t := range r.Targets {
			if t.Type != targetServer || t.Server == nil || t.Server.ID != serverID {
				kept = append(kept, t)
			}
		}
		if len(kept) == len(r.Targets) {
			continue
		}
		r.Targets = kept
		if err := put(tx, r); err != nil {
			return err
		}
	}
	return nil
}

// ForServer returns the load balancers with a server target on serverID.
func (m *Module) ForServer(tx store.Tx, serverID int64) ([]int64, error) {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, r := range recs {
		for _, t := range r.Targets {
			if t.Type == targetServer && t.Server != nil && t.Server.ID == serverID {
				out = append(out, r.ID)
				break
			}
		}
	}
	return out, nil
}
