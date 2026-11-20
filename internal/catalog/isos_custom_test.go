package catalog_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/catalog"
)

// ownISOEntry is an entry of isos.custom.json as the tray writes it.
func ownISOEntry(id int64, description, arch string, src catalog.ISOSource) map[string]any {
	return map[string]any{
		"id":           id,
		"name":         nil,
		"description":  description,
		"type":         "private",
		"deprecation":  nil,
		"architecture": arch,
		"_hlab": map[string]any{
			"name":   catalog.OwnISOCacheName(id, src),
			"source": src,
		},
	}
}

// shippedCatalog loads the shipped catalog; it has catalog ISOs.
func shippedCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := catalog.Load(shippedDir)
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	if len(cat.ISOs) == 0 {
		t.Fatal("shipped catalog has no ISOs")
	}
	return cat
}

// localISO writes a small .iso file and returns its path.
func localISO(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x42}, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func ownIDs(isos []catalog.ISO) []int64 {
	var ids []int64
	for _, iso := range isos {
		ids = append(ids, iso.ID)
	}
	return ids
}

// names reports whether warning w names the own ISO id with description desc.
func names(w string, id int64, desc string) bool {
	return strings.Contains(w, desc) || strings.Contains(w, strconv.FormatInt(id, 10))
}

// readOwnISOs reads the entries of the own ISOs file.
func readOwnISOs(t *testing.T, file string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read own ISOs file: %v", err)
	}
	var list []map[string]any
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("own ISOs file is not a JSON array of objects: %v\n%s", err, data)
	}
	return list
}

func entryByID(list []map[string]any, id int64) map[string]any {
	for _, e := range list {
		if e["id"] == float64(id) {
			return e
		}
	}
	return nil
}

var cacheNameRe = regexp.MustCompile(`^own-(\d+)-[0-9a-f]{8}\.iso$`)

