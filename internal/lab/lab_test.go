package lab_test

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
)

// projectID is the project of the tests' actions.
const projectID = 1

// start opens a lab on a fake machine; the test closes it.
func start(t testing.TB, machine *fake.Fake, opts lab.Options) *lab.Lab {
	t.Helper()
	opts.Logger = slog.New(slog.DiscardHandler)
	l, err := lab.Open(opts, system.New(machine.Platform()))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// open opens a lab on a new fake machine and closes it after the test.
func open(t testing.TB, opts lab.Options) (*lab.Lab, *fake.Fake) {
	t.Helper()
	machine := fake.New()
	if opts.DataDir == "" {
		opts.DataDir = t.TempDir()
	}
	l := start(t, machine, opts)
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	})
	return l, machine
}

// loadFile reads the lab's state file as it is on disk.
func loadFile(t *testing.T, dataDir string) *state.Tx {
	t.Helper()
	loaded, err := state.Load(filepath.Join(dataDir, lab.StateFile))
	if err != nil {
		t.Fatal(err)
	}
	return state.Begin(loaded)
}

// putProject adds a project named name and returns its ID.
func putProject(t *testing.T, l *lab.Lab, name string) int64 {
	t.Helper()
	var id int64
	err := l.Change(func(tx *state.Tx) error {
		id = state.NewID(tx, state.Projects)
		state.Put(tx, state.Projects, &state.Project{ID: id, Name: name})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestChangeSavesTheNewState(t *testing.T) {
	dataDir := t.TempDir()
	l, _ := open(t, lab.Options{DataDir: dataDir})
	id := putProject(t, l, "default")

	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	if project, err := state.Get(loadFile(t, dataDir), state.Projects, id); err != nil || project.Name != "default" {
		t.Fatalf("the file holds %+v, %v", project, err)
	}
}

func TestOpenTellsANewLab(t *testing.T) {
	dataDir := t.TempDir()
	machine := fake.New()
	l := start(t, machine, lab.Options{DataDir: dataDir})
	if !l.Fresh() {
		t.Error("a lab without a state file is not fresh")
	}
	putProject(t, l, "default")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	l, _ = open(t, lab.Options{DataDir: dataDir})
	if l.Fresh() {
		t.Error("a lab with a state file is fresh")
	}
}

func TestChangeThatOnlyReadsWritesNothing(t *testing.T) {
	dataDir := t.TempDir()
	l, _ := open(t, lab.Options{DataDir: dataDir})
	putProject(t, l, "default")
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, lab.StateFile)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	err := l.Change(func(tx *state.Tx) error {
		state.All(tx, state.Projects)[0].Name = "default" // the same value again
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the state was written again: %v", err)
	}
}

func TestFailedChangeKeepsTheOldState(t *testing.T) {
	l, _ := open(t, lab.Options{})
	id := putProject(t, l, "default")
	refused := errors.New("refused")

	err := l.Change(func(tx *state.Tx) error {
		project, _ := state.Get(tx, state.Projects, id)
		project.Name = "renamed"
		state.Put(tx, state.Projects, &state.Project{ID: state.NewID(tx, state.Projects), Name: "second"})
		return refused
	})

	if !errors.Is(err, refused) {
		t.Fatalf("Change returned %v, want the function's error", err)
	}
	if project, _ := lab.Current(l, state.Projects, id); project.Name != "default" {
		t.Errorf("the project is named %q, want default", project.Name)
	}
	if next := putProject(t, l, "next"); next != id+1 {
		t.Errorf("the next project got ID %d, want %d: the failed change took an ID", next, id+1)
	}
}

func TestCurrentFailsForARecordThatIsGone(t *testing.T) {
	l, _ := open(t, lab.Options{})
	id := putProject(t, l, "default")
	err := l.Change(func(tx *state.Tx) error {
		state.Delete(tx, state.Projects, id)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := lab.Current(l, state.Projects, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("got %v, want state.ErrNotFound", err)
	}
}

func TestOnlyOneLabUsesADataFolder(t *testing.T) {
	machine := fake.New()
	dataDir := t.TempDir()
	first := start(t, machine, lab.Options{DataDir: dataDir})

	if _, err := lab.Open(lab.Options{DataDir: dataDir}, system.New(machine.Platform())); !errors.Is(err, platform.ErrLocked) {
		t.Fatalf("a second lab opened: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := start(t, machine, lab.Options{DataDir: dataDir})
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenEndsActionsThatRanBeforeARestart(t *testing.T) {
	dataDir := t.TempDir()
	tx := state.Begin(state.New())
	id := state.NewID(tx, state.Actions)
	state.Put(tx, state.Actions, &state.Action{ID: id, Command: "start_server", Status: state.ActionRunning})
	data, err := state.Encode(tx.Result(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	writer := state.NewWriter(filepath.Join(dataDir, lab.StateFile), slog.New(slog.DiscardHandler))
	writer.Put(data)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	l, _ := open(t, lab.Options{DataDir: dataDir})

	action, err := lab.Current(l, state.Actions, id)
	if err != nil {
		t.Fatal(err)
	}
	if action.Status != state.ActionError || !strings.Contains(action.Error.Message, "restarted") {
		t.Errorf("the action is %s with %+v, want an error about the restart", action.Status, action.Error)
	}
}

// BenchmarkChangeWithManyActions measures one Change on a lab with five
// projects of a thousand actions each, as when servers poll their actions:
// a real change copies and compares the records it touched and encodes the
// whole state under the lab's mutex, reusing the JSON of the others.
func BenchmarkChangeWithManyActions(b *testing.B) {
	l, _ := open(b, lab.Options{})
	err := l.Change(func(tx *state.Tx) error {
		for range 5 {
			projectID := state.NewID(tx, state.Projects)
			state.Put(tx, state.Projects, &state.Project{ID: projectID, Name: fmt.Sprintf("p%d", projectID)})
			for range lab.MaxFinishedActions {
				id := state.NewID(tx, state.Actions)
				state.Put(tx, state.Actions, &state.Action{
					ID: id, ProjectID: projectID, Command: "start_server", Status: state.ActionSuccess,
					Resources: []state.ResourceRef{{Type: "server", ID: 1}},
				})
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}

	b.Run("ProgressOfOneAction", func(b *testing.B) {
		progress := 0
		for b.Loop() {
			progress++
			err := l.Change(func(tx *state.Tx) error {
				action, err := state.Get(tx, state.Actions, state.FirstID+1)
				if err != nil {
					return err
				}
				action.Progress = progress
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ActionWithoutSteps", func(b *testing.B) {
		// creates and finishes an action, which drops the project's oldest;
		// the last project's, so that the actions the others read stay
		for b.Loop() {
			if _, err := l.Act(tableWith(instant), "poweroff", lab.Request{ProjectID: state.FirstID + 4, ID: 1}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("OnlyRead", func(b *testing.B) {
		for b.Loop() {
			err := l.Change(func(tx *state.Tx) error {
				_, err := state.Get(tx, state.Actions, state.FirstID+1)
				return err
			})
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
