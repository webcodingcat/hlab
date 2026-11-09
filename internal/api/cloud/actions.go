package cloud

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
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

// planFunc is the plan of one row of an action table (lab.ActionSpec.Plan):
// it checks the request, makes the quick changes and returns the slow part.
type planFunc func(tx *state.Tx, req lab.Request) (lab.Plan, error)

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

// actionMisses are Hetzner's answers to a GET of a single action that is
// not there, which differ per resource (measured 2026-11-09). An action ID
// that is no number gets invalid's answer, with the resource's ID as given
// ("" under /<things>/actions); a numeric one under a resource the caller
// does not have gets missing's, if set; else unknown's. A nil invalid is
// Hetzner's "api route not found", as for a path no route takes.
type actionMisses struct {
	invalid func(resource, action string) error
	missing func(tx *state.Tx, projectID, resource int64) error
	unknown func(action string) error
}

// actionMissesOf returns Hetzner's answers for the actions of a resource.
func actionMissesOf(path string) actionMisses {
	actionNotFound := func(action string) error {
		return web.NotFoundNull(fmt.Sprintf("action with ID '%s' not found", action))
	}
	invalidActionID := func(_, action string) error {
		return web.NotFoundNull(fmt.Sprintf("invalid action ID: '%s'", action))
	}
	switch path {
	case "networks":
		return actionMisses{
			invalid: func(resource, action string) error {
				if resource == "" {
					return actionNotFound(action)
				}
				return web.NotFoundNull(fmt.Sprintf("action with ID '%s' for network with ID '%s' not found", action, resource))
			},
			missing: missingOf(state.Networks, "network with ID %d not found"),
			unknown: actionNotFound,
		}
	case "firewalls":
		return actionMisses{
			invalid: func(_, action string) error { return actionNotFound(action) },
			missing: missingOf(state.Firewalls, "firewall with ID %d not found"),
			unknown: actionNotFound,
		}
	case "floating_ips":
		return actionMisses{invalid: invalidActionID, missing: missingOf(state.FloatingIPs, "Floating IP with ID %d not found"), unknown: actionNotFound}
	case "primary_ips":
		return actionMisses{invalid: invalidActionID, missing: missingOf(state.PrimaryIPs, "Primary IP with ID %d not found"), unknown: actionNotFound}
	}
	// the servers: "action not found" with the details {}, also under a
	// server that is not there
	return actionMisses{unknown: func(string) error { return web.NotFound("action") }}
}

// missingOf returns a check that refuses a resource of the kind that the
// caller's project does not have, with Hetzner's text and the details null.
func missingOf[R state.Owned](kind state.Kind[R], text string) func(tx *state.Tx, projectID, id int64) error {
	return func(tx *state.Tx, projectID, id int64) error {
		if record, err := state.Get(tx, kind, id); err == nil && (*record).RecordProject() == projectID {
			return nil
		}
		return web.NotFoundNull(fmt.Sprintf(text, id))
	}
}

