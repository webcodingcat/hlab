// Package images serves Hetzner's system images from the catalog and the
// project images (snapshots and backups) from the store.
package images

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/store"
)

// refType is the resource type in the actions of images.
const refType = "image"

// Deps are the dependencies of a module that serves project images too.
type Deps struct {
	Catalog *catalog.Catalog
	// DataFolder holds the image disks as images\image-<id>.vhdx.
	DataFolder string
}

// Module is the images resource type.
type Module struct {
	cat      *catalog.Catalog
	items    []image // catalog images ordered by ID
	projects bool    // serves the project images too
	dataDir  string
}

// NewModule returns the module serving the catalog images and the images
// of the project.
func NewModule(d Deps) *Module {
	m := New(d.Catalog)
	m.projects = true
	m.dataDir = d.DataFolder
	return m
}

// New returns the module serving the images of cat only.
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
func (m *Module) Type() string { return Kind }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /images", http.HandlerFunc(m.list))
	r.Handle("GET /images/{id}", http.HandlerFunc(m.get))
	if !m.projects {
		return
	}
	r.Handle("PUT /images/{id}", http.HandlerFunc(m.update))
	r.Handle("DELETE /images/{id}", http.HandlerFunc(m.delete))
	r.Handle("POST /images/{id}/actions/change_protection", http.HandlerFunc(m.changeProtection))
	resactions.RoutesFor(r, Kind, refType)
}

// image is a catalog or project image with the fields the list filters and
// sorts by; it is written as its object (Raw).
type image struct {
	catalog.Image
	created time.Time
	labels  map[string]string
	boundTo *int64
}

func (img image) MarshalJSON() ([]byte, error) { return img.Raw, nil }

// projectImage returns the list entry of a project image.
func projectImage(r Record) (image, error) {
	raw, err := json.Marshal(r.Image)
	if err != nil {
		return image{}, err
	}
	ci := catalog.Image{ID: r.ID, Type: r.Type, Status: r.Status, Architecture: r.Architecture, DiskSize: r.DiskSize, Raw: raw}
	if r.Name != nil {
		ci.Name = *r.Name
	}
	return image{Image: ci, created: r.Created, labels: r.Labels, boundTo: r.BoundTo}, nil
}

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
// include_deprecated=true. Project images follow the catalog images.
func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	withDeprecated := req.URL.Query().Get("include_deprecated") == "true"
	items := make([]image, 0, len(m.items))
	for _, img := range m.items {
		if withDeprecated || !img.Deprecated {
			items = append(items, img)
		}
	}
	if m.projects {
		p, _ := api.ProjectFrom(req.Context())
		var recs []Record
		err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
			var err error
			recs, err = store.List[Record](tx, Kind, p.ID)
			return err
		})
		if err != nil {
			api.ServerError(w, err)
			return
		}
		for _, r := range recs {
			img, err := projectImage(r)
			if err != nil {
				api.ServerError(w, err)
				return
			}
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
	if !m.projects {
		api.NotFound(w)
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var r Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		r, err = Load(tx, p.ID, id)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"image": r.Image})
}

// update changes only the fields present in the body: description, labels
// (replacing the whole set) and type, which only turns a backup into a
// snapshot that is bound to no server.
func (m *Module) update(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Description *string            `json:"description"`
		Labels      *map[string]string `json:"labels"`
		Type        *string            `json:"type"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	if body.Type != nil && *body.Type != TypeSnapshot {
		api.InvalidInput(w, "type", "type can only be changed to snapshot")
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var r Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if r, err = Load(tx, p.ID, id); err != nil {
			return err
		}
		if body.Description != nil {
			r.Description = *body.Description
		}
		if body.Labels != nil {
			if err := api.CheckLabels(*body.Labels); err != nil {
				return err
			}
			r.Labels = *body.Labels
		}
		if body.Type != nil {
			r.Type = TypeSnapshot
			r.BoundTo = nil
		}
		return Put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"image": r.Image})
}

// delete removes a project image without delete protection and its disk;
// an image still being created is locked.
func (m *Module) delete(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var r Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if r, err = Load(tx, p.ID, id); err != nil {
			return err
		}
		if r.Protection.Delete {
			return actions.ErrProtected
		}
		// create_image is still writing the disk; its failure path
		// removes the image.
		if r.Status == StatusCreating {
			return actions.ErrLocked
		}
		return Remove(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	if err := removeFile(r.Hlab.Path); err != nil {
		slog.Warn("remove the disk of a deleted image", "image", id, "path", r.Hlab.Path, "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// Delete implements resource.Deleter for project deletion: it removes the
// disk file of image id, images\image-<id>.vhdx in the data folder; the
// project deletion removes the record. A missing file is no error.
func (m *Module) Delete(_ context.Context, id int64) error {
	return removeFile(filepath.Join(m.dataDir, "images", fmt.Sprintf("image-%d.vhdx", id)))
}

// removeFile removes path; a missing file is no error.
func removeFile(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// changeProtection sets the delete protection present in the body as an
// action. The change is written with the action; its work has nothing
// left to do.
func (m *Module) changeProtection(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Delete *bool `json:"delete"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		action, err = acts.Start(tx, p.ID, "change_protection", []actions.Ref{{ID: id, Type: refType}})
		if err != nil {
			return err
		}
		if body.Delete != nil {
			r.Protection.Delete = *body.Delete
		}
		return Put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(context.Context, func(int)) error { return nil })
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}
