// Package datacenters answers the datacenter endpoints with 410 Gone, as
// Hetzner's API does since 2026-10-01.
package datacenters

import (
	"net/http"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
)

// Module is the datacenters resource type.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Type implements resource.Module.
func (*Module) Type() string { return "datacenters" }

// Routes implements resource.Module.
func (*Module) Routes(r resource.Registrar) {
	gone := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		api.Error(w, http.StatusGone, "deprecated_api_endpoint",
			"datacenters have been removed from the API; use locations", nil)
	})
	r.Handle("GET /datacenters", gone)
	r.Handle("GET /datacenters/{id}", gone)
}
