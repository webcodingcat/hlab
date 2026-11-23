package catalog_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
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

	t.Run("UI-04/add-remove", func(t *testing.T) {
		dataDir := t.TempDir()
		customFile := filepath.Join(t.TempDir(), "server-types.custom.json")
		if err := os.WriteFile(customFile, []byte("[]"), 0o644); err != nil {
			t.Fatal(err)
		}
		cat := loadAll(t, dataDir, customFile)
		firstID := nextFreeID(cat.Types(), catalog.MinOwnID)

		spec := catalog.OwnSpec{
			Name: "own-a", Description: "own type a", CPUType: "dedicated",
			Cores: 3, Memory: 6, Disk: 50, Locations: []string{"fsn1", "nbg1"},
		}
		st, err := catalog.AddOwn(cat, customFile, spec)
		if err != nil {
			t.Fatalf("AddOwn %s: %v", spec.Name, err)
		}
		if st.ID != firstID || st.Name != "own-a" {
			t.Errorf("AddOwn returned %s with ID %d, want own-a with ID %d", st.Name, st.ID, firstID)
		}
		own := readCustom(t, customFile)
		if len(own) != 1 {
			t.Fatalf("custom file holds %d types after the first AddOwn, want 1", len(own))
		}
		checkOwnObject(t, own[0], spec, firstID, []string{"fsn1", "nbg1"})

		cat = loadAll(t, dataDir, customFile)
		if got, ok := cat.ServerTypeByRef("own-a"); !ok || got.ID != firstID || got.Cores != 3 || got.Memory != 6 || got.Disk != 50 {
			t.Errorf("served own-a after AddOwn = %+v, %v; want ID %d, 3 cores, 6 GB, 50 GB", got, ok, firstID)
		}

		all := catalog.OwnSpec{Name: "own-b", CPUType: "shared", Cores: 1, Memory: 2, Disk: 20}
		var allLocs []string
		for _, l := range cat.Locations {
			allLocs = append(allLocs, l.Name)
		}
		secondID := nextFreeID(cat.Types(), catalog.MinOwnID)
		if st, err = catalog.AddOwn(cat, customFile, all); err != nil {
			t.Fatalf("AddOwn %s without locations: %v", all.Name, err)
		}
		if st.ID != secondID || secondID == firstID {
			t.Errorf("second AddOwn gave ID %d, want the next free ID %d", st.ID, secondID)
		}
		own = readCustom(t, customFile)
		if len(own) != 2 {
			t.Fatalf("custom file holds %d types after the second AddOwn, want 2", len(own))
		}
		checkOwnObject(t, own[1], all, secondID, allLocs)
		cat = loadAll(t, dataDir, customFile)

		refusals := []struct {
			name  string
			spec  catalog.OwnSpec
			field string
		}{
			{"catalog name taken", catalog.OwnSpec{Name: "cx23", CPUType: "shared", Cores: 2, Memory: 4, Disk: 40}, "name"},
			{"own name taken", catalog.OwnSpec{Name: "own-a", CPUType: "shared", Cores: 2, Memory: 4, Disk: 40}, "name"},
			{"empty name", catalog.OwnSpec{Name: "", CPUType: "shared", Cores: 2, Memory: 4, Disk: 40}, "name"},
			{"no cores", catalog.OwnSpec{Name: "own-c", CPUType: "shared", Cores: 0, Memory: 4, Disk: 40}, "cores"},
			{"no memory", catalog.OwnSpec{Name: "own-c", CPUType: "shared", Cores: 2, Memory: 0, Disk: 40}, "memory"},
			{"no disk", catalog.OwnSpec{Name: "own-c", CPUType: "shared", Cores: 2, Memory: 4, Disk: 0}, "disk"},
			{"unknown cpu type", catalog.OwnSpec{Name: "own-c", CPUType: "turbo", Cores: 2, Memory: 4, Disk: 40}, "cpu_type"},
			{"unknown location", catalog.OwnSpec{Name: "own-c", CPUType: "shared", Cores: 2, Memory: 4, Disk: 40,
				Locations: []string{"fsn1", "nowhere1"}}, "locations"},
		}
		for _, r := range refusals {
			t.Run(r.name, func(t *testing.T) {
				before, err := os.ReadFile(customFile)
				if err != nil {
					t.Fatal(err)
				}
				_, err = catalog.AddOwn(cat, customFile, r.spec)
				var fe *catalog.FieldError
				if !errors.As(err, &fe) || fe.Field != r.field {
					t.Errorf("AddOwn %+v = %v, want a FieldError for %q", r.spec, err, r.field)
				}
				if after, _ := os.ReadFile(customFile); !bytes.Equal(before, after) {
					t.Errorf("custom file changed by a refused AddOwn: %s", after)
				}
			})
		}

		if err := catalog.RemoveOwn(customFile, "own-a"); err != nil {
			t.Fatalf("RemoveOwn own-a: %v", err)
		}
		if got := mapNames(readCustom(t, customFile)); !slices.Equal(got, []string{"own-b"}) {
			t.Errorf("custom file after RemoveOwn own-a holds %v, want [own-b]", got)
		}
		if err := catalog.RemoveOwn(customFile, "own-a"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("RemoveOwn of an unknown name = %v, want an fs.ErrNotExist error", err)
		}
	})

	t.Run("UI-04/update", func(t *testing.T) {
		dataDir := t.TempDir()
		customFile := filepath.Join(t.TempDir(), "server-types.custom.json")
		if err := os.WriteFile(customFile, []byte("[]"), 0o644); err != nil {
			t.Fatal(err)
		}
		cat := loadAll(t, dataDir, customFile)
		a := catalog.OwnSpec{Name: "own-a", Description: "own type a", CPUType: "shared",
			Cores: 2, Memory: 4, Disk: 40, Locations: []string{"fsn1"}}
		stA, err := catalog.AddOwn(cat, customFile, a)
		if err != nil {
			t.Fatalf("AddOwn own-a: %v", err)
		}
		cat = loadAll(t, dataDir, customFile)
		if _, err := catalog.AddOwn(cat, customFile, catalog.OwnSpec{Name: "own-b", CPUType: "shared", Cores: 1, Memory: 2, Disk: 20}); err != nil {
			t.Fatalf("AddOwn own-b: %v", err)
		}
		ownB := readCustom(t, customFile)[1]
		cat = loadAll(t, dataDir, customFile)

		changed := catalog.OwnSpec{Name: "own-a", Description: "own type a, bigger", CPUType: "dedicated",
			Cores: 4, Memory: 8, Disk: 80, Locations: []string{"nbg1", "hel1"}}
		st, err := catalog.UpdateOwn(cat, customFile, changed)
		if err != nil {
			t.Fatalf("UpdateOwn own-a: %v", err)
		}
		if st.ID != stA.ID || st.Name != "own-a" {
			t.Errorf("UpdateOwn returned %s with ID %d, want own-a with its ID %d", st.Name, st.ID, stA.ID)
		}
		own := readCustom(t, customFile)
		if got := mapNames(own); len(own) != 2 || own[0]["name"] != "own-a" || own[1]["name"] != "own-b" {
			t.Fatalf("custom file after UpdateOwn holds %v, want own-a then own-b", got)
		}
		checkOwnObject(t, own[0], changed, stA.ID, changed.Locations)
		if !reflect.DeepEqual(own[1], ownB) {
			t.Errorf("own-b changed by UpdateOwn own-a: %v, want %v", own[1], ownB)
		}
		cat = loadAll(t, dataDir, customFile)
		if got, ok := cat.ServerTypeByRef("own-a"); !ok || got.ID != stA.ID || got.Cores != 4 || got.Memory != 8 || got.Disk != 80 {
			t.Errorf("served own-a after UpdateOwn = %+v, %v; want ID %d, 4 cores, 8 GB, 80 GB", got, ok, stA.ID)
		}

		refusals := []struct {
			name     string
			spec     catalog.OwnSpec
			field    string
			notExist bool
		}{
			{"no cores", catalog.OwnSpec{Name: "own-a", CPUType: "shared", Cores: 0, Memory: 4, Disk: 40}, "cores", false},
			{"no memory", catalog.OwnSpec{Name: "own-a", CPUType: "shared", Cores: 2, Memory: 0, Disk: 40}, "memory", false},
			{"no disk", catalog.OwnSpec{Name: "own-a", CPUType: "shared", Cores: 2, Memory: 4, Disk: 0}, "disk", false},
			{"unknown cpu type", catalog.OwnSpec{Name: "own-a", CPUType: "turbo", Cores: 2, Memory: 4, Disk: 40}, "cpu_type", false},
			{"unknown location", catalog.OwnSpec{Name: "own-a", CPUType: "shared", Cores: 2, Memory: 4, Disk: 40,
				Locations: []string{"fsn1", "nowhere1"}}, "locations", false},
			{"unknown name", catalog.OwnSpec{Name: "own-x", CPUType: "shared", Cores: 2, Memory: 4, Disk: 40}, "", true},
		}
		for _, r := range refusals {
			t.Run(r.name, func(t *testing.T) {
				before, err := os.ReadFile(customFile)
				if err != nil {
					t.Fatal(err)
				}
				_, err = catalog.UpdateOwn(cat, customFile, r.spec)
				if r.notExist {
					if !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("UpdateOwn of an unknown name = %v, want an fs.ErrNotExist error", err)
					}
				} else {
					var fe *catalog.FieldError
					if !errors.As(err, &fe) || fe.Field != r.field {
						t.Errorf("UpdateOwn %+v = %v, want a FieldError for %q", r.spec, err, r.field)
					}
				}
				if after, _ := os.ReadFile(customFile); !bytes.Equal(before, after) {
					t.Errorf("custom file changed by a refused UpdateOwn: %s", after)
				}
			})
		}
	})

	t.Run("UI-04/reload", func(t *testing.T) {
		dataDir := t.TempDir()
		customFile := filepath.Join(t.TempDir(), "server-types.custom.json")
		writeJSON(t, customFile, []any{ownType(100001, "own-a")})
		cat := loadAll(t, dataDir, customFile)

		check := func(t *testing.T, step string, wantOwn []string, wantWarning bool) {
			t.Helper()
			if got := typeNames(cat.Own()); !slices.Equal(got, wantOwn) {
				t.Errorf("%s: Own() = %v, want %v", step, got, wantOwn)
			}
			names := typeNames(cat.Types())
			for _, n := range []string{"own-a", "own-b"} {
				if slices.Contains(names, n) != slices.Contains(wantOwn, n) {
					t.Errorf("%s: Types() serves %s = %v, want %v", step, n, slices.Contains(names, n), slices.Contains(wantOwn, n))
				}
			}
			if w := cat.OwnTypesWarning(); (w != "") != wantWarning {
				t.Errorf("%s: OwnTypesWarning() = %q, want a warning: %v", step, w, wantWarning)
			}
		}

		writeJSON(t, customFile, []any{ownType(100001, "own-a"), ownType(100002, "own-b")})
		cat.ReloadOwn(customFile)
		check(t, "changed file", []string{"own-a", "own-b"}, false)

		if err := os.WriteFile(customFile, []byte("[{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		cat.ReloadOwn(customFile)
		check(t, "invalid file", nil, true)

		writeJSON(t, customFile, []any{ownType(100001, "own-a")})
		cat.ReloadOwn(customFile)
		check(t, "valid file after the invalid one", []string{"own-a"}, false)

		writeJSON(t, customFile, []any{ownType(100001, "own-a"), ownType(100003, "cx23")})
		cat.ReloadOwn(customFile)
		check(t, "colliding file", nil, true)
		if st, ok := cat.ServerType("cx23"); !ok || st.ID == 100003 {
			t.Errorf("cx23 after a colliding own type = %+v, %v; want the catalog type", st, ok)
		}

		writeJSON(t, customFile, []any{ownType(100002, "own-b")})
		cat.ReloadOwn(customFile)
		check(t, "valid file after the colliding one", []string{"own-b"}, false)
	})

	t.Run("SBOX-01", testStorageBoxTypes)
	t.Run("SBOX-21", testOwnStorageBoxTypes)
}

// loadAll loads the shipped catalog with dataDir and customFile and fails
// on an error or a warning.
func loadAll(t *testing.T, dataDir, customFile string) *catalog.Catalog {
	t.Helper()
	cat, warns, err := catalog.LoadAll(shippedDir, dataDir, customFile)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("LoadAll warnings %q, want none", warns)
	}
	return cat
}

