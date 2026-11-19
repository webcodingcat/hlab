package networks

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"slices"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// member is a server attached to a network with its addresses there.
type member struct {
	Server int64
	IPs    []netip.Addr // the private IP and the alias IPs
}

// memberDoc reads only the private_net entries of a server record.
type memberDoc struct {
	ID         int64 `json:"id"`
	PrivateNet []struct {
		Network  int64    `json:"network"`
		IP       string   `json:"ip"`
		AliasIPs []string `json:"alias_ips"`
	} `json:"private_net"`
}

// members returns the servers of projectID attached to network id; the
// membership lives on the server records.
func members(tx store.Tx, projectID, id int64) ([]member, error) {
	docs, err := store.List[memberDoc](tx, servers.Kind, projectID)
	if err != nil {
		return nil, err
	}
	var out []member
	for _, d := range docs {
		for _, p := range d.PrivateNet {
			if p.Network != id {
				continue
			}
			mb := member{Server: d.ID}
			for _, s := range append([]string{p.IP}, p.AliasIPs...) {
				if a, err := netip.ParseAddr(s); err == nil {
					mb.IPs = append(mb.IPs, a)
				}
			}
			out = append(out, mb)
		}
	}
	return out, nil
}

// change runs one network action: in a single transaction it loads network
// id, applies f (which validates and changes the record), starts the action
// command and stores the record; the action's work pushes the agent
// document.
func (m *Module) change(w http.ResponseWriter, req *http.Request, id int64, command string, f func(tx store.Tx, r *Record) error) {
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		if err := f(tx, &r); err != nil {
			return err
		}
		if action, err = acts.Start(tx, p.ID, command, []actions.Ref{{ID: id, Type: refType}}); err != nil {
			return err
		}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error { return m.push(ctx) })
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// decodeAction reads the path ID and the body of a network action.
func decodeAction(w http.ResponseWriter, req *http.Request, body any) (int64, bool) {
	id, ok := api.PathID(w, req)
	if !ok {
		return 0, false
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
	m.change(w, req, id, "change_protection", func(_ store.Tx, r *Record) error {
		if body.Delete != nil {
			r.Protection.Delete = *body.Delete
		}
		return nil
	})
}

// addSubnet adds a subnet validated like those of create.
func (m *Module) addSubnet(w http.ResponseWriter, req *http.Request) {
	var body subnetRequest
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "add_subnet", func(_ store.Tx, r *Record) error {
		rng, err := netip.ParsePrefix(r.IPRange)
		if err != nil {
			return err
		}
		s, err := m.checkSubnet(rng, body, r.Subnets, r.Routes, "")
		if err != nil {
			return err
		}
		r.Subnets = append(r.Subnets, s)
		return nil
	})
}

// deleteSubnet removes a subnet no attached server holds an IP in.
func (m *Module) deleteSubnet(w http.ResponseWriter, req *http.Request) {
	var body struct {
		IPRange string `json:"ip_range"`
	}
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "delete_subnet", func(tx store.Tx, r *Record) error {
		p, err := parsePrefix(body.IPRange, "ip_range")
		if err != nil {
			return err
		}
		i := slices.IndexFunc(r.Subnets, func(s Subnet) bool { return s.IPRange == p.String() })
		if i < 0 {
			return invalid("ip_range", "the network has no subnet %s", p)
		}
		mbs, err := members(tx, r.Hlab.ProjectID, r.ID)
		if err != nil {
			return err
		}
		for _, mb := range mbs {
			if slices.ContainsFunc(mb.IPs, p.Contains) {
				return &api.FieldError{Code: "service_error",
					Message: fmt.Sprintf("server %d holds an IP in subnet %s; detach it first", mb.Server, p)}
			}
		}
		r.Subnets = slices.Delete(r.Subnets, i, i+1)
		return nil
	})
}

// addRoute adds a route validated like those of create.
func (m *Module) addRoute(w http.ResponseWriter, req *http.Request) {
	var body Route
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "add_route", func(_ store.Tx, r *Record) error {
		rng, err := netip.ParsePrefix(r.IPRange)
		if err != nil {
			return err
		}
		rt, err := checkRoute(rng, body, r.Subnets, r.Routes, "")
		if err != nil {
			return err
		}
		r.Routes = append(r.Routes, rt)
		return nil
	})
}

// deleteRoute removes the route with the body's destination and gateway.
func (m *Module) deleteRoute(w http.ResponseWriter, req *http.Request) {
	var body Route
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "delete_route", func(_ store.Tx, r *Record) error {
		d, err := parsePrefix(body.Destination, "destination")
		if err != nil {
			return err
		}
		gw, err := netip.ParseAddr(body.Gateway)
		if err != nil {
			return invalid("gateway", "%q is no IPv4 address", body.Gateway)
		}
		i := slices.Index(r.Routes, Route{Destination: d.String(), Gateway: gw.String()})
		if i < 0 {
			return invalid("destination", "the network has no route to %s via %s", d, gw)
		}
		r.Routes = slices.Delete(r.Routes, i, i+1)
		return nil
	})
}

// changeIPRange grows the network range keeping its start address, while
// no server is attached (C2).
func (m *Module) changeIPRange(w http.ResponseWriter, req *http.Request) {
	var body struct {
		IPRange string `json:"ip_range"`
	}
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.change(w, req, id, "change_ip_range", func(tx store.Tx, r *Record) error {
		next, err := checkRange(body.IPRange, "ip_range")
		if err != nil {
			return err
		}
		cur, err := netip.ParsePrefix(r.IPRange)
		if err != nil {
			return err
		}
		if next.Addr() != cur.Addr() {
			return invalid("ip_range", "the range must keep its start address %s", cur.Addr())
		}
		if next.Bits() > cur.Bits() {
			return invalid("ip_range", "%s is smaller than the current range %s; a range can only grow", next, cur)
		}
		mbs, err := members(tx, r.Hlab.ProjectID, r.ID)
		if err != nil {
			return err
		}
		if len(mbs) > 0 {
			return &api.FieldError{Code: "service_error",
				Message: fmt.Sprintf("the network has attached servers (server %d); detach them first", mbs[0].Server)}
		}
		r.IPRange = next.String()
		return nil
	})
}
