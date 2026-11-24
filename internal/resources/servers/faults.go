package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/faults"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/store"
)

// errCrashed ends poweron, reboot, reset and rebuild while a server_crash
// is active on the server.
var errCrashed = &actions.Error{Code: "server_error", Message: "the server's host failed (injected fault)"}

// Crash implements faults.ServerInjector: it ends a freeze of the server,
// turns its VM off hard and shows the server off at once, without an
// action. An off server stays off.
func (m *Module) Crash(ctx context.Context, project string, id int64) error {
	if err := m.faultTarget(project, id); err != nil {
		return err
	}
	m.endFreeze(ctx, project, id)
	if err := m.d.Platform.Power(ctx, m.vmName(id), platform.PowerStop); err != nil {
		return err
	}
	return m.setStatus(m.d.Store, id, StatusOff)
}

// Freeze implements faults.ServerInjector: it pauses the server's running
// VM; the status stays.
func (m *Module) Freeze(ctx context.Context, project string, id int64) error {
	if err := m.faultTarget(project, id); err != nil {
		return err
	}
	name := m.vmName(id)
	st, err := m.d.Platform.State(ctx, name)
	if err != nil {
		return err
	}
	if st != platform.StateRunning {
		return &faults.InputError{Field: "target", Message: "server is not running"}
	}
	return m.d.Platform.Power(ctx, name, platform.PowerPause)
}

// Thaw implements faults.ServerInjector: it resumes the server's VM only
// when it is Paused.
func (m *Module) Thaw(ctx context.Context, project string, id int64) error {
	if err := m.faultTarget(project, id); err != nil {
		return err
	}
	name := m.vmName(id)
	st, err := m.d.Platform.State(ctx, name)
	if err != nil || st != platform.StatePaused {
		return err
	}
	return m.d.Platform.Power(ctx, name, platform.PowerResume)
}

// SetSystemDiskIOPS implements faults.ServerInjector: it limits the
// server's system disk to max I/Os per second; 0 removes the limit.
func (m *Module) SetSystemDiskIOPS(ctx context.Context, project string, id int64, max int) error {
	if err := m.faultTarget(project, id); err != nil {
		return err
	}
	return m.d.Platform.SetDiskIOPS(ctx, m.vmName(id), m.diskPath(id), max)
}

// faultTarget checks server id of project; faults.ErrNotFound (wrapped)
// for an unknown project or server.
func (m *Module) faultTarget(project string, id int64) error {
	err := m.d.Store.Tx(func(tx store.Tx) error {
		p, err := store.ProjectByName(tx, project)
		if err != nil {
			return err
		}
		_, err = store.Get[Record](tx, Kind, p.ID, id)
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("server %d of project %s: %w", id, project, faults.ErrNotFound)
	}
	return err
}

// endFreeze ends a server_freeze on server id of project; an error is
// logged, the caller goes on.
func (m *Module) endFreeze(ctx context.Context, project string, id int64) {
	if m.d.EndFault == nil {
		return
	}
	if err := m.d.EndFault(ctx, project, faults.KindServerFreeze, faults.TargetServer, id); err != nil {
		slog.Warn("end the freeze of a server", "project", project, "id", id, "err", err)
	}
}

// crashed reports whether a server_crash is active on server id of
// project.
func (m *Module) crashed(project string, id int64) bool {
	return m.d.FaultActive != nil && m.d.FaultActive(project, faults.KindServerCrash, faults.TargetServer, id)
}