func TestOwnISOs(t *testing.T) {
	t.Run("CAT-10_served", func(t *testing.T) {
		cat := shippedCatalog(t)
		dir := t.TempDir()
		file := filepath.Join(dir, catalog.OwnISOsFile)

		if warns := cat.ReloadOwnISOs(file); len(warns) != 0 {
			t.Errorf("ReloadOwnISOs of a missing file: warnings %q, want none", warns)
		}
		if own := cat.OwnISOs(); len(own) != 0 {
			t.Errorf("own ISOs of a missing file: %v, want none", ownIDs(own))
		}
		if got, want := ownIDs(cat.AllISOs()), ownIDs(cat.ISOs); !slices.Equal(got, want) {
			t.Errorf("AllISOs without own ISOs = %v, want the catalog ISOs %v", got, want)
		}

		srcA := catalog.ISOSource{URL: "https://example.com/a.iso", SHA256: strings.Repeat("ab", 32)}
		srcB := catalog.ISOSource{URL: "https://example.com/b.iso"}
		entries := []any{
			ownISOEntry(catalog.MinOwnISOID, "Own A", "x86", srcA),
			ownISOEntry(catalog.MinOwnISOID+1, "Own B", "arm", srcB),
		}
		writeJSON(t, file, entries)
		if warns := cat.ReloadOwnISOs(file); len(warns) != 0 {
			t.Fatalf("ReloadOwnISOs of a valid file: warnings %q, want none", warns)
		}
		if w := cat.OwnISOsWarnings(); len(w) != 0 {
			t.Errorf("OwnISOsWarnings after a valid file = %q, want none", w)
		}

		want := []struct {
			id   int64
			desc string
			arch string
			src  catalog.ISOSource
		}{
			{catalog.MinOwnISOID, "Own A", "x86", srcA},
			{catalog.MinOwnISOID + 1, "Own B", "arm", srcB},
		}
		own := cat.OwnISOs()
		if len(own) != len(want) {
			t.Fatalf("own ISOs %v, want %d", ownIDs(own), len(want))
		}
		for i, w := range want {
			iso := own[i]
			if iso.ID != w.id || !iso.Own || iso.CacheName != catalog.OwnISOCacheName(w.id, w.src) || iso.OwnSource != w.src {
				t.Errorf("own ISO %d = {ID %d Own %v CacheName %q OwnSource %+v}, want ID %d, own, cache name %q, source %+v",
					i, iso.ID, iso.Own, iso.CacheName, iso.OwnSource, w.id, catalog.OwnISOCacheName(w.id, w.src), w.src)
			}
			var raw map[string]any
			if err := json.Unmarshal(iso.Raw, &raw); err != nil {
				t.Fatalf("own ISO %d raw: %v", w.id, err)
			}
			wantRaw := map[string]any{
				"id": float64(w.id), "name": nil, "description": w.desc,
				"type": "private", "deprecation": nil, "architecture": w.arch,
			}
			if len(raw) != len(wantRaw) {
				t.Errorf("own ISO %d serves fields %v, want exactly %v", w.id, raw, wantRaw)
			}
			for k, v := range wantRaw {
				got, ok := raw[k]
				if !ok || got != v {
					t.Errorf("own ISO %d: %s = %v (present %v), want %v", w.id, k, got, ok, v)
				}
			}
			if _, ok := raw["_hlab"]; ok {
				t.Errorf("own ISO %d serves _hlab", w.id)
			}
			if src, ok := cat.ISOSourceByCacheName(iso.CacheName); !ok || src.URL != w.src.URL {
				t.Errorf("ISOSourceByCacheName(%q) = %+v, %v; want the URL %s", iso.CacheName, src, ok, w.src.URL)
			}
		}

		wantAll := append(ownIDs(cat.ISOs), catalog.MinOwnISOID, catalog.MinOwnISOID+1)
		if got := ownIDs(cat.AllISOs()); !slices.Equal(got, wantAll) {
			t.Errorf("AllISOs = %v, want the catalog ISOs then the own ISOs %v", got, wantAll)
		}

		if iso, ok := cat.ISO(strconv.FormatInt(catalog.MinOwnISOID, 10)); !ok || iso.ID != catalog.MinOwnISOID || !iso.Own {
			t.Errorf("ISO(%d) = %+v, %v; want the own ISO", catalog.MinOwnISOID, iso, ok)
		}
		for _, ref := range []string{"Own A", own[0].CacheName} {
			if iso, ok := cat.ISO(ref); ok {
				t.Errorf("ISO(%q) = ISO %d, want no match: own ISOs match by ID only", ref, iso.ID)
			}
		}
		first := cat.ISOs[0]
		if iso, ok := cat.ISO(first.Name); !ok || iso.ID != first.ID {
			t.Errorf("ISO(%q) = %+v, %v; want the catalog ISO %d", first.Name, iso, ok, first.ID)
		}
	})

	t.Run("CAT-10_invalid", func(t *testing.T) {
		cat := shippedCatalog(t)
		file := filepath.Join(t.TempDir(), catalog.OwnISOsFile)
		valid := []any{ownISOEntry(catalog.MinOwnISOID, "Own A", "x86", catalog.ISOSource{URL: "https://example.com/a.iso"})}

		srcA := catalog.ISOSource{URL: "https://example.com/a.iso"}
		srcB := catalog.ISOSource{URL: "https://example.com/b.iso"}
		sameName := ownISOEntry(catalog.MinOwnISOID+1, "Own B", "x86", srcB)
		sameName["_hlab"].(map[string]any)["name"] = catalog.OwnISOCacheName(catalog.MinOwnISOID, srcA)

		cases := []struct {
			name    string
			content []byte
		}{
			{"not JSON", []byte("[{not json")},
			{"ID below 100000", mustJSON(t, []any{
				ownISOEntry(catalog.MinOwnISOID, "Own A", "x86", srcA),
				ownISOEntry(catalog.MinOwnISOID-1, "Own low", "x86", srcB),
			})},
			{"duplicate ID", mustJSON(t, []any{
				ownISOEntry(catalog.MinOwnISOID, "Own A", "x86", srcA),
				ownISOEntry(catalog.MinOwnISOID, "Own B", "x86", srcB),
			})},
			{"duplicate cache name", mustJSON(t, []any{
				ownISOEntry(catalog.MinOwnISOID, "Own A", "x86", srcA),
				sameName,
			})},
			{"ID of a catalog ISO", mustJSON(t, []any{
				ownISOEntry(catalog.MinOwnISOID, "Own A", "x86", srcA),
				ownISOEntry(cat.ISOs[0].ID, "Own catalog ID", "x86", srcB),
			})},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				writeJSON(t, file, valid)
				if warns := cat.ReloadOwnISOs(file); len(warns) != 0 || len(cat.OwnISOs()) != 1 {
					t.Fatalf("valid file before the case: warnings %q, own ISOs %v; want none and one", warns, ownIDs(cat.OwnISOs()))
				}
				if err := os.WriteFile(file, c.content, 0o644); err != nil {
					t.Fatal(err)
				}
				warns := cat.ReloadOwnISOs(file)
				if len(warns) != 1 {
					t.Errorf("ReloadOwnISOs warnings %q, want exactly one", warns)
				}
				if got := cat.OwnISOsWarnings(); !slices.Equal(got, warns) {
					t.Errorf("OwnISOsWarnings = %q, want the returned %q", got, warns)
				}
				if own := cat.OwnISOs(); len(own) != 0 {
					t.Errorf("own ISOs %v, want none", ownIDs(own))
				}
				if got, want := ownIDs(cat.AllISOs()), ownIDs(cat.ISOs); !slices.Equal(got, want) {
					t.Errorf("AllISOs = %v, want only the catalog ISOs %v", got, want)
				}
			})
		}
	})

	t.Run("CAT-10_local_missing", func(t *testing.T) {
		cat := shippedCatalog(t)
		file := filepath.Join(t.TempDir(), catalog.OwnISOsFile)
		present := catalog.ISOSource{Path: localISO(t, "present.iso")}
		missing := catalog.ISOSource{Path: filepath.Join(t.TempDir(), "missing.iso")}
		remote := catalog.ISOSource{URL: "https://example.com/remote.iso"}
		idPresent, idMissing, idRemote := catalog.MinOwnISOID, catalog.MinOwnISOID+1, catalog.MinOwnISOID+2
		writeJSON(t, file, []any{
			ownISOEntry(idPresent, "Own present", "x86", present),
			ownISOEntry(idMissing, "Own missing", "x86", missing),
			ownISOEntry(idRemote, "Own remote", "x86", remote),
		})

		warns := cat.ReloadOwnISOs(file)
		if len(warns) != 1 || !names(warns[0], idMissing, "Own missing") {
			t.Errorf("ReloadOwnISOs warnings %q, want one naming the missing file's ISO", warns)
		}
		if got := ownIDs(cat.OwnISOs()); !slices.Equal(got, []int64{idPresent, idRemote}) {
			t.Errorf("own ISOs %v, want [%d %d]", got, idPresent, idRemote)
		}

		cat.DropOwnISO(idPresent, "copy failed")
		if got := ownIDs(cat.OwnISOs()); !slices.Equal(got, []int64{idRemote}) {
			t.Errorf("own ISOs after DropOwnISO(%d) %v, want [%d]", idPresent, got, idRemote)
		}
		if _, ok := cat.ISO(strconv.FormatInt(idPresent, 10)); ok {
			t.Errorf("ISO(%d) found after DropOwnISO", idPresent)
		}
		w := cat.OwnISOsWarnings()
		if len(w) != 2 || !slices.ContainsFunc(w, func(s string) bool { return names(s, idPresent, "Own present") }) {
			t.Errorf("OwnISOsWarnings after DropOwnISO = %q, want two, one naming the dropped ISO", w)
		}
	})

	t.Run("UI-10", func(t *testing.T) {
		cat := shippedCatalog(t)
		file := filepath.Join(t.TempDir(), catalog.OwnISOsFile)
		if err := os.WriteFile(file, []byte("[]"), 0o644); err != nil {
			t.Fatal(err)
		}
		firstID := int64(catalog.MinOwnISOID)
		for slices.Contains(ownIDs(cat.ISOs), firstID) {
			firstID++
		}

		srcA := catalog.ISOSource{URL: "https://example.com/a.iso", SHA256: strings.Repeat("0f", 32)}
		specA := catalog.OwnISOSpec{Description: "Own A", Architecture: "x86", Source: srcA}
		a, err := catalog.AddOwnISO(cat, file, specA)
		if err != nil {
			t.Fatalf("AddOwnISO %+v: %v", specA, err)
		}
		if a.ID != firstID || a.CacheName != catalog.OwnISOCacheName(firstID, srcA) {
			t.Errorf("AddOwnISO = ID %d cache %q, want ID %d cache %q", a.ID, a.CacheName, firstID, catalog.OwnISOCacheName(firstID, srcA))
		}
		if m := cacheNameRe.FindStringSubmatch(a.CacheName); m == nil || m[1] != strconv.FormatInt(firstID, 10) {
			t.Errorf("cache name %q, want own-%d-<8 hex>.iso", a.CacheName, firstID)
		}
		cat.ReloadOwnISOs(file)

		srcB := catalog.ISOSource{Path: localISO(t, "b.iso")}
		specB := catalog.OwnISOSpec{Description: "Own B", Architecture: "arm", Source: srcB}
		b, err := catalog.AddOwnISO(cat, file, specB)
		if err != nil {
			t.Fatalf("AddOwnISO %+v: %v", specB, err)
		}
		if b.ID <= a.ID {
			t.Errorf("second AddOwnISO gave ID %d, want the next free ID after %d", b.ID, a.ID)
		}
		cat.ReloadOwnISOs(file)

		list := readOwnISOs(t, file)
		if len(list) != 2 {
			t.Fatalf("own ISOs file holds %d entries, want 2", len(list))
		}
		entryA := entryByID(list, a.ID)
		hl, _ := entryA["_hlab"].(map[string]any)
		src, _ := hl["source"].(map[string]any)
		if entryA["name"] != nil || entryA["type"] != "private" || entryA["description"] != "Own A" || entryA["architecture"] != "x86" ||
			hl["name"] != a.CacheName || src["url"] != srcA.URL || src["sha256"] != srcA.SHA256 {
			t.Errorf("file entry of Own A = %v, want a private ISO with name null, description, architecture and _hlab name and source", entryA)
		}
		entryB, _ := json.Marshal(entryByID(list, b.ID))

		refusals := []struct {
			name   string
			spec   catalog.OwnISOSpec
			fields []string
		}{
			{"empty description", catalog.OwnISOSpec{Architecture: "x86", Source: catalog.ISOSource{URL: "https://example.com/c.iso"}}, []string{"description"}},
			{"architecture riscv", catalog.OwnISOSpec{Description: "C", Architecture: "riscv", Source: catalog.ISOSource{URL: "https://example.com/c.iso"}}, []string{"architecture"}},
			{"no source", catalog.OwnISOSpec{Description: "C", Architecture: "x86"}, []string{"url", "path"}},
			{"both sources", catalog.OwnISOSpec{Description: "C", Architecture: "x86", Source: catalog.ISOSource{URL: "https://example.com/c.iso", Path: srcB.Path}}, []string{"url", "path"}},
			{"malformed URL", catalog.OwnISOSpec{Description: "C", Architecture: "x86", Source: catalog.ISOSource{URL: "not a url"}}, []string{"url"}},
			{"malformed SHA-256", catalog.OwnISOSpec{Description: "C", Architecture: "x86", Source: catalog.ISOSource{URL: "https://example.com/c.iso", SHA256: "abc"}}, []string{"sha256"}},
			{"path without .iso", catalog.OwnISOSpec{Description: "C", Architecture: "x86", Source: catalog.ISOSource{Path: localISO(t, "c.img")}}, []string{"path"}},
		}
		for _, r := range refusals {
			t.Run(r.name, func(t *testing.T) {
				before, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				_, err = catalog.AddOwnISO(cat, file, r.spec)
				var fe *catalog.FieldError
				if !errors.As(err, &fe) || !slices.Contains(r.fields, fe.Field) {
					t.Errorf("AddOwnISO %+v = %v, want a FieldError for one of %q", r.spec, err, r.fields)
				}
				if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
					t.Errorf("own ISOs file changed by a refused AddOwnISO: %s", after)
				}
			})
		}

		renamed := catalog.OwnISOSpec{ID: a.ID, Description: "Own A renamed", Architecture: "x86", Source: srcA}
		u, err := catalog.UpdateOwnISO(cat, file, renamed)
		if err != nil {
			t.Fatalf("UpdateOwnISO %+v: %v", renamed, err)
		}
		if u.ID != a.ID || u.CacheName != a.CacheName {
			t.Errorf("UpdateOwnISO of the description = ID %d cache %q, want ID %d and cache %q unchanged", u.ID, u.CacheName, a.ID, a.CacheName)
		}
		if e := entryByID(readOwnISOs(t, file), a.ID); e == nil || e["description"] != "Own A renamed" {
			t.Errorf("file entry after the description update = %v, want description Own A renamed", e)
		}
		cat.ReloadOwnISOs(file)

		srcA2 := catalog.ISOSource{URL: "https://example.com/a2.iso"}
		moved := catalog.OwnISOSpec{ID: a.ID, Description: "Own A renamed", Architecture: "x86", Source: srcA2}
		u, err = catalog.UpdateOwnISO(cat, file, moved)
		if err != nil {
			t.Fatalf("UpdateOwnISO %+v: %v", moved, err)
		}
		if u.ID != a.ID || u.CacheName == a.CacheName || u.CacheName != catalog.OwnISOCacheName(a.ID, srcA2) {
			t.Errorf("UpdateOwnISO with a new source = ID %d cache %q, want ID %d and the new cache %q", u.ID, u.CacheName, a.ID, catalog.OwnISOCacheName(a.ID, srcA2))
		}
		cat.ReloadOwnISOs(file)

		unknown := catalog.OwnISOSpec{ID: b.ID + 1000, Description: "Nobody", Architecture: "x86", Source: srcA2}
		if _, err := catalog.UpdateOwnISO(cat, file, unknown); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("UpdateOwnISO of an unknown ID = %v, want an fs.ErrNotExist error", err)
		}

		if err := catalog.RemoveOwnISO(file, a.ID); err != nil {
			t.Fatalf("RemoveOwnISO %d: %v", a.ID, err)
		}
		list = readOwnISOs(t, file)
		if len(list) != 1 || entryByID(list, b.ID) == nil {
			t.Errorf("own ISOs file after RemoveOwnISO %d holds %v, want only %d", a.ID, list, b.ID)
		}
		if after, _ := json.Marshal(entryByID(list, b.ID)); !bytes.Equal(after, entryB) {
			t.Errorf("entry of Own B changed by the other changes: %s, want %s", after, entryB)
		}
		if err := catalog.RemoveOwnISO(file, a.ID); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("RemoveOwnISO of an unknown ID = %v, want an fs.ErrNotExist error", err)
		}
	})
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
