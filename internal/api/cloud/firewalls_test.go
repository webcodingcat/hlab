package cloud_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/platform"
)

// firewallJSON is a firewall as the tests read it.
type firewallJSON struct {
	ID        int64                 `json:"id"`
	Name      string                `json:"name"`
	Labels    map[string]string     `json:"labels"`
	Rules     []schema.FirewallRule `json:"rules"`
	AppliedTo []struct {
		Type          string `json:"type"`
		Server        *struct{ ID int64 }
		LabelSelector *struct{ Selector string } `json:"label_selector"`
		Resources     *[]struct {
			Type   string
			Server struct{ ID int64 }
		} `json:"applied_to_resources"`
	} `json:"applied_to"`
}

// firewallCreated is the answer of POST /firewalls.
type firewallCreated struct {
	Firewall firewallJSON    `json:"firewall"`
	Actions  []schema.Action `json:"actions"`
}

// anyRule is an inbound tcp rule from everywhere on a port.
func anyRule(port string) map[string]any {
	return map[string]any{"direction": "in", "protocol": "tcp", "port": port, "source_ips": []string{"0.0.0.0/0", "::/0"}}
}

// createFirewall creates a firewall and fails the test if it cannot.
func createFirewall(t *testing.T, test *testLab, body map[string]any) firewallCreated {
	t.Helper()
	response := test.client.Do(http.MethodPost, "/v1/firewalls", body)
	if response.Status != http.StatusCreated {
		t.Fatalf("POST /v1/firewalls answered %d %s", response.Status, response.Body)
	}
	var created firewallCreated
	response.Decode(t, &created)
	return created
}

// getFirewall reads a firewall.
func getFirewall(t *testing.T, test *testLab, id int64) firewallJSON {
	t.Helper()
	var answer struct{ Firewall firewallJSON }
	test.client.Get(fmt.Sprintf("/v1/firewalls/%d", id)).Decode(t, &answer)
	return answer.Firewall
}

// firewallAction runs a firewall action and returns the answer.
func firewallAction(test *testLab, id int64, command string, body any) *responseOf {
	return &responseOf{test.client.Do(http.MethodPost, fmt.Sprintf("/v1/firewalls/%d/actions/%s", id, command), body)}
}

// firewallActions runs a firewall action that must succeed and returns its
// actions.
func firewallActions(t *testing.T, test *testLab, id int64, command string, body any) []schema.Action {
	t.Helper()
	response := firewallAction(test, id, command, body)
	if response.Status != http.StatusCreated {
		t.Fatalf("%s answered %d %s", command, response.Status, response.Body)
	}
	var answer struct{ Actions []schema.Action }
	response.Decode(t, &answer)
	return answer.Actions
}

// serverTarget and selectorTarget are entries of apply_to and remove_from.
func serverTarget(id int64) map[string]any {
	return map[string]any{"type": "server", "server": map[string]any{"id": id}}
}

func selectorTarget(selector string) map[string]any {
	return map[string]any{"type": "label_selector", "label_selector": map[string]any{"selector": selector}}
}

// commands returns the actions' commands with the types of the resources
// they name, such as "apply_firewall firewall,server".
func commands(actions []schema.Action) []string {
	var out []string
	for _, action := range actions {
		var types []string
		for _, resource := range action.Resources {
			types = append(types, resource.Type)
		}
		out = append(out, action.Command+" "+strings.Join(types, ","))
	}
	return out
}

// newOffServer creates a server that stays off, with labels, and waits for
// its create.
func newOffServer(t *testing.T, test *testLab, name string, labels map[string]string) createAnswer {
	t.Helper()
	created := createServer(t, test, map[string]any{"name": name, "server_type": "cpx22", "image": "debian-13",
		"start_after_create": false, "labels": labels})
	waitActions(t, test, created.Action)
	return created
}

