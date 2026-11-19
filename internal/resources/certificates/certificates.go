// Package certificates is the certificates resource: uploaded certificates
// with their key, and managed ones issued by the hlab CA.
package certificates

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/scheduler"
	"github.com/webcodingcat/hlab/internal/store"
)

// Kind is the store kind and ID sequence of certificates.
const Kind = "certificates"

// refType is the resource type of a certificate in actions.
const refType = "certificate"

// maxName is the longest certificate name.
const maxName = 128

// Certificate types.
const (
	TypeUploaded = "uploaded"
	TypeManaged  = "managed"
)

// ErrNotIssued is what KeyPair returns for a managed certificate that has
// no leaf yet.
var ErrNotIssued = errors.New("certificate not issued")

// Issuer signs a leaf for domains: the leaf followed by its chain, and the
// key, both PEM.
type Issuer interface {
	Issue(domains []string) (certPEM, keyPEM []byte, err error)
}

// Deps are what the module needs.
type Deps struct {
	// Store is what the jobs work on outside a request; the handlers use
	// the store of the request.
	Store *store.Store
	// CA issues managed certificates; nil: managed creation answers 422
	// invalid_input.
	CA Issuer
	// PushAgent rebuilds and pushes the agent document.
	PushAgent func(ctx context.Context) error
	// UsedBy returns the IDs of the load balancers using certID; nil: none.
	UsedBy func(tx store.Tx, projectID, certID int64) ([]int64, error)
	// Now is the clock of timestamps; nil: time.Now.
	Now func() time.Time
	// Clock drives the renewal job; nil: real.
	Clock scheduler.Clock
}

// Ref names a resource in used_by.
type Ref struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// StatusError is status.error of a managed certificate.
type StatusError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Status is the issuance and renewal state of a managed certificate.
type Status struct {
	Issuance string       `json:"issuance"`
	Renewal  string       `json:"renewal"`
	Error    *StatusError `json:"error"`
}

// Certificate is a certificate in Hetzner's API shape.
type Certificate struct {
	ID             int64             `json:"id"`
	Name           string            `json:"name"`
	Labels         map[string]string `json:"labels"`
	Type           string            `json:"type"`
	Certificate    *string           `json:"certificate"`
	Created        time.Time         `json:"created"`
	NotValidBefore *time.Time        `json:"not_valid_before"`
	NotValidAfter  *time.Time        `json:"not_valid_after"`
	DomainNames    []string          `json:"domain_names"`
	Fingerprint    *string           `json:"fingerprint"`
	Status         *Status           `json:"status"`
	UsedBy         []Ref             `json:"used_by"`
}

// Record is the stored document of a certificate. The key in _hlab is
// never shown.
type Record struct {
	Certificate
	Hlab struct {
		ProjectID int64  `json:"project_id"`
		Key       string `json:"key,omitempty"`
	} `json:"_hlab"`
}

// Module is the certificates resource type.
type Module struct {
	d Deps
}

// New returns the module.
func New(d Deps) *Module {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Module{d: d}
}

// Type implements resource.Module.
func (m *Module) Type() string { return Kind }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /certificates", http.HandlerFunc(m.list))
	r.Handle("POST /certificates", http.HandlerFunc(m.create))
	r.Handle("GET /certificates/{id}", http.HandlerFunc(m.get))
	r.Handle("PUT /certificates/{id}", http.HandlerFunc(m.update))
	r.Handle("DELETE /certificates/{id}", http.HandlerFunc(m.delete))
	r.Handle("POST /certificates/{id}/actions/retry", http.HandlerFunc(m.retry))
	resactions.RoutesFor(r, Kind, refType)
}

// Load returns certificate id of projectID, or store.ErrNotFound.
func Load(tx store.Tx, projectID, id int64) (Record, error) {
	return store.Get[Record](tx, Kind, projectID, id)
}

// KeyPair returns the certificate (leaf+chain) and key PEM of certificate
// id of projectID; store.ErrNotFound if unknown; ErrNotIssued for a
// managed certificate that has none yet.
func KeyPair(tx store.Tx, projectID, id int64) (certPEM, keyPEM string, err error) {
	r, err := Load(tx, projectID, id)
	if err != nil {
		return "", "", err
	}
	if r.Certificate.Certificate == nil || r.Hlab.Key == "" {
		return "", "", ErrNotIssued
	}
	return *r.Certificate.Certificate, r.Hlab.Key, nil
}

func put(tx store.Tx, r Record) error {
	if r.Labels == nil {
		r.Labels = map[string]string{}
	}
	r.UsedBy = nil // computed on read
	return store.Put(tx, store.Meta{
		Kind: Kind, ID: r.ID, ProjectID: r.Hlab.ProjectID, Name: r.Name, Labels: r.Labels, Created: r.Created,
	}, r)
}

// shown is certificate r as the API shows it, with used_by from UsedBy.
func (m *Module) shown(tx store.Tx, r Record) (Certificate, error) {
	c := r.Certificate
	if c.Labels == nil {
		c.Labels = map[string]string{}
	}
	if c.DomainNames == nil {
		c.DomainNames = []string{}
	}
	c.UsedBy = []Ref{}
	lbs, err := m.usedBy(tx, r)
	if err != nil {
		return Certificate{}, err
	}
	for _, id := range lbs {
		c.UsedBy = append(c.UsedBy, Ref{ID: id, Type: "load_balancer"})
	}
	return c, nil
}

