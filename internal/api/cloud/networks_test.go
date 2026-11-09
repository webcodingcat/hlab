package cloud_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform"
)

// networkAnswer is a network as the API shows it, with the fields
// schema.Network leaves out.
type networkAnswer struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	IPRange       string  `json:"ip_range"`
	Servers       []int64 `json:"servers"`
	LoadBalancers []int64 `json:"load_balancers"`
	Subnets       []struct {
		Type        string `json:"type"`
		IPRange     string `json:"ip_range"`
		NetworkZone string `json:"network_zone"`
		Gateway     string `json:"gateway"`
		VSwitchID   *int64 `json:"vswitch_id"`
	} `json:"subnets"`
	Routes                []schema.NetworkRoute    `json:"routes"`
	Labels                map[string]string        `json:"labels"`
	Protection            schema.NetworkProtection `json:"protection"`
	ExposeRoutesToVSwitch bool                     `json:"expose_routes_to_vswitch"`
}

// cloudSubnet is a subnet of the type cloud in eu-central.
func cloudSubnet(ipRange string) map[string]any {
	return map[string]any{"type": "cloud", "network_zone": "eu-central", "ip_range": ipRange}
}

// createNetwork creates a network and fails the test if it cannot.
func createNetwork(t *testing.T, test *testLab, body map[string]any) networkAnswer {
	t.Helper()
	response := test.client.Do(http.MethodPost, "/v1/networks", body)
	if response.Status != http.StatusCreated {
		t.Fatalf("POST /v1/networks answered %d %s", response.Status, response.Body)
	}
	var created struct {
		Network networkAnswer `json:"network"`
	}
	response.Decode(t, &created)
	return created.Network
}

// getNetwork reads a network.
func getNetwork(t *testing.T, test *testLab, id int64) networkAnswer {
	t.Helper()
	var answer struct {
		Network networkAnswer `json:"network"`
	}
	test.client.Get(fmt.Sprintf("/v1/networks/%d", id)).Decode(t, &answer)
	return answer.Network
}

// networkAction runs a network action and returns the answer.
func networkAction(test *testLab, id int64, command string, body any) *responseOf {
	return &responseOf{test.client.Do(http.MethodPost, fmt.Sprintf("/v1/networks/%d/actions/%s", id, command), body)}
}

// serverNetworkAction runs a server's action in a network and returns the
// answer.
func serverNetworkAction(test *testLab, id int64, command string, body any) *responseOf {
	return &responseOf{test.client.Do(http.MethodPost, fmt.Sprintf("/v1/servers/%d/actions/%s", id, command), body)}
}

// actionOf returns the action of an answer, which must be 201.
func actionOf(t *testing.T, what string, response *responseOf) schema.Action {
	t.Helper()
	if response.Status != http.StatusCreated {
		t.Fatalf("%s answered %d %s", what, response.Status, response.Body)
	}
	var answer schema.ActionGetResponse
	response.Decode(t, &answer)
	return answer.Action
}

// networkError is an expected error answer, with the messages of its
// fields if it names fields.
type networkError struct {
	status        int
	code, message string
	fields        map[string]string
}

// expectErrors sends each body to a path and checks the answer.
func expectErrors(t *testing.T, test *testLab, path string, cases map[string]struct {
	body any
	want networkError
}) {
	t.Helper()
	for name, c := range cases {
		response := &responseOf{test.client.Do(http.MethodPost, path, c.body)}
		expectError(t, name, response, c.want.status, c.want.code, c.want.message)
		if c.want.fields == nil {
			continue
		}
		messages := fieldMessages(t, errorOf(t, response.Response))
		for field, message := range c.want.fields {
			if got := messages[field]; len(got) != 1 || got[0] != message {
				t.Errorf("%s: the field %s has %q, want %q", name, field, got, message)
			}
		}
	}
}

// invalid is an invalid_input without fields.
func invalid(message string) networkError {
	return networkError{http.StatusUnprocessableEntity, "invalid_input", message, nil}
}

// invalidField is an invalid_input that names one field.
func invalidField(field, message string) networkError {
	return networkError{http.StatusUnprocessableEntity, "invalid_input", "invalid input in field '" + field + "'", map[string]string{field: message}}
}

// refused is a 422 with another code than invalid_input.
func refused(code, message string) networkError {
	return networkError{http.StatusUnprocessableEntity, code, message, nil}
}

