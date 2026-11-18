package catalog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/webcodingcat/hlab/internal/catalog"
)

// shippedDir is the catalog folder shipped with the program.
const shippedDir = "../../catalog"

// shippedTypes returns the server type objects of the shipped catalog file.
func shippedTypes(t *testing.T) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(shippedDir, "server-types.json"))
	if err != nil {
		t.Fatalf("read shipped server types: %v", err)
	}
	var file struct {
		ServerTypes []map[string]any `json:"server_types"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("decode shipped server types: %v", err)
	}
	if len(file.ServerTypes) == 0 {
		t.Fatal("shipped catalog has no server types")
	}
	return file.ServerTypes
}

// pick returns copies of the shipped server types with the given names.
func pick(t *testing.T, names ...string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, st := range shippedTypes(t) {
		if slices.Contains(names, st["name"].(string)) {
			out = append(out, st)
		}
	}
	if len(out) != len(names) {
		t.Fatalf("shipped catalog lacks one of %v", names)
	}
	return out
}

// ownType returns an own server type without prices, available in fsn1 and
// deprecated in nbg1.
func ownType(id int64, name string) map[string]any {
	return map[string]any{
		"id":           id,
		"name":         name,
		"description":  "own type " + name,
		"architecture": "x86",
		"cores":        2,
		"cpu_type":     "shared",
		"category":     "regular_purpose",
		"memory":       4,
		"disk":         40,
		"storage_type": "local",
		"deprecation":  nil,
		"prices":       []any{},
		"locations": []any{
			map[string]any{"id": 1, "name": "fsn1", "available": true, "recommended": false, "deprecation": nil},
			map[string]any{"id": 2, "name": "nbg1", "available": false, "recommended": false, "deprecation": map[string]any{
				"announced":         "2026-01-01T00:00:00Z",
				"unavailable_after": "2026-02-01T00:00:00Z",
			}},
		},
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeDataCatalog writes the data-folder server type file of dataDir.
func writeDataCatalog(t *testing.T, dataDir string, types []map[string]any) {
	t.Helper()
	writeJSON(t, filepath.Join(dataDir, "catalog", "server-types.json"), map[string]any{"server_types": types})
}

func typeNames(types []catalog.ServerType) []string {
	var names []string
	for _, st := range types {
		names = append(names, st.Name)
	}
	slices.Sort(names)
	return names
}

func mapNames(types []map[string]any) []string {
	var names []string
	for _, st := range types {
		names = append(names, st["name"].(string))
	}
	slices.Sort(names)
	return names
}

func TestCatalog(t *testing.T) {
	t.Run("CAT-02", func(t *testing.T) {
		shipped := mapNames(shippedTypes(t))
		dataTypes := pick(t, "cpx11", "cx23")
		bigID := pick(t, "cx33")[0]
		bigID["id"] = 100005 // a catalog type in the own ID range
		dataWithBigID := append(pick(t, "cpx11"), bigID)

		cases := []struct {
			name      string
			data      []map[string]any // nil: no data-folder file
			custom    any              // nil: no custom file; string: raw content
			want      []string
			wantOwn   bool
			wantWarns bool
		}{
			{name: "shipped only", want: shipped},
			{name: "data folder replaces shipped", data: dataTypes, want: []string{"cpx11", "cx23"}},
			{name: "own types added", data: dataTypes,
				custom: []any{ownType(100001, "own-a")}, want: []string{"cpx11", "cx23", "own-a"}, wantOwn: true},
			{name: "own types added to shipped",
				custom: []any{ownType(100001, "own-a")}, want: append(slices.Clone(shipped), "own-a"), wantOwn: true},
			{name: "unreadable custom file", data: dataTypes,
				custom: "[{not json", want: []string{"cpx11", "cx23"}, wantWarns: true},
			{name: "own ID below 100000", data: dataTypes,
				custom: []any{ownType(99999, "own-a")}, want: []string{"cpx11", "cx23"}, wantWarns: true},
			{name: "own name collides", data: dataTypes,
				custom: []any{ownType(100001, "cx23")}, want: []string{"cpx11", "cx23"}, wantWarns: true},
			{name: "own ID collides", data: dataWithBigID,
				custom: []any{ownType(100005, "own-a")}, want: []string{"cpx11", "cx33"}, wantWarns: true},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				dataDir := t.TempDir()
				customFile := filepath.Join(t.TempDir(), "server-types.custom.json")
				if c.data != nil {
					writeDataCatalog(t, dataDir, c.data)
				}
				switch v := c.custom.(type) {
				case nil:
				case string:
					if err := os.WriteFile(customFile, []byte(v), 0o644); err != nil {
						t.Fatal(err)
					}
				default:
					writeJSON(t, customFile, v)
				}

				cat, warns, err := catalog.LoadAll(shippedDir, dataDir, customFile)
				if err != nil {
					t.Fatalf("LoadAll: %v", err)
				}
				if c.wantWarns && len(warns) == 0 {
					t.Errorf("LoadAll gave no warning, want one")
				}
				if !c.wantWarns && len(warns) != 0 {
					t.Errorf("LoadAll warnings %q, want none", warns)
				}
				want := slices.Clone(c.want)
				slices.Sort(want)
				if got := typeNames(cat.Types()); !slices.Equal(got, want) {
					t.Errorf("Types() = %v, want %v", got, want)
				}
				if !c.wantOwn {
					return
				}

				for _, ref := range []string{"own-a", strconv.Itoa(100001)} {
					st, ok := cat.ServerTypeByRef(ref)
					if !ok || st.ID != 100001 || st.Name != "own-a" {
						t.Errorf("ServerTypeByRef(%q) = %+v, %v; want own-a with ID 100001", ref, st, ok)
					}
				}
				st, _ := cat.ServerTypeByRef("own-a")
				var raw struct {
					Locations []struct {
						Name        string          `json:"name"`
						Available   bool            `json:"available"`
						Deprecation json.RawMessage `json:"deprecation"`
					} `json:"locations"`
				}
				if err := json.Unmarshal(st.Raw, &raw); err != nil {
					t.Fatalf("decode own type: %v", err)
				}
				byName := map[string]int{}
				for i, l := range raw.Locations {
					byName[l.Name] = i
				}
				fsn1, okF := byName["fsn1"]
				nbg1, okN := byName["nbg1"]
				if !okF || !okN || len(raw.Locations) != 2 {
					t.Fatalf("own type locations = %s, want fsn1 and nbg1", st.Raw)
				}
				if l := raw.Locations[fsn1]; !l.Available || (len(l.Deprecation) != 0 && string(l.Deprecation) != "null") {
					t.Errorf("own type in fsn1 = %+v, want available without deprecation", l)
				}
				var dep struct {
					UnavailableAfter string `json:"unavailable_after"`
				}
				if l := raw.Locations[nbg1]; l.Available || json.Unmarshal(l.Deprecation, &dep) != nil || dep.UnavailableAfter != "2026-02-01T00:00:00Z" {
					t.Errorf("own type in nbg1 = available %v, deprecation %s; want unavailable with its deprecation kept", l.Available, l.Deprecation)
				}
			})
		}
	})

	t.Run("CAT-03", testUpdate)
}
