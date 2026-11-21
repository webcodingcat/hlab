//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-01", Name: "firewalls", Fixture: true, Run: testCLI01Firewalls})
	registerRow(clientRow{Req: "CLI-02", Name: "firewalls", Fixture: true, Run: testCLI02Firewalls})
	registerRow(clientRow{Req: "CLI-03", Name: "firewalls", Fixture: true, Run: testCLI03Firewalls})
}

// firewallDetail is what the client rows check of GET /firewalls/{id}.
type firewallDetail struct {
	ID     int64             `json:"id"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Rules  []struct {
		Direction string  `json:"direction"`
		Protocol  string  `json:"protocol"`
		Port      *string `json:"port"`
	} `json:"rules"`
	AppliedTo []struct {
		Type   string `json:"type"`
		Server *struct {
			ID int64 `json:"id"`
		} `json:"server"`
		LabelSelector *struct {
			Selector string `json:"selector"`
		} `json:"label_selector"`
	} `json:"applied_to"`
}

func (e *env) firewallInfo(t *testing.T, id int64) firewallDetail {
	t.Helper()
	var body struct {
		Firewall firewallDetail `json:"firewall"`
	}
	e.getOK(t, fmt.Sprintf("/firewalls/%d", id), &body)
	return body.Firewall
}

// rules are the firewall's rules as direction/protocol/port, sorted.
func (f firewallDetail) rules() []string {
	var out []string
	for _, r := range f.Rules {
		port := ""
		if r.Port != nil {
			port = *r.Port
		}
		out = append(out, r.Direction+"/"+r.Protocol+"/"+port)
	}
	slices.Sort(out)
	return out
}

// appliedTo are the firewall's resources as server:<id> or
// label_selector:<selector>, sorted.
func (f firewallDetail) appliedTo() []string {
	var out []string
	for _, a := range f.AppliedTo {
		switch {
		case a.Server != nil:
			out = append(out, fmt.Sprintf("server:%d", a.Server.ID))
		case a.LabelSelector != nil:
			out = append(out, "label_selector:"+a.LabelSelector.Selector)
		default:
			out = append(out, a.Type)
		}
	}
	slices.Sort(out)
	return out
}

// wantFirewall fails unless the firewall has the rules and is applied to
// exactly the resources (as appliedTo lists them).
func (e *env) wantFirewall(t *testing.T, step string, id int64, rules, applied []string) firewallDetail {
	t.Helper()
	got := e.firewallInfo(t, id)
	slices.Sort(rules)
	slices.Sort(applied)
	if !slices.Equal(got.rules(), rules) || !slices.Equal(got.appliedTo(), applied) {
		t.Errorf("%s: firewall %d rules %v, applied to %v; want %v, %v", step, id, got.rules(), got.appliedTo(), rules, applied)
	}
	return got
}

// deleteFirewallByID removes a firewall a row made from its resources and
// deletes it; 0 is no firewall.
func (e *env) deleteFirewallByID(t *testing.T, id int64) {
	t.Helper()
	if id == 0 {
		return
	}
	path := fmt.Sprintf("/firewalls/%d", id)
	var body struct {
		Firewall firewallDetail `json:"firewall"`
	}
	if status, _, err := e.apiDo(context.Background(), http.MethodGet, path, nil, &body); err != nil || status == http.StatusNotFound {
		return
	}
	var from []map[string]any
	for _, a := range body.Firewall.AppliedTo {
		switch {
		case a.Server != nil:
			from = append(from, map[string]any{"type": "server", "server": map[string]any{"id": a.Server.ID}})
		case a.LabelSelector != nil:
			from = append(from, map[string]any{"type": "label_selector", "label_selector": map[string]any{"selector": a.LabelSelector.Selector}})
		}
	}
	if len(from) > 0 {
		_, _, _ = e.apiDo(context.Background(), http.MethodPost, path+"/actions/remove_from_resources", map[string]any{"remove_from": from}, nil)
	}
	err := waitFor(5*time.Minute, 3*time.Second, func() (bool, error) {
		status, _, err := e.apiDo(context.Background(), http.MethodDelete, path, nil, nil)
		return err == nil && (status == http.StatusNoContent || status == http.StatusNotFound), nil
	})
	if err != nil {
		t.Errorf("DELETE %s: %v", path, err)
	}
}

// sshRuleIn is the rule every firewall row keeps, so the fixture server
// stays reachable over SSH.
const sshRuleIn = "in/tcp/22"

// writeRules writes rules as an hcloud rules file into the row's folder.
func writeRules(t *testing.T, name string, rules []map[string]any) string {
	t.Helper()
	b, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(f, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// testCLI01Firewalls: hcloud creates a firewall with rules, adds, deletes
// and replaces rules, updates, labels, applies it to the fixture server and
// a label selector, removes it and deletes it.
func testCLI01Firewalls(t *testing.T, c *rowCtx) {
	e, fx := c.E, c.Fixture
	name := resName("hcloud", "firewalls")
	renamed := name + "-renamed"
	var id int64
	t.Cleanup(func() { e.deleteFirewallByID(t, id) })
	selector := fixtureLabel + "=hcloud"
	icmp := map[string]any{"direction": "in", "protocol": "icmp", "source_ips": []string{"0.0.0.0/0", "::/0"}}

	rules := writeRules(t, "rules.json", []map[string]any{sshInRule()})
	e.hcloudOK(t, "firewall", "create", "--name", name, "--rules-file", rules, "--label", "row=cli-01-firewalls")
	var listed []firewallDetail
	e.hcloudJSON(t, &listed, "firewall", "list", "-l", "row=cli-01-firewalls")
	if len(listed) != 1 || listed[0].Name != name {
		t.Fatalf("hcloud firewall list -l row=cli-01-firewalls: %+v, want only %s", listed, name)
	}
	id = listed[0].ID
	e.wantFirewall(t, "create", id, []string{sshRuleIn}, nil)
	var described firewallDetail
	e.hcloudJSON(t, &described, "firewall", "describe", name)
	if described.ID != id || !slices.Equal(described.rules(), []string{sshRuleIn}) {
		t.Errorf("hcloud firewall describe %s: %+v, want ID %d with rule %s", name, described, id, sshRuleIn)
	}

	web := []string{"--direction", "in", "--protocol", "tcp", "--port", "80", "--source-ips", "0.0.0.0/0", name}
	e.hcloudOK(t, append([]string{"firewall", "add-rule"}, web...)...)
	e.wantFirewall(t, "add-rule", id, []string{sshRuleIn, "in/tcp/80"}, nil)
	e.hcloudOK(t, append([]string{"firewall", "delete-rule"}, web...)...)
	e.wantFirewall(t, "delete-rule", id, []string{sshRuleIn}, nil)
	e.hcloudOK(t, "firewall", "replace-rules", "--rules-file", writeRules(t, "replaced.json", []map[string]any{sshInRule(), icmp}), name)
	e.wantFirewall(t, "replace-rules", id, []string{sshRuleIn, "in/icmp/"}, nil)

	e.hcloudOK(t, "firewall", "update", "--name", renamed, name)
	e.hcloudOK(t, "firewall", "add-label", renamed, "stage=two")
	e.hcloudOK(t, "firewall", "remove-label", renamed, "row")
	if got := e.firewallInfo(t, id); got.Name != renamed || !maps.Equal(got.Labels, map[string]string{"stage": "two"}) {
		t.Errorf("updated firewall %q with labels %v, want %q with stage=two", got.Name, got.Labels, renamed)
	}

	e.hcloudOK(t, "firewall", "apply-to-resource", "--type", "server", "--server", fx.Name, renamed)
	e.hcloudOK(t, "firewall", "apply-to-resource", "--type", "label_selector", "--label-selector", selector, renamed)
	e.wantFirewall(t, "apply-to-resource", id, []string{sshRuleIn, "in/icmp/"},
		[]string{fmt.Sprintf("server:%d", fx.ID), "label_selector:" + selector})
	e.hcloudOK(t, "firewall", "remove-from-resource", "--type", "server", "--server", fx.Name, renamed)
	e.hcloudOK(t, "firewall", "remove-from-resource", "--type", "label_selector", "--label-selector", selector, renamed)
	e.wantFirewall(t, "remove-from-resource", id, []string{sshRuleIn, "in/icmp/"}, nil)

	e.hcloudOK(t, "firewall", "delete", renamed)
	assertGoneIDs(t, e, "/firewalls", "firewalls", id)
}

// testCLI02Firewalls: OpenTofu manages hcloud_firewall and
// hcloud_firewall_attachment (fixture server and label selector), changes
// rules in place, reads hcloud_firewall and hcloud_firewalls, and destroys
// them.
func testCLI02Firewalls(t *testing.T, c *rowCtx) {
	e, fx := sharedTofu(t), c.Fixture
	d := e.newTofuDir(t, "firewalls")
	selector := fixtureLabel + "=tofu"
	main := func(name, stage, port string) string {
		return fmt.Sprintf(`resource "hcloud_firewall" "f" {
  name = %q
  labels = {
    row   = "cli-02-firewalls"
    stage = %q
  }

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "22"
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = %q
    source_ips = ["0.0.0.0/0"]
  }
}