// The errors of a create, as Hetzner answers them (measured 2026-11-08):
// the fields first, then the range, the subnets and the routes, then the
// limits.
func TestNetworkCreateErrors(t *testing.T) {
	test := newTestLab(t)
	network := func(ipRange string, extra map[string]any) map[string]any {
		body := map[string]any{"name": "net", "ip_range": ipRange}
		for key, value := range extra {
			body[key] = value
		}
		return body
	}
	subnets := func(list ...map[string]any) map[string]any { return map[string]any{"subnets": list} }
	route := func(destination, gateway string) map[string]any {
		return map[string]any{"routes": []map[string]any{{"destination": destination, "gateway": gateway}}}
	}
	many := func(count int) []map[string]any {
		var list []map[string]any
		for i := range count {
			list = append(list, cloudSubnet(fmt.Sprintf("10.%d.0.0/24", i+1)))
		}
		return list
	}
	expectErrors(t, test, "/v1/networks", map[string]struct {
		body any
		want networkError
	}{
		"no name":                {map[string]any{"ip_range": "10.0.0.0/16"}, invalidField("name", "'name' is required")},
		"an empty name":          {network("10.0.0.0/16", map[string]any{"name": ""}), invalidField("name", "'name' is required")},
		"a long name":            {network("10.0.0.0/16", map[string]any{"name": strings.Repeat("x", 129)}), invalidField("name", "value required to be smaller")},
		"no range":               {map[string]any{"name": "net"}, invalidField("ip_range", "'ip_range' is required")},
		"a range without a mask": {network("10.0.0.0", nil), invalidField("ip_range", "must be a valid IP prefix")},
		"a public range":         {network("8.8.0.0/16", nil), invalid("invalid ip range")},
		"a /25":                  {network("10.0.0.0/25", nil), invalid("invalid ip range")},
		"host bits":              {network("10.0.0.1/16", nil), invalid("invalid ip range")},
		"IPv6":                   {network("fd00::/48", nil), invalid("invalid ip range")},
		"a /7":                   {network("10.0.0.0/7", nil), invalid("invalid ip range")},
		"the name before the range": {
			map[string]any{"name": "", "ip_range": "8.8.0.0/16"}, invalidField("name", "'name' is required")},
		"bad labels": {network("10.0.0.0/16", map[string]any{"labels": map[string]string{"bad key!": "x"}}),
			invalidField("labels", "key contains invalid characters or is malformed")},
		"a subnet outside":     {network("10.0.0.0/16", subnets(cloudSubnet("10.1.0.0/24"))), invalid("invalid subnetwork")},
		"a /31 subnet":         {network("10.0.0.0/16", subnets(cloudSubnet("10.0.0.0/31"))), invalid("invalid subnetwork")},
		"a subnet's host bits": {network("10.0.0.0/16", subnets(cloudSubnet("10.0.1.1/24"))), invalid("invalid subnetwork")},
		"overlapping subnets": {network("10.0.0.0/16", subnets(cloudSubnet("10.0.0.0/24"), cloudSubnet("10.0.0.0/25"))),
			invalid("ip ranges of subnetworks or routes overlap")},
		"a subnet without a zone": {network("10.0.0.0/16", subnets(map[string]any{"type": "cloud", "ip_range": "10.0.1.0/24"})),
			invalidField("subnets[0].network_zone", "'subnets[0].network_zone' is required")},
		"a subnet without a type": {network("10.0.0.0/16", subnets(map[string]any{"network_zone": "eu-central", "ip_range": "10.0.1.0/24"})),
			invalidField("subnets[0].type", "'subnets[0].type' is required")},
		"a subnet without a range": {network("10.0.0.0/16", subnets(map[string]any{"type": "cloud", "network_zone": "eu-central"})),
			invalidField("subnets[0].ip_range", "'subnets[0].ip_range' is required")},
		"an unknown type": {network("10.0.0.0/16", subnets(map[string]any{"type": "foo", "network_zone": "eu-central", "ip_range": "10.0.1.0/24"})),
			invalidField("subnets[0].type", "field contains invalid value")},
		"an unknown zone": {network("10.0.0.0/16", subnets(map[string]any{"type": "cloud", "network_zone": "mars", "ip_range": "10.0.1.0/24"})),
			invalidField("subnets[0].network_zone", "network zone does not exist")},
		"a vSwitch without an ID": {network("10.0.0.0/16", subnets(map[string]any{"type": "vswitch", "network_zone": "eu-central", "ip_range": "10.0.1.0/24"})),
			invalidField("subnets[0].vswitch_id", "Field must not be empty")},
		// the lab has no Robot, so it knows no vSwitch
		"a vSwitch": {network("10.0.0.0/16", subnets(map[string]any{"type": "vswitch", "network_zone": "eu-central", "ip_range": "10.0.1.0/24", "vswitch_id": 1000})),
			refused("service_error", "vswitch not found")},
		"two zones": {network("10.0.0.0/16", subnets(cloudSubnet("10.0.1.0/24"), map[string]any{"type": "cloud", "network_zone": "us-east", "ip_range": "10.0.2.0/24"})),
			invalid("subnetwork zones are not aligned")},
		"a public route":          {network("10.0.0.0/16", route("8.8.8.0/24", "10.0.0.2")), invalid("invalid route")},
		"a route's host bits":     {network("10.0.0.0/16", route("10.100.0.1/24", "10.0.0.2")), invalid("invalid route")},
		"the gateway first":       {network("10.0.0.0/16", route("10.100.0.0/24", "10.0.0.1")), invalid("invalid gateway")},
		"a gateway outside":       {network("10.0.0.0/16", route("10.100.0.0/24", "10.1.0.2")), invalid("invalid gateway")},
		"the public gateway":      {network("10.0.0.0/16", route("10.100.0.0/24", "172.31.1.1")), invalid("invalid gateway")},
		"to the public gateway":   {network("10.0.0.0/16", route("172.31.1.1/32", "10.0.0.2")), invalid("invalid gateway")},
		"a route without gateway": {network("10.0.0.0/16", map[string]any{"routes": []map[string]any{{"destination": "10.100.0.0/24"}}}), invalidField("routes[0].gateway", "'routes[0].gateway' is required")},
		"a route over a subnet":   {network("10.0.0.0/16", map[string]any{"subnets": []map[string]any{cloudSubnet("10.0.1.0/24")}, "routes": []map[string]any{{"destination": "10.0.1.0/25", "gateway": "10.0.0.2"}}}), invalid("ip ranges of subnetworks or routes overlap")},
		"51 subnets":              {network("10.0.0.0/8", map[string]any{"subnets": many(51)}), networkError{http.StatusForbidden, "resource_limit_exceeded", "subnet limit reached", nil}},
	})
	created := createNetwork(t, test, map[string]any{"name": "net", "ip_range": "10.0.0.0/16"})
	expectErrors(t, test, "/v1/networks", map[string]struct {
		body any
		want networkError
	}{
		"a name taken": {map[string]any{"name": created.Name, "ip_range": "10.0.0.0/16"}, networkError{http.StatusConflict, "uniqueness_error", "name is already used", nil}},
	})
	// Hetzner takes these (measured 2026-11-08)
	for name, body := range map[string]map[string]any{
		"a name with a space":    {"name": "a b", "ip_range": "10.0.0.0/16"},
		"the same range again":   {"name": "again", "ip_range": "10.0.0.0/16"},
		"a /30 subnet":           {"name": "s30", "ip_range": "10.0.0.0/16", "subnets": []map[string]any{cloudSubnet("10.0.0.0/30")}},
		"the type server":        {"name": "srv", "ip_range": "10.0.0.0/16", "subnets": []map[string]any{{"type": "server", "network_zone": "eu-central", "ip_range": "10.0.1.0/24"}}},
		"a cloud's vswitch_id":   {"name": "vs", "ip_range": "10.0.0.0/16", "subnets": []map[string]any{{"type": "cloud", "network_zone": "eu-central", "ip_range": "10.0.1.0/24", "vswitch_id": 1000}}},
		"a default route":        {"name": "r0", "ip_range": "10.0.0.0/16", "subnets": []map[string]any{cloudSubnet("10.0.1.0/24")}, "routes": []map[string]any{{"destination": "0.0.0.0/0", "gateway": "10.0.0.2"}}},
		"a route in the range":   {"name": "rin", "ip_range": "10.0.0.0/16", "routes": []map[string]any{{"destination": "10.0.5.0/24", "gateway": "10.0.0.2"}}},
		"a subnet's first as gw": {"name": "gw", "ip_range": "10.0.0.0/16", "subnets": []map[string]any{cloudSubnet("10.0.1.0/24")}, "routes": []map[string]any{{"destination": "10.100.0.0/24", "gateway": "10.0.1.1"}}},
		"a /24 in 192.168":       {"name": "c", "ip_range": "192.168.0.0/24"},
		"all of 172.16.0.0/12":   {"name": "b", "ip_range": "172.16.0.0/12"},
		"50 subnets and 100 routes": {"name": "full", "ip_range": "10.0.0.0/8", "subnets": many(50), "routes": func() []map[string]any {
			var routes []map[string]any
			for i := range 100 {
				routes = append(routes, map[string]any{"destination": fmt.Sprintf("172.16.%d.0/24", i), "gateway": "10.0.0.2"})
			}
			return routes
		}()},
	} {
		if response := test.client.Do(http.MethodPost, "/v1/networks", body); response.Status != http.StatusCreated {
			t.Errorf("%s: %d %s", name, response.Status, response.Body)
		}
	}
	vs := getNetwork(t, test, networkByName(t, test, "vs"))
	if vs.Subnets[0].VSwitchID != nil {
		t.Errorf("a cloud subnet kept the vswitch_id %d", *vs.Subnets[0].VSwitchID)
	}
	routes := map[string]any{"name": "routes", "ip_range": "10.0.0.0/8", "routes": []map[string]any{}}
	for i := range 101 {
		routes["routes"] = append(routes["routes"].([]map[string]any), map[string]any{"destination": fmt.Sprintf("172.16.%d.0/24", i), "gateway": "10.0.0.2"})
	}
	expectError(t, "101 routes", &responseOf{test.client.Do(http.MethodPost, "/v1/networks", routes)},
		http.StatusForbidden, "resource_limit_exceeded", "route limit reached")
}

