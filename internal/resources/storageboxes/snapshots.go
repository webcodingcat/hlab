package storageboxes

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/scheduler"
	"github.com/webcodingcat/hlab/internal/store"
)

// SnapshotKind is the store kind of the snapshots of the boxes.
const SnapshotKind = "storage_box_snapshots"

// snapRefType is the action resource type of a snapshot.
const snapRefType = "storage_box_snapshot"

const (
	createSnapCommand = "create_snapshot"
	deleteSnapCommand = "delete_snapshot"
	maxSnapDesc       = 1000
	snapshotsPath     = "/storage_boxes/{id}/snapshots"
	snapshotPath      = snapshotsPath + "/{snapshot_id}"
	// snapshotNameLayout is the UTC time in a snapshot's name.
	snapshotNameLayout = "2006-01-02T15-04-05"
)

// snapDescPattern is the pattern of a snapshot description in the API
// description, anchored.
var snapDescPattern = regexp.MustCompile(`^[a-zA-Z0-9\-_,:<>+#!()\[\]{} ]*$`)

// errSnapLimit reports a box at the snapshot_limit of its type.
var errSnapLimit = errors.New("snapshot limit reached")

// SnapshotStats are the disk usage figures of a snapshot.
type SnapshotStats struct {
	Size           int64 `json:"size"`
	SizeFilesystem int64 `json:"size_filesystem"`
}