// nextFreeID is the smallest ID from minID that no type in types has.
func nextFreeID(types []catalog.ServerType, minID int64) int64 {
	id := minID
	for slices.ContainsFunc(types, func(st catalog.ServerType) bool { return st.ID == id }) {
		id++
	}
	return id
}

// readCustom reads the own server types of customFile.
func readCustom(t *testing.T, customFile string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(customFile)
	if err != nil {
		t.Fatalf("read custom file: %v", err)
	}
	var own []map[string]any
	if err := json.Unmarshal(data, &own); err != nil {
		t.Fatalf("custom file is not a JSON array of objects: %v\n%s", err, data)
	}
	return own
}

// checkOwnObject checks that obj is the Hetzner server_type object AddOwn
// builds for spec: ID id, x86, local storage, zero prices and the
// locations locs, each available.
func checkOwnObject(t *testing.T, obj map[string]any, spec catalog.OwnSpec, id int64, locs []string) {
	t.Helper()
	want := map[string]any{
		"id": float64(id), "name": spec.Name, "cores": float64(spec.Cores), "memory": spec.Memory,
		"disk": float64(spec.Disk), "cpu_type": spec.CPUType, "architecture": "x86", "storage_type": "local",
	}
	if spec.Description != "" {
		want["description"] = spec.Description
	}
	for k, v := range want {
		if obj[k] != v {
			t.Errorf("own type %s: %s = %v, want %v", spec.Name, k, obj[k], v)
		}
	}
	rawLocs, _ := obj["locations"].([]any)
	var got []string
	for _, l := range rawLocs {
		m, _ := l.(map[string]any)
		name, _ := m["name"].(string)
		got = append(got, name)
		if m["available"] != true {
			t.Errorf("own type %s: location %s not available", spec.Name, name)
		}
	}
	slices.Sort(got)
	wantLocs := slices.Clone(locs)
	slices.Sort(wantLocs)
	if !slices.Equal(got, wantLocs) {
		t.Errorf("own type %s: locations %v, want %v", spec.Name, got, wantLocs)
	}
	prices, ok := obj["prices"].([]any)
	if !ok {
		t.Errorf("own type %s: prices %v, want a list", spec.Name, obj["prices"])
	}
	for _, p := range prices {
		entry, _ := p.(map[string]any)
		for key, v := range entry {
			amount, isAmount := v.(map[string]any)
			if !isAmount {
				continue
			}
			for _, part := range []string{"net", "gross"} {
				s, _ := amount[part].(string)
				if f, err := strconv.ParseFloat(s, 64); err != nil || f != 0 {
					t.Errorf("own type %s: price %s.%s = %v, want zero", spec.Name, key, part, amount[part])
				}
			}
		}
	}
}