// networkByName returns the ID of the network with a name.
func networkByName(t *testing.T, test *testLab, name string) int64 {
	t.Helper()
	var answer struct {
		Networks []networkAnswer `json:"networks"`
	}
	test.client.Get("/v1/networks?name="+name).Decode(t, &answer)
	if len(answer.Networks) != 1 {
		t.Fatalf("networks named %s: %+v", name, answer.Networks)
	}
	return answer.Networks[0].ID
}

// A network's JSON, list, update, protection and delete, as Hetzner
// answers (measured 2026-11-08).
func TestNetworkLifeCycle(t *testing.T) {
	test := newTestLab(t)
	created := createNetwork(t, test, map[string]any{
		"name": "net", "ip_range": "10.0.0.0/16", "labels": map[string]string{"env": "dev"},
		"subnets": []map[string]any{cloudSubnet("10.0.2.0/24"), cloudSubnet("10.0.1.0/24")},
		"routes":  []map[string]any{{"destination": "10.100.0.0/24", "gateway": "10.0.1.10"}},
	})
	// subnets in the order of their ranges, each with the network's first
	// address as its gateway and vswitch_id null
	if len(created.Subnets) != 2 || created.Subnets[0].IPRange != "10.0.1.0/24" || created.Subnets[0].Gateway != "10.0.0.1" ||
		created.Subnets[1].Gateway != "10.0.0.1" || created.Subnets[0].VSwitchID != nil {
		t.Errorf("the subnets are %+v", created.Subnets)
	}
	if len(created.Routes) != 1 || len(created.Servers) != 0 || len(created.LoadBalancers) != 0 || created.ExposeRoutesToVSwitch {
		t.Errorf("created %+v", created)
	}
	other := createNetwork(t, test, map[string]any{"name": "other", "ip_range": "10.1.0.0/16"})

	var list struct {
		Networks []networkAnswer `json:"networks"`
	}
	test.client.Get("/v1/networks?label_selector=env%3Ddev").Decode(t, &list)
	if len(list.Networks) != 1 || list.Networks[0].ID != created.ID {
		t.Errorf("the selector found %+v", list.Networks)
	}
	test.client.Get("/v1/networks?sort=name:desc").Decode(t, &list)
	if len(list.Networks) != 2 || list.Networks[0].ID != other.ID {
		t.Errorf("sorted by name, descending: %+v", list.Networks)
	}
	// an unknown sort value is ignored
	if response := test.client.Get("/v1/networks?sort=ip_range"); response.Status != http.StatusOK {
		t.Errorf("an unknown sort value: %d", response.Status)
	}
	expectError(t, "an unknown network", &responseOf{test.client.Get("/v1/networks/1")}, http.StatusNotFound, "not_found", "entity not found")

	path := fmt.Sprintf("/v1/networks/%d", created.ID)
	var updated struct {
		Network networkAnswer `json:"network"`
	}
	test.client.Do(http.MethodPut, path, map[string]any{"name": "renamed", "expose_routes_to_vswitch": true}).Decode(t, &updated)
	if updated.Network.Name != "renamed" || !updated.Network.ExposeRoutesToVSwitch || updated.Network.Labels["env"] != "dev" {
		t.Errorf("updated %+v", updated.Network)
	}
	expectError(t, "an empty name", &responseOf{test.client.Do(http.MethodPut, path, map[string]any{"name": ""})},
		http.StatusUnprocessableEntity, "invalid_input", "invalid input in field 'name'")
	expectError(t, "a name taken", &responseOf{test.client.Do(http.MethodPut, path, map[string]any{"name": "other"})},
		http.StatusConflict, "uniqueness_error", "name is already used")

	actionOf(t, "protect", networkAction(test, created.ID, "change_protection", map[string]any{"delete": true}))
	expectError(t, "a protected delete", &responseOf{test.client.Do(http.MethodDelete, path, nil)},
		http.StatusLocked, "protected", "network is delete protected")
	actionOf(t, "change_protection without a field", networkAction(test, created.ID, "change_protection", map[string]any{}))
	actionOf(t, "unprotect", networkAction(test, created.ID, "change_protection", map[string]any{"delete": false}))
	if response := test.client.Do(http.MethodDelete, path, nil); response.Status != http.StatusNoContent {
		t.Errorf("the delete answered %d %s", response.Status, response.Body)
	}
	expectError(t, "the deleted network", &responseOf{test.client.Get(path)}, http.StatusNotFound, "not_found", "entity not found")
}