// The errors of a create, as Hetzner answers them (measured 2026-11-08):
// first the fields with their index, then each rule's protocol, addresses
// and port.
func TestFirewallCreateErrors(t *testing.T) {
	test := newTestLab(t)
	rule := func(change map[string]any) map[string]any {
		r := anyRule("80")
		for key, value := range change {
			r[key] = value
		}
		return map[string]any{"name": "x", "rules": []any{r}}
	}
	many := func(n int) []string {
		var cidrs []string
		for i := range n {
			cidrs = append(cidrs, fmt.Sprintf("10.0.%d.0/24", i))
		}
		return cidrs
	}
	for name, want := range map[string]struct {
		body          any
		status        int
		code, message string
		field, text   string
	}{
		"no name":             {map[string]any{}, 422, "invalid_input", "invalid input in field 'name'", "name", "value required to be larger"},
		"an empty name":       {map[string]any{"name": ""}, 422, "invalid_input", "invalid input in field 'name'", "name", "value required to be larger"},
		"a long name":         {map[string]any{"name": strings.Repeat("x", 129)}, 422, "invalid_input", "invalid input in field 'name'", "name", "value required to be smaller"},
		"a bad label":         {map[string]any{"name": "x", "labels": map[string]string{"bad key!": "x"}}, 422, "invalid_input", "invalid input in field 'labels'", "labels", "invalid label value: key contains invalid characters or is malformed"},
		"a bad direction":     {rule(map[string]any{"direction": "both"}), 422, "invalid_input", "invalid input in field 'rules[0].direction'", "rules[0].direction", "'rules[0].direction' value not allowed"},
		"a long description":  {rule(map[string]any{"description": strings.Repeat("d", 256)}), 422, "invalid_input", "invalid input in field 'rules[0].description'", "rules[0].description", "value required to be smaller"},
		"101 addresses":       {rule(map[string]any{"source_ips": many(101)}), 422, "invalid_input", "invalid input in field 'rules[0].source_ips'", "rules[0].source_ips", "value required to be smaller"},
		"a bad protocol":      {rule(map[string]any{"protocol": "sctp"}), 422, "invalid_input", "invalid input in field 'rules[].protocol_l4'", "rules[].protocol_l4", "field contains invalid value"},
		"no sources":          {rule(map[string]any{"source_ips": []string{}}), 422, "invalid_input", "invalid input in field 'rules'", "rules[0].source_ips", "source_ips needs to have at least one entry"},
		"no destinations":     {rule(map[string]any{"direction": "out", "source_ips": nil}), 422, "invalid_input", "invalid input in field 'rules'", "rules[0].destination_ips", "destination_ips needs to have at least one entry"},
		"port 0":              {rule(map[string]any{"port": "0"}), 422, "invalid_input", "invalid input in fields 'rules[].port', 'rules[].port'", "rules[].port", "value is out of range"},
		"port 65536":          {rule(map[string]any{"port": "65536"}), 422, "invalid_input", "invalid input in field 'port'", "port", "invalid port"},
		"a port of letters":   {rule(map[string]any{"port": "abc"}), 422, "invalid_input", "invalid input in field 'port'", "port", "invalid port"},
		"a port with a space": {rule(map[string]any{"port": " 80"}), 422, "invalid_input", "invalid input in field 'port'", "port", "invalid port"},
		"a reversed range":    {rule(map[string]any{"port": "90-80"}), 422, "invalid_input", "invalid input in field 'rules[].port'", "rules[].port", "field value is too small"},
		"no CIDR":             {rule(map[string]any{"source_ips": []string{"10.0.0.1"}}), 422, "invalid_input", "invalid input in field 'rules[].source_ips'", "rules[].source_ips", "IP not in cidr notation: 10.0.0.1"},
		"a bad address":       {rule(map[string]any{"source_ips": []string{"10.0.0.300/32"}}), 422, "invalid_input", "invalid input in field 'rules[].source_ips'", "rules[].source_ips", "IP not in cidr notation: 10.0.0.300/32"},
		"host bits":           {rule(map[string]any{"source_ips": []string{"10.0.0.1/24"}}), 422, "invalid_input", "invalid input in field 'rules[].source_ips'", "rules[].source_ips", "IP has host bit(s) set, did you mean 10.0.0.0/24?"},
		"a bad apply type":    {map[string]any{"name": "x", "apply_to": []any{map[string]any{"type": "volume"}}}, 422, "invalid_input", "invalid input in field 'apply_to[0].type'", "apply_to[0].type", "'apply_to[0].type' value not allowed"},
		"a selector missing":  {map[string]any{"name": "x", "apply_to": []any{map[string]any{"type": "label_selector"}}}, 422, "invalid_input", "invalid input in field 'apply_to[0].label_selector'", "apply_to[0].label_selector", "parameter label_selector needs to be specified if type is label_selector"},
		"a bad selector":      {map[string]any{"name": "x", "apply_to": []any{selectorTarget("a=b=c,,")}}, 422, "invalid_input", "invalid input in field 'apply_to[].label_selector'", "apply_to[].label_selector", "invalid label_selector: invalid syntax"},
		"an unknown server":   {map[string]any{"name": "x", "apply_to": []any{serverTarget(999999999)}}, 404, "firewall_resource_not_found", "server with ID 999999999 not found", "", ""},
		"a rule of a string":  {map[string]any{"name": "x", "rules": []any{"x"}}, 400, "json_error", "A valid JSON document is required.", "", ""},
	} {
		response := &responseOf{test.client.Do(http.MethodPost, "/v1/firewalls", want.body)}
		expectError(t, name, response, want.status, want.code, want.message)
		if want.field == "" {
			continue
		}
		if got := fieldMessages(t, errorOf(t, response.Response))[want.field]; len(got) == 0 || got[0] != want.text {
			t.Errorf("%s: the field %s has %q, want %q", name, want.field, got, want.text)
		}
	}
	var list struct{ Firewalls []firewallJSON }
	test.client.Get("/v1/firewalls").Decode(t, &list)
	if len(list.Firewalls) != 0 {
		t.Errorf("failed creates left %+v", list.Firewalls)
	}
}

