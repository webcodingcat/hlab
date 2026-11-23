package catalog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/catalog"
)

// boxTypeNames are the names of types in their order.
func boxTypeNames(types []catalog.StorageBoxType) []string {
	var names []string
	for _, st := range types {
		names = append(names, st.Name)
	}
	return names
}

// shippedBoxTypes are the shipped storage box types in the order of the
// file.
func shippedBoxTypes(t *testing.T) []catalog.StorageBoxType {
	t.Helper()
	cat, err := catalog.Load(shippedDir)
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	types := cat.StorageBoxTypes()
	if len(types) == 0 {
		t.Fatal("shipped catalog has no storage box types")
	}
	return types
}

// testOwnStorageBoxTypes runs the tests of storage-box-types.custom.json.
func testOwnStorageBoxTypes(t *testing.T) {
	t.Run("own", func(t *testing.T) {
		catalogNames := boxTypeNames(shippedBoxTypes(t))
		cat, err := catalog.Load(shippedDir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		file := filepath.Join(t.TempDir(), "storage-box-types.custom.json")
		own := []map[string]any{
			boxType(catalog.MinOwnID, "own-64m", 64<<20, 2, 3, "fsn1"),
			boxType(catalog.MinOwnID+1, "own-128m", 128<<20, nil, 5, "fsn1", "nbg1"),
		}
		writeJSON(t, file, own)

		cat.ReloadOwnStorageBoxTypes(file)
		if w := cat.OwnStorageBoxTypesWarning(); w != "" {
			t.Fatalf("warning %q for a valid own file, want none", w)
		}
		types := cat.StorageBoxTypes()
		want := append(slices.Clone(catalogNames), "own-64m", "own-128m")
		if got := boxTypeNames(types); !slices.Equal(got, want) {
			t.Fatalf("storage box types %v, want the catalog's followed by the own ones %v", got, want)
		}

		two := 2
		wantOwn := []catalog.StorageBoxType{
			{ID: catalog.MinOwnID, Name: "own-64m", Size: 64 << 20, SnapshotLimit: &two, AutomaticSnapshotLimit: &two,
				SubaccountsLimit: 3, Locations: []string{"fsn1"}},
			{ID: catalog.MinOwnID + 1, Name: "own-128m", Size: 128 << 20, SubaccountsLimit: 5, Locations: []string{"fsn1", "nbg1"}},
		}
		for i, got := range types[len(catalogNames):] {
			raw := got.Raw
			got.Raw = nil
			if !reflect.DeepEqual(got, wantOwn[i]) {
				t.Errorf("own type %d = %+v, want %+v", i, got, wantOwn[i])
			}
			var gotObj, wantObj any
			if err := json.Unmarshal(raw, &gotObj); err != nil {
				t.Errorf("own type %d: Raw %q: %v", i, raw, err)
				continue
			}
			wantRaw, _ := json.Marshal(own[i])
			_ = json.Unmarshal(wantRaw, &wantObj)
			if !reflect.DeepEqual(gotObj, wantObj) {
				t.Errorf("own type %d: Raw %s, want the object of the file %s", i, raw, wantRaw)
			}
		}
	})

	t.Run("invalid", func(t *testing.T) {
		shipped := shippedBoxTypes(t)
		catalogNames := boxTypeNames(shipped)
		cases := []struct {
			name    string
			content any // written as JSON; a string is written as it is
		}{
			{"bad JSON", "[{not json"},
			{"ID collision", []any{boxType(shipped[0].ID, "own-other", 64<<20, 1, 1, "fsn1")}},
			{"name collision", []any{boxType(catalog.MinOwnID+5, shipped[0].Name, 64<<20, 1, 1, "fsn1")}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				cat, err := catalog.Load(shippedDir)
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				file := filepath.Join(t.TempDir(), "storage-box-types.custom.json")
				writeJSON(t, file, []any{boxType(catalog.MinOwnID, "own-valid", 64<<20, 1, 1, "fsn1")})
				cat.ReloadOwnStorageBoxTypes(file)
				if got := boxTypeNames(cat.StorageBoxTypes()); !slices.Contains(got, "own-valid") {
					t.Fatalf("storage box types %v after a valid own file, want own-valid among them", got)
				}

				if s, ok := c.content.(string); ok {
					if err := os.WriteFile(file, []byte(s), 0o644); err != nil {
						t.Fatal(err)
					}
				} else {
					writeJSON(t, file, c.content)
				}
				cat.ReloadOwnStorageBoxTypes(file)
				if w := cat.OwnStorageBoxTypesWarning(); w == "" {
					t.Error("no warning for the invalid own file")
				}
				if got := boxTypeNames(cat.StorageBoxTypes()); !slices.Equal(got, catalogNames) {
					t.Errorf("storage box types %v, want only the catalog's %v", got, catalogNames)
				}
			})
		}
	})
}
