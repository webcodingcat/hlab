package storageboxes

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/storagepass"
	"github.com/webcodingcat/hlab/internal/store"
)

// actionRoutes registers the actions of a box under
// /storage_boxes/{id}/actions/.
func (m *Module) actionRoutes(r resource.Registrar) {
	r.Handle("POST /storage_boxes/{id}/actions/change_protection", http.HandlerFunc(m.changeProtection))
	r.Handle("POST /storage_boxes/{id}/actions/change_type", http.HandlerFunc(m.changeType))
	r.Handle("POST /storage_boxes/{id}/actions/reset_password", http.HandlerFunc(m.resetPassword))
	r.Handle("POST /storage_boxes/{id}/actions/update_access_settings", http.HandlerFunc(m.updateAccessSettings))
	r.Handle("POST /storage_boxes/{id}/actions/enable_snapshot_plan", http.HandlerFunc(m.enableSnapshotPlan))
	r.Handle("POST /storage_boxes/{id}/actions/disable_snapshot_plan", http.HandlerFunc(m.disableSnapshotPlan))
}

// changeProtection sets the delete protection present in the body.
func (m *Module) changeProtection(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Delete *bool `json:"delete"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	m.boxAction(w, req, "change_protection", nil, func(tx store.Tx, projectID int64, r *Record) error {
		if body.Delete != nil {
			r.Protection.Delete = *body.Delete
		}
		return put(tx, projectID, *r)
	})
}

// changeType moves the box to another type with a price in its location;
// a smaller type is allowed (phase 19 refuses one the data does not fit).
func (m *Module) changeType(w http.ResponseWriter, req *http.Request) {
	var body struct {
		StorageBoxType string `json:"storage_box_type"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	st, ok := m.boxType(body.StorageBoxType)
	if !ok {
		api.Fail(w, &api.FieldError{Field: "storage_box_type", Message: "unknown storage box type"})
		return
	}
	m.boxAction(w, req, "change_type", func(r Record) error {
		var loc struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(r.Location, &loc); err != nil {
			return fmt.Errorf("location of storage box %d: %w", r.ID, err)
		}
		if !slices.Contains(st.Locations, loc.Name) {
			return &api.FieldError{Field: "storage_box_type",
				Message: fmt.Sprintf("storage box type %s is not available in %s", st.Name, loc.Name)}
		}
		return nil
	}, func(tx store.Tx, projectID int64, r *Record) error {
		r.StorageBoxType = st.Raw
		return put(tx, projectID, *r)
	})
}

// resetPassword replaces the stored hashes; the plain password is only
// hashed, never kept.
func (m *Module) resetPassword(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	if err := storagepass.Check(body.Password); err != nil {
		api.Fail(w, &api.FieldError{Field: "password", Message: err.Error()})
		return
	}
	hashes, err := storagepass.Hash(body.Password)
	if err != nil {
		api.ServerError(w, err)
		return
	}
	m.boxAction(w, req, "reset_password", nil, func(tx store.Tx, projectID int64, r *Record) error {
		r.HLab.Hashes = hashes
		return put(tx, projectID, *r)
	})
}

// updateAccessSettings sets the flags present in the body and keeps the
// others.
func (m *Module) updateAccessSettings(w http.ResponseWriter, req *http.Request) {
	var body accessRequest
	if !api.Decode(w, req, &body) {
		return
	}
	m.boxAction(w, req, "update_access_settings", nil, func(tx store.Tx, projectID int64, r *Record) error {
		body.apply(&r.AccessSettings)
		return put(tx, projectID, *r)
	})
}

// snapshotPlanRequest is the body of enable_snapshot_plan; pointers tell
// an absent field from zero.
type snapshotPlanRequest struct {
	MaxSnapshots *int `json:"max_snapshots"`
	Minute       *int `json:"minute"`
	Hour         *int `json:"hour"`
	DayOfWeek    *int `json:"day_of_week"`
	DayOfMonth   *int `json:"day_of_month"`
}

// checkRange refuses an absent required value or one outside lo..hi.
func checkRange(field string, v *int, required bool, lo, hi int) error {
	if v == nil {
		if required {
			return &api.FieldError{Field: field, Message: field + " is required"}
		}
		return nil
	}
	if *v < lo || *v > hi {
		return &api.FieldError{Field: field, Message: fmt.Sprintf("%s must be between %d and %d", field, lo, hi)}
	}
	return nil
}

// validate checks the plan; limit is the type's automatic_snapshot_limit
// (nil: no upper bound).
func (b snapshotPlanRequest) validate(limit *int) error {
	maxSnapshots := math.MaxInt
	if limit != nil {
		maxSnapshots = *limit
	}
	for _, c := range []struct {
		field    string
		v        *int
		required bool
		lo, hi   int
	}{
		{"max_snapshots", b.MaxSnapshots, true, 1, maxSnapshots},
		{"minute", b.Minute, true, 0, 59},
		{"hour", b.Hour, true, 0, 23},
		{"day_of_week", b.DayOfWeek, false, 1, 7},
		{"day_of_month", b.DayOfMonth, false, 1, 31},
	} {
		if err := checkRange(c.field, c.v, c.required, c.lo, c.hi); err != nil {
			return err
		}
	}
	return nil
}

// snapshotLimit returns the automatic_snapshot_limit of the type object
// stored on box r, so a later catalog does not change it.
func snapshotLimit(r Record) (*int, error) {
	var st struct {
		AutomaticSnapshotLimit *int `json:"automatic_snapshot_limit"`
	}
	if err := json.Unmarshal(r.StorageBoxType, &st); err != nil {
		return nil, fmt.Errorf("storage box type of box %d: %w", r.ID, err)
	}
	return st.AutomaticSnapshotLimit, nil
}

// enableSnapshotPlan sets the snapshot plan, replacing an existing one;
// phase 18 only stores it.
func (m *Module) enableSnapshotPlan(w http.ResponseWriter, req *http.Request) {
	var body snapshotPlanRequest
	if !api.Decode(w, req, &body) {
		return
	}
	m.boxAction(w, req, "enable_snapshot_plan", func(r Record) error {
		limit, err := snapshotLimit(r)
		if err != nil {
			return err
		}
		return body.validate(limit)
	}, func(tx store.Tx, projectID int64, r *Record) error {
		r.SnapshotPlan = &SnapshotPlan{
			MaxSnapshots: *body.MaxSnapshots,
			Minute:       *body.Minute,
			Hour:         *body.Hour,
			DayOfWeek:    body.DayOfWeek,
			DayOfMonth:   body.DayOfMonth,
		}
		return put(tx, projectID, *r)
	})
}

// disableSnapshotPlan removes the snapshot plan.
func (m *Module) disableSnapshotPlan(w http.ResponseWriter, req *http.Request) {
	m.boxAction(w, req, "disable_snapshot_plan", nil, func(tx store.Tx, projectID int64, r *Record) error {
		r.SnapshotPlan = nil
		return put(tx, projectID, *r)
	})
}
