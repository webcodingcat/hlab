//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-03", Name: "inventory", Fixture: true, Run: testCLI03Inventory})
}

// testCLI03Inventory: the inventory plugin hetzner.hcloud.hcloud lists the
// fixture server with ansible_host and hcloud_ipv4 equal to its public
// IPv4, in the group of its label.
func testCLI03Inventory(t *testing.T, c *rowCtx) {
	e, w := sharedAnsible(t)
	fx := c.Fixture
	file := w.dir + "/cli03.hcloud.yml"
	inventory := `plugin: hetzner.hcloud.hcloud
keyed_groups:
  - key: hcloud_labels
    prefix: label
`
	script := "set -e\ncat > '" + file + "' <<'HLAB_INVENTORY_EOF'\n" + inventory + "HLAB_INVENTORY_EOF\n" +
		"ANSIBLE_NOCOLOR=1 ansible-inventory -i '" + file + "' --list\n"
	r, err := w.run(context.Background(), 5*time.Minute, e.ansibleEnviron(), script)
	if err != nil {
		t.Fatalf("ansible-inventory --list: %v\n%s", err, r)
	}
	var inv struct {
		Meta struct {
			HostVars map[string]struct {
				AnsibleHost string `json:"ansible_host"`
				HcloudIPv4  string `json:"hcloud_ipv4"`
			} `json:"hostvars"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &inv); err != nil {
		t.Fatalf("ansible-inventory --list printed no JSON: %v\n%s", err, r)
	}
	ipv4 := e.serverInfo(t, fx.ID).PublicNet.IPv4
	if ipv4 == nil {
		t.Fatalf("fixture server %d has no public IPv4", fx.ID)
	}
	host, ok := inv.Meta.HostVars[fx.Name]
	if !ok {
		t.Fatalf("the inventory has no host %s:\n%s", fx.Name, r.Stdout)
	}
	if host.AnsibleHost != ipv4.IP || host.HcloudIPv4 != ipv4.IP {
		t.Errorf("host %s: ansible_host %q, hcloud_ipv4 %q; the API has %s", fx.Name, host.AnsibleHost, host.HcloudIPv4, ipv4.IP)
	}

	var groups map[string]struct {
		Hosts []string `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &groups); err != nil {
		t.Fatalf("ansible-inventory --list: %v", err)
	}
	group := "label_" + fixtureLabel + "_" + clientOf("CLI-03")
	if !slices.Contains(groups[group].Hosts, fx.Name) {
		t.Errorf("group %s has hosts %v, want %s among them:\n%s", group, groups[group].Hosts, fx.Name, r.Stdout)
	}
}
