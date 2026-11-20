package networks_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/networks"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// netEnv is the servers and networks modules wired like the cloud does on
// the shipped catalog and the fake platform; every agent push records the
// document both modules contribute.
type netEnv struct {
	*apitest.Env
	fake *platformtest.Fake
	srv  *servers.Module
	nw   *networks.Module

	mu     sync.Mutex
	pushed *agentapi.Config
}

func newNetEnv(t *testing.T) *netEnv {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	e := &netEnv{fake: platformtest.NewFake()}
	d := apitest.ServersDeps(t, cat, e.fake)
	d.PushAgent = e.push
	e.srv = servers.New(d)
	e.nw = networks.New(networks.Deps{Store: d.Store, Catalog: cat, Servers: e.srv, PushAgent: e.push})
	e.srv.SetNetworks(e.nw)
	e.Env = apitest.New(t, e.srv, e.nw, resactions.New())
	return e
}

// contribute builds the agent document in the cloud's module order.
func (e *netEnv) contribute() (agentapi.Config, error) {
	var doc agentapi.Config
	err := e.Store.Tx(func(tx store.Tx) error {
		if err := e.srv.Contribute(tx, &doc); err != nil {
			return err
		}
		return e.nw.Contribute(tx, &doc)
	})
	return doc, err
}

func (e *netEnv) push(context.Context) error {
	doc, err := e.contribute()
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.pushed = &doc
	e.mu.Unlock()
	return nil
}

// waitPushed waits until the last pushed document satisfies ok.
func (e *netEnv) waitPushed(t *testing.T, what string, ok func(doc *agentapi.Config) bool) {
	t.Helper()
	deadline := time.Now().Add(apitest.ActionTimeout)
	for {
		e.mu.Lock()
		doc := e.pushed
		e.mu.Unlock()
		if doc != nil && ok(doc) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pushed agent document with %s after %s", what, apitest.ActionTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type subnetView struct {
	Type        string `json:"type"`
	IPRange     string `json:"ip_range"`
	NetworkZone string `json:"network_zone"`
	Gateway     string `json:"gateway"`
}

type routeView struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
}

type networkView struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	IPRange    string            `json:"ip_range"`
	Subnets    []subnetView      `json:"subnets"`
	Routes     []routeView       `json:"routes"`
	Servers    []int64           `json:"servers"`
	Labels     map[string]string `json:"labels"`
	Protection struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
}

func (n networkView) subnetRanges() []string {
	var out []string
	for _, s := range n.Subnets {
		out = append(out, s.IPRange)
	}
	return out
}

func subnet(typ, ipRange, zone string) map[string]any {
	s := map[string]any{"type": typ, "network_zone": zone}
	if ipRange != "" {
		s["ip_range"] = ipRange
	}
	return s
}

func route(destination, gateway string) map[string]any {
	return map[string]any{"destination": destination, "gateway": gateway}
}

// netBody is a create body with cloud subnets in eu-central.
func netBody(name, ipRange string, subnets ...string) map[string]any {
	b := map[string]any{"name": name, "ip_range": ipRange}
	var s []any
	for _, r := range subnets {
		s = append(s, subnet("cloud", r, "eu-central"))
	}
	if s != nil {
		b["subnets"] = s
	}
	return b
}

// createNetwork posts body and expects 201.
func createNetwork(t *testing.T, e *netEnv, body map[string]any) networkView {
	t.Helper()
	resp := e.Do(http.MethodPost, "/networks", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /networks %v: status %d (%s), want 201", body, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Network networkView `json:"network"`
	}
	apitest.Decode(t, resp, &b)
	return b.Network
}

