package loadbalancers

import (
	"encoding/json"
	"net/http"
	"net/netip"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/resources/networks"
	"github.com/webcodingcat/hlab/internal/store"
)

// networkRefType is the resource type of a network in actions.
const networkRefType = "network"

// networkRoutes registers attach_to_network, detach_from_network and
// enable/disable_public_interface.
func (m *Module) networkRoutes(r resource.Registrar) {
	r.Handle("POST /load_balancers/{id}/actions/attach_to_network", http.HandlerFunc(m.attachToNetwork))
	r.Handle("POST /load_balancers/{id}/actions/detach_from_network", http.HandlerFunc(m.detachFromNetwork))
	r.Handle("POST /load_balancers/{id}/actions/enable_public_interface", http.HandlerFunc(m.enablePublicInterface))
	r.Handle("POST /load_balancers/{id}/actions/disable_public_interface", http.HandlerFunc(m.disablePublicInterface))
}

// attach puts r into network netID with ip (given, or from ipRange or the
// network's subnets) through the networks module. A load balancer has at
// most one network.
func (m *Module) attach(tx store.Tx, r *Record, netID int64, ip, ipRange string) error {
	if len(r.PrivateNet) > 0 {
		return &api.FieldError{Code: "load_balancer_already_attached", Message: "the load balancer is already attached to a network"}
	}
	if m.d.Networks == nil {
		return &api.FieldError{Field: "network", Message: "networks are not available"}
	}
	loc, _ := m.location(r.Hlab.Location)
	a, vlan, err := m.d.Networks.AttachLoadBalancer(tx, r.Hlab.ProjectID, loc.NetworkZone, netID, ip, ipRange)
	if err != nil {
		return err
	}
	r.PrivateNet = []PrivateNet{{Network: netID, IP: a.String()}}
	r.Hlab.VLAN = vlan
	return nil
}

// attachAtCreate attaches the new load balancer r to network (0: none).
func (m *Module) attachAtCreate(tx store.Tx, r *Record, network int64) error {
	if network == 0 {
		return nil
	}
	return m.attach(tx, r, network, "", "")
}

// attachToNetwork attaches the load balancer to a network.
func (m *Module) attachToNetwork(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Network int64  `json:"network"`
		IP      string `json:"ip"`
		IPRange string `json:"ip_range"`
	}
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.queued(w, req, id, "attach_to_network", true, func(tx store.Tx, r *Record) ([]actions.Ref, error) {
		if err := m.attach(tx, r, body.Network, body.IP, body.IPRange); err != nil {
			return nil, err
		}
		return []actions.Ref{{ID: body.Network, Type: networkRefType}}, nil
	})
}

// detachFromNetwork detaches the load balancer from its network, unless
// targets are reached through private IPs.
func (m *Module) detachFromNetwork(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Network int64 `json:"network"`
	}
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.queued(w, req, id, "detach_from_network", true, func(_ store.Tx, r *Record) ([]actions.Ref, error) {
		if len(r.PrivateNet) == 0 || r.PrivateNet[0].Network != body.Network {
			return nil, &api.FieldError{Code: "load_balancer_not_attached_to_network", Message: "the load balancer is not attached to this network"}
		}
		if hasTargets(r.Targets, true) {
			return nil, &api.FieldError{Field: "network", Message: "targets use private IPs of this network; remove them first"}
		}
		r.PrivateNet = []PrivateNet{}
		r.Hlab.VLAN = 0
		return []actions.Ref{{ID: body.Network, Type: networkRefType}}, nil
	})
}

// enablePublicInterface turns the public interface on.
func (m *Module) enablePublicInterface(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	m.change(w, req, id, "enable_public_interface", func(_ store.Tx, r *Record) ([]actions.Ref, error) {
		r.PublicInterface = true
		return nil, nil
	})
}

// disablePublicInterface turns the public interface off; the load balancer
// needs a network and only targets reached through private IPs.
func (m *Module) disablePublicInterface(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	m.change(w, req, id, "disable_public_interface", func(_ store.Tx, r *Record) ([]actions.Ref, error) {
		if len(r.PrivateNet) == 0 {
			return nil, &api.FieldError{Code: "load_balancer_not_attached_to_network", Message: "the load balancer is not attached to a network"}
		}
		if hasTargets(r.Targets, false) {
			return nil, &api.FieldError{Code: "targets_without_use_private_ip", Message: "targets without use_private_ip need the public interface"}
		}
		r.PublicInterface = false
		return nil, nil
	})
}

// hasTargets reports whether a target of ts has use_private_ip equal to
// private; ip targets never use private IPs. It reads the stored JSON
// form, the field name of Hetzner's target shape.
func hasTargets(ts []Target, private bool) bool {
	for _, t := range ts {
		var v struct {
			UsePrivateIP bool `json:"use_private_ip"`
		}
		b, err := json.Marshal(t)
		if err == nil {
			err = json.Unmarshal(b, &v)
		}
		if err == nil && v.UsePrivateIP == private {
			return true
		}
	}
	return false
}

// InNetwork returns the IDs and IPs of the load balancers of projectID
// attached to network netID.
func (m *Module) InNetwork(tx store.Tx, projectID, netID int64) ([]networks.LBMember, error) {
	recs, err := store.List[Record](tx, Kind, projectID)
	if err != nil {
		return nil, err
	}
	var out []networks.LBMember
	for _, r := range recs {
		for _, p := range r.PrivateNet {
			if a, err := netip.ParseAddr(p.IP); err == nil && p.Network == netID {
				out = append(out, networks.LBMember{ID: r.ID, IP: a})
			}
		}
	}
	return out, nil
}

// NetworkDeleted detaches every load balancer from network netID.
func (m *Module) NetworkDeleted(tx store.Tx, netID int64) error {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if len(r.PrivateNet) == 0 || r.PrivateNet[0].Network != netID {
			continue
		}
		r.PrivateNet = []PrivateNet{}
		r.Hlab.VLAN = 0
		if err := put(tx, r); err != nil {
			return err
		}
	}
	return nil
}
