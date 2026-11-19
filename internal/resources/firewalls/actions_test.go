package firewalls_test

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/resources/firewalls"
)

var sshRule = inRule("tcp", ptr("22"), "0.0.0.0/0")

func applyBody(targets ...map[string]any) map[string]any {
	return map[string]any{"apply_to": targets}
}

func removeBody(targets ...map[string]any) map[string]any {
	return map[string]any{"remove_from": targets}
}

func commands(acts []actionView) []string {
	var c []string
	for _, a := range acts {
		c = append(c, a.Command)
	}
	return c
}

func testApplyRemove(t *testing.T) {
	e := newFWEnv(t)
	fw, _ := createFirewall(t, e, map[string]any{"name": "fw", "rules": []firewalls.Rule{sshRule}})
	s := createServer(t, e, "s", nil)

	before := e.pushCount()
	acts := startFWAction(t, e, fw.ID, "apply_to_resources", applyBody(serverTarget(s.ID)))
	if len(acts) != 1 || acts[0].Command != "apply_firewall" || !acts[0].names(ref{s.ID, "server"}, ref{fw.ID, "firewall"}) {
		t.Fatalf("apply_to_resources actions %+v, want one apply_firewall naming server %d and firewall %d", acts, s.ID, fw.ID)
	}
	waitAll(t, e, acts, "success", "apply firewall")
	if e.pushCount() <= before {
		t.Errorf("apply_firewall ended success without an agent push")
	}
	if got := mustFirewall(t, e, fw.ID).appliedServers(); !slices.Equal(got, []int64{s.ID}) {
		t.Errorf("after apply: applied_to servers %v, want [%d]", got, s.ID)
	}
	if got := getServer(t, e, s.ID).PublicNet.Firewalls; !slices.Equal(got, []serverFirewall{{fw.ID, "applied"}}) {
		t.Errorf("after apply: server public_net.firewalls %+v, want [{%d applied}]", got, fw.ID)
	}

	wantError(t, fwAction(e, fw.ID, "apply_to_resources", applyBody(serverTarget(s.ID))),
		http.StatusUnprocessableEntity, "firewall_already_applied", "apply to the same server again")
	wantError(t, fwAction(e, fw.ID, "apply_to_resources", applyBody(serverTarget(unknownID))),
		http.StatusNotFound, "firewall_resource_not_found", "apply to an unknown server")
	private := createServer(t, e, "private", noIPv4)
	wantError(t, fwAction(e, fw.ID, "apply_to_resources", applyBody(serverTarget(private.ID))),
		http.StatusUnprocessableEntity, "private_net_only_server", "apply to a server without public IPv4")

	for i := 2; i <= 5; i++ {
		f, _ := createFirewall(t, e, map[string]any{"name": fmt.Sprintf("fw%d", i)})
		runFWAction(t, e, f.ID, "apply_to_resources", applyBody(serverTarget(s.ID)))
	}
	sixth, _ := createFirewall(t, e, map[string]any{"name": "fw6"})
	wantError(t, fwAction(e, sixth.ID, "apply_to_resources", applyBody(serverTarget(s.ID))),
		http.StatusForbidden, "resource_limit_exceeded", "apply a sixth firewall to a server")

	acts = startFWAction(t, e, fw.ID, "remove_from_resources", removeBody(serverTarget(s.ID)))
	if len(acts) != 1 || acts[0].Command != "remove_firewall" || !acts[0].names(ref{s.ID, "server"}, ref{fw.ID, "firewall"}) {
		t.Fatalf("remove_from_resources actions %+v, want one remove_firewall naming server %d and firewall %d", acts, s.ID, fw.ID)
	}
	waitAll(t, e, acts, "success", "remove firewall")
	if got := mustFirewall(t, e, fw.ID).AppliedTo; len(got) != 0 {
		t.Errorf("after remove: applied_to %+v, want []", got)
	}
	if got := getServer(t, e, s.ID).PublicNet.Firewalls; slices.ContainsFunc(got, func(f serverFirewall) bool { return f.ID == fw.ID }) {
		t.Errorf("after remove: server public_net.firewalls %+v still lists firewall %d", got, fw.ID)
	}
	wantError(t, fwAction(e, fw.ID, "remove_from_resources", removeBody(serverTarget(s.ID))),
		http.StatusNotFound, "firewall_resource_not_found", "remove from a server not applied")

	other := createServer(t, e, "other", nil)
	e.failPush.Store(true)
	acts = startFWAction(t, e, fw.ID, "apply_to_resources", applyBody(serverTarget(other.ID)))
	waitAll(t, e, acts, "error", "apply firewall with a failing agent push")
	e.failPush.Store(false)
}

