package lab_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
)

// testClock is a clock that moves only when the test advances it.
type testClock struct {
	mu      sync.Mutex
	changed *sync.Cond
	now     time.Time
	timers  []testTimer
}

// testTimer is a pending After of the test clock.
type testTimer struct {
	at   time.Time
	fire chan time.Time
}

func newTestClock() *testClock {
	c := &testClock{now: time.Date(2026, 11, 7, 18, 0, 0, 0, time.UTC)}
	c.changed = sync.NewCond(&c.mu)
	return c
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	fire := make(chan time.Time, 1)
	c.timers = append(c.timers, testTimer{at: c.now.Add(d), fire: fire})
	c.changed.Broadcast()
	return fire
}

// Advance moves the clock forward and fires the timers that are due.
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	pending := c.timers[:0]
	for _, timer := range c.timers {
		if timer.at.After(c.now) {
			pending = append(pending, timer)
		} else {
			timer.fire <- c.now
		}
	}
	c.timers = pending
}

// WaitForSleepers waits until n timers are pending.
func (c *testClock) WaitForSleepers(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.timers) < n {
		c.changed.Wait()
	}
}

func TestActionTakesHetznersTimeTimesTheTimeScale(t *testing.T) {
	clock := newTestClock()
	l, _ := openWithProject(t, lab.Options{TimeScale: 2, Clock: clock})
	table := tableWith(lab.ActionSpec{Name: "stop_server", Plan: func(*state.Tx, lab.Request) (lab.Plan, error) {
		return lab.Plan{Steps: func(ctx context.Context) error { return l.Sleep(ctx, lab.PowerOff) }}, nil
	}})

	action, err := l.Act(table, "poweroff", request(1))
	if err != nil {
		t.Fatal(err)
	}
	clock.WaitForSleepers(1)
	clock.Advance(17 * time.Second) // 2 × 8.7 s = 17.4 s
	if got := actionStatus(t, l, action.ID); got != state.ActionRunning {
		t.Fatalf("after 17 s the action is %s, want running", got)
	}
	clock.Advance(time.Second)
	waitForStatus(t, l, action.ID, state.ActionSuccess)
}

func TestTimeScaleZeroDoesNotWait(t *testing.T) {
	l, _ := open(t, lab.Options{TimeScale: 0})
	start := time.Now()
	if err := l.Sleep(t.Context(), lab.ChangeType); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("Sleep waited %s", waited)
	}
}

func TestSleepEndsWithTheContext(t *testing.T) {
	l, _ := open(t, lab.Options{TimeScale: 1, Clock: newTestClock()})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := l.Sleep(ctx, lab.PowerOn); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}