// Hetzner keeps rules in a form of its own (measured 2026-11-08): tcp
// and udp without a port or with "any" take "any", "80-80" is "80", the
// other protocols drop a port, a rule keeps only the addresses of its
// direction, IPv6 in its canonical form; rules of null are none.
func TestFirewallRulesAreKeptAsHetznerKeepsThem(t *testing.T) {
	test := newTestLab(t)
	created := createFirewall(t, test, map[string]any{"name": "kept", "rules": []any{
		map[string]any{"direction": "in", "protocol": "tcp", "source_ips": []string{"0.0.0.0/0"}},
		map[string]any{"direction": "in", "protocol": "udp", "port": "", "source_ips": []string{"0.0.0.0/0"}},
		map[string]any{"direction": "in", "protocol": "tcp", "port": "80-80", "source_ips": []string{"2001:0DB8:0000::0/32", "2001:db8:1::1/128"}},
		map[string]any{"direction": "in", "protocol": "icmp", "port": "80", "source_ips": []string{"0.0.0.0/0"}, "destination_ips": []string{"10.0.0.0/8"}},
		map[string]any{"direction": "out", "protocol": "esp", "source_ips": []string{"10.0.0.0/8"}, "destination_ips": []string{"::/0"}, "description": "vpn"},
	}})
	rules := created.Firewall.Rules
	port := func(i int) string {
		if rules[i].Port == nil {
			return "null"
		}
		return *rules[i].Port
	}
	if len(rules) != 5 || port(0) != "any" || port(1) != "any" || port(2) != "80" || port(3) != "null" || port(4) != "null" {
		t.Fatalf("rules %+v", rules)
	}
	if !slices.Equal(rules[2].SourceIPs, []string{"2001:db8::/32", "2001:db8:1::1/128"}) {
		t.Errorf("IPv6 kept as %v", rules[2].SourceIPs)
	}
	if len(rules[3].DestinationIPs) != 0 || len(rules[4].SourceIPs) != 0 || rules[4].Description == nil || *rules[4].Description != "vpn" {
		t.Errorf("addresses of the other direction kept: %+v %+v", rules[3], rules[4])
	}
	if got := createFirewall(t, test, map[string]any{"name": "none", "rules": nil}).Firewall.Rules; len(got) != 0 {
		t.Errorf("rules of null kept as %+v", got)
	}
	// Hetzner has no limit of 50 rules: only the effective rules count
	var rows []any
	for i := range 51 {
		rows = append(rows, anyRule(fmt.Sprint(1000+i)))
	}
	if got := createFirewall(t, test, map[string]any{"name": "many", "rules": rows}).Firewall.Rules; len(got) != 51 {
		t.Errorf("51 rules kept as %d", len(got))
	}
}

