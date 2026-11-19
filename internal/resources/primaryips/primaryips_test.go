package primaryips_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/primaryips"
	"github.com/webcodingcat/hlab/internal/resources/servers"
)

// plenty is a host with room for every server of the tests.
var plenty = platform.Resources{MemoryMB: 64 << 10, FreeMemoryMB: 64 << 10, LogicalCPUs: 16, FreeDiskBytes: 1 << 50}

var publicRange = netip.MustParsePrefix("198.19.0.0/16")

type pipEnv struct{ *apitest.Env }

func loadCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	return cat
}

// newPIPEnv mounts the servers and primary IPs modules on a fake platform,
// wired like the cloud does: the primary IPs make the public IPs of new
// servers and follow server deletion.
func newPIPEnv(t *testing.T) *pipEnv {
	t.Helper()
	cat := loadCatalog(t)
	fake := platformtest.NewFake()
	fake.SetResources(plenty)
	d := apitest.ServersDeps(t, cat, fake)
	srv := servers.New(d)
	pip := newModule(d, cat, srv)
	srv.SetPublicIPs(pip)
	srv.OnDelete(pip.ServerDeleted)
	return &pipEnv{apitest.New(t, srv, pip, resactions.New())}
}

func newModule(d servers.Deps, cat *catalog.Catalog, srv *servers.Module) *primaryips.Module {
	return primaryips.New(primaryips.Deps{
		Store:       d.Store,
		Catalog:     cat,
		PublicRange: d.PublicRange,
		Servers:     srv,
		PushAgent:   func(context.Context) error { return nil },
	})
}

type pipView struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	IP           string            `json:"ip"`
	Type         string            `json:"type"`
	AssigneeID   *int64            `json:"assignee_id"`
	AssigneeType string            `json:"assignee_type"`
	AutoDelete   bool              `json:"auto_delete"`
	Labels       map[string]string `json:"labels"`
	Protection   struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
}

type publicNet struct {
	IPv4 *struct {
		ID int64  `json:"id"`
		IP string `json:"ip"`
	} `json:"ipv4"`
	IPv6 *struct {
		ID int64  `json:"id"`
		IP string `json:"ip"`
	} `json:"ipv6"`
}

type serverView struct {
	ID        int64     `json:"id"`
	Status    string    `json:"status"`
	PublicNet publicNet `json:"public_net"`
}

// createPIP posts a primary IP create, expects 201 and returns it.
func createPIP(t *testing.T, e *pipEnv, body map[string]any) pipView {
	t.Helper()
	if _, ok := body["assignee_type"]; !ok {
		body["assignee_type"] = "server"
	}
	resp := e.Do(http.MethodPost, "/primary_ips", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /primary_ips %v: status %d (%s), want 201", body, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		PrimaryIP pipView `json:"primary_ip"`
	}
	apitest.Decode(t, resp, &b)
	return b.PrimaryIP
}

// getPIP returns the primary IP and whether it exists.
func getPIP(t *testing.T, e *pipEnv, id int64) (pipView, bool) {
	t.Helper()
	resp := e.Do(http.MethodGet, fmt.Sprintf("/primary_ips/%d", id), nil)
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return pipView{}, false
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /primary_ips/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		PrimaryIP pipView `json:"primary_ip"`
	}
	apitest.Decode(t, resp, &b)
	return b.PrimaryIP, true
}

func mustPIP(t *testing.T, e *pipEnv, id int64) pipView {
	t.Helper()
	p, ok := getPIP(t, e, id)
	if !ok {
		t.Fatalf("GET /primary_ips/%d: 404, want the primary IP", id)
	}
	return p
}

func listPIPs(t *testing.T, e *pipEnv) []pipView {
	t.Helper()
	resp := e.Do(http.MethodGet, "/primary_ips", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /primary_ips: status %d, want 200", resp.StatusCode)
	}
	var b struct {
		PrimaryIPs []pipView `json:"primary_ips"`
	}
	apitest.Decode(t, resp, &b)
	slices.SortFunc(b.PrimaryIPs, func(a, b pipView) int { return int(a.ID - b.ID) })
	return b.PrimaryIPs
}

