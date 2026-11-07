package cloud

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// actionsListRemoved is the changelog entry of Hetzner's answer to GET
// /actions without IDs: listing all actions was removed on 2025-01-30.
const actionsListRemoved = "https://docs.hetzner.cloud/changelog#2025-01-30-listing-arbitrary-actions-in-the-actions-list-endpoint-is-removed"

// registerActions adds the routes of actions.
func registerActions(api *web.API, l *lab.Lab) {
	api.Handle("GET /actions", func(req *web.Request) (int, any, error) {
		ids, err := queryIDs(req)
		if err != nil {
			return 0, nil, err
		}
		actions := []schema.Action{}
		err = l.View(func(tx *state.Tx) error {
			for _, id := range ids {
				action, err := state.Get(tx, state.Actions, id)
				if err == nil && action.ProjectID == req.Caller.ProjectID {
					actions = append(actions, actionJSON(action))
				}
			}
			return nil
		})
		return http.StatusOK, schema.ActionListResponse{Actions: actions}, err
	})

	api.Handle("GET /actions/{id}", func(req *web.Request) (int, any, error) {
		var action *state.Action
		err := l.View(func(tx *state.Tx) error {
			var err error
			action, err = state.Get(tx, state.Actions, web.PathID(req, "id"))
			return err
		})
		if errors.Is(err, state.ErrNotFound) || (err == nil && action.ProjectID != req.Caller.ProjectID) {
			return 0, nil, web.PlainNotFound()
		}
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, schema.ActionGetResponse{Action: actionJSON(action)}, nil
	})
}

// queryIDs returns the IDs of the query parameter id, given once or more, as
// Hetzner's API reads them.
func queryIDs(req *web.Request) ([]int64, error) {
	values := req.URL.Query()["id"]
	if len(values) == 0 {
		return nil, web.Deprecated(actionsListRemoved)
	}
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, web.InvalidInteger()
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// actionJSON returns an action as the API shows it.
func actionJSON(action *state.Action) schema.Action {
	out := schema.Action{
		ID:        action.ID,
		Status:    string(action.Status),
		Command:   action.Command,
		Progress:  action.Progress,
		Started:   action.Started,
		Finished:  action.Finished,
		Resources: []schema.ActionResourceReference{},
	}
	if action.Error != nil {
		out.Error = &schema.ActionError{Code: action.Error.Code, Message: action.Error.Message}
	}
	for _, ref := range action.Resources {
		out.Resources = append(out.Resources, schema.ActionResourceReference{ID: ref.ID, Type: ref.Type})
	}
	return out
}
