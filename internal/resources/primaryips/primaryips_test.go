package primaryips_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/primaryips"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// plenty is a host with room for every server of the tests.
var plenty = platform.Resources{MemoryMB: 64 << 10, FreeMemoryMB: 64 << 10, LogicalCPUs: 16, FreeDiskBytes: 1 << 50}

var publicRange = netip.MustParsePrefix("198.19.0.0/16")

type pipEnv struct {
	*apitest.Env
	pip    *primaryips.Module
	pushes *atomic.Int64     // agent pushes of both modules
	gate   *apitest.PushGate // holds the agent pushes of both modules
}

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
// servers, follow server deletion and handle the servers' reverse DNS.
// Both modules count their agent pushes in pushes.
func newPIPEnv(t *testing.T) *pipEnv {
	t.Helper()
	cat := loadCatalog(t)
	fake := platformtest.NewFake()
	fake.SetResources(plenty)
	d := apitest.ServersDeps(t, cat, fake)
	pushes := new(atomic.Int64)
	gate := apitest.NewPushGate(t)
	d.PushAgent = func(ctx context.Context) error {
		if err := gate.Wait(ctx); err != nil {
			return err
		}
		pushes.Add(1)
		return nil
	}
	srv := servers.New(d)
	pip := newModule(d, cat, srv)
	srv.SetPublicIPs(pip)
	srv.OnDelete(pip.ServerDeleted)
	srv.SetDNSPtrs(pip)
	return &pipEnv{Env: apitest.New(t, srv, pip, resactions.New()), pip: pip, pushes: pushes, gate: gate}
}

