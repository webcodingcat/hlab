package locations_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resources/datacenters"
	"github.com/webcodingcat/hlab/internal/resources/locations"
)

func TestLocations(t *testing.T) {
	t.Run("CAT-01", func(t *testing.T) {
		cat, err := catalog.Load("../../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		env := apitest.New(t, locations.New(cat), datacenters.New())

		want := map[string]string{
			"fsn1": "eu-central",
			"nbg1": "eu-central",
			"hel1": "eu-central",
			"ash":  "us-east",
			"hil":  "us-west",
			"sin":  "ap-southeast",
		}
		resp := env.Do(http.MethodGet, "/locations", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /locations: status %d, want 200", resp.StatusCode)
		}
		var list struct {
			Locations []struct {
				ID          int64  `json:"id"`
				Name        string `json:"name"`
				NetworkZone string `json:"network_zone"`
			} `json:"locations"`
		}
		apitest.Decode(t, resp, &list)
		got := map[string]string{}
		var fsn1 int64
		for _, l := range list.Locations {
			got[l.Name] = l.NetworkZone
			if l.Name == "fsn1" {
				fsn1 = l.ID
			}
		}
		if len(list.Locations) != len(want) || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("locations = %v, want exactly %v", got, want)
		}

		resp = env.Do(http.MethodGet, fmt.Sprintf("/locations/%d", fsn1), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /locations/%d: status %d, want 200", fsn1, resp.StatusCode)
		}
		var one struct {
			Location struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"location"`
		}
		apitest.Decode(t, resp, &one)
		if one.Location.ID != fsn1 || one.Location.Name != "fsn1" {
			t.Errorf("GET /locations/%d = %+v, want fsn1", fsn1, one.Location)
		}

		resp = env.Do(http.MethodGet, "/datacenters", nil)
		if resp.StatusCode != http.StatusGone {
			t.Errorf("GET /datacenters: status %d, want 410", resp.StatusCode)
		}
	})
}
