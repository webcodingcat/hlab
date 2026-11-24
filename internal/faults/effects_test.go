package faults_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/webcodingcat/hlab/internal/faults"
)

func init() {
	register("FAULT-10", testFault10)
	register("FAULT-07", testFault07)
}

// injectorCall is one call of a fake injector.
type injectorCall struct {
	Method  string
	Project string
	ID      int64
	Max     int
}

// fakeInjectors is a faults.ServerInjector and faults.VolumeInjector that
// records its calls.
type fakeInjectors struct {
	mu    sync.Mutex
	calls []injectorCall
}

func (f *fakeInjectors) record(c injectorCall) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return nil
}

// Calls returns the recorded calls in call order.
func (f *fakeInjectors) Calls() []injectorCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeInjectors) Crash(_ context.Context, project string, id int64) error {
	return f.record(injectorCall{Method: "Crash", Project: project, ID: id})
}

func (f *fakeInjectors) Freeze(_ context.Context, project string, id int64) error {
	return f.record(injectorCall{Method: "Freeze", Project: project, ID: id})
}

func (f *fakeInjectors) Thaw(_ context.Context, project string, id int64) error {
	return f.record(injectorCall{Method: "Thaw", Project: project, ID: id})
}

func (f *fakeInjectors) SetSystemDiskIOPS(_ context.Context, project string, id int64, max int) error {
	return f.record(injectorCall{Method: "SetSystemDiskIOPS", Project: project, ID: id, Max: max})
}

func (f *fakeInjectors) Remove(_ context.Context, project string, id int64) error {
	return f.record(injectorCall{Method: "Remove", Project: project, ID: id})
}

func (f *fakeInjectors) Restore(_ context.Context, project string, id int64) error {
	return f.record(injectorCall{Method: "Restore", Project: project, ID: id})
}

func (f *fakeInjectors) SetIOPS(_ context.Context, project string, id int64, max int) error {
	return f.record(injectorCall{Method: "SetIOPS", Project: project, ID: id, Max: max})
}

func testFault10(t *testing.T) {
	t.Run("effects", func(t *testing.T) {
		cases := []struct {
			name   string
			target *faults.Target
			method string
		}{
			{"volume", volumeTarget(7), "SetIOPS"},
			{"server", serverTarget(7), "SetSystemDiskIOPS"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				servers, volumes := &fakeInjectors{}, &fakeInjectors{}
				e := newEnv(t, func(o *faults.Options) { o.Effects = faults.Effects(servers, volumes) })
				f := e.create(t, "p", faults.Spec{Kind: faults.KindDiskSlow, Target: tc.target, Params: map[string]any{"iops": 200}})

				all := func() []injectorCall { return append(servers.Calls(), volumes.Calls()...) }
				applied := []injectorCall{{Method: tc.method, Project: "p", ID: 7, Max: 200}}
				if got := all(); !slices.Equal(got, applied) {
					t.Fatalf("calls after Apply %v, want %v", got, applied)
				}

				if err := e.M.End(t.Context(), "p", f.ID); err != nil {
					t.Fatalf("End(p, %d): %v", f.ID, err)
				}
				undone := append(applied, injectorCall{Method: tc.method, Project: "p", ID: 7, Max: 0})
				if got := all(); !slices.Equal(got, undone) {
					t.Errorf("calls after Undo %v, want %v", got, undone)
				}
			})
		}
	})
}

// netPushes records the calls of a NetEffects push function: the IDs
// Faults() held during each call. Err, when set, is the push's result.
type netPushes struct {
	mu   sync.Mutex
	Err  error
	net  *faults.NetEffects
	seen [][]int64
}

func (p *netPushes) push(_ context.Context) error {
	ids := faultIDs(p.net.Faults())
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = append(p.seen, ids)
	return p.Err
}

// Seen returns the IDs Faults() held during each push, in call order.
func (p *netPushes) Seen() [][]int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.seen)
}

// newNetEnv is an env whose only effects are those of a NetEffects, so
// the three net kinds are available only through it.
func newNetEnv(t *testing.T) (*env, *netPushes) {
	t.Helper()
	p := &netPushes{}
	p.net = faults.NewNetEffects(p.push)
	e := newEnv(t, func(o *faults.Options) { o.Effects = p.net.Effects() })
	return e, p
}

func testFault07(t *testing.T) {
	t.Run("net_effect", func(t *testing.T) {
		cases := []struct {
			name string
			spec faults.Spec
		}{
			{"network", faults.Spec{Kind: faults.KindNetwork, Target: serverTarget(7), Params: map[string]any{"traffic": "all", "loss_percent": 100}}},
			{"latency", faults.Spec{Kind: faults.KindLatency, Target: serverTarget(7), Params: map[string]any{"delay_ms": 50, "jitter_ms": 10}}},
			{"storage_box_unavailable", faults.Spec{Kind: faults.KindStorageBoxUnavailable, Target: storageBoxTarget(5), Params: map[string]any{}}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				e, p := newNetEnv(t)

				f := e.create(t, "p", tc.spec)
				if got, want := p.Seen(), [][]int64{{f.ID}}; !slices.EqualFunc(got, want, slices.Equal) {
					t.Fatalf("Faults() during the pushes of Create %v, want %v", got, want)
				}
				if got := faultIDs(p.net.Faults()); !slices.Equal(got, []int64{f.ID}) {
					t.Errorf("Faults() after Create %v, want [%d]", got, f.ID)
				}

				if err := e.M.End(t.Context(), "p", f.ID); err != nil {
					t.Fatalf("End(p, %d): %v", f.ID, err)
				}
				if got := faultIDs(p.net.Faults()); len(got) != 0 {
					t.Errorf("Faults() after End %v, want none", got)
				}
				if got, want := p.Seen(), [][]int64{{f.ID}, {}}; !slices.EqualFunc(got, want, slices.Equal) {
					t.Errorf("Faults() during the pushes of Create and End %v, want %v", got, want)
				}

				p.mu.Lock()
				p.Err = errors.New("agent unreachable")
				p.mu.Unlock()
				if refused, err := e.M.Create(t.Context(), "p", tc.spec); err == nil {
					t.Fatalf("Create(p, %s) with a failing push accepted fault %d, want an error", tc.spec.Kind, refused.ID)
				}
				if got := e.listedIDs("p"); len(got) != 0 {
					t.Errorf("List(p) after the refused create %v, want none", got)
				}
				if got := faultIDs(p.net.Faults()); len(got) != 0 {
					t.Errorf("Faults() after the refused create %v, want none", got)
				}
			})
		}
	})
}
