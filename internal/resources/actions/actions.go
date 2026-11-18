// Package actions is the resource module of Hetzner's /actions endpoints:
// GET /actions with repeated id parameters, as hcloud polls, and
// GET /actions/{id}.
package actions

import (
	"errors"
	"net/http"
	"strconv"

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

func notFound(w http.ResponseWriter) {
	api.Error(w, "not_found", "action not found", nil)
}

func serverError(w http.ResponseWriter, err error) {
	api.ServerError(w, err)
}
