package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// currentVersion is the version of the state file this code writes. A change
// of the format raises it and adds a migration (DEVELOPING.md, Recipes).
var currentVersion = 1

// Migration lifts a state file from its version to the next one. It works on
// the file as plain JSON values: objects are map[string]any, lists []any,
// numbers json.Number.
type Migration func(raw map[string]any) error

// migrations lifts version n to n+1, by n.
var migrations = map[int]Migration{}

// ErrNewerVersion reports a state file of a newer HLab: saving it would lose
// what this code does not know, so the lab does not start.
var ErrNewerVersion = errors.New("the state file is newer than this HLab")

// file is the state file's layout. decode reads it; Encode writes the same
// by hand.
type file struct {
	Version int                        `json:"version"`
	Saved   time.Time                  `json:"saved"`
	NextIDs map[string]int64           `json:"next_ids"`
	Kinds   map[string]json.RawMessage `json:"kinds"`
	Lab     Lab                        `json:"lab"`
}

// The indents of the state file, as json.MarshalIndent writes it: two spaces
// per level; a kind's name and the end of its list on level 2, its records on
// level 3.
const (
	fileIndent   = "  "
	listIndent   = "    "
	recordIndent = "      "
)

// Encode returns the state file's content for s, saved at the given time.
//
// It writes what json.MarshalIndent writes for a file, but by hand: each
// record's JSON stays in s (State.encoded) and is written again as it is, so
// a change of one action does not encode the thousands of others. Encode
// fills s.encoded, so it runs under the same lock as the changes of s.
func Encode(s *State, saved time.Time) ([]byte, error) {
	var out bytes.Buffer
	out.Grow(s.fileSize + 4096) // a little more than last time: a change rarely adds more
	out.WriteString("{\n")
	head := []struct {
		name  string
		value any
	}{
		{"version", currentVersion},
		{"saved", saved.UTC()},
		{"next_ids", s.nextIDs},
	}
	for _, field := range head {
		if err := writeField(&out, field.name, field.value); err != nil {
			return nil, err
		}
		out.WriteString(",\n")
	}
	out.WriteString(fileIndent + `"kinds": {`)
	byName := slices.SortedFunc(slices.Values(kinds), func(a, b kind) int {
		return strings.Compare(a.Name(), b.Name()) // as MarshalIndent sorts a map
	})
	for i, k := range byName {
		if i > 0 {
			out.WriteString(",")
		}
		fmt.Fprintf(&out, "\n%s%q: ", listIndent, k.Name())
		if err := k.writeList(&out, s); err != nil {
			return nil, err
		}
	}
	out.WriteString("\n" + fileIndent + "},\n")
	if err := writeField(&out, "lab", s.lab); err != nil {
		return nil, err
	}
	out.WriteString("\n}")
	s.fileSize = out.Len()
	return out.Bytes(), nil
}

// writeField writes one field of the file's top level, without the comma.
func writeField(out *bytes.Buffer, name string, value any) error {
	data, err := json.MarshalIndent(value, fileIndent, fileIndent)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	fmt.Fprintf(out, "%s%q: %s", fileIndent, name, data)
	return nil
}

// Load reads the state file at path; without one the state is empty. An older
// file is migrated: it stays as path.v<version> next to the new one, which the
// lab saves later.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	raw, version, err := readRaw(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if version > currentVersion {
		return nil, fmt.Errorf("%s has version %d, this HLab knows up to %d: %w", path, version, currentVersion, ErrNewerVersion)
	}
	if version < currentVersion {
		if err := writeFile(path+".v"+strconv.Itoa(version), data); err != nil {
			return nil, fmt.Errorf("keeping the old state file: %w", err)
		}
		if data, err = migrate(raw, version); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	s, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// readRaw reads a state file as plain JSON values, and its version.
func readRaw(data []byte) (map[string]any, int, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber() // IDs stay exact, whatever their size
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil {
		return nil, 0, err
	}
	number, ok := raw["version"].(json.Number)
	if !ok {
		return nil, 0, errors.New("the state file has no version")
	}
	version, err := strconv.Atoi(number.String())
	if err != nil || version < 1 {
		return nil, 0, fmt.Errorf("the state file has the version %q", number)
	}
	return raw, version, nil
}

// migrate lifts a state file from version to currentVersion and returns it as
// JSON.
func migrate(raw map[string]any, version int) ([]byte, error) {
	for from := version; from < currentVersion; from++ {
		migration, found := migrations[from]
		if !found {
			return nil, fmt.Errorf("no migration from version %d", from)
		}
		if err := migration(raw); err != nil {
			return nil, fmt.Errorf("migrating from version %d: %w", from, err)
		}
		raw["version"] = json.Number(strconv.Itoa(from + 1))
	}
	return json.Marshal(raw)
}

// decode builds the state from a state file of the current version. A kind
// this code does not know is an error: saving would drop it.
func decode(data []byte) (*State, error) {
	var in file
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, err
	}
	s := New()
	if in.NextIDs != nil {
		s.nextIDs = in.NextIDs
	}
	s.lab = in.Lab
	known := map[string]kind{}
	for _, k := range kinds {
		known[k.Name()] = k
	}
	for name, list := range in.Kinds {
		k, found := known[name]
		if !found {
			return nil, fmt.Errorf("unknown kind %q", name)
		}
		table, err := k.decodeTable(list)
		if err != nil {
			return nil, err
		}
		s.tables[name] = table
	}
	return s, nil
}
