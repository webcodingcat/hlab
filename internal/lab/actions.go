package lab

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/webcodingcat/hlab/internal/state"
)

// MaxFinishedActions is how many finished actions the state keeps per project.
// Hetzner keeps every action; the lab drops the oldest, because every save
// writes the whole state.
const MaxFinishedActions = 1000

// ErrUnknownCommand reports a command that the resource's action table lacks.
var ErrUnknownCommand = errors.New("unknown action command")

// ErrClosed reports that the lab closes: it starts no action any more.
var ErrClosed = errors.New("the lab is closing")

// LockedError reports that another action works on a resource; the API
// answers it with 423 and the code "locked", as Hetzner does.
type LockedError struct {
	Message string
}

func (e *LockedError) Error() string { return e.Message }

// ActionTable is a resource's actions and what they share. While an action
// runs, it holds the resource's flag, and a second action gets a LockedError
// with the message Busy, unless the table is Queued.
type ActionTable struct {
	ResourceType string                // how actions name the resource: "server"
	Busy         string                // the 423 message: "server is locked by another action"
	Actions      map[string]ActionSpec // by Hetzner's command: "poweroff"
	// Queued: a second action waits until the first is done, as Hetzner
	// runs a server's actions one after another (measured 2026-11-08: a
	// poweron, reset, shutdown and reboot sent during a poweroff all got
	// 201 and ran in order).
	Queued bool
}

// ActionSpec is one row of an action table.
type ActionSpec struct {
	// Name is Hetzner's name of the action ("stop_server" for "poweroff").
	Name string
	// Plan checks the request, makes the quick changes and returns the slow
	// part. It runs in the Act's transaction: if it fails, nothing changes.
	Plan func(tx *state.Tx, req Request) (Plan, error)
	// Unflagged actions change only the record: they take no flag and may run
	// alongside another action (change_protection, a firewall's rows).
	Unflagged bool
}

// Request is the resource and body that an action is asked for.
type Request struct {
	ProjectID int64
	ID        int64  // the resource's ID
	Body      []byte // the request's JSON body, which Plan reads
	// Created is the resource that a Create has just made, for a row of
	// another resource's table (Row.Table); 0 otherwise.
	Created int64
}

// Plan is what an action does besides its quick changes.
type Plan struct {
	// Steps is the slow part, run in a goroutine; nil means the change is
	// complete and the action finishes at once. It ends early when ctx ends
	// (the lab closes, the project is deleted). Across its calls to the system
	// it keeps IDs and looks records up again with Current.
	Steps func(ctx context.Context) error
	// RunIfCanceled runs the steps, with the canceled context, also when
	// the action is canceled while it waits in its queue: they do work
	// nothing else would do (a detach has taken its NIC out of the record,
	// so a network's delete does not find the NIC in the VM).
	RunIfCanceled bool
	// UndoIfLast runs Undo only if no action was queued behind this one on
	// its resource: the later plans have set again what Undo would take
	// back (want_running), and Undo must not overwrite them.
	UndoIfLast bool
	// Undo takes back the quick changes when the steps fail. A canceled
	// action undoes nothing: a deleted project's records are gone anyway,
	// and a stopping lab keeps the state as it is. Undo runs under the
	// mutex, like a Change.
	// Its records may be gone (the project was deleted while steps that did
	// not watch the context failed): Undo then treats state.ErrNotFound as
	// nothing left to undo.
	// After an undo the appliance gets new pictures, before the action's
	// goroutine ends (Machine.updatePicturesAfterUndo): the steps may have
	// sent pictures with the quick changes, and an undo takes them back
	// from the state only.
	Undo func(tx *state.Tx) error
	// Resources are more resources the action names (a target server).
	Resources []state.ResourceRef
	// Locks are more resources the steps work on (the server a volume
	// attaches to): they are flagged too, and a LockedError if busy.
	Locks []state.ResourceRef
}

// Row is one row of an action table to run, with the body its plan reads;
// a nil Body takes the Request's.
type Row struct {
	Command string
	Body    []byte
	// Table, if set, is another resource's table, and ID that resource:
	// a create that starts actions of other resources (a server's create
	// applies firewalls, whose actions name the firewall first). The row's
	// plan finds the created resource in Request.Created.
	Table *ActionTable
	ID    int64
}

// Act runs one action of a resource from its table and returns the action as
// the API shows it right after.
func (l *Lab) Act(table ActionTable, command string, req Request) (state.Action, error) {
	actions, err := l.run(table, req, nil, nil, []Row{{Command: command}})
	if err != nil {
		return state.Action{}, err
	}
	return actions[0], nil
}

