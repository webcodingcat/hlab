package cloud

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

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

// actionSortKeys are the sort values of a resource's action lists.
var actionSortKeys = web.SortKeys[schema.Action]{
	"id":       func(a, b schema.Action) int { return cmp.Compare(a.ID, b.ID) },
	"command":  func(a, b schema.Action) int { return cmp.Compare(a.Command, b.Command) },
	"status":   func(a, b schema.Action) int { return cmp.Compare(a.Status, b.Status) },
	"progress": func(a, b schema.Action) int { return cmp.Compare(a.Progress, b.Progress) },
	"started":  func(a, b schema.Action) int { return a.Started.Compare(b.Started) },
	"finished": func(a, b schema.Action) int {
		return cmp.Compare(timeOrZero(a.Finished).UnixNano(), timeOrZero(b.Finished).UnixNano())
	},
}

// actionStatuses are the values of an action list's status filter.
var actionStatuses = []string{string(state.ActionRunning), string(state.ActionSuccess), string(state.ActionError)}

// registerResourceActions adds the action lists of a resource ("servers",
// whose actions name it "server"): all of them, one, those of one
// resource, and one of those. Unlike GET /actions, the lists need no IDs
// (measured 2026-11-07); a resource that is gone has none, not a 404.
func registerResourceActions(api *web.API, l *lab.Lab, path, resourceType string) {
	list := func(req *web.Request, resourceID int64) (int, any, error) {
		query := req.URL.Query()
		for _, status := range query["status"] {
			if !slices.Contains(actionStatuses, status) {
				return 0, nil, web.InvalidInput("invalid status value",
					web.Field{Name: "status", Messages: []string{"value '" + status + "' is not allowed"}})
			}
		}
		ids, err := optionalIDs(req)
		if err != nil {
			return 0, nil, err
		}
		var actions []schema.Action
		_ = l.View(func(tx *state.Tx) error {
			for _, action := range state.All(tx, state.Actions) {
				statuses, filtered := query["status"]
				if action.ProjectID == req.Caller.ProjectID && namesResource(action, resourceType, resourceID) &&
					(!filtered || slices.Contains(statuses, string(action.Status))) &&
					(len(ids) == 0 || slices.Contains(ids, action.ID)) {
					actions = append(actions, actionJSON(action))
				}
			}
			return nil
		})
		if err := web.Sort(req, actions, actionSortKeys); err != nil {
			return 0, nil, err
		}
		return listJSON(req, "actions", actions)
	}
	one := func(req *web.Request, resourceID int64) (int, any, error) {
		var out schema.Action
		err := l.View(func(tx *state.Tx) error {
			action, err := state.Get(tx, state.Actions, web.PathID(req, "action_id"))
			if err != nil || action.ProjectID != req.Caller.ProjectID || !namesResource(action, resourceType, resourceID) {
				return web.NotFound("action")
			}
			out = actionJSON(action)
			return nil
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, schema.ActionGetResponse{Action: out}, nil
	}
	api.Handle("GET /"+path+"/actions", func(req *web.Request) (int, any, error) { return list(req, 0) })
	// net/http refuses …/actions/{action_id} beside …/{id}/actions: both
	// take …/actions/actions. One pattern takes both; this handler checks
	// the IDs itself, after the token, where the router would check them
	// before.
	api.Handle("GET /"+path+"/{first}/{second}", func(req *web.Request) (int, any, error) {
		first, second := req.PathValue("first"), req.PathValue("second")
		switch {
		case first == "actions":
			if id, err := strconv.ParseInt(second, 10, 64); err == nil && id > 0 {
				req.SetPathValue("action_id", second)
				return one(req, 0)
			}
		case second == "actions":
			if id, err := strconv.ParseInt(first, 10, 64); err == nil && id > 0 {
				return list(req, id)
			}
		}
		return 0, nil, web.RouteNotFound()
	})
	api.Handle("GET /"+path+"/{id}/actions/{action_id}", func(req *web.Request) (int, any, error) {
		return one(req, web.PathID(req, "id"))
	})
}

// namesResource tells whether an action names a resource of the type, of
// that ID or (0) of any.
func namesResource(action *state.Action, resourceType string, id int64) bool {
	return slices.ContainsFunc(action.Resources, func(ref state.ResourceRef) bool {
		return ref.Type == resourceType && (id == 0 || ref.ID == id)
	})
}

// timeOrZero returns the time a pointer holds, or the zero time.
func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// optionalIDs returns the IDs of the query parameter id, if any.
func optionalIDs(req *web.Request) ([]int64, error) {
	if !req.URL.Query().Has("id") {
		return nil, nil
	}
	return queryIDs(req)
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

// act runs an action of the request's resource and turns the lab's errors
// into Hetzner's.
func act(l *lab.Lab, table lab.ActionTable, command string, req *web.Request) (state.Action, error) {
	body, err := readBody(req)
	if err != nil {
		return state.Action{}, err
	}
	action, err := l.Act(table, command, lab.Request{ProjectID: req.Caller.ProjectID, ID: web.PathID(req, "id"), Body: body})
	return action, actError(err)
}

// actError turns the errors of Lab.Act and Lab.Create into Hetzner's; it
// passes the others on.
func actError(err error) error {
	var locked *lab.LockedError
	switch {
	case errors.As(err, &locked):
		return web.Locked(locked.Message)
	case errors.Is(err, lab.ErrProjectGone):
		return web.TokenInvalid()
	case errors.Is(err, lab.ErrNoRoom):
		return web.Refusal(http.StatusPreconditionFailed, "resource_unavailable", serverNoRoom)
	}
	return err
}

// readBody reads a request's body for an action's plan; an empty body is
// an empty object.
func readBody(req *web.Request) ([]byte, error) {
	var raw json.RawMessage
	if req.ContentLength == 0 {
		return []byte("{}"), nil
	}
	if err := web.DecodeJSON(req.Request, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}
