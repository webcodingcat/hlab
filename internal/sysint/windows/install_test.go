//go:build windows

package windows

import (
	"context"
	"sync"
	"testing"
	"time"

	xwin "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

// fakeService is a service manager entry for stopAndWait. It stays in
// START_PENDING for startQueries queries (forever when negative), then
// runs; a stop is refused while it starts, as Windows does, and leads
// through STOP_PENDING to Stopped. Ending its process stops it at once.
type fakeService struct {
	mu           sync.Mutex
	pid          uint32
	state        svc.State
	startQueries int
	stopQueries  int
	stopSent     bool
	refused      int
}

func (f *fakeService) Query() (svc.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.state {
	case svc.StartPending:
		if f.startQueries > 0 {
			f.startQueries--
			if f.startQueries == 0 {
				f.state = svc.Running
			}
		}
	case svc.StopPending:
		f.stopQueries--
		if f.stopQueries <= 0 {
			f.state = svc.Stopped
		}
	}
	return f.status(), nil
}

func (f *fakeService) Control(c svc.Cmd) (svc.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c != svc.Stop {
		return f.status(), xwin.ERROR_INVALID_SERVICE_CONTROL
	}
	switch f.state {
	case svc.StartPending, svc.StopPending:
		f.refused++
		return f.status(), xwin.ERROR_INVALID_SERVICE_CONTROL
	case svc.Stopped:
		return f.status(), xwin.ERROR_SERVICE_NOT_ACTIVE
	}
	f.stopSent = true
	f.state = svc.StopPending
	f.stopQueries = 2
	return f.status(), nil
}

// endProcess ends the service's process if pid is its process.
func (f *fakeService) endProcess(pid uint32) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pid != f.pid || f.state == svc.Stopped {
		return false
	}
	f.state = svc.Stopped
	return true
}

func (f *fakeService) status() svc.Status {
	st := svc.Status{State: f.state}
	if f.state != svc.Stopped {
		st.ProcessId = f.pid
	}
	return st
}

func (f *fakeService) current() svc.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

// shortStopWaits sets the waits of stopAndWait to test lengths and
// endProcess to end the process of fake, recording the PIDs it was called
// with; everything is restored at the test's end.
func shortStopWaits(t *testing.T, fake *fakeService) *[]uint32 {
	t.Helper()
	oldStart, oldStop, oldPoll, oldEnd := startPendingWait, uninstallStopWait, stopPoll, endProcess
	t.Cleanup(func() {
		startPendingWait, uninstallStopWait, stopPoll, endProcess = oldStart, oldStop, oldPoll, oldEnd
	})
	startPendingWait = 300 * time.Millisecond
	uninstallStopWait = 2 * time.Second
	stopPoll = 5 * time.Millisecond
	var mu sync.Mutex
	var ended []uint32
	endProcess = func(pid uint32) error {
		mu.Lock()
		ended = append(ended, pid)
		mu.Unlock()
		if !fake.endProcess(pid) {
			return xwin.ERROR_INVALID_PARAMETER
		}
		return nil
	}
	return &ended
}

func TestStopService(t *testing.T) {
	t.Run("INST-05", func(t *testing.T) {
		const pid = 4242
		for _, c := range []struct {
			name         string
			startQueries int
			wantEnded    bool
		}{
			{name: "service reaches running during the wait", startQueries: 3},
			{name: "service stays start pending", startQueries: -1, wantEnded: true},
		} {
			fake := &fakeService{pid: pid, state: svc.StartPending, startQueries: c.startQueries}
			ended := shortStopWaits(t, fake)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			begin := time.Now()
			err := stopAndWait(ctx, fake, "hlab", fake.status())
			cancel()
			if err != nil {
				t.Errorf("%s: stopAndWait = %v, want the service stopped so it can be removed", c.name, err)
			}
			if got := fake.current(); got != svc.Stopped {
				t.Errorf("%s: service state after stopAndWait = %d, want stopped (%d)", c.name, got, svc.Stopped)
			}
			if c.wantEnded {
				if len(*ended) != 1 || (*ended)[0] != pid {
					t.Errorf("%s: processes ended = %v, want the service's process %d once", c.name, *ended, pid)
				}
				if waited := time.Since(begin); waited < startPendingWait {
					t.Errorf("%s: process ended after %s, before the start wait of %s", c.name, waited, startPendingWait)
				}
			} else {
				if len(*ended) != 0 {
					t.Errorf("%s: processes ended = %v, want none: the service accepted the stop", c.name, *ended)
				}
				if !fake.stopSent {
					t.Errorf("%s: no stop sent after the service was running", c.name)
				}
			}
		}
	})
}
