// Package actions is the resource module of Hetzner's /actions endpoints:
// GET /actions with repeated id parameters, as hcloud polls, and
// GET /actions/{id}. RoutesFor adds the action endpoints of one resource
// type to that type's module.
package actions

import (
	"cmp"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/store"
)

type module struct{}

// New returns the actions module.
func New() resource.Module { return module{} }

func (module) Type() string { return "actions" }

func (module) Routes(r resource.Registrar) {
	r.Handle("GET /actions", http.HandlerFunc(list))
	r.Handle("GET /actions/{id}", http.HandlerFunc(get))
}

// list answers GET /actions?id=..: the description offers only the
// repeated id parameter; the answer is paged like every list so that
// clients that send page and per_page get every action.
func list(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	if len(q["id"]) == 0 {
		api.InvalidInput(w, "id", "id is required")
		return
	}
	ids := make([]int64, 0, len(q["id"]))
	for _, s := range q["id"] {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id < 1 {
			api.InvalidInput(w, "id", "invalid id: "+s)
			return
		}
		ids = append(ids, id)
	}

	project, _ := api.ProjectFrom(req.Context())
	found, err := api.ActionsFrom(req.Context()).List(project.ID, ids)
	if err != nil {
		serverError(w, err)
		return
	}
	api.ListRequest(w, req, "actions", found, api.ListOptions[actions.Action]{})
}

func get(w http.ResponseWriter, req *http.Request) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil {
		notFound(w)
		return
	}
	project, _ := api.ProjectFrom(req.Context())
	a, err := api.ActionsFrom(req.Context()).Get(project.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"action": a})
}

// RoutesFor registers the generic action endpoints of resource type typ
// (the path segment, "servers") whose actions reference refType
// ("server"): GET /<typ>/actions, /<typ>/{id}/actions and
// /<typ>/{id}/actions/{action_id}.
func RoutesFor(r resource.Registrar, typ, refType string) {
	r.Handle("GET /"+typ+"/actions", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		listFor(w, req, refType, 0)
	}))
	r.Handle("GET /"+typ+"/{id}/actions", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		id, ok := api.PathID(w, req)
		if !ok {
			return
		}
		listFor(w, req, refType, id)
	}))
	r.Handle("GET /"+typ+"/{id}/actions/{action_id}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		getFor(w, req, refType)
	}))
}

// timeCompare orders unfinished (nil) times first.
func timeCompare(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	return a.Compare(*b)
}

var resourceListOptions = api.ListOptions[actions.Action]{
	Sort: map[string]func(a, b actions.Action) int{
		"id":       func(a, b actions.Action) int { return cmp.Compare(a.ID, b.ID) },
		"command":  func(a, b actions.Action) int { return cmp.Compare(a.Command, b.Command) },
		"status":   func(a, b actions.Action) int { return cmp.Compare(a.Status, b.Status) },
		"started":  func(a, b actions.Action) int { return a.Started.Compare(b.Started) },
		"finished": func(a, b actions.Action) int { return timeCompare(a.Finished, b.Finished) },
	},
	Filters: map[string]func(actions.Action, []string) bool{
		"status": func(a actions.Action, v []string) bool { return slices.Contains(v, a.Status) },
		"id": func(a actions.Action, v []string) bool {
			return slices.Contains(v, strconv.FormatInt(a.ID, 10))
		},
	},
}

func listFor(w http.ResponseWriter, req *http.Request, refType string, resourceID int64) {
	project, _ := api.ProjectFrom(req.Context())
	found, err := api.ActionsFrom(req.Context()).ListFor(project.ID, refType, resourceID)
	if err != nil {
		serverError(w, err)
		return
	}
	api.ListRequest(w, req, "actions", found, resourceListOptions)
}

// getFor answers the action action_id if it references resource id.
func getFor(w http.ResponseWriter, req *http.Request, refType string) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil {
		notFound(w)
		return
	}
	actionID, err := strconv.ParseInt(req.PathValue("action_id"), 10, 64)
	if err != nil {
		notFound(w)
		return
	}
	project, _ := api.ProjectFrom(req.Context())
	a, err := api.ActionsFrom(req.Context()).Get(project.ID, actionID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !slices.Contains(a.Resources, actions.Ref{ID: id, Type: refType})) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"action": a})
}

func notFound(w http.ResponseWriter) {
	api.Error(w, "not_found", "action not found", nil)
}

func serverError(w http.ResponseWriter, err error) {
	api.ServerError(w, err)
}