// ActAll runs several actions of a resource in one transaction, in order,
// each with its own body, as a firewall's apply_to_resources does for each
// resource it names. If one fails, nothing changes. Act, ActAll and
// Create share run and differ only in what they pass it: each is the
// name its callers read, for one of the three ways an API starts actions.
func (l *Lab) ActAll(table ActionTable, req Request, rows ...Row) ([]state.Action, error) {
	return l.run(table, req, nil, nil, rows)
}

// Create records a new resource and runs its first actions in one
// transaction, so that a resource never stays without the action that
// makes it: create puts the record and returns its ID, then each row
// runs on it in order, a later one queued behind an earlier one if the
// table is Queued. answer, if not nil, then reads the records as the rows
// left them, still in the transaction, for the API's answer: once it ends,
// the steps may change them. req gives the project and the body; its ID is
// create's. If anything fails, nothing changes. The rows' flags are set
// only when all are planned, so a row's Locks are not checked against an
// earlier row's.
func (l *Lab) Create(table ActionTable, req Request, create func(tx *state.Tx) (int64, error),
	answer func(tx *state.Tx, id int64), rows ...Row) ([]state.Action, error) {
	return l.run(table, req, create, answer, rows)
}

// run is Act, ActAll and Create: it checks the project, runs create if
// there is one, plans the rows in one transaction and starts them once
// the transaction is saved.
func (l *Lab) run(table ActionTable, req Request, create func(tx *state.Tx) (int64, error),
	answer func(tx *state.Tx, id int64), rows []Row) ([]state.Action, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, ErrClosed
	}
	var planned []plannedAction
	err := l.changeLocked(func(tx *state.Tx) error {
		if err := CheckProject(tx, req.ProjectID); err != nil {
			return err
		}
		if create != nil {
			var err error
			if req.ID, err = create(tx); err != nil {
				return err
			}
		}
		for _, row := range rows {
			rowReq, rowTable := req, table
			if row.Body != nil {
				rowReq.Body = row.Body
			}
			if row.Table != nil {
				rowTable, rowReq.ID, rowReq.Created = *row.Table, row.ID, req.ID
			}
			action, err := l.planAction(tx, rowTable, row.Command, rowReq)
			if err != nil {
				return err
			}
			planned = append(planned, action)
		}
		if answer != nil {
			answer(tx, req.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	actions := make([]state.Action, 0, len(planned))
	for _, action := range planned {
		l.start(action)
		actions = append(actions, *action.action)
	}
	return actions, nil
}

// RecordAction records an action of a change that is no action, as
// Hetzner records one when a label selector takes a server under a
// firewall or lets it go. The change has nothing left to do on the
// machine, so the action ends at once. The caller is in a transaction.
func (l *Lab) RecordAction(tx *state.Tx, projectID int64, command string, resources []state.ResourceRef) {
	l.finishAction(tx, l.newAction(tx, projectID, command, resources), nil)
}

// plannedAction is an action that its transaction has recorded and whose
// steps start once the transaction is saved.
type plannedAction struct {
	action *state.Action
	plan   Plan
	target state.ResourceRef
	spec   ActionSpec
	queued bool
}

// planAction checks the flags, runs the row's Plan and records the action,
// finished at once if it has no steps. The caller holds the mutex and saves
// tx.
func (l *Lab) planAction(tx *state.Tx, table ActionTable, command string, req Request) (plannedAction, error) {
	spec, found := table.Actions[command]
	if !found {
		return plannedAction{}, fmt.Errorf("%s %q: %w", table.ResourceType, command, ErrUnknownCommand)
	}
	target := state.ResourceRef{Type: table.ResourceType, ID: req.ID}
	if !spec.Unflagged && !table.Queued && l.flags[target] > 0 {
		return plannedAction{}, &LockedError{Message: table.Busy}
	}
	plan, err := spec.Plan(tx, req)
	if err != nil {
		return plannedAction{}, err
	}
	for _, ref := range plan.Locks {
		if l.flags[ref] > 0 {
			return plannedAction{}, &LockedError{Message: fmt.Sprintf("%s is locked by another action", ref.Type)}
		}
	}
	resources := append([]state.ResourceRef{target}, plan.Resources...)
	action := l.newAction(tx, req.ProjectID, spec.Name, resources)
	if plan.Steps == nil {
		l.finishAction(tx, action, nil)
	}
	return plannedAction{action: action, plan: plan, target: target, spec: spec, queued: table.Queued}, nil
}

// ChangeIdle runs change as Change does, unless an action works on the
// resource: then it fails with a LockedError with the message busy. It is
// for changes that are no actions but must not run beside one, such as
// deleting a Primary IP while it is being assigned.
func (l *Lab) ChangeIdle(ref state.ResourceRef, busy string, change func(tx *state.Tx) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.flags[ref] > 0 {
		return &LockedError{Message: busy}
	}
	return l.changeLocked(change)
}

// ChangeAborting runs change as Change does, for the delete of a resource
// that actions may work on: once the change is saved, each running action
// that flags or names the resource is canceled and ends with Hetzner's
// "aborted", as a network's add_route and a server's attach_to_network do
// when the network is deleted (measured 2026-11-09). Its quick changes
// are not undone: the resource is gone. It returns the channels that
// close when those actions have ended, for work that must come after
// their steps (DetachNICsLater: an attach's NIC may still arrive).
func (l *Lab) ChangeAborting(ref state.ResourceRef, change func(tx *state.Tx) error) ([]<-chan struct{}, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.changeLocked(change); err != nil {
		return nil, err
	}
	var aborted []<-chan struct{}
	for _, run := range l.running {
		if slices.Contains(run.flagged, ref) || slices.Contains(run.names, ref) {
			run.aborted = true
			run.cancel()
			aborted = append(aborted, run.done)
		}
	}
	return aborted, nil
}

// newAction adds a running action.
func (l *Lab) newAction(tx *state.Tx, projectID int64, command string, resources []state.ResourceRef) *state.Action {
	action := &state.Action{
		ID:        state.NewID(tx, state.Actions),
		ProjectID: projectID,
		Command:   command,
		Status:    state.ActionRunning,
		Started:   l.Now(),
		Resources: resources,
	}
	state.Put(tx, state.Actions, action)
	l.logger.Info("action started", "id", action.ID, "command", command, "resources", resources)
	return action
}

// finishAction ends an action (endAction) and then drops the project's
// oldest finished actions (trimActions).
func (l *Lab) finishAction(tx *state.Tx, action *state.Action, failure *state.Failure) {
	l.endAction(action, failure)
	trimActions(tx, action.ProjectID)
}

// endAction sets an action finished: success without a failure, else an
// error.
func (l *Lab) endAction(action *state.Action, failure *state.Failure) {
	finished := l.Now()
	action.Progress = 100
	action.Finished = &finished
	action.Status = state.ActionSuccess
	if failure != nil {
		action.Status = state.ActionError
		action.Error = failure
		l.logger.Warn("action failed", "id", action.ID, "command", action.Command, "code", failure.Code, "error", failure.Message)
	}
}

// actionFailed is the failure of an action whose steps failed.
func actionFailed(message string) *state.Failure {
	return &state.Failure{Code: "action_failed", Message: message}
}

// actionAborted is Hetzner's failure of an action whose resource was
// deleted while it ran (measured 2026-11-09: a network's add_route).
var actionAborted = state.Failure{Code: "aborted", Message: ""}

// trimActions drops a project's oldest finished actions beyond
// MaxFinishedActions.
func trimActions(tx *state.Tx, projectID int64) {
	done := state.IDs(tx, state.Actions, func(kept *state.Action) bool {
		return kept.ProjectID == projectID && kept.Status != state.ActionRunning
	})
	for _, id := range done[:max(len(done)-MaxFinishedActions, 0)] {
		state.Delete(tx, state.Actions, id)
	}
}

// start runs a planned action's steps in a goroutine, if it has any. An
// action's life: Act or Create plans it and records it (planAction); start
// flags its resources and, with a queue, puts it behind the action queued
// before on its resource; the goroutine waits for that one, runs the steps
// and calls end, which takes the flags off, undoes the plan's quick changes
// if the steps failed and finishes the action. The caller holds the mutex.
func (l *Lab) start(planned plannedAction) {
	if planned.plan.Steps == nil {
		return
	}
	flagged := planned.plan.Locks
	var queue *state.ResourceRef
	if !planned.spec.Unflagged {
		flagged = append([]state.ResourceRef{planned.target}, flagged...)
		if planned.queued {
			queue = &planned.target
		}
	}
	var before chan struct{}
	if queue != nil {
		before = l.queues[*queue]
	}
	ctx, cancel := context.WithCancel(l.ctx)
	run := &runningAction{
		id: planned.action.ID, projectID: planned.action.ProjectID, cancel: cancel, done: make(chan struct{}),
		before: before, undo: planned.plan.Undo, undoIfLast: planned.plan.UndoIfLast, flagged: flagged,
		queue: queue, lockQueues: planned.plan.Locks, names: planned.action.Resources,
	}
	l.running[run.id] = run
	for _, ref := range flagged {
		l.flags[ref]++
		l.machine.noteFlag(ref)
	}
	if queue != nil {
		l.queues[*queue] = run.done
	}
	// a resource's own queued actions wait behind an action that locks it
	// (a server's poweron behind a Primary IP's assign); the lock check
	// found nothing flagged on it, so nothing waits in its queue yet
	for _, ref := range planned.plan.Locks {
		l.queues[ref] = run.done
	}
	l.background.Go(func() {
		defer func() {
			// an action aborted while it waits ends at once, but the one
			// behind it must still wait for the one before it
			if before != nil {
				<-before
			}
			close(run.done)
		}()
		defer cancel()
		err := waitFor(ctx, before)
		if err == nil || planned.plan.RunIfCanceled {
			err = planned.plan.Steps(ctx)
		}
		// canceled only when the steps gave up: steps that finished despite
		// the cancel have done their work, and the action succeeded
		if undone := l.end(run, err, err != nil && ctx.Err() != nil); undone {
			l.machine.updatePicturesAfterUndo()
		}
	})
}

// waitFor waits until done is closed (nil is done already) or ctx ends.
func waitFor(ctx context.Context, done <-chan struct{}) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stillRunning reports whether the action whose done this is has not
// ended yet.
func stillRunning(done <-chan struct{}) bool {
	if done == nil {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

// end finishes an action whose steps have returned: it takes the flags off,
// undoes the quick changes if the steps failed, and finishes the action.
// It tells whether it undid: the caller then sends the pictures.
func (l *Lab) end(run *runningAction, err error, canceled bool) (undone bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var failure *state.Failure
	switch {
	case run.aborted:
		failure = new(actionAborted)
		canceled = true // nothing to undo: its resource is gone
	case canceled:
		failure = actionFailed("the action was canceled: HLab stopped or the project was deleted")
	case err != nil:
		failure = actionFailed(err.Error())
	}
	for _, ref := range run.flagged {
		if l.flags[ref]--; l.flags[ref] <= 0 {
			delete(l.flags, ref)
		}
	}
	last := run.queue == nil || l.queues[*run.queue] == run.done
	if run.queue != nil && last {
		// the last of its queue; one that ends before the action it waited
		// for (aborted) hands the queue back to that one
		if stillRunning(run.before) {
			l.queues[*run.queue] = run.before
		} else {
			delete(l.queues, *run.queue)
		}
	}
	for _, ref := range run.lockQueues {
		if l.queues[ref] == run.done {
			delete(l.queues, ref)
		}
	}
	delete(l.running, run.id)
	finish := func(tx *state.Tx) error {
		action, err := state.Get(tx, state.Actions, run.id)
		if errors.Is(err, state.ErrNotFound) {
			return nil // its project was deleted meanwhile
		}
		if err != nil {
			return err
		}
		l.finishAction(tx, action, failure)
		return nil
	}
	undo := err != nil && !canceled && run.undo != nil && (last || !run.undoIfLast)
	if undo {
		// the undo and the end in one change; a failing undo must not keep
		// the action running, so the end then goes alone
		undoErr := l.changeLocked(func(tx *state.Tx) error {
			if err := run.undo(tx); err != nil {
				return err
			}
			return finish(tx)
		})
		if undoErr == nil {
			return true
		}
		l.logger.Error("undoing a failed action failed", "id", run.id, "error", undoErr)
	}
	if finishErr := l.changeLocked(finish); finishErr != nil {
		l.logger.Error("finishing an action failed", "id", run.id, "error", finishErr)
	}
	return false
}

// StopProjectActions cancels the running actions of a project and waits until
// they have ended; DeleteProject calls it before it removes the project's
// servers from the machine.
func (l *Lab) StopProjectActions(projectID int64) {
	l.mu.Lock()
	var waits []chan struct{}
	for _, run := range l.running {
		if run.projectID == projectID {
			run.cancel()
			waits = append(waits, run.done)
		}
	}
	l.mu.Unlock()
	for _, done := range waits {
		<-done
	}
}
