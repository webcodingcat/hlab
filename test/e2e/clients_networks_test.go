//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-01", Name: "networks", Fixture: true, Run: testCLI01Networks})
	registerRow(clientRow{Req: "CLI-02", Name: "networks", Fixture: true, Run: testCLI02Networks})
	registerRow(clientRow{Req: "CLI-03", Name: "networks", Fixture: true, Run: testCLI03Networks})
}

// networkDetail is what the client rows check of GET /networks/{id}.
type networkDetail struct {
	ID      int64             `json:"id"`
	Name    string            `json:"name"`
	IPRange string            `json:"ip_range"`
	Labels  map[string]string `json:"labels"`
	Subnets []struct {
		Type        string `json:"type"`
		IPRange     string `json:"ip_range"`
		NetworkZone string `json:"network_zone"`
	} `json:"subnets"`
	Routes []struct {
		Destination string `json:"destination"`
		Gateway     string `json:"gateway"`
	} `json:"routes"`
	Servers       []int64 `json:"servers"`
	LoadBalancers []int64 `json:"load_balancers"`
	Protection    struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
}

func (e *env) networkInfo(t *testing.T, id int64) networkDetail {
	t.Helper()
	var body struct {
		Network networkDetail `json:"network"`
	}
	e.getOK(t, fmt.Sprintf("/networks/%d", id), &body)
	return body.Network
}

// subnetRanges are the IP ranges of the network's subnets, sorted.
func (n networkDetail) subnetRanges() []string {
	var out []string
	for _, s := range n.Subnets {
		out = append(out, s.IPRange)
	}
	slices.Sort(out)
	return out
}

// routeList are the network's routes as destination>gateway, sorted.
func (n networkDetail) routeList() []string {
	var out []string
	for _, r := range n.Routes {
		out = append(out, r.Destination+">"+r.Gateway)
	}
	slices.Sort(out)
	return out
}

// serverNet is a private_net entry of GET /servers/{id} with alias IPs.
type serverNet struct {
	Network  int64    `json:"network"`
	IP       string   `json:"ip"`
	AliasIPs []string `json:"alias_ips"`
}

// serverNetIn is the server's private_net entry for the network, if any.
func (e *env) serverNetIn(t *testing.T, id, network int64) (serverNet, bool) {
	t.Helper()
	var body struct {
		Server struct {
			PrivateNet []serverNet `json:"private_net"`
		} `json:"server"`
	}
	e.getOK(t, fmt.Sprintf("/servers/%d", id), &body)
	for _, n := range body.Server.PrivateNet {
		if n.Network == network {
			slices.Sort(n.AliasIPs)
			return n, true
		}
	}
	return serverNet{}, false
}

// wantServerNet fails unless the server is in the network with ip and
// exactly the alias IPs.
func (e *env) wantServerNet(t *testing.T, step string, srv *server, network int64, ip string, alias ...string) {
	t.Helper()
	slices.Sort(alias)
	got, ok := e.serverNetIn(t, srv.ID, network)
	if !ok || got.IP != ip || !slices.Equal(got.AliasIPs, alias) {
		t.Errorf("%s: server %d in network %d: %t, %+v; want ip %s, alias IPs %v", step, srv.ID, network, ok, got, ip, alias)
	}
	if n := e.networkInfo(t, network); !slices.Contains(n.Servers, srv.ID) {
		t.Errorf("%s: network %d lists servers %v, want %d among them", step, network, n.Servers, srv.ID)
	}
}

// wantNoServerNet fails if the server is still in the network.
func (e *env) wantNoServerNet(t *testing.T, step string, srv *server, network int64) {
	t.Helper()
	if got, ok := e.serverNetIn(t, srv.ID, network); ok {
		t.Errorf("%s: server %d still in network %d: %+v", step, srv.ID, network, got)
	}
}