func getNetwork(t *testing.T, e *netEnv, id int64) networkView {
	t.Helper()
	resp := e.Do(http.MethodGet, fmt.Sprintf("/networks/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /networks/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		Network networkView `json:"network"`
	}
	apitest.Decode(t, resp, &b)
	return b.Network
}

func listNetworks(t *testing.T, e *netEnv) []networkView {
	t.Helper()
	resp := e.Do(http.MethodGet, "/networks", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /networks: status %d, want 200", resp.StatusCode)
	}
	var b struct {
		Networks []networkView `json:"networks"`
	}
	apitest.Decode(t, resp, &b)
	return b.Networks
}

func networkAction(e *netEnv, id int64, name string, body any) *http.Response {
	return e.Do(http.MethodPost, fmt.Sprintf("/networks/%d/actions/%s", id, name), body)
}

func serverAction(e *netEnv, id int64, name string, body any) *http.Response {
	return e.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/%s", id, name), body)
}

// runAction expects 201 for the action request and waits for its success.
func runAction(t *testing.T, e *netEnv, resp *http.Response, what string) {
	t.Helper()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("%s: status %d (%s), want 201", what, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	if a := e.WaitAction(b.Action.ID); a.Status != "success" {
		t.Fatalf("%s: action %q %+v, want success", what, a.Status, a.Error)
	}
}

func runNetworkAction(t *testing.T, e *netEnv, id int64, name string, body any) {
	t.Helper()
	runAction(t, e, networkAction(e, id, name, body), fmt.Sprintf("%s network %d %v", name, id, body))
}

func runServerAction(t *testing.T, e *netEnv, id int64, name string, body any) {
	t.Helper()
	runAction(t, e, serverAction(e, id, name, body), fmt.Sprintf("%s server %d %v", name, id, body))
}

// attach attaches server to network with the fields of extra.
func attach(t *testing.T, e *netEnv, server, network int64, extra map[string]any) {
	t.Helper()
	body := map[string]any{"network": network}
	for k, v := range extra {
		body[k] = v
	}
	runServerAction(t, e, server, "attach_to_network", body)
}

// postServer posts a cx23 server create in location with extra fields.
func postServer(e *netEnv, name, location string, extra map[string]any) *http.Response {
	req := map[string]any{"name": name, "server_type": "cx23", "image": "ubuntu-24.04", "location": location}
	for k, v := range extra {
		req[k] = v
	}
	return e.Do(http.MethodPost, "/servers", req)
}

// createServer creates a running cx23 server and waits for it.
func createServer(t *testing.T, e *netEnv, name, location string, extra map[string]any) int64 {
	t.Helper()
	resp := postServer(e, name, location, extra)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers %s: status %d (%s), want 201", name, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Server struct {
			ID int64 `json:"id"`
		} `json:"server"`
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	if a := e.WaitAction(b.Action.ID); a.Status != "success" {
		t.Fatalf("create server %s: action %q %+v, want success", name, a.Status, a.Error)
	}
	return b.Server.ID
}

type privateNetView struct {
	Network    int64    `json:"network"`
	IP         string   `json:"ip"`
	AliasIPs   []string `json:"alias_ips"`
	MACAddress string   `json:"mac_address"`
}

func privateNets(t *testing.T, e *netEnv, server int64) []privateNetView {
	t.Helper()
	resp := e.Do(http.MethodGet, fmt.Sprintf("/servers/%d", server), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /servers/%d: status %d, want 200", server, resp.StatusCode)
	}
	var b struct {
		Server struct {
			PrivateNet []privateNetView `json:"private_net"`
		} `json:"server"`
	}
	apitest.Decode(t, resp, &b)
	return b.Server.PrivateNet
}

// privateNet is the server's private_net entry of network, if any.
func privateNet(t *testing.T, e *netEnv, server, network int64) (privateNetView, bool) {
	t.Helper()
	for _, p := range privateNets(t, e, server) {
		if p.Network == network {
			return p, true
		}
	}
	return privateNetView{}, false
}

func mustPrivateNet(t *testing.T, e *netEnv, server, network int64) privateNetView {
	t.Helper()
	p, ok := privateNet(t, e, server, network)
	if !ok {
		t.Fatalf("server %d: no private_net entry for network %d", server, network)
	}
	return p
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details struct {
		Fields []struct {
			Name     string   `json:"name"`
			Messages []string `json:"messages"`
		} `json:"fields"`
	} `json:"details"`
}

func decodeError(t *testing.T, resp *http.Response) apiError {
	t.Helper()
	var b struct {
		Error apiError `json:"error"`
	}
	apitest.Decode(t, resp, &b)
	return b.Error
}

// wantError checks status and error.code of resp.
func wantError(t *testing.T, resp *http.Response, status int, code, what string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Errorf("%s: status %d, want %d", what, resp.StatusCode, status)
	}
	if got := apitest.ErrorCode(t, resp); got != code {
		t.Errorf("%s: error.code %q, want %s", what, got, code)
	}
}

