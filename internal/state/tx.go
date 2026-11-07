package state

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
)

// Tx is one change of the state. It starts from the state as it is and copies
// each record on first use, so that what the change does stays in the Tx until
// the lab takes its result. A Tx that is dropped leaves the state as it was.
//
// Records from a Tx are its copies: changing one in place changes the Tx, no
// Put needed. Once the Tx ends they may still be read, such as to build an
// answer, but never changed: the lab's state shares the records of a Tx it
// took (DEVELOPING.md, Concurrency).
//
// A Tx from Read copies nothing: its records are the state's own, to read only.
type Tx struct {
	base *State
	work State
	// touched holds, per kind, the IDs of the records the Tx copied, put or
	// deleted; a kind in it has a table of its own in work
	touched  map[string]map[int64]bool
	nextIDs  bool // work has its own next IDs
	lab      bool // work has its own Lab
	readOnly bool
}

// Begin starts a change of s.
func Begin(s *State) *Tx {
	return &Tx{
		base:    s,
		work:    State{nextIDs: s.nextIDs, tables: maps.Clone(s.tables), lab: s.lab, fileSize: s.fileSize},
		touched: map[string]map[int64]bool{},
	}
}

// Read starts a Tx that only reads s. It copies nothing, which makes reads
// cheap (the API's token check reads on every request); Put, Delete and NewID
// in it panic, and nothing may change its records or its Lab.
func Read(s *State) *Tx {
	return &Tx{base: s, work: *s, readOnly: true}
}

// Changed tells whether the Tx changed the state it began from. Records are
// changed in place, so it compares each record the Tx copied with the
// original; a record that is equal again counts as unchanged.
func (tx *Tx) Changed() bool {
	if tx.nextIDs && !maps.Equal(tx.work.nextIDs, tx.base.nextIDs) {
		return true
	}
	if tx.lab && !reflect.DeepEqual(tx.work.lab, tx.base.lab) {
		return true
	}
	for _, k := range kinds {
		if k.changed(tx) {
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

// Result returns the state with the Tx's changes. The records the Tx touched
// lose their saved JSON (Encode), so that it is encoded anew. The JSON of the
// other kinds stays shared with the state the Tx began from: both states hold
// the same records there, which never change, so whichever state Encode
// fills it for, the JSON fits both.
func (tx *Tx) Result() *State {
	result := tx.work
	result.encoded = maps.Clone(tx.base.encoded)
	for name, ids := range tx.touched {
		cache := maps.Clone(tx.base.encoded[name])
		for id := range ids {
			delete(cache, id)
		}
		result.encoded[name] = cache
	}
	return &result
}

// Lab returns the lab's own records, to read or change (only to read in a Tx
// from Read).
func (tx *Tx) Lab() *Lab {
	if !tx.readOnly && !tx.lab {
		tx.work.lab = deepCopy(tx.work.lab)
		tx.lab = true
	}
	return &tx.work.lab
}

// table returns the Tx's table of a kind. In a Tx that changes, it is a copy
// of the map whose records are still the state's own: own copies a record
// before the Tx hands it out.
func table[R Record](tx *Tx, kind Kind[R]) map[int64]*R {
	if !tx.readOnly && tx.touched[kind.name] == nil {
		tx.work.tables[kind.name] = maps.Clone(records(&tx.work, kind))
		tx.touched[kind.name] = map[int64]bool{}
	}
	return records(&tx.work, kind)
}

// own returns the Tx's copy of a record, copying it on first use; a Tx from
// Read gets the state's own record.
func own[R Record](tx *Tx, kind Kind[R], id int64) (*R, bool) {
	byID := table(tx, kind)
	record, found := byID[id]
	if !found || tx.readOnly || tx.touched[kind.name][id] {
		return record, found
	}
	record = deepCopy(record)
	byID[id] = record
	tx.touched[kind.name][id] = true
	return record, true
}

// NewID returns the next ID of a kind; it is never handed out again.
func NewID[R Record](tx *Tx, kind Kind[R]) int64 {
	tx.mustWrite()
	if !tx.nextIDs {
		tx.work.nextIDs = maps.Clone(tx.work.nextIDs)
		tx.nextIDs = true
	}
	id := max(tx.work.nextIDs[kind.name], FirstID)
	tx.work.nextIDs[kind.name] = id + 1
	return id
}

// Get returns a record by ID, or ErrNotFound.
func Get[R Record](tx *Tx, kind Kind[R], id int64) (*R, error) {
	record, found := own(tx, kind, id)
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
		record, _ := own(tx, kind, id)
		list = append(list, record)
	}
	return list
}

// Find returns a record that match accepts, if there is one. Unlike All it
// does not sort, so it suits a lookup on every request (the API's token
// check); with several matches it returns any one of them. match may get the
// state's own record, so it only reads.
func Find[R Record](tx *Tx, kind Kind[R], match func(*R) bool) (*R, bool) {
	for id, record := range table(tx, kind) {
		if match(record) {
			return own(tx, kind, id)
		}
	}
	return nil, false
}

// IDs returns the IDs of the records that match accepts, sorted. Unlike All
// it copies no record, so it suits a scan of a large kind in a Tx that changes
// only a few of its records: All copies them all, which Changed then compares
// and Encode writes anew. match may get the state's own records, so it only
// reads.
func IDs[R Record](tx *Tx, kind Kind[R], match func(*R) bool) []int64 {
	var ids []int64
	for id, record := range table(tx, kind) {
		if match(record) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// Put adds a record or replaces the one with its ID. A new record takes its ID
// from NewID.
func Put[R Record](tx *Tx, kind Kind[R], record *R) {
	tx.mustWrite()
	id := (*record).RecordID()
	table(tx, kind)[id] = record
	tx.touched[kind.name][id] = true
}

// Delete removes a record; a record that is gone needs nothing.
func Delete[R Record](tx *Tx, kind Kind[R], id int64) {
	tx.mustWrite()
	delete(table(tx, kind), id)
	tx.touched[kind.name][id] = true
}
