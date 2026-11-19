package loadbalancers_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
)

func selectorTargetBody(sel string) map[string]any {
	return map[string]any{"type": "label_selector", "label_selector": map[string]any{"selector": sel}}
}

func ipTargetBody(ip string) map[string]any {
	return map[string]any{"type": "ip", "ip": map[string]any{"ip": ip}}
}

func addTarget(e *lbEnv, id int64, body map[string]any) *http.Response {
	return lbAction(e, id, "add_target", body)
}

// selectorTarget returns the label selector target sel, if any.
func (l lbView) selectorTarget(sel string) (targetView, bool) {
	for _, tg := range l.Targets {
		if tg.Type == "label_selector" && tg.LabelSelector != nil && tg.LabelSelector.Selector == sel {
			return tg, true
		}
	}
	return targetView{}, false
}

// ipTarget reports whether the load balancer has ip target ip.
func (l lbView) ipTarget(ip string) bool {
	return slices.ContainsFunc(l.Targets, func(tg targetView) bool {
		return tg.Type == "ip" && tg.IP != nil && tg.IP.IP == ip
	})
}

// nestedServers returns the server IDs of a label selector's targets.
func nestedServers(tg targetView) []int64 {
	var ids []int64
	for _, n := range tg.Targets {
		if n.Server != nil {
			ids = append(ids, n.Server.ID)
		}
	}
	slices.Sort(ids)
	return ids
}

// testTargets: adding and removing server, label selector and ip targets.
func testTargets(t *testing.T) {
	e := newEnv(t)
	n := createNetwork(t, e, "n", "10.0.0.0/16", "10.0.1.0/24")
	lb := createLB(t, e, lbBody("targets"))
	runLBAction(t, e, lb.ID, "attach_to_network", map[string]any{"network": n})

	web := map[string]string{"role": "web"}
	public := createServer(t, e, "public", "fsn1", map[string]any{"labels": web})
	private := createServer(t, e, "private", "fsn1", map[string]any{"labels": web, "networks": []int64{n}})
	outside := createServer(t, e, "outside", "fsn1", nil)
	noV4 := createServer(t, e, "no-ipv4", "fsn1", noIPv4)
	far := createServer(t, e, "far", "ash", nil)

	runLBAction(t, e, lb.ID, "add_target", serverTargetBody(public.ID, false))
	runLBAction(t, e, lb.ID, "add_target", serverTargetBody(private.ID, true))
	runLBAction(t, e, lb.ID, "add_target", selectorTargetBody("role=web"))
	runLBAction(t, e, lb.ID, "add_target", ipTargetBody("203.0.113.10"))
	got := mustLB(t, e, lb.ID)
	if tg, ok := got.serverTarget(public.ID); !ok || tg.UsePrivateIP == nil || *tg.UsePrivateIP {
		t.Errorf("public server target %+v (found %v), want use_private_ip false", tg, ok)
	}
	if tg, ok := got.serverTarget(private.ID); !ok || tg.UsePrivateIP == nil || !*tg.UsePrivateIP {
		t.Errorf("private server target %+v (found %v), want use_private_ip true", tg, ok)
	}
	sel, ok := got.selectorTarget("role=web")
	want := []int64{public.ID, private.ID}
	slices.Sort(want)
	if !ok || !slices.Equal(nestedServers(sel), want) {
		t.Errorf("label selector target %+v (found %v), want nested servers %v", sel, ok, want)
	}
	if !got.ipTarget("203.0.113.10") {
		t.Errorf("targets %+v lack ip 203.0.113.10", got.Targets)
	}

	wantError(t, addTarget(e, lb.ID, serverTargetBody(outside.ID, true)),
		http.StatusUnprocessableEntity, "resolve_cloud_private_targets_error", "use_private_ip for a server outside the network")
	wantError(t, addTarget(e, lb.ID, serverTargetBody(noV4.ID, false)),
		http.StatusUnprocessableEntity, "resolve_cloud_public_targets_error", "public target of a server without IPv4")
	wantInvalid(t, addTarget(e, lb.ID, serverTargetBody(far.ID, false)), "server in another network zone")
	wantInvalid(t, addTarget(e, lb.ID, ipTargetBody("198.19.0.2")), "ip target in the transit /30")
	wantInvalid(t, addTarget(e, lb.ID, ipTargetBody(got.ipv4())), "ip target equal to the LB's IP")
	wantError(t, addTarget(e, lb.ID, serverTargetBody(public.ID, false)),
		http.StatusUnprocessableEntity, "target_already_defined", "duplicate server target")

	runLBAction(t, e, lb.ID, "remove_target", ipTargetBody("203.0.113.10"))
	if mustLB(t, e, lb.ID).ipTarget("203.0.113.10") {
		t.Errorf("ip target still there after remove_target")
	}

	resp := e.Do(http.MethodDelete, fmt.Sprintf("/servers/%d", public.ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /servers/%d: status %d (%s), want 200", public.ID, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var del struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &del)
	if a := e.For(t).WaitAction(del.Action.ID); a.Status != "success" {
		t.Fatalf("delete server: action %q %+v", a.Status, a.Error)
	}
	if _, ok := mustLB(t, e, lb.ID).serverTarget(public.ID); ok {
		t.Errorf("target of the deleted server %d still there", public.ID)
	}

	// lb11 allows 25 targets.
	full := createLB(t, e, lbBody("full"))
	for i := range 25 {
		runLBAction(t, e, full.ID, "add_target", ipTargetBody(fmt.Sprintf("203.0.113.%d", 100+i)))
	}
	wantError(t, addTarget(e, full.ID, ipTargetBody("203.0.113.200")),
		http.StatusForbidden, "resource_limit_exceeded", "a 26th target on lb11")
}