// fieldIs reports whether an error field name denotes field: the name
// itself or a nested name ending in it ("subnets[0].type").
func fieldIs(name, field string) bool {
	return name == field || strings.HasSuffix(name, "."+field)
}

// wantFieldError checks a 422 invalid_input naming field and returns the
// error.
func wantFieldError(t *testing.T, resp *http.Response, field, what string) apiError {
	t.Helper()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("%s: status %d, want 422", what, resp.StatusCode)
	}
	e := decodeError(t, resp)
	var names []string
	found := false
	for _, f := range e.Details.Fields {
		names = append(names, f.Name)
		found = found || fieldIs(f.Name, field)
	}
	if e.Code != "invalid_input" || !found {
		t.Errorf("%s: error %q on fields %v, want invalid_input on %s", what, e.Code, names, field)
	}
	return e
}

func TestNetworks(t *testing.T) {
	t.Run("NET-01/create", func(t *testing.T) {
		e := newNetEnv(t)
		n := createNetwork(t, e, map[string]any{
			"name":     "net-a",
			"ip_range": "10.0.0.0/16",
			"subnets": []any{
				subnet("cloud", "10.0.1.0/24", "eu-central"),
				subnet("server", "10.0.2.0/24", "eu-central"),
			},
			"routes": []any{route("10.100.0.0/24", "10.0.1.5")},
			"labels": map[string]string{"env": "test"},
		})
		if n.ID == 0 || n.Name != "net-a" || n.IPRange != "10.0.0.0/16" {
			t.Errorf("created network id %d name %q ip_range %q, want an ID, net-a, 10.0.0.0/16", n.ID, n.Name, n.IPRange)
		}
		if got := n.subnetRanges(); !slices.Equal(got, []string{"10.0.1.0/24", "10.0.2.0/24"}) {
			t.Errorf("subnets %v, want 10.0.1.0/24 and 10.0.2.0/24", got)
		}
		for _, s := range n.Subnets {
			if s.Gateway != "10.0.0.1" || s.NetworkZone != "eu-central" {
				t.Errorf("subnet %s: gateway %q zone %q, want 10.0.0.1 (first host of the network range), eu-central", s.IPRange, s.Gateway, s.NetworkZone)
			}
		}
		if want := []routeView{{"10.100.0.0/24", "10.0.1.5"}}; !slices.Equal(n.Routes, want) {
			t.Errorf("routes %v, want %v", n.Routes, want)
		}
		if n.Labels["env"] != "test" || n.Protection.Delete {
			t.Errorf("labels %v protection %+v, want env=test, delete false", n.Labels, n.Protection)
		}

		valid := func(name string) map[string]any {
			return map[string]any{
				"name": name, "ip_range": "10.0.0.0/16",
				"subnets": []any{subnet("cloud", "10.0.1.0/24", "eu-central")},
				"routes":  []any{route("10.100.0.0/24", "10.0.1.5")},
			}
		}
		for _, c := range []struct {
			name, field string
			change      func(b map[string]any)
		}{
			{"range smaller than /24", "ip_range", func(b map[string]any) {
				b["ip_range"] = "10.0.0.0/25"
				b["subnets"] = []any{subnet("cloud", "10.0.0.0/26", "eu-central")}
				b["routes"] = []any{}
			}},
			{"range not RFC1918", "ip_range", func(b map[string]any) {
				b["ip_range"] = "100.64.0.0/16"
				b["subnets"] = []any{}
				b["routes"] = []any{}
			}},
			{"subnet outside the range", "ip_range", func(b map[string]any) {
				b["subnets"] = []any{subnet("cloud", "10.1.1.0/24", "eu-central")}
			}},
			{"subnet smaller than /30", "ip_range", func(b map[string]any) {
				b["subnets"] = []any{subnet("cloud", "10.0.1.0/31", "eu-central")}
				b["routes"] = []any{}
			}},
			{"overlapping subnets", "ip_range", func(b map[string]any) {
				b["subnets"] = []any{subnet("cloud", "10.0.1.0/24", "eu-central"), subnet("cloud", "10.0.1.128/25", "eu-central")}
			}},
			{"route destination overlapping a subnet", "destination", func(b map[string]any) {
				b["routes"] = []any{route("10.0.1.0/25", "10.0.1.200")}
			}},
			{"route destination overlapping 172.31.1.1", "destination", func(b map[string]any) {
				b["routes"] = []any{route("172.31.1.0/24", "10.0.1.5")}
			}},
			{"route gateway outside the range", "gateway", func(b map[string]any) {
				b["routes"] = []any{route("10.100.0.0/24", "10.1.0.5")}
			}},
			{"route gateway is the network gateway", "gateway", func(b map[string]any) {
				b["routes"] = []any{route("10.100.0.0/24", "10.0.0.1")}
			}},
		} {
			t.Run(c.name, func(t *testing.T) {
				b := valid("refused-" + strings.ReplaceAll(c.name, " ", "-"))
				c.change(b)
				wantFieldError(t, e.For(t).Do(http.MethodPost, "/networks", b), c.field, c.name)
			})
		}

		listed := false
		for _, l := range listNetworks(t, e) {
			listed = listed || l.ID == n.ID
		}
		if !listed {
			t.Errorf("GET /networks does not list network %d", n.ID)
		}
		if g := getNetwork(t, e, n.ID); g.Name != "net-a" || g.IPRange != "10.0.0.0/16" || len(g.Subnets) != 2 || len(g.Routes) != 1 {
			t.Errorf("GET /networks/%d: %+v, want the created network", n.ID, g)
		}

		resp := e.Do(http.MethodPut, fmt.Sprintf("/networks/%d", n.ID), map[string]any{
			"name": "net-b", "labels": map[string]string{"tier": "x"},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT /networks/%d: status %d (%s), want 200", n.ID, resp.StatusCode, apitest.ErrorCode(t, resp))
		}
		if g := getNetwork(t, e, n.ID); g.Name != "net-b" || g.Labels["tier"] != "x" || g.Labels["env"] != "" {
			t.Errorf("after PUT: name %q labels %v, want net-b and only tier=x", g.Name, g.Labels)
		}

		runNetworkAction(t, e, n.ID, "change_protection", map[string]any{"delete": true})
		if !getNetwork(t, e, n.ID).Protection.Delete {
			t.Errorf("protection.delete false after change_protection delete=true")
		}
		wantError(t, e.Do(http.MethodDelete, fmt.Sprintf("/networks/%d", n.ID), nil),
			http.StatusLocked, "protected", "DELETE of a protected network")
		getNetwork(t, e, n.ID)
		runNetworkAction(t, e, n.ID, "change_protection", map[string]any{"delete": false})
		if resp := e.Do(http.MethodDelete, fmt.Sprintf("/networks/%d", n.ID), nil); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("DELETE /networks/%d: status %d (%s), want 204", n.ID, resp.StatusCode, apitest.ErrorCode(t, resp))
		}
		wantError(t, e.Do(http.MethodGet, fmt.Sprintf("/networks/%d", n.ID), nil),
			http.StatusNotFound, "not_found", "GET of the deleted network")
	})

	t.Run("NET-01/subnets_routes", func(t *testing.T) {
		e := newNetEnv(t)
		n := createNetwork(t, e, netBody("net", "10.0.0.0/16", "10.0.0.0/24"))

		runNetworkAction(t, e, n.ID, "add_subnet", subnet("cloud", "", "eu-central"))
		if got := getNetwork(t, e, n.ID).subnetRanges(); !slices.Equal(got, []string{"10.0.0.0/24", "10.0.1.0/24"}) {
			t.Errorf("subnets after add_subnet without ip_range: %v, want the first free /24 10.0.1.0/24 added", got)
		}
		runNetworkAction(t, e, n.ID, "add_subnet", subnet("server", "10.0.5.0/24", "eu-central"))
		if got := getNetwork(t, e, n.ID).subnetRanges(); !slices.Contains(got, "10.0.5.0/24") {
			t.Errorf("subnets after add_subnet 10.0.5.0/24: %v", got)
		}
		wantFieldError(t, networkAction(e, n.ID, "add_subnet", subnet("cloud", "10.1.0.0/24", "eu-central")),
			"ip_range", "add_subnet outside the range")

		runNetworkAction(t, e, n.ID, "delete_subnet", map[string]any{"ip_range": "10.0.5.0/24"})
		if got := getNetwork(t, e, n.ID).subnetRanges(); slices.Contains(got, "10.0.5.0/24") {
			t.Errorf("subnets after delete_subnet 10.0.5.0/24: %v, want it gone", got)
		}

		srv := createServer(t, e, "holder", "fsn1", nil)
		attach(t, e, srv, n.ID, map[string]any{"ip": "10.0.1.10"})
		wantError(t, networkAction(e, n.ID, "delete_subnet", map[string]any{"ip_range": "10.0.1.0/24"}),
			http.StatusUnprocessableEntity, "service_error", "delete_subnet holding a server's IP")
		if got := getNetwork(t, e, n.ID).subnetRanges(); !slices.Contains(got, "10.0.1.0/24") {
			t.Errorf("subnets after refused delete_subnet: %v, want 10.0.1.0/24 kept", got)
		}

		runNetworkAction(t, e, n.ID, "add_route", route("10.100.0.0/24", "10.0.0.5"))
		if got := getNetwork(t, e, n.ID).Routes; !slices.Equal(got, []routeView{{"10.100.0.0/24", "10.0.0.5"}}) {
			t.Errorf("routes after add_route: %v", got)
		}
		wantFieldError(t, networkAction(e, n.ID, "add_route", route("10.101.0.0/24", "10.0.0.1")),
			"gateway", "add_route via the network gateway")
		wantFieldError(t, networkAction(e, n.ID, "add_route", route("10.0.1.0/25", "10.0.0.5")),
			"destination", "add_route overlapping a subnet")
		runNetworkAction(t, e, n.ID, "delete_route", route("10.100.0.0/24", "10.0.0.5"))
		if got := getNetwork(t, e, n.ID).Routes; len(got) != 0 {
			t.Errorf("routes after delete_route: %v, want none", got)
		}
	})

	t.Run("NET-01/ip_range", func(t *testing.T) {
		e := newNetEnv(t)
		n := createNetwork(t, e, netBody("net", "10.0.0.0/16", "10.0.1.0/24"))

		runNetworkAction(t, e, n.ID, "change_ip_range", map[string]any{"ip_range": "10.0.0.0/15"})
		if got := getNetwork(t, e, n.ID).IPRange; got != "10.0.0.0/15" {
			t.Errorf("ip_range after change_ip_range: %q, want 10.0.0.0/15", got)
		}
		wantFieldError(t, networkAction(e, n.ID, "change_ip_range", map[string]any{"ip_range": "10.0.0.0/17"}),
			"ip_range", "change_ip_range to a smaller range")
		wantFieldError(t, networkAction(e, n.ID, "change_ip_range", map[string]any{"ip_range": "10.4.0.0/14"}),
			"ip_range", "change_ip_range to another start address")

		srv := createServer(t, e, "member", "fsn1", nil)
		attach(t, e, srv, n.ID, nil)
		wantError(t, networkAction(e, n.ID, "change_ip_range", map[string]any{"ip_range": "10.0.0.0/14"}),
			http.StatusUnprocessableEntity, "service_error", "change_ip_range with an attached server")
		if got := getNetwork(t, e, n.ID).IPRange; got != "10.0.0.0/15" {
			t.Errorf("ip_range after refused change_ip_range: %q, want 10.0.0.0/15", got)
		}
	})

	t.Run("NET-05/subnet", func(t *testing.T) {
		e := newNetEnv(t)
		b := netBody("unknown-zone", "10.0.0.0/16")
		b["subnets"] = []any{subnet("cloud", "10.0.1.0/24", "nowhere-1")}
		wantFieldError(t, e.Do(http.MethodPost, "/networks", b), "network_zone", "create with an unknown network_zone")
		if l := listNetworks(t, e); len(l) != 0 {
			t.Errorf("networks after refused create: %d, want 0", len(l))
		}
		n := createNetwork(t, e, netBody("net", "10.0.0.0/16"))
		wantFieldError(t, networkAction(e, n.ID, "add_subnet", subnet("cloud", "10.0.1.0/24", "nowhere-1")),
			"network_zone", "add_subnet with an unknown network_zone")
	})

	t.Run("NET-06", func(t *testing.T) {
		e := newNetEnv(t)
		const message = "vswitch subnets are not supported by HLab"
		vswitch := map[string]any{"type": "vswitch", "ip_range": "10.0.1.0/24", "network_zone": "eu-central", "vswitch_id": 1000}
		check := func(resp *http.Response, what string) {
			t.Helper()
			apiErr := wantFieldError(t, resp, "type", what)
			found := apiErr.Message == message
			for _, f := range apiErr.Details.Fields {
				found = found || (fieldIs(f.Name, "type") && slices.Contains(f.Messages, message))
			}
			if !found {
				t.Errorf("%s: message %q, field messages %+v, want %q", what, apiErr.Message, apiErr.Details.Fields, message)
			}
		}

		b := netBody("with-vswitch", "10.0.0.0/16")
		b["subnets"] = []any{vswitch}
		check(e.Do(http.MethodPost, "/networks", b), "create with a vswitch subnet")
		if l := listNetworks(t, e); len(l) != 0 {
			t.Errorf("networks after refused create: %d, want 0", len(l))
		}

		n := createNetwork(t, e, netBody("net", "10.0.0.0/16", "10.0.2.0/24"))
		check(networkAction(e, n.ID, "add_subnet", vswitch), "add_subnet of type vswitch")
		if got := getNetwork(t, e, n.ID).subnetRanges(); !slices.Equal(got, []string{"10.0.2.0/24"}) {
			t.Errorf("subnets after refused add_subnet: %v, want only 10.0.2.0/24", got)
		}
	})

	t.Run("LB-01", testLoadBalancerMembers)

	testAttach(t)
	testContribute(t)
}