// The network's own actions and their errors, as Hetzner answers them
// (measured 2026-11-08).
func TestNetworkSubnetsRoutesAndRange(t *testing.T) {
	test := newTestLab(t)
	network := createNetwork(t, test, map[string]any{
		"name": "net", "ip_range": "10.0.0.0/16",
		"subnets": []map[string]any{cloudSubnet("10.0.1.0/24"), cloudSubnet("10.0.2.0/24")},
		"routes":  []map[string]any{{"destination": "10.100.0.0/24", "gateway": "10.0.1.10"}},
	})
	id := network.ID
	add := actionOf(t, "add_subnet", networkAction(test, id, "add_subnet", cloudSubnet("10.0.3.0/24")))
	if add.Command != "add_subnet" || len(add.Resources) != 1 || add.Resources[0].Type != "network" {
		t.Errorf("the add_subnet is %+v", add)
	}
	// without a range: the first free /24
	waitActions(t, test, add, actionOf(t, "add_subnet", networkAction(test, id, "add_subnet", map[string]any{"type": "cloud", "network_zone": "eu-central"})))
	if got := getNetwork(t, test, id).Subnets; len(got) != 4 || got[0].IPRange != "10.0.0.0/24" {
		t.Errorf("after the add_subnets: %+v", got)
	}
	expectErrors(t, test, fmt.Sprintf("/v1/networks/%d/actions/add_subnet", id), map[string]struct {
		body any
		want networkError
	}{
		"the same subnet": {cloudSubnet("10.0.3.0/24"), invalid("ip range overlaps with existing subnetwork or route")},
		"an overlap":      {cloudSubnet("10.0.3.0/25"), invalid("ip range overlaps with existing subnetwork or route")},
		"outside":         {cloudSubnet("10.9.0.0/24"), invalid("invalid subnetwork")},
		"no type nor zone": {map[string]any{"ip_range": "10.0.7.0/24"}, networkError{http.StatusUnprocessableEntity, "invalid_input",
			"invalid input in fields 'network_zone', 'type'", map[string]string{"network_zone": "'network_zone' is required", "type": "'type' is required"}}},
		"a vSwitch": {map[string]any{"type": "vswitch", "network_zone": "eu-central", "ip_range": "10.0.8.0/24", "vswitch_id": 1000},
			refused("service_error", "vswitch not found")},
	})
	waitActions(t, test, actionOf(t, "delete_subnet", networkAction(test, id, "delete_subnet", map[string]any{"ip_range": "10.0.3.0/24"})))
	expectErrors(t, test, fmt.Sprintf("/v1/networks/%d/actions/delete_subnet", id), map[string]struct {
		body any
		want networkError
	}{
		"an unknown subnet": {map[string]any{"ip_range": "10.0.3.0/24"}, refused("service_error", "subnet not found")},
		"no range":          {map[string]any{}, invalidField("ip_range", "'ip_range' is required")},
		"no CIDR":           {map[string]any{"ip_range": "nope"}, invalidField("ip_range", "cidrv4 violated")},
	})

	waitActions(t, test, actionOf(t, "add_route", networkAction(test, id, "add_route", map[string]any{"destination": "10.101.0.0/24", "gateway": "10.0.1.11"})),
		// a gateway in no subnet
		actionOf(t, "add_route", networkAction(test, id, "add_route", map[string]any{"destination": "10.102.0.0/24", "gateway": "10.0.200.2"})))
	expectErrors(t, test, fmt.Sprintf("/v1/networks/%d/actions/add_route", id), map[string]struct {
		body any
		want networkError
	}{
		"the same destination": {map[string]any{"destination": "10.101.0.0/24", "gateway": "10.0.1.12"}, invalid("ip range overlaps with existing subnetwork or route")},
		"over a subnet":        {map[string]any{"destination": "10.0.1.0/24", "gateway": "10.0.2.2"}, invalid("ip range overlaps with existing subnetwork or route")},
		"no gateway":           {map[string]any{"destination": "10.103.0.0/24"}, invalidField("gateway", "'gateway' is required")},
	})
	waitActions(t, test, actionOf(t, "delete_route", networkAction(test, id, "delete_route", map[string]any{"destination": "10.101.0.0/24", "gateway": "10.0.1.11"})))
	expectErrors(t, test, fmt.Sprintf("/v1/networks/%d/actions/delete_route", id), map[string]struct {
		body any
		want networkError
	}{
		"an unknown route": {map[string]any{"destination": "10.101.0.0/24", "gateway": "10.0.1.11"}, refused("service_error", "route not found")},
		"another gateway":  {map[string]any{"destination": "10.100.0.0/24", "gateway": "10.0.1.99"}, refused("service_error", "route not found")},
		"no gateway":       {map[string]any{"destination": "10.100.0.0/24"}, invalidField("gateway", "'gateway' is required")},
	})

	waitActions(t, test, actionOf(t, "change_ip_range", networkAction(test, id, "change_ip_range", map[string]any{"ip_range": "10.0.0.0/15"})))
	expectErrors(t, test, fmt.Sprintf("/v1/networks/%d/actions/change_ip_range", id), map[string]struct {
		body any
		want networkError
	}{
		"smaller":   {map[string]any{"ip_range": "10.0.0.0/16"}, invalid("invalid ip range")},
		"the same":  {map[string]any{"ip_range": "10.0.0.0/15"}, invalid("invalid ip range")},
		"elsewhere": {map[string]any{"ip_range": "10.2.0.0/15"}, invalid("invalid ip range")},
		"public":    {map[string]any{"ip_range": "8.0.0.0/7"}, invalid("invalid ip range")},
		"too large": {map[string]any{"ip_range": "10.0.0.0/7"}, invalid("invalid ip range")},
	})
	got := getNetwork(t, test, id)
	if got.IPRange != "10.0.0.0/15" || len(got.Subnets) != 3 || len(got.Routes) != 2 {
		t.Errorf("at the end the network is %+v", got)
	}

	full := createNetwork(t, test, map[string]any{"name": "full", "ip_range": "10.0.0.0/8", "subnets": func() []map[string]any {
		var list []map[string]any
		for i := range 50 {
			list = append(list, cloudSubnet(fmt.Sprintf("10.%d.0.0/24", i+1)))
		}
		return list
	}()})
	expectError(t, "a 51st subnet", networkAction(test, full.ID, "add_subnet", cloudSubnet("10.250.0.0/24")),
		http.StatusForbidden, "resource_limit_exceeded", "subnet limit reached")
}

// offServer creates a server that does not start and waits for its create.
func offServer(t *testing.T, test *testLab, name string) createAnswer {
	t.Helper()
	created := createServer(t, test, map[string]any{"name": name, "server_type": "cpx22", "image": "debian-13", "start_after_create": false})
	waitActions(t, test, created.Action)
	return created
}

