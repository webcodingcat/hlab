package firewalls

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/servers"
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
		out = make([]Firewall, 0, len(recs))
		for _, r := range recs {
			out = append(out, view(r))
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
	api.JSON(w, http.StatusOK, map[string]any{"firewall": view(r)})
}

// createRequest is POST /firewalls.
type createRequest struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Rules  []Rule            `json:"rules"`
}

// create stores a firewall; with rules it starts a set_firewall_rules
// action that ends after the agent push.
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
	var r Record
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
		r.Firewall = Firewall{
			ID:        id,
			Name:      body.Name,
			Labels:    body.Labels,
			Created:   time.Now().UTC().Truncate(time.Second),
			Rules:     rules,
			AppliedTo: []Applied{},
		}
		r.Hlab.ProjectID = p.ID
		if len(rules) > 0 {
			a, err := acts.Start(tx, p.ID, "set_firewall_rules", []actions.Ref{{ID: id, Type: refType}})
			if err != nil {
				return err
			}
			started = append(started, a)
		}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	for _, a := range started {
		acts.Run(a.ID, func(ctx context.Context, _ func(int)) error { return m.d.PushAgent(ctx) })
	}
	api.JSON(w, http.StatusCreated, map[string]any{"firewall": view(r), "actions": started})
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
	var r Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if r, err = Load(tx, p.ID, id); err != nil {
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
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"firewall": view(r)})
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

// The hooks below let the servers module and the agent document use the
// module; plans 06-06 (resolution, attach at create, server deletion) and
// 06-07 (agent document) fill them in. Until then no firewall reaches a
// server.

// ForServer implements servers.Firewalls: the firewalls applied to server r.
func (m *Module) ForServer(store.Tx, servers.Record) ([]servers.ServerFirewall, error) {
	return []servers.ServerFirewall{}, nil
}

// AttachAtCreate implements servers.Firewalls; firewalls cannot be applied
// yet.
func (m *Module) AttachAtCreate(store.Tx, *actions.Manager, int64, int64, bool, []int64) ([]*actions.Action, error) {
	return nil, &api.FieldError{Field: "firewalls", Message: "firewalls are not available"}
}

// ServerDeleted removes server serverID from every applied_to; nothing is
// applied yet.
func (m *Module) ServerDeleted(store.Tx, int64) error { return nil }

// Contribute implements resource.NetContributor; no firewall reaches a
// server yet.
func (m *Module) Contribute(store.Tx, *agentapi.Config) error { return nil }
