package firewalls

import (
	"cmp"
	"slices"

	"github.com/webcodingcat/hlab/internal/labels"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// maxPerServer is the number of firewalls one server can have, counted
// over direct and selector entries together, as at Hetzner.
const maxPerServer = 5

// Applied entry types.
const (
	typeServer   = "server"
	typeSelector = "label_selector"
)

// resolution is what the firewalls of one project reach.
type resolution struct {
	// servers holds per server ID the IDs of the firewalls applied to it,
	// ordered by ID.
	servers map[int64][]int64
	// selected holds per firewall ID and selector the servers the
	// selector reaches, ordered by ID.
	selected map[int64]map[string][]int64
}

// firewallsOf returns the firewall IDs applied to server id.
func (res resolution) firewallsOf(id int64) []int64 { return res.servers[id] }

// reaches reports whether firewall fw is applied to server id.
func (res resolution) reaches(fw, id int64) bool { return slices.Contains(res.servers[id], fw) }

// resolve computes the servers the firewalls of projectID reach: first the
// direct entries, then the selectors over the project's servers that have a
// public IPv4 and are not deleting. A selector match that would give a
// server a sixth firewall is skipped.
func resolve(tx store.Tx, projectID int64) (resolution, error) {
	fws, err := store.List[Record](tx, Kind, projectID)
	if err != nil {
		return resolution{}, err
	}
	srvs, err := store.List[servers.Record](tx, servers.Kind, projectID)
	if err != nil {
		return resolution{}, err
	}
	slices.SortFunc(fws, func(a, b Record) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(srvs, func(a, b servers.Record) int { return cmp.Compare(a.ID, b.ID) })
	res := resolution{servers: map[int64][]int64{}, selected: map[int64]map[string][]int64{}}
	add := func(server, fw int64) {
		if !slices.Contains(res.servers[server], fw) {
			res.servers[server] = append(res.servers[server], fw)
		}
	}
	for _, f := range fws {
		for _, a := range f.AppliedTo {
			if a.Type == typeServer && a.Server != nil {
				add(a.Server.ID, f.ID)
			}
		}
	}
	for _, f := range fws {
		for _, a := range f.AppliedTo {
			if a.Type != typeSelector || a.LabelSelector == nil {
				continue
			}
			sel, err := labels.ParseSelector(a.LabelSelector.Selector)
			if err != nil {
				continue // stored selectors were checked when applied
			}
			matched := []int64{}
			for _, s := range srvs {
				if !s.IPv4().IsValid() || s.Status == servers.StatusDeleting || !sel.Matches(s.Labels) {
					continue
				}
				if !res.reaches(f.ID, s.ID) {
					if len(res.servers[s.ID]) >= maxPerServer {
						continue
					}
					add(s.ID, f.ID)
				}
				matched = append(matched, s.ID)
			}
			if res.selected[f.ID] == nil {
				res.selected[f.ID] = map[string][]int64{}
			}
			res.selected[f.ID][a.LabelSelector.Selector] = matched
		}
	}
	for id := range res.servers {
		slices.Sort(res.servers[id])
	}
	return res, nil
}

// show is the firewall as the API shows it, with applied_to_resources on
// selector entries.
func (res resolution) show(r Record) Firewall {
	f := view(r)
	applied := make([]Applied, 0, len(f.AppliedTo))
	for _, a := range f.AppliedTo {
		if a.Type == typeSelector && a.LabelSelector != nil {
			a.AppliedToResources = []AppliedResource{}
			for _, id := range res.selected[r.ID][a.LabelSelector.Selector] {
				a.AppliedToResources = append(a.AppliedToResources, AppliedResource{Type: typeServer, Server: ServerRef{ID: id}})
			}
		}
		applied = append(applied, a)
	}
	f.AppliedTo = applied
	return f
}

// ForServer implements servers.Firewalls: the firewalls applied to server
// r, directly or by label selector, ordered by ID.
func (m *Module) ForServer(tx store.Tx, r servers.Record) ([]servers.ServerFirewall, error) {
	res, err := resolve(tx, r.Hlab.ProjectID)
	if err != nil {
		return nil, err
	}
	out := []servers.ServerFirewall{}
	for _, id := range res.firewallsOf(r.ID) {
		out = append(out, servers.ServerFirewall{ID: id, Status: "applied"})
	}
	return out, nil
}

// ServerDeleted removes server serverID from every applied_to.
func (m *Module) ServerDeleted(tx store.Tx, serverID int64) error {
	fws, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	for _, f := range fws {
		kept := slices.DeleteFunc(slices.Clone(f.AppliedTo), func(a Applied) bool {
			return a.Type == typeServer && a.Server != nil && a.Server.ID == serverID
		})
		if len(kept) == len(f.AppliedTo) {
			continue
		}
		f.AppliedTo = kept
		if err := put(tx, f); err != nil {
			return err
		}
	}
	return nil
}