func testLabelSelector(t *testing.T) {
	e := newFWEnv(t)
	web := createServer(t, e, "web", map[string]any{"labels": map[string]string{"role": "web"}})
	webPrivate := createServer(t, e, "web-private", map[string]any{"labels": map[string]string{"role": "web"}, "public_net": noIPv4["public_net"]})
	plain := createServer(t, e, "plain", nil)

	wantError(t, e.Do(http.MethodPost, "/firewalls", map[string]any{"name": "ghost", "apply_to": []any{serverTarget(unknownID)}}),
		http.StatusUnprocessableEntity, "firewall_resource_not_found", "create applied to an unknown server")
	wantError(t, e.Do(http.MethodPost, "/firewalls", map[string]any{"name": "twice", "apply_to": []any{serverTarget(plain.ID), serverTarget(plain.ID)}}),
		http.StatusUnprocessableEntity, "server_already_added", "create applied to the same server twice")

	fw, acts := createFirewall(t, e, map[string]any{
		"name":     "sel",
		"rules":    []firewalls.Rule{sshRule},
		"apply_to": []any{serverTarget(plain.ID), selectorTarget("role=web")},
	})
	cmds := commands(acts)
	if slices.Index(cmds, "set_firewall_rules") < 0 || !slices.ContainsFunc(acts, func(a actionView) bool {
		return a.Command == "apply_firewall" && a.names(ref{plain.ID, "server"}, ref{fw.ID, "firewall"})
	}) || !slices.ContainsFunc(acts, func(a actionView) bool {
		return a.Command == "apply_firewall" && a.names(ref{fw.ID, "firewall"})
	}) {
		t.Errorf("create actions %+v, want set_firewall_rules, apply_firewall for server %d and apply_firewall for the selector", acts, plain.ID)
	}

	got := mustFirewall(t, e, fw.ID)
	sel, ok := got.selector("role=web")
	if !ok {
		t.Fatalf("applied_to %+v, want an entry for label selector role=web", got.AppliedTo)
	}
	var matched []int64
	for _, r := range sel.AppliedToResources {
		if r.Type == "server" {
			matched = append(matched, r.Server.ID)
		}
	}
	if !slices.Equal(matched, []int64{web.ID}) {
		t.Errorf("selector applied_to_resources servers %v, want only [%d] (not %d without public IPv4)", matched, web.ID, webPrivate.ID)
	}

	wantError(t, fwAction(e, fw.ID, "remove_from_resources", removeBody(serverTarget(web.ID))),
		http.StatusUnprocessableEntity, "firewall_managed_by_label_selector", "remove a server attached by the selector")

	runFWAction(t, e, fw.ID, "remove_from_resources", removeBody(selectorTarget("role=web")))
	got = mustFirewall(t, e, fw.ID)
	if _, ok := got.selector("role=web"); ok {
		t.Errorf("after removing the selector: applied_to %+v still lists it", got.AppliedTo)
	}
	if ids := got.appliedServers(); !slices.Equal(ids, []int64{plain.ID}) {
		t.Errorf("after removing the selector: applied servers %v, want [%d]", ids, plain.ID)
	}
}

func testSetRules(t *testing.T) {
	e := newFWEnv(t)
	fw, _ := createFirewall(t, e, map[string]any{"name": "fw", "rules": []firewalls.Rule{sshRule}})
	a := createServer(t, e, "a", nil)
	b := createServer(t, e, "b", nil)
	runFWAction(t, e, fw.ID, "apply_to_resources", applyBody(serverTarget(a.ID), serverTarget(b.ID)))

	rules := []firewalls.Rule{
		outRule("udp", ptr("53"), "9.9.9.9/32"),
		inRule("tcp", ptr("443"), "0.0.0.0/0"),
		inRule("icmp", nil, "0.0.0.0/0"),
	}
	acts := runFWAction(t, e, fw.ID, "set_rules", map[string]any{"rules": rules})
	var set int
	var applied []int64
	for _, act := range acts {
		switch act.Command {
		case "set_firewall_rules":
			set++
		case "apply_firewall":
			for _, r := range act.Resources {
				if r.Type == "server" {
					applied = append(applied, r.ID)
				}
			}
		}
	}
	slices.Sort(applied)
	if set != 1 || len(acts) != 3 || !slices.Equal(applied, []int64{a.ID, b.ID}) {
		t.Errorf("set_rules actions %+v, want one set_firewall_rules and apply_firewall for servers %d and %d", acts, a.ID, b.ID)
	}
	if got := mustFirewall(t, e, fw.ID).Rules; !reflect.DeepEqual(got, rules) {
		t.Errorf("after set_rules: rules %+v, want %+v", got, rules)
	}

	wantFieldError(t, fwAction(e, fw.ID, "set_rules", map[string]any{"rules": []firewalls.Rule{inRule("tcp", ptr("0"), "0.0.0.0/0")}}),
		"rules[0].port", "set_rules with port 0")
	if got := mustFirewall(t, e, fw.ID).Rules; !reflect.DeepEqual(got, rules) {
		t.Errorf("after refused set_rules: rules %+v, want %+v kept", got, rules)
	}
}