// The errors of attach_to_network, as Hetzner answers them (measured
// 2026-11-08): the fields, the network, the alias IPs' count, an attach
// to the network already, the server's other networks, then the
// addresses.
func TestAttachToNetworkErrors(t *testing.T) {
	test := newTestLab(t)
	first, second := offServer(t, test, "first"), offServer(t, test, "second")
	n0 := createNetwork(t, test, map[string]any{"name": "n0", "ip_range": "10.0.0.0/16",
		"subnets": []map[string]any{cloudSubnet("10.0.1.0/24"), cloudSubnet("10.0.2.0/24")}}).ID
	nosub := createNetwork(t, test, map[string]any{"name": "nosub", "ip_range": "10.4.0.0/16"}).ID
	waitActions(t, test, actionOf(t, "attach", serverNetworkAction(test, second.Server.ID, "attach_to_network",
		map[string]any{"network": n0, "ip": "10.0.1.2", "alias_ips": []string{"10.0.1.20"}})))
	with := func(extra map[string]any) map[string]any {
		body := map[string]any{"network": n0}
		for key, value := range extra {
			body[key] = value
		}
		return body
	}
	noSubnet := refused("no_subnet_available", "no subnet or IP available")
	notAvailable := refused("ip_not_available", "IP not available")
	expectErrors(t, test, fmt.Sprintf("/v1/servers/%d/actions/attach_to_network", first.Server.ID), map[string]struct {
		body any
		want networkError
	}{
		"no network":           {map[string]any{}, invalidField("network", "'network' is required")},
		"an unknown network":   {map[string]any{"network": 1}, networkError{http.StatusNotFound, "not_found", "entity not found", nil}},
		"no subnet":            {map[string]any{"network": nosub}, noSubnet},
		"an IP outside":        {with(map[string]any{"ip": "10.9.0.5"}), noSubnet},
		"an IP in no subnet":   {with(map[string]any{"ip": "10.0.5.5"}), noSubnet},
		"the gateway":          {with(map[string]any{"ip": "10.0.0.1"}), noSubnet},
		"a subnet's own":       {with(map[string]any{"ip": "10.0.1.0"}), notAvailable},
		"a subnet's last":      {with(map[string]any{"ip": "10.0.1.255"}), notAvailable},
		"an IP taken":          {with(map[string]any{"ip": "10.0.1.2"}), notAvailable},
		"another's alias":      {with(map[string]any{"ip": "10.0.1.20"}), notAvailable},
		"an alias taken":       {with(map[string]any{"alias_ips": []string{"10.0.1.20"}}), notAvailable},
		"an alias of its own":  {with(map[string]any{"ip": "10.0.1.5", "alias_ips": []string{"10.0.1.5"}}), notAvailable},
		"an alias outside":     {with(map[string]any{"alias_ips": []string{"10.9.0.5"}}), noSubnet},
		"an alias twice":       {with(map[string]any{"alias_ips": []string{"10.0.1.6", "10.0.1.6"}}), noSubnet},
		"the gateway as alias": {with(map[string]any{"alias_ips": []string{"10.0.0.1"}}), noSubnet},
		"no IP":                {with(map[string]any{"ip": "nope"}), invalidField("ip", "must be a valid IP address")},
		"no CIDR":              {with(map[string]any{"ip_range": "nope"}), invalidField("ip_range", "cidrv4 violated")},
		"no alias IP":          {with(map[string]any{"alias_ips": []string{"nope"}}), invalidField("alias_ips[0]", "must be a valid IP address")},
		"an unknown subnet":    {with(map[string]any{"ip_range": "10.0.9.0/24"}), refused("service_error", "subnet not found")},
		"an IP in another subnet": {with(map[string]any{"ip": "10.0.1.5", "ip_range": "10.0.2.0/24"}),
			refused("service_error", "ip not in ip range")},
		"six alias IPs": {with(map[string]any{"alias_ips": []string{"10.0.1.30", "10.0.1.31", "10.0.1.32", "10.0.1.33", "10.0.1.34", "10.0.1.35"}}),
			networkError{http.StatusForbidden, "invalid_input", "alias ips limit reached", nil}},
	})
	expectErrors(t, test, fmt.Sprintf("/v1/servers/%d/actions/attach_to_network", second.Server.ID), map[string]struct {
		body any
		want networkError
	}{
		"attached already": {with(nil), refused("server_already_attached", "server already attached")},
		// the count comes before the attach (measured 2026-11-08)
		"attached with six alias IPs": {with(map[string]any{"alias_ips": []string{"10.0.1.30", "10.0.1.31", "10.0.1.32", "10.0.1.33", "10.0.1.34", "10.0.1.35"}}),
			networkError{http.StatusForbidden, "invalid_input", "alias ips limit reached", nil}},
	})
	overlap := createNetwork(t, test, map[string]any{"name": "overlap", "ip_range": "10.0.0.0/16", "subnets": []map[string]any{cloudSubnet("10.0.1.0/24")}}).ID
	expectError(t, "an overlap", serverNetworkAction(test, second.Server.ID, "attach_to_network", map[string]any{"network": overlap}),
		http.StatusUnprocessableEntity, "networks_overlap", "network IP range overlaps with one of the resource networks")
	for i := 1; i <= 3; i++ {
		other := createNetwork(t, test, map[string]any{"name": fmt.Sprint("n", i), "ip_range": fmt.Sprintf("10.%d.0.0/16", i),
			"subnets": []map[string]any{cloudSubnet(fmt.Sprintf("10.%d.1.0/24", i))}}).ID
		response := serverNetworkAction(test, second.Server.ID, "attach_to_network", map[string]any{"network": other})
		if i < 3 {
			waitActions(t, test, actionOf(t, "attach", response))
			continue
		}
		expectError(t, "a fourth network", response, http.StatusForbidden, "resource_limit_exceeded", "networks per resource limit reached")
	}
}