func pipAction(e *pipEnv, id int64, name string, body any) *http.Response {
	return e.Do(http.MethodPost, fmt.Sprintf("/primary_ips/%d/actions/%s", id, name), body)
}

// runPIPAction posts the action, expects 201 and waits for its success.
func runPIPAction(t *testing.T, e *pipEnv, id int64, name string, body any) {
	t.Helper()
	resp := pipAction(e, id, name, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /primary_ips/%d/actions/%s %v: status %d (%s), want 201", id, name, body, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	waitSuccess(t, e, b.Action.ID, fmt.Sprintf("%s primary IP %d", name, id))
}

func assignTo(server int64) map[string]any {
	return map[string]any{"assignee_id": server, "assignee_type": "server"}
}

func waitSuccess(t *testing.T, e *pipEnv, act int64, what string) {
	t.Helper()
	if a := e.WaitAction(act); a.Status != "success" {
		t.Fatalf("%s: action %q %+v, want success", what, a.Status, a.Error)
	}
}

// startServer posts a server create and returns the response.
func startServer(e *pipEnv, name, location string, extra map[string]any) *http.Response {
	req := map[string]any{"name": name, "server_type": "cx23", "image": "ubuntu-24.04", "location": location}
	for k, v := range extra {
		req[k] = v
	}
	return e.Do(http.MethodPost, "/servers", req)
}

// createServer creates a cx23 server and waits for its create action.
func createServer(t *testing.T, e *pipEnv, name, location string, extra map[string]any) int64 {
	t.Helper()
	resp := startServer(e, name, location, extra)
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
	waitSuccess(t, e, b.Action.ID, "create server "+name)
	return b.Server.ID
}

// noIPv4 is the create field for a server without public IPv4.
var noIPv4 = map[string]any{"public_net": map[string]any{"enable_ipv4": false, "enable_ipv6": true}}

// stoppedServer creates a server without public IPv4 and powers it off.
func stoppedServer(t *testing.T, e *pipEnv, name, location string) int64 {
	t.Helper()
	id := createServer(t, e, name, location, noIPv4)
	serverAction(t, e, id, "poweroff")
	return id
}

func serverAction(t *testing.T, e *pipEnv, id int64, name string) {
	t.Helper()
	resp := e.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/%s", id, name), nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers/%d/actions/%s: status %d (%s), want 201", id, name, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	waitSuccess(t, e, b.Action.ID, fmt.Sprintf("%s server %d", name, id))
	want := map[string]string{"poweroff": "off", "poweron": "running"}[name]
	deadline := time.Now().Add(apitest.ActionTimeout)
	for getServer(t, e, id).Status != want {
		if time.Now().After(deadline) {
			t.Fatalf("server %d not %s within %s after %s", id, want, apitest.ActionTimeout, name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func getServer(t *testing.T, e *pipEnv, id int64) serverView {
	t.Helper()
	resp := e.Do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /servers/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		Server serverView `json:"server"`
	}
	apitest.Decode(t, resp, &b)
	return b.Server
}

func deleteServer(t *testing.T, e *pipEnv, id int64) {
	t.Helper()
	resp := e.Do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /servers/%d: status %d (%s), want 200", id, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	waitSuccess(t, e, b.Action.ID, fmt.Sprintf("delete server %d", id))
}

func ipv4ID(s serverView) int64 {
	if s.PublicNet.IPv4 == nil {
		return 0
	}
	return s.PublicNet.IPv4.ID
}

func assignee(p pipView) int64 {
	if p.AssigneeID == nil {
		return 0
	}
	return *p.AssigneeID
}

func wantError(t *testing.T, resp *http.Response, status int, code, what string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Errorf("%s: status %d, want %d", what, resp.StatusCode, status)
	}
	if got := apitest.ErrorCode(t, resp); got != code {
		t.Errorf("%s: error.code %q, want %s", what, got, code)
	}
}

func TestPrimaryIPs(t *testing.T) {
	t.Run("PIP-01/create_delete", func(t *testing.T) {
		e := newPIPEnv(t)

		v4 := createPIP(t, e, map[string]any{"name": "v4", "type": "ipv4", "location": "fsn1"})
		if a, err := netip.ParseAddr(v4.IP); err != nil || !a.Is4() || !publicRange.Contains(a) {
			t.Errorf("IPv4 primary IP ip = %q, want an address of %s", v4.IP, publicRange)
		}
		if v4.AutoDelete || v4.AssigneeID != nil || v4.Type != "ipv4" {
			t.Errorf("created IPv4 = %+v, want type ipv4, auto_delete false, unassigned", v4)
		}
		v6 := createPIP(t, e, map[string]any{"name": "v6", "type": "ipv6", "location": "fsn1"})
		if want := primaryips.IPv6Net(v6.ID).String(); v6.IP != want || v6.Type != "ipv6" {
			t.Errorf("created IPv6 = %+v, want type ipv6 with ip %s", v6, want)
		}

		srv := stoppedServer(t, e, "holder", "fsn1")
		held := createPIP(t, e, map[string]any{"name": "held", "type": "ipv4", "assignee_id": srv})
		if assignee(held) != srv {
			t.Errorf("created with assignee_id %d: assignee_id %v, want %d", srv, held.AssigneeID, srv)
		}
		if got := ipv4ID(getServer(t, e, srv)); got != held.ID {
			t.Errorf("server public_net.ipv4.id = %d, want %d", got, held.ID)
		}

		var ids []int64
		for _, p := range listPIPs(t, e) {
			ids = append(ids, p.ID)
		}
		for _, id := range []int64{v4.ID, v6.ID, held.ID} {
			if !slices.Contains(ids, id) {
				t.Errorf("GET /primary_ips = %v, want it to hold %d", ids, id)
			}
		}

		resp := e.Do(http.MethodPut, fmt.Sprintf("/primary_ips/%d", v4.ID),
			map[string]any{"name": "renamed", "labels": map[string]string{"env": "test"}, "auto_delete": true})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT /primary_ips/%d: status %d (%s), want 200", v4.ID, resp.StatusCode, apitest.ErrorCode(t, resp))
		}
		_ = resp.Body.Close()
		if got := mustPIP(t, e, v4.ID); got.Name != "renamed" || got.Labels["env"] != "test" || !got.AutoDelete {
			t.Errorf("after PUT: %+v, want name renamed, label env=test, auto_delete true", got)
		}

		wantError(t, pipAction(e, v4.ID, "change_protection", map[string]any{"delete": true}),
			http.StatusUnprocessableEntity, "invalid_input", "change_protection delete=true with auto_delete")
		if mustPIP(t, e, v4.ID).Protection.Delete {
			t.Error("refused change_protection set delete protection")
		}

		wantError(t, e.Do(http.MethodDelete, fmt.Sprintf("/primary_ips/%d", held.ID), nil),
			http.StatusUnprocessableEntity, "must_be_unassigned", "DELETE assigned primary IP")
		if _, ok := getPIP(t, e, held.ID); !ok {
			t.Error("assigned primary IP gone after refused DELETE")
		}

		runPIPAction(t, e, v6.ID, "change_protection", map[string]any{"delete": true})
		wantError(t, e.Do(http.MethodDelete, fmt.Sprintf("/primary_ips/%d", v6.ID), nil),
			http.StatusLocked, "protected", "DELETE protected primary IP")
		runPIPAction(t, e, v6.ID, "change_protection", map[string]any{"delete": false})

		for _, id := range []int64{v6.ID, v4.ID} {
			resp := e.Do(http.MethodDelete, fmt.Sprintf("/primary_ips/%d", id), nil)
			if resp.StatusCode != http.StatusNoContent {
				t.Errorf("DELETE /primary_ips/%d unassigned: status %d (%s), want 204", id, resp.StatusCode, apitest.ErrorCode(t, resp))
				continue
			}
			_ = resp.Body.Close()
			if _, ok := getPIP(t, e, id); ok {
				t.Errorf("primary IP %d still there after DELETE", id)
			}
		}
	})

	t.Run("PIP-01/assign", func(t *testing.T) {
		e := newPIPEnv(t)
		ip := createPIP(t, e, map[string]any{"name": "move", "type": "ipv4", "location": "fsn1"})

		running := createServer(t, e, "running", "fsn1", noIPv4)
		wantError(t, pipAction(e, ip.ID, "assign", assignTo(running)),
			http.StatusUnprocessableEntity, "server_not_stopped", "assign to a running server")

		withIPv4 := createServer(t, e, "with-ipv4", "fsn1", nil)
		serverAction(t, e, withIPv4, "poweroff")
		wantError(t, pipAction(e, ip.ID, "assign", assignTo(withIPv4)),
			http.StatusUnprocessableEntity, "server_has_ipv4", "assign to a server with an IPv4")

		elsewhere := stoppedServer(t, e, "in-nbg1", "nbg1")
		wantError(t, pipAction(e, ip.ID, "assign", assignTo(elsewhere)),
			http.StatusUnprocessableEntity, "primary_ip_datacenter_mismatch", "assign to a server in another location")

		x := stoppedServer(t, e, "x", "fsn1")
		runPIPAction(t, e, ip.ID, "assign", assignTo(x))
		if got := assignee(mustPIP(t, e, ip.ID)); got != x {
			t.Errorf("after assign: assignee_id %d, want %d", got, x)
		}
		if s := getServer(t, e, x); ipv4ID(s) != ip.ID || s.PublicNet.IPv4.IP != ip.IP {
			t.Errorf("after assign: server public_net.ipv4 = %+v, want id %d ip %s", s.PublicNet.IPv4, ip.ID, ip.IP)
		}

		y := stoppedServer(t, e, "y", "fsn1")
		wantError(t, pipAction(e, ip.ID, "assign", assignTo(y)),
			http.StatusUnprocessableEntity, "primary_ip_already_assigned", "assign an IP assigned elsewhere")

		serverAction(t, e, x, "poweron")
		wantError(t, pipAction(e, ip.ID, "unassign", nil),
			http.StatusUnprocessableEntity, "server_not_stopped", "unassign from a running server")
		serverAction(t, e, x, "poweroff")

		runPIPAction(t, e, ip.ID, "unassign", nil)
		if p := mustPIP(t, e, ip.ID); p.AssigneeID != nil {
			t.Errorf("after unassign: assignee_id %v, want null", *p.AssigneeID)
		}
		if s := getServer(t, e, x); s.PublicNet.IPv4 != nil {
			t.Errorf("after unassign: server public_net.ipv4 = %+v, want null", s.PublicNet.IPv4)
		}
	})

	t.Run("PIP-01/server", func(t *testing.T) {
		e := newPIPEnv(t)

		auto := getServer(t, e, createServer(t, e, "auto", "fsn1", nil))
		if auto.PublicNet.IPv4 == nil || auto.PublicNet.IPv6 == nil {
			t.Fatalf("server public_net = %+v, want IPv4 and IPv6", auto.PublicNet)
		}
		autoIDs := []int64{auto.PublicNet.IPv4.ID, auto.PublicNet.IPv6.ID}
		for i, id := range autoIDs {
			p := mustPIP(t, e, id)
			wantIP := []string{auto.PublicNet.IPv4.IP, auto.PublicNet.IPv6.IP}[i]
			if assignee(p) != auto.ID || !p.AutoDelete || p.IP != wantIP {
				t.Errorf("primary IP %d of the new server = %+v, want assigned to %d, auto_delete true, ip %s", id, p, auto.ID, wantIP)
			}
		}

		given := createPIP(t, e, map[string]any{"name": "given", "type": "ipv4", "location": "fsn1"})
		taker := getServer(t, e, createServer(t, e, "taker", "fsn1",
			map[string]any{"public_net": map[string]any{"ipv4": given.ID}}))
		if ipv4ID(taker) != given.ID || taker.PublicNet.IPv4.IP != given.IP {
			t.Errorf("server created with public_net.ipv4 %d: ipv4 %+v, want id %d ip %s", given.ID, taker.PublicNet.IPv4, given.ID, given.IP)
		}
		if got := assignee(mustPIP(t, e, given.ID)); got != taker.ID {
			t.Errorf("given primary IP assignee_id %d, want %d", got, taker.ID)
		}

		wantError(t, startServer(e, "second-taker", "fsn1", map[string]any{"public_net": map[string]any{"ipv4": given.ID}}),
			http.StatusUnprocessableEntity, "primary_ip_assigned", "create with a primary IP assigned elsewhere")
		v6 := createPIP(t, e, map[string]any{"name": "v6", "type": "ipv6", "location": "fsn1"})
		wantError(t, startServer(e, "mismatch", "fsn1", map[string]any{"public_net": map[string]any{"ipv4": v6.ID}}),
			http.StatusPreconditionFailed, "primary_ip_version_mismatch", "create with an IPv6 given as ipv4")

		if s := getServer(t, e, createServer(t, e, "v6only", "fsn1", noIPv4)); s.PublicNet.IPv4 != nil {
			t.Errorf("server with enable_ipv4 false: public_net.ipv4 = %+v, want null", s.PublicNet.IPv4)
		}
		wantError(t, startServer(e, "nothing", "fsn1",
			map[string]any{"public_net": map[string]any{"enable_ipv4": false, "enable_ipv6": false}}),
			http.StatusUnprocessableEntity, "invalid_input", "create without IP and network")

		deleteServer(t, e, auto.ID)
		for _, id := range autoIDs {
			if _, ok := getPIP(t, e, id); ok {
				t.Errorf("auto_delete primary IP %d still there after its server was deleted", id)
			}
		}
		deleteServer(t, e, taker.ID)
		if p, ok := getPIP(t, e, given.ID); !ok {
			t.Errorf("primary IP %d without auto_delete gone after its server was deleted", given.ID)
		} else if p.AssigneeID != nil {
			t.Errorf("primary IP %d after its server was deleted: assignee_id %d, want null", given.ID, *p.AssigneeID)
		}
	})

	t.Run("PIP-01/recover", func(t *testing.T) {
		// Phase 04 state: a servers module without the primary IP hook
		// stores IDs in public_net but no primary IP records.
		cat := loadCatalog(t)
		fake := platformtest.NewFake()
		fake.SetResources(plenty)
		d := apitest.ServersDeps(t, cat, fake)
		srv := servers.New(d)
		e := &pipEnv{apitest.New(t, srv, resactions.New())}
		s := getServer(t, e, createServer(t, e, "old", "fsn1", nil))
		if s.PublicNet.IPv4 == nil || s.PublicNet.IPv6 == nil || s.PublicNet.IPv4.ID == 0 || s.PublicNet.IPv6.ID == 0 {
			t.Fatalf("setup: server public_net = %+v, want IPv4 and IPv6 with IDs", s.PublicNet)
		}

		pip := newModule(d, cat, srv)
		e.Router.Mount(pip)
		if err := pip.Recover(context.Background()); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		first := listPIPs(t, e)
		if len(first) != 2 {
			t.Fatalf("primary IPs after Recover = %+v, want the server's two", first)
		}
		for _, p := range first {
			var want int64
			var wantIP string
			switch p.ID {
			case s.PublicNet.IPv4.ID:
				want, wantIP = s.PublicNet.IPv4.ID, s.PublicNet.IPv4.IP
			case s.PublicNet.IPv6.ID:
				want, wantIP = s.PublicNet.IPv6.ID, s.PublicNet.IPv6.IP
			}
			if p.ID != want || p.IP != wantIP || assignee(p) != s.ID || !p.AutoDelete {
				t.Errorf("recovered primary IP %+v, want one of the server's IDs with its ip, assigned to %d, auto_delete true", p, s.ID)
			}
		}

		if err := pip.Recover(context.Background()); err != nil {
			t.Fatalf("second Recover: %v", err)
		}
		a, _ := json.Marshal(first)
		b, _ := json.Marshal(listPIPs(t, e))
		if string(a) != string(b) {
			t.Errorf("second Recover changed the primary IPs:\nbefore %s\nafter  %s", a, b)
		}
	})
}