// testCLI01Networks: hcloud manages a network with subnets, routes, IP
// range, labels and protection, attaches the fixture server with alias IPs,
// changes them, lists members, detaches and deletes.
func testCLI01Networks(t *testing.T, c *rowCtx) {
	e, fx := c.E, c.Fixture
	name := resName("hcloud", "networks")
	renamed := name + "-renamed"
	t.Cleanup(func() {
		ctx := context.Background()
		for _, n := range []string{name, renamed} {
			_, _ = e.hcloudRun(ctx, 5*time.Minute, "server", "detach-from-network", "--network", n, fx.Name)
			_, _ = e.hcloudRun(ctx, 2*time.Minute, "network", "disable-protection", n, "delete")
			_, _ = e.hcloudRun(ctx, 2*time.Minute, "network", "delete", n)
		}
	})

	e.hcloudOK(t, "network", "create", "--name", name, "--ip-range", "10.40.0.0/16", "--label", "row=cli-01-networks")
	var listed []networkDetail
	e.hcloudJSON(t, &listed, "network", "list", "-l", "row=cli-01-networks")
	if len(listed) != 1 || listed[0].Name != name {
		t.Fatalf("hcloud network list -l row=cli-01-networks: %+v, want only %s", listed, name)
	}
	id := listed[0].ID

	e.hcloudOK(t, "network", "add-subnet", "--type", "cloud", "--network-zone", "eu-central", "--ip-range", "10.40.1.0/24", name)
	e.hcloudOK(t, "network", "add-subnet", "--type", "cloud", "--network-zone", "eu-central", "--ip-range", "10.40.2.0/24", name)
	e.hcloudOK(t, "network", "add-route", "--destination", "10.100.0.0/24", "--gateway", "10.40.1.10", name)
	e.hcloudOK(t, "network", "change-ip-range", "--ip-range", "10.40.0.0/15", name)
	got := e.networkInfo(t, id)
	if got.IPRange != "10.40.0.0/15" || !slices.Equal(got.subnetRanges(), []string{"10.40.1.0/24", "10.40.2.0/24"}) ||
		!slices.Equal(got.routeList(), []string{"10.100.0.0/24>10.40.1.10"}) {
		t.Errorf("network after add-subnet, add-route, change-ip-range: range %s, subnets %v, routes %v", got.IPRange, got.subnetRanges(), got.routeList())
	}
	var described networkDetail
	e.hcloudJSON(t, &described, "network", "describe", name)
	if described.ID != id || described.IPRange != got.IPRange || len(described.Subnets) != len(got.Subnets) {
		t.Errorf("hcloud network describe %s: %+v, the API has %+v", name, described, got)
	}

	e.hcloudOK(t, "network", "update", "--name", renamed, name)
	e.hcloudOK(t, "network", "add-label", renamed, "stage=two")
	e.hcloudOK(t, "network", "remove-label", renamed, "row")
	got = e.networkInfo(t, id)
	if want := map[string]string{"stage": "two"}; got.Name != renamed || !maps.Equal(got.Labels, want) {
		t.Errorf("updated network %q with labels %v, want %q with %v", got.Name, got.Labels, renamed, want)
	}
	e.hcloudOK(t, "network", "enable-protection", renamed, "delete")
	if !e.networkInfo(t, id).Protection.Delete {
		t.Error("delete protection off after enable-protection")
	}
	e.hcloudOK(t, "network", "disable-protection", renamed, "delete")
	if e.networkInfo(t, id).Protection.Delete {
		t.Error("delete protection on after disable-protection")
	}

	e.hcloudOK(t, "server", "attach-to-network", "--network", renamed, "--ip", "10.40.1.5", "--alias-ips", "10.40.1.6", fx.Name)
	e.wantServerNet(t, "attach-to-network", fx, id, "10.40.1.5", "10.40.1.6")
	e.hcloudOK(t, "server", "change-alias-ips", "--network", renamed, "--alias-ips", "10.40.1.7,10.40.1.8", fx.Name)
	e.wantServerNet(t, "change-alias-ips", fx, id, "10.40.1.5", "10.40.1.7", "10.40.1.8")
	var members []map[string]any
	e.hcloudJSON(t, &members, "network", "list-members", renamed)
	found := false
	for _, m := range members {
		if v, ok := m["id"].(float64); ok && int64(v) == fx.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("hcloud network list-members %s: %v, want the fixture server %d", renamed, members, fx.ID)
	}
	e.hcloudOK(t, "server", "detach-from-network", "--network", renamed, fx.Name)
	e.wantNoServerNet(t, "detach-from-network", fx, id)

	e.hcloudOK(t, "network", "remove-route", "--destination", "10.100.0.0/24", "--gateway", "10.40.1.10", renamed)
	e.hcloudOK(t, "network", "remove-subnet", "--ip-range", "10.40.2.0/24", renamed)
	got = e.networkInfo(t, id)
	if len(got.Routes) != 0 || !slices.Equal(got.subnetRanges(), []string{"10.40.1.0/24"}) {
		t.Errorf("network after remove-route, remove-subnet: subnets %v, routes %v", got.subnetRanges(), got.routeList())
	}

	e.hcloudOK(t, "network", "delete", renamed)
	assertGoneIDs(t, e, "/networks", "networks", id)
}

