package loadbalancertypes_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resources/loadbalancertypes"
)

type lbType struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func TestLoadBalancerTypes(t *testing.T) {
	t.Run("LB-01", func(t *testing.T) {
		cat, err := catalog.Load("../../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		var want []lbType
		for _, raw := range cat.LoadBalancerTypes {
			var lt lbType
			if err := json.Unmarshal(raw, &lt); err != nil {
				t.Fatalf("catalog load balancer type: %v", err)
			}
			want = append(want, lt)
		}
		if len(want) < 2 {
			t.Fatalf("catalog has %d load balancer types, the test needs two", len(want))
		}
		env := apitest.New(t, loadbalancertypes.New(cat))

		list := func(query string) []lbType {
			t.Helper()
			resp := env.Do(http.MethodGet, "/load_balancer_types"+query, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /load_balancer_types%s: status %d, want 200", query, resp.StatusCode)
			}
			var body struct {
				LoadBalancerTypes []lbType `json:"load_balancer_types"`
			}
			apitest.Decode(t, resp, &body)
			return body.LoadBalancerTypes
		}

		if got := list(""); !slices.Equal(got, want) {
			t.Errorf("GET /load_balancer_types = %+v, want the catalog's %+v", got, want)
		}
		if got := list("?name=" + want[1].Name); !slices.Equal(got, want[1:2]) {
			t.Errorf("GET /load_balancer_types?name=%s = %+v, want %+v", want[1].Name, got, want[1:2])
		}

		resp := env.Do(http.MethodGet, fmt.Sprintf("/load_balancer_types/%d", want[1].ID), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /load_balancer_types/%d: status %d, want 200", want[1].ID, resp.StatusCode)
		}
		var one struct {
			LoadBalancerType lbType `json:"load_balancer_type"`
		}
		apitest.Decode(t, resp, &one)
		if one.LoadBalancerType != want[1] {
			t.Errorf("GET /load_balancer_types/%d = %+v, want %+v", want[1].ID, one.LoadBalancerType, want[1])
		}

		resp = env.Do(http.MethodGet, "/load_balancer_types/999999", nil)
		if code := apitest.ErrorCode(t, resp); resp.StatusCode != http.StatusNotFound || code != "not_found" {
			t.Errorf("GET /load_balancer_types/999999: status %d code %q, want 404 not_found", resp.StatusCode, code)
		}
	})
}
