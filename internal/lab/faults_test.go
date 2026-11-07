package lab_test

import (
	"errors"
	"testing"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/state"
)

func TestAPIFaultFailsTheNextCalls(t *testing.T) {
	dataDir := t.TempDir()
	machine := fake.New()
	l := start(t, machine, lab.Options{DataDir: dataDir})
	project, _ := l.CreateProject("default", state.Limits{})
	other, _ := l.CreateProject("other", state.Limits{})
	fault := state.APIFault{Status: 503, Count: 2, DelayMS: 10}
	if err := l.SetAPIFault(project.ID, fault); err != nil {
		t.Fatal(err)
	}

	if _, found := l.TakeAPIFault(other.ID); found {
		t.Error("the other project's call fails too")
	}
	if taken, found := l.TakeAPIFault(project.ID); !found || taken.Status != 503 || taken.DelayMS != 10 {
		t.Errorf("the first call takes %+v, %v", taken, found)
	}

	// the counter outlives a restart
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l = start(t, machine, lab.Options{DataDir: dataDir})
	defer func() { _ = l.Close() }()
	if _, found := l.TakeAPIFault(project.ID); !found {
		t.Error("the second call does not fail after a restart")
	}
	if _, found := l.TakeAPIFault(project.ID); found {
		t.Error("the third call fails")
	}
}

func TestSetAPIFaultChecksItsInput(t *testing.T) {
	l, _ := open(t, lab.Options{})
	project, _ := l.CreateProject("default", state.Limits{})
	for name, test := range map[string]struct {
		project int64
		fault   state.APIFault
		want    error
	}{
		"a negative count": {project.ID, state.APIFault{Status: 500, Count: -1}, lab.ErrInvalid},
		"a status of 200":  {project.ID, state.APIFault{Status: 200, Count: 1}, lab.ErrInvalid},
		"no project":       {project.ID + 1, state.APIFault{Status: 500, Count: 1}, state.ErrNotFound},
	} {
		if err := l.SetAPIFault(test.project, test.fault); !errors.Is(err, test.want) {
			t.Errorf("%s: %v, want %v", name, err, test.want)
		}
	}
	// a count of zero takes the fault away, whatever its status
	if err := l.SetAPIFault(project.ID, state.APIFault{Status: 500, Count: 1}); err != nil {
		t.Fatal(err)
	}
	if err := l.SetAPIFault(project.ID, state.APIFault{}); err != nil {
		t.Fatal(err)
	}
	if _, found := l.TakeAPIFault(project.ID); found {
		t.Error("the fault is still there")
	}
}
