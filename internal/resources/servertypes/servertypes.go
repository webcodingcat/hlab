// Package servertypes serves Hetzner's server types from the shipped catalog.
package servertypes

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resource"
)

// Module is the server_types resource type.
type Module struct{ cat *catalog.Catalog }

// New returns the module serving the server types of cat.
func New(cat *catalog.Catalog) *Module { return &Module{cat: cat} }

// Type implements resource.Module.
func (m *Module) Type() string { return "server_types" }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /server_types", http.HandlerFunc(m.list))
	r.Handle("GET /server_types/{id}", http.HandlerFunc(m.get))
}

// serverType is written as its catalog object.
type serverType catalog.ServerType

func (st serverType) MarshalJSON() ([]byte, error) { return st.Raw, nil }

// list offers name, sort (id, name), page and per_page, as the
// description does.
func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	types := m.cat.Types()
	items := make([]serverType, 0, len(types))
	for _, st := range types {
		items = append(items, serverType(st))
	}
	slices.SortStableFunc(items, func(a, b serverType) int { return cmp.Compare(a.ID, b.ID) })
	api.ListRequest(w, req, "server_types", items, api.ListOptions[serverType]{
		Name: func(st serverType) string { return st.Name },
		Sort: map[string]func(a, b serverType) int{
			"id":   func(a, b serverType) int { return cmp.Compare(a.ID, b.ID) },
			"name": func(a, b serverType) int { return cmp.Compare(a.Name, b.Name) },
		},
	})
}

func (m *Module) get(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	for _, st := range m.cat.Types() {
		if st.ID == id {
			api.JSON(w, http.StatusOK, map[string]any{"server_type": st.Raw})
			return
		}
	}
	api.NotFound(w)
}
