package storageboxes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/scheduler"
	"github.com/webcodingcat/hlab/internal/store"
)

// UseActions implements resource.ActionUser: the stats job reads and
// writes the boxes through the manager's store.
func (m *Module) UseActions(a *actions.Manager) { m.acts = a }

// Jobs implements resource.Scheduled: the stats of every active box are
// refreshed every StatsEvery.
func (m *Module) Jobs() []scheduler.Job {
	return []scheduler.Job{{Name: "storage box stats", Every: StatsEvery, Run: m.refreshAll}}
}

// refreshAll refreshes the stats of every active box of every project.
func (m *Module) refreshAll(ctx context.Context) {
	if m.d.Job == nil {
		return
	}
	st := m.acts.Store()
	type active struct{ projectID, id int64 }
	var boxes []active
	err := st.Tx(func(tx store.Tx) error {
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
				if r.Status == StatusActive && !r.HLab.Deleting {
					boxes = append(boxes, active{p.ID, r.ID})
				}
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("storage box stats", "err", err)
		return
	}
	for _, b := range boxes {
		if ctx.Err() != nil {
			return
		}
		m.refreshStats(ctx, st, b.projectID, b.id)
	}
}

// refreshStats sets the stats of active box boxID of projectID from the
// storage agent's usage; a failure is logged.
func (m *Module) refreshStats(ctx context.Context, st *store.Store, projectID, boxID int64) {
	if m.d.Job == nil {
		return
	}
	if err := m.setStats(ctx, st, projectID, boxID); err != nil && ctx.Err() == nil {
		slog.Warn("storage box stats", "box", boxID, "err", err)
	}
}

// setStats does the work of refreshStats; a box that is gone, not active
// or being deleted is skipped.
func (m *Module) setStats(ctx context.Context, st *store.Store, projectID, boxID int64) error {
	var r Record
	err := st.Tx(func(tx store.Tx) error {
		var err error
		r, err = Load(tx, projectID, boxID)
		return err
	})
	if errors.Is(err, store.ErrNotFound) || err == nil && (r.Status != StatusActive || r.HLab.Deleting) {
		return nil
	}
	if err != nil {
		return err
	}
	u, err := m.usage(ctx, r)
	if err != nil {
		return err
	}
	return st.Tx(func(tx store.Tx) error {
		r, err := Load(tx, projectID, boxID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		r.Stats = Stats{Size: u.Data + u.Snapshots, SizeData: u.Data, SizeSnapshots: u.Snapshots}
		return put(tx, projectID, r)
	})
}

// usage asks the storage agent for the usage of box r.
func (m *Module) usage(ctx context.Context, r Record) (agentapi.Usage, error) {
	res, err := m.d.Job(ctx, agentapi.StorageJob{Kind: agentapi.StorageJobUsage, Box: userName(r.HLab.N)})
	return res.Usage, err
}

// checkFree refuses with 412 resource_unavailable when the host drive of
// the data folder has less than FreeSpaceReserve free. Without FreeBytes
// nothing is checked.
func (m *Module) checkFree() error {
	if m.d.FreeBytes == nil {
		return nil
	}
	free, err := m.d.FreeBytes()
	if err != nil {
		return fmt.Errorf("free space of the host drive: %w", err)
	}
	if free < FreeSpaceReserve {
		return &api.FieldError{Code: "resource_unavailable",
			Message: fmt.Sprintf("the host drive has %d bytes free, less than the reserve of %d", free, uint64(FreeSpaceReserve))}
	}
	return nil
}

// checkResize checks a change_type of box r to st: a larger type needs the
// free space reserve, a smaller one must hold the box's data and
// snapshots as the storage agent reports them now.
func (m *Module) checkResize(ctx context.Context, r Record, st catalog.StorageBoxType) error {
	var cur struct {
		Size int64 `json:"size"`
	}
	if err := json.Unmarshal(r.StorageBoxType, &cur); err != nil {
		return fmt.Errorf("storage box type of box %d: %w", r.ID, err)
	}
	switch {
	case st.Size > cur.Size:
		return m.checkFree()
	case st.Size < cur.Size && m.d.Job != nil:
		u, err := m.usage(ctx, r)
		if err != nil {
			return err
		}
		if u.Data+u.Snapshots > st.Size {
			return &api.FieldError{Field: "storage_box_type",
				Message: fmt.Sprintf("the box holds %d bytes of data and snapshots, more than the %d of storage box type %s",
					u.Data+u.Snapshots, st.Size, st.Name)}
		}
	}
	return nil
}
