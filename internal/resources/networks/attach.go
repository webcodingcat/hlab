package networks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// Hetzner's limits per server (RESEARCH R1).
const (
	maxNetworksPerServer = 3
	maxAliasIPs          = 5
)

// serverRefType is the resource type of a server in actions.
const serverRefType = "server"

// attachRequest is the body of attach_to_network.
type attachRequest struct {
	Network  int64    `json:"network"`
	IP       string   `json:"ip"`
	AliasIPs []string `json:"alias_ips"`
	IPRange  string   `json:"ip_range"`
}

// serverRoutes registers the network actions of servers.
func (m *Module) serverRoutes(r resource.Registrar) {
	r.Handle("POST /servers/{id}/actions/attach_to_network", http.HandlerFunc(m.attachToNetwork))
	r.Handle("POST /servers/{id}/actions/detach_from_network", http.HandlerFunc(m.detachFromNetwork))
	r.Handle("POST /servers/{id}/actions/change_alias_ips", http.HandlerFunc(m.changeAliasIPs))
}

// AttachAtCreate implements servers.Networks: it checks and allocates the
// networks ids for a new server in location.
func (m *Module) AttachAtCreate(tx store.Tx, projectID, _ int64, location string, ids []int64) ([]servers.PrivateNet, map[int64]servers.NetInternal, error) {
	var held []servers.PrivateNet
	nets := map[int64]servers.NetInternal{}
	for _, id := range ids {
		pn, ni, err := m.allocate(tx, projectID, location, held, nets, attachRequest{Network: id}, "networks")
		if err != nil {
			return nil, nil, err
		}
		if pn.MACAddress, err = nextMAC(tx); err != nil {
			return nil, nil, err
		}
		held = append(held, pn)
		nets[id] = ni
	}
	return held, nets, nil
}

// allocate checks the attach of a server in location, which holds the
// networks held (with nets), to network req.Network and returns its
// private_net entry without MAC address. field names the network in
// errors.
func (m *Module) allocate(tx store.Tx, projectID int64, location string, held []servers.PrivateNet,
	nets map[int64]servers.NetInternal, req attachRequest, field string) (servers.PrivateNet, servers.NetInternal, error) {
	var none servers.PrivateNet
	n, err := Load(tx, projectID, req.Network)
	if errors.Is(err, store.ErrNotFound) {
		return none, servers.NetInternal{}, invalid(field, "network %d not found", req.Network)
	}
	if err != nil {
		return none, servers.NetInternal{}, err
	}
	if slices.ContainsFunc(held, func(p servers.PrivateNet) bool { return p.Network == n.ID }) {
		return none, servers.NetInternal{}, &api.FieldError{Code: "server_already_attached",
			Message: fmt.Sprintf("the server is already attached to network %d", n.ID)}
	}
	rng, err := netip.ParsePrefix(n.IPRange)
	if err != nil {
		return none, servers.NetInternal{}, err
	}
	for _, p := range held {
		if o, err := netip.ParsePrefix(nets[p.Network].IPRange); err == nil && o.Overlaps(rng) {
			return none, servers.NetInternal{}, &api.FieldError{Code: "networks_overlap",
				Message: fmt.Sprintf("the range %s overlaps the range %s of network %d of the server", rng, o, p.Network)}
		}
	}
	if len(held) >= maxNetworksPerServer {
		return none, servers.NetInternal{}, &api.FieldError{Code: "resource_limit_exceeded",
			Message: fmt.Sprintf("a server can be attached to at most %d networks", maxNetworksPerServer)}
	}

	zone := ""
	if l, ok := m.d.Catalog.Location(location); ok {
		zone = l.NetworkZone
	}
	ip, used, err := m.pickIP(tx, projectID, n, zone, req.IP, req.IPRange)
	if err != nil {
		return none, servers.NetInternal{}, err
	}
	gw := Gateway(rng)
	aliases, err := checkAliases(n.Subnets, gw, used, req.AliasIPs)
	if err != nil {
		return none, servers.NetInternal{}, err
	}
	return servers.PrivateNet{Network: n.ID, IP: ip.String(), AliasIPs: aliases},
		servers.NetInternal{VLAN: n.Hlab.VLAN, IPRange: rng.String()}, nil
}

// nextMAC is a new private adapter MAC.
func nextMAC(tx store.Tx) (string, error) {
	mac, err := servers.NextPrivateMAC(tx)
	if err != nil {
		return "", err
	}
	return mac.String(), nil
}

