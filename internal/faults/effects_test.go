package faults_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/webcodingcat/hlab/internal/faults"
)

func init() {
	register("FAULT-10", testFault10)
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
