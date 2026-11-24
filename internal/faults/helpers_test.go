package faults_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/faults"
)

// stepTimeout is the deadline of every single waiting step.
const stepTimeout = 5 * time.Second

// --- test groups ---

// groups are the requirement groups of TestFaults, registered by the test
// files of the package in init, run in name order.
var groups = map[string]func(t *testing.T){}

// register adds a requirement group (for example "FAULT-03") to TestFaults.
func register(name string, run func(t *testing.T)) {
	if _, dup := groups[name]; dup {
		panic("faults test group registered twice: " + name)
	}
	groups[name] = run
}

func TestFaults(t *testing.T) {
	names := make([]string, 0, len(groups))
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		t.Run(n, groups[n])
	}
}

// --- test clock ---

// testClock is a scheduler.Clock whose time moves only by Advance; an After
// channel fires once Advance passes its deadline.
type testClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []clockWaiter
}

type clockWaiter struct {
	at time.Time
	ch chan time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, clockWaiter{at: c.now.Add(d), ch: ch})
	return ch
}

// Advance moves the time by d and fires every After channel that is due.
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if w.at.After(c.now) {
			kept = append(kept, w)
			continue
		}
		w.ch <- c.now
	}
	c.waiters = kept
}

// --- fake effect ---

// fakeEffect records the faults it applied and undid. ApplyErr makes Apply
// fail; OnUndo, when set, runs inside Undo before it records the fault.
type fakeEffect struct {
	mu       sync.Mutex
	ApplyErr error
	OnUndo   func(f faults.Fault)
	applied  []faults.Fault
	undone   []faults.Fault
}

func (e *fakeEffect) Apply(_ context.Context, f faults.Fault) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ApplyErr != nil {
		return e.ApplyErr
	}
	e.applied = append(e.applied, f)
	return nil
}

func (e *fakeEffect) Undo(_ context.Context, f faults.Fault) error {
	e.mu.Lock()
	hook := e.OnUndo
	e.mu.Unlock()
	if hook != nil {
		hook(f)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.undone = append(e.undone, f)
	return nil
}

// Applied returns the IDs of the faults Apply accepted, in call order.
func (e *fakeEffect) Applied() []int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return faultIDs(e.applied)
}

// Undone returns the IDs of the faults Undo ran for, in call order.
func (e *fakeEffect) Undone() []int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return faultIDs(e.undone)
}

// UndoCount reports how often Undo ran for fault id.
func (e *fakeEffect) UndoCount(id int64) int {
	n := 0
	for _, u := range e.Undone() {
		if u == id {
			n++
		}
	}
	return n
}

func faultIDs(fs []faults.Fault) []int64 {
	ids := make([]int64, 0, len(fs))
	for _, f := range fs {
		ids = append(ids, f.ID)
	}
	return ids
}

// --- target lookup ---

// The fixed targets of testLookup. Project "p": servers 7 and 8, volume 7
// (attached), volume 4 (not attached), storage box 5. Project "q": server
// 7, volume 7 (attached). Locations fsn1, nbg1 and hel1 in every project.
var (
	testServers     = map[string][]int64{"p": {7, 8}, "q": {7}}
	testVolumes     = map[string][]int64{"p": {7, 4}, "q": {7}}
	testUnattached  = map[string][]int64{"p": {4}}
	testStorageBoxs = map[string][]int64{"p": {5}}
	testLocations   = []string{"fsn1", "nbg1", "hel1"}
)

// testLookup is a faults.TargetLookup over the fixed targets above.
func testLookup(_ context.Context, project, kind string, t faults.Target) error {
	switch t.Type {
	case faults.TargetServer:
		if !slices.Contains(testServers[project], t.ID) {
			return fmt.Errorf("server %d: %w", t.ID, faults.ErrNotFound)
		}
	case faults.TargetVolume:
		if !slices.Contains(testVolumes[project], t.ID) {
			return fmt.Errorf("volume %d: %w", t.ID, faults.ErrNotFound)
		}
		if kind == faults.KindVolumeUnavailable && slices.Contains(testUnattached[project], t.ID) {
			return &faults.InputError{Field: "target", Message: fmt.Sprintf("volume %d is not attached", t.ID)}
		}
	case faults.TargetStorageBox:
		if !slices.Contains(testStorageBoxs[project], t.ID) {
			return fmt.Errorf("storage box %d: %w", t.ID, faults.ErrNotFound)
		}
	case faults.TargetLocations:
		for _, l := range []string{t.From, t.To} {
			if !slices.Contains(testLocations, l) {
				return fmt.Errorf("location %q: %w", l, faults.ErrNotFound)
			}
		}
	}
	return nil
}

