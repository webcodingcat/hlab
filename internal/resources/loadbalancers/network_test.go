package loadbalancers_test

import (
	"net/http"
	"net/netip"
	"testing"
)

func serverTargetBody(id int64, private bool) map[string]any {
	return map[string]any{"type": "server", "server": map[string]any{"id": id}, "use_private_ip": private}
}

// testNetwork: attach and detach, and the public interface.
func testNetwork(t *testing.T) {
	e := newEnv(t)
	n := createNetwork(t, e, "n", "10.0.0.0/16", "10.0.1.0/24")
	n2 := createNetwork(t, e, "n2", "10.1.0.0/16", "10.1.1.0/24")
	lb := createLB(t, e, lbBody("net"))

	wantError(t, lbAction(e, lb.ID, "disable_public_interface", nil),
		http.StatusUnprocessableEntity, "load_balancer_not_attached_to_network", "disable_public_interface without a network")

	runLBAction(t, e, lb.ID, "attach_to_network", map[string]any{"network": n, "ip": "10.0.1.5"})
	got := mustLB(t, e, lb.ID)
	if len(got.PrivateNet) != 1 || got.PrivateNet[0].Network != n || got.PrivateNet[0].IP != "10.0.1.5" {
		t.Errorf("after attach with ip 10.0.1.5: private_net %+v, want network %d ip 10.0.1.5", got.PrivateNet, n)
	}
	wantError(t, lbAction(e, lb.ID, "attach_to_network", map[string]any{"network": n2}),
		http.StatusUnprocessableEntity, "load_balancer_already_attached", "attach to a second network")

	runLBAction(t, e, lb.ID, "detach_from_network", map[string]any{"network": n})
	if got := mustLB(t, e, lb.ID); len(got.PrivateNet) != 0 {
		t.Errorf("after detach: private_net %+v, want none", got.PrivateNet)
	}

	runLBAction(t, e, lb.ID, "attach_to_network", map[string]any{"network": n})
	got = mustLB(t, e, lb.ID)
	if len(got.PrivateNet) != 1 || got.PrivateNet[0].Network != n {
		t.Fatalf("after automatic attach: private_net %+v, want network %d", got.PrivateNet, n)
	}
	if a, err := netip.ParseAddr(got.PrivateNet[0].IP); err != nil || !netip.MustParsePrefix("10.0.1.0/24").Contains(a) {
		t.Errorf("automatic attach: ip %q, want one of 10.0.1.0/24", got.PrivateNet[0].IP)
	}

	s := createServer(t, e, "public", "fsn1", nil)
	runLBAction(t, e, lb.ID, "add_target", serverTargetBody(s.ID, false))
	wantError(t, lbAction(e, lb.ID, "disable_public_interface", nil),
		http.StatusUnprocessableEntity, "targets_without_use_private_ip", "disable_public_interface with a public target")
	if !mustLB(t, e, lb.ID).PublicNet.Enabled {
		t.Errorf("public interface disabled after the refusal")
	}
	runLBAction(t, e, lb.ID, "remove_target", map[string]any{"type": "server", "server": map[string]any{"id": s.ID}})

	runLBAction(t, e, lb.ID, "disable_public_interface", nil)
	if mustLB(t, e, lb.ID).PublicNet.Enabled {
		t.Errorf("after disable_public_interface: public_net.enabled true, want false")
	}
	wantError(t, lbAction(e, lb.ID, "add_target", serverTargetBody(s.ID, false)),
		http.StatusUnprocessableEntity, "load_balancer_public_interface_disabled", "public server target while the interface is disabled")

	runLBAction(t, e, lb.ID, "enable_public_interface", nil)
	if !mustLB(t, e, lb.ID).PublicNet.Enabled {
		t.Errorf("after enable_public_interface: public_net.enabled false, want true")
	}
}
