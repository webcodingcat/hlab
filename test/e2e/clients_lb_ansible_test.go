//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"maps"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-03", Name: "load_balancers", Fixture: true, Run: testCLI03LoadBalancers})
}

// testCLI03LoadBalancers: load_balancer, load_balancer_network,
// load_balancer_service and load_balancer_target create, update and remove;
// load_balancer_info shows them.
func testCLI03LoadBalancers(t *testing.T, c *rowCtx) {
	e, w := sharedAnsible(t)
	fx := c.Fixture
	netName, name := resName("ansible", "lb-net"), resName("ansible", "load-balancers")
	selector := "row=cli-03-lb-targets"
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = e.hcloudRun(ctx, 2*time.Minute, "load-balancer", "disable-protection", name, "delete")
		_, _ = e.hcloudRun(ctx, 5*time.Minute, "load-balancer", "delete", name)
		_, _ = e.hcloudRun(ctx, 5*time.Minute, "server", "detach-from-network", "--network", netName, fx.Name)
		_, _ = e.hcloudRun(ctx, 5*time.Minute, "network", "delete", netName)
	})
	row := map[string]any{
		"name": name, "network": netName, "server": fx.Name, "location": serverLocation,
		"selector": selector, "ip": lbTargetIP,
	}
	lb := func(lbType, algorithm string, webPort int) string {
		return fmt.Sprintf(`    - name: load balancer
      hetzner.hcloud.load_balancer:
        name: "{{ row.name }}"
        load_balancer_type: %s
        location: "{{ row.location }}"
        algorithm: %s
        labels:
          row: cli-03-load-balancers
        state: present
    - name: web service
      hetzner.hcloud.load_balancer_service:
        load_balancer: "{{ row.name }}"
        protocol: http
        listen_port: 8080
        destination_port: %d
        state: present
`, lbType, algorithm, webPort)
	}
	members := func(state string) string {
		return `    - name: ssh service
      hetzner.hcloud.load_balancer_service:
        load_balancer: "{{ row.name }}"
        protocol: tcp
        listen_port: 2222
        destination_port: 22
        state: ` + state + `
    - name: server target
      hetzner.hcloud.load_balancer_target:
        load_balancer: "{{ row.name }}"
        type: server
        server: "{{ row.server }}"
        use_private_ip: true
        state: ` + state + `
    - name: label selector target
      hetzner.hcloud.load_balancer_target:
        load_balancer: "{{ row.name }}"
        type: label_selector
        label_selector: "{{ row.selector }}"
        state: ` + state + `
    - name: IP target
      hetzner.hcloud.load_balancer_target:
        load_balancer: "{{ row.name }}"
        type: ip
        ip: "{{ row.ip }}"
        state: ` + state + `
`
	}

	e.ansibleOK(t, w, "cli03-load-balancers-create", localPlay(t, row, `    - name: network
      hetzner.hcloud.network:
        name: "{{ row.network }}"
        ip_range: 10.46.0.0/16
        state: present
    - name: subnetwork
      hetzner.hcloud.subnetwork:
        network: "{{ row.network }}"
        type: cloud
        network_zone: eu-central
        ip_range: 10.46.1.0/24
        state: present
    - name: fixture server in the network
      hetzner.hcloud.server_network:
        network: "{{ row.network }}"
        server: "{{ row.server }}"
        state: present
`+lb("lb11", "round_robin", 80)+`    - name: load balancer network
      hetzner.hcloud.load_balancer_network:
        load_balancer: "{{ row.name }}"
        network: "{{ row.network }}"
        state: present
`+members("present")+`    - name: load balancer info
      hetzner.hcloud.load_balancer_info:
        name: "{{ row.name }}"
      register: info
    - name: check the answer
      ansible.builtin.assert:
        that:
          - info.hcloud_load_balancer_info | length == 1
          - info.hcloud_load_balancer_info[0].services | length == 2
          - info.hcloud_load_balancer_info[0].targets | length == 3
          - info.hcloud_load_balancer_info[0].load_balancer_type == "lb11"
`))
	var described lbDetail
	e.hcloudJSON(t, &described, "load-balancer", "describe", name)
	id := described.ID
	var nw networkDetail
	e.hcloudJSON(t, &nw, "network", "describe", netName)
	targets := []string{fmt.Sprintf("server:%d:private", fx.ID), "label_selector:" + selector, "ip:" + lbTargetIP}
	got := e.wantLB(t, "create", id, []string{"tcp:2222>22", "http:8080>80"}, targets)
	if got.Type.Name != "lb11" || got.Algorithm.Type != "round_robin" || !got.inNetwork(nw.ID) ||
		!maps.Equal(got.Labels, map[string]string{"row": "cli-03-load-balancers"}) {
		t.Errorf("load balancer %+v, want lb11, round_robin, in network %d", got, nw.ID)
	}

	e.ansibleOK(t, w, "cli03-load-balancers-update", localPlay(t, row, lb("lb21", "least_connections", 81)))
	got = e.wantLB(t, "update", id, []string{"tcp:2222>22", "http:8080>81"}, targets)
	if got.Type.Name != "lb21" || got.Algorithm.Type != "least_connections" {
		t.Errorf("updated load balancer: %s, %s; want lb21, least_connections", got.Type.Name, got.Algorithm.Type)
	}

	e.ansibleOK(t, w, "cli03-load-balancers-remove", localPlay(t, row, members("absent")+`    - name: web service absent
      hetzner.hcloud.load_balancer_service:
        load_balancer: "{{ row.name }}"
        listen_port: 8080
        state: absent
    - name: load balancer network absent
      hetzner.hcloud.load_balancer_network:
        load_balancer: "{{ row.name }}"
        network: "{{ row.network }}"
        state: absent
`))
	if got := e.wantLB(t, "remove", id, nil, nil); got.inNetwork(nw.ID) {
		t.Errorf("load balancer %d still in network %d after load_balancer_network absent", id, nw.ID)
	}

	e.ansibleOK(t, w, "cli03-load-balancers-absent", localPlay(t, row, `    - name: load balancer absent
      hetzner.hcloud.load_balancer:
        name: "{{ row.name }}"
        state: absent
    - name: fixture server out of the network
      hetzner.hcloud.server_network:
        network: "{{ row.network }}"
        server: "{{ row.server }}"
        state: absent
    - name: network absent
      hetzner.hcloud.network:
        name: "{{ row.network }}"
        state: absent
`))
	assertGoneIDs(t, e, "/load_balancers", "load_balancers", id)
	assertGoneIDs(t, e, "/networks", "networks", nw.ID)
	e.wantNoServerNet(t, "absent", fx, nw.ID)
}