func (m *Module) usedBy(tx store.Tx, r Record) ([]int64, error) {
	if m.d.UsedBy == nil {
		return nil, nil
	}
	return m.d.UsedBy(tx, r.Hlab.ProjectID, r.ID)
}

// checkName refuses an empty or too long name and one another certificate
// of the project has.
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

var listOptions = api.ListOptions[Certificate]{
	Name:   func(c Certificate) string { return c.Name },
	Labels: func(c Certificate) map[string]string { return c.Labels },
	Sort: map[string]func(a, b Certificate) int{
		"id":      func(a, b Certificate) int { return cmp.Compare(a.ID, b.ID) },
		"name":    func(a, b Certificate) int { return cmp.Compare(a.Name, b.Name) },
		"created": func(a, b Certificate) int { return a.Created.Compare(b.Created) },
	},
	Filters: map[string]func(Certificate, []string) bool{
		"type": func(c Certificate, v []string) bool { return slices.Contains(v, c.Type) },
	},
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var out []Certificate
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		recs, err := store.List[Record](tx, Kind, p.ID)
		if err != nil {
			return err
		}
		out = make([]Certificate, 0, len(recs))
		for _, r := range recs {
			c, err := m.shown(tx, r)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return nil
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
	var c Certificate
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		c, err = m.shown(tx, r)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"certificate": c})
}

// createRequest is POST /certificates.
type createRequest struct {
	Name        string            `json:"name"`
	Labels      map[string]string `json:"labels"`
	Type        string            `json:"type"`
	Certificate string            `json:"certificate"`
	PrivateKey  string            `json:"private_key"`
	DomainNames []string          `json:"domain_names"`
}

// create stores an uploaded certificate with its key, which has no action
// and no status, or a managed one with its create_certificate action.
func (m *Module) create(w http.ResponseWriter, req *http.Request) {
	var body createRequest
	if !api.Decode(w, req, &body) {
		return
	}
	if body.Labels == nil {
		body.Labels = map[string]string{}
	}
	if body.Type == "" {
		body.Type = TypeUploaded
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	st := api.StoreFrom(req.Context())
	var c Certificate
	var action *actions.Action
	err := st.Tx(func(tx store.Tx) error {
		if err := checkName(tx, p.ID, body.Name, 0); err != nil {
			return err
		}
		if err := api.CheckLabels(body.Labels); err != nil {
			return err
		}
		if body.Type == TypeManaged {
			r, err := m.newManaged(body)
			if err != nil {
				return err
			}
			if r.ID, err = store.NextID(tx, Kind); err != nil {
				return err
			}
			r.Hlab.ProjectID = p.ID
			if err := put(tx, r); err != nil {
				return err
			}
			if action, err = acts.Start(tx, p.ID, "create_certificate", []actions.Ref{certRef(r.ID)}); err != nil {
				return err
			}
			c, err = m.shown(tx, r)
			return err
		}
		if body.Type != TypeUploaded {
			return &api.FieldError{Field: "type", Message: fmt.Sprintf("type %q is not supported", body.Type)}
		}
		if strings.TrimSpace(body.Certificate) == "" {
			return &api.FieldError{Field: "certificate", Message: "certificate is required for type uploaded"}
		}
		if strings.TrimSpace(body.PrivateKey) == "" {
			return &api.FieldError{Field: "private_key", Message: "private_key is required for type uploaded"}
		}
		leaf, err := parseUpload(body.Certificate, body.PrivateKey)
		if err != nil {
			return err
		}
		id, err := store.NextID(tx, Kind)
		if err != nil {
			return err
		}
		var r Record
		r.Certificate = Certificate{
			ID:             id,
			Name:           body.Name,
			Labels:         body.Labels,
			Type:           TypeUploaded,
			Certificate:    &body.Certificate,
			Created:        m.d.Now().UTC().Truncate(time.Second),
			NotValidBefore: &leaf.NotBefore,
			NotValidAfter:  &leaf.NotAfter,
			DomainNames:    leaf.DomainNames,
			Fingerprint:    &leaf.Fingerprint,
		}
		r.Hlab.ProjectID = p.ID
		r.Hlab.Key = body.PrivateKey
		if err := put(tx, r); err != nil {
			return err
		}
		c, err = m.shown(tx, r)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	if action != nil {
		id := c.ID
		acts.Run(action.ID, func(ctx context.Context, _ func(int)) error {
			return m.issue(ctx, st, p.ID, id, false)
		})
	}
	api.JSON(w, http.StatusCreated, map[string]any{"certificate": c, "action": action})
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
	var c Certificate
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
		c, err = m.shown(tx, r)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"certificate": c})
}

// delete removes a certificate no load balancer uses.
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
		lbs, err := m.usedBy(tx, r)
		if err != nil {
			return err
		}
		if len(lbs) > 0 {
			return &api.FieldError{Code: "resource_in_use", Message: fmt.Sprintf("certificate %d is still in use", id)}
		}
		return store.Delete(tx, Kind, p.ID, id)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
