package lab

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
)

// watchedServer opens a lab on the fake machine with a project and a
// server that runs in its record but whose VM is off, as after a guest's
// own shutdown.
func watchedServer(t *testing.T) (l *Lab, projectID, id int64) {
	t.Helper()
	machine := fake.New()
	l, err := Open(Options{DataDir: t.TempDir(), Logger: slog.New(slog.DiscardHandler)}, system.New(machine.Platform()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	err = l.Change(func(tx *state.Tx) error {
		projectID = state.NewID(tx, state.Projects)
		state.Put(tx, state.Projects, &state.Project{ID: projectID, Name: "watched"})
		id = state.NewID(tx, state.Servers)
		state.Put(tx, state.Servers, &state.Server{ID: id, ProjectID: projectID, Status: state.ServerRunning, WantRunning: true})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := platform.VMSpec{Name: ServerVM(id), Tag: system.VMTag, CPUs: 1, MemoryMB: 512}
	if err := machine.Platform().Hypervisor.CreateVM(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	return l, projectID, id
}

// runInstantAction runs an action without quick changes on the server and
// waits until it has ended.
func runInstantAction(t *testing.T, l *Lab, projectID, id int64) {
	t.Helper()
	table := ActionTable{
		ResourceType: ServerResource,
		Busy:         "server is locked by another action",
		Queued:       true,
		Actions: map[string]ActionSpec{"poweron": {Name: "start_server", Plan: func(*state.Tx, Request) (Plan, error) {
			return Plan{Steps: func(context.Context) error { return nil }}, nil
		}}},
	}
	action, err := l.Act(table, "poweron", Request{ProjectID: projectID, ID: id})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		current, err := Current(l, state.Actions, action.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != state.ActionRunning {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the action did not end")
		}
	}
}

// The watchdog looks at a VM without the mutex. An action that started and
// ended meanwhile makes what it saw out of date: settleServer then leaves
// the server alone. An internal test, as the window cannot be hit from
// outside on purpose.
func TestSettleServerIgnoresWhatAnActionChangedMeanwhile(t *testing.T) {
	l, projectID, id := watchedServer(t)
	machine := l.Machine()
	ref := serverRef(id)
	l.mu.Lock()
	machine.looks = map[state.ResourceRef]bool{ref: false} // the watchdog begins to look
	l.mu.Unlock()

	runInstantAction(t, l, projectID, id)
	machine.settleServer(id, false)
	if server, _ := Current(l, state.Servers, id); server.Status != state.ServerRunning || !server.WantRunning {
		t.Errorf("an out-of-date look changed the server: %s, wanting to run %v", server.Status, server.WantRunning)
	}

	l.mu.Lock()
	machine.looks = map[state.ResourceRef]bool{ref: false} // the next round
	l.mu.Unlock()
	machine.settleServer(id, false)
	if server, _ := Current(l, state.Servers, id); server.Status != state.ServerOff || server.WantRunning {
		t.Errorf("a current look left the server %s, wanting to run %v; want off", server.Status, server.WantRunning)
	}
}

// A deleted server leaves nothing in the lab's memory: its flags and queue
// go when its last action ends, and the watchdog's notes when its round
// ends.
func TestADeletedServerLeavesNothingBehind(t *testing.T) {
	l, projectID, id := watchedServer(t)
	runInstantAction(t, l, projectID, id)
	if err := l.Change(func(tx *state.Tx) error { state.Delete(tx, state.Servers, id); return nil }); err != nil {
		t.Fatal(err)
	}

	l.Machine().watchRound(t.Context())

	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.flags) != 0 || len(l.queues) != 0 || len(l.machine.looks) != 0 {
		t.Errorf("left behind: flags %v, queues %v, looks %v", l.flags, l.queues, l.machine.looks)
	}
}