// --- manager ---

// allKinds are the nine fault kinds.
var allKinds = []string{
	faults.KindServerCrash, faults.KindServerFreeze, faults.KindNetwork,
	faults.KindLatency, faults.KindAPIError, faults.KindRateLimit,
	faults.KindVolumeUnavailable, faults.KindDiskSlow, faults.KindStorageBoxUnavailable,
}

// testResources are the Options.Resources of newEnv.
var testResources = []string{"servers", "ssh_keys", "storage_boxes", "volumes"}

// env is a faults.Manager on a test clock with a fake effect for every
// kind except api_error and rate_limit (their effect is the middleware),
// testLookup and testResources.
type env struct {
	M       *faults.Manager
	Clock   *testClock
	Effects map[string]*fakeEffect
}

// newEnv builds an env; mods change the options before New.
func newEnv(t testing.TB, mods ...func(*faults.Options)) *env {
	t.Helper()
	e := &env{Clock: newTestClock(), Effects: map[string]*fakeEffect{}}
	o := faults.Options{
		Clock:     e.Clock,
		Lookup:    testLookup,
		Effects:   map[string]faults.Effect{},
		Resources: slices.Clone(testResources),
	}
	for _, k := range allKinds {
		if k == faults.KindAPIError || k == faults.KindRateLimit {
			continue
		}
		fe := &fakeEffect{}
		e.Effects[k] = fe
		o.Effects[k] = fe
	}
	for _, m := range mods {
		m(&o)
	}
	e.M = faults.New(o)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), stepTimeout)
		defer cancel()
		_ = e.M.EndAll(ctx)
	})
	return e
}

// create creates a fault and fails the test on an error.
func (e *env) create(t testing.TB, project string, s faults.Spec) faults.Fault {
	t.Helper()
	f, err := e.M.Create(t.Context(), project, s)
	if err != nil {
		t.Fatalf("Create(%s, %s): %v", project, s.Kind, err)
	}
	return f
}

// listedIDs returns the IDs List(project) gives, in its order.
func (e *env) listedIDs(project string) []int64 {
	return faultIDs(e.M.List(project))
}

// --- small helpers ---

func seed(v uint64) *uint64 { return &v }

func serverTarget(id int64) *faults.Target {
	return &faults.Target{Type: faults.TargetServer, ID: id}
}

func volumeTarget(id int64) *faults.Target {
	return &faults.Target{Type: faults.TargetVolume, ID: id}
}

func storageBoxTarget(id int64) *faults.Target {
	return &faults.Target{Type: faults.TargetStorageBox, ID: id}
}

func locationsTarget(from, to string) *faults.Target {
	return &faults.Target{Type: faults.TargetLocations, From: from, To: to}
}

// apiErrorSpec is an api_error with the given code and percent and no
// filter, seed or duration.
func apiErrorSpec(code string, percent int) faults.Spec {
	return faults.Spec{Kind: faults.KindAPIError, Params: map[string]any{"code": code, "percent": percent}}
}

// rateLimitSpec is a rate_limit of perHour requests per hour.
func rateLimitSpec(perHour int) faults.Spec {
	return faults.Spec{Kind: faults.KindRateLimit, Params: map[string]any{"per_hour": perHour}}
}

// inputField returns the field of an *faults.InputError in err's chain, or
// fails the test.
func inputField(t testing.TB, err error) string {
	t.Helper()
	var ie *faults.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("error %v (%T), want *faults.InputError", err, err)
	}
	return ie.Field
}

// eventually polls cond until it holds or stepTimeout passes.
func eventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(stepTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", stepTimeout, what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
