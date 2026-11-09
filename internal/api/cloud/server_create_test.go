package cloud_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/platform"
)

// networkWithSubnet creates a network of a /16 with one /24 subnet.
func networkWithSubnet(t *testing.T, test *testLab, name, base string) int64 {
	t.Helper()
	return createNetwork(t, test, map[string]any{"name": name, "ip_range": base + ".0.0/16",
		"subnets": []map[string]any{cloudSubnet(base + ".1.0/24")}}).ID
}

// A create with networks and firewalls, as Hetzner answers it (measured
// 2026-11-09): next_actions has an attach_to_network per network, an
// apply_firewall per firewall, then the start; the answer shows the
// server in its networks and its firewalls pending; a label selector's
// firewall comes after the create; the server's first pictures have them.
func TestServerCreateJoinsNetworksAndFirewalls(t *testing.T) {
	test := newTestLab(t)
	agent := startMachine(t, test, platform.HostResources{Threads: 8, MemoryMB: 16 * 1024})
	first, second := networkWithSubnet(t, test, "first", "10.1"), networkWithSubnet(t, test, "second", "10.2")
	web := createFirewall(t, test, map[string]any{"name": "web", "rules": []any{anyRule("22")}}).Firewall.ID
	ssh := createFirewall(t, test, map[string]any{"name": "ssh", "rules": []any{anyRule("80")}}).Firewall.ID
	selected := createFirewall(t, test, map[string]any{"name": "selected", "rules": []any{anyRule("443")},
		"apply_to": []any{selectorTarget("role=web")}}).Firewall.ID

	created := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13",
		"labels": map[string]string{"role": "web"}, "networks": []int64{first, second},
		"firewalls": []map[string]int64{{"firewall": web}, {"firewall": ssh}}})
	want := []string{"attach_to_network server,network", "attach_to_network server,network",
		"apply_firewall firewall,server", "apply_firewall firewall,server", "start_server server"}
	if got := commands(created.NextActions); !slices.Equal(got, want) {
		t.Errorf("next_actions are %v, want %v", got, want)
	}
	if got := created.NextActions[2].Resources[0].ID; got != web {
		t.Errorf("the first apply_firewall names the firewall %d, want %d", got, web)
	}
	if len(created.Server.PrivateNet) != 2 || created.Server.PrivateNet[0].IP != "10.1.1.1" || created.Server.PrivateNet[1].IP != "10.2.1.1" {
		t.Errorf("the answer's private_net is %+v", created.Server.PrivateNet)
	}
	// the selector's firewall comes later: with the time scale 0 of the
	// test at once, applied (TestFirewallSelectorShowsPendingAsHetznerDoes)
	got := map[int64]string{}
	for _, firewall := range created.Server.PublicNet.Firewalls {
		got[firewall.ID] = firewall.Status
	}
	if got[web] != "pending" || got[ssh] != "pending" || got[selected] == "pending" {
		t.Errorf("the answer's firewalls are %v, want web and ssh pending", got)
	}
	waitActions(t, test, append([]schema.Action{created.Action}, created.NextActions...)...)
	waitForFirewalls(t, test, created.Server.ID, map[int64]string{web: "applied", ssh: "applied", selected: "applied"})
	var ruleset agentproto.Ruleset
	if err := json.Unmarshal(agent.Picture(agentproto.PictureRuleset), &ruleset); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{web, ssh, selected} {
		if want := fmt.Sprintf("jump firewall_%d_in", id); !strings.Contains(ruleset.Text, want) {
			t.Errorf("the ruleset lacks %q", want)
		}
	}
	if got := commands(firewallActionList(t, test, selected)); !slices.Contains(got, "apply_firewall firewall,server") {
		t.Errorf("the selector's firewall has the actions %v, want an apply_firewall of the server", got)
	}
	if got := getNetwork(t, test, first).Servers; !slices.Equal(got, []int64{created.Server.ID}) {
		t.Errorf("the first network has the servers %v", got)
	}
}