// pickIP returns the IP for a new member of network n in network zone
// zone: ip if given (free and in a subnet of the zone, of ipRange if
// given), else the next free IP of ipRange or of the first subnet of the
// zone that has one. It also returns the IPs used in n, ip included.
func (m *Module) pickIP(tx store.Tx, projectID int64, n Record, zone, ip, ipRange string) (netip.Addr, map[netip.Addr]bool, error) {
	var none netip.Addr
	rng, err := netip.ParsePrefix(n.IPRange)
	if err != nil {
		return none, nil, err
	}
	var cands []netip.Prefix
	for _, s := range n.Subnets {
		p, err := netip.ParsePrefix(s.IPRange)
		if err != nil || s.NetworkZone != zone {
			continue
		}
		if ipRange == "" || p.String() == ipRange {
			cands = append(cands, p)
		}
	}
	if ipRange != "" && !slices.ContainsFunc(n.Subnets, func(s Subnet) bool { return s.IPRange == ipRange }) {
		return none, nil, invalid("ip_range", "the network has no subnet %s", ipRange)
	}
	if len(cands) == 0 {
		return none, nil, &api.FieldError{Code: "no_subnet_available",
			Message: fmt.Sprintf("network %d has no subnet in network zone %s", n.ID, zone)}
	}

	used, err := m.usedIPs(tx, projectID, n.ID, 0)
	if err != nil {
		return none, nil, err
	}
	gw := Gateway(rng)
	var a netip.Addr
	if ip != "" {
		if a, err = netip.ParseAddr(ip); err != nil || !a.Is4() {
			return none, nil, invalid("ip", "%q is no IPv4 address", ip)
		}
		if !slices.ContainsFunc(cands, func(p netip.Prefix) bool { return p.Contains(a) }) {
			return none, nil, invalid("ip", "%s is not inside a subnet of network zone %s", a, zone)
		}
		if used[a] || !isHost(n.Subnets, gw, a) {
			return none, nil, &api.FieldError{Code: "ip_not_available",
				Message: fmt.Sprintf("the IP %s is not available", a)}
		}
	} else {
		found := false
		for _, p := range cands {
			if a, found = NextIP(p, gw, used); found {
				break
			}
		}
		if !found {
			return none, nil, &api.FieldError{Code: "no_subnet_available",
				Message: fmt.Sprintf("no free IP left in the subnets of network %d in network zone %s", n.ID, zone)}
		}
	}
	used[a] = true
	return a, used, nil
}

// usedIPs are the private and alias IPs the servers of projectID hold in
// network id, and the IPs of its load balancers; the alias IPs of server
// except are left out.
func (m *Module) usedIPs(tx store.Tx, projectID, id, except int64) (map[netip.Addr]bool, error) {
	mbs, err := members(tx, projectID, id)
	if err != nil {
		return nil, err
	}
	used := map[netip.Addr]bool{}
	for _, mb := range mbs {
		for i, a := range mb.IPs {
			if mb.Server == except && i > 0 {
				continue
			}
			used[a] = true
		}
	}
	lbs, err := m.lbMembers(tx, projectID, id)
	if err != nil {
		return nil, err
	}
	for _, lb := range lbs {
		used[lb.IP] = true
	}
	return used, nil
}

// isHost reports whether a is a host address of one of subnets that is
// neither the network gateway gw nor the public gateway.
func isHost(subnets []Subnet, gw, a netip.Addr) bool {
	if a == gw || a == publicGateway {
		return false
	}
	for _, s := range subnets {
		p, err := netip.ParsePrefix(s.IPRange)
		if err != nil || !p.Contains(a) {
			continue
		}
		p = p.Masked()
		last := a.Next()
		return a != p.Addr() && last.IsValid() && p.Contains(last)
	}
	return false
}

// checkAliases validates alias IPs: at most five, each a free host address
// of a subnet of the network, no duplicates. used holds the addresses taken
// so far.
func checkAliases(subnets []Subnet, gw netip.Addr, used map[netip.Addr]bool, ips []string) ([]string, error) {
	if len(ips) > maxAliasIPs {
		return nil, invalid("alias_ips", "a server can have at most %d alias IPs in a network", maxAliasIPs)
	}
	out := []string{}
	seen := map[netip.Addr]bool{}
	for _, s := range ips {
		a, err := netip.ParseAddr(s)
		if err != nil || !a.Is4() {
			return nil, invalid("alias_ips", "%q is no IPv4 address", s)
		}
		if !isHost(subnets, gw, a) {
			return nil, invalid("alias_ips", "%s is no host address of a subnet of the network", a)
		}
		if used[a] || seen[a] {
			return nil, &api.FieldError{Code: "ip_not_available", Message: fmt.Sprintf("the IP %s is not available", a)}
		}
		seen[a] = true
		out = append(out, a.String())
	}
	return out, nil
}