// boxType returns a storage box type object in the shape of the Hetzner
// API with a price for each of locs.
func boxType(id int64, name string, size int64, snapshots any, subaccounts int, locs ...string) map[string]any {
	prices := []any{}
	for _, l := range locs {
		prices = append(prices, map[string]any{
			"location":      l,
			"price_hourly":  map[string]any{"net": "0.0050", "gross": "0.0060"},
			"price_monthly": map[string]any{"net": "3.2000", "gross": "3.8080"},
			"setup_fee":     map[string]any{"net": "0.0000", "gross": "0.0000"},
		})
	}
	return map[string]any{
		"id":                       id,
		"name":                     name,
		"description":              "Storage Box " + name,
		"snapshot_limit":           snapshots,
		"automatic_snapshot_limit": snapshots,
		"subaccounts_limit":        subaccounts,
		"size":                     size,
		"prices":                   prices,
		"deprecation":              nil,
	}
}

// copyShipped copies the shipped catalog files except
// storage-box-types.json into a new folder.
func copyShipped(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(shippedDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "storage-box-types.json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(shippedDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func testStorageBoxTypes(t *testing.T) {
	t.Run("without the file", func(t *testing.T) {
		cat, err := catalog.Load(copyShipped(t))
		if err != nil {
			t.Fatalf("Load without storage-box-types.json: %v", err)
		}
		if len(cat.StorageBoxTypes()) != 0 {
			t.Errorf("StorageBoxTypes = %+v, want none", cat.StorageBoxTypes())
		}
	})

	t.Run("from the file", func(t *testing.T) {
		dir := copyShipped(t)
		objects := []map[string]any{
			boxType(1333, "bx11", 1099511627776, 10, 100, "fsn1", "hel1"),
			boxType(1334, "bx21", 5497558138880, nil, 200, "fsn1", "nbg1", "hel1"),
		}
		writeJSON(t, filepath.Join(dir, "storage-box-types.json"), map[string]any{"storage_box_types": objects})

		cat, err := catalog.Load(dir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(cat.StorageBoxTypes()) != len(objects) {
			t.Fatalf("StorageBoxTypes has %d types, want %d", len(cat.StorageBoxTypes()), len(objects))
		}
		ten := 10
		want := []catalog.StorageBoxType{
			{ID: 1333, Name: "bx11", Size: 1099511627776, SnapshotLimit: &ten, AutomaticSnapshotLimit: &ten,
				SubaccountsLimit: 100, Locations: []string{"fsn1", "hel1"}},
			{ID: 1334, Name: "bx21", Size: 5497558138880, SubaccountsLimit: 200, Locations: []string{"fsn1", "nbg1", "hel1"}},
		}
		for i, got := range cat.StorageBoxTypes() {
			raw := got.Raw
			got.Raw = nil
			if !reflect.DeepEqual(got, want[i]) {
				t.Errorf("StorageBoxTypes[%d] = %+v, want %+v", i, got, want[i])
			}
			var gotObj, wantObj any
			wantRaw, _ := json.Marshal(objects[i])
			if err := json.Unmarshal(raw, &gotObj); err != nil {
				t.Errorf("StorageBoxTypes[%d].Raw = %q: %v", i, raw, err)
				continue
			}
			_ = json.Unmarshal(wantRaw, &wantObj)
			if !reflect.DeepEqual(gotObj, wantObj) {
				t.Errorf("StorageBoxTypes[%d].Raw = %s, want the object of the file %s", i, raw, wantRaw)
			}
		}
	})
}
