package actions_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/store"
)

// timeout bounds every wait of the tests.
const timeout = 10 * time.Second

// manyActions is more queued actions than the manager has workers.
const manyActions = 12

type env struct {
	st      *store.Store
	m       *actions.Manager
	project int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	e := &env{st: st, m: actions.New(st)}
	if err := st.Tx(func(tx store.Tx) error {
		p, err := store.CreateProject(tx, "p", "p")
		e.project = p.ID
		return err
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := e.m.WaitIdle(ctx, e.project); err != nil {
			t.Errorf("actions still running at the end: %v", err)
		}
		_ = st.Close()
	})
	return e
}

func ref(typ string, id int64) actions.Ref { return actions.Ref{ID: id, Type: typ} }

// queued starts an action with StartQueued and fails the test on an error.
func (e *env) queued(t *testing.T, command string, resources, refuse []actions.Ref) *actions.Action {
	t.Helper()
	a, err := e.startQueued(command, resources, refuse)
	if err != nil {
		t.Fatalf("StartQueued %s %v refuse %v: %v", command, resources, refuse, err)
	}
	if a.Status != actions.StatusRunning {
		t.Errorf("StartQueued %s: status %q, want running", command, a.Status)
	}
	return a
}

func (e *env) startQueued(command string, resources, refuse []actions.Ref) (*actions.Action, error) {
	var a *actions.Action
	err := e.st.Tx(func(tx store.Tx) error {
		var err error
		a, err = e.m.StartQueued(tx, e.project, command, resources, refuse)
		return err
	})
	return a, err
}

func (e *env) start(command string, resources []actions.Ref) (*actions.Action, error) {
	var a *actions.Action
	err := e.st.Tx(func(tx store.Tx) error {
		var err error
		a, err = e.m.Start(tx, e.project, command, resources)
		return err
	})
	return a, err
}

func (e *env) status(t *testing.T, id int64) string {
	t.Helper()
	a, err := e.m.Get(e.project, id)
	if err != nil {
		t.Fatalf("get action %d: %v", id, err)
	}
	return a.Status
}

// waitIdle waits until no action of the project runs.
func (e *env) waitIdle(t *testing.T, what string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := e.m.WaitIdle(ctx, e.project); err != nil {
		t.Fatalf("%s: actions still running after %s", what, timeout)
	}
}

// gate is work that waits until open is called; started is closed when
// the work begins.
type gate struct {
	started chan struct{}
	release chan struct{}
	open    func()
}

func newGate(t *testing.T) *gate {
	g := &gate{started: make(chan struct{}), release: make(chan struct{})}
	g.open = sync.OnceFunc(func() { close(g.release) })
	t.Cleanup(g.open)
	return g
}