// A firewall's record, list, update and delete, as Hetzner answers
// (measured 2026-11-08).
func TestFirewallLifeCycle(t *testing.T) {
	test := newTestLab(t)
	created := createFirewall(t, test, map[string]any{"name": "web", "labels": map[string]string{"env": "dev"}, "rules": []any{anyRule("22")}})
	if got := commands(created.Actions); !slices.Equal(got, []string{"set_firewall_rules firewall"}) || created.Actions[0].Status != "success" {
		t.Errorf("a create without resources has the actions %v", created.Actions)
	}
	second := createFirewall(t, test, map[string]any{"name": "db"}).Firewall
	expectError(t, "a taken name", &responseOf{test.client.Do(http.MethodPost, "/v1/firewalls", map[string]any{"name": "web"})},
		http.StatusConflict, "uniqueness_error", "name is already used")
	expectError(t, "a missing firewall", &responseOf{test.client.Get("/v1/firewalls/999999999")},
		http.StatusNotFound, "not_found", "firewall with ID '999999999' not found")
	expectError(t, "an action of a missing firewall", firewallAction(test, 999999999, "set_rules", map[string]any{"rules": []any{}}),
		http.StatusNotFound, "not_found", "firewall with ID 999999999 not found")

	listed := func(query string) []firewallJSON {
		var list struct{ Firewalls []firewallJSON }
		test.client.Get("/v1/firewalls?"+query).Decode(t, &list)
		return list.Firewalls
	}
	if got := listed("label_selector=env%3Ddev"); len(got) != 1 || got[0].ID != created.Firewall.ID {
		t.Errorf("selected by env=dev %+v", got)
	}
	if got := listed("name=db"); len(got) != 1 || got[0].ID != second.ID {
		t.Errorf("by name %+v", got)
	}
	for sort, want := range map[string]int64{"id:desc": second.ID, "name": second.ID, "name:desc": created.Firewall.ID, "created": created.Firewall.ID, "bogus": created.Firewall.ID} {
		if got := listed("sort=" + sort); len(got) != 2 || got[0].ID != want {
			t.Errorf("sorted by %s: %+v, want %d first", sort, got, want)
		}
	}

	path := fmt.Sprintf("/v1/firewalls/%d", second.ID)
	var updated struct{ Firewall firewallJSON }
	test.client.Do(http.MethodPut, path, map[string]any{"name": "cache", "labels": map[string]string{"x": "y"}, "rules": []any{anyRule("80")}}).Decode(t, &updated)
	if updated.Firewall.Name != "cache" || updated.Firewall.Labels["x"] != "y" || len(updated.Firewall.Rules) != 0 {
		t.Errorf("updated %+v; a PUT changes no rules", updated.Firewall)
	}
	expectError(t, "an empty name", &responseOf{test.client.Do(http.MethodPut, path, map[string]any{"name": ""})},
		http.StatusUnprocessableEntity, "invalid_input", "invalid input in field 'name'")
	expectError(t, "a taken name", &responseOf{test.client.Do(http.MethodPut, path, map[string]any{"name": "web"})},
		http.StatusConflict, "uniqueness_error", "name is already used")

	actions := firewallActions(t, test, second.ID, "set_rules", map[string]any{"rules": []any{anyRule("443")}})
	if got := commands(actions); !slices.Equal(got, []string{"set_firewall_rules firewall"}) || actions[0].Status != "success" {
		t.Errorf("set_rules without servers has the actions %v", actions)
	}
	if got := getFirewall(t, test, second.ID).Rules; len(got) != 1 || *got[0].Port != "443" {
		t.Errorf("after set_rules the rules are %+v", got)
	}
	expectError(t, "set_rules with port 0", firewallAction(test, second.ID, "set_rules", map[string]any{"rules": []any{anyRule("0")}}),
		http.StatusUnprocessableEntity, "invalid_input", "invalid input in fields 'rules[].port', 'rules[].port'")
	firewallActions(t, test, second.ID, "set_rules", map[string]any{})
	if got := getFirewall(t, test, second.ID).Rules; len(got) != 0 {
		t.Errorf("set_rules without rules left %+v", got)
	}
	if status := test.client.Do(http.MethodDelete, path, nil).Status; status != http.StatusNoContent {
		t.Errorf("the delete answered %d", status)
	}

	var list schema.ActionListResponse
	test.client.Get("/v1/firewalls/actions").Decode(t, &list)
	if len(list.Actions) != 4 || list.Actions[0].Resources[0].Type != "firewall" {
		t.Errorf("the firewalls' actions are %v", commands(list.Actions))
	}
}

