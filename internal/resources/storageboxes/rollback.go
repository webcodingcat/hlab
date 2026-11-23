package storageboxes

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/store"
)

const rollbackCommand = "rollback_snapshot"

// rollbackSnapshot runs rollback_snapshot on the box and the snapshot the
// body names by ID or name: the storage agent's rollback job brings the
// box's data back to that snapshot and removes every snapshot of the box
// created after it, manual and automatic; then their records go. An
// unknown snapshot answers 404 not_found.
func (m *Module) rollbackSnapshot(w http.ResponseWriter, req *http.Request) {
	boxID, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	ref := refString(body.Snapshot)
	if ref == "" {
		api.Fail(w, &api.FieldError{Field: "snapshot", Message: "snapshot is required"})
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	st := api.StoreFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var a *actions.Action
	var box Record
	var target SnapshotRecord
	var newer []SnapshotRecord
	err := st.Tx(func(tx store.Tx) error {
		var err error
		if box, err = Load(tx, p.ID, boxID); err != nil {
			return err
		}
		snaps, err := boxSnaps(tx, p.ID, boxID)
		if err != nil {
			return err
		}
		found := false
		for _, s := range snaps {
			if s.Name == ref || strconv.FormatInt(s.ID, 10) == ref {
				target, found = s, true
				break
			}
		}
		if !found {
			return store.ErrNotFound
		}
		if err := checkActive(box); err != nil {
			return err
		}
		// IDs grow with every create, so a higher ID is a later snapshot,
		// also within the same second.
		newer = newer[:0]
		for _, s := range snaps {
			if s.ID > target.ID {
				newer = append(newer, s)
			}
		}
		a, err = begin(tx, acts, p.ID, boxID, rollbackCommand, snapRef(target.ID))
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(a.ID, func(ctx context.Context, _ func(int)) error {
		names := make([]string, 0, len(newer))
		for _, s := range newer {
			names = append(names, s.Name)
		}
		if _, err := m.d.Job(ctx, agentapi.StorageJob{
			Kind: agentapi.StorageJobRollback, Box: userName(box.HLab.N), Snapshot: target.Name, Newer: names,
		}); err != nil {
			return err
		}
		if err := st.Tx(func(tx store.Tx) error {
			for _, s := range newer {
				if err := store.Delete(tx, SnapshotKind, p.ID, s.ID); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		m.refreshStats(ctx, st, p.ID, boxID)
		return nil
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": a})
}
