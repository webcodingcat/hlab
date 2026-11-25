// Package isos serves the ISO catalog and the own ISOs.
package isos

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resource"
)

// Module is the isos resource type.
type Module struct{ cat *catalog.Catalog }

// New returns the module serving the ISOs of cat.
func New(cat *catalog.Catalog) *Module { return &Module{cat: cat} }

// Type implements resource.Module.
func (m *Module) Type() string { return "isos" }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /isos", http.HandlerFunc(m.list))
	r.Handle("GET /isos/{id}", http.HandlerFunc(m.get))
}

// iso is written as its catalog object.
type iso catalog.ISO

func (i iso) MarshalJSON() ([]byte, error) { return i.Raw, nil }

// list offers name, architecture, include_architecture_wildcard (with an
// architecture filter also ISOs without architecture), sort (id, name), page
// and per_page.
// The catalog ISOs come first, then the own ISOs, each by ID; a name matches
// catalog ISOs only, as own ISOs have none.
func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	all := m.cat.AllISOs()
	items := make([]iso, 0, len(all))
	for _, i := range all {
		items = append(items, iso(i))
	}
	slices.SortStableFunc(items, func(a, b iso) int {
		if a.Own != b.Own {
			if b.Own {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.ID, b.ID)
	})
	wildcard := req.URL.Query().Get("include_architecture_wildcard") == "true"
	api.ListRequest(w, req, "isos", items, api.ListOptions[iso]{
		Name: func(i iso) string { return i.Name },
		Sort: map[string]func(a, b iso) int{
			"id":   func(a, b iso) int { return cmp.Compare(a.ID, b.ID) },
			"name": func(a, b iso) int { return cmp.Compare(a.Name, b.Name) },
		},
		Filters: map[string]func(iso, []string) bool{
			"architecture": func(i iso, v []string) bool {
				return slices.Contains(v, i.Architecture) || (wildcard && i.Architecture == "")
			},
		},
	})
}

func (m *Module) get(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	for _, i := range m.cat.AllISOs() {
		if i.ID == id {
			api.JSON(w, http.StatusOK, map[string]any{"iso": i.Raw})
			return
		}
	}
	api.NotFound(w)
}
