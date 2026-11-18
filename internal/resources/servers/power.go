package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/store"
)

// powerAction is one of the power actions with Hetzner's command name, the
// status the server shows while it runs ("" keeps the status), the
// platform operation and the status after it.
type powerAction struct {
	command string
	during  string
	op      platform.PowerOp
	after   string
}

var (
	actPowerOn  = powerAction{"start_server", StatusStarting, platform.PowerStart, StatusRunning}
	actPowerOff = powerAction{"stop_server", StatusStopping, platform.PowerStop, StatusOff}
	// shutdown succeeds once the request is sent; the status watch shows
	// off when the guest has turned the VM off.
	actShutdown = powerAction{"shutdown_server", StatusStopping, platform.PowerShutdown, StatusStopping}
	actReboot   = powerAction{"reboot_server", "", platform.PowerReboot, StatusRunning}
	actReset    = powerAction{"reset_server", "", platform.PowerReset, StatusRunning}
)

func (m *Module) poweron(w http.ResponseWriter, req *http.Request)  { m.power(w, req, actPowerOn) }
func (m *Module) poweroff(w http.ResponseWriter, req *http.Request) { m.power(w, req, actPowerOff) }
func (m *Module) shutdown(w http.ResponseWriter, req *http.Request) { m.power(w, req, actShutdown) }
func (m *Module) reboot(w http.ResponseWriter, req *http.Request)   { m.power(w, req, actReboot) }
func (m *Module) reset(w http.ResponseWriter, req *http.Request)    { m.power(w, req, actReset) }

// power runs a as a locking action on the server of the request. poweron
// checks the host's resources first, in the request and again in the
// action.
func (m *Module) power(w http.ResponseWriter, req *http.Request, a powerAction) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	stor := api.StoreFrom(req.Context())

	var rec Record
	if err := stor.Tx(func(tx store.Tx) error {
		var err error
		rec, err = store.Get[Record](tx, Kind, p.ID, id)
		return err
	}); err != nil {
		api.Fail(w, err)
		return
	}
	check := func(context.Context) error { return nil }
	if a.op == platform.PowerStart && rec.Status != StatusRunning {
		typ, ok := m.d.Catalog.ServerType(rec.Hlab.ServerType)
		if !ok {
			api.ServerError(w, fmt.Errorf("server type %q not in the catalog", rec.Hlab.ServerType))
			return
		}
		check = func(ctx context.Context) error {
			return m.checkResources(ctx, stor, typ.Cores, int64(typ.Memory*1024), 0)
		}
		if err := check(req.Context()); err != nil {
			api.Fail(w, err)
			return
		}
	}

	var action *actions.Action
	err := stor.Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
		if err != nil {
			return err
		}
		action, err = acts.Start(tx, p.ID, a.command, []actions.Ref{{ID: id, Type: refType}})
		if err != nil {
			return err
		}
		if a.during == "" {
			return nil
		}
		r.Status = a.during
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}

	acts.Run(action.ID, func(ctx context.Context, progress func(int)) error {
		progress(10)
		err := check(ctx)
		if err == nil {
			err = m.d.Platform.Power(ctx, m.vmName(id), a.op)
		}
		if err != nil {
			// The status follows what the VM does now.
			m.settle(ctx, stor, id)
			return err
		}
		return m.setStatus(stor, id, a.after)
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// settle sets the status of server id from its VM's state after a failed
// action; a state without a server status leaves it.
func (m *Module) settle(ctx context.Context, stor *store.Store, id int64) {
	st, err := m.d.Platform.State(ctx, m.vmName(id))
	if err != nil {
		st = platform.StateOther
	}
	status, ok := statusOf(st)
	if !ok {
		return
	}
	if err := m.setStatus(stor, id, status); err != nil && !errors.Is(err, store.ErrNotFound) {
		slog.Error("set server status after a failed action", "id", id, "err", err)
	}
}
