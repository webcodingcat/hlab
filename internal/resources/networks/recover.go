package networks

import (
	"context"

	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// restoreServer puts r's network state back to the one before the network
// action that was running on it.
func restoreServer(r *servers.Record) {
	if b := r.Hlab.NetBefore; b != nil {
		r.PrivateNet, r.Hlab.Networks = b.PrivateNet, b.Networks
		r.Hlab.NetBefore = nil
	}
}

// restoreNetwork puts r's subnets and routes back to those before the
// subnet or route action that was running on it.
func restoreNetwork(r *Record) {
	if s := r.Hlab.Shown; s != nil {
		r.Subnets, r.Routes = s.Subnets, s.Routes
		r.Hlab.Shown = nil
	}
}

// Recover implements resource.Recoverer: a network action interrupted by a
// stop has been marked error, so every server and network it left with a
// saved earlier state gets that state back. The adapters of a VM follow at
// the server's next network action.
func (m *Module) Recover(_ context.Context) error {
	return m.d.Store.Tx(func(tx store.Tx) error {
		recs, err := store.ListAll[servers.Record](tx, servers.Kind)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if r.Hlab.NetBefore == nil {
				continue
			}
			if err := servers.Update(tx, r.ID, func(r *servers.Record) error {
				restoreServer(r)
				return nil
			}); err != nil {
				return err
			}
		}
		nets, err := store.ListAll[Record](tx, Kind)
		if err != nil {
			return err
		}
		for _, n := range nets {
			if n.Hlab.Shown == nil {
				continue
			}
			restoreNetwork(&n)
			if err := put(tx, n); err != nil {
				return err
			}
		}
		return nil
	})
}
