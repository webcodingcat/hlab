package state

import (
	"fmt"
	"maps"
	"slices"
)

// Tx is one change of the state. It starts from the state as it is and copies
// each kind on first use, so that what the change does stays in the Tx until
// the lab takes its result. A Tx that is dropped leaves the state as it was.
//
// Records from a Tx belong to it: they are copies, and they must not be kept
// once the Tx ends (DEVELOPING.md, Concurrency).
type Tx struct {
	work    State
	touched map[string]bool // the kinds copied so far, and "next_ids" and "lab"
}

// Begin starts a change of s.
func Begin(s *State) *Tx {
	return &Tx{
		work:    State{nextIDs: s.nextIDs, tables: maps.Clone(s.tables), lab: s.lab},
		touched: map[string]bool{},
	}
}

// Touched tells whether the Tx copied any part of the state; one that did not
// has nothing to save. A Tx that only read counts as touched too: telling a
// read from a change would cost more than saving once more.
func (tx *Tx) Touched() bool {
	return len(tx.touched) > 0
}

// Result returns the state with the Tx's changes.
func (tx *Tx) Result() *State {
	result := tx.work
	return &result
}

// Lab returns the lab's own records, to read or change.
func (tx *Tx) Lab() *Lab {
	if !tx.touched["lab"] {
		tx.work.lab = deepCopy(tx.work.lab)
		tx.touched["lab"] = true
	}
	return &tx.work.lab
}

// table returns the Tx's copy of a kind's table.
func table[R Record](tx *Tx, kind Kind[R]) map[int64]*R {
	if !tx.touched[kind.name] {
		tx.work.tables[kind.name] = deepCopy(records(&tx.work, kind))
		tx.touched[kind.name] = true
	}
	return records(&tx.work, kind)
}

// NewID returns the next ID of a kind; it is never handed out again.
func NewID[R Record](tx *Tx, kind Kind[R]) int64 {
	if !tx.touched["next_ids"] {
		tx.work.nextIDs = maps.Clone(tx.work.nextIDs)
		tx.touched["next_ids"] = true
	}
	id := max(tx.work.nextIDs[kind.name], FirstID)
	tx.work.nextIDs[kind.name] = id + 1
	return id
}

// Get returns a record by ID, or ErrNotFound.
func Get[R Record](tx *Tx, kind Kind[R], id int64) (*R, error) {
	record, found := table(tx, kind)[id]
	if !found {
		return nil, fmt.Errorf("%s %d: %w", kind.name, id, ErrNotFound)
	}
	return record, nil
}

// All returns every record of a kind, sorted by ID.
func All[R Record](tx *Tx, kind Kind[R]) []*R {
	byID := table(tx, kind)
	list := make([]*R, 0, len(byID))
	for _, id := range slices.Sorted(maps.Keys(byID)) {
		list = append(list, byID[id])
	}
	return list
}

// Put adds a record or replaces the one with its ID. A new record takes its ID
// from NewID.
func Put[R Record](tx *Tx, kind Kind[R], record *R) {
	table(tx, kind)[(*record).RecordID()] = record
}

// Delete removes a record; a record that is gone needs nothing.
func Delete[R Record](tx *Tx, kind Kind[R], id int64) {
	delete(table(tx, kind), id)
}