func newModule(d servers.Deps, cat *catalog.Catalog, srv *servers.Module) *primaryips.Module {
	return primaryips.New(primaryips.Deps{
		Store:       d.Store,
		Catalog:     cat,
		PublicRange: d.PublicRange,
		Servers:     srv,
		PushAgent:   d.PushAgent,
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
	DNSPtr       []dnsPtr          `json:"dns_ptr"`
	Labels       map[string]string `json:"labels"`
	Protection   struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
}

type dnsPtr struct {
	IP     string `json:"ip"`
	DNSPtr string `json:"dns_ptr"`
}

type publicNet struct {
	IPv4 *struct {
		ID     int64  `json:"id"`
		IP     string `json:"ip"`
		DNSPtr string `json:"dns_ptr"`
	} `json:"ipv4"`
	IPv6 *struct {
		ID     int64    `json:"id"`
		IP     string   `json:"ip"`
		DNSPtr []dnsPtr `json:"dns_ptr"`
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

// wantInvalidField expects 422 invalid_input naming field.
func wantInvalidField(t *testing.T, resp *http.Response, field, what string) {
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

// changeDNSPtr posts change_dns_ptr to path (".../actions/change_dns_ptr"
// is appended), expects 201 with a change_dns_ptr action and waits for its
// success and an agent push.
func changeDNSPtr(t *testing.T, e *pipEnv, path, ip string, ptr any) {
	t.Helper()
	before := e.pushes.Load()
	body := map[string]any{"ip": ip, "dns_ptr": ptr}
	resp := e.Do(http.MethodPost, path+"/actions/change_dns_ptr", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST %s/actions/change_dns_ptr %v: status %d (%s), want 201", path, body, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	if b.Action.Command != "change_dns_ptr" {
		t.Errorf("POST %s/actions/change_dns_ptr: action command %q, want change_dns_ptr", path, b.Action.Command)
	}
	waitSuccess(t, e, b.Action.ID, fmt.Sprintf("change_dns_ptr %s on %s", ip, path))
	if e.pushes.Load() == before {
		t.Errorf("change_dns_ptr %s on %s: no agent push", ip, path)
	}
}

// defaultPTR is Hetzner's default reverse name of an IPv4 address.
func defaultPTR(ip string) string {
	a := netip.MustParseAddr(ip).As4()
	return fmt.Sprintf("static.%d.%d.%d.%d.clients.your-server.de", a[3], a[2], a[1], a[0])
}

// inNet returns the address with last byte host in the /64 of ip, which is
// either the network ("2001:db8::/64") or an address.
func inNet(t *testing.T, ip string, host byte) string {
	t.Helper()
	p, err := netip.ParsePrefix(ip)
	if err != nil {
		a, err := netip.ParseAddr(ip)
		if err != nil {
			t.Fatalf("IPv6 %q is neither a prefix nor an address", ip)
		}
		p = netip.PrefixFrom(a, 64)
	}
	b := p.Masked().Addr().As16()
	b[15] = host
	return netip.AddrFrom16(b).String()
}

// ptrFor returns the dns_ptr of the entry for ip and whether there is one.
func ptrFor(entries []dnsPtr, ip string) (string, bool) {
	want := netip.MustParseAddr(ip)
	for _, en := range entries {
		if a, err := netip.ParseAddr(en.IP); err == nil && a == want {
			return en.DNSPtr, true
		}
	}
	return "", false
}

// pushedPTR returns the PTR name the primary IPs contribute to the agent
// document for ip ("" if none).
func pushedPTR(t *testing.T, e *pipEnv, ip string) string {
	t.Helper()
	var doc agentapi.Config
	if err := e.Store.Tx(func(tx store.Tx) error { return e.pip.Contribute(tx, &doc) }); err != nil {
		t.Fatalf("Contribute: %v", err)
	}
	return doc.PTR[netip.MustParseAddr(ip)]
}

// wantPIPPtr checks the primary IP's dns_ptr entry for ip.
func wantPIPPtr(t *testing.T, e *pipEnv, id int64, ip, want, what string) {
	t.Helper()
	got, ok := ptrFor(mustPIP(t, e, id).DNSPtr, ip)
	if !ok || got != want {
		t.Errorf("%s: primary IP %d dns_ptr for %s = %q (present %v), want %q", what, id, ip, got, ok, want)
	}
}

func TestPrimaryIPs(t *testing.T) {
	t.Run("LB-01", testLoadBalancerIPs)
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
		e := &pipEnv{Env: apitest.New(t, srv, resactions.New())}
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

	t.Run("RDNS-01/primary_ip", func(t *testing.T) {
		e := newPIPEnv(t)

		v4 := createPIP(t, e, map[string]any{"name": "rdns-v4", "type": "ipv4", "location": "fsn1"})
		path := fmt.Sprintf("/primary_ips/%d", v4.ID)
		wantPIPPtr(t, e, v4.ID, v4.IP, defaultPTR(v4.IP), "new primary IPv4")

		changeDNSPtr(t, e, path, v4.IP, "mail.example.com")
		wantPIPPtr(t, e, v4.ID, v4.IP, "mail.example.com", "after change_dns_ptr")
		if got := pushedPTR(t, e, v4.IP); got != "mail.example.com" {
			t.Errorf("after change_dns_ptr: agent PTR for %s = %q, want mail.example.com", v4.IP, got)
		}

		changeDNSPtr(t, e, path, v4.IP, nil)
		wantPIPPtr(t, e, v4.ID, v4.IP, defaultPTR(v4.IP), "after dns_ptr null")
		if got := pushedPTR(t, e, v4.IP); got != defaultPTR(v4.IP) {
			t.Errorf("after dns_ptr null: agent PTR for %s = %q, want %q", v4.IP, got, defaultPTR(v4.IP))
		}

		v6 := createPIP(t, e, map[string]any{"name": "rdns-v6", "type": "ipv6", "location": "fsn1"})
		path6 := fmt.Sprintf("/primary_ips/%d", v6.ID)
		host := inNet(t, v6.IP, 1)
		changeDNSPtr(t, e, path6, host, "v6.example.com")
		wantPIPPtr(t, e, v6.ID, host, "v6.example.com", "after IPv6 change_dns_ptr")

		changeDNSPtr(t, e, path6, host, nil)
		if got, ok := ptrFor(mustPIP(t, e, v6.ID).DNSPtr, host); ok {
			t.Errorf("after IPv6 dns_ptr null: entry for %s = %q, want none", host, got)
		}
	})

	t.Run("RDNS-01/server", func(t *testing.T) {
		e := newPIPEnv(t)

		s := getServer(t, e, createServer(t, e, "rdns", "fsn1", nil))
		if s.PublicNet.IPv4 == nil || s.PublicNet.IPv6 == nil {
			t.Fatalf("server public_net = %+v, want IPv4 and IPv6", s.PublicNet)
		}
		v4 := s.PublicNet.IPv4.IP
		path := fmt.Sprintf("/servers/%d", s.ID)

		changeDNSPtr(t, e, path, v4, "web.example.com")
		got := getServer(t, e, s.ID)
		if got.PublicNet.IPv4 == nil || got.PublicNet.IPv4.DNSPtr != "web.example.com" {
			t.Errorf("after server change_dns_ptr: public_net.ipv4 = %+v, want dns_ptr web.example.com", got.PublicNet.IPv4)
		}
		wantPIPPtr(t, e, s.PublicNet.IPv4.ID, v4, "web.example.com", "server's primary IPv4 after server change_dns_ptr")
		if p := pushedPTR(t, e, v4); p != "web.example.com" {
			t.Errorf("after server change_dns_ptr: agent PTR for %s = %q, want web.example.com", v4, p)
		}

		host := inNet(t, s.PublicNet.IPv6.IP, 2)
		changeDNSPtr(t, e, path, host, "web6.example.com")
		got = getServer(t, e, s.ID)
		if got.PublicNet.IPv6 == nil {
			t.Fatalf("after IPv6 change_dns_ptr: public_net.ipv6 null")
		}
		if p, ok := ptrFor(got.PublicNet.IPv6.DNSPtr, host); !ok || p != "web6.example.com" {
			t.Errorf("after IPv6 change_dns_ptr: public_net.ipv6.dns_ptr = %+v, want %s -> web6.example.com", got.PublicNet.IPv6.DNSPtr, host)
		}

		changeDNSPtr(t, e, path, v4, nil)
		if got := getServer(t, e, s.ID); got.PublicNet.IPv4 == nil || got.PublicNet.IPv4.DNSPtr != defaultPTR(v4) {
			t.Errorf("after dns_ptr null: public_net.ipv4 = %+v, want dns_ptr %s", got.PublicNet.IPv4, defaultPTR(v4))
		}

		other := createPIP(t, e, map[string]any{"name": "foreign", "type": "ipv4", "location": "fsn1"})
		wantInvalidField(t, e.Do(http.MethodPost, path+"/actions/change_dns_ptr",
			map[string]any{"ip": other.IP, "dns_ptr": "x.example.com"}), "ip", "server change_dns_ptr on an IP it does not hold")
	})

	t.Run("CLI-02", func(t *testing.T) {
		e := newPIPEnv(t)
		ip := createPIP(t, e, map[string]any{"name": "queue", "type": "ipv4", "location": "fsn1"})
		s := stoppedServer(t, e, "s", "fsn1")
		y := stoppedServer(t, e, "y", "fsn1")
		v6 := getServer(t, e, s).PublicNet.IPv6
		if v6 == nil {
			t.Fatalf("server %d has no IPv6 primary IP", s)
		}
		e.gate.Close()

		// The unassign of the server's IPv6 holds the server as a
		// secondary ref only.
		holder := startPIPAction(t, e, v6.ID, "unassign", nil)
		releaseHolder := e.gate.Next(t, "unassign of the IPv6")
		assign := startPIPAction(t, e, ip.ID, "assign", assignTo(s))
		wantError(t, pipAction(e, ip.ID, "assign", assignTo(y)),
			http.StatusLocked, "locked", "second assign of the primary IP")

		releaseHolder()
		releaseAssign := e.gate.Next(t, "assign queued behind the unassign")
		if a := e.GetAction(holder.ID); a.Status != "success" {
			t.Errorf("unassign when the queued assign worked: %q, want success", a.Status)
		}
		if got := assignee(mustPIP(t, e, ip.ID)); got != 0 {
			t.Errorf("while assign runs: assignee_id %d, want null", got)
		}
		if got := getServer(t, e, s).PublicNet.IPv4; got != nil {
			t.Errorf("while assign runs: server public_net.ipv4 %+v, want null", got)
		}
		releaseAssign()
		waitSuccess(t, e, assign.ID, "assign")
		if got := assignee(mustPIP(t, e, ip.ID)); got != s {
			t.Errorf("after assign: assignee_id %d, want %d", got, s)
		}
		if got := getServer(t, e, s); ipv4ID(got) != ip.ID {
			t.Errorf("after assign: server public_net.ipv4 %+v, want primary IP %d", got.PublicNet.IPv4, ip.ID)
		}
	})
}

// startPIPAction posts the action, expects 201 and returns the action,
// which is still running.
func startPIPAction(t *testing.T, e *pipEnv, id int64, name string, body any) apitest.Action {
	t.Helper()
	resp := pipAction(e, id, name, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /primary_ips/%d/actions/%s %v: status %d (%s), want 201", id, name, body, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	if b.Action.Status != "running" {
		t.Errorf("%s primary IP %d: action status %q, want running", name, id, b.Action.Status)
	}
	return b.Action
}

// apiCode is the API code of an error the module returns to the
// loadbalancers module.
func apiCode(err error) string {
	var fe *api.FieldError
	if errors.As(err, &fe) {
		if fe.Code == "" {
			return "invalid_input"
		}
		return fe.Code
	}
	if err == nil {
		return ""
	}
	return "other: " + err.Error()
}

// block marks primary IP id as blocked, as Hetzner does for abuse.
func block(t *testing.T, e *pipEnv, id int64) {
	t.Helper()
	err := e.Store.Tx(func(tx store.Tx) error {
		r, err := primaryips.Load(tx, e.Project.ID, id)
		if err != nil {
			return err
		}
		r.Blocked = true
		return store.Put(tx, store.Meta{Kind: primaryips.Kind, ID: r.ID, ProjectID: r.Hlab.ProjectID,
			Name: r.Name, Labels: r.Labels, Created: r.Created}, r)
	})
	if err != nil {
		t.Fatalf("block primary IP %d: %v", id, err)
	}
}

// testLoadBalancerIPs: primary IPs held by a load balancer.
func testLoadBalancerIPs(t *testing.T) {
	e := newPIPEnv(t)
	const lbID = 77
	v4 := createPIP(t, e, map[string]any{"name": "lb-v4", "type": "ipv4", "location": "fsn1"})
	v6 := createPIP(t, e, map[string]any{"name": "lb-v6", "type": "ipv6", "location": "fsn1"})
	assignLB := func(lb int64, location string, ipv4, ipv6 *int64) (netip.Addr, netip.Prefix, error) {
		t.Helper()
		var a netip.Addr
		var p netip.Prefix
		var aerr error
		if err := e.Store.Tx(func(tx store.Tx) error {
			a, p, aerr = e.pip.AssignLoadBalancer(tx, e.Project.ID, lb, location, ipv4, ipv6)
			return aerr
		}); err != nil && aerr == nil {
			t.Fatalf("transaction: %v", err)
		}
		return a, p, aerr
	}

	a, p, err := assignLB(lbID, "fsn1", &v4.ID, &v6.ID)
	if err != nil {
		t.Fatalf("AssignLoadBalancer: %v", err)
	}
	if a.String() != v4.IP || p.String() != v6.IP {
		t.Errorf("AssignLoadBalancer = %s, %s; want %s, %s", a, p, v4.IP, v6.IP)
	}
	if primaryips.AssigneeLoadBalancer != "load_balancer" {
		t.Errorf("AssigneeLoadBalancer = %q, want load_balancer", primaryips.AssigneeLoadBalancer)
	}
	for _, id := range []int64{v4.ID, v6.ID} {
		if got := mustPIP(t, e, id); got.AssigneeType != "load_balancer" || assignee(got) != lbID {
			t.Errorf("primary IP %d after AssignLoadBalancer: assignee %q %d, want load_balancer %d", id, got.AssigneeType, assignee(got), lbID)
		}
	}

	blocked := createPIP(t, e, map[string]any{"name": "blocked", "type": "ipv4", "location": "fsn1"})
	block(t, e, blocked.ID)
	elsewhere := createPIP(t, e, map[string]any{"name": "nbg1", "type": "ipv4", "location": "nbg1"})
	free6 := createPIP(t, e, map[string]any{"name": "free6", "type": "ipv6", "location": "fsn1"})
	for _, c := range []struct {
		what       string
		ipv4, ipv6 *int64
		code       string
		status     int
	}{
		{"an assigned IP", &v4.ID, nil, "primary_ip_assigned", http.StatusUnprocessableEntity},
		{"a blocked IP", &blocked.ID, nil, "primary_ip_blocked", http.StatusUnprocessableEntity},
		{"an IP of another location", &elsewhere.ID, nil, "primary_ip_location_mismatch", http.StatusUnprocessableEntity},
		{"an IPv6 as ipv4", &free6.ID, nil, "primary_ip_version_mismatch", http.StatusPreconditionFailed},
	} {
		_, _, err := assignLB(lbID+1, "fsn1", c.ipv4, c.ipv6)
		if got := apiCode(err); got != c.code || api.Status(got) != c.status {
			t.Errorf("AssignLoadBalancer with %s: %v (status %d), want %d %s", c.what, err, api.Status(got), c.status, c.code)
		}
	}
	if got := mustPIP(t, e, free6.ID); got.AssigneeID != nil {
		t.Errorf("refused AssignLoadBalancer assigned primary IP %d to %d", free6.ID, *got.AssigneeID)
	}

	x := stoppedServer(t, e, "x", "fsn1")
	wantError(t, pipAction(e, v4.ID, "assign", assignTo(x)),
		http.StatusUnprocessableEntity, "primary_ip_already_assigned", "assign of an LB-held IP")
	wantError(t, pipAction(e, v4.ID, "unassign", nil),
		http.StatusUnprocessableEntity, "invalid_input", "unassign of an LB-held IP")

	resp := e.Do(http.MethodPut, fmt.Sprintf("/primary_ips/%d", v6.ID), map[string]any{"auto_delete": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /primary_ips/%d auto_delete: status %d (%s), want 200", v6.ID, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	_ = resp.Body.Close()
	if err := e.Store.Tx(func(tx store.Tx) error { return e.pip.LoadBalancerDeleted(tx, lbID) }); err != nil {
		t.Fatalf("LoadBalancerDeleted: %v", err)
	}
	if got := mustPIP(t, e, v4.ID); got.AssigneeID != nil {
		t.Errorf("after LoadBalancerDeleted: primary IP %d assignee %q %d, want unassigned", v4.ID, got.AssigneeType, *got.AssigneeID)
	}
	if _, ok := getPIP(t, e, v6.ID); ok {
		t.Errorf("after LoadBalancerDeleted: primary IP %d with auto_delete still there", v6.ID)
	}
}
