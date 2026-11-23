// Package actions keeps Hetzner actions and runs their work in a worker
// pool. A handler creates the action with Start or StartQueued inside its
// transaction and hands the work to Run after the commit. Run starts the
// work only after every earlier running action that shares a resource with
// it has finished, so actions on one resource work in order of creation.
package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/store"
)

// Status values of an action.
const (
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusError   = "error"
)

// workers is the number of actions whose work runs at the same time.
const workers = 4

// Ref names a resource an action touches.
type Ref struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// Error is the error object of a failed action. Work that returns an
// *Error reports it as is; any other error becomes code "server_error".
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// ErrLocked is returned by Start, StartAll and StartQueued when a resource
// they refuse on has a running action. Handlers answer it with Hetzner's 423 "locked".
var ErrLocked = &Error{Code: "locked", Message: "the resource is locked by a running action"}

// ErrProtected is returned for an operation refused by the resource's
// protection (change_protection). Handlers answer it with 423 "protected".
var ErrProtected = &Error{Code: "protected", Message: "the resource is protected"}

// Action is an action in Hetzner's API shape.
type Action struct {
	ID        int64      `json:"id"`
	Command   string     `json:"command"`
	Status    string     `json:"status"`
	Progress  int        `json:"progress"`
	Started   time.Time  `json:"started"`
	Finished  *time.Time `json:"finished"`
	Resources []Ref      `json:"resources"`
	Error     *Error     `json:"error"`
}

// Manager creates actions and runs their work.
type Manager struct {
	st  *store.Store
	sem chan struct{}

	mu sync.Mutex
	// finished is closed and replaced whenever an action finishes.
	finished chan struct{}
}

// New returns a manager on st. Its workers need no separate start. Actions
// still running from a previous run of hlabd end as "error" with code
// "server_error".
func New(st *store.Store) *Manager {
	m := &Manager{st: st, sem: make(chan struct{}, workers), finished: make(chan struct{})}
	if err := m.failInterrupted(); err != nil {
		slog.Error("end interrupted actions", "err", err)
	}
	return m
}

func (m *Manager) failInterrupted() error {
	return m.st.Tx(func(tx store.Tx) error {
		running, err := store.RunningActions(tx)
		if err != nil {
			return err
		}
		for _, a := range running {
			a.Status = StatusError
			a.Finished = time.Now().UTC().Truncate(time.Second)
			a.Error = errorJSON(&Error{Code: "server_error", Message: "the action was interrupted by a restart"})
			if err := store.UpdateAction(tx, a); err != nil {
				return err
			}
		}
		return nil
	})
}

// Start creates a running action of projectID for command on resources in
// tx. It returns ErrLocked if one of resources has a running action.
func (m *Manager) Start(tx store.Tx, projectID int64, command string, resources []Ref) (*Action, error) {
	if err := checkLocks(tx, resources); err != nil {
		return nil, err
	}
	return create(tx, projectID, Spec{Command: command, Resources: resources})
}

// StartQueued creates a running action of projectID for command on
// resources in tx. It returns ErrLocked if a running action (one that
// still waits in the queue included) references one of refuse; refuse is
// a subset of resources and names the resources whose own state the
// action changes. A resource of resources that is not in refuse never
// refuses: the action queues behind the actions holding it.
func (m *Manager) StartQueued(tx store.Tx, projectID int64, command string, resources, refuse []Ref) (*Action, error) {
	if err := checkLocks(tx, refuse); err != nil {
		return nil, err
	}
	return create(tx, projectID, Spec{Command: command, Resources: resources})
}

// Spec describes one action of a batch started by StartAll.
type Spec struct {
	Command   string
	Resources []Ref
}