func testDeleteInUse(t *testing.T) {
	e := newFWEnv(t)
	s := createServer(t, e, "s", nil)
	direct, _ := createFirewall(t, e, map[string]any{"name": "direct"})
	runFWAction(t, e, direct.ID, "apply_to_resources", applyBody(serverTarget(s.ID)))
	bySelector, _ := createFirewall(t, e, map[string]any{"name": "by-selector"})
	runFWAction(t, e, bySelector.ID, "apply_to_resources", applyBody(selectorTarget("role=db")))

	for _, f := range []fwView{direct, bySelector} {
		b := wantError(t, e.Do(http.MethodDelete, fmt.Sprintf("/firewalls/%d", f.ID), nil),
			http.StatusUnprocessableEntity, "resource_in_use", "DELETE applied firewall "+f.Name)
		if !strings.Contains(b.Error.Message, "is still in use") {
			t.Errorf("DELETE applied firewall %s: message %q, want it to contain \"is still in use\"", f.Name, b.Error.Message)
		}
	}

	runFWAction(t, e, direct.ID, "remove_from_resources", removeBody(serverTarget(s.ID)))
	runFWAction(t, e, bySelector.ID, "remove_from_resources", removeBody(selectorTarget("role=db")))
	for _, f := range []fwView{direct, bySelector} {
		resp := e.Do(http.MethodDelete, fmt.Sprintf("/firewalls/%d", f.ID), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("DELETE firewall %s after removal: status %d (%s), want 204", f.Name, resp.StatusCode, apitest.ErrorCode(t, resp))
		} else {
			_ = resp.Body.Close()
		}
	}

	kept, _ := createFirewall(t, e, map[string]any{"name": "kept"})
	gone := createServer(t, e, "gone", nil)
	runFWAction(t, e, kept.ID, "apply_to_resources", applyBody(serverTarget(gone.ID)))
	deleteServer(t, e, gone.ID)
	if got := mustFirewall(t, e, kept.ID).AppliedTo; len(got) != 0 {
		t.Errorf("after deleting the server: applied_to %+v, want []", got)
	}
}

func testServerCreate(t *testing.T) {
	e := newFWEnv(t)
	fw, _ := createFirewall(t, e, map[string]any{"name": "fw", "rules": []firewalls.Rule{sshRule}})

	resp := e.Do(http.MethodPost, "/servers", map[string]any{
		"name": "guarded", "server_type": "cx23", "image": "ubuntu-24.04", "location": "fsn1",
		"firewalls": []any{map[string]any{"firewall": fw.ID}},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers with firewalls: status %d (%s), want 201", resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Server      serverView   `json:"server"`
		Action      actionView   `json:"action"`
		NextActions []actionView `json:"next_actions"`
	}
	apitest.Decode(t, resp, &b)
	if !slices.Contains(b.Server.PublicNet.Firewalls, serverFirewall{fw.ID, "applied"}) {
		t.Errorf("created server public_net.firewalls %+v, want {%d applied}", b.Server.PublicNet.Firewalls, fw.ID)
	}
	if !slices.ContainsFunc(b.NextActions, func(a actionView) bool { return a.Command == "apply_firewall" }) {
		t.Errorf("create next_actions %v, want an apply_firewall", commands(b.NextActions))
	}
	waitAll(t, e, append([]actionView{b.Action}, b.NextActions...), "success", "create server with firewall")
	if got := mustFirewall(t, e, fw.ID).appliedServers(); !slices.Contains(got, b.Server.ID) {
		t.Errorf("firewall applied_to servers %v, want %d", got, b.Server.ID)
	}

	wantError(t, e.Do(http.MethodPost, "/servers", map[string]any{
		"name": "ghost", "server_type": "cx23", "image": "ubuntu-24.04", "location": "fsn1",
		"firewalls": []any{map[string]any{"firewall": unknownID}},
	}), http.StatusNotFound, "not_found", "POST /servers with an unknown firewall")
	resp = e.Do(http.MethodGet, "/servers?name=ghost", nil)
	var list struct {
		Servers []serverView `json:"servers"`
	}
	apitest.Decode(t, resp, &list)
	if len(list.Servers) != 0 {
		t.Errorf("refused create stored servers %+v", list.Servers)
	}
}
