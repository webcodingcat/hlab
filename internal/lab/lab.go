// Package lab is the core of HLab: it owns the state, runs every change as a
// transaction, runs the actions and their flags, keeps Hetzner's timings, and
// starts and stops the lab.
//
// The api packages decide what to change and call Change and Act; the slow
// work goes to the system (package system) from the actions' goroutines. The
// rules are in DEVELOPING.md, Concurrency.
package lab

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
)

// StateFile is the name of the state file in the data folder.
const StateFile = "state.json"

// Options are what the lab needs to start.
type Options struct {
	DataDir string
	// TimeScale is the factor on Hetzner's timings: 1 is as fast as Hetzner, 0
	// does not wait at all (the unit tests).
	TimeScale float64
	// Clock tells the time and waits; nil is the real clock.
	Clock Clock
	// Logger gets the lab's log; nil is slog.Default().
	Logger *slog.Logger
	// Catalog is what the lab offers (catalog.Load); nil is the built-in
	// catalog.
	Catalog *catalog.Catalog
}

// Lab is the running lab.
type Lab struct {
	system    *system.System
	clock     Clock
	timeScale float64
	logger    *slog.Logger
	catalog   *catalog.Catalog
	writer    *state.Writer
	release   func() // gives the data folder up
	dataDir   string

	// envMu makes EnvToken one at a time, across its read and write of the
	// env token file; it is not the state's mutex, so it may be held across
	// calls to the system
	envMu sync.Mutex

	// mu guards everything below: the state, the flags and the running actions
	mu      sync.Mutex
	state   *state.State
	flags   map[state.ResourceRef]bool // resources that an action works on
	running map[int64]*runningAction   // by action ID

	ctx     context.Context    // ends when the lab closes; the actions' contexts derive from it
	cancel  context.CancelFunc // ends ctx
	actions sync.WaitGroup     // the running actions' goroutines
}

// runningAction is an action whose steps run in a goroutine.
type runningAction struct {
	projectID int64
	cancel    context.CancelFunc
	done      chan struct{} // closed when the action has ended
}

// Open starts the lab: it claims the data folder, loads the state and ends the
// actions that ran when HLab last stopped.
func Open(opts Options, sys *system.System) (*Lab, error) {
	release, err := sys.ClaimDataDir(opts.DataDir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(opts.DataDir, StateFile)
	loaded, err := state.Load(path)
	if err != nil {
		release()
		return nil, err
	}
	l := &Lab{
		system:    sys,
		clock:     opts.Clock,
		timeScale: opts.TimeScale,
		logger:    opts.Logger,
		catalog:   opts.Catalog,
		release:   release,
		dataDir:   opts.DataDir,
		state:     loaded,
		flags:     map[state.ResourceRef]bool{},
		running:   map[int64]*runningAction{},
	}
	if l.clock == nil {
		l.clock = realClock{}
	}
	if l.logger == nil {
		l.logger = slog.Default()
	}
	if l.catalog == nil {
		l.catalog = catalog.Builtin()
	}
	l.writer = state.NewWriter(path, l.logger)
	l.ctx, l.cancel = context.WithCancel(context.Background())
	if err := l.Change(l.endInterruptedActions); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// endInterruptedActions ends the actions that were running when HLab stopped:
// no goroutine runs them any more, and clients would poll them forever.
func (l *Lab) endInterruptedActions(tx *state.Tx) error {
	for _, action := range state.All(tx, state.Actions) {
		if action.Status == state.ActionRunning {
			l.finishAction(tx, action, "HLab was restarted while the action ran")
		}
	}
	return nil
}

// Close stops the lab: it cancels the running actions, waits until they have
// ended, writes the state and gives the data folder up.
func (l *Lab) Close() error {
	l.cancel()
	l.actions.Wait()
	err := l.writer.Close()
	l.release()
	return err
}

// System returns the system, for the actions' steps.
func (l *Lab) System() *system.System {
	return l.system
}

// Change runs change on the state as one transaction. If change returns an
// error, the state stays as it was and Change returns that error; otherwise
// the new state is saved.
//
// change runs under the lab's mutex: it must not call the system or wait.
func (l *Lab) Change(change func(tx *state.Tx) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.changeLocked(change)
}

// changeLocked is Change for a caller that holds the mutex already, because
// it must check or change more than one transaction's worth without letting
// go: Act checks the flags with the state, the end of an action's steps undoes
// and finishes, TakeAPIFault reads before it changes.
func (l *Lab) changeLocked(change func(tx *state.Tx) error) error {
	tx := state.Begin(l.state)
	if err := change(tx); err != nil {
		return err
	}
	if !tx.Touched() {
		return nil
	}
	next := tx.Result()
	data, err := state.Encode(next, l.Now())
	if err != nil {
		return fmt.Errorf("encoding the state: %w", err)
	}
	l.state = next
	l.writer.Put(data)
	return nil
}

// View runs read on the state; nothing it changes is kept.
func (l *Lab) View(read func(tx *state.Tx) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return read(state.Begin(l.state))
}

// Current returns a record as it is now, after a call to the system: the
// record may have changed or gone meanwhile, so code keeps only its ID across
// such a call. It fails with state.ErrNotFound when the record is gone. The
// record is a copy: to change it, use Change.
//
// It is a function, not a method, because Go methods take no type parameters.
func Current[R state.Record](l *Lab, kind state.Kind[R], id int64) (*R, error) {
	var record *R
	err := l.View(func(tx *state.Tx) error {
		var err error
		record, err = state.Get(tx, kind, id)
		return err
	})
	return record, err
}

// Flush waits until the newest state is on disk.
func (l *Lab) Flush() error {
	return l.writer.Flush()
}

// Now returns the time as the API shows it: UTC, in whole seconds. Records
// that the api packages create take it as their time of creation.
func (l *Lab) Now() time.Time {
	return l.clock.Now().UTC().Truncate(time.Second)
}

// Catalog returns what the lab offers. It does not change while the lab
// runs, so it needs no lock.
func (l *Lab) Catalog() *catalog.Catalog {
	return l.catalog
}