// testCLI02Networks: OpenTofu creates a network, two subnets in parallel, a
// route and hcloud_server_network with alias IPs on the fixture server,
// updates them, reads hcloud_network, hcloud_networks and
// hcloud_network_members, and destroys them.
func testCLI02Networks(t *testing.T, c *rowCtx) {
	e, fx := sharedTofu(t), c.Fixture
	d := e.newTofuDir(t, "networks")
	main := func(name, stage, alias string) string {
		return fmt.Sprintf(`resource "hcloud_network" "n" {
  name     = %q
  ip_range = "10.41.0.0/16"
  labels = {
    row   = "cli-02-networks"
    stage = %q
  }
}

resource "hcloud_network_subnet" "a" {
  network_id   = hcloud_network.n.id
  type         = "cloud"
  network_zone = "eu-central"
  ip_range     = "10.41.1.0/24"
}

resource "hcloud_network_subnet" "b" {
  network_id   = hcloud_network.n.id
  type         = "cloud"
  network_zone = "eu-central"
  ip_range     = "10.41.2.0/24"
}

resource "hcloud_network_route" "r" {
  network_id  = hcloud_network.n.id
  destination = "10.101.0.0/24"
  gateway     = "10.41.1.10"
  depends_on  = [hcloud_network_subnet.a]
}

resource "hcloud_server_network" "fx" {
  server_id = %d
  subnet_id = hcloud_network_subnet.a.id
  ip        = "10.41.1.5"
  alias_ips = [%q]
}

data "hcloud_network" "by_id" {
  id         = hcloud_network.n.id
  depends_on = [hcloud_network.n]
}

data "hcloud_networks" "by_label" {
  with_selector = "row=cli-02-networks"
  depends_on    = [hcloud_network.n]
}

data "hcloud_network_members" "m" {
  network_id = hcloud_network.n.id
  depends_on = [hcloud_server_network.fx]
}

output "id" {
  value = hcloud_network.n.id
}

output "by_id_name" {
  value = data.hcloud_network.by_id.name
}

output "label_ids" {
  value = jsonencode(data.hcloud_networks.by_label.networks[*].id)
}

output "member_ids" {
  value = jsonencode(data.hcloud_network_members.m.members[*].id)
}
`, name, stage, fx.ID, alias)
	}
	d.run(t, "init", "-no-color")

	name := resName("tofu", "networks")
	renamed := name + "-renamed"
	d.writeMain(t, main(name, "one", "10.41.1.6"))
	t.Cleanup(func() {
		for _, n := range []string{name, renamed} {
			_, _ = e.hcloudRun(context.Background(), 5*time.Minute, "server", "detach-from-network", "--network", n, fx.Name)
			_, _ = e.hcloudRun(context.Background(), 5*time.Minute, "network", "delete", n)
		}
	})
	d.run(t, "apply", "-auto-approve", "-no-color")
	idText := d.output(t, "id")
	id := parseID(t, idText)
	got := e.networkInfo(t, id)
	if want := map[string]string{"row": "cli-02-networks", "stage": "one"}; got.Name != name || !maps.Equal(got.Labels, want) ||
		!slices.Equal(got.subnetRanges(), []string{"10.41.1.0/24", "10.41.2.0/24"}) ||
		!slices.Equal(got.routeList(), []string{"10.101.0.0/24>10.41.1.10"}) {
		t.Errorf("network %+v, want %s with %v, subnets 10.41.1.0/24 and 10.41.2.0/24, route 10.101.0.0/24 via 10.41.1.10", got, name, want)
	}
	e.wantServerNet(t, "hcloud_server_network", fx, id, "10.41.1.5", "10.41.1.6")
	if n := d.output(t, "by_id_name"); n != name {
		t.Errorf("data hcloud_network by ID: name %q, want %q", n, name)
	}
	if ids := d.output(t, "label_ids"); ids != "["+idText+"]" {
		t.Errorf("data hcloud_networks with row=cli-02-networks: IDs %s, want [%s]", ids, idText)
	}
	if ids := d.output(t, "member_ids"); !strings.Contains(ids, strconv.FormatInt(fx.ID, 10)) {
		t.Errorf("data hcloud_network_members: IDs %s, want the fixture server %d", ids, fx.ID)
	}

	d.writeMain(t, main(renamed, "two", "10.41.1.7"))
	r := d.run(t, "apply", "-auto-approve", "-no-color")
	if want := "0 added, 2 changed, 0 destroyed"; !strings.Contains(r.Stdout, want) {
		t.Errorf("second apply is no in-place update of network and attachment (want %q):\n%s", want, r.Stdout)
	}
	got = e.networkInfo(t, id)
	if want := map[string]string{"row": "cli-02-networks", "stage": "two"}; got.Name != renamed || !maps.Equal(got.Labels, want) {
		t.Errorf("updated network %q with labels %v, want %q with %v", got.Name, got.Labels, renamed, want)
	}
	e.wantServerNet(t, "updated hcloud_server_network", fx, id, "10.41.1.5", "10.41.1.7")
	if n := d.output(t, "by_id_name"); n != renamed {
		t.Errorf("data hcloud_network by ID after the update: name %q, want %q", n, renamed)
	}

	d.run(t, "destroy", "-auto-approve", "-no-color")
	assertGoneIDs(t, e, "/networks", "networks", id)
	e.wantNoServerNet(t, "destroy", fx, id)
}

