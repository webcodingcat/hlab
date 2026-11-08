// Package state holds the lab's records: the kinds, the transactions that change
// them, and the file they are saved in (loading, saving, migrations).
//
// The package does not lock: the lab owns the state and guards it with its mutex
// (package lab). Every change runs in a Tx, which works on deep copies of the
// kinds it touches, so that a failed change leaves the state as it was.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
)

// FirstID is the first ID of every kind. The catalog uses Hetzner's real IDs,
// which are far below, so a record never shares an ID with a catalog entry.
const FirstID = 1_000_000

// ErrNotFound reports a record that is not (or no longer) there.
var ErrNotFound = errors.New("not found")

// Record is a record of a kind; its ID never changes and is never reused.
type Record interface {
	RecordID() int64
}

// Owned is a record that belongs to a project; it goes with its project
// (DeleteProject).
type Owned interface {
	Record
	RecordProject() int64
}

// Named is a record whose name is unique within its project (ByName).
type Named interface {
	Owned
	RecordName() string
}

// Kind is a kind of record, such as Projects; R is its record type. Its name is
// the key in the state file and the counter of its IDs.
type Kind[R Record] struct {
	name string
}

// Name returns the kind's name in the state file ("ssh_keys").
func (k Kind[R]) Name() string { return k.name }

// The kinds of records. A new kind: DEVELOPING.md, Recipes.
var (
	Projects    = Kind[Project]{name: "projects"}
	Tokens      = Kind[Token]{name: "tokens"}
	SSHKeys     = Kind[SSHKey]{name: "ssh_keys"}
	Actions     = Kind[Action]{name: "actions"}
	Servers     = Kind[Server]{name: "servers"}
	PrimaryIPs  = Kind[PrimaryIP]{name: "primary_ips"}
	FloatingIPs = Kind[FloatingIP]{name: "floating_ips"}
	Firewalls   = Kind[Firewall]{name: "firewalls"}
	Networks    = Kind[Network]{name: "networks"}
)

// kinds lists every kind, so that loading, saving and copying reach them all.
var kinds = []kind{Projects, Tokens, SSHKeys, Actions, Servers, PrimaryIPs, FloatingIPs, Firewalls, Networks}

// kind is what the state needs of a Kind without knowing its record type.
type kind interface {
	Name() string
	emptyTable() any
	decodeTable(data []byte) (any, error)
	writeList(out *bytes.Buffer, s *State) error
	changed(tx *Tx) bool
	deleteProject(tx *Tx, projectID int64)
}

// State is the lab's records: one table per kind, the next ID per kind, and the
// lab's own records.
type State struct {
	nextIDs map[string]int64
	tables  map[string]any // kind name → map[int64]*R
	lab     Lab
	// encoded keeps each record's JSON as Encode wrote it, by kind name and
	// ID; Tx.Result drops the records a Tx touched. It is right only as long
	// as a state's records never change (DEVELOPING.md, Concurrency), and
	// states share it (Tx.Result)
	encoded map[string]map[int64][]byte
	// fileSize is the length of the file Encode wrote last, so that the
	// next one grows its buffer once
	fileSize int
}

// New returns an empty state.
func New() *State {
	s := &State{nextIDs: map[string]int64{}, tables: map[string]any{}, encoded: map[string]map[int64][]byte{}}
	for _, k := range kinds {
		s.tables[k.Name()] = k.emptyTable()
		s.encoded[k.Name()] = map[int64][]byte{}
	}
	return s
}

// records returns the table of a kind.
func records[R Record](s *State, kind Kind[R]) map[int64]*R {
	return s.tables[kind.name].(map[int64]*R)
}

func (k Kind[R]) emptyTable() any {
	return map[int64]*R{}
}

// writeList writes the records as a list sorted by ID, so that diffs of the
// state file stay readable, indented as the kind's value in the file. A
// record's JSON comes from s.encoded if it is there, and goes there if not.
func (k Kind[R]) writeList(out *bytes.Buffer, s *State) error {
	byID := records(s, k)
	if len(byID) == 0 {
		out.WriteString("[]")
		return nil
	}
	cache := s.encoded[k.name]
	out.WriteString("[")
	for i, id := range slices.Sorted(maps.Keys(byID)) {
		data, found := cache[id]
		if !found {
			var err error
			if data, err = json.MarshalIndent(byID[id], recordIndent, fileIndent); err != nil {
				return fmt.Errorf("%s %d: %w", k.name, id, err)
			}
			cache[id] = data
		}
		if i > 0 {
			out.WriteString(",")
		}
		out.WriteString("\n" + recordIndent)
		out.Write(data)
	}
	out.WriteString("\n" + listIndent + "]")
	return nil
}

// changed tells whether a Tx changed a record of the kind: it compares the
// records the Tx touched with the state's.
func (k Kind[R]) changed(tx *Tx) bool {
	before, after := records(tx.base, k), records(&tx.work, k)
	for id := range tx.touched[k.name] {
		old, was := before[id]
		current, is := after[id]
		if was != is {
			return true // added or deleted
		}
		if is && !reflect.DeepEqual(*old, *current) {
			return true
		}
	}
	return false
}

func (k Kind[R]) decodeTable(data []byte) (any, error) {
	var list []*R
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", k.name, err)
	}
	byID := make(map[int64]*R, len(list))
	for _, record := range list {
		id := (*record).RecordID()
		if _, twice := byID[id]; twice {
			return nil, fmt.Errorf("%s: the ID %d is there twice", k.name, id)
		}
		byID[id] = record
	}
	return byID, nil
}

// deleteProject removes the records of a project, if the kind's records
// belong to projects.
func (k Kind[R]) deleteProject(tx *Tx, projectID int64) {
	var zero R
	if _, owned := any(zero).(Owned); !owned {
		return // a kind of no project: copying it would be for nothing
	}
	for id, record := range table(tx, k) {
		if any(*record).(Owned).RecordProject() == projectID {
			Delete(tx, k, id)
		}
	}
}

// DeleteProject removes a project, every record that belongs to it and its
// faults. A project that is gone needs nothing.
func DeleteProject(tx *Tx, projectID int64) {
	Delete(tx, Projects, projectID)
	for _, k := range kinds {
		k.deleteProject(tx, projectID)
	}
	delete(tx.Lab().Faults, projectID)
}
