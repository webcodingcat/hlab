package loadbalancers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/ipam"
	"github.com/webcodingcat/hlab/internal/rdns"
	"github.com/webcodingcat/hlab/internal/store"
)

// createRequest is POST /load_balancers.
type createRequest struct {
	Name             string            `json:"name"`
	LoadBalancerType json.RawMessage   `json:"load_balancer_type"`
	Algorithm        *Algorithm        `json:"algorithm"`
	Labels           map[string]string `json:"labels"`
	PublicInterface  *bool             `json:"public_interface"`
	PublicNet        *struct {
		IPv4 *int64 `json:"ipv4"`
		IPv6 *int64 `json:"ipv6"`
	} `json:"public_net"`
	Network     int64             `json:"network"`
	NetworkZone string            `json:"network_zone"`
	Location    string            `json:"location"`
	Services    []json.RawMessage `json:"services"`
	Targets     []json.RawMessage `json:"targets"`
}

// create stores a load balancer and starts its create_load_balancer
// action, which ends after the agent push.
func (m *Module) create(w http.ResponseWriter, req *http.Request) {
	var body createRequest
	if !api.Decode(w, req, &body) {
		return
	}
	if body.Labels == nil {
		body.Labels = map[string]string{}
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var l LoadBalancer
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := m.newRecord(tx, p.ID, body)
		if err != nil {
			return err
		}
		if err := m.attachAtCreate(tx, &r, body.Network); err != nil {
			return err
		}
		typ, _ := m.lbType(r.Hlab.Type)
		if err := m.createServices(tx, &r, typ, body.Services); err != nil {
			return err
		}
		if err := m.createTargets(tx, &r, typ, body.Targets); err != nil {
			return err
		}
		if err := put(tx, r); err != nil {
			return err
		}
		if action, err = acts.Start(tx, p.ID, "create_load_balancer", []actions.Ref{{ID: r.ID, Type: refType}}); err != nil {
			return err
		}
		l, err = m.show(tx, r)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error { return m.d.PushAgent(ctx) })
	api.JSON(w, http.StatusCreated, map[string]any{"load_balancer": l, "action": action})
}

// newRecord checks name, labels, type, location and algorithm of body and
// returns the new record with its public IPs: the primary IPs of
// public_net, and a system IPv4 of the public range when none is given.
func (m *Module) newRecord(tx store.Tx, projectID int64, body createRequest) (Record, error) {
	var r Record
	if err := checkName(tx, projectID, body.Name, 0); err != nil {
		return r, err
	}
	if err := api.CheckLabels(body.Labels); err != nil {
		return r, err
	}
	typ, ok := m.lbType(refString(body.LoadBalancerType))
	if !ok {
		return r, &api.FieldError{Field: "load_balancer_type", Message: "load balancer type " + refString(body.LoadBalancerType) + " not found"}
	}
	loc, err := m.createLocation(body.Location, body.NetworkZone)
	if err != nil {
		return r, err
	}
	algo := algoRoundRobin
	if body.Algorithm != nil {
		if err := checkAlgorithm("algorithm", body.Algorithm.Type); err != nil {
			return r, err
		}
		algo = body.Algorithm.Type
	}
	public := body.PublicInterface == nil || *body.PublicInterface
	var ipv4, ipv6 *int64
	if body.PublicNet != nil {
		ipv4, ipv6 = body.PublicNet.IPv4, body.PublicNet.IPv6
	}
	held := ipv4 != nil || ipv6 != nil
	if held && !public {
		return r, &api.FieldError{Field: "public_net", Message: "primary IPs need the public interface"}
	}
	if held && m.d.PublicIPs == nil {
		return r, &api.FieldError{Field: "public_net", Message: "primary IPs are not available"}
	}

	id, err := store.NextID(tx, Kind)
	if err != nil {
		return r, err
	}
	r = Record{
		ID:              id,
		Name:            body.Name,
		Labels:          body.Labels,
		Created:         time.Now().UTC().Truncate(time.Second),
		Algorithm:       algo,
		PublicInterface: public,
		Services:        []Service{},
		Targets:         []Target{},
		PrivateNet:      []PrivateNet{},
		DNSPtr:          []rdns.Entry{},
	}
	r.Hlab.ProjectID = projectID
	r.Hlab.Location = loc.Name
	r.Hlab.Type = typ.Name

	var a4 netip.Addr
	if held {
		var p6 netip.Prefix
		if a4, p6, err = m.d.PublicIPs.AssignLoadBalancer(tx, projectID, id, loc.Name, ipv4, ipv6); err != nil {
			return r, err
		}
		if ipv4 != nil {
			r.Hlab.IPv4ID = ipv4
		}
		if ipv6 != nil {
			r.Hlab.IPv6ID = ipv6
			r.Hlab.IPv6Net = p6.Masked().String()
		}
	}
	if ipv4 == nil {
		a4, err = ipam.NextPublic(tx, m.d.PublicRange)
		if errors.Is(err, ipam.ErrFull) {
			return r, &api.FieldError{Code: "resource_unavailable", Message: "no free public IPv4 address in the public range"}
		}
		if err != nil {
			return r, err
		}
	}
	r.Hlab.IPv4 = a4.String()
	return r, nil
}

// createLocation is the location of a new load balancer: location, or the
// first location of network_zone in catalog order.
func (m *Module) createLocation(location, zone string) (catalog.Location, error) {
	if location != "" {
		loc, ok := m.location(location)
		if !ok {
			return loc, &api.FieldError{Field: "location", Message: "location " + location + " not found"}
		}
		if zone != "" && loc.NetworkZone != zone {
			return loc, &api.FieldError{Field: "network_zone", Message: "location " + loc.Name + " is not in network zone " + zone}
		}
		return loc, nil
	}
	if zone == "" {
		return catalog.Location{}, &api.FieldError{Field: "location", Message: "give location or network_zone"}
	}
	for _, l := range m.d.Catalog.Locations {
		if l.NetworkZone == zone {
			return l, nil
		}
	}
	return catalog.Location{}, &api.FieldError{Field: "network_zone", Message: "network zone " + zone + " not found"}
}
