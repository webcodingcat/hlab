package isos_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resources/isos"
)

// isoFields are the fields of Hetzner's ISO object.
var isoFields = []string{"id", "name", "description", "type", "deprecation", "architecture"}

func listISOs(t *testing.T, env *apitest.Env, query string) []map[string]any {
	t.Helper()
	resp := env.Do(http.MethodGet, "/isos"+query, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /isos%s: status %d, want 200", query, resp.StatusCode)
	}
	var list struct {
		ISOs []map[string]any `json:"isos"`
	}
	apitest.Decode(t, resp, &list)
	return list.ISOs
}

func TestISOs(t *testing.T) {
	t.Run("CAT-08", func(t *testing.T) {
		cat, err := catalog.Load("../../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		if len(cat.ISOs) == 0 {
			t.Fatal("shipped catalog has no ISOs")
		}
		env := apitest.New(t, isos.New(cat))

		want := map[string]bool{}
		for _, iso := range cat.ISOs {
			want[iso.Name] = true
		}
		all := listISOs(t, env, "?per_page=50")
		got := map[string]bool{}
		for _, iso := range all {
			name, _ := iso["name"].(string)
			got[name] = true
			for _, f := range isoFields {
				if _, ok := iso[f]; !ok {
					t.Errorf("ISO %q lacks field %s", name, f)
				}
			}
			if _, ok := iso["_hlab"]; ok {
				t.Errorf("ISO %q carries _hlab", name)
			}
		}
		if len(all) != len(cat.ISOs) || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("GET /isos names = %v, want exactly the catalog's %v", got, want)
		}

		first := cat.ISOs[0]
		byName := listISOs(t, env, "?name="+first.Name)
		if len(byName) != 1 || byName[0]["name"] != first.Name {
			t.Errorf("GET /isos?name=%s = %v, want exactly that ISO", first.Name, byName)
		}

		wantArm := 0
		for _, iso := range cat.ISOs {
			if iso.Architecture == "arm" {
				wantArm++
			}
		}
		arm := listISOs(t, env, "?architecture=arm")
		for _, iso := range arm {
			if iso["architecture"] != "arm" {
				t.Errorf("GET /isos?architecture=arm returned %v", iso)
			}
		}
		if len(arm) != wantArm {
			t.Errorf("GET /isos?architecture=arm returned %d ISOs, want %d", len(arm), wantArm)
		}

		path := fmt.Sprintf("/isos/%d", first.ID)
		resp := env.Do(http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
		}
		var one struct {
			ISO map[string]any `json:"iso"`
		}
		apitest.Decode(t, resp, &one)
		if one.ISO["name"] != first.Name || one.ISO["id"] != float64(first.ID) {
			t.Errorf("GET %s = %v, want ISO %s", path, one.ISO, first.Name)
		}
		if _, ok := one.ISO["_hlab"]; ok {
			t.Errorf("GET %s carries _hlab", path)
		}

		unknown := int64(1)
		for _, iso := range cat.ISOs {
			unknown = max(unknown, iso.ID+1)
		}
		if resp := env.Do(http.MethodGet, fmt.Sprintf("/isos/%d", unknown), nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET /isos/%d: status %d, want 404", unknown, resp.StatusCode)
		}
	})
}
