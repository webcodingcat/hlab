package state

import (
	"errors"
	"testing"
	"time"
)

// withKey returns a state with one SSH key, and the key's ID.
func withKey(t *testing.T) (*State, int64) {
	t.Helper()
	tx := Begin(New())
	id := NewID(tx, SSHKeys)
	Put(tx, SSHKeys, &SSHKey{ID: id, Name: "admin", Labels: map[string]string{"team": "a"}})
	return tx.Result(), id
}

func TestDroppedTxLeavesTheStateAsItWas(t *testing.T) {
	base, id := withKey(t)

	tx := Begin(base)
	key, err := Get(tx, SSHKeys, id)
	if err != nil {
		t.Fatal(err)
	}
	key.Name = "changed"
	key.Labels["team"] = "b"
	Delete(tx, SSHKeys, id)
	NewID(tx, SSHKeys)
	// the tx is dropped: no Result

	check := Begin(base)
	kept, err := Get(check, SSHKeys, id)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Name != "admin" || kept.Labels["team"] != "a" {
		t.Errorf("the key became %+v", kept)
	}
	if next := NewID(check, SSHKeys); next != id+1 {
		t.Errorf("the next ID is %d, want %d", next, id+1)
	}
}

func TestResultHoldsTheChanges(t *testing.T) {
	base, id := withKey(t)

	tx := Begin(base)
	key, _ := Get(tx, SSHKeys, id)
	key.Name = "renamed"
	tx.Lab().Faults = map[int64]ProjectFaults{1: {API: APIFault{Status: 503, Count: 2}}}
	changed := tx.Result()

	check := Begin(changed)
	if got, _ := Get(check, SSHKeys, id); got.Name != "renamed" {
		t.Errorf("the key is named %q, want renamed", got.Name)
	}
	if got := check.Lab().Faults[1].API.Count; got != 2 {
		t.Errorf("the fault count is %d, want 2", got)
	}
}

func TestNewIDsStartAtFirstIDPerKind(t *testing.T) {
	tx := Begin(New())
	first, second := NewID(tx, Projects), NewID(tx, Projects)
	other := NewID(tx, Tokens)

	if first != FirstID || second != FirstID+1 || other != FirstID {
		t.Errorf("IDs %d, %d and %d; want %d, %d and %d", first, second, other, FirstID, FirstID+1, FirstID)
	}
}

func TestGetOfAMissingRecordIsErrNotFound(t *testing.T) {
	tx := Begin(New())
	if _, err := Get(tx, Projects, 42); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestAllIsSortedByID(t *testing.T) {
	tx := Begin(New())
	for _, id := range []int64{3, 1, 2} {
		Put(tx, Projects, &Project{ID: id})
	}

	var ids []int64
	for _, project := range All(tx, Projects) {
		ids = append(ids, project.ID)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Errorf("All returned IDs %v, want [1 2 3]", ids)
	}
}

func TestFindReturnsTheMatchingRecord(t *testing.T) {
	tx := Begin(New())
	for _, name := range []string{"a", "b", "c"} {
		Put(tx, Projects, &Project{ID: NewID(tx, Projects), Name: name})
	}
	if project, found := Find(tx, Projects, func(p *Project) bool { return p.Name == "b" }); !found || project.Name != "b" {
		t.Errorf("found %+v, %v", project, found)
	}
	if _, found := Find(tx, Projects, func(p *Project) bool { return p.Name == "x" }); found {
		t.Error("found a project that is not there")
	}
}

func TestATxThatOnlyReadsHasNothingToSave(t *testing.T) {
	s := New()
	tx := Begin(s)
	Put(tx, Projects, &Project{ID: NewID(tx, Projects), Name: "default"})
	s = tx.Result()

	tx = Begin(s)
	All(tx, Projects)
	_ = tx.Lab()
	if tx.Changed() {
		t.Error("a Tx that only read counts as changed")
	}
	All(tx, Projects)[0].Name = "other"
	if !tx.Changed() {
		t.Error("a Tx that renamed a project counts as unchanged")
	}
}

func TestChangedSeesEveryKindOfChangeToARecord(t *testing.T) {
	base, id := withKey(t)
	tests := map[string]func(tx *Tx){
		"in place": func(tx *Tx) {
			key, _ := Get(tx, SSHKeys, id)
			key.Labels["team"] = "b"
		},
		"put":    func(tx *Tx) { Put(tx, SSHKeys, &SSHKey{ID: id, Name: "other"}) },
		"add":    func(tx *Tx) { Put(tx, SSHKeys, &SSHKey{ID: id + 1, Name: "new"}) },
		"delete": func(tx *Tx) { Delete(tx, SSHKeys, id) },
		"found":  func(tx *Tx) { record, _ := Find(tx, SSHKeys, func(*SSHKey) bool { return true }); record.Name = "x" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			tx := Begin(base)
			change(tx)
			if !tx.Changed() {
				t.Error("the change counts as unchanged")
			}
		})
	}

	tx := Begin(base)
	key, _ := Get(tx, SSHKeys, id)
	Put(tx, SSHKeys, &SSHKey{ID: id, Name: key.Name, Labels: map[string]string{"team": "a"}})
	Delete(tx, SSHKeys, id+5)
	if tx.Changed() {
		t.Error("a Put of an equal record and a Delete of a missing one count as changed")
	}
}

