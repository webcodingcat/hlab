// Package images serves Hetzner's system images from the catalog.
package images

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resource"
)

// Module is the images resource type.
type Module struct{ cat *catalog.Catalog }

// New returns the module serving the images of cat.
func New(cat *catalog.Catalog) *Module { return &Module{cat: cat} }

// Type implements resource.Module.
func (m *Module) Type() string { return "images" }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /images", http.HandlerFunc(m.list))
	r.Handle("GET /images/{id}", http.HandlerFunc(m.get))
}

// list filters by name, type, architecture and include_deprecated; each
// filter but include_deprecated may be given more than once.
func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	match := func(key, v string) bool { return len(q[key]) == 0 || slices.Contains(q[key], v) }
	withDeprecated := q.Get("include_deprecated") == "true"
	var out []json.RawMessage
	for _, img := range m.cat.Images {
		if match("name", img.Name) && match("type", img.Type) && match("architecture", img.Architecture) &&
			(withDeprecated || !img.Deprecated) {
			out = append(out, img.Raw)
		}
	}
	api.List(w, "images", out)
}

func (m *Module) get(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	for _, img := range m.cat.Images {
		if img.ID == id {
			api.JSON(w, http.StatusOK, map[string]any{"image": img.Raw})
			return
		}
	}
	api.NotFound(w)
}