// A server joins a network, changes its alias IPs and leaves; the
// appliance routes its addresses in the network's VRF and the VM has a
// NIC in the network's VLAN while it is in it.
func TestServerJoinsAndLeavesANetwork(t *testing.T) {
	test := newTestLab(t)
	agent := quietMachine(t, test)
	first, second := offServer(t, test, "first"), offServer(t, test, "second")
	network := createNetwork(t, test, map[string]any{"name": "net", "ip_range": "10.0.0.0/16",
		"subnets": []map[string]any{cloudSubnet("10.0.1.0/24"), cloudSubnet("10.0.2.0/24")}})

	attach := actionOf(t, "attach", serverNetworkAction(test, first.Server.ID, "attach_to_network",
		map[string]any{"network": network.ID, "alias_ips": []string{"10.0.1.20"}}))
	if attach.Command != "attach_to_network" || len(attach.Resources) != 2 ||
		attach.Resources[0].Type != "server" || attach.Resources[1].Type != "network" {
		t.Errorf("the attach is %+v", attach)
	}
	// the second server without an address: the last subnet's first
	waitActions(t, test, attach, actionOf(t, "attach", serverNetworkAction(test, second.Server.ID, "attach_to_network",
		map[string]any{"network": network.ID, "ip_range": "10.0.1.0/24"})))
	server := getServer(t, test, first.Server.ID)
	if len(server.PrivateNet) != 1 || server.PrivateNet[0].IP != "10.0.2.1" || server.PrivateNet[0].Network != network.ID ||
		!slices.Equal(server.PrivateNet[0].AliasIPs, []string{"10.0.1.20"}) || !strings.HasPrefix(server.PrivateNet[0].MACAddress, "86:00:") {
		t.Errorf("the first server's private_net is %+v", server.PrivateNet)
	}
	if got := getServer(t, test, second.Server.ID).PrivateNet; len(got) != 1 || got[0].IP != "10.0.1.1" {
		t.Errorf("the second server's private_net is %+v", got)
	}
	if got := getNetwork(t, test, network.ID).Servers; !slices.Equal(got, []int64{first.Server.ID, second.Server.ID}) {
		t.Errorf("the network's servers are %v", got)
	}
	mac := server.PrivateNet[0].MACAddress
	if !slices.Contains(test.machine.Calls(), fmt.Sprintf("AttachNIC %s %s", lab.ServerVM(first.Server.ID), mac)) {
		t.Errorf("the VM got no NIC %s: %v", mac, test.machine.Calls())
	}
	waitForPicture(t, agent, agentproto.PictureLinks, `"destination":"10.0.1.20/32","device":"private.`, true)
	waitForPicture(t, agent, agentproto.PictureDHCP, "dhcp-host="+mac+",10.0.2.1", true)

	var members struct {
		Members []schema.NetworkMember `json:"members"`
	}
	test.client.Get(fmt.Sprintf("/v1/networks/%d/members?sort=ip:desc", network.ID)).Decode(t, &members)
	if len(members.Members) != 2 || members.Members[0].ID != first.Server.ID || members.Members[0].Subnet != "10.0.2.0/24" ||
		members.Members[0].Status != "ok" || members.Members[0].Type != "server" {
		t.Errorf("the members are %+v", members.Members)
	}
	test.client.Get(fmt.Sprintf("/v1/networks/%d/members?subnet=10.0.1.0/24&type=server", network.ID)).Decode(t, &members)
	if len(members.Members) != 1 || members.Members[0].ID != second.Server.ID {
		t.Errorf("the members in 10.0.1.0/24 are %+v", members.Members)
	}
	membersPath := fmt.Sprintf("/v1/networks/%d/members", network.ID)
	expectError(t, "an unknown type", &responseOf{test.client.Get(membersPath + "?type=foo")},
		http.StatusUnprocessableEntity, "invalid_input", "invalid input in field 'type'")
	expectError(t, "an unknown status", &responseOf{test.client.Get(membersPath + "?status=foo")},
		http.StatusUnprocessableEntity, "invalid_input", "invalid input in field 'status'")
	expectError(t, "no subnet", &responseOf{test.client.Get(membersPath + "?subnet=nope")},
		http.StatusUnprocessableEntity, "invalid_input", "invalid input in field 'filters.subnets[0]'")

	changePath := fmt.Sprintf("/v1/servers/%d/actions/change_alias_ips", first.Server.ID)
	other := createNetwork(t, test, map[string]any{"name": "other", "ip_range": "10.3.0.0/16"}).ID
	expectErrors(t, test, changePath, map[string]struct {
		body any
		want networkError
	}{
		"no alias_ips":           {map[string]any{"network": network.ID}, invalidField("alias_ips", "'alias_ips' is required")},
		"another's address":      {map[string]any{"network": network.ID, "alias_ips": []string{"10.0.1.1"}}, refused("ip_not_available", "IP not available")},
		"its own address":        {map[string]any{"network": network.ID, "alias_ips": []string{"10.0.2.1"}}, refused("ip_not_available", "IP not available")},
		"an alias twice":         {map[string]any{"network": network.ID, "alias_ips": []string{"10.0.2.21", "10.0.2.21"}}, refused("no_subnet_available", "no subnet or IP available")},
		"outside":                {map[string]any{"network": network.ID, "alias_ips": []string{"10.9.0.1"}}, refused("no_subnet_available", "no subnet or IP available")},
		"no IP":                  {map[string]any{"network": network.ID, "alias_ips": []string{"nope"}}, invalidField("alias_ips[0]", "must be a valid IP address")},
		"an unknown network":     {map[string]any{"network": 1, "alias_ips": []string{}}, networkError{http.StatusNotFound, "not_found", "entity not found", nil}},
		"a network it is not in": {map[string]any{"network": other, "alias_ips": []string{"10.3.1.9"}}, refused("service_error", "resource not found")},
		"six": {map[string]any{"network": network.ID, "alias_ips": []string{"10.0.1.30", "10.0.1.31", "10.0.1.32", "10.0.1.33", "10.0.1.34", "10.0.1.35"}},
			invalid("alias ips limit reached")},
	})
	// an empty list for a network it is not in changes nothing
	if empty := actionOf(t, "an empty change", serverNetworkAction(test, first.Server.ID, "change_alias_ips",
		map[string]any{"network": other, "alias_ips": []string{}})); empty.Status != "success" {
		t.Errorf("an empty change for a network the server is not in is %s", empty.Status)
	}
	waitActions(t, test, actionOf(t, "change", serverNetworkAction(test, first.Server.ID, "change_alias_ips",
		map[string]any{"network": network.ID, "alias_ips": []string{"10.0.2.21", "10.0.1.21"}})))
	if got := getServer(t, test, first.Server.ID).PrivateNet[0].AliasIPs; !slices.Equal(got, []string{"10.0.2.21", "10.0.1.21"}) {
		t.Errorf("the alias IPs are %v", got)
	}
	waitForPicture(t, agent, agentproto.PictureLinks, `"destination":"10.0.1.20/32"`, false)
	waitForPicture(t, agent, agentproto.PictureLinks, `"destination":"10.0.1.21/32"`, true)

	detachPath := fmt.Sprintf("/v1/servers/%d/actions/detach_from_network", first.Server.ID)
	expectErrors(t, test, detachPath, map[string]struct {
		body any
		want networkError
	}{
		"no network":             {map[string]any{}, invalidField("network", "'network' is required")},
		"an unknown network":     {map[string]any{"network": 1}, networkError{http.StatusNotFound, "not_found", "entity not found", nil}},
		"a network it is not in": {map[string]any{"network": other}, refused("service_error", "resource not found")},
	})
	waitActions(t, test, actionOf(t, "detach", serverNetworkAction(test, first.Server.ID, "detach_from_network", map[string]any{"network": network.ID})))
	if got := getServer(t, test, first.Server.ID).PrivateNet; len(got) != 0 {
		t.Errorf("after the detach the private_net is %+v", got)
	}
	if !slices.Contains(test.machine.Calls(), fmt.Sprintf("DetachNIC %s %s", lab.ServerVM(first.Server.ID), mac)) {
		t.Errorf("the NIC %s stayed in the VM: %v", mac, test.machine.Calls())
	}
	waitForPicture(t, agent, agentproto.PictureLinks, `"destination":"10.0.2.1/32"`, false)

	// the actions are the network's, not the server's (measured 2026-11-08)
	var actions schema.ActionListResponse
	test.client.Get(fmt.Sprintf("/v1/servers/%d/actions", first.Server.ID)).Decode(t, &actions)
	for _, action := range actions.Actions {
		if action.Command != "create_server" {
			t.Errorf("the server's actions list %s", action.Command)
		}
	}
	test.client.Get(fmt.Sprintf("/v1/networks/%d/actions", network.ID)).Decode(t, &actions)
	if len(actions.Actions) != 4 {
		t.Errorf("the network's actions are %+v", actions.Actions)
	}
	expectError(t, "a range change with a server in it", networkAction(test, network.ID, "change_ip_range", map[string]any{"ip_range": "10.0.0.0/15"}),
		http.StatusUnprocessableEntity, "service_error", "network has attached resources")
	expectError(t, "deleting the server's subnet", networkAction(test, network.ID, "delete_subnet", map[string]any{"ip_range": "10.0.1.0/24"}),
		http.StatusUnprocessableEntity, "service_error", "network has attached resources")
}

