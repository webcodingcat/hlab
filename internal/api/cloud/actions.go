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

// registerActions adds the routes of actions. Listing all actions of a
// project is gone from Hetzner's API since 2025-01-30: GET /actions returns
// only the actions whose IDs it is given.
func registerActions(api *web.API, l *lab.Lab) {
	api.Handle("GET /actions", func(r *http.Request, caller web.Caller) (int, any, error) {
		ids, err := queryIDs(r, "id")
		if err != nil {
			return 0, nil, err
		}
		actions := []schema.Action{}
		err = l.View(func(tx *state.Tx) error {
			for _, id := range ids {
				action, err := state.Get(tx, state.Actions, id)
				if err == nil && action.ProjectID == caller.ProjectID {
					actions = append(actions, actionJSON(action))
				}
			}
			return nil
		})
		return http.StatusOK, schema.ActionListResponse{Actions: actions}, err
	})

	api.Handle("GET /actions/{id}", func(r *http.Request, caller web.Caller) (int, any, error) {
		id, err := web.PathID(r, "action")
		if err != nil {
			return 0, nil, err
		}
		action, err := lab.Current(l, state.Actions, id)
		if errors.Is(err, state.ErrNotFound) || (err == nil && action.ProjectID != caller.ProjectID) {
			return 0, nil, web.NotFound("action")
		}
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, schema.ActionGetResponse{Action: actionJSON(action)}, nil
	})
}

// queryIDs returns the IDs of a query parameter given once or more; it must
// be given at least once.
func queryIDs(r *http.Request, name string) ([]int64, error) {
	values := r.URL.Query()[name]
	if len(values) == 0 {
		return nil, web.InvalidField(name, "is required")
	}
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id < 1 {
			return nil, web.InvalidField(name, "must be an ID")
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
