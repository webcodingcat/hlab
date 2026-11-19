package firewalls_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/firewalls"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// plenty is a host with room for every server of the tests.
var plenty = platform.Resources{MemoryMB: 64 << 10, FreeMemoryMB: 64 << 10, LogicalCPUs: 16, FreeDiskBytes: 1 << 50}

// noIPv4 is the create field for a server without public IPv4.
var noIPv4 = map[string]any{"public_net": map[string]any{"enable_ipv4": false, "enable_ipv6": true}}

// unknownID is an ID no resource of the tests has.
const unknownID = 999999

type fwEnv struct {
	*apitest.Env
	srv *servers.Module
	fw  *firewalls.Module

	failPush atomic.Bool
	mu       sync.Mutex
	pushes   []agentapi.Config
}

// newFWEnv mounts the servers and firewalls modules on a fake platform,
// wired like the cloud does. The agent push records the document the
// servers and firewalls modules contribute; it fails while failPush is set.
func newFWEnv(t *testing.T) *fwEnv {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	fake := platformtest.NewFake()
	fake.SetResources(plenty)
	e := &fwEnv{}
	d := apitest.ServersDeps(t, cat, fake)
	d.PushAgent = e.push
	e.srv = servers.New(d)
	e.fw = firewalls.New(firewalls.Deps{Store: d.Store, PushAgent: e.push})
	e.srv.SetFirewalls(e.fw)
	e.srv.OnDelete(e.fw.ServerDeleted)
	e.Env = apitest.New(t, e.srv, e.fw, resactions.New())
	return e
}

func (e *fwEnv) push(context.Context) error {
	if e.failPush.Load() {
		return errors.New("agent unreachable")
	}
	var doc agentapi.Config
	err := e.Store.Tx(func(tx store.Tx) error {
		if err := e.srv.Contribute(tx, &doc); err != nil {
			return err
		}
		return e.fw.Contribute(tx, &doc)
	})
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.pushes = append(e.pushes, doc)
	e.mu.Unlock()
	return nil
}

// pushCount returns the number of successful pushes so far.
func (e *fwEnv) pushCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pushes)
}

