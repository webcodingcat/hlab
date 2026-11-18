// Package pricing serves Hetzner's prices from the catalog, with the server
// types of the merged catalog.
package pricing

import (
	"net/http"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resource"
)

// Module is the pricing resource type.
type Module struct{ cat *catalog.Catalog }

// New returns the module serving the prices of cat.
func New(cat *catalog.Catalog) *Module { return &Module{cat: cat} }

// Type implements resource.Module.
func (m *Module) Type() string { return "pricing" }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /pricing", http.HandlerFunc(m.get))
}

func (m *Module) get(w http.ResponseWriter, _ *http.Request) {
	api.JSON(w, http.StatusOK, map[string]any{"pricing": m.cat.Pricing()})
}
