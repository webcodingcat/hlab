package storageboxes

import (
	"context"

	"github.com/webcodingcat/hlab/internal/store"
)

// Recover implements resource.Recoverer: a create_storage_box or
// delete_storage_box interrupted by a stop has been marked error. Every box
// still initializing is deleted with its subaccounts and its IP released,
// as after a failed create. Every box still marked deleting is deleted the
// same way, as its delete would have done: the storage VM's document has
// left it out since the delete began, so its data are gone or go at the
// next apply. Every snapshot still marked creating is deleted, as after a
// failed create_snapshot; the storage VM removes its subvolume at the next
// apply.
func (m *Module) Recover(_ context.Context) error {
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
				if r.Status == StatusInitializing || r.HLab.Deleting {
					if err := removeBox(tx, p.ID, r); err != nil {
						return err
					}
				}
			}
			snaps, err := store.List[SnapshotRecord](tx, SnapshotKind, p.ID)
			if err != nil {
				return err
			}
			for _, s := range snaps {
				if s.HLab.Creating {
					if err := store.Delete(tx, SnapshotKind, p.ID, s.ID); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}