// Hetzner's limits (measured 2026-11-08): 500 effective rules in a
// firewall, each address of a direction counted once, both directions
// together; 5 firewalls on a server, also through a label selector, where
// the sixth is not applied.
func TestFirewallLimits(t *testing.T) {
	test := newTestLab(t)
	withAddresses := func(direction string, rules, addresses, offset int) []any {
		var out []any
		for i := range rules {
			key := "source_ips"
			if direction == "out" {
				key = "destination_ips"
			}
			var cidrs []string
			for j := range addresses {
				cidrs = append(cidrs, fmt.Sprintf("10.%d.%d.0/24", i+offset, j))
			}
			out = append(out, map[string]any{"direction": direction, "protocol": "tcp", "port": fmt.Sprint(1000 + i), key: cidrs})
		}
		return out
	}
	createFirewall(t, test, map[string]any{"name": "500 in", "rules": withAddresses("in", 50, 10, 0)})
	createFirewall(t, test, map[string]any{"name": "60 rules, 10 addresses", "rules": append(withAddresses("in", 1, 10, 0), withAddresses("in", 1, 10, 0)...)})
	for name, rules := range map[string][]any{
		"501 in":          append(withAddresses("in", 50, 10, 0), withAddresses("in", 1, 1, 99)...),
		"300 in, 300 out": append(withAddresses("in", 30, 10, 0), withAddresses("out", 30, 10, 0)...),
	} {
		response := &responseOf{test.client.Do(http.MethodPost, "/v1/firewalls", map[string]any{"name": "x", "rules": rules})}
		expectError(t, name, response, http.StatusForbidden, "resource_limit_exceeded", "firewall limit exceeded")
		if !strings.Contains(string(response.Body), `"name":"firewall_effective_rules"`) {
			t.Errorf("%s: the details are %s", name, response.Body)
		}
	}

	server := newOffServer(t, test, "web", map[string]string{"role": "web"}).Server.ID
	var ids []int64
	for i := range 5 {
		created := createFirewall(t, test, map[string]any{"name": fmt.Sprint("fw", i), "apply_to": []any{serverTarget(server)}})
		waitActions(t, test, created.Actions...)
		ids = append(ids, created.Firewall.ID)
	}
	sixth := createFirewall(t, test, map[string]any{"name": "sixth"}).Firewall.ID
	response := firewallAction(test, sixth, "apply_to_resources", map[string]any{"apply_to": []any{serverTarget(server)}})
	expectError(t, "a sixth by apply", response, http.StatusForbidden, "resource_limit_exceeded", "firewall limit exceeded")
	if !strings.Contains(string(response.Body), `"name":"firewalls_per_server"`) {
		t.Errorf("the sixth's details are %s", response.Body)
	}
	expectError(t, "a sixth by create", &responseOf{test.client.Do(http.MethodPost, "/v1/firewalls", map[string]any{"name": "x", "apply_to": []any{serverTarget(server)}})},
		http.StatusForbidden, "resource_limit_exceeded", "firewall limit exceeded")
	waitActions(t, test, firewallActions(t, test, sixth, "apply_to_resources", map[string]any{"apply_to": []any{selectorTarget("role=web")}})...)
	if got := getServer(t, test, server).PublicNet.Firewalls; len(got) != 5 || slices.ContainsFunc(got, func(f schema.ServerFirewall) bool { return f.ID == sixth }) {
		t.Errorf("the server has the firewalls %+v, want the first five", got)
	}
	// once one goes, the selector's firewall takes its place
	waitActions(t, test, firewallActions(t, test, ids[0], "remove_from_resources", map[string]any{"remove_from": []any{serverTarget(server)}})...)
	if got := getFirewall(t, test, sixth).AppliedTo[0].Resources; got == nil || len(*got) != 1 || (*got)[0].Server.ID != server {
		t.Errorf("after a remove the selector holds %+v", got)
	}
}

