package storageboxtypes_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resources/storageboxtypes"
)

const base = "/hetzner/v1/storage_box_types"

// sameJSON reports whether got and want decode to the same value.
func sameJSON(t *testing.T, got, want json.RawMessage) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("decode %s: %v", got, err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("decode %s: %v", want, err)
	}
	return reflect.DeepEqual(g, w)
}

func TestStorageBoxTypes(t *testing.T) {
	t.Run("SBOX-01", func(t *testing.T) {
		cat, err := catalog.Load("../../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		cat.StorageBoxTypes = apitest.StorageBoxTypes(t)
		if len(cat.StorageBoxTypes) == 0 {
			t.Fatal("recorded storage box types are empty")
		}
		env := apitest.New(t, storageboxtypes.New(cat))
		byName := map[string]catalog.StorageBoxType{}
		for _, st := range cat.StorageBoxTypes {
			byName[st.Name] = st
		}

		t.Run("list", func(t *testing.T) {
			e := env.For(t)
			resp := e.Do(http.MethodGet, base, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", base, resp.StatusCode)
			}
			var body struct {
				StorageBoxTypes []json.RawMessage `json:"storage_box_types"`
			}
			apitest.Decode(t, resp, &body)
			var names []string
			for _, raw := range body.StorageBoxTypes {
				var item struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(raw, &item); err != nil {
					t.Fatalf("decode item %s: %v", raw, err)
				}
				names = append(names, item.Name)
				want, ok := byName[item.Name]
				if !ok {
					t.Errorf("listed type %q is not in the catalog", item.Name)
					continue
				}
				if !sameJSON(t, raw, want.Raw) {
					t.Errorf("listed %s = %s, want as recorded %s", item.Name, raw, want.Raw)
				}
			}
			slices.Sort(names)
			want := make([]string, 0, len(byName))
			for n := range byName {
				want = append(want, n)
			}
			slices.Sort(want)
			if !slices.Equal(names, want) {
				t.Errorf("listed types %v, want %v", names, want)
			}
		})

		t.Run("filter name", func(t *testing.T) {
			e := env.For(t)
			resp := e.Do(http.MethodGet, base+"?name=bx21", nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET ?name=bx21 = %d, want 200", resp.StatusCode)
			}
			var body struct {
				StorageBoxTypes []struct {
					Name string `json:"name"`
				} `json:"storage_box_types"`
			}
			apitest.Decode(t, resp, &body)
			if len(body.StorageBoxTypes) != 1 || body.StorageBoxTypes[0].Name != "bx21" {
				t.Errorf("GET ?name=bx21 = %+v, want only bx21", body.StorageBoxTypes)
			}
		})

		t.Run("get", func(t *testing.T) {
			e := env.For(t)
			want := byName["bx11"]
			path := base + "/" + strconv.FormatInt(want.ID, 10)
			resp := e.Do(http.MethodGet, path, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
			}
			var body struct {
				StorageBoxType json.RawMessage `json:"storage_box_type"`
			}
			apitest.Decode(t, resp, &body)
			if !sameJSON(t, body.StorageBoxType, want.Raw) {
				t.Errorf("GET %s = %s, want as recorded %s", path, body.StorageBoxType, want.Raw)
			}
		})

		t.Run("unknown id", func(t *testing.T) {
			e := env.For(t)
			resp := e.Do(http.MethodGet, base+"/999999", nil)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("GET %s/999999 = %d, want 404", base, resp.StatusCode)
			}
			if code := apitest.ErrorCode(t, resp); code != "not_found" {
				t.Errorf("error code %q, want not_found", code)
			}
		})
	})
}