// Snapshot is a snapshot of a box in Hetzner's API shape. Name is the UTC
// time of the create ("2006-01-02T15-04-05"), for an automatic snapshot
// prefixed with "snapshot-daily--".
type Snapshot struct {
	ID          int64             `json:"id"`
	StorageBox  int64             `json:"storage_box"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	Stats       SnapshotStats     `json:"stats"`
	IsAutomatic bool              `json:"is_automatic"`
	Created     time.Time         `json:"created"`
}

// SnapshotHLab is the part of a stored snapshot the API never shows.
type SnapshotHLab struct {
	// Creating marks a snapshot whose create_snapshot is not yet
	// confirmed; Recover deletes it.
	Creating bool `json:"creating,omitempty"`
}

// SnapshotRecord is a stored snapshot.
type SnapshotRecord struct {
	Snapshot
	HLab SnapshotHLab `json:"_hlab"`
}

// snapshotRoutes registers the snapshots of a box under
// /storage_boxes/{id}/snapshots.
func (m *Module) snapshotRoutes(r resource.Registrar) {
	r.Handle("GET "+snapshotsPath, http.HandlerFunc(m.listSnapshots))
	r.Handle("POST "+snapshotsPath, http.HandlerFunc(m.createSnapshot))
	r.Handle("GET "+snapshotPath, http.HandlerFunc(m.getSnapshot))
	r.Handle("PUT "+snapshotPath, http.HandlerFunc(m.updateSnapshot))
	r.Handle("DELETE "+snapshotPath, http.HandlerFunc(m.deleteSnapshot))
}

func putSnap(tx store.Tx, projectID int64, s SnapshotRecord) error {
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	return store.Put(tx, store.Meta{
		Kind: SnapshotKind, ID: s.ID, ProjectID: projectID, Name: s.Name, Labels: s.Labels, Created: s.Created,
	}, s)
}

func snapRef(id int64) actions.Ref { return actions.Ref{ID: id, Type: snapRefType} }

// clock is the module's clock: Deps.Clock or the real one.
func (m *Module) clock() scheduler.Clock {
	if m.d.Clock != nil {
		return m.d.Clock
	}
	return scheduler.RealClock()
}

// boxSnaps returns the snapshots of box boxID of projectID.
func boxSnaps(tx store.Tx, projectID, boxID int64) ([]SnapshotRecord, error) {
	all, err := store.List[SnapshotRecord](tx, SnapshotKind, projectID)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(s SnapshotRecord) bool { return s.StorageBox != boxID }), nil
}

// snapsByBox returns the snapshots of all projects by box ID, each list
// ordered by ID.
func snapsByBox(tx store.Tx) (map[int64][]SnapshotRecord, error) {
	all, err := store.ListAll[SnapshotRecord](tx, SnapshotKind)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(all, func(a, b SnapshotRecord) int { return cmp.Compare(a.ID, b.ID) })
	by := map[int64][]SnapshotRecord{}
	for _, s := range all {
		by[s.StorageBox] = append(by[s.StorageBox], s)
	}
	return by, nil
}

// loadSnap returns snapshot snapID of box boxID of projectID, or
// store.ErrNotFound when either does not exist or the snapshot belongs to
// another box.
func loadSnap(tx store.Tx, projectID, boxID, snapID int64) (SnapshotRecord, error) {
	if _, err := Load(tx, projectID, boxID); err != nil {
		return SnapshotRecord{}, err
	}
	s, err := store.Get[SnapshotRecord](tx, SnapshotKind, projectID, snapID)
	if err == nil && s.StorageBox != boxID {
		err = store.ErrNotFound
	}
	return s, err
}

// snapPathIDs returns the box and snapshot IDs of the path; when one is
// not a positive integer it writes not_found and returns false.
func snapPathIDs(w http.ResponseWriter, req *http.Request) (int64, int64, bool) {
	boxID, ok := api.PathID(w, req)
	if !ok {
		return 0, 0, false
	}
	snapID, err := strconv.ParseInt(req.PathValue("snapshot_id"), 10, 64)
	if err != nil || snapID < 1 {
		api.NotFound(w)
		return 0, 0, false
	}
	return boxID, snapID, true
}

// checkSnapDescription checks a description against the API description:
// at most 1000 characters of its pattern.
func checkSnapDescription(d string) error {
	switch {
	case utf8.RuneCountInString(d) > maxSnapDesc:
		return &api.FieldError{Field: "description", Message: fmt.Sprintf("description is longer than %d characters", maxSnapDesc)}
	case !snapDescPattern.MatchString(d):
		return &api.FieldError{Field: "description",
			Message: "description may only contain a-z, A-Z, 0-9, space and -_,:<>+#!()[]{}"}
	}
	return nil
}

// snapLimit returns the snapshot_limit of the type object stored on box
// r; nil means no limit.
func snapLimit(r Record) (*int, error) {
	var t struct {
		SnapshotLimit *int `json:"snapshot_limit"`
	}
	if err := json.Unmarshal(r.StorageBoxType, &t); err != nil {
		return nil, fmt.Errorf("storage box %d: type: %w", r.ID, err)
	}
	return t.SnapshotLimit, nil
}

// freeSnapName returns prefix plus the UTC time at, moved on by whole
// seconds until no snapshot of snaps has that name.
func freeSnapName(snaps []SnapshotRecord, prefix string, at time.Time) string {
	at = at.UTC().Truncate(time.Second)
	for {
		name := prefix + at.Format(snapshotNameLayout)
		if !slices.ContainsFunc(snaps, func(s SnapshotRecord) bool { return s.Name == name }) {
			return name
		}
		at = at.Add(time.Second)
	}
}

var snapListOptions = api.ListOptions[Snapshot]{
	Name:   func(s Snapshot) string { return s.Name },
	Labels: func(s Snapshot) map[string]string { return s.Labels },
	Sort: map[string]func(a, b Snapshot) int{
		"id":      func(a, b Snapshot) int { return cmp.Compare(a.ID, b.ID) },
		"name":    func(a, b Snapshot) int { return cmp.Compare(a.Name, b.Name) },
		"created": func(a, b Snapshot) int { return a.Created.Compare(b.Created) },
	},
	Filters: map[string]func(Snapshot, []string) bool{
		"is_automatic": func(s Snapshot, v []string) bool { return slices.Contains(v, strconv.FormatBool(s.IsAutomatic)) },
	},
}

func (m *Module) listSnapshots(w http.ResponseWriter, req *http.Request) {
	boxID, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var recs []SnapshotRecord
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		if _, err := Load(tx, p.ID, boxID); err != nil {
			return err
		}
		var err error
		recs, err = boxSnaps(tx, p.ID, boxID)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	snaps := make([]Snapshot, 0, len(recs))
	for _, s := range recs {
		snaps = append(snaps, s.Snapshot)
	}
	api.ListAllRequest(w, req, "snapshots", snaps, snapListOptions)
}

func (m *Module) getSnapshot(w http.ResponseWriter, req *http.Request) {
	boxID, snapID, ok := snapPathIDs(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var s SnapshotRecord
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		s, err = loadSnap(tx, p.ID, boxID, snapID)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"snapshot": s.Snapshot})
}

// createSnapshot stores a manual snapshot named after the UTC time and
// runs create_snapshot on box and snapshot; the work is the storage
// agent's snapshot job (see snapshotWork).
func (m *Module) createSnapshot(w http.ResponseWriter, req *http.Request) {
	boxID, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Description string            `json:"description"`
		Labels      map[string]string `json:"labels"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	if err := checkSnapDescription(body.Description); err != nil {
		api.Fail(w, err)
		return
	}
	if err := api.CheckLabels(body.Labels); err != nil {
		api.Fail(w, err)
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	st := api.StoreFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	now := m.clock().Now().UTC().Truncate(time.Second)
	s := SnapshotRecord{
		Snapshot: Snapshot{StorageBox: boxID, Description: body.Description, Labels: body.Labels, Created: now},
		HLab:     SnapshotHLab{Creating: true},
	}
	var a *actions.Action
	var box Record
	limit := 0
	err := st.Tx(func(tx store.Tx) error {
		var err error
		if box, err = Load(tx, p.ID, boxID); err != nil {
			return err
		}
		if err := checkActive(box); err != nil {
			return err
		}
		snaps, err := boxSnaps(tx, p.ID, boxID)
		if err != nil {
			return err
		}
		lim, err := snapLimit(box)
		if err != nil {
			return err
		}
		manual := 0
		for _, x := range snaps {
			if !x.IsAutomatic {
				manual++
			}
		}
		if lim != nil && manual >= *lim {
			limit = *lim
			return errSnapLimit
		}
		s.Name = freeSnapName(snaps, "", now)
		if s.ID, err = store.NextID(tx, SnapshotKind); err != nil {
			return err
		}
		if a, err = begin(tx, acts, p.ID, boxID, createSnapCommand, snapRef(s.ID)); err != nil {
			return err
		}
		return putSnap(tx, p.ID, s)
	})
	if errors.Is(err, errSnapLimit) {
		api.Error(w, "resource_limit_exceeded",
			fmt.Sprintf("the storage box type allows %d manual snapshots", limit),
			map[string]any{"limits": []any{map[string]any{"name": "snapshot_limit"}}})
		return
	}
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(a.ID, m.snapshotWork(st, p.ID, box, s))
	api.JSON(w, http.StatusCreated, map[string]any{
		"snapshot": map[string]any{"id": s.ID, "storage_box": boxID},
		"action":   a,
	})
}

// snapshotWork is the work of a create_snapshot of stored snapshot s of
// box of projectID: the storage agent's snapshot job, then the record
// loses its creating mark and gets the sizes the job reports, and the
// box's stats are refreshed. When the job fails the record is deleted.
func (m *Module) snapshotWork(st *store.Store, projectID int64, box Record, s SnapshotRecord) func(context.Context, func(int)) error {
	return func(ctx context.Context, _ func(int)) error {
		res, err := m.d.Job(ctx, agentapi.StorageJob{
			Kind: agentapi.StorageJobSnapshot, Box: userName(box.HLab.N), Snapshot: s.Name,
		})
		if err != nil {
			if derr := st.Tx(func(tx store.Tx) error {
				return store.Delete(tx, SnapshotKind, projectID, s.ID)
			}); derr != nil {
				return errors.Join(err, derr)
			}
			return err
		}
		err = st.Tx(func(tx store.Tx) error {
			r, err := store.Get[SnapshotRecord](tx, SnapshotKind, projectID, s.ID)
			if err != nil {
				return err
			}
			r.HLab.Creating = false
			r.Stats = SnapshotStats{Size: res.SnapshotSizes[s.Name], SizeFilesystem: res.SnapshotFS[s.Name]}
			return putSnap(tx, projectID, r)
		})
		if err != nil {
			return err
		}
		m.refreshStats(ctx, st, projectID, box.ID)
		return nil
	}
}

// updateSnapshot changes the description and labels present in the body
// at once; labels, when present, replace the whole set.
func (m *Module) updateSnapshot(w http.ResponseWriter, req *http.Request) {
	boxID, snapID, ok := snapPathIDs(w, req)
	if !ok {
		return
	}
	var body struct {
		Description *string            `json:"description"`
		Labels      *map[string]string `json:"labels"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var s SnapshotRecord
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if s, err = loadSnap(tx, p.ID, boxID, snapID); err != nil {
			return err
		}
		if body.Description != nil {
			if err := checkSnapDescription(*body.Description); err != nil {
				return err
			}
			s.Description = *body.Description
		}
		if body.Labels != nil {
			if err := api.CheckLabels(*body.Labels); err != nil {
				return err
			}
			s.Labels = *body.Labels
		}
		return putSnap(tx, p.ID, s)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	api.JSON(w, http.StatusOK, map[string]any{"snapshot": s.Snapshot})
}

// deleteSnapshot runs delete_snapshot on box and snapshot: the storage
// agent's delete-snapshot job, then the record goes. It never answers
// locked: it waits as running behind the box's actions, and when box or
// snapshot is gone at its turn it succeeds without a job.
func (m *Module) deleteSnapshot(w http.ResponseWriter, req *http.Request) {
	boxID, snapID, ok := snapPathIDs(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	st := api.StoreFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var a *actions.Action
	err := st.Tx(func(tx store.Tx) error {
		if _, err := loadSnap(tx, p.ID, boxID, snapID); err != nil {
			return err
		}
		var err error
		a, err = acts.StartQueued(tx, p.ID, deleteSnapCommand, []actions.Ref{boxRef(boxID), snapRef(snapID)}, nil)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(a.ID, func(ctx context.Context, _ func(int)) error {
		var box Record
		var s SnapshotRecord
		err := st.Tx(func(tx store.Tx) error {
			var err error
			if s, err = loadSnap(tx, p.ID, boxID, snapID); err != nil {
				return err
			}
			box, err = Load(tx, p.ID, boxID)
			return err
		})
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := m.removeSnapshot(ctx, st, p.ID, box, s); err != nil {
			return err
		}
		m.refreshStats(ctx, st, p.ID, boxID)
		return nil
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": a})
}

// removeSnapshot runs the storage agent's delete-snapshot job for stored
// snapshot s of box of projectID, then deletes the record.
func (m *Module) removeSnapshot(ctx context.Context, st *store.Store, projectID int64, box Record, s SnapshotRecord) error {
	_, err := m.d.Job(ctx, agentapi.StorageJob{
		Kind: agentapi.StorageJobDeleteSnapshot, Box: userName(box.HLab.N), Snapshot: s.Name,
	})
	if err != nil {
		return err
	}
	return st.Tx(func(tx store.Tx) error {
		return store.Delete(tx, SnapshotKind, projectID, s.ID)
	})
}
