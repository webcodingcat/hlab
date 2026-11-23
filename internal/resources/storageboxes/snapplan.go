package storageboxes

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/store"
)

// PlanEvery is the interval of the snapshot plan job.
const PlanEvery = time.Minute

// automaticPrefix starts the name of a snapshot the plan takes.
const automaticPrefix = "snapshot-daily--"

// Due returns the latest minute in (from, to] at which plan p runs, in
// UTC; day_of_week (1 Monday .. 7 Sunday) and day_of_month both must
// match when both are set (AND).
func Due(p SnapshotPlan, from, to time.Time) (time.Time, bool) {
	to = to.UTC()
	at := time.Date(to.Year(), to.Month(), to.Day(), p.Hour, p.Minute, 0, 0, time.UTC)
	if at.After(to) {
		at = at.AddDate(0, 0, -1)
	}
	for ; at.After(from); at = at.AddDate(0, 0, -1) {
		if planDay(p, at) {
			return at, true
		}
	}
	return time.Time{}, false
}

// planDay reports whether plan p runs on the day of at.
func planDay(p SnapshotPlan, at time.Time) bool {
	wd := int(at.Weekday())
	if wd == 0 {
		wd = 7
	}
	return (p.DayOfWeek == nil || *p.DayOfWeek == wd) && (p.DayOfMonth == nil || *p.DayOfMonth == at.Day())
}

// Prune returns the automatic snapshots of snaps beyond the newest max,
// oldest first; manual snapshots are never returned.
func Prune(snaps []Snapshot, max int) []Snapshot {
	var auto []Snapshot
	for _, s := range snaps {
		if s.IsAutomatic {
			auto = append(auto, s)
		}
	}
	slices.SortFunc(auto, func(a, b Snapshot) int {
		return cmp.Or(a.Created.Compare(b.Created), cmp.Compare(a.ID, b.ID))
	})
	if len(auto) <= max {
		return nil
	}
	return auto[:len(auto)-max]
}

// planRun is one automatic snapshot the plan job started.
type planRun struct {
	action    *actions.Action
	projectID int64
	box       Record
	snap      SnapshotRecord
}

// runPlans is the snapshot plan job: for each active box with a plan
// whose time falls between its last check and now, it starts
// create_snapshot for an automatic snapshot, queued behind the box's
// running actions, and prunes the box's automatic snapshots after it.
// A box seen first, or last checked before started (the cloud start),
// counts as checked at that time, so missed plan times are skipped.
func (m *Module) runPlans(ctx context.Context, started time.Time) {
	if m.d.Job == nil {
		return
	}
	st := m.acts.Store()
	now := m.clock().Now().UTC().Truncate(time.Second)
	var runs []planRun
	err := st.Tx(func(tx store.Tx) error {
		projects, err := store.ListProjects(tx)
		if err != nil {
			return err
		}
		for _, p := range projects {
			recs, err := store.List[Record](tx, Kind, p.ID)
			if err != nil {
				return err
			}
			for _, r := range recs {
				run, err := m.checkPlan(tx, p.ID, r, started, now)
				if err != nil {
					return err
				}
				if run != nil {
					runs = append(runs, *run)
				}
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("storage box snapshot plans", "err", err)
		return
	}
	for _, r := range runs {
		create := m.snapshotWork(st, r.projectID, r.box, r.snap)
		m.acts.Run(r.action.ID, func(ctx context.Context, progress func(int)) error {
			if err := create(ctx, progress); err != nil {
				return err
			}
			return m.prune(ctx, st, r.projectID, r.box)
		})
	}
}

// checkPlan checks the plan of box r of projectID in tx: it stores the
// check time now and, when the plan ran since the last check, stores an
// automatic snapshot and starts its create_snapshot.
func (m *Module) checkPlan(tx store.Tx, projectID int64, r Record, started, now time.Time) (*planRun, error) {
	if r.SnapshotPlan == nil {
		if r.HLab.PlanChecked.IsZero() {
			return nil, nil
		}
		r.HLab.PlanChecked = time.Time{}
		return nil, put(tx, projectID, r)
	}
	if r.Status != StatusActive || r.HLab.Deleting {
		return nil, nil
	}
	checked := r.HLab.PlanChecked
	if checked.IsZero() {
		checked = now
	} else if checked.Before(started) {
		checked = started
	}
	_, due := Due(*r.SnapshotPlan, checked, now)
	r.HLab.PlanChecked = now
	if err := put(tx, projectID, r); err != nil || !due {
		return nil, err
	}
	snaps, err := boxSnaps(tx, projectID, r.ID)
	if err != nil {
		return nil, err
	}
	s := SnapshotRecord{
		Snapshot: Snapshot{StorageBox: r.ID, Name: freeSnapName(snaps, automaticPrefix, now), IsAutomatic: true, Created: now},
		HLab:     SnapshotHLab{Creating: true},
	}
	if s.ID, err = store.NextID(tx, SnapshotKind); err != nil {
		return nil, err
	}
	a, err := m.acts.StartQueued(tx, projectID, createSnapCommand, []actions.Ref{boxRef(r.ID), snapRef(s.ID)}, nil)
	if err != nil {
		return nil, err
	}
	if err := putSnap(tx, projectID, s); err != nil {
		return nil, err
	}
	return &planRun{action: a, projectID: projectID, box: r, snap: s}, nil
}

// prune deletes the automatic snapshots of box beyond its plan's
// max_snapshots, oldest first, then refreshes the box's stats.
func (m *Module) prune(ctx context.Context, st *store.Store, projectID int64, box Record) error {
	var recs []SnapshotRecord
	keep := 0
	err := st.Tx(func(tx store.Tx) error {
		r, err := Load(tx, projectID, box.ID)
		if err != nil {
			return err
		}
		if r.SnapshotPlan == nil {
			return nil
		}
		keep = r.SnapshotPlan.MaxSnapshots
		recs, err = boxSnaps(tx, projectID, box.ID)
		return err
	})
	if err != nil || recs == nil {
		return err
	}
	snaps := make([]Snapshot, 0, len(recs))
	for _, s := range recs {
		snaps = append(snaps, s.Snapshot)
	}
	for _, s := range Prune(snaps, keep) {
		if err := m.removeSnapshot(ctx, st, projectID, box, SnapshotRecord{Snapshot: s}); err != nil {
			return err
		}
	}
	m.refreshStats(ctx, st, projectID, box.ID)
	return nil
}
