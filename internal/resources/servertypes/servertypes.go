// Package servertypes serves Hetzner's server types from the shipped catalog.
package servertypes

import (
	"encoding/json"
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

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	names := req.URL.Query()["name"]
	var out []json.RawMessage
	for _, st := range m.cat.Types() {
		if len(names) == 0 || slices.Contains(names, st.Name) {
			out = append(out, st.Raw)
		}
	}
	api.List(w, "server_types", out)
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