func (g *gate) work(ctx context.Context, _ func(int)) error {
	close(g.started)
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitStarted waits until the gate's work runs.
func (g *gate) waitStarted(t *testing.T, what string) {
	t.Helper()
	select {
	case <-g.started:
	case <-time.After(timeout):
		t.Fatalf("%s: work did not start within %s", what, timeout)
	}
}

func TestActions(t *testing.T) {
	t.Run("CLI-02/queue", func(t *testing.T) {
		e := newEnv(t)
		network := ref("network", 1)

		// An action sharing only a queuing ref is created running and
		// works after the earlier action finished.
		first := e.queued(t, "attach_to_network", []actions.Ref{ref("server", 1), network}, []actions.Ref{ref("server", 1)})
		second := e.queued(t, "attach_to_network", []actions.Ref{ref("server", 2), network}, []actions.Ref{ref("server", 2)})
		if got := e.status(t, second.ID); got != actions.StatusRunning {
			t.Errorf("queued action: status %q, want running", got)
		}
		g := newGate(t)
		e.m.Run(first.ID, g.work)
		g.waitStarted(t, "first action")
		var firstAtSecond string
		secondDone := make(chan struct{})
		e.m.Run(second.ID, func(context.Context, func(int)) error {
			defer close(secondDone)
			a, err := e.m.Get(e.project, first.ID)
			if err == nil {
				firstAtSecond = a.Status
			}
			return nil
		})
		g.open()
		select {
		case <-secondDone:
		case <-time.After(timeout):
			t.Fatalf("queued action did not run within %s after the first finished", timeout)
		}
		if firstAtSecond != actions.StatusSuccess {
			t.Errorf("first action when the queued work started: %q, want success", firstAtSecond)
		}
		e.waitIdle(t, "two actions on one network")

		// Three actions on one ref run in order of creation, whatever the
		// order of Run.
		lb := ref("load_balancer", 1)
		var mu sync.Mutex
		var order []int64
		var ids []int64
		for range 3 {
			ids = append(ids, e.queued(t, "add_target", []actions.Ref{lb}, nil).ID)
		}
		for _, id := range slices.Backward(ids) {
			e.m.Run(id, func(context.Context, func(int)) error {
				mu.Lock()
				order = append(order, id)
				mu.Unlock()
				return nil
			})
		}
		e.waitIdle(t, "three actions on one load balancer")
		mu.Lock()
		if !slices.Equal(order, ids) {
			t.Errorf("work order %v, want the order of creation %v", order, ids)
		}
		mu.Unlock()

		// Actions without a shared ref run at the same time: each waits
		// for the other to have started.
		a := e.queued(t, "add_route", []actions.Ref{ref("network", 2)}, nil)
		b := e.queued(t, "add_route", []actions.Ref{ref("network", 3)}, nil)
		ga, gb := newGate(t), newGate(t)
		both := func(own, other *gate) func(context.Context, func(int)) error {
			return func(ctx context.Context, progress func(int)) error {
				close(own.started)
				select {
				case <-other.started:
					return nil
				case <-time.After(timeout):
					return errors.New("the other action did not run at the same time")
				}
			}
		}
		e.m.Run(a.ID, both(ga, gb))
		e.m.Run(b.ID, both(gb, ga))
		e.waitIdle(t, "two actions on different networks")
		for _, id := range []int64{a.ID, b.ID} {
			if got := e.status(t, id); got != actions.StatusSuccess {
				t.Errorf("action %d on its own network: %q, want success (both run at the same time)", id, got)
			}
		}

		// More queued actions than workers all finish, and an unrelated
		// action still runs while they wait.
		shared := ref("server", 10)
		holder := e.queued(t, "attach", []actions.Ref{ref("volume", 10), shared}, []actions.Ref{ref("volume", 10)})
		hg := newGate(t)
		e.m.Run(holder.ID, hg.work)
		hg.waitStarted(t, "holder")
		var waiting []int64
		for i := range manyActions {
			w := e.queued(t, "assign", []actions.Ref{ref("floating_ip", int64(20+i)), shared}, []actions.Ref{ref("floating_ip", int64(20+i))})
			waiting = append(waiting, w.ID)
			e.m.Run(w.ID, func(context.Context, func(int)) error { return nil })
		}
		free := e.queued(t, "poweron", []actions.Ref{ref("server", 99)}, nil)
		fg := newGate(t)
		fg.open()
		e.m.Run(free.ID, fg.work)
		fg.waitStarted(t, fmt.Sprintf("unrelated action while %d actions wait", manyActions))
		hg.open()
		e.waitIdle(t, fmt.Sprintf("%d queued actions", manyActions))
		for _, id := range waiting {
			if got := e.status(t, id); got != actions.StatusSuccess {
				t.Errorf("queued action %d: %q, want success", id, got)
			}
		}
	})

	t.Run("CLI-02/refuse", func(t *testing.T) {
		e := newEnv(t)
		server1, server2, network := ref("server", 1), ref("server", 2), ref("network", 1)

		running, err := e.start("attach_to_network", []actions.Ref{server1, network})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		g := newGate(t)
		e.m.Run(running.ID, g.work)
		g.waitStarted(t, "running action")

		if _, err := e.startQueued("attach_to_network", []actions.Ref{server1, ref("network", 2)}, []actions.Ref{server1}); !errors.Is(err, actions.ErrLocked) {
			t.Errorf("StartQueued refusing a ref of a running action: %v, want ErrLocked", err)
		}
		queued := e.queued(t, "attach_to_network", []actions.Ref{server2, network}, []actions.Ref{server2})
		e.m.Run(queued.ID, func(context.Context, func(int)) error { return nil })
		if _, err := e.startQueued("change_alias_ips", []actions.Ref{server2, ref("network", 3)}, []actions.Ref{server2}); !errors.Is(err, actions.ErrLocked) {
			t.Errorf("StartQueued refusing a ref of a queued action: %v, want ErrLocked", err)
		}
		if _, err := e.start("change_ip_range", []actions.Ref{network}); !errors.Is(err, actions.ErrLocked) {
			t.Errorf("Start on a ref of the running action: %v, want ErrLocked", err)
		}
		if _, err := e.start("poweroff", []actions.Ref{server2}); !errors.Is(err, actions.ErrLocked) {
			t.Errorf("Start on a ref held only by a queued action: %v, want ErrLocked", err)
		}

		g.open()
		e.waitIdle(t, "running and queued action")
	})
}
