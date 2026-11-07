package cloud_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/spectest"
)

// entry is what every catalog resource has.
type entry struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// listEntries returns the entries of a list answer under its key.
func listEntries(t *testing.T, response *spectest.Response, key string) []entry {
	t.Helper()
	var lists map[string]json.RawMessage
	response.Decode(t, &lists)
	var entries []entry
	if err := json.Unmarshal(lists[key], &entries); err != nil {
		t.Fatalf("%s: %v", response.Body, err)
	}
	return entries
}

// The catalog's resources are read through their list and their single
// endpoint; each list takes a name. Where the tools check Hetzner's real IDs
// (hetzner.hcloud's integration tests), the test names them.
func TestCatalogResourcesListAndGet(t *testing.T) {
	test := newTestLab(t)
	for _, resource := range []struct {
		path, key, name string
		id              int64
	}{
		{"/v1/locations", "locations", "hel1", 3},
		{"/v1/server_types", "server_types", "cpx22", 109},
		{"/v1/load_balancer_types", "load_balancer_types", "lb11", 1},
		{"/v1/images", "images", "debian-13", 0},
		{"/v1/isos", "isos", "", 0},
	} {
		response := test.client.Get(resource.path)
		all := listEntries(t, response, resource.key)
		if response.Status != http.StatusOK || len(all) == 0 {
			t.Errorf("GET %s answered %d %s", resource.path, response.Status, response.Body)
			continue
		}
		if resource.name == "" {
			resource.name, resource.id = all[0].Name, all[0].ID
		}
		named := listEntries(t, test.client.Get(resource.path+"?name="+resource.name), resource.key)
		if len(named) != 1 || named[0].Name != resource.name || (resource.id != 0 && named[0].ID != resource.id) {
			t.Errorf("GET %s?name=%s listed %+v", resource.path, resource.name, named)
			continue
		}
		id := named[0].ID
		single := strings.TrimSuffix(resource.key, "s")
		var one map[string]struct {
			ID int64 `json:"id"`
		}
		test.client.Get(fmt.Sprintf("%s/%d", resource.path, id)).Decode(t, &one)
		if one[single].ID != id {
			t.Errorf("GET %s/%d answered %+v", resource.path, id, one)
		}
		if response := test.client.Get(resource.path + "/999999999"); response.Status != http.StatusNotFound || errorOf(t, response).Code != "not_found" {
			t.Errorf("GET %s/999999999 answered %d %s", resource.path, response.Status, response.Body)
		}
	}
}

func TestServerTypesNameTheirLocations(t *testing.T) {
	test := newTestLab(t)
	var got schema.ServerTypeGetResponse
	test.client.Get("/v1/server_types/108").Decode(t, &got)
	serverType := got.ServerType
	if serverType.Name != "cpx12" || serverType.Architecture != "x86" || len(serverType.Locations) == 0 || len(serverType.Prices) == 0 {
		t.Fatalf("cpx12 is %+v", serverType)
	}
	ids := map[string]int64{}
	for _, location := range test.lab.Catalog().Locations {
		ids[location.Name] = location.ID
	}
	for _, location := range serverType.Locations {
		if location.ID != ids[location.Name] {
			t.Errorf("the location %s has the ID %d, want %d", location.Name, location.ID, ids[location.Name])
		}
	}
}

func TestLocationsSort(t *testing.T) {
	test := newTestLab(t)
	var list schema.LocationListResponse
	test.client.Get("/v1/locations?sort=name:desc").Decode(t, &list)
	for i := 1; i < len(list.Locations); i++ {
		if list.Locations[i-1].Name < list.Locations[i].Name {
			t.Fatalf("the locations are not sorted by name: %+v", list.Locations)
		}
	}
}

func TestPricingHasEveryType(t *testing.T) {
	test := newTestLab(t)
	response := test.client.Get("/v1/pricing")
	var got schema.PricingGetResponse
	response.Decode(t, &got)
	catalog := test.lab.Catalog()
	if response.Status != http.StatusOK || got.Pricing.Currency != "EUR" ||
		len(got.Pricing.ServerTypes) != len(catalog.ServerTypes) || len(got.Pricing.LoadBalancerTypes) != len(catalog.LoadBalancerTypes) {
		t.Errorf("answered %d %s", response.Status, response.Body)
	}
	// the real API sends neither of these any more
	if strings.Contains(string(response.Body), `"datacenter"`) || strings.Contains(string(response.Body), `"traffic"`) {
		t.Errorf("the pricing names a datacenter or the removed traffic: %s", response.Body)
	}
}

func TestImageFilters(t *testing.T) {
	test := newTestLab(t)
	// sorted by name, so that the order does not depend on the IDs
	for query, want := range map[string]string{
		"sort=name":                           "debian-13 ubuntu-26.04",
		"sort=name&type=system":               "debian-13 ubuntu-26.04",
		"sort=name&type=snapshot":             "",
		"sort=name&type=snapshot&type=system": "debian-13 ubuntu-26.04",
		"sort=name&status=available":          "debian-13 ubuntu-26.04",
		"sort=name&bound_to=1":                "",
		"sort=name&architecture=x86":          "debian-13 ubuntu-26.04",
		"sort=name&architecture=arm":          "",
		"sort=name&name=ubuntu-26.04":         "ubuntu-26.04",
		"sort=name&label_selector=env":        "",
		"sort=name:desc":                      "ubuntu-26.04 debian-13",
	} {
		var list schema.ImageListResponse
		response := test.client.Get("/v1/images?" + query)
		response.Decode(t, &list)
		var names []string
		for _, image := range list.Images {
			names = append(names, *image.Name)
		}
		if response.Status != http.StatusOK || strings.Join(names, " ") != want {
			t.Errorf("GET /v1/images%s listed %v, want %q", query, names, want)
		}
	}
}

func TestISOArchitectureFilter(t *testing.T) {
	test := newTestLab(t)
	count := func(query string) int {
		var list schema.ISOListResponse
		test.client.Get("/v1/isos"+query).Decode(t, &list)
		return len(list.ISOs)
	}
	all, x86, wildcard := count(""), count("?architecture=x86"), count("?architecture=x86&include_architecture_wildcard=true")
	if x86 > all || wildcard < x86 || wildcard > all {
		t.Errorf("all %d, x86 %d, x86 with the wildcard %d", all, x86, wildcard)
	}
}

// Hetzner's API (measured): the datacenter endpoints were removed.
func TestDatacentersAreRemoved(t *testing.T) {
	test := newTestLab(t)
	for _, path := range []string{"/v1/datacenters", "/v1/datacenters/1"} {
		response := test.client.Get(path)
		got := errorOf(t, response)
		if response.Status != http.StatusGone || got.Code != "deprecated_api_endpoint" || got.Message != "API functionality was removed" ||
			string(got.Details) != `{"announcement":"https://docs.hetzner.cloud/changelog#2026-06-02-datacenters-deprecated"}` {
			t.Errorf("GET %s answered %d %s", path, response.Status, response.Body)
		}
	}
}

// No answer of the catalog names a datacenter: Hetzner removed them.
func TestNoAnswerNamesADatacenter(t *testing.T) {
	test := newTestLab(t)
	for _, path := range []string{"/v1/locations", "/v1/server_types", "/v1/load_balancer_types", "/v1/pricing", "/v1/images", "/v1/isos"} {
		if body := string(test.client.Get(path).Body); strings.Contains(body, "datacenter") {
			t.Errorf("GET %s names a datacenter: %s", path, body)
		}
	}
}
