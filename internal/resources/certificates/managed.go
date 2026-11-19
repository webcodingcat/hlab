package certificates

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resources/zones"
	"github.com/webcodingcat/hlab/internal/store"
)

// Issuance and renewal states of a managed certificate.
const (
	IssuancePending   = "pending"
	IssuanceCompleted = "completed"
	IssuanceFailed    = "failed"

	RenewalScheduled   = "scheduled"
	RenewalPending     = "pending"
	RenewalFailed      = "failed"
	RenewalUnavailable = "unavailable"
)

// Error codes of a failed issuance or renewal.
const (
	codeZoneNotFound  = "dns_zone_not_found"
	codeZoneSecondary = "dns_zone_is_secondary_zone"
)

func certRef(id int64) actions.Ref { return actions.Ref{ID: id, Type: refType} }

// newManaged validates a managed certificate request and returns the record
// without ID and project: issuance pending, renewal unavailable.
func (m *Module) newManaged(body createRequest) (Record, error) {
	if m.d.CA == nil {
		return Record{}, &api.FieldError{Field: "type", Message: "managed certificates are not available"}
	}
	if len(body.DomainNames) == 0 {
		return Record{}, &api.FieldError{Field: "domain_names", Message: "domain_names is required for type managed"}
	}
	for _, d := range body.DomainNames {
		if strings.TrimSpace(d) == "" {
			return Record{}, &api.FieldError{Field: "domain_names", Message: "domain names must not be empty"}
		}
	}
	var r Record
	r.Certificate = Certificate{
		Name:        body.Name,
		Labels:      body.Labels,
		Type:        TypeManaged,
		Created:     m.d.Now().UTC().Truncate(time.Second),
		DomainNames: body.DomainNames,
		Status:      &Status{Issuance: IssuancePending, Renewal: RenewalUnavailable},
	}
	return r, nil
}

// retry re-runs a failed issuance or renewal of a managed certificate.
func (m *Module) retry(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	st := api.StoreFrom(req.Context())
	var action *actions.Action
	renewal := false
	err := st.Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		s := r.Status
		if r.Type != TypeManaged || s == nil || (s.Issuance != IssuanceFailed && s.Renewal != RenewalFailed) {
			return &api.FieldError{Message: "retry needs a managed certificate whose issuance or renewal failed"}
		}
		if action, err = acts.Start(tx, p.ID, "retry_issuance", []actions.Ref{certRef(id)}); err != nil {
			return err
		}
		renewal = s.Issuance == IssuanceCompleted
		if renewal {
			s.Renewal = RenewalPending
		} else {
			s.Issuance = IssuancePending
		}
		s.Error = nil
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error {
		return m.issue(ctx, st, p.ID, id, renewal)
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// issue issues a new leaf for managed certificate id when every domain lies
// in a primary zone of the project. renewal says which state it drives:
// issuance (a first leaf) or renewal (the old leaf stays on failure). A
// failure sets the state failed with its error and returns it as an
// *actions.Error.
func (m *Module) issue(ctx context.Context, st *store.Store, projectID, id int64, renewal bool) error {
	var domains []string
	err := st.Tx(func(tx store.Tx) error {
		r, err := Load(tx, projectID, id)
		if err != nil {
			return err
		}
		domains = r.DomainNames
		return checkZones(tx, projectID, domains)
	})
	var certPEM, keyPEM []byte
	if err == nil {
		certPEM, keyPEM, err = m.d.CA.Issue(domains)
	}
	var leaf leafInfo
	if err == nil {
		leaf, err = parseUpload(string(certPEM), string(keyPEM))
	}
	var ae *actions.Error
	if err != nil && !errors.As(err, &ae) {
		ae = &actions.Error{Code: "server_error", Message: err.Error()}
	}
	err = st.Tx(func(tx store.Tx) error {
		r, err := Load(tx, projectID, id)
		if err != nil {
			return err
		}
		if r.Status == nil {
			r.Status = &Status{}
		}
		if ae != nil {
			r.Status.Error = &StatusError{Code: ae.Code, Message: ae.Message}
			if renewal {
				r.Status.Renewal = RenewalFailed
			} else {
				r.Status.Issuance = IssuanceFailed
			}
			return put(tx, r)
		}
		cert := string(certPEM)
		r.Certificate.Certificate = &cert
		r.Hlab.Key = string(keyPEM)
		r.NotValidBefore, r.NotValidAfter = &leaf.NotBefore, &leaf.NotAfter
		r.Fingerprint = &leaf.Fingerprint
		r.DomainNames = leaf.DomainNames
		r.Status = &Status{Issuance: IssuanceCompleted, Renewal: RenewalScheduled}
		return put(tx, r)
	})
	if err != nil {
		return err
	}
	if ae != nil {
		return ae
	}
	if m.d.PushAgent != nil {
		if err := m.d.PushAgent(ctx); err != nil {
			slog.Error("push agent after certificate issuance", "certificate", id, "err", err)
		}
	}
	return nil
}

// checkZones returns dns_zone_not_found unless every domain equals or lies
// below a zone of projectID, and dns_zone_is_secondary_zone when the most
// specific such zone is a secondary one.
func checkZones(tx store.Tx, projectID int64, domains []string) error {
	zs, err := store.List[zones.Record](tx, zones.Kind, projectID)
	if err != nil {
		return err
	}
	for _, d := range domains {
		name := strings.ToLower(strings.TrimSuffix(d, "."))
		var best *zones.Record
		for i := range zs {
			z := strings.ToLower(strings.TrimSuffix(zs[i].Name, "."))
			if (name == z || strings.HasSuffix(name, "."+z)) && (best == nil || len(z) > len(best.Name)) {
				best = &zs[i]
			}
		}
		switch {
		case best == nil:
			return &actions.Error{Code: codeZoneNotFound, Message: fmt.Sprintf("DNS zone not found for %s", d)}
		case best.Mode == zones.ModeSecondary:
			return &actions.Error{Code: codeZoneSecondary, Message: fmt.Sprintf("DNS zone %s of %s is a secondary zone", best.Name, d)}
		}
	}
	return nil
}
