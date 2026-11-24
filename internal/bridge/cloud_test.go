package bridge_test

import (
	"context"
	"errors"
	"fmt"
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
	// startErr and stopErr, when set, refuse Start and Stop.
	startErr, stopErr error
	// afterStart and afterStop, when set, become the state once Start or
	// Stop is accepted, as the SCM reports a pending state at once.
	afterStart, afterStop string
	// hold, when set, makes the next State call read the state, send on
	// entered and then wait until hold is closed before it answers.
	hold, entered chan struct{}
}

var _ sysint.ServiceControl = (*fakeSCM)(nil)

func (s *fakeSCM) Start(_ context.Context, params ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startErr != nil {
		return s.startErr
	}
	s.starts = append(s.starts, slices.Clone(params))
	if s.afterStart != "" {
		s.state = s.afterStart
	}
	return nil
}

func (s *fakeSCM) Stop(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopErr != nil {
		return s.stopErr
	}
	s.stops++
	if s.afterStop != "" {
		s.state = s.afterStop
	}
	return nil
}

func (s *fakeSCM) State(context.Context) (string, error) {
	s.queries.Add(1)
	s.mu.Lock()
	state, hold, entered := s.state, s.hold, s.entered
	s.hold, s.entered = nil, nil
	s.mu.Unlock()
	if hold != nil {
		entered <- struct{}{}
		<-hold
	}
	return state, nil
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
	refused := &cloudstatus.Problem{Code: "state_incompatible", Message: "the state is of a newer HLab version", Fatal: true}
	cases := []struct {
		name   string
		state  bridge.State
		icon   string
		word   string // in tooltip and status line, any case
		item   string
		action string
	}{
		{"stopped", bridge.State{State: "stopped"}, "stopped", "stopped", "Start cloud", "start"},
		{"starting", bridge.State{State: "starting"}, "busy", "starting", "Starting cloud…", ""},
		{"running", bridge.State{State: "running"}, "running", "running", "Stop cloud", "stop"},
		{"stopping", bridge.State{State: "stopping"}, "busy", "stopping", "Stopping cloud…", ""},
		{"refused", bridge.State{State: "refused", Refused: refused}, "error", "refused", "Start cloud", "start"},
		{"stopped after a failed start", bridge.State{State: "stopped", Error: "open the store: disk full"}, "error", "fail", "Start cloud", "start"},
		{"connection lost", bridge.State{State: "running", ConnectionLost: true}, "error", "connection", "Stop cloud", "stop"},
		{"unknown", bridge.State{}, "busy", "", "Checking cloud…", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := bridge.Tray(tc.state)
			if m.Icon != tc.icon {
				t.Errorf("icon %q, want %q", m.Icon, tc.icon)
			}
			if !strings.HasPrefix(m.Tooltip, "HLab: ") || len(m.Tooltip) == len("HLab: ") {
				t.Errorf("tooltip %q, want \"HLab: <status>\"", m.Tooltip)
			}
			if !strings.Contains(strings.ToLower(m.Tooltip), tc.word) {
				t.Errorf("tooltip %q does not say %q", m.Tooltip, tc.word)
			}
			if m.Status == "" || !strings.Contains(strings.ToLower(m.Status), tc.word) {
				t.Errorf("status line %q does not say %q", m.Status, tc.word)
			}
			if m.CloudItem != tc.item || m.CloudAction != tc.action {
				t.Errorf("cloud item %q with action %q, want %q with action %q", m.CloudItem, m.CloudAction, tc.item, tc.action)
			}
		})
	}
}

// since returns the states OnChange reported after the first n.
func (c *changes) since(n int) []bridge.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.all[n:])
}

// count is the number of states OnChange reported.
func (c *changes) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.all)
}

// holdPoll makes the next SCM query wait and returns once a poll has read
// the state and waits; release lets it answer.
func holdPoll(t *testing.T, scm *fakeSCM) (release func()) {
	t.Helper()
	hold, entered := make(chan struct{}), make(chan struct{}, 1)
	scm.mu.Lock()
	scm.hold, scm.entered = hold, entered
	scm.mu.Unlock()
	var once sync.Once
	release = func() { once.Do(func() { close(hold) }) }
	t.Cleanup(release)
	select {
	case <-entered:
	case <-time.After(waitTimeout):
		t.Fatalf("no poll queried the SCM within %s", waitTimeout)
	}
	return release
}