// serverChange runs one network action of server id on network netID. The
// request checks f on the current server record and starts the action
// command, refused while the server has an action and queued behind the
// actions holding the network. The work applies f again to the state the
// earlier actions left, stores it, syncs the server's adapters, seed and
// agent document, and only then lets the API show the new private_net. An
// entry f adds gets its MAC address in the work.
func (m *Module) serverChange(w http.ResponseWriter, req *http.Request, id, netID int64, command string, f func(tx store.Tx, r *servers.Record) error) {
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	stor := api.StoreFrom(req.Context())
	var action *actions.Action
	err := stor.Tx(func(tx store.Tx) error {
		r, err := store.Get[servers.Record](tx, servers.Kind, p.ID, id)
		if err != nil {
			return err
		}
		server := actions.Ref{ID: id, Type: serverRefType}
		if action, err = acts.StartQueued(tx, p.ID, command, []actions.Ref{server, {ID: netID, Type: refType}}, []actions.Ref{server}); err != nil {
			return err
		}
		return f(tx, &r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error {
		err := stor.Tx(func(tx store.Tx) error {
			return servers.Update(tx, id, func(r *servers.Record) error {
				shown := append([]servers.PrivateNet{}, r.PrivateNet...)
				if err := f(tx, r); err != nil {
					return err
				}
				for i := range r.PrivateNet {
					if r.PrivateNet[i].MACAddress != "" {
						continue
					}
					var err error
					if r.PrivateNet[i].MACAddress, err = nextMAC(tx); err != nil {
						return err
					}
				}
				r.Hlab.ShownPrivateNet = &shown
				return nil
			})
		})
		if err != nil {
			return workError(err)
		}
		err = m.d.Servers.SyncAdapters(ctx, stor, id, []int64{netID})
		if serr := stor.Tx(func(tx store.Tx) error {
			return servers.Update(tx, id, func(r *servers.Record) error {
				r.Hlab.ShownPrivateNet = nil
				return nil
			})
		}); serr != nil && err == nil {
			err = serr
		}
		return err
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// workError is err of an action's work as Hetzner's error object: a field
// error keeps its code and message.
func workError(err error) error {
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

// attachToNetwork attaches a server to a network (running or not).
func (m *Module) attachToNetwork(w http.ResponseWriter, req *http.Request) {
	var body attachRequest
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.serverChange(w, req, id, body.Network, "attach_to_network", func(tx store.Tx, r *servers.Record) error {
		pn, ni, err := m.allocate(tx, r.Hlab.ProjectID, r.Hlab.Location, r.PrivateNet, r.Hlab.Networks, body, "network")
		if err != nil {
			return err
		}
		r.PrivateNet = append(r.PrivateNet, pn)
		if r.Hlab.Networks == nil {
			r.Hlab.Networks = map[int64]servers.NetInternal{}
		}
		r.Hlab.Networks[pn.Network] = ni
		return nil
	})
}

// netIndex is the index of network netID in r's private_net; not attached
// is an input error.
func netIndex(r *servers.Record, netID int64) (int, error) {
	i := slices.IndexFunc(r.PrivateNet, func(p servers.PrivateNet) bool { return p.Network == netID })
	if i < 0 {
		return -1, invalid("network", "the server is not attached to network %d", netID)
	}
	return i, nil
}

// detachFromNetwork removes a server from a network; its adapter goes.
func (m *Module) detachFromNetwork(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Network int64 `json:"network"`
	}
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.serverChange(w, req, id, body.Network, "detach_from_network", func(_ store.Tx, r *servers.Record) error {
		i, err := netIndex(r, body.Network)
		if err != nil {
			return err
		}
		detach(r, i)
		return nil
	})
}

// detach removes entry i of r's private_net with its network data.
func detach(r *servers.Record, i int) {
	delete(r.Hlab.Networks, r.PrivateNet[i].Network)
	r.PrivateNet = slices.Delete(r.PrivateNet, i, i+1)
}

// changeAliasIPs replaces the alias IPs of a server in a network.
func (m *Module) changeAliasIPs(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Network  int64    `json:"network"`
		AliasIPs []string `json:"alias_ips"`
	}
	id, ok := decodeAction(w, req, &body)
	if !ok {
		return
	}
	m.serverChange(w, req, id, body.Network, "change_alias_ips", func(tx store.Tx, r *servers.Record) error {
		i, err := netIndex(r, body.Network)
		if err != nil {
			return err
		}
		n, err := Load(tx, r.Hlab.ProjectID, body.Network)
		if err != nil {
			return err
		}
		rng, err := netip.ParsePrefix(n.IPRange)
		if err != nil {
			return err
		}
		used, err := m.usedIPs(tx, r.Hlab.ProjectID, n.ID, r.ID)
		if err != nil {
			return err
		}
		aliases, err := checkAliases(n.Subnets, Gateway(rng), used, body.AliasIPs)
		if err != nil {
			return err
		}
		r.PrivateNet[i].AliasIPs = aliases
		return nil
	})
}

// detachAll removes every server of r's project from network r and
// returns their IDs; the caller syncs their adapters after the commit.
func detachAll(tx store.Tx, r Record) ([]int64, error) {
	mbs, err := members(tx, r.Hlab.ProjectID, r.ID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, mb := range mbs {
		err := servers.Update(tx, mb.Server, func(s *servers.Record) error {
			i, err := netIndex(s, r.ID)
			if err == nil {
				detach(s, i)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		ids = append(ids, mb.Server)
	}
	return ids, nil
}

// syncDetached syncs the adapters of the servers ids after network netID
// was taken from them; failures are logged, the records are stored.
func (m *Module) syncDetached(ctx context.Context, stor *store.Store, netID int64, ids []int64) {
	for _, id := range ids {
		if err := m.d.Servers.SyncAdapters(ctx, stor, id, []int64{netID}); err != nil {
			slog.Warn("sync adapters after a network was deleted", "server", id, "network", netID, "err", err)
		}
	}
}
