// Package images serves Hetzner's system images from the catalog.
package images

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resource"
)

// Module is the images resource type.
type Module struct {
	cat   *catalog.Catalog
	items []image // catalog images ordered by ID
}

// New returns the module serving the images of cat.
func New(cat *catalog.Catalog) *Module {
	m := &Module{cat: cat}
	for _, img := range cat.Images {
		// The catalog was checked when it was loaded; a field that does not
		// decode stays at its zero value.
		var extra struct {
			Created time.Time         `json:"created"`
			Labels  map[string]string `json:"labels"`
			BoundTo *int64            `json:"bound_to"`
		}
		_ = json.Unmarshal(img.Raw, &extra)
		m.items = append(m.items, image{Image: img, created: extra.Created, labels: extra.Labels, boundTo: extra.BoundTo})
	}
	slices.SortStableFunc(m.items, func(a, b image) int { return cmp.Compare(a.ID, b.ID) })
	return m
}

// Type implements resource.Module.
func (m *Module) Type() string { return "images" }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /images", http.HandlerFunc(m.list))
	r.Handle("GET /images/{id}", http.HandlerFunc(m.get))
}

// image is a catalog image with the fields the list filters and sorts by;
// it is written as its catalog object.
type image struct {
	catalog.Image
	created time.Time
	labels  map[string]string
	boundTo *int64
}

func (img image) MarshalJSON() ([]byte, error) { return img.Raw, nil }

// in reports whether v is one of the values of a repeated filter.
func in(values []string, v string) bool { return slices.Contains(values, v) }

var listOptions = api.ListOptions[image]{
	Name:   func(img image) string { return img.Name },
	Labels: func(img image) map[string]string { return img.labels },
	Sort: map[string]func(a, b image) int{
		"id":      func(a, b image) int { return cmp.Compare(a.ID, b.ID) },
		"name":    func(a, b image) int { return cmp.Compare(a.Name, b.Name) },
		"created": func(a, b image) int { return a.created.Compare(b.created) },
	},
	Filters: map[string]func(image, []string) bool{
		"type":         func(img image, v []string) bool { return in(v, img.Type) },
		"status":       func(img image, v []string) bool { return in(v, img.Status) },
		"architecture": func(img image, v []string) bool { return in(v, img.Architecture) },
		"bound_to": func(img image, v []string) bool {
			return img.boundTo != nil && in(v, fmt.Sprint(*img.boundTo))
		},
	},
}

// list offers the parameters of the description: name, label_selector,
// type, status, bound_to, architecture, include_deprecated, sort (id, name,
// created), page and per_page. Deprecated images are left out unless
// include_deprecated=true.
func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	withDeprecated := req.URL.Query().Get("include_deprecated") == "true"
	items := make([]image, 0, len(m.items))
	for _, img := range m.items {
		if withDeprecated || !img.Deprecated {
			items = append(items, img)
		}
	}
	api.ListRequest(w, req, "images", items, listOptions)
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
