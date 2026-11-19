package firewalls

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/store"
)

// Kind is the store kind and ID sequence of firewalls.
const Kind = "firewalls"

// refType is the resource type of a firewall in actions.
const refType = "firewall"

// maxName is the longest firewall name, as at Hetzner.
const maxName = 128

// Deps are what the module needs.
type Deps struct {
	// Store is what the hooks work on outside a request; the handlers use
	// the store of the request.
	Store *store.Store
	// PushAgent rebuilds and pushes the agent document.
	PushAgent func(ctx context.Context) error
}

// ServerRef names a server in applied_to.
type ServerRef struct {
	ID int64 `json:"id"`
}

// LabelSelector is the selector of an applied_to entry.
type LabelSelector struct {
	Selector string `json:"selector"`
}

// AppliedResource is an entry of applied_to_resources.
type AppliedResource struct {
	Type   string    `json:"type"`
	Server ServerRef `json:"server"`
}

// Applied is an entry of applied_to. It is stored as {type, server} or
// {type, label_selector}; applied_to_resources is computed on read.
type Applied struct {
	Type               string            `json:"type"`
	Server             *ServerRef        `json:"server,omitempty"`
	LabelSelector      *LabelSelector    `json:"label_selector,omitempty"`
	AppliedToResources []AppliedResource `json:"applied_to_resources,omitempty"`
}

// Firewall is a firewall in Hetzner's API shape.
type Firewall struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Labels    map[string]string `json:"labels"`
	Created   time.Time         `json:"created"`
	Rules     []Rule            `json:"rules"`
	AppliedTo []Applied         `json:"applied_to"`
}

// Record is the stored document of a firewall.
type Record struct {
	Firewall
	Hlab struct {
		ProjectID int64 `json:"project_id"`
	} `json:"_hlab"`
}

// Module is the firewalls resource type.
type Module struct {
	d Deps
}

// New returns the module.
func New(d Deps) *Module { return &Module{d: d} }

// Type implements resource.Module.
func (m *Module) Type() string { return Kind }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /firewalls", http.HandlerFunc(m.list))
	r.Handle("POST /firewalls", http.HandlerFunc(m.create))
	r.Handle("GET /firewalls/{id}", http.HandlerFunc(m.get))
	r.Handle("PUT /firewalls/{id}", http.HandlerFunc(m.update))
	r.Handle("DELETE /firewalls/{id}", http.HandlerFunc(m.delete))
	r.Handle("POST /firewalls/{id}/actions/apply_to_resources", http.HandlerFunc(m.applyToResources))
	r.Handle("POST /firewalls/{id}/actions/remove_from_resources", http.HandlerFunc(m.removeFromResources))
	r.Handle("POST /firewalls/{id}/actions/set_rules", http.HandlerFunc(m.setRules))
	resactions.RoutesFor(r, Kind, refType)
}

// Load returns firewall id of projectID, or store.ErrNotFound.
func Load(tx store.Tx, projectID, id int64) (Record, error) {
	return store.Get[Record](tx, Kind, projectID, id)
}

func put(tx store.Tx, r Record) error {
	if r.Labels == nil {
		r.Labels = map[string]string{}
	}
	return store.Put(tx, store.Meta{
		Kind: Kind, ID: r.ID, ProjectID: r.Hlab.ProjectID, Name: r.Name, Labels: r.Labels, Created: r.Created,
	}, r)
}

// view is the firewall as the API shows it.
func view(r Record) Firewall {
	f := r.Firewall
	if f.Labels == nil {
		f.Labels = map[string]string{}
	}
	if f.Rules == nil {
		f.Rules = []Rule{}
	}
	if f.AppliedTo == nil {
		f.AppliedTo = []Applied{}
	}
	return f
}

// checkName refuses an empty or too long name and one another firewall of
// the project has.
func checkName(tx store.Tx, projectID int64, name string, exceptID int64) error {
	if strings.TrimSpace(name) == "" {
		return &api.FieldError{Field: "name", Message: "name must not be empty"}
	}
	if utf8.RuneCountInString(name) > maxName {
		return &api.FieldError{Field: "name", Message: fmt.Sprintf("name must be at most %d characters", maxName)}
	}
	taken, err := store.NameTaken(tx, Kind, projectID, name, exceptID)
	if err != nil {
		return err
	}
	if taken {
		return &api.FieldError{Code: "uniqueness_error", Field: "name", Message: "name is already used"}
	}
	return nil
}

