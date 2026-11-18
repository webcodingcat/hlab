// Package actions keeps Hetzner actions and runs their work in a worker
// pool. A handler creates the action with Start inside its transaction and
// hands the work to Run after the commit.
package actions

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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
}

// New returns a manager on st. Its workers need no separate start.
func New(st *store.Store) *Manager {
	return &Manager{st: st, sem: make(chan struct{}, workers)}
}

// Start creates a running action for command on resources in tx.
func (m *Manager) Start(tx store.Tx, command string, resources []Ref) (*Action, error) {
	if resources == nil {
		resources = []Ref{}
	}
	refs, err := json.Marshal(resources)
	if err != nil {
		return nil, err
	}
	a := &Action{
		Command:   command,
		Status:    StatusRunning,
		Started:   time.Now().UTC().Truncate(time.Second),
		Resources: resources,
	}
	a.ID, err = store.CreateAction(tx, store.Action{
		Command:   command,
		Status:    a.Status,
		Started:   a.Started,
		Resources: string(refs),
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// Run runs fn for action id in the worker pool and records its progress
// and result. It returns at once.
func (m *Manager) Run(id int64, fn func(ctx context.Context, progress func(int)) error) {
	go func() {
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
	}()
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
