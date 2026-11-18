// Package locations serves Hetzner's locations from the catalog.
package locations

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resource"
)

// Module is the locations resource type.
type Module struct{ cat *catalog.Catalog }

// New returns the module serving the locations of cat.
func New(cat *catalog.Catalog) *Module { return &Module{cat: cat} }

// Type implements resource.Module.
func (m *Module) Type() string { return "locations" }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /locations", http.HandlerFunc(m.list))
	r.Handle("GET /locations/{id}", http.HandlerFunc(m.get))
}

// location is written as its catalog object.
type location catalog.Location

func (l location) MarshalJSON() ([]byte, error) { return l.Raw, nil }

// list offers name, sort (id, name), page and per_page.
func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	items := make([]location, 0, len(m.cat.Locations))
	for _, l := range m.cat.Locations {
		items = append(items, location(l))
	}
	byID := func(a, b location) int { return cmp.Compare(a.ID, b.ID) }
	slices.SortStableFunc(items, byID)
	api.ListRequest(w, req, "locations", items, api.ListOptions[location]{
		Name: func(l location) string { return l.Name },
		Sort: map[string]func(a, b location) int{
			"id":   byID,
			"name": func(a, b location) int { return cmp.Compare(a.Name, b.Name) },
		},
	})
}

func (m *Module) get(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	for _, l := range m.cat.Locations {
		if l.ID == id {
			api.JSON(w, http.StatusOK, map[string]any{"location": l.Raw})
			return
		}
	}
	api.NotFound(w)
}
