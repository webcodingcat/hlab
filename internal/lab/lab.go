// Package lab is the core of HLab: it owns the state, runs every change as a
// transaction, runs the actions and their flags, and
// starts and stops the lab.
//
// The api packages decide what to change and call Change and Act; the slow
// work goes to the system (package system) from the actions' goroutines,
// through the methods of Machine, the lab's side on the machine. The rules
// are in DEVELOPING.md, Concurrency and Actions.
package lab

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/config"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
)

// StateFile is the name of the state file in the data folder.
const StateFile = "state.json"

// Options are what the lab needs to start.
type Options struct {
	DataDir string
	// Now tells the time; nil is time.Now. A test sets it to give
	// resources created one after another different times, which Hetzner
	// gives in whole seconds.
	Now func() time.Time
	// Logger gets the lab's log; nil is slog.Default().
	Logger *slog.Logger
	// Catalog is what the lab offers (catalog.Load); nil is the built-in
	// catalog.
	Catalog *catalog.Catalog
	// LabKey is the lab's public SSH key, which every server's root takes
	// for `hlab ssh`; "" for none.
	LabKey string
	// Room bounds what the servers may take of the host (hlab.yaml); the
	// zero value takes config.Default's.
	Room config.Room
}

// Lab is the running lab.
type Lab struct {
	system    *system.System
	now       func() time.Time
	logger    *slog.Logger
	catalog   *catalog.Catalog
	writer    *state.Writer
	release   func() // gives the data folder up
	dataDir   string
	fresh     bool // Open found no state file
	closeOnce sync.Once
	closeErr  error // of the first Close

	machine *Machine

	// envMu makes EnvToken one at a time, across its read and write of the
	// env token file; it is not the state's mutex, so it may be held across
	// calls to the system
	envMu sync.Mutex

	// mu guards everything below: the state, the flags and the running
	// actions; and the machine's fields (Machine says why)
	mu    sync.Mutex
	state *state.State
	// closed is set when Close begins: no action and no background work
	// starts after it, so that Close waits for all of them
	closed bool
	// flags count the actions that work on or wait for a resource
	flags map[state.ResourceRef]int
	// queues hold the last action queued on a resource (ActionTable.Queued):
	// the next one waits until it is done
	queues  map[state.ResourceRef]chan struct{}
	running map[int64]*runningAction // by action ID

	ctx    context.Context    // ends when the lab closes; the actions' contexts derive from it
	cancel context.CancelFunc // ends ctx
	// background runs every goroutine the lab starts, so Close waits for
	// them: the actions', UpdatePicturesLater's, DetachNICsLater's and
	// DeleteProject's removals; it is added to only under mu while the
	// lab is not closed
	background sync.WaitGroup
}

// runningAction is an action whose steps run in a goroutine.
type runningAction struct {
	id         int64
	projectID  int64
	cancel     context.CancelFunc
	done       chan struct{} // closed when the action has ended
	undo       func(tx *state.Tx) error
	undoIfLast bool
	flagged    []state.ResourceRef // the flags it holds until it ends
	queue      *state.ResourceRef  // the resource it queued on, if any
	lockQueues []state.ResourceRef // the resources it locks, whose queues wait for it
	names      []state.ResourceRef // the resources the action names
	aborted    bool                // its resource was deleted: it ends as Hetzner's "aborted"
}

// Open starts the lab: it claims the data folder, loads the state and ends the
// actions that ran when HLab last stopped.
func Open(opts Options, sys *system.System) (*Lab, error) {
	release, err := sys.ClaimDataDir(opts.DataDir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(opts.DataDir, StateFile)
	_, statErr := os.Stat(path)
	loaded, err := state.Load(path)
	if err != nil {
		release()
		return nil, err
	}
	l := &Lab{
		system:  sys,
		now:     opts.Now,
		logger:  opts.Logger,
		catalog: opts.Catalog,
		release: release,
		dataDir: opts.DataDir,
		fresh:   errors.Is(statErr, fs.ErrNotExist),
		state:   loaded,
		flags:   map[state.ResourceRef]int{},
		queues:  map[state.ResourceRef]chan struct{}{},
		running: map[int64]*runningAction{},
	}
	if l.now == nil {
		l.now = time.Now
	}
	if l.logger == nil {
		l.logger = slog.Default()
	}
	if l.catalog == nil {
		l.catalog = catalog.Builtin()
	}
	room := opts.Room
	if room == (config.Room{}) {
		room = config.Default().Room
	}
	l.machine = &Machine{lab: l, system: sys, labKey: opts.LabKey, room: room, imageLoads: map[string]*sync.Mutex{}}
	l.writer = state.NewWriter(path, l.logger)
	l.ctx, l.cancel = context.WithCancel(context.Background())
	if err := l.Change(l.endInterruptedActions); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// endInterruptedActions ends the actions that were running when HLab stopped:
// no goroutine runs them any more, and clients would poll them forever. Each
// project's finished actions are trimmed once, after all of them ended.
func (l *Lab) endInterruptedActions(tx *state.Tx) error {
	running := state.IDs(tx, state.Actions, func(action *state.Action) bool {
		return action.Status == state.ActionRunning
	})
	projects := map[int64]bool{}
	for _, id := range running {
		action, err := state.Get(tx, state.Actions, id)
		if err != nil {
			return err
		}
		l.endAction(action, actionFailed("HLab was restarted while the action ran"))
		projects[action.ProjectID] = true
	}
	for projectID := range projects {
		trimActions(tx, projectID)
	}
	return nil
}

// Close stops the lab: it cancels the running actions, waits until they have
// ended, stops what Machine.Start started (Machine.stop), writes the state and gives
// the data folder up. Later calls return the first one's result, so that
// every way to stop may call it.
func (l *Lab) Close() error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()
		l.cancel()
		l.background.Wait()
		machineErr := l.machine.stop()
		if machineErr != nil {
			machineErr = fmt.Errorf("stopping the machine's side: %w", machineErr)
		}
		l.closeErr = errors.Join(machineErr, l.writer.Close())
		l.release()
	})
	return l.closeErr
}

// Fresh tells whether Open found no state file: the lab is new, and the
// service sets it up (the project default).
func (l *Lab) Fresh() bool {
	return l.fresh
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
	if !tx.Changed() {
		return nil // only read, or changed back: nothing to save
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

// View runs read on the state. Its Tx copies nothing (state.Read): read must
// not change a record, and Put, Delete and NewID panic.
func (l *Lab) View(read func(tx *state.Tx) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return read(l.read())
}

// read returns the state to read for a caller that holds the mutex, such as
// one that reads it together with the flags or the machine's fields. Its Tx
// copies nothing (state.Read): Put, Delete and NewID panic.
func (l *Lab) read() *state.Tx {
	return state.Read(l.state)
}

// Current returns a record as it is now, after a call to the system: the
// record may have changed or gone meanwhile, so code keeps only its ID across
// such a call. It fails with state.ErrNotFound when the record is gone. The
// record is the state's, to read only: to change it, use Change.
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
	return l.now().UTC().Truncate(time.Second)
}

// Catalog returns what the lab offers. It does not change while the lab
// runs, so it needs no lock.
func (l *Lab) Catalog() *catalog.Catalog {
	return l.catalog
}

// Machine returns the lab's side on the machine, whose methods are the
// actions' steps.
func (l *Lab) Machine() *Machine {
	return l.machine
}
