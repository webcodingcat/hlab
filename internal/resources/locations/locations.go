// Package locations serves Hetzner's locations from the catalog.
package locations

import (
	"encoding/json"
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

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	names := req.URL.Query()["name"]
	var out []json.RawMessage
	for _, l := range m.cat.Locations {
		if len(names) == 0 || slices.Contains(names, l.Name) {
			out = append(out, l.Raw)
		}
	}
	api.List(w, "locations", out)
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
