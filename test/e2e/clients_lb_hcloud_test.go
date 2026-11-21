//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-01", Name: "load_balancers", Fixture: true, Run: testCLI01LoadBalancers})
}

// lbDetail is what the client rows check of GET /load_balancers/{id}.
type lbDetail struct {
	ID     int64             `json:"id"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Type   struct {
		Name string `json:"name"`
	} `json:"load_balancer_type"`
	Algorithm struct {
		Type string `json:"type"`
	} `json:"algorithm"`
	PublicNet struct {
		Enabled bool `json:"enabled"`
		IPv4    struct {
			IP     string `json:"ip"`
			DNSPtr string `json:"dns_ptr"`
		} `json:"ipv4"`
	} `json:"public_net"`
	PrivateNet []struct {
		Network int64  `json:"network"`
		IP      string `json:"ip"`
	} `json:"private_net"`
	Services []struct {
		Protocol        string `json:"protocol"`
		ListenPort      int    `json:"listen_port"`
		DestinationPort int    `json:"destination_port"`
	} `json:"services"`
	Targets []struct {
		Type   string `json:"type"`
		Server *struct {
			ID int64 `json:"id"`
		} `json:"server"`
		LabelSelector *struct {
			Selector string `json:"selector"`
		} `json:"label_selector"`
		IP *struct {
			IP string `json:"ip"`
		} `json:"ip"`
		UsePrivateIP bool `json:"use_private_ip"`
	} `json:"targets"`
	Protection struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
}

func (e *env) lbInfo(t *testing.T, id int64) lbDetail {
	t.Helper()
	var body struct {
		LoadBalancer lbDetail `json:"load_balancer"`
	}
	e.getOK(t, fmt.Sprintf("/load_balancers/%d", id), &body)
	return body.LoadBalancer
}

// services are the load balancer's services as protocol:listen>destination,
// sorted.
func (l lbDetail) services() []string {
	var out []string
	for _, s := range l.Services {
		out = append(out, fmt.Sprintf("%s:%d>%d", s.Protocol, s.ListenPort, s.DestinationPort))
	}
	slices.Sort(out)
	return out
}

// targets are the load balancer's targets as server:<id>[:private],
// label_selector:<selector> or ip:<ip>, sorted.
func (l lbDetail) targets() []string {
	var out []string
	for _, tg := range l.Targets {
		var s string
		switch {
		case tg.Server != nil:
			s = fmt.Sprintf("server:%d", tg.Server.ID)
		case tg.LabelSelector != nil:
			s = "label_selector:" + tg.LabelSelector.Selector
		case tg.IP != nil:
			s = "ip:" + tg.IP.IP
		default:
			s = tg.Type
		}
		if tg.UsePrivateIP {
			s += ":private"
		}
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// inNetwork reports whether the load balancer is attached to the network.
func (l lbDetail) inNetwork(network int64) bool {
	for _, n := range l.PrivateNet {
		if n.Network == network {
			return true
		}
	}
	return false
}

// wantLB fails unless the load balancer has exactly the services and
// targets (as services and targets list them).
func (e *env) wantLB(t *testing.T, step string, id int64, services, targets []string) lbDetail {
	t.Helper()
	got := e.lbInfo(t, id)
	slices.Sort(services)
	slices.Sort(targets)
	if !slices.Equal(got.services(), services) || !slices.Equal(got.targets(), targets) {
		t.Errorf("%s: load balancer %d services %v, targets %v; want %v, %v", step, id, got.services(), got.targets(), services, targets)
	}
	return got
}

// deleteLBByID deletes a load balancer a row made, with its delete
// protection turned off first; 0 is no load balancer.
func (e *env) deleteLBByID(t *testing.T, id int64) {
	t.Helper()
	if id == 0 {
		return
	}
	path := fmt.Sprintf("/load_balancers/%d", id)
	_, _, _ = e.apiDo(context.Background(), http.MethodPost, path+"/actions/change_protection", map[string]any{"delete": false}, nil)
	err := waitFor(5*time.Minute, 3*time.Second, func() (bool, error) {
		status, _, err := e.apiDo(context.Background(), http.MethodDelete, path, nil, nil)
		return err == nil && (status == http.StatusNoContent || status == http.StatusOK || status == http.StatusNotFound), nil
	})
	if err != nil {
		t.Errorf("DELETE %s: %v", path, err)
	}
}

// lbTargetIP is the IP target of the load balancer rows (TEST-NET-3).
const lbTargetIP = "203.0.113.10"

// testCLI01LoadBalancers: hcloud manages a load balancer through every
// load-balancer command: network, services, targets (server with private
// IP, label selector, IP), algorithm, type, public interface, reverse DNS,
// metrics, labels, protection, delete.
func testCLI01LoadBalancers(t *testing.T, c *rowCtx) {
	e, fx := c.E, c.Fixture
	netName, name := resName("hcloud", "lb-net"), resName("hcloud", "load-balancers")
	renamed := name + "-renamed"
	selector := "row=cli-01-lb-targets"
	t.Cleanup(func() {
		ctx := context.Background()
		for _, n := range []string{name, renamed} {
			_, _ = e.hcloudRun(ctx, 2*time.Minute, "load-balancer", "disable-protection", n, "delete")
			_, _ = e.hcloudRun(ctx, 5*time.Minute, "load-balancer", "delete", n)
		}
		_, _ = e.hcloudRun(ctx, 5*time.Minute, "server", "detach-from-network", "--network", netName, fx.Name)
		_, _ = e.hcloudRun(ctx, 5*time.Minute, "network", "delete", netName)
	})

	// The network of the row and the fixture server in it, for the target
	// with its private IP.
	e.hcloudOK(t, "network", "create", "--name", netName, "--ip-range", "10.44.0.0/16")
	e.hcloudOK(t, "network", "add-subnet", "--type", "cloud", "--network-zone", "eu-central", "--ip-range", "10.44.1.0/24", netName)
	e.hcloudOK(t, "server", "attach-to-network", "--network", netName, fx.Name)
	var nw networkDetail
	e.hcloudJSON(t, &nw, "network", "describe", netName)

	e.hcloudOK(t, "load-balancer", "create", "--name", name, "--type", "lb11", "--location", serverLocation,
		"--algorithm-type", "round_robin", "--label", "row=cli-01-load-balancers")
	var listed []lbDetail
	e.hcloudJSON(t, &listed, "load-balancer", "list", "-l", "row=cli-01-load-balancers")
	if len(listed) != 1 || listed[0].Name != name {
		t.Fatalf("hcloud load-balancer list -l row=cli-01-load-balancers: %+v, want only %s", listed, name)
	}
	id := listed[0].ID
	got := e.lbInfo(t, id)
	if got.Type.Name != "lb11" || got.Algorithm.Type != "round_robin" || !got.PublicNet.Enabled {
		t.Errorf("created load balancer %+v, want lb11, round_robin, public interface on", got)
	}
	var described lbDetail
	e.hcloudJSON(t, &described, "load-balancer", "describe", name)
	if described.ID != id || described.PublicNet.IPv4.IP != got.PublicNet.IPv4.IP {
		t.Errorf("hcloud load-balancer describe %s: %+v, the API has %+v", name, described, got)
	}

	e.hcloudOK(t, "load-balancer", "attach-to-network", "--network", netName, name)
	if !e.lbInfo(t, id).inNetwork(nw.ID) {
		t.Errorf("load balancer %d not in network %d after attach-to-network", id, nw.ID)
	}

	e.hcloudOK(t, "load-balancer", "add-service", "--protocol", "tcp", "--listen-port", "2222", "--destination-port", "22", name)
	e.hcloudOK(t, "load-balancer", "add-service", "--protocol", "http", "--listen-port", "8080", "--destination-port", "80", name)
	e.hcloudOK(t, "load-balancer", "update-service", "--listen-port", "8080", "--destination-port", "81", name)
	e.wantLB(t, "add-service, update-service", id, []string{"tcp:2222>22", "http:8080>81"}, nil)

	e.hcloudOK(t, "load-balancer", "add-target", "--server", fx.Name, "--use-private-ip", name)
	e.hcloudOK(t, "load-balancer", "add-target", "--label-selector", selector, name)
	e.hcloudOK(t, "load-balancer", "add-target", "--ip", lbTargetIP, name)
	services := []string{"tcp:2222>22", "http:8080>81"}
	e.wantLB(t, "add-target", id, services,
		[]string{fmt.Sprintf("server:%d:private", fx.ID), "label_selector:" + selector, "ip:" + lbTargetIP})
	e.hcloudOK(t, "load-balancer", "remove-target", "--ip", lbTargetIP, name)
	e.hcloudOK(t, "load-balancer", "remove-target", "--label-selector", selector, name)
	e.hcloudOK(t, "load-balancer", "delete-service", "--listen-port", "8080", name)
	// hcloud delete-service returns without waiting for its action (unlike
	// the other load-balancer commands); the next steps need it finished.
	if err := waitFor(2*time.Minute, time.Second, func() (bool, error) {
		return len(e.lbInfo(t, id).Services) == 1, nil
	}); err != nil {
		t.Errorf("delete-service: service 8080 still there: %v", err)
	}
	e.wantLB(t, "remove-target, delete-service", id, []string{"tcp:2222>22"}, []string{fmt.Sprintf("server:%d:private", fx.ID)})

	e.hcloudOK(t, "load-balancer", "change-algorithm", "--algorithm-type", "least_connections", name)
	e.hcloudOK(t, "load-balancer", "change-type", name, "lb21")
	if got := e.lbInfo(t, id); got.Algorithm.Type != "least_connections" || got.Type.Name != "lb21" {
		t.Errorf("after change-algorithm and change-type: %s, %s; want least_connections, lb21", got.Algorithm.Type, got.Type.Name)
	}
	e.hcloudOK(t, "load-balancer", "disable-public-interface", name)
	if e.lbInfo(t, id).PublicNet.Enabled {
		t.Error("public interface still on after disable-public-interface")
	}
	e.hcloudOK(t, "load-balancer", "enable-public-interface", name)
	if !e.lbInfo(t, id).PublicNet.Enabled {
		t.Error("public interface off after enable-public-interface")
	}
	ptr := "lb." + zoneName("hcloud-rdns")
	e.hcloudOK(t, "load-balancer", "set-rdns", "--ip", got.PublicNet.IPv4.IP, "--hostname", ptr, name)
	if p := e.lbInfo(t, id).PublicNet.IPv4.DNSPtr; p != ptr {
		t.Errorf("dns_ptr of %s after set-rdns: %q, want %q", got.PublicNet.IPv4.IP, p, ptr)
	}
	e.hcloudOK(t, "load-balancer", "metrics", "--type", "open_connections", name)

	e.hcloudOK(t, "load-balancer", "update", "--name", renamed, name)
	e.hcloudOK(t, "load-balancer", "add-label", renamed, "stage=two")
	e.hcloudOK(t, "load-balancer", "remove-label", renamed, "row")
	if got := e.lbInfo(t, id); got.Name != renamed || !maps.Equal(got.Labels, map[string]string{"stage": "two"}) {
		t.Errorf("updated load balancer %q with labels %v, want %q with stage=two", got.Name, got.Labels, renamed)
	}
	e.hcloudOK(t, "load-balancer", "enable-protection", renamed, "delete")
	if !e.lbInfo(t, id).Protection.Delete {
		t.Error("delete protection off after enable-protection")
	}
	e.hcloudOK(t, "load-balancer", "disable-protection", renamed, "delete")
	if e.lbInfo(t, id).Protection.Delete {
		t.Error("delete protection on after disable-protection")
	}

	e.hcloudOK(t, "load-balancer", "remove-target", "--server", fx.Name, renamed)
	e.hcloudOK(t, "load-balancer", "detach-from-network", "--network", netName, renamed)
	if got := e.wantLB(t, "remove-target", id, []string{"tcp:2222>22"}, nil); got.inNetwork(nw.ID) {
		t.Errorf("load balancer %d still in network %d after detach-from-network", id, nw.ID)
	}

	e.hcloudOK(t, "load-balancer", "delete", renamed)
	assertGoneIDs(t, e, "/load_balancers", "load_balancers", id)
	e.hcloudOK(t, "server", "detach-from-network", "--network", netName, fx.Name)
	e.hcloudOK(t, "network", "delete", netName)
}