var listOptions = api.ListOptions[Firewall]{
	Name:   func(f Firewall) string { return f.Name },
	Labels: func(f Firewall) map[string]string { return f.Labels },
	Sort: map[string]func(a, b Firewall) int{
		"id":      func(a, b Firewall) int { return cmp.Compare(a.ID, b.ID) },
		"name":    func(a, b Firewall) int { return cmp.Compare(a.Name, b.Name) },
		"created": func(a, b Firewall) int { return a.Created.Compare(b.Created) },
	},
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var out []Firewall
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		recs, err := store.List[Record](tx, Kind, p.ID)
		if err != nil {
			return err
		}
		res, err := resolve(tx, p.ID)
		out = make([]Firewall, 0, len(recs))
		for _, r := range recs {
			out = append(out, res.show(r))
		}
		return err
	})
	if err != nil {
		api.ServerError(w, err)
		return
	}
	api.ListRequest(w, req, Kind, out, listOptions)
}

func (m *Module) get(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var f Firewall
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		f, err = shown(tx, r)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"firewall": f})
}

// createRequest is POST /firewalls.
type createRequest struct {
	Name    string            `json:"name"`
	Labels  map[string]string `json:"labels"`
	Rules   []Rule            `json:"rules"`
	ApplyTo []Target          `json:"apply_to"`
}

// create stores a firewall; with rules it starts a set_firewall_rules
// action, with apply_to the apply actions; each ends after the agent push.
func (m *Module) create(w http.ResponseWriter, req *http.Request) {
	var body createRequest
	if !api.Decode(w, req, &body) {
		return
	}
	if body.Labels == nil {
		body.Labels = map[string]string{}
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var f Firewall
	started := []*actions.Action{}
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		if err := checkName(tx, p.ID, body.Name, 0); err != nil {
			return err
		}
		if err := api.CheckLabels(body.Labels); err != nil {
			return err
		}
		rules, err := CheckRules(body.Rules)
		if err != nil {
			return err
		}
		id, err := store.NextID(tx, Kind)
		if err != nil {
			return err
		}
		var r Record
		r.Firewall = Firewall{
			ID:        id,
			Name:      body.Name,
			Labels:    body.Labels,
			Created:   time.Now().UTC().Truncate(time.Second),
			Rules:     rules,
			AppliedTo: []Applied{},
		}
		r.Hlab.ProjectID = p.ID
		res, err := resolve(tx, p.ID)
		if err != nil {
			return err
		}
		var specs []actions.Spec
		if len(rules) > 0 {
			specs = append(specs, actions.Spec{Command: "set_firewall_rules", Resources: []actions.Ref{fwRef(id)}})
		}
		applySpecs, err := applyTargets(tx, &r, res, body.ApplyTo, "apply_to")
		if err != nil {
			return err
		}
		if err := put(tx, r); err != nil {
			return err
		}
		a, err := acts.StartAll(tx, p.ID, nil, append(specs, applySpecs...))
		if err != nil {
			return err
		}
		started = append(started, a...)
		f, err = shown(tx, r)
		return err
	})
	if err != nil {
		failCreate(w, err)
		return
	}
	m.runPush(acts, started)
	api.JSON(w, http.StatusCreated, map[string]any{"firewall": f, "actions": started})
}

// failCreate is api.Fail, except that an unknown server in apply_to
// answers 422 as the spec of POST /firewalls says.
func failCreate(w http.ResponseWriter, err error) {
	var fe *api.FieldError
	if errors.As(err, &fe) && fe.Code == codeResourceNotFound {
		api.JSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{
			"code": fe.Code, "message": fe.Message, "details": map[string]any{},
		}})
		return
	}
	api.Fail(w, err)
}

// shown is firewall r as the API shows it.
func shown(tx store.Tx, r Record) (Firewall, error) {
	res, err := resolve(tx, r.Hlab.ProjectID)
	if err != nil {
		return Firewall{}, err
	}
	return res.show(r), nil
}

// update changes only the name and labels present in the body; labels,
// when present, replace the whole set.
func (m *Module) update(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Name   *string            `json:"name"`
		Labels *map[string]string `json:"labels"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var f Firewall
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		if body.Name != nil {
			if err := checkName(tx, p.ID, *body.Name, id); err != nil {
				return err
			}
			r.Name = *body.Name
		}
		if body.Labels != nil {
			if err := api.CheckLabels(*body.Labels); err != nil {
				return err
			}
			r.Labels = *body.Labels
		}
		if err := put(tx, r); err != nil {
			return err
		}
		f, err = shown(tx, r)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"firewall": f})
}

// delete removes a firewall that is applied nowhere, neither to a server
// nor by a label selector.
func (m *Module) delete(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		if len(r.AppliedTo) > 0 {
			return &api.FieldError{Code: "resource_in_use", Message: fmt.Sprintf("firewall %d is still in use", id)}
		}
		return store.Delete(tx, Kind, p.ID, id)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