// Hetzner allows 50 firewalls per account (its documentation); the lab
// counts them per project, so another project starts afresh. The error is
// not measured.
func TestFirewallsPerProjectAreLimited(t *testing.T) {
	test := newTestLab(t)
	for i := range 50 {
		createFirewall(t, test, map[string]any{"name": fmt.Sprint("fw", i)})
	}
	response := &responseOf{test.client.Do(http.MethodPost, "/v1/firewalls", map[string]any{"name": "fw50"})}
	expectError(t, "the 51st", response, http.StatusForbidden, "resource_limit_exceeded", "firewall limit exceeded")
	if !strings.Contains(string(response.Body), `"limits":[{"name":"firewalls"}]`) {
		t.Errorf("the 51st's details are %s", response.Body)
	}
	test.client.Token = newToken(t, test.lab, "other")
	createFirewall(t, test, map[string]any{"name": "fw50"})
}

// A firewall applied to a server directly, as Hetzner answers (measured
// 2026-11-08): apply_firewall names the firewall and the server, the
// server shows the firewall pending until it is applied, set_rules is
// followed by an apply_firewall, a firewall on a server cannot be deleted.
func TestFirewallAppliedToAServer(t *testing.T) {
	test := newTestLab(t)
	agent := startMachine(t, test, platform.HostResources{Threads: 8, MemoryMB: 16 * 1024})
	server := newOffServer(t, test, "web", nil).Server.ID
	firewall := createFirewall(t, test, map[string]any{"name": "web", "rules": []any{anyRule("22")}}).Firewall.ID

	waiting, release := agent.HoldNext(agentproto.PictureRuleset)
	actions := firewallActions(t, test, firewall, "apply_to_resources", map[string]any{"apply_to": []any{serverTarget(server)}})
	<-waiting
	if got := commands(actions); !slices.Equal(got, []string{"apply_firewall firewall,server"}) || actions[0].Status != "running" {
		t.Errorf("the apply is %v %s", got, actions[0].Status)
	}
	if got := getServer(t, test, server).PublicNet.Firewalls; len(got) != 1 || got[0].ID != firewall || got[0].Status != "pending" {
		t.Errorf("while the apply runs the server has %+v", got)
	}
	release()
	waitActions(t, test, actions...)
	if got := getServer(t, test, server).PublicNet.Firewalls; len(got) != 1 || got[0].Status != "applied" {
		t.Errorf("after the apply the server has %+v", got)
	}
	var ruleset agentproto.Ruleset
	if err := json.Unmarshal(agent.Picture(agentproto.PictureRuleset), &ruleset); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("jump firewall_%d_in", firewall); !strings.Contains(ruleset.Text, want) {
		t.Errorf("the ruleset lacks %q:\n%s", want, ruleset.Text)
	}

	expectError(t, "applied twice", firewallAction(test, firewall, "apply_to_resources", map[string]any{"apply_to": []any{serverTarget(server)}}),
		http.StatusUnprocessableEntity, "firewall_already_applied", "firewall has already been applied to resource")
	expectError(t, "an unknown server", firewallAction(test, firewall, "apply_to_resources", map[string]any{"apply_to": []any{serverTarget(999999999)}}),
		http.StatusNotFound, "firewall_resource_not_found", "server with ID 999999999 not found")
	expectError(t, "no apply_to", firewallAction(test, firewall, "apply_to_resources", map[string]any{}),
		http.StatusUnprocessableEntity, "invalid_input", "invalid input in field 'apply_to'")
	if got := firewallActions(t, test, firewall, "apply_to_resources", map[string]any{"apply_to": []any{}}); len(got) != 0 {
		t.Errorf("an empty apply_to has the actions %v", got)
	}
	expectError(t, "a delete", &responseOf{test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/firewalls/%d", firewall), nil)},
		http.StatusUnprocessableEntity, "resource_in_use", fmt.Sprintf("firewall with ID %d is still in use", firewall))

	actions = firewallActions(t, test, firewall, "set_rules", map[string]any{"rules": []any{anyRule("80")}})
	if got := commands(actions); !slices.Equal(got, []string{"set_firewall_rules firewall", "apply_firewall firewall,server"}) {
		t.Errorf("set_rules with a server has the actions %v", got)
	}
	waitActions(t, test, actions...)

	expectError(t, "removing a missing server", firewallAction(test, firewall, "remove_from_resources", map[string]any{"remove_from": []any{serverTarget(999999999)}}),
		http.StatusNotFound, "firewall_resource_not_found", "resource not found")
	expectError(t, "removing a selector that is not there", firewallAction(test, firewall, "remove_from_resources", map[string]any{"remove_from": []any{selectorTarget("not=applied")}}),
		http.StatusNotFound, "firewall_resource_not_found", fmt.Sprintf("firewall with ID %d cannot be removed from label_selector: resource not found", firewall))
	expectError(t, "no remove_from", firewallAction(test, firewall, "remove_from_resources", map[string]any{}),
		http.StatusUnprocessableEntity, "invalid_input", "invalid input in field 'remove_from'")
	actions = firewallActions(t, test, firewall, "remove_from_resources", map[string]any{"remove_from": []any{serverTarget(server)}})
	if got := commands(actions); !slices.Equal(got, []string{"remove_firewall firewall,server"}) {
		t.Errorf("the remove is %v", got)
	}
	waitActions(t, test, actions...)
	if got := getServer(t, test, server).PublicNet.Firewalls; len(got) != 0 {
		t.Errorf("after the remove the server has %+v", got)
	}
	if status := test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/firewalls/%d", firewall), nil).Status; status != http.StatusNoContent {
		t.Errorf("the delete after the remove answered %d", status)
	}
}

