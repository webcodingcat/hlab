package state

import "testing"

func TestDeleteProjectRemovesWhatBelongsToIt(t *testing.T) {
	tx := Begin(New())
	gone, kept := NewID(tx, Projects), NewID(tx, Projects)
	for _, project := range []int64{gone, kept} {
		Put(tx, Projects, &Project{ID: project})
		Put(tx, Tokens, &Token{ID: NewID(tx, Tokens), ProjectID: project})
		Put(tx, SSHKeys, &SSHKey{ID: NewID(tx, SSHKeys), ProjectID: project})
		Put(tx, Actions, &Action{ID: NewID(tx, Actions), ProjectID: project})
	}
	tx.Lab().Faults = map[int64]ProjectFaults{gone: {}, kept: {}}

	DeleteProject(tx, gone)

	if _, err := Get(tx, Projects, gone); err == nil {
		t.Error("the project is still there")
	}
	if _, err := Get(tx, Projects, kept); err != nil {
		t.Errorf("the other project went: %v", err)
	}
	for name, owners := range map[string][]int64{
		"tokens":   owners(All(tx, Tokens)),
		"ssh keys": owners(All(tx, SSHKeys)),
		"actions":  owners(All(tx, Actions)),
	} {
		if len(owners) != 1 || owners[0] != kept {
			t.Errorf("the %s belong to %v, want only %d", name, owners, kept)
		}
	}
	if _, found := tx.Lab().Faults[gone]; found {
		t.Error("the project's faults are still there")
	}
	if _, found := tx.Lab().Faults[kept]; !found {
		t.Error("the other project's faults went")
	}
}

// owners returns the projects of records, in their order.
func owners[R Owned](records []*R) []int64 {
	var projects []int64
	for _, record := range records {
		projects = append(projects, (*record).RecordProject())
	}
	return projects
}