// A network's delete takes it from the servers in it at once, without an
// action of theirs, as on Hetzner (measured 2026-11-08); their NICs leave
// the VMs.
func TestDeletingANetworkDetachesItsServers(t *testing.T) {
	test := newTestLab(t)
	agent := quietMachine(t, test)
	server := offServer(t, test, "web")
	network := createNetwork(t, test, map[string]any{"name": "net", "ip_range": "10.0.0.0/16", "subnets": []map[string]any{cloudSubnet("10.0.1.0/24")}})
	waitActions(t, test, actionOf(t, "attach", serverNetworkAction(test, server.Server.ID, "attach_to_network", map[string]any{"network": network.ID})))
	mac := getServer(t, test, server.Server.ID).PrivateNet[0].MACAddress
	waitForPicture(t, agent, agentproto.PictureLinks, `"vrf-net-`+fmt.Sprint(network.ID)+`"`, true)

	if response := test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/networks/%d", network.ID), nil); response.Status != http.StatusNoContent {
		t.Fatalf("the delete answered %d %s", response.Status, response.Body)
	}
	if got := getServer(t, test, server.Server.ID).PrivateNet; len(got) != 0 {
		t.Errorf("after the network's delete the server's private_net is %+v", got)
	}
	waitForPicture(t, agent, agentproto.PictureLinks, `"vrf-net-`+fmt.Sprint(network.ID)+`"`, false)
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(test.machine.Calls(), fmt.Sprintf("DetachNIC %s %s", lab.ServerVM(server.Server.ID), mac)) {
		if time.Now().After(deadline) {
			t.Fatalf("the NIC %s stayed in the VM: %v", mac, test.machine.Calls())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A failed attach takes the server's NIC out of the record again, and the
// appliance's pictures with it.
func TestFailedAttachLeavesNoTrace(t *testing.T) {
	test := newTestLab(t)
	agent := quietMachine(t, test)
	server := offServer(t, test, "web")
	network := createNetwork(t, test, map[string]any{"name": "net", "ip_range": "10.0.0.0/16", "subnets": []map[string]any{cloudSubnet("10.0.1.0/24")}})
	// the network's create sends its pictures in the background; the
	// failure must meet the attach's. The attach changes the links and
	// DHCP, not the metadata.
	waitForPicture(t, agent, agentproto.PictureDHCP, fmt.Sprint("private network ", network.ID), true)
	agent.FailNext(agentproto.PictureDHCP)
	attach := actionOf(t, "attach", serverNetworkAction(test, server.Server.ID, "attach_to_network", map[string]any{"network": network.ID}))
	expectFailed(t, test, attach)
	if got := getServer(t, test, server.Server.ID).PrivateNet; len(got) != 0 {
		t.Errorf("after the failed attach the private_net is %+v", got)
	}
	waitForPicture(t, agent, agentproto.PictureLinks, `"destination":"10.0.1.1/32"`, false)
	nics := 0
	for _, call := range test.machine.Calls() {
		if strings.HasPrefix(call, "AttachNIC "+lab.ServerVM(server.Server.ID)+" 86:00:") {
			nics++
		} else if strings.HasPrefix(call, "DetachNIC "+lab.ServerVM(server.Server.ID)+" 86:00:") {
			nics--
		}
	}
	if nics != 0 {
		t.Errorf("the VM kept %d NICs of the failed attach: %v", nics, test.machine.Calls())
	}
}

// handClock is a clock that moves only when the test advances it; its
// waits end at once, so that actions do not take Hetzner's times.
type handClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *handClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *handClock) After(time.Duration) <-chan time.Time {
	fire := make(chan time.Time, 1)
	fire <- c.Now()
	return fire
}

// Advance moves the clock forward.
func (c *handClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// A server that leaves a network stays among its members as "detaching"
// for 5 s, and the other members show "updating" from 2 s after for 10 s,
// as on Hetzner (measured 2026-11-08 and 2026-11-09).
func TestMembersShowAServerLeavingAsHetznerDoes(t *testing.T) {
	clock := &handClock{now: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)}
	test := newTimedTestLab(t, clock, 1)
	first, second := offServer(t, test, "first"), offServer(t, test, "second")
	network := createNetwork(t, test, map[string]any{"name": "net", "ip_range": "10.0.0.0/16", "subnets": []map[string]any{cloudSubnet("10.0.1.0/24")}})
	for _, server := range []createAnswer{first, second} {
		waitActions(t, test, actionOf(t, "attach", serverNetworkAction(test, server.Server.ID, "attach_to_network", map[string]any{"network": network.ID})))
	}
	clock.Advance(time.Minute) // the attaches' notes are over
	waitActions(t, test, actionOf(t, "detach", serverNetworkAction(test, first.Server.ID, "detach_from_network", map[string]any{"network": network.ID})))
	statuses := func() map[int64]string {
		var answer struct {
			Members []schema.NetworkMember `json:"members"`
		}
		test.client.Get(fmt.Sprintf("/v1/networks/%d/members", network.ID)).Decode(t, &answer)
		got := map[int64]string{}
		for _, member := range answer.Members {
			got[member.ID] = member.Status
		}
		return got
	}
	for _, step := range []struct {
		advance       time.Duration
		first, second string // "" for not a member
	}{
		{0, "detaching", "ok"},
		{2 * time.Second, "detaching", "updating"},
		{3 * time.Second, "", "updating"}, // 5 s
		{6 * time.Second, "", "updating"}, // 11 s
		{time.Second, "", "ok"},           // 12 s
	} {
		clock.Advance(step.advance)
		got := statuses()
		if got[first.Server.ID] != step.first || got[second.Server.ID] != step.second {
			t.Errorf("at %s the members are %v, want first %q and second %q", clock.Now().Format(time.TimeOnly), got, step.first, step.second)
		}
	}
	// a detaching member is listed with its address, and filtered by status
	waitActions(t, test, actionOf(t, "attach", serverNetworkAction(test, first.Server.ID, "attach_to_network", map[string]any{"network": network.ID})))
	clock.Advance(time.Minute)
	test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/servers/%d", second.Server.ID), nil)
	var detaching struct {
		Members []schema.NetworkMember `json:"members"`
	}
	test.client.Get(fmt.Sprintf("/v1/networks/%d/members?status=detaching", network.ID)).Decode(t, &detaching)
	if len(detaching.Members) != 1 || detaching.Members[0].ID != second.Server.ID || detaching.Members[0].IP == "" || detaching.Members[0].Subnet != "10.0.1.0/24" {
		t.Errorf("after the second server's delete the detaching members are %+v", detaching.Members)
	}
}

// add_subnet without ip_range takes the first free /24, else the first free
// block of the next size down; a network without one is refused (measured
// 2026-11-09).
func TestAddSubnetWithoutRangeTakesWhatIsFree(t *testing.T) {
	test := newTestLab(t)
	half := createNetwork(t, test, map[string]any{"name": "half", "ip_range": "10.70.0.0/24",
		"subnets": []map[string]any{cloudSubnet("10.70.0.0/25")}}).ID
	actionOf(t, "add_subnet", networkAction(test, half, "add_subnet", map[string]any{"type": "cloud", "network_zone": "eu-central"}))
	var ranges []string
	for _, subnet := range getNetwork(t, test, half).Subnets {
		ranges = append(ranges, subnet.IPRange)
	}
	if !slices.Contains(ranges, "10.70.0.128/25") {
		t.Errorf("the subnets are %v, want 10.70.0.128/25 among them", ranges)
	}
	full := createNetwork(t, test, map[string]any{"name": "full", "ip_range": "10.72.0.0/22", "subnets": []map[string]any{
		cloudSubnet("10.72.0.0/24"), cloudSubnet("10.72.1.0/24"), cloudSubnet("10.72.2.0/24"), cloudSubnet("10.72.3.0/24")}}).ID
	expectError(t, "a full network", networkAction(test, full, "add_subnet", map[string]any{"type": "cloud", "network_zone": "eu-central"}),
		http.StatusUnprocessableEntity, "no_subnet_available", "no subnet or IP available")
}

// A network deleted while an action runs on it is gone at once, and the
// action ends with Hetzner's "aborted": its add_route, a server's attach
// (measured 2026-11-09).
func TestNetworkDeletedDuringItsActionAbortsIt(t *testing.T) {
	test := newTestLab(t)
	agent := startMachine(t, test, platform.HostResources{Threads: 8, MemoryMB: 16 * 1024})
	network := networkWithSubnet(t, test, "net", "10.1")
	server := offServer(t, test, "web")
	waitActions(t, test, actionOf(t, "attach", serverNetworkAction(test, server.Server.ID, "attach_to_network", map[string]any{"network": network})))
	waiting, release := agent.HoldNext(agentproto.PictureLinks)
	route := actionOf(t, "add_route", networkAction(test, network, "add_route", map[string]any{"destination": "10.100.0.0/24", "gateway": "10.1.1.5"}))
	<-waiting
	if response := test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/networks/%d", network), nil); response.Status != http.StatusNoContent {
		t.Errorf("the delete answered %d %s", response.Status, response.Body)
	}
	release()
	ended := waitActions(t, test, route)[0]
	if ended.Status != "error" || ended.Error.Code != "aborted" || ended.Error.Message != "" {
		t.Errorf("the add_route ended %s with %+v, want aborted", ended.Status, ended.Error)
	}

	// a server's attach too, and the server is in no network after
	network = networkWithSubnet(t, test, "second", "10.2")
	waiting, release = agent.HoldNext(agentproto.PictureLinks)
	attach := actionOf(t, "attach", serverNetworkAction(test, server.Server.ID, "attach_to_network", map[string]any{"network": network}))
	<-waiting
	if response := test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/networks/%d", network), nil); response.Status != http.StatusNoContent {
		t.Errorf("the second delete answered %d %s", response.Status, response.Body)
	}
	release()
	if ended := waitActions(t, test, attach)[0]; ended.Status != "error" || ended.Error.Code != "aborted" {
		t.Errorf("the attach ended %s with %+v, want aborted", ended.Status, ended.Error)
	}
	if got := getServer(t, test, server.Server.ID).PrivateNet; len(got) != 0 {
		t.Errorf("after the delete the server is in %+v", got)
	}
}