// A label selector follows the servers, as on Hetzner (measured
// 2026-11-08): a new server with matching labels and one relabelled to
// match come under the firewall, each with an apply_firewall that names
// it; one relabelled away leaves it; a server applied directly moves out
// of the selector; a server's delete takes it out; a firewall that applies
// only through selectors can be deleted.
func TestFirewallLabelSelectorFollowsTheServers(t *testing.T) {
	test := newTestLab(t)
	created := createFirewall(t, test, map[string]any{"name": "web", "rules": []any{anyRule("22")}, "apply_to": []any{selectorTarget("role=web")}})
	firewall := created.Firewall.ID
	if got := commands(created.Actions); !slices.Equal(got, []string{"set_firewall_rules firewall", "apply_firewall firewall,label_selector"}) {
		t.Errorf("a create with a selector has the actions %v", got)
	}
	waitActions(t, test, created.Actions...)
	selected := func() []int64 {
		var ids []int64
		for _, target := range getFirewall(t, test, firewall).AppliedTo {
			if target.Type == "label_selector" {
				for _, resource := range *target.Resources {
					ids = append(ids, resource.Server.ID)
				}
			}
		}
		return ids
	}
	if got := selected(); len(got) != 0 {
		t.Errorf("a selector without servers holds %v", got)
	}

	web := newOffServer(t, test, "web", map[string]string{"role": "web"}).Server.ID
	other := newOffServer(t, test, "other", nil).Server.ID
	if got := selected(); !slices.Equal(got, []int64{web}) {
		t.Errorf("after a matching create the selector holds %v", got)
	}
	if got := getServer(t, test, web).PublicNet.Firewalls; len(got) != 1 || got[0].ID != firewall {
		t.Errorf("the new server has the firewalls %+v", got)
	}
	test.client.Do(http.MethodPut, fmt.Sprintf("/v1/servers/%d", other), map[string]any{"labels": map[string]string{"role": "web"}})
	if got := selected(); !slices.Equal(got, []int64{web, other}) {
		t.Errorf("after a relabel the selector holds %v", got)
	}
	var list schema.ActionListResponse
	test.client.Get(fmt.Sprintf("/v1/firewalls/%d/actions?sort=id", firewall)).Decode(t, &list)
	if got := commands(list.Actions); len(got) != 4 || got[2] != "apply_firewall firewall,server" || got[3] != "apply_firewall firewall,server" {
		t.Errorf("the firewall's actions are %v", got)
	}
	test.client.Do(http.MethodPut, fmt.Sprintf("/v1/servers/%d", other), map[string]any{"labels": map[string]string{}})
	if got := selected(); !slices.Equal(got, []int64{web}) {
		t.Errorf("after a relabel away the selector holds %v", got)
	}

	actions := firewallActions(t, test, firewall, "apply_to_resources", map[string]any{"apply_to": []any{serverTarget(web)}})
	if got := commands(actions); !slices.Equal(got, []string{"remove_firewall firewall,server,label_selector", "apply_firewall firewall,server"}) ||
		actions[0].Status != "success" || actions[1].Status != "success" {
		t.Errorf("applying a selected server directly has the actions %v", actions)
	}
	if got := selected(); len(got) != 0 {
		t.Errorf("the selector still holds %v", got)
	}
	// a server the firewall does not reach (measured 2026-11-09)
	expectError(t, "removing a server the firewall does not reach", firewallAction(test, firewall, "remove_from_resources", map[string]any{"remove_from": []any{serverTarget(other)}}),
		http.StatusNotFound, "firewall_resource_not_found", "resource not found")

	second := createFirewall(t, test, map[string]any{"name": "second", "apply_to": []any{selectorTarget("role=web")}}).Firewall.ID
	expectError(t, "removing a server held by a selector", firewallAction(test, second, "remove_from_resources", map[string]any{"remove_from": []any{serverTarget(web)}}),
		http.StatusUnprocessableEntity, "firewall_managed_by_label_selector", "firewall has been applied via label selector.")
	waitActions(t, test, serverDelete(t, test, web))
	if got := getFirewall(t, test, firewall).AppliedTo; len(got) != 1 || got[0].Type != "label_selector" {
		t.Errorf("after the server's delete the firewall applies to %+v", got)
	}
	if status := test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/firewalls/%d", second), nil).Status; status != http.StatusNoContent {
		t.Errorf("deleting a firewall on a selector answered %d", status)
	}
}