func TestIDsListsTheMatchesSortedAndCopiesNothing(t *testing.T) {
	tx := Begin(New())
	for _, id := range []int64{3, 1, 2, 4} {
		Put(tx, Projects, &Project{ID: id, Internet: id != 2})
	}
	base := tx.Result()

	tx = Begin(base)
	ids := IDs(tx, Projects, func(project *Project) bool { return project.Internet })
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 3 || ids[2] != 4 {
		t.Errorf("IDs returned %v, want [1 3 4]", ids)
	}
	if len(tx.touched[Projects.Name()]) != 0 || tx.Changed() {
		t.Error("IDs copied a record")
	}
}

func TestATxCopiesOnlyTheRecordsItHandsOut(t *testing.T) {
	tx := Begin(New())
	first, second := NewID(tx, Projects), NewID(tx, Projects)
	Put(tx, Projects, &Project{ID: first, Name: "a"})
	Put(tx, Projects, &Project{ID: second, Name: "b"})
	base := tx.Result()

	tx = Begin(base)
	if _, err := Get(tx, Projects, first); err != nil {
		t.Fatal(err)
	}
	if len(tx.touched[Projects.Name()]) != 1 {
		t.Errorf("a Get touched the projects %v, want only %d", tx.touched[Projects.Name()], first)
	}
}

func TestReadSharesTheRecordsAndRefusesChanges(t *testing.T) {
	s := New()
	tx := Begin(s)
	Put(tx, Projects, &Project{ID: NewID(tx, Projects), Name: "default"})
	s = tx.Result()

	read := Read(s)
	if All(read, Projects)[0] != All(Read(s), Projects)[0] {
		t.Error("two reads got different copies of a record")
	}
	defer func() {
		if recover() == nil {
			t.Error("Put in a Tx that only reads did not panic")
		}
	}()
	Put(read, Projects, &Project{ID: 99, Name: "other"})
}

func TestDeepCopySharesNothing(t *testing.T) {
	finished := time.Date(2026, 11, 7, 18, 0, 0, 0, time.UTC)
	action := &Action{
		ID:        1,
		Finished:  &finished,
		Error:     &Failure{Code: "action_failed", Message: "boom"},
		Resources: []ResourceRef{{Type: "server", ID: 7}},
	}

	copied := deepCopy(action)
	*copied.Finished = finished.Add(time.Hour)
	copied.Error.Message = "changed"
	copied.Resources[0].ID = 8

	if !action.Finished.Equal(finished) || action.Error.Message != "boom" || action.Resources[0].ID != 7 {
		t.Errorf("changing the copy changed the original: %+v", action)
	}
}

func BenchmarkTxOnAThousandActions(b *testing.B) {
	tx := Begin(New())
	for range 1000 {
		id := NewID(tx, Actions)
		Put(tx, Actions, &Action{ID: id, Command: "start_server", Resources: []ResourceRef{{Type: "server", ID: 1}}})
	}
	base := tx.Result()

	for b.Loop() {
		tx := Begin(base)
		action, _ := Get(tx, Actions, FirstID)
		action.Progress = 50
		_ = tx.Result()
	}
}
