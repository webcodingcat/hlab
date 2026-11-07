package state

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
)

// Tx is one change of the state. It starts from the state as it is and copies
// each kind on first use, so that what the change does stays in the Tx until
// the lab takes its result. A Tx that is dropped leaves the state as it was.
//
// Records from a Tx are its copies: changing one in place changes the Tx, no
// Put needed. Once the Tx ends they may still be read, such as to build an
// answer, but never changed: the lab's state shares the records of a Tx it
// took (DEVELOPING.md, Concurrency).
//
// A Tx from Read copies nothing: its records are the state's own, to read only.
type Tx struct {
	base     *State
	work     State
	touched  map[string]bool // the kinds copied so far, and "next_ids" and "lab"
	readOnly bool
}

// Begin starts a change of s.
func Begin(s *State) *Tx {
	return &Tx{
		base:    s,
		work:    State{nextIDs: s.nextIDs, tables: maps.Clone(s.tables), lab: s.lab},
		touched: map[string]bool{},
	}
}

// Read starts a Tx that only reads s. It copies nothing, which makes reads
// cheap (the API's token check reads on every request); Put, Delete and NewID
// in it panic, and nothing may change its records or its Lab.
func Read(s *State) *Tx {
	return &Tx{base: s, work: *s, readOnly: true}
}

// Changed tells whether the Tx changed the state it began from. Records are
// changed in place, so it compares each part the Tx copied with the original;
// a part that is equal again counts as unchanged.
func (tx *Tx) Changed() bool {
	for name := range tx.touched {
		var equal bool
		switch name {
		case "next_ids":
			equal = maps.Equal(tx.work.nextIDs, tx.base.nextIDs)
		case "lab":
			equal = reflect.DeepEqual(tx.work.lab, tx.base.lab)
		default:
			equal = reflect.DeepEqual(tx.work.tables[name], tx.base.tables[name])
		}
		if !equal {
			return true
		}
	}
	return false
}

// mustWrite panics in a Tx from Read: a change there would change the state
// behind the lab's back.
func (tx *Tx) mustWrite() {
	if tx.readOnly {
		panic("state: a change in a Tx that only reads")
	}
}

// Result returns the state with the Tx's changes.
func (tx *Tx) Result() *State {
	result := tx.work
	return &result
}

// Lab returns the lab's own records, to read or change (only to read in a Tx
// from Read).
func (tx *Tx) Lab() *Lab {
	if !tx.readOnly && !tx.touched["lab"] {
		tx.work.lab = deepCopy(tx.work.lab)
		tx.touched["lab"] = true
	}
	return &tx.work.lab
}

// table returns the Tx's copy of a kind's table.
func table[R Record](tx *Tx, kind Kind[R]) map[int64]*R {
	if !tx.readOnly && !tx.touched[kind.name] {
		tx.work.tables[kind.name] = deepCopy(records(&tx.work, kind))
		tx.touched[kind.name] = true
	}
	return records(&tx.work, kind)
}

// NewID returns the next ID of a kind; it is never handed out again.
func NewID[R Record](tx *Tx, kind Kind[R]) int64 {
	tx.mustWrite()
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

// Find returns a record that match accepts, if there is one. Unlike All it
// does not sort, so it suits a lookup on every request (the API's token
// check); with several matches it returns any one of them.
func Find[R Record](tx *Tx, kind Kind[R], match func(*R) bool) (*R, bool) {
	for _, record := range table(tx, kind) {
		if match(record) {
			return record, true
		}
	}
	return nil, false
}

// Put adds a record or replaces the one with its ID. A new record takes its ID
// from NewID.
func Put[R Record](tx *Tx, kind Kind[R], record *R) {
	tx.mustWrite()
	table(tx, kind)[(*record).RecordID()] = record
}

// Delete removes a record; a record that is gone needs nothing.
func Delete[R Record](tx *Tx, kind Kind[R], id int64) {
	tx.mustWrite()
	delete(table(tx, kind), id)
}
