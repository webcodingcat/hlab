// Package actions is the resource module of Hetzner's /actions endpoints:
// GET /actions with repeated id parameters, as hcloud polls, and
// GET /actions/{id}.
package actions

import (
	"cmp"
	"errors"
	"log/slog"
	"net/http"
	"slices"
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

// compare orders two actions by one sort field, ascending.
type compare func(a, b actions.Action) int

var sortFields = map[string]compare{
	"id":     func(a, b actions.Action) int { return cmp.Compare(a.ID, b.ID) },
	"status": func(a, b actions.Action) int { return cmp.Compare(a.Status, b.Status) },
}

func list(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	if len(q["id"]) == 0 {
		api.Error(w, http.StatusBadRequest, "invalid_input", "id is required", nil)
		return
	}
	ids := make([]int64, 0, len(q["id"]))
	for _, s := range q["id"] {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id < 1 {
			api.Error(w, http.StatusBadRequest, "invalid_input", "invalid id: "+s, nil)
			return
		}
		ids = append(ids, id)
	}
	var order []compare
	for _, s := range q["sort"] {
		c, ok := sortFields[s]
		if !ok {
			api.Error(w, http.StatusBadRequest, "invalid_input", "invalid sort: "+s, nil)
			return
		}
		order = append(order, c)
	}

	project, _ := api.ProjectFrom(req.Context())
	found, err := api.ActionsFrom(req.Context()).List(project.ID, ids)
	if err != nil {
		serverError(w, err)
		return
	}
	slices.SortStableFunc(found, func(a, b actions.Action) int {
		for _, c := range order {
			if r := c(a, b); r != 0 {
				return r
			}
		}
		return 0
	})
	api.List(w, "actions", found)
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
	api.Error(w, http.StatusNotFound, "not_found", "action not found", nil)
}

func serverError(w http.ResponseWriter, err error) {
	slog.Error("actions", "err", err)
	api.Error(w, http.StatusInternalServerError, "server_error", "internal error", nil)
}