// testCLI03Networks: network, subnetwork, route and server_network create,
// update and remove; network_info and network_member_info show them.
func testCLI03Networks(t *testing.T, c *rowCtx) {
	e, w := sharedAnsible(t)
	fx := c.Fixture
	name := resName("ansible", "networks")
	t.Cleanup(func() {
		_, _ = e.hcloudRun(context.Background(), 5*time.Minute, "server", "detach-from-network", "--network", name, fx.Name)
		_, _ = e.hcloudRun(context.Background(), 5*time.Minute, "network", "delete", name)
	})
	row := map[string]any{"name": name, "server": fx.Name, "server_id": fx.ID}
	present := func(ipRange, stage, alias string) string {
		return `    - name: network
      hetzner.hcloud.network:
        name: "{{ row.name }}"
        ip_range: ` + ipRange + `
        labels:
          row: cli-03-networks
          stage: ` + stage + `
        state: present
    - name: subnetwork
      hetzner.hcloud.subnetwork:
        network: "{{ row.name }}"
        type: cloud
        network_zone: eu-central
        ip_range: 10.42.1.0/24
        state: present
    - name: route
      hetzner.hcloud.route:
        network: "{{ row.name }}"
        destination: 10.102.0.0/24
        gateway: 10.42.1.10
        state: present
    - name: server network
      hetzner.hcloud.server_network:
        network: "{{ row.name }}"
        server: "{{ row.server }}"
        ip: 10.42.1.5
        alias_ips:
          - ` + alias + `
        state: present
`
	}

	e.ansibleOK(t, w, "cli03-networks-create", localPlay(t, row, present("10.42.0.0/16", "one", "10.42.1.6")+`    - name: network info
      hetzner.hcloud.network_info:
        name: "{{ row.name }}"
      register: info
    - name: member info
      hetzner.hcloud.network_member_info:
        network: "{{ row.name }}"
      register: members
    - name: check the answers
      ansible.builtin.assert:
        that:
          - info.hcloud_network_info | length == 1
          - info.hcloud_network_info[0].subnetworks | length == 1
          - info.hcloud_network_info[0].routes | length == 1
          - row.server_id in (info.hcloud_network_info[0].servers | map(attribute='id') | map('int') | list)
          - row.server_id in (members.hcloud_network_member_info | map(attribute='id') | map('int') | list)
`))
	var described networkDetail
	e.hcloudJSON(t, &described, "network", "describe", name)
	id := described.ID
	got := e.networkInfo(t, id)
	if want := map[string]string{"row": "cli-03-networks", "stage": "one"}; got.IPRange != "10.42.0.0/16" || !maps.Equal(got.Labels, want) ||
		!slices.Equal(got.subnetRanges(), []string{"10.42.1.0/24"}) || !slices.Equal(got.routeList(), []string{"10.102.0.0/24>10.42.1.10"}) {
		t.Errorf("network %+v, want 10.42.0.0/16 with %v, subnet 10.42.1.0/24, route 10.102.0.0/24 via 10.42.1.10", got, want)
	}
	e.wantServerNet(t, "server_network", fx, id, "10.42.1.5", "10.42.1.6")

	e.ansibleOK(t, w, "cli03-networks-update", localPlay(t, row, present("10.42.0.0/15", "two", "10.42.1.7")))
	got = e.networkInfo(t, id)
	if want := map[string]string{"row": "cli-03-networks", "stage": "two"}; got.IPRange != "10.42.0.0/15" || !maps.Equal(got.Labels, want) {
		t.Errorf("updated network: range %s, labels %v; want 10.42.0.0/15, %v", got.IPRange, got.Labels, want)
	}
	e.wantServerNet(t, "updated server_network", fx, id, "10.42.1.5", "10.42.1.7")

	e.ansibleOK(t, w, "cli03-networks-remove", localPlay(t, row, `    - name: server network absent
      hetzner.hcloud.server_network:
        network: "{{ row.name }}"
        server: "{{ row.server }}"
        state: absent
    - name: route absent
      hetzner.hcloud.route:
        network: "{{ row.name }}"
        destination: 10.102.0.0/24
        gateway: 10.42.1.10
        state: absent
    - name: subnetwork absent
      hetzner.hcloud.subnetwork:
        network: "{{ row.name }}"
        type: cloud
        network_zone: eu-central
        ip_range: 10.42.1.0/24
        state: absent
`))
	e.wantNoServerNet(t, "server_network absent", fx, id)
	if got = e.networkInfo(t, id); len(got.Subnets) != 0 || len(got.Routes) != 0 {
		t.Errorf("network after route and subnetwork absent: subnets %v, routes %v", got.subnetRanges(), got.routeList())
	}

	e.ansibleOK(t, w, "cli03-networks-absent", localPlay(t, row, `    - name: network absent
      hetzner.hcloud.network:
        name: "{{ row.name }}"
        state: absent
`))
	assertGoneIDs(t, e, "/networks", "networks", id)
}