// waitPush waits until more than after pushes succeeded and returns the
// latest pushed document.
func (e *fwEnv) waitPush(t *testing.T, after int) agentapi.Config {
	t.Helper()
	deadline := time.Now().Add(apitest.ActionTimeout)
	for {
		e.mu.Lock()
		n := len(e.pushes)
		var last agentapi.Config
		if n > 0 {
			last = e.pushes[n-1]
		}
		e.mu.Unlock()
		if n > after {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("no agent push after push %d within %s", after, apitest.ActionTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type ref struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type actionView struct {
	ID        int64  `json:"id"`
	Command   string `json:"command"`
	Status    string `json:"status"`
	Resources []ref  `json:"resources"`
}

// names reports whether the action names exactly the given resources.
func (a actionView) names(want ...ref) bool {
	return len(a.Resources) == len(want) && !slices.ContainsFunc(want, func(r ref) bool {
		return !slices.Contains(a.Resources, r)
	})
}

type serverRef struct {
	ID int64 `json:"id"`
}

type appliedView struct {
	Type          string     `json:"type"`
	Server        *serverRef `json:"server"`
	LabelSelector *struct {
		Selector string `json:"selector"`
	} `json:"label_selector"`
	AppliedToResources []struct {
		Type   string    `json:"type"`
		Server serverRef `json:"server"`
	} `json:"applied_to_resources"`
}

type fwView struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Labels    map[string]string `json:"labels"`
	Rules     []firewalls.Rule  `json:"rules"`
	AppliedTo []appliedView     `json:"applied_to"`
}

// appliedServers returns the IDs of the servers applied directly.
func (f fwView) appliedServers() []int64 {
	var ids []int64
	for _, a := range f.AppliedTo {
		if a.Type == "server" && a.Server != nil {
			ids = append(ids, a.Server.ID)
		}
	}
	return ids
}

// selector returns the applied_to entry of label selector sel.
func (f fwView) selector(sel string) (appliedView, bool) {
	for _, a := range f.AppliedTo {
		if a.Type == "label_selector" && a.LabelSelector != nil && a.LabelSelector.Selector == sel {
			return a, true
		}
	}
	return appliedView{}, false
}

type serverFirewall struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

type serverView struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Labels    map[string]string `json:"labels"`
	PublicNet struct {
		Firewalls []serverFirewall `json:"firewalls"`
	} `json:"public_net"`
}

func ptr(s string) *string { return &s }

// inRule is an inbound rule as Hetzner answers it.
func inRule(protocol string, port *string, src ...string) firewalls.Rule {
	return firewalls.Rule{Direction: "in", Protocol: protocol, Port: port, SourceIPs: src, DestinationIPs: []string{}}
}

// outRule is an outbound rule as Hetzner answers it.
func outRule(protocol string, port *string, dst ...string) firewalls.Rule {
	return firewalls.Rule{Direction: "out", Protocol: protocol, Port: port, SourceIPs: []string{}, DestinationIPs: dst}
}

func serverTarget(id int64) map[string]any {
	return map[string]any{"type": "server", "server": map[string]any{"id": id}}
}

func selectorTarget(sel string) map[string]any {
	return map[string]any{"type": "label_selector", "label_selector": map[string]any{"selector": sel}}
}

// waitAll waits for every action and fails unless each ends with status.
func waitAll(t *testing.T, e *fwEnv, acts []actionView, status, what string) {
	t.Helper()
	for _, a := range acts {
		if got := e.For(t).WaitAction(a.ID); got.Status != status {
			t.Fatalf("%s: action %d %s ended %q %+v, want %s", what, a.ID, a.Command, got.Status, got.Error, status)
		}
	}
}

// createFirewall posts a firewall create, expects 201, waits for its
// actions to succeed and returns the firewall and the actions.
func createFirewall(t *testing.T, e *fwEnv, body map[string]any) (fwView, []actionView) {
	t.Helper()
	resp := e.Do(http.MethodPost, "/firewalls", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /firewalls %v: status %d (%s), want 201", body, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Firewall fwView       `json:"firewall"`
		Actions  []actionView `json:"actions"`
	}
	apitest.Decode(t, resp, &b)
	waitAll(t, e, b.Actions, "success", "create firewall")
	return b.Firewall, b.Actions
}

// getFirewall returns the firewall and whether it exists.
func getFirewall(t *testing.T, e *fwEnv, id int64) (fwView, bool) {
	t.Helper()
	resp := e.Do(http.MethodGet, fmt.Sprintf("/firewalls/%d", id), nil)
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return fwView{}, false
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /firewalls/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		Firewall fwView `json:"firewall"`
	}
	apitest.Decode(t, resp, &b)
	return b.Firewall, true
}

func mustFirewall(t *testing.T, e *fwEnv, id int64) fwView {
	t.Helper()
	f, ok := getFirewall(t, e, id)
	if !ok {
		t.Fatalf("GET /firewalls/%d: 404, want the firewall", id)
	}
	return f
}

func fwAction(e *fwEnv, id int64, name string, body any) *http.Response {
	return e.Do(http.MethodPost, fmt.Sprintf("/firewalls/%d/actions/%s", id, name), body)
}

// startFWAction posts the action, expects 201 and returns its actions
// without waiting for them.
func startFWAction(t *testing.T, e *fwEnv, id int64, name string, body any) []actionView {
	t.Helper()
	resp := fwAction(e, id, name, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /firewalls/%d/actions/%s %v: status %d (%s), want 201", id, name, body, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Actions []actionView `json:"actions"`
	}
	apitest.Decode(t, resp, &b)
	return b.Actions
}

// runFWAction is startFWAction waiting for every action's success.
func runFWAction(t *testing.T, e *fwEnv, id int64, name string, body any) []actionView {
	t.Helper()
	acts := startFWAction(t, e, id, name, body)
	waitAll(t, e, acts, "success", fmt.Sprintf("%s firewall %d", name, id))
	return acts
}

// createServer creates a running server with the extra create fields.
func createServer(t *testing.T, e *fwEnv, name string, extra map[string]any) serverView {
	t.Helper()
	req := map[string]any{"name": name, "server_type": "cx23", "image": "ubuntu-24.04", "location": "fsn1"}
	for k, v := range extra {
		req[k] = v
	}
	resp := e.Do(http.MethodPost, "/servers", req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers %s: status %d (%s), want 201", name, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Server      serverView   `json:"server"`
		Action      actionView   `json:"action"`
		NextActions []actionView `json:"next_actions"`
	}
	apitest.Decode(t, resp, &b)
	waitAll(t, e, append([]actionView{b.Action}, b.NextActions...), "success", "create server "+name)
	return getServer(t, e, b.Server.ID)
}

func getServer(t *testing.T, e *fwEnv, id int64) serverView {
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

func deleteServer(t *testing.T, e *fwEnv, id int64) {
	t.Helper()
	resp := e.Do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /servers/%d: status %d (%s), want 200", id, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Action actionView `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	waitAll(t, e, []actionView{b.Action}, "success", fmt.Sprintf("delete server %d", id))
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"details"`
	} `json:"error"`
}

// wantError checks status and error.code and returns the error body.
func wantError(t *testing.T, resp *http.Response, status int, code, what string) errorBody {
	t.Helper()
	var b errorBody
	apitest.Decode(t, resp, &b)
	if resp.StatusCode != status || b.Error.Code != code {
		t.Errorf("%s: status %d code %q, want %d %s", what, resp.StatusCode, b.Error.Code, status, code)
	}
	return b
}

// wantFieldError checks a 422 invalid_input naming field in
// error.details.fields.
func wantFieldError(t *testing.T, resp *http.Response, field, what string) {
	t.Helper()
	b := wantError(t, resp, http.StatusUnprocessableEntity, "invalid_input", what)
	var names []string
	for _, f := range b.Error.Details.Fields {
		names = append(names, f.Name)
	}
	if !slices.Contains(names, field) {
		t.Errorf("%s: error on fields %v, want %s", what, names, field)
	}
}

func TestFirewalls(t *testing.T) {
	t.Run("FW-01/rules", testRules)
	t.Run("FW-01/crud", testCRUD)
	t.Run("FW-01/apply_remove", testApplyRemove)
	t.Run("FW-01/label_selector", testLabelSelector)
	t.Run("FW-01/set_rules", testSetRules)
	t.Run("FW-01/delete_in_use", testDeleteInUse)
	t.Run("FW-01/server_create", testServerCreate)
	t.Run("FW-05", testLabelChange)
}

func testCRUD(t *testing.T) {
	e := newFWEnv(t)
	rules := []firewalls.Rule{
		inRule("tcp", ptr("80"), "0.0.0.0/0", "::/0"),
		outRule("udp", ptr("53"), "10.0.0.0/8"),
	}
	rules[1].Description = ptr("dns")
	resp := e.Do(http.MethodPost, "/firewalls", map[string]any{
		"name": "web", "labels": map[string]string{"env": "prod"}, "rules": rules,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /firewalls: status %d (%s), want 201", resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var created struct {
		Firewall fwView       `json:"firewall"`
		Actions  []actionView `json:"actions"`
	}
	apitest.Decode(t, resp, &created)
	fw := created.Firewall
	if fw.Name != "web" || fw.Labels["env"] != "prod" || !reflect.DeepEqual(fw.Rules, rules) {
		t.Errorf("created firewall %+v, want name web, label env=prod, rules %+v", fw, rules)
	}
	if fw.AppliedTo == nil || len(fw.AppliedTo) != 0 {
		t.Errorf("created firewall applied_to %+v, want []", fw.AppliedTo)
	}
	if len(created.Actions) != 1 || created.Actions[0].Command != "set_firewall_rules" ||
		!created.Actions[0].names(ref{fw.ID, "firewall"}) {
		t.Fatalf("create actions %+v, want one set_firewall_rules naming firewall %d", created.Actions, fw.ID)
	}
	act := created.Actions[0].ID
	waitAll(t, e, created.Actions, "success", "create firewall")
	resp = e.Do(http.MethodGet, fmt.Sprintf("/actions?id=%d", act), nil)
	var listed struct {
		Actions []actionView `json:"actions"`
	}
	apitest.Decode(t, resp, &listed)
	if resp.StatusCode != http.StatusOK || len(listed.Actions) != 1 || listed.Actions[0].ID != act || listed.Actions[0].Status != "success" {
		t.Errorf("GET /actions?id=%d: status %d actions %+v, want the action with status success", act, resp.StatusCode, listed.Actions)
	}

	bad := []firewalls.Rule{inRule("tcp", nil, "0.0.0.0/0")}
	wantFieldError(t, e.Do(http.MethodPost, "/firewalls", map[string]any{"name": "bad", "rules": bad}),
		"rules[0].port", "create with a tcp rule without port")
	resp = e.Do(http.MethodGet, "/firewalls?name=bad", nil)
	var none struct {
		Firewalls []fwView `json:"firewalls"`
	}
	apitest.Decode(t, resp, &none)
	if len(none.Firewalls) != 0 {
		t.Errorf("refused create stored %+v", none.Firewalls)
	}

	resp = e.Do(http.MethodGet, "/firewalls", nil)
	var list struct {
		Firewalls []fwView `json:"firewalls"`
	}
	apitest.Decode(t, resp, &list)
	if resp.StatusCode != http.StatusOK || len(list.Firewalls) != 1 || list.Firewalls[0].ID != fw.ID {
		t.Errorf("GET /firewalls: status %d %+v, want firewall %d", resp.StatusCode, list.Firewalls, fw.ID)
	}
	if got := mustFirewall(t, e, fw.ID); !reflect.DeepEqual(got.Rules, rules) {
		t.Errorf("GET /firewalls/%d rules %+v, want %+v", fw.ID, got.Rules, rules)
	}

	resp = e.Do(http.MethodPut, fmt.Sprintf("/firewalls/%d", fw.ID),
		map[string]any{"name": "renamed", "labels": map[string]string{"env": "test"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /firewalls/%d: status %d (%s), want 200", fw.ID, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	_ = resp.Body.Close()
	if got := mustFirewall(t, e, fw.ID); got.Name != "renamed" || !reflect.DeepEqual(got.Labels, map[string]string{"env": "test"}) {
		t.Errorf("after PUT: %+v, want name renamed, labels env=test", got)
	}

	resp = e.Do(http.MethodDelete, fmt.Sprintf("/firewalls/%d", fw.ID), nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE /firewalls/%d: status %d (%s), want 204", fw.ID, resp.StatusCode, apitest.ErrorCode(t, resp))
	} else {
		_ = resp.Body.Close()
	}
	if _, ok := getFirewall(t, e, fw.ID); ok {
		t.Errorf("firewall %d still there after DELETE", fw.ID)
	}
}