// Each private NIC keeps its number in the metadata's private-networks
// while others come and go; a new one takes the lowest free number, as
// on Hetzner (measured 2026-11-09).
func TestPrivateNICKeepsItsInterfaceNumber(t *testing.T) {
	test := newTestLab(t)
	agent := startMachine(t, test, platform.HostResources{Threads: 8, MemoryMB: 16 * 1024})
	first, second, third := networkWithSubnet(t, test, "a", "10.1"), networkWithSubnet(t, test, "b", "10.2"), networkWithSubnet(t, test, "c", "10.3")
	server := offServer(t, test, "web").Server.ID
	join := func(command string, network int64) {
		waitActions(t, test, actionOf(t, command, serverNetworkAction(test, server, command, map[string]any{"network": network})))
	}
	numbers := func() map[string]string {
		var metadata agentproto.Metadata
		if err := json.Unmarshal(agent.Picture(agentproto.PictureMetadata), &metadata); err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, entry := range metadata.Servers {
			text := entry.Keys["private-networks"]
			for _, block := range strings.Split(text, "- ip: ")[1:] {
				name := strings.Fields(block[strings.Index(block, "network_name: "):])[1]
				number := strings.Fields(block[strings.Index(block, "interface_num: "):])[1]
				got[name] = number
			}
		}
		return got
	}
	join("attach_to_network", first)
	join("attach_to_network", second)
	join("detach_from_network", first)
	join("attach_to_network", third)
	if got := numbers(); fmt.Sprint(got) != fmt.Sprint(map[string]string{"b": "2", "c": "1"}) {
		t.Errorf("after a detach and an attach the NICs are %v", got)
	}
	join("attach_to_network", first)
	if got := numbers(); fmt.Sprint(got) != fmt.Sprint(map[string]string{"a": "3", "b": "2", "c": "1"}) {
		t.Errorf("after attaching the first again the NICs are %v", got)
	}
}
