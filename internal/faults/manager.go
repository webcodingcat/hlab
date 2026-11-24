package faults

import (
	"cmp"
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/scheduler"
)

// Manager holds the active faults of all projects in memory.
type Manager struct {
	clock     scheduler.Clock
	lookup    TargetLookup
	effects   map[string]Effect
	resources []string

	mu      sync.Mutex
	nextID  int64
	entries map[int64]*entry
	// defaults are the default rate limit buckets by project, kept for
	// the cloud's lifetime.
	defaults map[string]*bucket
}

// entry is a fault and its lifetime state.
type entry struct {
	fault Fault
	// applied is false while Apply runs; the fault is not listed yet but
	// blocks a conflicting create.
	applied bool
	// ending is set once a caller ends the fault; Undo runs then.
	ending bool
	stop   chan struct{} // closed when the fault ends; stops its timer
	// rng draws the hits of an api_error fault; bucket is the token
	// bucket of a rate_limit fault. Both are used under Manager.mu.
	rng    *rand.Rand
	bucket *bucket
}

// New returns a Manager without faults.
func New(o Options) *Manager {
	m := &Manager{
		clock:     o.Clock,
		lookup:    o.Lookup,
		effects:   maps.Clone(o.Effects),
		resources: slices.Clone(o.Resources),
		entries:   map[int64]*entry{},
		defaults:  map[string]*bucket{},
	}
	if m.clock == nil {
		m.clock = scheduler.RealClock()
	}
	return m
}

// Create validates s, checks its target, applies its effect and lists the
// fault of project; without a seed, a kind that draws random numbers gets
// one chosen in 1..MaxChosenSeed.
func (m *Manager) Create(ctx context.Context, project string, s Spec) (Fault, error) {
	params, err := m.validate(s)
	if err != nil {
		return Fault{}, err
	}
	if s.Target != nil && m.lookup != nil {
		if err := m.lookup(ctx, project, s.Kind, *s.Target); err != nil {
			return Fault{}, err
		}
	}
	f := Fault{Project: project, Kind: s.Kind, Params: params}
	if s.Target != nil {
		t := *s.Target
		f.Target = &t
	}
	if s.Seed != nil {
		v := *s.Seed
		f.Seed = &v
	} else if takesSeed(s.Kind, params) {
		v, err := chooseSeed()
		if err != nil {
			return Fault{}, err
		}
		f.Seed = &v
	}

	m.mu.Lock()
	for _, e := range m.entries {
		if conflicts(e.fault, f) {
			m.mu.Unlock()
			return Fault{}, fmt.Errorf("fault %d of kind %s is already active on this target: %w", e.fault.ID, f.Kind, ErrConflict)
		}
	}
	m.nextID++
	f.ID = m.nextID
	f.Created = m.clock.Now()
	if s.Duration != 0 {
		ends := f.Created.Add(s.Duration)
		f.Ends = &ends
	}
	e := &entry{fault: f, stop: make(chan struct{})}
	switch f.Kind {
	case KindAPIError:
		e.rng = rand.New(rand.NewPCG(*f.Seed, *f.Seed))
	case KindRateLimit:
		perHour, _ := params["per_hour"].(int64)
		e.bucket = newBucket(float64(perHour), f.Created)
	}
	m.entries[f.ID] = e
	m.mu.Unlock()

	if eff := m.effects[f.Kind]; eff != nil {
		if err := eff.Apply(ctx, clone(f)); err != nil {
			m.mu.Lock()
			delete(m.entries, f.ID)
			m.mu.Unlock()
			return Fault{}, fmt.Errorf("apply fault %d (%s): %w", f.ID, f.Kind, err)
		}
	}
	m.mu.Lock()
	e.applied = true
	m.mu.Unlock()
	if s.Duration != 0 {
		go m.expire(e, m.clock.After(s.Duration))
	}
	return clone(f), nil
}

// expire ends the fault of e when due fires, unless it ended before.
func (m *Manager) expire(e *entry, due <-chan time.Time) {
	select {
	case <-due:
		id := e.fault.ID
		m.end(context.Background(), func(f Fault) bool { return f.ID == id })
	case <-e.stop:
	}
}