// waitForFirewalls waits until a server shows exactly these firewalls with
// these statuses: a label selector's comes in the background.
func waitForFirewalls(t *testing.T, test *testLab, server int64, want map[int64]string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := map[int64]string{}
		for _, firewall := range getServer(t, test, server).PublicNet.Firewalls {
			got[firewall.ID] = firewall.Status
		}
		if fmt.Sprint(got) == fmt.Sprint(want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server shows the firewalls %v, want %v", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// firewallActionList returns a firewall's actions.
func firewallActionList(t *testing.T, test *testLab, id int64) []schema.Action {
	t.Helper()
	var answer schema.ActionListResponse
	test.client.Get(fmt.Sprintf("/v1/firewalls/%d/actions", id)).Decode(t, &answer)
	return answer.Actions
}

// The errors of a create's networks and firewalls, as Hetzner answers them
// (measured 2026-11-09), each alone; a network or firewall that is missing
// comes after the SSH keys, a network before a firewall.
func TestServerCreateNetworkAndFirewallErrors(t *testing.T) {
	test := newTestLab(t)
	first, second := networkWithSubnet(t, test, "first", "10.1"), networkWithSubnet(t, test, "second", "10.2")
	third := networkWithSubnet(t, test, "third", "10.3")
	overlapping := networkWithSubnet(t, test, "overlapping", "10.1")
	empty := createNetwork(t, test, map[string]any{"name": "empty", "ip_range": "10.9.0.0/16"}).ID
	var firewalls []map[string]int64
	for i := range 6 {
		firewalls = append(firewalls, map[string]int64{"firewall": createFirewall(t, test, map[string]any{"name": fmt.Sprint("fw", i)}).Firewall.ID})
	}
	body := func(extra map[string]any) map[string]any {
		out := map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13", "start_after_create": false}
		for key, value := range extra {
			out[key] = value
		}
		return out
	}
	expectErrors(t, test, "/v1/servers", map[string]struct {
		body any
		want networkError
	}{
		"an unknown network":       {body(map[string]any{"networks": []int64{1}}), networkError{http.StatusNotFound, "not_found", "unknown network with ID 1", nil}},
		"a network twice":          {body(map[string]any{"networks": []int64{first, first}}), invalidField("networks", "Networks must be unique.")},
		"four networks":            {body(map[string]any{"networks": []int64{first, second, third, overlapping}}), networkError{http.StatusForbidden, "resource_limit_exceeded", "networks per resource limit reached", nil}},
		"overlapping networks":     {body(map[string]any{"networks": []int64{first, overlapping}}), refused("networks_overlap", "network IP range overlaps with one of the server networks")},
		"a network without subnet": {body(map[string]any{"networks": []int64{empty}}), invalidField("networks", "networks must have at least one subnetwork")},
		"the network ID 0":         {body(map[string]any{"networks": []int64{0}}), networkError{http.StatusInternalServerError, "server_error", "internal server error", nil}},
		"a network that is no ID":  {body(map[string]any{"networks": []string{"x"}}), invalid("failed to parse json")},
		"an unknown firewall":      {body(map[string]any{"firewalls": []map[string]int64{{"firewall": 1}}}), networkError{http.StatusNotFound, "firewall_not_found", "firewall not found", nil}},
		"a firewall entry without firewall": {body(map[string]any{"firewalls": []map[string]any{{}}}),
			invalidField("firewalls", "Missing data for required field firewalls.firewall.")},
		"a firewall twice":         {body(map[string]any{"firewalls": []map[string]int64{firewalls[0], firewalls[0]}}), invalidField("firewalls", "Firewalls must be unique.")},
		"six firewalls":            {body(map[string]any{"firewalls": firewalls}), networkError{http.StatusForbidden, "resource_limit_exceeded", "firewall limit on server exceeded", nil}},
		"a firewall that is an ID": {body(map[string]any{"firewalls": []int64{1}}), invalid("failed to parse json")},
		"both missing":             {body(map[string]any{"networks": []int64{1}, "firewalls": []map[string]int64{{"firewall": 1}}}), networkError{http.StatusNotFound, "not_found", "unknown network with ID 1", nil}},
		"an SSH key first":         {body(map[string]any{"networks": []int64{1}, "ssh_keys": []string{"nope"}}), networkError{http.StatusNotFound, "not_found", "SSH key not found", nil}},
	})
	response := &responseOf{test.client.Do(http.MethodPost, "/v1/servers", body(map[string]any{"firewalls": firewalls}))}
	if !strings.Contains(string(response.Body), `"limits":[{"name":"firewalls_per_server_limit"}]`) {
		t.Errorf("six firewalls' details are %s", response.Body)
	}
	// Floating IPs have no field: Hetzner ignores one
	created := createServer(t, test, body(map[string]any{"floating_ips": []int64{1}}))
	if len(created.NextActions) != 0 {
		t.Errorf("a create with floating_ips has the next actions %v", commands(created.NextActions))
	}
}

// A server created into a network shows "attaching" among its members only
// for a moment, while its create and its attach still run (measured
// 2026-11-09: 1.2 s).
func TestCreatedMemberAttachesForAMoment(t *testing.T) {
	clock := &handClock{now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
	test := newTimedTestLab(t, clock, 1)
	agent := startMachine(t, test, platform.HostResources{Threads: 8, MemoryMB: 16 * 1024})
	network := networkWithSubnet(t, test, "net", "10.1")
	waiting, release := agent.HoldNext(agentproto.PictureLinks) // the create's pictures
	created := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13",
		"start_after_create": false, "networks": []int64{network}})
	<-waiting
	status := func() string {
		var answer struct {
			Members []schema.NetworkMember `json:"members"`
		}
		test.client.Get(fmt.Sprintf("/v1/networks/%d/members", network)).Decode(t, &answer)
		if len(answer.Members) != 1 {
			t.Fatalf("the members are %+v", answer.Members)
		}
		return answer.Members[0].Status
	}
	if got := status(); got != "attaching" {
		t.Errorf("right after the create the member is %q, want attaching", got)
	}
	clock.Advance(1200 * time.Millisecond)
	if got := status(); got != "ok" {
		t.Errorf("after 1.2 s the member is %q, want ok; the server is %s", got, getServer(t, test, created.Server.ID).Status)
	}
	release()
	waitActions(t, test, append([]schema.Action{created.Action}, created.NextActions...)...)
}
