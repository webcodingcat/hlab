// Package loadbalancertypes serves Hetzner's load balancer types from the
// shipped catalog.
package loadbalancertypes

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resource"
)

// Module is the load_balancer_types resource type.
type Module struct{ cat *catalog.Catalog }

// New returns the module serving the load balancer types of cat.
func New(cat *catalog.Catalog) *Module { return &Module{cat: cat} }

// Type implements resource.Module.
func (m *Module) Type() string { return "load_balancer_types" }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /load_balancer_types", http.HandlerFunc(m.list))
	r.Handle("GET /load_balancer_types/{id}", http.HandlerFunc(m.get))
}

// lbType is one catalog type, written as its catalog object.
type lbType struct {
	ID   int64
	Name string
	Raw  json.RawMessage
}

func (t lbType) MarshalJSON() ([]byte, error) { return t.Raw, nil }

// types returns the catalog's types in catalog order.
func (m *Module) types() ([]lbType, error) {
	out := make([]lbType, 0, len(m.cat.LoadBalancerTypes))
	for _, raw := range m.cat.LoadBalancerTypes {
		var t struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("catalog load balancer type: %w", err)
		}
		out = append(out, lbType{ID: t.ID, Name: t.Name, Raw: raw})
	}
	return out, nil
}

// list offers name, page and per_page only, as the description does.
func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	items, err := m.types()
	if err != nil {
		api.ServerError(w, err)
		return
	}
	api.ListRequest(w, req, "load_balancer_types", items, api.ListOptions[lbType]{
		Name: func(t lbType) string { return t.Name },
	})
}

func (m *Module) get(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	items, err := m.types()
	if err != nil {
		api.ServerError(w, err)
		return
	}
	for _, t := range items {
		if t.ID == id {
			api.JSON(w, http.StatusOK, map[string]any{"load_balancer_type": t.Raw})
			return
		}
	}
	api.NotFound(w)
}
