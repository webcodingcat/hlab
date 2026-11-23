package servers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resources/placementgroups"
	"github.com/webcodingcat/hlab/internal/store"
)

// changeProtection sets the delete and rebuild protection present in the
// body as an action. The change is written with the action; its work has
// nothing left to do.
func (m *Module) changeProtection(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Delete  *bool `json:"delete"`
		Rebuild *bool `json:"rebuild"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
		if err != nil {
			return err
		}
		action, err = acts.Start(tx, p.ID, "change_protection", []actions.Ref{{ID: id, Type: refType}})
		if err != nil {
			return err
		}
		if body.Delete != nil {
			r.Protection.Delete = *body.Delete
		}
		if body.Rebuild != nil {
			r.Protection.Rebuild = *body.Rebuild
		}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(context.Context, func(int)) error { return nil })
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// requestConsole records a finished request_console action on the server,
// in any state and without waiting for its other actions, and answers the
// console URL and password the console service issues; the server itself
// does not change.
func (m *Module) requestConsole(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var (
		action           *actions.Action
		wssURL, password string
	)
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		if _, err := store.Get[Record](tx, Kind, p.ID, id); err != nil {
			return err
		}
		started, err := acts.StartAll(tx, p.ID, nil, []actions.Spec{{
			Command: "request_console", Resources: []actions.Ref{{ID: id, Type: refType}},
		}})
		if err != nil {
			return err
		}
		if err := acts.Succeed(tx, started); err != nil {
			return err
		}
		action = started[0]
		wssURL, password, err = m.d.Console.Issue(id, m.vmName(id))
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusCreated, map[string]any{"action": action, "wss_url": wssURL, "password": password})
}

// addToPlacementGroup makes a stopped server a member of a placement group
// as an action; the membership is written with the action.
func (m *Module) addToPlacementGroup(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		PlacementGroup json.RawMessage `json:"placement_group"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	ref := refString(body.PlacementGroup)
	m.groupAction(w, req, id, "add_to_placement_group", func(tx store.Tx, r Record) error {
		if r.Status != StatusOff {
			return errNotStopped
		}
		g, err := placementgroups.Find(tx, r.Hlab.ProjectID, ref)
		if errors.Is(err, store.ErrNotFound) {
			return &api.FieldError{Field: "placement_group", Message: "placement group " + ref + " not found"}
		}
		if err != nil {
			return err
		}
		return placementgroups.AddServer(tx, r.Hlab.ProjectID, g.ID, r.ID)
	})
}

// removeFromPlacementGroup removes a server from its placement group as an
// action; a server in no group changes nothing.
func (m *Module) removeFromPlacementGroup(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	m.groupAction(w, req, id, "remove_from_placement_group", leaveGroup)
}

// groupAction loads server id, starts the action command on it and applies
// change in the same transaction; the action has no work left.
func (m *Module) groupAction(w http.ResponseWriter, req *http.Request, id int64, command string, change func(store.Tx, Record) error) {
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
		if err != nil {
			return err
		}
		if err := change(tx, r); err != nil {
			return err
		}
		action, err = acts.Start(tx, p.ID, command, []actions.Ref{{ID: id, Type: refType}})
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(context.Context, func(int)) error { return nil })
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// leaveGroup removes server r from its placement group, if it has one.
func leaveGroup(tx store.Tx, r Record) error {
	g, ok, err := placementgroups.Of(tx, r.Hlab.ProjectID, r.ID)
	if err != nil || !ok {
		return err
	}
	return placementgroups.RemoveServer(tx, r.Hlab.ProjectID, g.ID, r.ID)
}
