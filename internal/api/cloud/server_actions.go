package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// serverActions is the servers' action table. Hetzner runs a server's
// actions one after another, so the table is queued: a power action
// during another waits instead of 423 (measured 2026-11-08). "create" and
// "delete" are no routes of their own: POST and DELETE /servers run them.
func serverActions(l *lab.Lab) lab.ActionTable {
	return lab.ActionTable{
		ResourceType: lab.ServerResource,
		Busy:         serverBusy,
		Queued:       true,
		Actions: map[string]lab.ActionSpec{
			"create":            {Name: "create_server", Plan: planCreateServer(l)},
			"delete":            {Name: "delete_server", Plan: planDeleteServer(l)},
			"poweron":           {Name: "start_server", Plan: planStart(l, l.PowerOnServer)},
			"poweroff":          {Name: "stop_server", Plan: planStop(l.PowerOffServer)},
			"shutdown":          {Name: "shutdown_server", Plan: planSteps(l.ShutdownServer)},
			"reboot":            {Name: "reboot_server", Plan: planStart(l, l.RebootServer)},
			"reset":             {Name: "reset_server", Plan: planStart(l, l.ResetServer)},
			"change_protection": {Name: "change_protection", Plan: planServerProtection, Unflagged: true},
		},
	}
}

// planSteps returns the plan of a power action without quick changes: it
// checks that the caller has the server, and its steps call run with it.
func planSteps(run func(ctx context.Context, id int64) error) func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		if _, err := callerServer(tx, req.ProjectID, req.ID); err != nil {
			return lab.Plan{}, err
		}
		return lab.Plan{Steps: func(ctx context.Context) error { return run(ctx, req.ID) }}, nil
	}
}

// planStop returns the plan of poweroff. It clears want_running at once,
// when the poweroff is asked for, not when its steps run. A start queued
// behind the poweroff then sees a server that does not want to run, so it
// checks the room and sets want_running again (planStart); the poweroff's
// steps leave it so. If the steps fail, want_running is as before.
func planStop(run func(ctx context.Context, id int64) error) func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		server, err := callerServer(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		wanted := server.WantRunning
		server.WantRunning = false
		return lab.Plan{
			Steps: func(ctx context.Context) error { return run(ctx, req.ID) },
			Undo:  undoServer(req.ID, func(_ *state.Tx, server *state.Server) { server.WantRunning = wanted }),
		}, nil
	}
}

// planStart returns the plan of an action that starts the server:
// poweron, and reboot and reset, which start a server that is off, as on
// Hetzner. If the server does not want to run yet, the plan checks the
// room on the host and sets want_running at once, so that the room check
// of every later action counts the server even before these steps run. If
// the steps fail, the server is off and does not want to run.
func planStart(l *lab.Lab, run func(ctx context.Context, id int64) error) func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		server, err := callerServer(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		plan := lab.Plan{Steps: func(ctx context.Context) error { return run(ctx, req.ID) }}
		if server.WantRunning {
			return plan, nil
		}
		// a type that the user's catalog dropped counts nothing, as in CheckRoom
		serverType, _ := l.Catalog().ServerTypeByName(server.Type)
		if err := l.CheckRoom(tx, server.ID, serverType); err != nil {
			return lab.Plan{}, err
		}
		server.WantRunning = true
		plan.Undo = undoServer(req.ID, func(_ *state.Tx, server *state.Server) {
			server.Status, server.WantRunning = state.ServerOff, false
		})
		return plan, nil
	}
}

// planCreateServer builds the new server's VM; the action names the image
// too. If the steps fail, the server is deleted with its VM, its files and
// its new Primary IPs.
func planCreateServer(l *lab.Lab) func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		server, err := callerServer(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		start := server.WantRunning
		return lab.Plan{
			Resources: []state.ResourceRef{{Type: "image", ID: server.ImageID}},
			Steps: func(ctx context.Context) error {
				err := l.BuildServer(ctx, req.ID)
				if err == nil {
					err = l.Sleep(ctx, lab.ServerCreate)
				}
				switch {
				case err != nil && ctx.Err() == nil:
					// the undo removes the record, and only the steps may call
					// the system: the VM, the disk and the seed go here
					return errors.Join(err, l.DeleteServerVM(ctx, req.ID))
				case err != nil:
					return err // canceled: the next start builds the server anew
				case !start:
					return l.SetServerStatus(req.ID, state.ServerOff)
				}
				return nil // the start that follows takes over
			},
			Undo: undoServer(req.ID, releaseServer),
		}, nil
	}
}

// planDeleteServer removes the server's record at once, as Hetzner's API
// does, and its VM, disk and seed in the steps. Its Primary IPs with
// auto_delete go with it, the others stay unassigned.
func planDeleteServer(l *lab.Lab) func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		server, err := callerServer(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		if server.Protection.Delete {
			return lab.Plan{}, web.Refusal(http.StatusLocked, "protected", serverDeleteProtected)
		}
		releaseServer(tx, server)
		return lab.Plan{Steps: func(ctx context.Context) error {
			if err := l.DeleteServerVM(ctx, req.ID); err != nil {
				return err
			}
			return l.Sleep(ctx, lab.ServerDelete)
		}}, nil
	}
}

// releaseServer deletes a server's record and its Primary IPs with
// auto_delete; the others stay, unassigned.
func releaseServer(tx *state.Tx, server *state.Server) {
	for _, id := range []int64{server.IPv4, server.IPv6} {
		ip, err := state.Get(tx, state.PrimaryIPs, id)
		if err != nil {
			continue
		}
		if ip.AutoDelete {
			state.Delete(tx, state.PrimaryIPs, id)
		} else {
			ip.AssigneeID = 0
		}
	}
	state.Delete(tx, state.Servers, server.ID)
}

// serverProtectionRequest is the body of change_protection; a missing
// field stays as it is.
type serverProtectionRequest struct {
	Delete  *bool `json:"delete"`
	Rebuild *bool `json:"rebuild"`
}

// planServerProtection sets the server's protection at once.
func planServerProtection(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	var body serverProtectionRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return lab.Plan{}, web.ParseFailed()
	}
	server, err := callerServer(tx, req.ProjectID, req.ID)
	if err != nil {
		return lab.Plan{}, err
	}
	if body.Delete != nil {
		server.Protection.Delete = *body.Delete
	}
	if body.Rebuild != nil {
		server.Protection.Rebuild = *body.Rebuild
	}
	return lab.Plan{}, nil
}

// undoServer returns an undo that makes change to the server, if it is still
// there: a reset of its project may have removed it meanwhile.
func undoServer(id int64, change func(tx *state.Tx, server *state.Server)) func(tx *state.Tx) error {
	return func(tx *state.Tx) error {
		server, err := state.Get(tx, state.Servers, id)
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		change(tx, server)
		return nil
	}
}
