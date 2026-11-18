package servers

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/scheduler"
	"github.com/webcodingcat/hlab/internal/store"
)

// watchInterval is how often the status watch reads the VMs' states.
const watchInterval = 5 * time.Second

// Jobs implements resource.Scheduled: the status watch.
func (m *Module) Jobs() []scheduler.Job {
	return []scheduler.Job{{Name: "servers status watch", Every: watchInterval, Run: m.watch}}
}

// statusOf is the server status of a VM state; Saved, Paused and Other
// have none.
func statusOf(st platform.VMState) (string, bool) {
	switch st {
	case platform.StateOff:
		return StatusOff, true
	case platform.StateStarting:
		return StatusStarting, true
	case platform.StateRunning:
		return StatusRunning, true
	case platform.StateStopping:
		return StatusStopping, true
	}
	return "", false
}

// setStatus sets the status of server id and marks it changed for the
// status watch, so a state the watch read before does not overwrite it.
func (m *Module) setStatus(stor *store.Store, id int64, status string) error {
	m.touch(id)
	return stor.Tx(func(tx store.Tx) error { return SetStatus(tx, id, status) })
}

// touch counts a status change of server id by an action.
func (m *Module) touch(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.changes == nil {
		m.changes = map[int64]uint64{}
	}
	m.changes[id]++
}

func (m *Module) changeCount(id int64) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.changes[id]
}

// watch gives every server without a running action the status of its
// VM's state. A server shown stopping keeps it while its VM still runs: the
// guest has been asked to shut down and has not finished.
func (m *Module) watch(ctx context.Context) {
	stor := m.d.Store
	var recs []Record
	var locked map[int64]bool
	if err := stor.Tx(func(tx store.Tx) error {
		var err error
		if recs, err = store.ListAll[Record](tx, Kind); err != nil {
			return err
		}
		locked, err = lockedServers(tx)
		return err
	}); err != nil {
		slog.Error("server status watch", "err", err)
		return
	}

	type seen struct {
		status  string
		changes uint64
	}
	states := map[int64]seen{}
	for _, r := range recs {
		if locked[r.ID] || r.Status == StatusInitializing || r.Status == StatusDeleting {
			continue
		}
		changes := m.changeCount(r.ID)
		st, err := m.d.Platform.State(ctx, m.vmName(r.ID))
		if err != nil {
			continue
		}
		if status, ok := statusOf(st); ok {
			states[r.ID] = seen{status, changes}
		}
	}
	if len(states) == 0 {
		return
	}

	if err := stor.Tx(func(tx store.Tx) error {
		locked, err := lockedServers(tx)
		if err != nil {
			return err
		}
		for id, s := range states {
			if locked[id] || m.changeCount(id) != s.changes {
				continue
			}
			r, err := Load(tx, id)
			if err != nil {
				continue // deleted meanwhile
			}
			if r.Status == s.status || r.Status == StatusInitializing || r.Status == StatusDeleting ||
				(r.Status == StatusStopping && s.status == StatusRunning) {
				continue
			}
			slog.Info("server status follows its VM", "id", id, "from", r.Status, "to", s.status)
			r.Status = s.status
			if err := put(tx, r); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		slog.Error("server status watch", "err", err)
	}
}

// lockedServers are the IDs of the servers a running action references.
func lockedServers(tx store.Tx) (map[int64]bool, error) {
	running, err := store.RunningActions(tx)
	if err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for _, a := range running {
		var refs []actions.Ref
		if err := json.Unmarshal([]byte(a.Resources), &refs); err != nil {
			return nil, err
		}
		for _, ref := range refs {
			if ref.Type == refType {
				out[ref.ID] = true
			}
		}
	}
	return out, nil
}
