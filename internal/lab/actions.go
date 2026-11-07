package lab

import (
	"context"
	"errors"
	"fmt"

	"github.com/webcodingcat/hlab/internal/state"
)

// MaxFinishedActions is how many finished actions the state keeps per project.
// Hetzner keeps every action; the lab drops the oldest, because every save
// writes the whole state.
const MaxFinishedActions = 1000

// ErrUnknownCommand reports a command that the resource's action table lacks.
var ErrUnknownCommand = errors.New("unknown action command")

// LockedError reports that another action works on a resource; the API
// answers it with 423 and the code "locked", as Hetzner does.
type LockedError struct {
	Message string
}

func (e *LockedError) Error() string { return e.Message }

// ActionTable is a resource's actions and what they share. While an action
// runs, it holds the resource's flag, and a second action gets a LockedError
// with the message Busy.
type ActionTable struct {
	ResourceType string                // how actions name the resource: "server"
	Busy         string                // the 423 message: "server is locked by another action"
	Actions      map[string]ActionSpec // by Hetzner's command: "poweroff"
}

// ActionSpec is one row of an action table.
type ActionSpec struct {
	// Name is Hetzner's name of the action ("stop_server" for "poweroff").
	Name string
	// Plan checks the request, makes the quick changes and returns the slow
	// part. It runs in the Act's transaction: if it fails, nothing changes.
	Plan func(tx *state.Tx, req Request) (Plan, error)
	// Unflagged actions change only the record: they take no flag and may run
	// alongside another action (change_protection, change_dns_ptr).
	Unflagged bool
}

// Request is the resource and body that an action is asked for.
type Request struct {
	ProjectID int64
	ID        int64  // the resource's ID
	Body      []byte // the request's JSON body, which Plan reads
}

// Plan is what an action does besides its quick changes.
type Plan struct {
	// Steps is the slow part, run in a goroutine; nil means the change is
	// complete and the action finishes at once. It ends early when ctx ends
	// (the lab closes, the project is reset). Across its calls to the system
	// it keeps IDs and looks records up again with Current.
	Steps func(ctx context.Context) error
	// Undo takes back the quick changes when the steps fail. A canceled
	// action undoes nothing: a reset removes everything anyway, and a stopping
	// lab keeps the state as it is. Undo runs under the mutex, like a Change.
	Undo func(tx *state.Tx) error
	// Resources are more resources the action names (a target server).
	Resources []state.ResourceRef
	// Locks are more resources the steps work on (the server a volume
	// attaches to): they are flagged too, and a LockedError if busy.
	Locks []state.ResourceRef
}

// Act runs one action of a resource from its table and returns the action as
// the API shows it right after.
func (l *Lab) Act(table ActionTable, command string, req Request) (state.Action, error) {
	spec, found := table.Actions[command]
	if !found {
		return state.Action{}, fmt.Errorf("%s %q: %w", table.ResourceType, command, ErrUnknownCommand)
	}
	target := state.ResourceRef{Type: table.ResourceType, ID: req.ID}

	l.mu.Lock()
	defer l.mu.Unlock()
	if !spec.Unflagged && l.flags[target] {
		return state.Action{}, &LockedError{Message: table.Busy}
	}
	var plan Plan
	var action *state.Action
	err := l.changeLocked(func(tx *state.Tx) error {
		var err error
		if plan, err = spec.Plan(tx, req); err != nil {
			return err
		}
		for _, ref := range plan.Locks {
			if l.flags[ref] {
				return &LockedError{Message: fmt.Sprintf("%s is locked by another action", ref.Type)}
			}
		}
		resources := append([]state.ResourceRef{target}, plan.Resources...)
		action = l.newAction(tx, req.ProjectID, spec.Name, resources)
		if plan.Steps == nil {
			l.finishAction(tx, action, "")
		}
		return nil
	})
	if err != nil {
		return state.Action{}, err
	}
	if plan.Steps != nil {
		flagged := plan.Locks
		if !spec.Unflagged {
			flagged = append([]state.ResourceRef{target}, flagged...)
		}
		l.start(action.ID, req.ProjectID, plan, flagged)
	}
	return *action, nil
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

// finishAction ends an action: success, or an error with message. Then the
// project's oldest finished actions go, beyond MaxFinishedActions.
func (l *Lab) finishAction(tx *state.Tx, action *state.Action, message string) {
	finished := l.Now()
	action.Progress = 100
	action.Finished = &finished
	action.Status = state.ActionSuccess
	if message != "" {
		action.Status = state.ActionError
		action.Error = &state.Failure{Code: "action_failed", Message: message}
		l.logger.Warn("action failed", "id", action.ID, "command", action.Command, "error", message)
	}

	var done []int64
	for _, kept := range state.All(tx, state.Actions) {
		if kept.ProjectID == action.ProjectID && kept.Status != state.ActionRunning {
			done = append(done, kept.ID)
		}
	}
	for _, id := range done[:max(len(done)-MaxFinishedActions, 0)] {
		state.Delete(tx, state.Actions, id)
	}
}

// start runs an action's steps in a goroutine; it holds the flagged resources
// until the steps end. The caller holds the mutex.
func (l *Lab) start(actionID, projectID int64, plan Plan, flagged []state.ResourceRef) {
	ctx, cancel := context.WithCancel(l.ctx)
	run := &runningAction{projectID: projectID, cancel: cancel, done: make(chan struct{})}
	l.running[actionID] = run
	for _, ref := range flagged {
		l.flags[ref] = true
	}
	l.actions.Add(1)
	go func() {
		defer l.actions.Done()
		defer close(run.done)
		defer cancel()
		err := plan.Steps(ctx)
		// canceled only when the steps gave up: steps that finished despite
		// the cancel have done their work, and the action succeeded
		l.end(actionID, plan.Undo, flagged, err, err != nil && ctx.Err() != nil)
	}()
}

// end finishes an action whose steps have returned: it takes the flags off,
// undoes the quick changes if the steps failed, and finishes the action.
func (l *Lab) end(actionID int64, undo func(tx *state.Tx) error, flagged []state.ResourceRef, err error, canceled bool) {
	message := ""
	switch {
	case canceled:
		message = "the action was canceled: HLab stopped or the project was reset"
	case err != nil:
		message = err.Error()
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	for _, ref := range flagged {
		delete(l.flags, ref)
	}
	delete(l.running, actionID)
	if err != nil && !canceled && undo != nil {
		// its own change: a failing undo must not keep the action running
		if undoErr := l.changeLocked(undo); undoErr != nil {
			l.logger.Error("undoing a failed action failed", "id", actionID, "error", undoErr)
		}
	}
	finishErr := l.changeLocked(func(tx *state.Tx) error {
		action, err := state.Get(tx, state.Actions, actionID)
		if errors.Is(err, state.ErrNotFound) {
			return nil // its project was reset meanwhile
		}
		if err != nil {
			return err
		}
		l.finishAction(tx, action, message)
		return nil
	})
	if finishErr != nil {
		l.logger.Error("finishing an action failed", "id", actionID, "error", finishErr)
	}
}

// StopProjectActions cancels the running actions of a project and waits until
// they have ended; a project reset calls it before it removes the project's
// resources.
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
