package catalog_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/catalog"
)

// writeUserFile writes a user's catalog and returns its path.
func writeUserFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), catalog.UserFile)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The tools check Hetzner's real IDs (hetzner.hcloud's integration tests:
// cpx12 = 108, cpx22 = 109, hel1 = 3).
func TestBuiltinCatalogHasHetznersIDs(t *testing.T) {
	builtin := catalog.Builtin()
	ids := map[string]int64{}
	for _, serverType := range builtin.ServerTypes {
		ids[serverType.Name] = serverType.ID
	}
	if ids["cpx12"] != 108 || ids["cpx22"] != 109 {
		t.Errorf("cpx12 is %d, cpx22 is %d", ids["cpx12"], ids["cpx22"])
	}
	if hel1, found := builtin.LocationByName("hel1"); !found || hel1.ID != 3 {
		t.Errorf("hel1 is %+v", hel1)
	}
	for _, serverType := range builtin.ServerTypes {
		if serverType.Architecture != "x86" {
			t.Errorf("the server type %s is for %s; the lab runs x86 only", serverType.Name, serverType.Architecture)
		}
	}
}

// The lab offers system images only in their newest version.
func TestBuiltinCatalogHasOnlyTheNewestImages(t *testing.T) {
	var names []string
	for _, image := range catalog.Builtin().Images {
		names = append(names, image.Name)
	}
	slices.Sort(names)
	if strings.Join(names, " ") != "debian-13 ubuntu-26.04" {
		t.Errorf("the images are %v", names)
	}
}

func TestLoadWithoutUserFileIsTheBuiltinCatalog(t *testing.T) {
	loaded, err := catalog.Load(filepath.Join(t.TempDir(), catalog.UserFile))
	if err != nil || len(loaded.ServerTypes) != len(catalog.Builtin().ServerTypes) {
		t.Errorf("loaded %d server types, %v", len(loaded.ServerTypes), err)
	}
}

func TestUserFileReplacesWhatItNames(t *testing.T) {
	loaded, err := catalog.Load(writeUserFile(t, `
images:
  - {id: 7, name: debian-13, description: Debian 13, os_flavor: debian, os_version: "13", architecture: x86, disk_size: 5, created: 2025-08-09T00:00:00Z}
pricing:
  currency: USD
`))
	if err != nil {
		t.Fatal(err)
	}
	builtin := catalog.Builtin()
	if len(loaded.Images) != 1 || loaded.Images[0].ID != 7 {
		t.Errorf("the images are %+v", loaded.Images)
	}
	if loaded.Pricing.Currency != "USD" || loaded.Pricing.VATRate != builtin.Pricing.VATRate {
		t.Errorf("the pricing is %+v", loaded.Pricing)
	}
	if len(loaded.ServerTypes) != len(builtin.ServerTypes) {
		t.Errorf("the server types changed: %d of %d", len(loaded.ServerTypes), len(builtin.ServerTypes))
	}
}

func TestUserFileErrors(t *testing.T) {
	for name, content := range map[string]string{
		"an unknown key":      "server_typos: []",
		"a doubled ID":        "isos: [{id: 1, name: a.iso}, {id: 1, name: b.iso}]",
		"a doubled name":      "isos: [{id: 1, name: a.iso}, {id: 2, name: a.iso}]",
		"an entry without ID": "isos: [{name: a.iso}]",
		"an unknown location": "locations: [{id: 1, name: fsn1}]", // the server types still name others
		"an unknown flavor":   "images: [{id: 1, name: arch, os_flavor: arch, architecture: x86}]",
		"no YAML":             "images: [",
	} {
		if _, err := catalog.Load(writeUserFile(t, content)); err == nil {
			t.Errorf("%s: loaded", name)
		} else if !strings.Contains(err.Error(), catalog.UserFile) {
			t.Errorf("%s: the error %q does not name the file", name, err)
		}
	}
}

func TestListsAreSortedByID(t *testing.T) {
	loaded, err := catalog.Load(writeUserFile(t, "isos: [{id: 9, name: b.iso}, {id: 2, name: a.iso}]"))
	if err != nil || loaded.ISOs[0].ID != 2 {
		t.Errorf("the ISOs are %+v, %v", loaded.ISOs, err)
	}
}