// fakeLBs is the loadbalancers hook: the LB members per network, and the
// networks NetworkDeleted was called for.
type fakeLBs struct {
	mu      sync.Mutex
	members map[int64][]networks.LBMember
	deleted []int64
}

func (f *fakeLBs) InNetwork(_ store.Tx, _, netID int64) ([]networks.LBMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.members[netID]), nil
}

func (f *fakeLBs) NetworkDeleted(_ store.Tx, netID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, netID)
	delete(f.members, netID)
	return nil
}

func (f *fakeLBs) add(netID, lbID int64, ip netip.Addr) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[netID] = append(f.members[netID], networks.LBMember{ID: lbID, IP: ip})
}

// errCode is the API code of an error AttachLoadBalancer returns.
func errCode(err error) string {
	var fe *api.FieldError
	if errors.As(err, &fe) {
		if fe.Code == "" {
			return "invalid_input"
		}
		return fe.Code
	}
	var ae *actions.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	if err == nil {
		return ""
	}
	return "other: " + err.Error()
}

// testLoadBalancerMembers: AttachLoadBalancer allocates LB IPs next to the
// servers' IPs, the network view lists the LBs and deleting the network
// tells the loadbalancers module.
func testLoadBalancerMembers(t *testing.T) {
	e := newNetEnv(t)
	lbs := &fakeLBs{members: map[int64][]networks.LBMember{}}
	e.nw.SetLoadBalancers(lbs)
	n1 := createNetwork(t, e, netBody("n1", "10.0.0.0/16", "10.0.0.0/24"))
	us := createNetwork(t, e, map[string]any{
		"name": "us", "ip_range": "10.1.0.0/16",
		"subnets": []any{subnet("cloud", "10.1.1.0/24", "us-east")},
	})
	a := createServer(t, e, "a", "fsn1", nil)
	attach(t, e, a, n1.ID, nil) // 10.0.0.2

	attachLB := func(netID int64, ip, ipRange string) (netip.Addr, int, error) {
		t.Helper()
		var got netip.Addr
		var vlan int
		var aerr error
		err := e.Store.Tx(func(tx store.Tx) error {
			got, vlan, aerr = e.nw.AttachLoadBalancer(tx, e.Project.ID, "eu-central", netID, ip, ipRange)
			return nil
		})
		if err != nil {
			t.Fatalf("transaction: %v", err)
		}
		return got, vlan, aerr
	}
	var wantVLAN int
	if err := e.Store.Tx(func(tx store.Tx) error {
		r, err := networks.Load(tx, e.Project.ID, n1.ID)
		wantVLAN = r.Hlab.VLAN
		return err
	}); err != nil {
		t.Fatalf("load network: %v", err)
	}

	ip, vlan, err := attachLB(n1.ID, "10.0.0.9", "")
	if err != nil || ip != netip.MustParseAddr("10.0.0.9") || vlan != wantVLAN {
		t.Fatalf("AttachLoadBalancer with ip 10.0.0.9 = %s, %d, %v; want 10.0.0.9, VLAN %d", ip, vlan, err, wantVLAN)
	}
	lbs.add(n1.ID, 1, ip)

	ip, _, err = attachLB(n1.ID, "", "")
	if err != nil || ip != netip.MustParseAddr("10.0.0.3") {
		t.Fatalf("AttachLoadBalancer without ip = %s, %v; want the next free 10.0.0.3", ip, err)
	}
	lbs.add(n1.ID, 2, ip)

	if _, _, err := attachLB(n1.ID, "10.0.0.2", ""); errCode(err) != "ip_not_available" {
		t.Errorf("AttachLoadBalancer with the IP of server a: %v, want ip_not_available", err)
	}
	if _, _, err := attachLB(n1.ID, "10.0.0.9", ""); errCode(err) != "ip_not_available" {
		t.Errorf("AttachLoadBalancer with the IP of LB 1: %v, want ip_not_available", err)
	}
	if _, _, err := attachLB(us.ID, "", ""); errCode(err) != "no_subnet_available" {
		t.Errorf("AttachLoadBalancer in eu-central to a us-east network: %v, want no_subnet_available", err)
	}

	b := createServer(t, e, "b", "fsn1", nil)
	attach(t, e, b, n1.ID, nil)
	if got := mustPrivateNet(t, e, b, n1.ID).IP; got == "10.0.0.3" || got == "10.0.0.9" {
		t.Errorf("server attached after the LBs got the LB IP %s", got)
	}

	resp := e.Do(http.MethodGet, fmt.Sprintf("/networks/%d", n1.ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /networks/%d: status %d, want 200", n1.ID, resp.StatusCode)
	}
	var view struct {
		Network struct {
			LoadBalancers []int64 `json:"load_balancers"`
		} `json:"network"`
	}
	apitest.Decode(t, resp, &view)
	if got := sorted(view.Network.LoadBalancers); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("GET /networks/%d load_balancers %v, want [1 2]", n1.ID, got)
	}

	if resp := e.Do(http.MethodDelete, fmt.Sprintf("/networks/%d", n1.ID), nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /networks/%d: status %d (%s), want 204", n1.ID, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	lbs.mu.Lock()
	deleted := slices.Clone(lbs.deleted)
	lbs.mu.Unlock()
	if !slices.Equal(deleted, []int64{n1.ID}) {
		t.Errorf("NetworkDeleted called for %v, want [%d]", deleted, n1.ID)
	}
}
