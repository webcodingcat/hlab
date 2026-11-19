package floatingips_test

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/floatingips"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// plenty is a host with room for every server of the tests.
var plenty = platform.Resources{MemoryMB: 64 << 10, FreeMemoryMB: 64 << 10, LogicalCPUs: 16, FreeDiskBytes: 1 << 50}

type fipEnv struct {
	*apitest.Env
	fip *floatingips.Module
}

// newFIPEnv mounts the servers and floating IPs modules on a fake
// platform, wired like the cloud does: server deletion unassigns the
// server's floating IPs.
func newFIPEnv(t *testing.T) *fipEnv {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	fake := platformtest.NewFake()
	fake.SetResources(plenty)
	d := apitest.ServersDeps(t, cat, fake)
	srv := servers.New(d)
	fip := floatingips.New(floatingips.Deps{
		Store:       d.Store,
		Catalog:     cat,
		PublicRange: d.PublicRange,
		PushAgent:   func(context.Context) error { return nil },
	})
	srv.OnDelete(fip.ServerDeleted)
	return &fipEnv{Env: apitest.New(t, srv, fip, resactions.New()), fip: fip}
}

type fipView struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	Description  *string           `json:"description"`
	IP           string            `json:"ip"`
	Type         string            `json:"type"`
	Server       *int64            `json:"server"`
	Labels       map[string]string `json:"labels"`
	HomeLocation struct {
		Name string `json:"name"`
	} `json:"home_location"`
	Protection struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
}

type serverView struct {
	ID       int64 `json:"id"`
	Location struct {
		Name string `json:"name"`
	} `json:"location"`
	PublicNet struct {
		IPv4 *struct {
			IP string `json:"ip"`
		} `json:"ipv4"`
		FloatingIPs []int64 `json:"floating_ips"`
	} `json:"public_net"`
}

// createFIP posts a floating IP create, expects 201 and returns it.
func createFIP(t *testing.T, e *fipEnv, body map[string]any) fipView {
	t.Helper()
	resp := e.Do(http.MethodPost, "/floating_ips", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /floating_ips %v: status %d (%s), want 201", body, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		FloatingIP fipView `json:"floating_ip"`
	}
	apitest.Decode(t, resp, &b)
	return b.FloatingIP
}

// getFIP returns the floating IP and whether it exists.
func getFIP(t *testing.T, e *fipEnv, id int64) (fipView, bool) {
	t.Helper()
	resp := e.Do(http.MethodGet, fmt.Sprintf("/floating_ips/%d", id), nil)
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return fipView{}, false
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /floating_ips/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		FloatingIP fipView `json:"floating_ip"`
	}
	apitest.Decode(t, resp, &b)
	return b.FloatingIP, true
}

func mustFIP(t *testing.T, e *fipEnv, id int64) fipView {
	t.Helper()
	f, ok := getFIP(t, e, id)
	if !ok {
		t.Fatalf("GET /floating_ips/%d: 404, want the floating IP", id)
	}
	return f
}

func fipAction(e *fipEnv, id int64, name string, body any) *http.Response {
	return e.Do(http.MethodPost, fmt.Sprintf("/floating_ips/%d/actions/%s", id, name), body)
}

