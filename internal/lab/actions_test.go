package lab_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/state"
)

// busy is the 423 message of the tests' table.
const busy = "server is locked by another action"

// tableWith returns a server action table whose command "poweroff" is spec.
func tableWith(spec lab.ActionSpec) lab.ActionTable {
	return lab.ActionTable{
		ResourceType: "server",
		Busy:         busy,
		Actions:      map[string]lab.ActionSpec{"poweroff": spec},
	}
}

// openWithProject opens a lab as open does, with the tests' project in it:
// Act refuses an action of a project that is not there.
func openWithProject(t *testing.T) (*lab.Lab, *fake.Fake) {
	t.Helper()
	l, machine := open(t, lab.Options{})
	putTestProject(t, l)
	return l, machine
}

// putTestProject adds the tests' project, with the ID projectID.
func putTestProject(t *testing.T, l *lab.Lab) {
	t.Helper()
	putProjectWithID(t, l, projectID)
}

// putProjectWithID adds a project with a given ID, which CreateProject does
// not take.
func putProjectWithID(t *testing.T, l *lab.Lab, id int64) {
	t.Helper()
	err := l.Change(func(tx *state.Tx) error {
		state.Put(tx, state.Projects, &state.Project{ID: id, Name: "test" + strconv.FormatInt(id, 10)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// request asks for an action on the server id of the tests' project.
func request(id int64) lab.Request {
	return lab.Request{ProjectID: projectID, ID: id}
}

// blocking returns a spec whose steps run until release is closed (or the
// action is canceled).
func blocking(release <-chan struct{}) lab.ActionSpec {
	return lab.ActionSpec{Name: "stop_server", Plan: func(*state.Tx, lab.Request) (lab.Plan, error) {
		return lab.Plan{Steps: func(ctx context.Context) error {
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}, nil
	}}
}

// instant is a spec without steps.
var instant = lab.ActionSpec{Name: "change_protection", Plan: func(*state.Tx, lab.Request) (lab.Plan, error) {
	return lab.Plan{}, nil
}}

// actionStatus returns an action's status.
func actionStatus(t *testing.T, l *lab.Lab, id int64) state.ActionStatus {
	t.Helper()
	action, err := lab.Current(l, state.Actions, id)
	if err != nil {
		t.Fatal(err)
	}
	return action.Status
}

// waitForStatus waits until an action has a status, and returns the action.
func waitForStatus(t *testing.T, l *lab.Lab, id int64, want state.ActionStatus) *state.Action {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		action, err := lab.Current(l, state.Actions, id)
		if err != nil {
			t.Fatal(err)
		}
		if action.Status == want {
			return action
		}
		if time.Now().After(deadline) {
			t.Fatalf("the action is %s, want %s", action.Status, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestActionWithoutStepsFinishesAtOnce(t *testing.T) {
	l, _ := openWithProject(t)
	action, err := l.Act(tableWith(instant), "poweroff", request(7))
	if err != nil {
		t.Fatal(err)
	}
	if action.Status != state.ActionSuccess || action.Progress != 100 || action.Finished == nil {
		t.Errorf("the action is %+v, want it finished", action)
	}
	if len(action.Resources) != 1 || action.Resources[0] != (state.ResourceRef{Type: "server", ID: 7}) {
		t.Errorf("the action names %v, want server 7", action.Resources)
	}
}

func TestUnknownCommandIsAnError(t *testing.T) {
	l, _ := openWithProject(t)
	if _, err := l.Act(tableWith(instant), "explode", request(7)); !errors.Is(err, lab.ErrUnknownCommand) {
		t.Fatalf("got %v, want ErrUnknownCommand", err)
	}
}

// Hetzner answers 423 "locked" while another action runs on the resource.
func TestSecondActionOnABusyResourceIsLocked(t *testing.T) {
	l, _ := openWithProject(t)
	release := make(chan struct{})
	table := tableWith(blocking(release))
	table.Actions["protect"] = instant

	first, err := l.Act(table, "poweroff", request(7))
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"poweroff", "protect"} {
		_, err := l.Act(table, command, request(7))
		var locked *lab.LockedError
		if !errors.As(err, &locked) || locked.Message != busy {
			t.Fatalf("%s on a busy server: got %v, want a LockedError", command, err)
		}
	}
	if _, err := l.Act(table, "poweroff", request(8)); err != nil {
		t.Fatalf("another server is locked too: %v", err)
	}

	close(release)
	waitForStatus(t, l, first.ID, state.ActionSuccess)
	if _, err := l.Act(table, "protect", request(7)); err != nil {
		t.Fatalf("the flag stayed after the action: %v", err)
	}
}

func TestUnflaggedActionRunsAlongside(t *testing.T) {
	l, _ := openWithProject(t)
	release := make(chan struct{})
	defer close(release)
	table := tableWith(blocking(release))
	unflagged := instant
	unflagged.Unflagged = true
	table.Actions["change_dns_ptr"] = unflagged

	if _, err := l.Act(table, "poweroff", request(7)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Act(table, "change_dns_ptr", request(7)); err != nil {
		t.Fatalf("an unflagged action was locked out: %v", err)
	}
}

func TestPlanLocksMoreResources(t *testing.T) {
	l, _ := openWithProject(t)
	release := make(chan struct{})
	defer close(release)
	servers := tableWith(blocking(release))
	attach := lab.ActionSpec{Name: "attach_volume", Plan: func(*state.Tx, lab.Request) (lab.Plan, error) {
		return lab.Plan{Locks: []state.ResourceRef{{Type: "server", ID: 7}}, Steps: func(context.Context) error { return nil }}, nil
	}}
	volumes := lab.ActionTable{ResourceType: "volume", Busy: "volume is busy", Actions: map[string]lab.ActionSpec{"attach": attach}}

	if _, err := l.Act(servers, "poweroff", request(7)); err != nil {
		t.Fatal(err)
	}
	_, err := l.Act(volumes, "attach", request(3))
	var locked *lab.LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("attaching to a busy server: got %v, want a LockedError", err)
	}
}

func TestRefusedActionChangesNothing(t *testing.T) {
	l, _ := openWithProject(t)
	refused := errors.New("invalid input")
	spec := lab.ActionSpec{Name: "stop_server", Plan: func(tx *state.Tx, _ lab.Request) (lab.Plan, error) {
		state.Put(tx, state.Projects, &state.Project{ID: state.NewID(tx, state.Projects)})
		return lab.Plan{}, refused
	}}

	if _, err := l.Act(tableWith(spec), "poweroff", request(7)); !errors.Is(err, refused) {
		t.Fatalf("got %v, want the plan's error", err)
	}
	err := l.View(func(tx *state.Tx) error {
		// the tests' project only
		if projects, actions := state.All(tx, state.Projects), state.All(tx, state.Actions); len(projects) > 1 || len(actions) > 0 {
			t.Errorf("the refused action left %d projects and %d actions", len(projects), len(actions))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateRecordsTheResourceWithItsActions(t *testing.T) {
	l, _ := openWithProject(t)
	table := tableWith(instant)
	table.Actions["poweron"] = instant
	create := func(tx *state.Tx) (int64, error) {
		id := state.NewID(tx, state.Servers)
		state.Put(tx, state.Servers, &state.Server{ID: id, ProjectID: projectID, Name: "web"})
		return id, nil
	}
	actions, err := l.Create(table, request(0), create, nil, lab.Row{Command: "poweroff"}, lab.Row{Command: "poweron"})
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 {
		t.Fatalf("got %d actions, want 2", len(actions))
	}
	server, err := lab.Current(l, state.Servers, actions[0].Resources[0].ID)
	if err != nil {
		t.Fatalf("the actions name no server: %v", err)
	}
	if actions[1].Resources[0].ID != server.ID {
		t.Errorf("the second action names %v, want server %d", actions[1].Resources, server.ID)
	}
}

func TestRefusedCreateRecordsNothing(t *testing.T) {
	l, _ := openWithProject(t)
	refused := errors.New("invalid input")
	table := tableWith(instant)
	table.Actions["poweron"] = lab.ActionSpec{Name: "start_server", Plan: func(*state.Tx, lab.Request) (lab.Plan, error) {
		return lab.Plan{}, refused
	}}
	create := func(tx *state.Tx) (int64, error) {
		id := state.NewID(tx, state.Servers)
		state.Put(tx, state.Servers, &state.Server{ID: id, ProjectID: projectID, Name: "web"})
		return id, nil
	}
	if _, err := l.Create(table, request(0), create, nil, lab.Row{Command: "poweroff"}, lab.Row{Command: "poweron"}); !errors.Is(err, refused) {
		t.Fatalf("got %v, want the second plan's error", err)
	}
	err := l.View(func(tx *state.Tx) error {
		if servers, actions := state.All(tx, state.Servers), state.All(tx, state.Actions); len(servers) > 0 || len(actions) > 0 {
			t.Errorf("the refused create left %d servers and %d actions", len(servers), len(actions))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUndoRunsWhenTheStepsFail(t *testing.T) {
	l, _ := openWithProject(t)
	var keyID int64
	spec := lab.ActionSpec{Name: "stop_server", Plan: func(tx *state.Tx, _ lab.Request) (lab.Plan, error) {
		keyID = state.NewID(tx, state.SSHKeys)
		state.Put(tx, state.SSHKeys, &state.SSHKey{ID: keyID, Name: "made by the quick part"})
		return lab.Plan{
			Steps: func(context.Context) error {
				return errors.New("Hyper-V refused")
			},
			Undo: func(tx *state.Tx) error {
				state.Delete(tx, state.SSHKeys, keyID)
				return nil
			},
		}, nil
	}}

	action, err := l.Act(tableWith(spec), "poweroff", request(7))
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForStatus(t, l, action.ID, state.ActionError)
	if !strings.Contains(failed.Error.Message, "Hyper-V refused") {
		t.Errorf("the action failed with %q, want the platform's error", failed.Error.Message)
	}
	if _, err := lab.Current(l, state.SSHKeys, keyID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the undo did not remove the key: %v", err)
	}
	if _, err := l.Act(tableWith(instant), "poweroff", request(7)); err != nil {
		t.Errorf("the flag stayed after the failure: %v", err)
	}
}

// A queued resource's own action waits behind an action of another table
// that locks the resource (a server's poweron behind a Primary IP's
// assign); Hetzner's answer here is not measured, and the lab waits as
// for the resource's own actions.
func TestQueuedActionsWaitBehindAnActionThatLocksTheirResource(t *testing.T) {
	l, _ := openWithProject(t)
	release := make(chan struct{})
	server := state.ResourceRef{Type: "server", ID: 7}
	assign := lab.ActionTable{ResourceType: "primary_ip", Busy: "primary ip is locked", Actions: map[string]lab.ActionSpec{
		"assign": {Name: "assign_primary_ip", Plan: func(*state.Tx, lab.Request) (lab.Plan, error) {
			plan, err := blocking(release).Plan(nil, lab.Request{})
			plan.Locks = []state.ResourceRef{server}
			return plan, err
		}},
	}}
	servers := tableWith(lab.ActionSpec{Name: "stop_server", Plan: func(*state.Tx, lab.Request) (lab.Plan, error) {
		return lab.Plan{Steps: func(context.Context) error { return nil }}, nil
	}})
	servers.Queued = true

	locking, err := l.Act(assign, "assign", lab.Request{ProjectID: projectID, ID: 3})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := l.Act(servers, "poweroff", request(server.ID))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // a poweroff that does not wait has ended by now
	if got := actionStatus(t, l, queued.ID); got != state.ActionRunning {
		t.Errorf("the server's action is %s while the assign runs, want running (waiting)", got)
	}
	close(release)
	waitForStatus(t, l, locking.ID, state.ActionSuccess)
	waitForStatus(t, l, queued.ID, state.ActionSuccess)
	if _, err := l.Act(servers, "poweroff", request(server.ID)); err != nil {
		t.Errorf("an action after both: %v", err)
	}
}

// An undo marked UndoIfLast leaves alone what an action queued behind it
// has planned since.
func TestUndoIfLastSkipsWhenAnActionIsQueuedBehind(t *testing.T) {
	l, _ := openWithProject(t)
	release := make(chan struct{})
	failing := lab.ActionSpec{Name: "stop_server", Plan: func(*state.Tx, lab.Request) (lab.Plan, error) {
		return lab.Plan{
			Steps: func(ctx context.Context) error {
				select {
				case <-release:
				case <-ctx.Done():
				}
				return errors.New("Hyper-V refused")
			},
			Undo: func(tx *state.Tx) error {
				state.Put(tx, state.SSHKeys, &state.SSHKey{ID: state.NewID(tx, state.SSHKeys), Name: "undone"})
				return nil
			},
			UndoIfLast: true,
		}, nil
	}}
	table := tableWith(failing)
	table.Queued = true
	table.Actions["poweron"] = lab.ActionSpec{Name: "start_server", Plan: func(*state.Tx, lab.Request) (lab.Plan, error) {
		return lab.Plan{Steps: func(context.Context) error { return nil }}, nil
	}}
	first, err := l.Act(table, "poweroff", request(7))
	if err != nil {
		t.Fatal(err)
	}
	second, err := l.Act(table, "poweron", request(7))
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	waitForStatus(t, l, first.ID, state.ActionError)
	waitForStatus(t, l, second.ID, state.ActionSuccess)
	err = l.View(func(tx *state.Tx) error {
		if keys := state.All(tx, state.SSHKeys); len(keys) > 0 {
			t.Error("the undo ran although an action was queued behind")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCloseCancelsRunningActionsWithoutUndo(t *testing.T) {
	dataDir := t.TempDir()
	l := start(t, fake.New(), lab.Options{DataDir: dataDir})
	putTestProject(t, l)
	undone := false
	spec := blocking(nil) // runs until canceled
	plan := spec.Plan
	spec.Plan = func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		p, err := plan(tx, req)
		p.Undo = func(*state.Tx) error { undone = true; return nil }
		return p, err
	}
	action, err := l.Act(tableWith(spec), "poweroff", request(7))
	if err != nil {
		t.Fatal(err)
	}

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	saved, err := state.Get(loadFile(t, dataDir), state.Actions, action.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != state.ActionError || !strings.Contains(saved.Error.Message, "canceled") {
		t.Errorf("the saved action is %s with %+v, want canceled", saved.Status, saved.Error)
	}
	if undone {
		t.Error("a canceled action was undone")
	}
}

// A closed lab starts no action, so that none runs past Close.
func TestCloseRefusesNewActions(t *testing.T) {
	l := start(t, fake.New(), lab.Options{DataDir: t.TempDir()})
	putTestProject(t, l)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Act(tableWith(instant), "poweroff", request(7)); !errors.Is(err, lab.ErrClosed) {
		t.Errorf("an action after Close: %v, want ErrClosed", err)
	}
	created := func(*state.Tx) (int64, error) { return 8, nil }
	if _, err := l.Create(tableWith(instant), request(0), created, nil, lab.Row{Command: "poweroff"}); !errors.Is(err, lab.ErrClosed) {
		t.Errorf("a create after Close: %v, want ErrClosed", err)
	}
	l.Machine().UpdatePicturesLater() // starts nothing that would outlive Close
}

func TestStopProjectActionsCancelsOnlyThatProject(t *testing.T) {
	l, _ := openWithProject(t)
	putProjectWithID(t, l, 2)
	release := make(chan struct{})
	table := tableWith(blocking(release))
	ours, err := l.Act(table, "poweroff", lab.Request{ProjectID: 1, ID: 7})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := l.Act(table, "poweroff", lab.Request{ProjectID: 2, ID: 8})
	if err != nil {
		t.Fatal(err)
	}

	l.StopProjectActions(1)
	if got := actionStatus(t, l, ours.ID); got != state.ActionError {
		t.Errorf("the reset project's action is %s, want error", got)
	}
	if got := actionStatus(t, l, theirs.ID); got != state.ActionRunning {
		t.Errorf("the other project's action is %s, want running", got)
	}
	close(release)
	waitForStatus(t, l, theirs.ID, state.ActionSuccess)
}

func TestOnlyTheNewestFinishedActionsAreKept(t *testing.T) {
	l, _ := openWithProject(t)
	var oldest int64
	err := l.Change(func(tx *state.Tx) error {
		for i := range lab.MaxFinishedActions {
			id := state.NewID(tx, state.Actions)
			if i == 0 {
				oldest = id
			}
			state.Put(tx, state.Actions, &state.Action{ID: id, ProjectID: projectID, Status: state.ActionSuccess})
		}
		// another project's action stays, however old
		state.Put(tx, state.Actions, &state.Action{ID: 1, ProjectID: 2, Status: state.ActionSuccess})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := l.Act(tableWith(instant), "poweroff", request(7)); err != nil {
		t.Fatal(err)
	}
	if _, err := lab.Current(l, state.Actions, oldest); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("the oldest action is still there: %v", err)
	}
	if _, err := lab.Current(l, state.Actions, oldest+1); err != nil {
		t.Errorf("the second oldest action is gone: %v", err)
	}
	if _, err := lab.Current(l, state.Actions, 1); err != nil {
		t.Errorf("the other project's action is gone: %v", err)
	}
}

func TestOfTwoActionsAtOnceOneWins(t *testing.T) {
	l, _ := openWithProject(t)
	release := make(chan struct{})
	defer close(release)
	table := tableWith(blocking(release))

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := l.Act(table, "poweroff", request(7))
			results <- err
		})
	}
	wg.Wait()
	close(results)

	won := 0
	for err := range results {
		if err == nil {
			won++
		}
	}
	if won != 1 {
		t.Errorf("%d actions started, want 1", won)
	}
}

// An action of a change that is no action ends at once: Hetzner's
// apply_firewall when a label selector takes a server.
func TestRecordedActionEndsAtOnce(t *testing.T) {
	l, _ := openWithProject(t)
	resources := []state.ResourceRef{{Type: "firewall", ID: 7}, {Type: "server", ID: 8}}
	var id int64
	if err := l.Change(func(tx *state.Tx) error {
		l.RecordAction(tx, projectID, "apply_firewall", resources)
		id = state.IDs(tx, state.Actions, func(*state.Action) bool { return true })[0]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := actionStatus(t, l, id); got != state.ActionSuccess {
		t.Errorf("the action is %s, want success", got)
	}
}