resource "hcloud_firewall_attachment" "a" {
  firewall_id     = hcloud_firewall.f.id
  server_ids      = [%d]
  label_selectors = [%q]
}

data "hcloud_firewall" "by_id" {
  id         = hcloud_firewall.f.id
  depends_on = [hcloud_firewall.f, hcloud_firewall_attachment.a]
}

data "hcloud_firewalls" "by_label" {
  with_selector = "row=cli-02-firewalls"
  depends_on    = [hcloud_firewall.f]
}

output "id" {
  value = hcloud_firewall.f.id
}

output "by_id_rules" {
  value = length(data.hcloud_firewall.by_id.rule)
}

output "label_ids" {
  value = jsonencode(data.hcloud_firewalls.by_label.firewalls[*].id)
}
`, name, stage, port, fx.ID, selector)
	}
	d.run(t, "init", "-no-color")

	var id int64
	t.Cleanup(func() { e.deleteFirewallByID(t, id) })
	applied := []string{fmt.Sprintf("server:%d", fx.ID), "label_selector:" + selector}
	name := resName("tofu", "firewalls")
	d.writeMain(t, main(name, "one", "80"))
	d.run(t, "apply", "-auto-approve", "-no-color")
	idText := d.output(t, "id")
	id = parseID(t, idText)
	got := e.wantFirewall(t, "apply", id, []string{sshRuleIn, "in/tcp/80"}, applied)
	if want := map[string]string{"row": "cli-02-firewalls", "stage": "one"}; got.Name != name || !maps.Equal(got.Labels, want) {
		t.Errorf("firewall %q with labels %v, want %q with %v", got.Name, got.Labels, name, want)
	}
	if n := d.output(t, "by_id_rules"); n != "2" {
		t.Errorf("data hcloud_firewall by ID: %s rules, want 2", n)
	}
	if ids := d.output(t, "label_ids"); ids != "["+idText+"]" {
		t.Errorf("data hcloud_firewalls with row=cli-02-firewalls: IDs %s, want [%s]", ids, idText)
	}

	renamed := name + "-renamed"
	d.writeMain(t, main(renamed, "two", "443"))
	r := d.run(t, "apply", "-auto-approve", "-no-color")
	if !strings.Contains(r.Stdout, updatedInPlace) {
		t.Errorf("second apply is no in-place update (want %q):\n%s", updatedInPlace, r.Stdout)
	}
	got = e.wantFirewall(t, "rules changed", id, []string{sshRuleIn, "in/tcp/443"}, applied)
	if want := map[string]string{"row": "cli-02-firewalls", "stage": "two"}; got.Name != renamed || !maps.Equal(got.Labels, want) {
		t.Errorf("updated firewall %q with labels %v, want %q with %v", got.Name, got.Labels, renamed, want)
	}

	d.run(t, "destroy", "-auto-approve", "-no-color")
	assertGoneIDs(t, e, "/firewalls", "firewalls", id)
}

// testCLI03Firewalls: firewall and firewall_resource create, change rules,
// apply, remove and delete; firewall_info finds the firewall.
func testCLI03Firewalls(t *testing.T, c *rowCtx) {
	e, w := sharedAnsible(t)
	fx := c.Fixture
	name := resName("ansible", "firewalls")
	selector := fixtureLabel + "=ansible"
	var id int64
	t.Cleanup(func() { e.deleteFirewallByID(t, id) })
	row := map[string]any{"name": name, "server": fx.Name, "selector": selector}
	firewall := func(stage, port string) string {
		return `    - name: firewall
      hetzner.hcloud.firewall:
        name: "{{ row.name }}"
        labels:
          row: cli-03-firewalls
          stage: ` + stage + `
        rules:
          - direction: in
            protocol: tcp
            port: "22"
            source_ips: ["0.0.0.0/0", "::/0"]
          - direction: in
            protocol: tcp
            port: "` + port + `"
            source_ips: ["0.0.0.0/0"]
        state: present
