package storageboxes

import (
	"context"

	"github.com/webcodingcat/hlab/internal/store"
)

// Recover implements resource.Recoverer: a create_storage_box interrupted
// by a stop has been marked error, so every box still initializing is
// deleted with its subaccounts and its IP released, as after a failed
// create. The storage VM's next document leaves it out.
func (m *Module) Recover(_ context.Context) error {
	if m.d.Store == nil {
		return nil
	}
	return m.d.Store.Tx(func(tx store.Tx) error {
		projects, err := store.ListProjects(tx)
		if err != nil {
			return err
		}
		for _, p := range projects {
			recs, err := store.List[Record](tx, Kind, p.ID)
			if err != nil {
				return err
			}
			for _, r := range recs {
				if r.Status == StatusInitializing {
					if err := removeBox(tx, p.ID, r); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}
