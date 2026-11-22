// Package storageboxtypes serves Hetzner's Storage Box types from the
// shipped catalog under the Hetzner API prefix.
package storageboxtypes

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resource"
)

// Module is the storage_box_types resource type.
type Module struct{ cat *catalog.Catalog }

// New returns the module serving the Storage Box types of cat.
func New(cat *catalog.Catalog) *Module { return &Module{cat: cat} }

// Type implements resource.Module.
func (m *Module) Type() string { return "storage_box_types" }

// Prefix implements resource.Prefixed.
func (m *Module) Prefix() string { return api.HetznerPrefix }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /storage_box_types", http.HandlerFunc(m.list))
	r.Handle("GET /storage_box_types/{id}", http.HandlerFunc(m.get))
}

// boxType is written as its catalog object.
type boxType catalog.StorageBoxType

func (st boxType) MarshalJSON() ([]byte, error) { return st.Raw, nil }

// list offers name, page and per_page only, as the description does.
func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	items := make([]boxType, 0, len(m.cat.StorageBoxTypes))
	for _, st := range m.cat.StorageBoxTypes {
		items = append(items, boxType(st))
	}
	slices.SortStableFunc(items, func(a, b boxType) int { return cmp.Compare(a.ID, b.ID) })
	api.ListRequest(w, req, "storage_box_types", items, api.ListOptions[boxType]{
		Name: func(st boxType) string { return st.Name },
	})
}

func (m *Module) get(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	for _, st := range m.cat.StorageBoxTypes {
		if st.ID == id {
			api.JSON(w, http.StatusOK, map[string]any{"storage_box_type": st.Raw})
			return
		}
	}
	api.NotFound(w)
}
