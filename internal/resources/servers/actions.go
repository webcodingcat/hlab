package servers

import (
	"context"
	"net/http"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/store"
)

// consoleMessage is the fixed answer to request_console (SPEC.md "Fixed API
// answers").
const consoleMessage = "server console is not supported by hlab yet"

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

// requestConsole answers the fixed unsupported_error; it creates no action.
func (m *Module) requestConsole(w http.ResponseWriter, _ *http.Request) {
	api.Error(w, "unsupported_error", consoleMessage, nil)
}
