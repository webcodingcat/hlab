package firewalls

import (
	"cmp"
	"slices"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/store"
)

// Contribute implements resource.NetContributor. It runs after servers:
// each doc.Servers entry gets the IDs of the firewalls applied to it, label
// selectors resolved, and every firewall that reaches a server is added
// with its rules.
func (m *Module) Contribute(tx store.Tx, doc *agentapi.Config) error {
	fws, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	slices.SortFunc(fws, func(a, b Record) int { return cmp.Compare(a.ID, b.ID) })
	applied := map[int64][]int64{} // server ID -> firewall IDs
	resolved := map[int64]bool{}   // project IDs already resolved
	for _, f := range fws {
		if resolved[f.Hlab.ProjectID] {
			continue
		}
		resolved[f.Hlab.ProjectID] = true
		res, err := resolve(tx, f.Hlab.ProjectID)
		if err != nil {
			return err
		}
		for id, ids := range res.servers {
			applied[id] = ids
		}
	}
	used := map[int64]bool{}
	for i := range doc.Servers {
		doc.Servers[i].FirewallIDs = applied[doc.Servers[i].ID]
		for _, id := range doc.Servers[i].FirewallIDs {
			used[id] = true
		}
	}
	for _, f := range fws {
		if used[f.ID] {
			doc.Firewalls = append(doc.Firewalls, agentapi.Firewall{ID: f.ID, Rules: AgentRules(f.Rules)})
		}
	}
	return nil
}
