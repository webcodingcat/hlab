package firewalls_test

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/resources/firewalls"
)

// pushedServer returns the agent document's entry for server id.
func pushedServer(t *testing.T, doc agentapi.Config, id int64) agentapi.Server {
	t.Helper()
	for _, s := range doc.Servers {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("pushed Servers %+v, want an entry for server %d", doc.Servers, id)
	return agentapi.Server{}
}

func pushedFirewall(doc agentapi.Config, id int64) (agentapi.Firewall, bool) {
	for _, f := range doc.Firewalls {
		if f.ID == id {
			return f, true
		}
	}
	return agentapi.Firewall{}, false
}

// relabel sets the server's labels and returns the next pushed document.
func relabel(t *testing.T, e *fwEnv, id int64, labels map[string]string) agentapi.Config {
	t.Helper()
	before := e.pushCount()
	resp := e.Do(http.MethodPut, fmt.Sprintf("/servers/%d", id), map[string]any{"labels": labels})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /servers/%d labels %v: status %d (%s), want 200", id, labels, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	_ = resp.Body.Close()
	return e.waitPush(t, before)
}

// testLabelChange is FW-05: a server that gets a label matching an applied
// label selector is filtered by that firewall.
func testLabelChange(t *testing.T) {
	e := newFWEnv(t)
	rules := []firewalls.Rule{inRule("tcp", ptr("80"), "0.0.0.0/0")}
	s := createServer(t, e, "s", nil)
	before := e.pushCount()
	fw, _ := createFirewall(t, e, map[string]any{
		"name": "web", "rules": rules, "apply_to": []any{selectorTarget("role=web")},
	})

	doc := e.waitPush(t, before)
	if ids := pushedServer(t, doc, s.ID).FirewallIDs; len(ids) != 0 {
		t.Errorf("server without the label: pushed FirewallIDs %v, want none", ids)
	}
	if f, ok := pushedFirewall(doc, fw.ID); ok {
		t.Errorf("firewall reaching no server pushed as %+v, want no entry", f)
	}

	doc = relabel(t, e, s.ID, map[string]string{"role": "web"})
	if ids := pushedServer(t, doc, s.ID).FirewallIDs; !slices.Equal(ids, []int64{fw.ID}) {
		t.Errorf("after labelling role=web: pushed FirewallIDs %v, want [%d]", ids, fw.ID)
	}
	if f, ok := pushedFirewall(doc, fw.ID); !ok || !reflect.DeepEqual(f.Rules, firewalls.AgentRules(rules)) {
		t.Errorf("after labelling role=web: pushed Firewalls %+v, want firewall %d with rules %+v", doc.Firewalls, fw.ID, firewalls.AgentRules(rules))
	}
	if got := getServer(t, e, s.ID).PublicNet.Firewalls; !slices.Equal(got, []serverFirewall{{fw.ID, "applied"}}) {
		t.Errorf("after labelling role=web: server public_net.firewalls %+v, want [{%d applied}]", got, fw.ID)
	}

	doc = relabel(t, e, s.ID, map[string]string{})
	if ids := pushedServer(t, doc, s.ID).FirewallIDs; len(ids) != 0 {
		t.Errorf("after removing the label: pushed FirewallIDs %v, want none", ids)
	}
}