// StartAll creates one running action of projectID per spec in tx, in the
// order of specs. It returns ErrLocked if a running action outside the
// batch references one of lock (nil: no check); the actions of one batch
// never lock each other.
func (m *Manager) StartAll(tx store.Tx, projectID int64, lock []Ref, specs []Spec) ([]*Action, error) {
	if err := checkLocks(tx, lock); err != nil {
		return nil, err
	}
	out := make([]*Action, 0, len(specs))
	for _, s := range specs {
		a, err := create(tx, projectID, s)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// Succeed marks the running actions acts, created in tx, as finished with
// success, for work that has nothing to do; they are not handed to Run.
func (m *Manager) Succeed(tx store.Tx, acts []*Action) error {
	now := time.Now().UTC().Truncate(time.Second)
	for _, a := range acts {
		sa, err := store.GetAction(tx, a.ID)
		if err != nil {
			return err
		}
		sa.Status, sa.Progress, sa.Finished = StatusSuccess, 100, now
		if err := store.UpdateAction(tx, sa); err != nil {
			return err
		}
		a.Status, a.Progress, a.Finished = StatusSuccess, 100, &now
	}
	return nil
}

// create stores a running action for s without checking locks.
func create(tx store.Tx, projectID int64, s Spec) (*Action, error) {
	resources := s.Resources
	if resources == nil {
		resources = []Ref{}
	}
	refs, err := json.Marshal(resources)
	if err != nil {
		return nil, err
	}
	a := &Action{
		Command:   s.Command,
		Status:    StatusRunning,
		Started:   time.Now().UTC().Truncate(time.Second),
		Resources: resources,
	}
	a.ID, err = store.CreateAction(tx, store.Action{
		ProjectID: projectID,
		Command:   s.Command,
		Status:    a.Status,
		Started:   a.Started,
		Resources: string(refs),
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// checkLocks returns ErrLocked if a running action references one of refs.
func checkLocks(tx store.Tx, refs []Ref) error {
	if len(refs) == 0 {
		return nil
	}
	running, err := store.RunningActions(tx)
	if err != nil {
		return err
	}
	for _, r := range running {
		a, err := fromStore(r)
		if err != nil {
			return err
		}
		for _, held := range a.Resources {
			if slices.Contains(refs, held) {
				return ErrLocked
			}
		}
	}
	return nil
}

// Store returns the store the manager works on, for the jobs of a module
// that got the manager through resource.ActionUser.
func (m *Manager) Store() *store.Store { return m.st }

// Get returns the action id of projectID, or store.ErrNotFound if it does
// not exist or belongs to another project.
func (m *Manager) Get(projectID, id int64) (*Action, error) {
	var a *Action
	err := m.st.Tx(func(tx store.Tx) error {
		r, err := store.GetAction(tx, id)
		if err != nil {
			return err
		}
		if r.ProjectID != projectID {
			return store.ErrNotFound
		}
		a, err = fromStore(r)
		return err
	})
	return a, err
}

// List returns the actions of projectID with one of ids, ordered by ID.
// IDs that do not exist or belong to another project are skipped.
func (m *Manager) List(projectID int64, ids []int64) ([]Action, error) {
	var out []Action
	err := m.st.Tx(func(tx store.Tx) error {
		rows, err := store.ListActions(tx, projectID, ids)
		if err != nil {
			return err
		}
		for _, r := range rows {
			a, err := fromStore(r)
			if err != nil {
				return err
			}
			out = append(out, *a)
		}
		return nil
	})
	return out, err
}

// ListFor returns the actions of projectID that reference the resource
// resourceID of refType, ordered by ID; resourceID 0 means every resource
// of refType.
func (m *Manager) ListFor(projectID int64, refType string, resourceID int64) ([]Action, error) {
	out := []Action{}
	err := m.st.Tx(func(tx store.Tx) error {
		rows, err := store.ProjectActions(tx, projectID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			a, err := fromStore(r)
			if err != nil {
				return err
			}
			if slices.ContainsFunc(a.Resources, func(ref Ref) bool {
				return ref.Type == refType && (resourceID == 0 || ref.ID == resourceID)
			}) {
				out = append(out, *a)
			}
		}
		return nil
	})
	return out, err
}

// fromStore converts a stored action into the API shape.
func fromStore(r store.Action) (*Action, error) {
	a := &Action{
		ID:        r.ID,
		Command:   r.Command,
		Status:    r.Status,
		Progress:  r.Progress,
		Started:   r.Started,
		Resources: []Ref{},
	}
	if !r.Finished.IsZero() {
		f := r.Finished
		a.Finished = &f
	}
	if err := json.Unmarshal([]byte(r.Resources), &a.Resources); err != nil {
		return nil, fmt.Errorf("action %d resources: %w", r.ID, err)
	}
	if r.Error != "" {
		a.Error = &Error{}
		if err := json.Unmarshal([]byte(r.Error), a.Error); err != nil {
			return nil, fmt.Errorf("action %d error: %w", r.ID, err)
		}
	}
	return a, nil
}

// Run runs fn for action id in the worker pool and records its progress
// and result. It returns at once. The work waits, without holding a
// worker, until every running action with a lower ID that shares a
// resource with action id has finished.
func (m *Manager) Run(id int64, fn func(ctx context.Context, progress func(int)) error) {
	go func() {
		m.waitTurn(id)
		m.sem <- struct{}{}
		defer func() { <-m.sem }()
		ctx := context.Background()
		progress := func(p int) {
			m.update(id, func(a *store.Action) { a.Progress = min(max(p, 0), 100) })
		}
		err := fn(ctx, progress)
		m.update(id, func(a *store.Action) {
			a.Finished = time.Now().UTC().Truncate(time.Second)
			if err == nil {
				a.Status, a.Progress = StatusSuccess, 100
				return
			}
			a.Status = StatusError
			a.Error = errorJSON(err)
		})
		m.mu.Lock()
		close(m.finished)
		m.finished = make(chan struct{})
		m.mu.Unlock()
	}()
}

// waitTurn returns once no running action with a lower ID than id shares a
// resource with action id. A store error ends the wait, so the work runs
// rather than hangs.
func (m *Manager) waitTurn(id int64) {
	for {
		m.mu.Lock()
		finished := m.finished
		m.mu.Unlock()
		ahead, err := m.ahead(id)
		if err != nil {
			slog.Error("wait for earlier actions", "id", id, "err", err)
			return
		}
		if !ahead {
			return
		}
		<-finished
	}
}

// ahead reports whether a running action with a lower ID than id shares a
// resource with action id.
func (m *Manager) ahead(id int64) (bool, error) {
	found := false
	err := m.st.Tx(func(tx store.Tx) error {
		own, err := store.GetAction(tx, id)
		if err != nil {
			return err
		}
		a, err := fromStore(own)
		if err != nil {
			return err
		}
		running, err := store.RunningActions(tx)
		if err != nil {
			return err
		}
		for _, r := range running {
			if r.ID >= id {
				break
			}
			other, err := fromStore(r)
			if err != nil {
				return err
			}
			if slices.ContainsFunc(other.Resources, func(ref Ref) bool { return slices.Contains(a.Resources, ref) }) {
				found = true
				return nil
			}
		}
		return nil
	})
	return found, err
}

// WaitIdle returns when projectID has no running action, or ctx's error
// when ctx ends first.
func (m *Manager) WaitIdle(ctx context.Context, projectID int64) error {
	for {
		m.mu.Lock()
		finished := m.finished
		m.mu.Unlock()
		busy := false
		err := m.st.Tx(func(tx store.Tx) error {
			running, err := store.RunningActions(tx)
			busy = slices.ContainsFunc(running, func(a store.Action) bool { return a.ProjectID == projectID })
			return err
		})
		if err != nil {
			return err
		}
		if !busy {
			return nil
		}
		select {
		case <-finished:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *Manager) update(id int64, change func(a *store.Action)) {
	err := m.st.Tx(func(tx store.Tx) error {
		a, err := store.GetAction(tx, id)
		if err != nil {
			return err
		}
		change(&a)
		return store.UpdateAction(tx, a)
	})
	if err != nil {
		slog.Error("update action", "id", id, "err", err)
	}
}

func errorJSON(err error) string {
	var ae *Error
	if !errors.As(err, &ae) {
		ae = &Error{Code: "server_error", Message: err.Error()}
	}
	b, _ := json.Marshal(ae) // two strings always marshal
	return string(b)
}