// List returns the active faults of project ("" = all projects) by ID.
func (m *Manager) List(project string) []Fault {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Fault{}
	for _, e := range m.entries {
		if e.applied && (project == "" || e.fault.Project == project) {
			out = append(out, clone(e.fault))
		}
	}
	slices.SortFunc(out, func(a, b Fault) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Count returns the number of active faults of all projects.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.entries {
		if e.applied {
			n++
		}
	}
	return n
}

// End ends fault id of project; an unknown ID or one of another project
// is ErrNotFound.
func (m *Manager) End(ctx context.Context, project string, id int64) error {
	ended := m.end(ctx, func(f Fault) bool { return f.ID == id && f.Project == project })
	if ended.n == 0 {
		return fmt.Errorf("fault %d: %w", id, ErrNotFound)
	}
	return ended.err
}

// EndProject ends every fault of project.
func (m *Manager) EndProject(ctx context.Context, project string) error {
	return m.end(ctx, func(f Fault) bool { return f.Project == project }).err
}

// EndAll ends every fault.
func (m *Manager) EndAll(ctx context.Context) error {
	return m.end(ctx, func(Fault) bool { return true }).err
}

// EndTarget ends every fault of project on the target of targetType and id.
func (m *Manager) EndTarget(ctx context.Context, project, targetType string, id int64) error {
	return m.end(ctx, func(f Fault) bool {
		return f.Project == project && f.Target != nil && f.Target.Type == targetType && f.Target.ID == id
	}).err
}

// Active reports whether a fault of kind on the target of targetType and
// id is listed (applied) in project.
func (m *Manager) Active(project, kind, targetType string, id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.entries {
		if e.applied && onTarget(e.fault, project, kind, targetType, id) {
			return true
		}
	}
	return false
}

// EndKind ends the faults of kind on the target of targetType and id in
// project; none listed is no error.
func (m *Manager) EndKind(ctx context.Context, project, kind, targetType string, id int64) error {
	return m.end(ctx, func(f Fault) bool { return onTarget(f, project, kind, targetType, id) }).err
}

// onTarget reports whether f is of kind on the target of targetType and id
// in project.
func onTarget(f Fault, project, kind, targetType string, id int64) bool {
	return f.Project == project && f.Kind == kind && f.Target != nil && f.Target.Type == targetType && f.Target.ID == id
}

type endResult struct {
	n   int
	err error
}

// end ends the listed faults that match: it stops their timers, runs Undo
// while they are still listed and removes them after. Undo errors are
// logged and joined; the faults end anyway.
func (m *Manager) end(ctx context.Context, match func(Fault) bool) endResult {
	m.mu.Lock()
	var picked []*entry
	for _, e := range m.entries {
		if e.applied && !e.ending && match(e.fault) {
			e.ending = true
			close(e.stop)
			picked = append(picked, e)
		}
	}
	m.mu.Unlock()
	slices.SortFunc(picked, func(a, b *entry) int { return cmp.Compare(a.fault.ID, b.fault.ID) })

	var errs []error
	for _, e := range picked {
		if eff := m.effects[e.fault.Kind]; eff != nil {
			if err := eff.Undo(ctx, clone(e.fault)); err != nil {
				slog.Error("undo fault", "id", e.fault.ID, "project", e.fault.Project, "kind", e.fault.Kind, "err", err)
				errs = append(errs, fmt.Errorf("undo fault %d (%s): %w", e.fault.ID, e.fault.Kind, err))
			}
		}
		m.mu.Lock()
		delete(m.entries, e.fault.ID)
		m.mu.Unlock()
	}
	return endResult{n: len(picked), err: errors.Join(errs...)}
}

// conflicts reports whether a and b are of the same kind on the same
// target of one project (api_error and rate_limit: in one project).
func conflicts(a, b Fault) bool {
	if a.Project != b.Project || a.Kind != b.Kind {
		return false
	}
	if a.Target == nil || b.Target == nil {
		return a.Target == nil && b.Target == nil
	}
	x, y := *a.Target, *b.Target
	if x.Type == TargetLocations && y.Type == TargetLocations {
		// The traffic between two locations is one target either way.
		return (x.From == y.From && x.To == y.To) || (x.From == y.To && x.To == y.From)
	}
	return x == y
}

// chooseSeed returns a random seed in 1..MaxChosenSeed.
func chooseSeed() (uint64, error) {
	var b [8]byte
	for {
		if _, err := crand.Read(b[:]); err != nil {
			return 0, fmt.Errorf("choose seed: %w", err)
		}
		if v := binary.LittleEndian.Uint64(b[:]) & MaxChosenSeed; v != 0 {
			return v, nil
		}
	}
}

// clone copies f so callers cannot change the manager's copy.
func clone(f Fault) Fault {
	if f.Target != nil {
		t := *f.Target
		f.Target = &t
	}
	f.Params = maps.Clone(f.Params)
	if f.Seed != nil {
		v := *f.Seed
		f.Seed = &v
	}
	if f.Ends != nil {
		v := *f.Ends
		f.Ends = &v
	}
	return f
}