// callWithin runs call and fails the test when it does not return within
// waitTimeout (for example because it waits for the held poll).
func callWithin(t *testing.T, what string, call func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-time.After(waitTimeout):
		t.Fatalf("%s does not return within %s while a poll is under way", what, waitTimeout)
		return nil
	}
}

// testPending checks that Start and Stop show starting and stopping at once,
// that a poll begun before the call cannot bring back the old state and
// that a refused call changes nothing.
func testPending(t *testing.T) {
	scm := &fakeSCM{state: sysint.StateStopped}
	f := newFakeMgmt(t, statusMux())
	f.down.Store(true)
	c, rec := runCloud(t, scm, f, filepath.Join(t.TempDir(), "status.json"), nil)
	waitCloud(t, c, rec, "stopped", func(s bridge.State) bool { return s.State == "stopped" })

	// A refused Start leaves the state as it was.
	scm.mu.Lock()
	scm.startErr = errors.New("access is denied")
	scm.mu.Unlock()
	before, n := c.Status(), rec.count()
	if err := c.Start(false); err == nil {
		t.Error("Start with the SCM refusing it: no error")
	}
	if s := c.Status(); !reflect.DeepEqual(s, before) {
		t.Errorf("state after a refused Start %+v, want unchanged %+v", s, before)
	}
	for _, s := range rec.since(n) {
		if s.State != "stopped" {
			t.Errorf("OnChange reported %+v after a refused Start, want the state to stay stopped", s)
		}
	}

	// A successful Start shows starting at once; the poll that read
	// "stopped" before the call is dropped.
	scm.mu.Lock()
	scm.startErr, scm.afterStart = nil, sysint.StateStarting
	scm.mu.Unlock()
	release := holdPoll(t, scm)
	n = rec.count()
	if err := callWithin(t, "Start", func() error { return c.Start(false) }); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s := c.Status(); s.State != "starting" {
		t.Errorf("state right after Start %q, want starting", s.State)
	}
	if last, ok := rec.last(); !ok || last.State != "starting" || rec.count() == n {
		t.Errorf("OnChange right after Start: last %+v (fired: %v), want starting", last, rec.count() > n)
	}
	queries := scm.queries.Load()
	release()
	waitFor(t, "three polls after the held one", func() bool { return scm.queries.Load() >= queries+3 })
	for _, s := range rec.since(n) {
		if s.State == "stopped" {
			t.Errorf("OnChange reported %+v after Start: the poll begun before the call brought back the old state", s)
		}
	}
	if s := c.Status(); s.State != "starting" {
		t.Errorf("state after the held poll answered %q, want starting", s.State)
	}

	f.down.Store(false)
	scm.set(sysint.StateRunning)
	waitCloud(t, c, rec, "running", func(s bridge.State) bool { return s.State == "running" && !s.ConnectionLost })

	// A refused Stop leaves the state as it was.
	scm.mu.Lock()
	scm.stopErr = errors.New("access is denied")
	scm.mu.Unlock()
	n = rec.count()
	if err := c.Stop(); err == nil {
		t.Error("Stop with the SCM refusing it: no error")
	}
	if s := c.Status(); s.State != "running" {
		t.Errorf("state after a refused Stop %q, want running", s.State)
	}
	for _, s := range rec.since(n) {
		if s.State != "running" {
			t.Errorf("OnChange reported %+v after a refused Stop, want the state to stay running", s)
		}
	}

	// A successful Stop shows stopping at once; the poll that read
	// "running" before the call is dropped.
	scm.mu.Lock()
	scm.stopErr, scm.afterStop = nil, sysint.StateStopping
	scm.mu.Unlock()
	release = holdPoll(t, scm)
	n = rec.count()
	if err := callWithin(t, "Stop", c.Stop); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if s := c.Status(); s.State != "stopping" {
		t.Errorf("state right after Stop %q, want stopping", s.State)
	}
	if last, ok := rec.last(); !ok || last.State != "stopping" || rec.count() == n {
		t.Errorf("OnChange right after Stop: last %+v (fired: %v), want stopping", last, rec.count() > n)
	}
	queries = scm.queries.Load()
	release()
	waitFor(t, "three polls after the held one", func() bool { return scm.queries.Load() >= queries+3 })
	for _, s := range rec.since(n) {
		if s.State == "running" {
			t.Errorf("OnChange reported %+v after Stop: the poll begun before the call brought back the old state", s)
		}
	}
	if s := c.Status(); s.State != "stopping" {
		t.Errorf("state after the held poll answered %q, want stopping", s.State)
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

// faultsStatusMux answers GET /mgmt/v1/status as a running hlabd with the
// fault count n.
func faultsStatusMux(n *atomic.Int32) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /mgmt/v1/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"state":"running","version":"1.2.3","problems":[],"shadowing_zones":[],"faults":%d}`, n.Load())
	})
	return mux
}

// testFaultsModel checks that the polled state and the tray model carry the
// fault count of GET /status.
func testFaultsModel(t *testing.T) {
	scm := &fakeSCM{state: sysint.StateRunning}
	var n atomic.Int32
	f := newFakeMgmt(t, faultsStatusMux(&n))
	c, rec := runCloud(t, scm, f, filepath.Join(t.TempDir(), "status.json"), nil)

	// The cases build on each other in order.
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
		want  int
	}{
		{"running with 2", func(t *testing.T) {
			n.Store(2)
			waitCloud(t, c, rec, "running with 2 faults", func(s bridge.State) bool { return s.State == "running" && s.Faults == 2 })
		}, 2},
		{"running with 0", func(t *testing.T) {
			n.Store(0)
			waitCloud(t, c, rec, "running with 0 faults", func(s bridge.State) bool { return s.State == "running" && s.Faults == 0 })
		}, 0},
		{"connection lost after a count of 2", func(t *testing.T) {
			n.Store(2)
			waitCloud(t, c, rec, "running with 2 faults", func(s bridge.State) bool { return s.State == "running" && s.Faults == 2 })
			f.down.Store(true)
			waitCloud(t, c, rec, "connection lost", func(s bridge.State) bool { return s.ConnectionLost })
		}, 2},
		{"stopped", func(t *testing.T) {
			scm.set(sysint.StateStopped)
			waitCloud(t, c, rec, "stopped", func(s bridge.State) bool { return s.State == "stopped" })
		}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			s := c.Status()
			if s.Faults != tc.want {
				t.Errorf("State.Faults %d in %+v, want %d", s.Faults, s, tc.want)
			}
			if got := bridge.Tray(s).Faults; got != tc.want {
				t.Errorf("Tray(...).Faults %d, want %d", got, tc.want)
			}
		})
	}
}

// testEndAllFaults checks that EndAllFaults sends DELETE /mgmt/v1/faults and
// passes on a refusal with its message.
func testEndAllFaults(t *testing.T) {
	var refuse atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /mgmt/v1/faults", func(w http.ResponseWriter, _ *http.Request) {
		if refuse.Load() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"cloud_not_running","message":"the cloud is not running"}}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	f := newFakeMgmt(t, mux)
	c := bridge.NewCloud(bridge.CloudDeps{
		Service:    &fakeSCM{state: sysint.StateRunning},
		Client:     f.client(),
		StatusFile: filepath.Join(t.TempDir(), "status.json"),
		Interval:   pollInterval,
		OnChange:   func(bridge.State) {},
	})

	if err := c.EndAllFaults(); err != nil {
		t.Fatalf("EndAllFaults on 204: %v", err)
	}
	if got := f.count("DELETE /faults"); got != 1 {
		t.Errorf("DELETE /mgmt/v1/faults sent %d times, want 1", got)
	}

	refuse.Store(true)
	err := c.EndAllFaults()
	if err == nil {
		t.Fatal("EndAllFaults on cloud_not_running: no error")
	}
	if !strings.Contains(err.Error(), "the cloud is not running") {
		t.Errorf("EndAllFaults error %q, want the message \"the cloud is not running\"", err)
	}
}
