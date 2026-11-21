//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-02", Name: "load_balancers", Fixture: true, Run: testCLI02LoadBalancers})
}

// testCLI02LoadBalancers: OpenTofu creates a load balancer, its network,
// two services and two targets in parallel, updates type, algorithm and a
// service in place, reads hcloud_load_balancer(s) and
// hcloud_load_balancer_type(s), and destroys them.
func testCLI02LoadBalancers(t *testing.T, c *rowCtx) {
	e, fx := sharedTofu(t), c.Fixture
	d := e.newTofuDir(t, "load-balancers")
	netName, name := resName("tofu", "lb-net"), resName("tofu", "load-balancers")
	selector := "row=cli-02-lb-targets"
	main := func(lbType, algorithm string, webPort int) string {
		return fmt.Sprintf(`resource "hcloud_network" "n" {
  name     = %q
  ip_range = "10.45.0.0/16"
}

resource "hcloud_network_subnet" "s" {
  network_id   = hcloud_network.n.id
  type         = "cloud"
  network_zone = "eu-central"
  ip_range     = "10.45.1.0/24"
}

resource "hcloud_server_network" "fx" {
  server_id = %d
  subnet_id = hcloud_network_subnet.s.id
}

resource "hcloud_load_balancer" "lb" {
  name               = %q
  load_balancer_type = %q
  location           = %q
  labels = {
    row = "cli-02-load-balancers"
  }

  algorithm {
    type = %q
  }
}

resource "hcloud_load_balancer_network" "n" {
  load_balancer_id = hcloud_load_balancer.lb.id
  subnet_id        = hcloud_network_subnet.s.id
}

resource "hcloud_load_balancer_service" "ssh" {
  load_balancer_id = hcloud_load_balancer.lb.id
  protocol         = "tcp"
  listen_port      = 2222
  destination_port = 22
}

resource "hcloud_load_balancer_service" "web" {
  load_balancer_id = hcloud_load_balancer.lb.id
  protocol         = "http"
  listen_port      = 8080
  destination_port = %d
}

resource "hcloud_load_balancer_target" "server" {
  type             = "server"
  load_balancer_id = hcloud_load_balancer.lb.id
  server_id        = %d
  use_private_ip   = true
  depends_on       = [hcloud_load_balancer_network.n, hcloud_server_network.fx]
}

resource "hcloud_load_balancer_target" "selector" {
  type             = "label_selector"
  load_balancer_id = hcloud_load_balancer.lb.id
  label_selector   = %q
}

data "hcloud_load_balancer" "by_id" {
  id = hcloud_load_balancer.lb.id
  depends_on = [
    hcloud_load_balancer_service.ssh,
    hcloud_load_balancer_service.web,
    hcloud_load_balancer_target.server,
    hcloud_load_balancer_target.selector,
  ]
}

data "hcloud_load_balancers" "by_label" {
  with_selector = "row=cli-02-load-balancers"
  depends_on    = [hcloud_load_balancer.lb]
}

data "hcloud_load_balancer_type" "used" {
  name = hcloud_load_balancer.lb.load_balancer_type
}

data "hcloud_load_balancer_types" "all" {}

output "id" {
  value = hcloud_load_balancer.lb.id
}

output "network_id" {
  value = hcloud_network.n.id
}

output "by_id_algorithm" {
  value = data.hcloud_load_balancer.by_id.algorithm[0].type
}

output "by_id_targets" {
  value = length(data.hcloud_load_balancer.by_id.target)
}

output "label_ids" {
  value = jsonencode(data.hcloud_load_balancers.by_label.load_balancers[*].id)
}

output "type_id" {
  value = data.hcloud_load_balancer_type.used.id
}

output "type_names" {
  value = jsonencode(sort(data.hcloud_load_balancer_types.all.load_balancer_types[*].name))
}
`, netName, fx.ID, name, lbType, serverLocation, algorithm, webPort, fx.ID, selector)
	}
	d.run(t, "init", "-no-color")

	var id, network int64
	t.Cleanup(func() {
		e.deleteLBByID(t, id)
		_, _ = e.hcloudRun(context.Background(), 5*time.Minute, "server", "detach-from-network", "--network", netName, fx.Name)
		if network != 0 {
			e.deleteNetwork(t, network)
		}
	})
	d.writeMain(t, main("lb11", "round_robin", 80))
	d.run(t, "apply", "-auto-approve", "-no-color")
	idText := d.output(t, "id")
	id, network = parseID(t, idText), parseID(t, d.output(t, "network_id"))
	targets := []string{fmt.Sprintf("server:%d:private", fx.ID), "label_selector:" + selector}
	got := e.wantLB(t, "apply", id, []string{"tcp:2222>22", "http:8080>80"}, targets)
	if got.Name != name || got.Type.Name != "lb11" || got.Algorithm.Type != "round_robin" || !got.inNetwork(network) ||
		!maps.Equal(got.Labels, map[string]string{"row": "cli-02-load-balancers"}) {
		t.Errorf("load balancer %+v, want %s, lb11, round_robin, in network %d", got, name, network)
	}
	// Provider 1.70.2 declares a service list on the data source but never
	// sets it (getLoadBalancerAttributes in internal/loadbalancer/resource.go
	// has no "service" key), so the row reads the data source's targets;
	// wantLB above checks the services GET returns.
	if n := d.output(t, "by_id_targets"); n != "2" {
		t.Errorf("data hcloud_load_balancer by ID: %s targets, want 2", n)
	}
	if ids := d.output(t, "label_ids"); ids != "["+idText+"]" {
		t.Errorf("data hcloud_load_balancers with row=cli-02-load-balancers: IDs %s, want [%s]", ids, idText)
	}
	types := e.apiNamed(t, "/load_balancer_types", "load_balancer_types")
	var apiNames []string
	for _, n := range types {
		apiNames = append(apiNames, n)
	}
	slices.Sort(apiNames)
	var names []string
	if err := json.Unmarshal([]byte(d.output(t, "type_names")), &names); err != nil || !slices.Equal(names, apiNames) {
		t.Errorf("data hcloud_load_balancer_types: names %v (%v), the API has %v", names, err, apiNames)
	}
	if tid := parseID(t, d.output(t, "type_id")); types[tid] != "lb11" {
		t.Errorf("data hcloud_load_balancer_type lb11: ID %d, the API names it %q", tid, types[tid])
	}

	d.writeMain(t, main("lb21", "least_connections", 81))
	r := d.run(t, "apply", "-auto-approve", "-no-color")
	if want := "0 added, 2 changed, 0 destroyed"; !strings.Contains(r.Stdout, want) {
		t.Errorf("second apply is no in-place update of load balancer and service (want %q):\n%s", want, r.Stdout)
	}
	got = e.wantLB(t, "update", id, []string{"tcp:2222>22", "http:8080>81"}, targets)
	if got.Type.Name != "lb21" || got.Algorithm.Type != "least_connections" {
		t.Errorf("updated load balancer: %s, %s; want lb21, least_connections", got.Type.Name, got.Algorithm.Type)
	}
	if a := d.output(t, "by_id_algorithm"); a != "least_connections" {
		t.Errorf("data hcloud_load_balancer by ID after the update: algorithm %q, want least_connections", a)
	}

	d.run(t, "destroy", "-auto-approve", "-no-color")
	assertGoneIDs(t, e, "/load_balancers", "load_balancers", id)
	assertGoneIDs(t, e, "/networks", "networks", network)
	e.wantNoServerNet(t, "destroy", fx, network)
}