`
	}
	resource := func(state string) string {
		return `    - name: firewall resources
      hetzner.hcloud.firewall_resource:
        firewall: "{{ row.name }}"
        servers: ["{{ row.server }}"]
        label_selectors: ["{{ row.selector }}"]
        state: ` + state + `
`
	}
	applied := []string{fmt.Sprintf("server:%d", fx.ID), "label_selector:" + selector}

	e.ansibleOK(t, w, "cli03-firewalls-create", localPlay(t, row, firewall("one", "80")))
	var described firewallDetail
	e.hcloudJSON(t, &described, "firewall", "describe", name)
	id = described.ID
	got := e.wantFirewall(t, "firewall", id, []string{sshRuleIn, "in/tcp/80"}, nil)
	if want := map[string]string{"row": "cli-03-firewalls", "stage": "one"}; !maps.Equal(got.Labels, want) {
		t.Errorf("firewall labels %v, want %v", got.Labels, want)
	}

	e.ansibleOK(t, w, "cli03-firewalls-apply", localPlay(t, row, firewall("two", "443")+resource("present")+`    - name: firewall info
      hetzner.hcloud.firewall_info:
        name: "{{ row.name }}"
      register: info
    - name: check the answer
      ansible.builtin.assert:
        that:
          - info.hcloud_firewall_info | length == 1
          - info.hcloud_firewall_info[0].rules | length == 2
          - info.hcloud_firewall_info[0].applied_to | length == 2
          - info.hcloud_firewall_info[0].labels.stage == "two"
`))
	e.wantFirewall(t, "rules changed and firewall_resource", id, []string{sshRuleIn, "in/tcp/443"}, applied)

	e.ansibleOK(t, w, "cli03-firewalls-remove", localPlay(t, row, resource("absent")))
	e.wantFirewall(t, "firewall_resource absent", id, []string{sshRuleIn, "in/tcp/443"}, nil)

	e.ansibleOK(t, w, "cli03-firewalls-absent", localPlay(t, row, `    - name: firewall absent
      hetzner.hcloud.firewall:
        name: "{{ row.name }}"
        state: absent
`))
	assertGoneIDs(t, e, "/firewalls", "firewalls", id)
}