// runFIPAction posts the action, expects 201 and waits for its success.
func runFIPAction(t *testing.T, e *fipEnv, id int64, name string, body any) {
	t.Helper()
	resp := fipAction(e, id, name, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /floating_ips/%d/actions/%s %v: status %d (%s), want 201", id, name, body, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	waitSuccess(t, e, b.Action.ID, fmt.Sprintf("%s floating IP %d", name, id))
}

func waitSuccess(t *testing.T, e *fipEnv, act int64, what string) {
	t.Helper()
	if a := e.WaitAction(act); a.Status != "success" {
		t.Fatalf("%s: action %q %+v, want success", what, a.Status, a.Error)
	}
}

// createServer creates a running server of type typ in location.
func createServer(t *testing.T, e *fipEnv, name, typ, location string) serverView {
	t.Helper()
	req := map[string]any{"name": name, "server_type": typ, "image": "ubuntu-24.04", "location": location}
	resp := e.Do(http.MethodPost, "/servers", req)
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
	return getServer(t, e, b.Server.ID)
}

func getServer(t *testing.T, e *fipEnv, id int64) serverView {
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

func deleteServer(t *testing.T, e *fipEnv, id int64) {
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

func serverOf(f fipView) int64 {
	if f.Server == nil {
		return 0
	}
	return *f.Server
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

// wantFieldError checks a 422 invalid_input naming field in
// error.details.fields.
func wantFieldError(t *testing.T, resp *http.Response, field, what string) {
	t.Helper()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("%s: status %d, want 422", what, resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Fields []struct {
					Name string `json:"name"`
				} `json:"fields"`
			} `json:"details"`
		} `json:"error"`
	}
	apitest.Decode(t, resp, &body)
	var names []string
	for _, f := range body.Error.Details.Fields {
		names = append(names, f.Name)
	}
	if body.Error.Code != "invalid_input" || !slices.Contains(names, field) {
		t.Errorf("%s: error %q on fields %v, want invalid_input on %s", what, body.Error.Code, names, field)
	}
}

// target returns the target of the agent document's entry for the
// floating IP ip, failing when there is no entry.
func target(t *testing.T, e *fipEnv, ip string) netip.Addr {
	t.Helper()
	addr := netip.MustParseAddr(ip)
	var doc agentapi.Config
	if err := e.Store.Tx(func(tx store.Tx) error { return e.fip.Contribute(tx, &doc) }); err != nil {
		t.Fatalf("Contribute: %v", err)
	}
	for _, f := range doc.FloatingIPs {
		if f.IP == addr {
			return f.Target
		}
	}
	t.Fatalf("agent document FloatingIPs = %+v, want an entry for %s", doc.FloatingIPs, ip)
	return netip.Addr{}
}

func TestFloatingIPs(t *testing.T) {
	t.Run("FIP-01/create_delete", func(t *testing.T) {
		e := newFIPEnv(t)

		wantFieldError(t, e.Do(http.MethodPost, "/floating_ips", map[string]any{"name": "untyped", "home_location": "fsn1"}),
			"type", "create without type")

		s := createServer(t, e, "holder", "cx23", "nbg1")
		wantError(t, e.Do(http.MethodPost, "/floating_ips",
			map[string]any{"type": "ipv4", "server": s.ID, "home_location": "fsn1"}),
			http.StatusUnprocessableEntity, "invalid_input", "create with server and home_location")

		free := createFIP(t, e, map[string]any{"name": "free", "type": "ipv4", "home_location": "fsn1"})
		if free.HomeLocation.Name != "fsn1" || free.Server != nil || free.Type != "ipv4" {
			t.Errorf("created with home_location fsn1: %+v, want home location fsn1, unassigned, ipv4", free)
		}
		held := createFIP(t, e, map[string]any{"name": "held", "type": "ipv4", "server": s.ID})
		if held.HomeLocation.Name != s.Location.Name || serverOf(held) != s.ID {
			t.Errorf("created with server %d: home location %q server %v, want %q and %d",
				s.ID, held.HomeLocation.Name, held.Server, s.Location.Name, s.ID)
		}

		resp := e.Do(http.MethodGet, "/floating_ips", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /floating_ips: status %d, want 200", resp.StatusCode)
		}
		var list struct {
			FloatingIPs []fipView `json:"floating_ips"`
		}
		apitest.Decode(t, resp, &list)
		var ids []int64
		for _, f := range list.FloatingIPs {
			ids = append(ids, f.ID)
		}
		if !slices.Contains(ids, free.ID) || !slices.Contains(ids, held.ID) {
			t.Errorf("GET /floating_ips = %v, want %d and %d", ids, free.ID, held.ID)
		}

		resp = e.Do(http.MethodPut, fmt.Sprintf("/floating_ips/%d", free.ID),
			map[string]any{"name": "renamed", "description": "spare", "labels": map[string]string{"env": "test"}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT /floating_ips/%d: status %d (%s), want 200", free.ID, resp.StatusCode, apitest.ErrorCode(t, resp))
		}
		_ = resp.Body.Close()
		if f := mustFIP(t, e, free.ID); f.Name != "renamed" || f.Description == nil || *f.Description != "spare" || f.Labels["env"] != "test" {
			t.Errorf("after PUT: %+v, want name renamed, description spare, label env=test", f)
		}

		wantError(t, e.Do(http.MethodDelete, fmt.Sprintf("/floating_ips/%d", held.ID), nil),
			http.StatusUnprocessableEntity, "must_be_unassigned", "DELETE assigned floating IP")

		runFIPAction(t, e, free.ID, "change_protection", map[string]any{"delete": true})
		wantError(t, e.Do(http.MethodDelete, fmt.Sprintf("/floating_ips/%d", free.ID), nil),
			http.StatusLocked, "protected", "DELETE protected floating IP")
		runFIPAction(t, e, free.ID, "change_protection", map[string]any{"delete": false})
		resp = e.Do(http.MethodDelete, fmt.Sprintf("/floating_ips/%d", free.ID), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("DELETE /floating_ips/%d unassigned: status %d (%s), want 204", free.ID, resp.StatusCode, apitest.ErrorCode(t, resp))
		} else {
			_ = resp.Body.Close()
		}
		if _, ok := getFIP(t, e, free.ID); ok {
			t.Errorf("floating IP %d still there after DELETE", free.ID)
		}

		deleteServer(t, e, s.ID)
		if f := mustFIP(t, e, held.ID); f.Server != nil {
			t.Errorf("after deleting its server: server %d, want null", *f.Server)
		}
	})

	t.Run("FIP-01/assign", func(t *testing.T) {
		e := newFIPEnv(t)
		ip := createFIP(t, e, map[string]any{"name": "move", "type": "ipv4", "home_location": "fsn1"})
		s := createServer(t, e, "a", "cx23", "nbg1")

		runFIPAction(t, e, ip.ID, "assign", map[string]any{"server": s.ID})
		if got := serverOf(mustFIP(t, e, ip.ID)); got != s.ID {
			t.Errorf("after assign: server %d, want %d", got, s.ID)
		}
		if got := getServer(t, e, s.ID).PublicNet.FloatingIPs; !slices.Equal(got, []int64{ip.ID}) {
			t.Errorf("after assign: server public_net.floating_ips = %v, want [%d]", got, ip.ID)
		}

		wantError(t, fipAction(e, ip.ID, "assign", map[string]any{"server": s.ID}),
			http.StatusUnprocessableEntity, "floating_ip_assigned", "assign to the same server again")

		far := createServer(t, e, "far", "cpx11", "ash")
		wantError(t, fipAction(e, ip.ID, "assign", map[string]any{"server": far.ID}),
			http.StatusUnprocessableEntity, "invalid_input", "assign to a server outside the home network zone")
		if got := serverOf(mustFIP(t, e, ip.ID)); got != s.ID {
			t.Errorf("after refused assign: server %d, want %d", got, s.ID)
		}

		runFIPAction(t, e, ip.ID, "unassign", nil)
		if f := mustFIP(t, e, ip.ID); f.Server != nil {
			t.Errorf("after unassign: server %d, want null", *f.Server)
		}
		if got := getServer(t, e, s.ID).PublicNet.FloatingIPs; len(got) != 0 {
			t.Errorf("after unassign: server public_net.floating_ips = %v, want []", got)
		}
	})

	t.Run("FIP-02", func(t *testing.T) {
		e := newFIPEnv(t)
		a := createServer(t, e, "a", "cx23", "fsn1")
		b := createServer(t, e, "b", "cx23", "fsn1")
		if a.PublicNet.IPv4 == nil || b.PublicNet.IPv4 == nil {
			t.Fatalf("setup: servers without public IPv4: %+v, %+v", a.PublicNet, b.PublicNet)
		}
		ip := createFIP(t, e, map[string]any{"name": "shared", "type": "ipv4", "home_location": "fsn1"})

		runFIPAction(t, e, ip.ID, "assign", map[string]any{"server": a.ID})
		if got := target(t, e, ip.IP); got != netip.MustParseAddr(a.PublicNet.IPv4.IP) {
			t.Errorf("assigned to A: target %v, want A's IPv4 %s", got, a.PublicNet.IPv4.IP)
		}

		runFIPAction(t, e, ip.ID, "assign", map[string]any{"server": b.ID})
		if got := target(t, e, ip.IP); got != netip.MustParseAddr(b.PublicNet.IPv4.IP) {
			t.Errorf("reassigned to B: target %v, want B's IPv4 %s", got, b.PublicNet.IPv4.IP)
		}

		runFIPAction(t, e, ip.ID, "unassign", nil)
		if got := target(t, e, ip.IP); got.IsValid() {
			t.Errorf("unassigned: target %v, want an invalid address", got)
		}
	})
}
