package bridge_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/bridge"
	"github.com/webcodingcat/hlab/internal/cloudstatus"
	"github.com/webcodingcat/hlab/internal/sysint"
)

// pollInterval is the shortened poll interval of the cloud tests.
const pollInterval = 10 * time.Millisecond

// fakeSCM is the service control of hlabd: the test sets the state.
type fakeSCM struct {
	mu     sync.Mutex
	state  string
	starts [][]string
	stops  int
	// queries counts the State calls.
	queries atomic.Int32
}

var _ sysint.ServiceControl = (*fakeSCM)(nil)

func (s *fakeSCM) Start(_ context.Context, params ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, slices.Clone(params))
	return nil
}

func (s *fakeSCM) Stop(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stops++
	return nil
}

func (s *fakeSCM) State(context.Context) (string, error) {
	s.queries.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}

func (s *fakeSCM) set(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

// changes records the states OnChange reports.
type changes struct {
	mu  sync.Mutex
	all []bridge.State
}

func (c *changes) add(s bridge.State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.all = append(c.all, s)
}

func (c *changes) last() (bridge.State, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.all) == 0 {
		return bridge.State{}, false
	}
	return c.all[len(c.all)-1], true
}

// statusMux answers GET /mgmt/v1/status as a running hlabd with one
// problem.
func statusMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /mgmt/v1/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"running","version":"1.2.3","problems":[{"code":"dns_conflict","message":"port 53 is taken"}],"shadowing_zones":[]}`))
	})
	return mux
}

// runCloud starts the poller of a Cloud on scm and f with the status file
// statusFile; onChange is called after the state is recorded.
func runCloud(t *testing.T, scm *fakeSCM, f *fakeMgmt, statusFile string, onChange func(bridge.State)) (*bridge.Cloud, *changes) {
	t.Helper()
	rec := &changes{}
	c := bridge.NewCloud(bridge.CloudDeps{
		Service:    scm,
		Client:     f.client(),
		StatusFile: statusFile,
		Interval:   pollInterval,
		OnChange: func(s bridge.State) {
			rec.add(s)
			if onChange != nil {
				onChange(s)
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return c, rec
}

// waitCloud waits until the status satisfies cond and the last OnChange
// reported that status.
func waitCloud(t *testing.T, c *bridge.Cloud, rec *changes, what string, cond func(bridge.State) bool) bridge.State {
	t.Helper()
	waitFor(t, what, func() bool {
		s := c.Status()
		last, ok := rec.last()
		return cond(s) && ok && reflect.DeepEqual(last, s)
	})
	return c.Status()
}

func writeStatusFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testTray(t *testing.T) {
	refused := &cloudstatus.Problem{Code: "state_incompatible", Message: "the state is of a newer hlab", Fatal: true}
	cases := []struct {
		name     string
		state    bridge.State
		icon     string
		word     string // in tooltip and status line, any case
		canStart bool
		canStop  bool
	}{
		{"stopped", bridge.State{State: "stopped"}, "stopped", "stopped", true, false},
		{"starting", bridge.State{State: "starting"}, "busy", "starting", false, false},
		{"running", bridge.State{State: "running"}, "running", "running", false, true},
		{"stopping", bridge.State{State: "stopping"}, "busy", "stopping", false, false},
		{"refused", bridge.State{State: "refused", Refused: refused}, "error", "refused", true, false},
		{"stopped after a failed start", bridge.State{State: "stopped", Error: "open the store: disk full"}, "error", "fail", true, false},
		{"connection lost", bridge.State{State: "running", ConnectionLost: true}, "error", "connection", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := bridge.Tray(tc.state)
			if m.Icon != tc.icon {
				t.Errorf("icon %q, want %q", m.Icon, tc.icon)
			}
			if !strings.Contains(strings.ToLower(m.Tooltip), tc.word) {
				t.Errorf("tooltip %q does not say %q", m.Tooltip, tc.word)
			}
			if !strings.Contains(strings.ToLower(m.Status), tc.word) {
				t.Errorf("status line %q does not say %q", m.Status, tc.word)
			}
			if m.CanStart != tc.canStart || m.CanStop != tc.canStop {
				t.Errorf("Start enabled %v, Stop enabled %v; want %v, %v", m.CanStart, m.CanStop, tc.canStart, tc.canStop)
			}
		})
	}
}

func testControl(t *testing.T) {
	scm := &fakeSCM{state: sysint.StateStopped}
	f := newFakeMgmt(t, statusMux())
	f.down.Store(true)
	statusFile := filepath.Join(t.TempDir(), "status.json")
	c, rec := runCloud(t, scm, f, statusFile, nil)

	waitCloud(t, c, rec, "stopped without a status file", func(s bridge.State) bool {
		return s.State == "stopped" && s.Error == "" && s.Refused == nil
	})

	if err := c.Start(false); err != nil {
		t.Fatalf("Start(false): %v", err)
	}
	scm.mu.Lock()
	starts := slices.Clone(scm.starts)
	scm.mu.Unlock()
	if len(starts) != 1 || slices.Contains(starts[0], "discard-state") {
		t.Errorf("SCM starts after Start(false): %v, want one without discard-state", starts)
	}

	scm.set(sysint.StateStarting)
	waitCloud(t, c, rec, "starting", func(s bridge.State) bool { return s.State == "starting" })

	f.down.Store(false)
	scm.set(sysint.StateRunning)
	s := waitCloud(t, c, rec, "running with the problems of /status", func(s bridge.State) bool {
		return s.State == "running" && len(s.Problems) == 1
	})
	if s.Problems[0].Code != "dns_conflict" || s.Version != "1.2.3" || s.ConnectionLost {
		t.Errorf("running state %+v, want the dns_conflict problem, version 1.2.3 and no connection loss", s)
	}

	if err := c.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	scm.mu.Lock()
	stops := scm.stops
	scm.mu.Unlock()
	if stops != 1 {
		t.Errorf("SCM stops after Stop: %d, want 1", stops)
	}
	scm.set(sysint.StateStopping)
	waitCloud(t, c, rec, "stopping", func(s bridge.State) bool { return s.State == "stopping" })

	f.down.Store(true)
	writeStatusFile(t, statusFile, `{"state":"refused","problem":{"code":"state_incompatible","message":"the state is of a newer hlab","fatal":true}}`)
	scm.set(sysint.StateStopped)
	s = waitCloud(t, c, rec, "refused from the status file", func(s bridge.State) bool { return s.State == "refused" })
	if s.Refused == nil || s.Refused.Code != "state_incompatible" || s.Refused.Message == "" {
		t.Errorf("refused state %+v, want the state_incompatible problem of the status file", s)
	}

	if err := c.Start(true); err != nil {
		t.Fatalf("Start(true): %v", err)
	}
	scm.mu.Lock()
	starts = slices.Clone(scm.starts)
	scm.mu.Unlock()
	if len(starts) != 2 || !slices.Contains(starts[1], "discard-state") {
		t.Errorf("SCM starts after Start(true): %v, want a second one with discard-state", starts)
	}

	writeStatusFile(t, statusFile, `{"state":"stopped","error":"open the store: disk full"}`)
	waitCloud(t, c, rec, "stopped after a failed start", func(s bridge.State) bool {
		return s.State == "stopped" && s.Error == "open the store: disk full" && s.Refused == nil
	})
}

func testBanner(t *testing.T) {
	scm := &fakeSCM{state: sysint.StateRunning}
	f := newFakeMgmt(t, statusMux())
	f.down.Store(true)
	var early atomic.Value // the first state with ConnectionLost before three failed polls
	var stopping atomic.Bool
	var lostAfterStop atomic.Bool
	c, rec := runCloud(t, scm, f, filepath.Join(t.TempDir(), "status.json"), func(s bridge.State) {
		if !s.ConnectionLost {
			return
		}
		if stopping.Load() {
			lostAfterStop.Store(true)
		}
		if f.failedDials.Load() < 3 {
			early.CompareAndSwap(nil, s)
		}
	})

	waitCloud(t, c, rec, "connection lost while the SCM reports running", func(s bridge.State) bool { return s.ConnectionLost })
	if s := early.Load(); s != nil {
		t.Errorf("connection lost reported as %+v before three failed polls", s)
	}

	f.down.Store(false)
	waitCloud(t, c, rec, "connection back with the next answer", func(s bridge.State) bool {
		return !s.ConnectionLost && s.State == "running" && len(s.Problems) == 1
	})

	stopping.Store(true)
	f.down.Store(true)
	scm.set(sysint.StateStopping)
	waitCloud(t, c, rec, "stopping", func(s bridge.State) bool { return s.State == "stopping" })
	scm.set(sysint.StateStopped)
	waitCloud(t, c, rec, "stopped", func(s bridge.State) bool { return s.State == "stopped" })
	polls := scm.queries.Load()
	waitFor(t, "five more polls while stopped", func() bool { return scm.queries.Load() >= polls+5 })
	if lostAfterStop.Load() {
		t.Error("connection lost reported during a regular stop")
	}
	if s := c.Status(); s.State != "stopped" || s.ConnectionLost {
		t.Errorf("state after a regular stop %+v, want only stopped", s)
	}
}