// registerResourceActions adds the action lists of a resource ("servers",
// whose actions name it "server"): all of them, one, those of one
// resource, and one of those. Unlike GET /actions, the lists need no IDs
// (measured 2026-11-07); a resource that is gone has none, not a 404. A
// single action that is not there gets the resource's own answer
// (actionMissesOf).
func registerResourceActions(api *web.API, l *lab.Lab, path, resourceType string) {
	misses := actionMissesOf(path)
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
		statuses, filtered := query["status"]
		var actions []schema.Action
		_ = l.View(func(tx *state.Tx) error {
			for _, action := range state.All(tx, state.Actions) {
				if action.ProjectID == req.Caller.ProjectID && listedUnder(action, resourceType, resourceID) &&
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
	// one answers a single action; resource is the resource's ID as the
	// path gives it, "" under /<things>/actions
	one := func(req *web.Request, resource, actionText string) (int, any, error) {
		resourceID, resourceErr := strconv.ParseInt(resource, 10, 64)
		actionID, err := strconv.ParseInt(actionText, 10, 64)
		if err != nil || actionID < 1 || (resource != "" && (resourceErr != nil || resourceID < 1)) {
			if misses.invalid == nil {
				return 0, nil, web.RouteNotFound()
			}
			return 0, nil, misses.invalid(resource, actionText)
		}
		var out schema.Action
		err = l.View(func(tx *state.Tx) error {
			if resource != "" && misses.missing != nil {
				if err := misses.missing(tx, req.Caller.ProjectID, resourceID); err != nil {
					return err
				}
			}
			action, err := state.Get(tx, state.Actions, actionID)
			if err != nil || action.ProjectID != req.Caller.ProjectID || !listedUnder(action, resourceType, resourceID) {
				return misses.unknown(actionText)
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
	// net/http refuses …/actions/{action} beside …/{id}/actions: both
	// take …/actions/actions. One pattern takes both; this handler checks
	// the IDs itself, after the token, where the router would check them
	// before.
	api.Handle("GET /"+path+"/{first}/{second}", func(req *web.Request) (int, any, error) {
		first, second := req.PathValue("first"), req.PathValue("second")
		switch {
		case first == "actions":
			return one(req, "", second)
		case second == "actions":
			if id, err := strconv.ParseInt(first, 10, 64); err == nil && id > 0 {
				return list(req, id)
			}
		}
		return 0, nil, web.RouteNotFound()
	})
	// {action}, not {action_id}: an action ID that is no number gets the
	// resource's own answer, not the router's
	api.Handle("GET /"+path+"/{id}/actions/{action}", func(req *web.Request) (int, any, error) {
		return one(req, req.PathValue("id"), req.PathValue("action"))
	})
	registerOtherActionMethods(api, path)
}

// registerOtherActionMethods answers another method than GET on a single
// action, which is also a command the resource does not have, as Hetzner
// does (measured 2026-11-09 with POST and PUT; DELETE and PATCH are taken
// to go the same way). The servers answer 404 "404 Not Found" for an ID
// and "api route not found" for anything else; the networks, firewalls,
// Floating IPs and Primary IPs answer 405 without a body, also under
// /<path>/actions.
func registerOtherActionMethods(api *web.API, path string) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if path == "servers" {
			api.Handle(method+" /servers/{id}/actions/{action_id}", func(*web.Request) (int, any, error) {
				return 0, nil, web.Refusal(http.StatusNotFound, "not_found", "404 Not Found")
			})
			continue
		}
		notAllowed := func(req *web.Request) (int, any, error) {
			req.Header.Set("Allow", "GET, HEAD")
			return 0, nil, web.ActionMethodNotAllowed()
		}
		api.Handle(method+" /"+path+"/{id}/actions/{command}", notAllowed)
		api.Handle(method+" /"+path+"/actions/{command}", notAllowed)
	}
}

// namesResource tells whether an action names a resource of the type, of
// that ID or (0) of any.
func namesResource(action *state.Action, resourceType string, id int64) bool {
	return slices.ContainsFunc(action.Resources, func(ref state.ResourceRef) bool {
		return ref.Type == resourceType && (id == 0 || ref.ID == id)
	})
}

// networkCommands are the server's actions in a network. Hetzner lists
// them among the network's actions only, though they name the server too
// (measured 2026-11-08).
var networkCommands = []string{"attach_to_network", "detach_from_network", "change_alias_ips"}

// listedUnder tells whether a resource's action lists show an action that
// names it.
func listedUnder(action *state.Action, resourceType string, id int64) bool {
	if resourceType == lab.ServerResource && slices.Contains(networkCommands, action.Command) {
		return false
	}
	return namesResource(action, resourceType, id)
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

// actionsJSON returns actions as the API shows them.
func actionsJSON(actions []state.Action) []schema.Action {
	out := make([]schema.Action, 0, len(actions))
	for i := range actions {
		out = append(out, actionJSON(&actions[i]))
	}
	return out
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
	case errors.Is(err, lab.ErrClosed):
		// as the service answers every request once it stops
		return web.Refusal(http.StatusServiceUnavailable, "unavailable", "HLab is stopping")
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