// serverDelete deletes a server and returns the action.
func serverDelete(t *testing.T, test *testLab, id int64) schema.Action {
	t.Helper()
	var answer schema.ActionGetResponse
	test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/servers/%d", id), nil).Decode(t, &answer)
	return answer.Action
}

// A label selector that takes a server, or lets it go, gives the server the
// firewall, or takes it away, at once, with an apply_firewall or
// remove_firewall that names both and ends at once (the action's order
// and resources measured 2026-11-09).
func TestFirewallSelectorRecordsItsActions(t *testing.T) {
	test := newTestLab(t)
	firewall := createFirewall(t, test, map[string]any{"name": "web", "apply_to": []any{selectorTarget("role=web")}}).Firewall.ID
	server := offServer(t, test, "web").Server.ID
	relabel := func(labels map[string]string) {
		test.client.Do(http.MethodPut, fmt.Sprintf("/v1/servers/%d", server), map[string]any{"labels": labels})
	}
	shown := func() string {
		for _, entry := range getServer(t, test, server).PublicNet.Firewalls {
			if entry.ID == firewall {
				return entry.Status
			}
		}
		return ""
	}
	for _, phase := range []struct {
		labels  map[string]string
		want    string // "" for not listed
		command string
	}{
		{map[string]string{"role": "web"}, "applied", "apply_firewall"},
		{map[string]string{}, "", "remove_firewall"},
	} {
		relabel(phase.labels)
		if got := shown(); got != phase.want {
			t.Errorf("%s: the server shows the firewall %q, want %q", phase.command, got, phase.want)
		}
		if got := firewallActionsRunning(t, test, firewall); len(got) != 0 {
			t.Errorf("%s: the actions %v still run", phase.command, commands(got))
		}
		if got := commands(firewallActionList(t, test, firewall)); !slices.Contains(got, phase.command+" firewall,server") {
			t.Errorf("the firewall's actions are %v, want a %s", got, phase.command)
		}
	}
}

// firewallActionsRunning returns a firewall's running actions.
func firewallActionsRunning(t *testing.T, test *testLab, id int64) []schema.Action {
	t.Helper()
	var answer schema.ActionListResponse
	test.client.Get(fmt.Sprintf("/v1/firewalls/%d/actions?status=running", id)).Decode(t, &answer)
	return answer.Actions
}
