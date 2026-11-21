//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"
)

func init() {
	registerRow(clientRow{Req: "CLI-01", Name: "floating_ips", Fixture: true, Run: testCLI01FloatingIPs})
	registerRow(clientRow{Req: "CLI-02", Name: "floating_ips", Fixture: true, Run: testCLI02FloatingIPs})
	registerRow(clientRow{Req: "CLI-03", Name: "floating_ips", Fixture: true, Run: testCLI03FloatingIPs})
	registerRow(clientRow{Req: "CLI-02", Name: "rdns", Fixture: true, Run: testCLI02RDNS})
	registerRow(clientRow{Req: "CLI-03", Name: "rdns", Fixture: true, Run: testCLI03RDNS})
}

func (e *env) floatingIPInfo(t *testing.T, id int64) ipDetail {
	t.Helper()
	var body struct {
		FloatingIP ipDetail `json:"floating_ip"`
	}
	e.getOK(t, fmt.Sprintf("/floating_ips/%d", id), &body)
	return body.FloatingIP
}

// deleteFloatingIPUnprotected turns off the delete protection of a floating
// IP a row made and deletes it; 0 is no floating IP.
func (e *env) deleteFloatingIPUnprotected(t *testing.T, id int64) {
	t.Helper()
	if id == 0 {
		return
	}
	path := fmt.Sprintf("/floating_ips/%d/actions/change_protection", id)
	_, _, _ = e.apiDo(context.Background(), http.MethodPost, path, map[string]any{"delete": false}, nil)
	e.deleteFloatingIP(t, id)
}

// wantFloatingIP fails unless the floating IP has the name, description,
// labels and server (nil: unassigned).
func (e *env) wantFloatingIP(t *testing.T, step string, id int64, name, description string, labels map[string]string, server *int64) ipDetail {
	t.Helper()
	got := e.floatingIPInfo(t, id)
	desc := ""
	if got.Description != nil {
		desc = *got.Description
	}
	sameServer := (got.Server == nil) == (server == nil) && (server == nil || *got.Server == *server)
	if got.Name != name || desc != description || !maps.Equal(got.Labels, labels) || !sameServer {
		t.Errorf("%s: floating IP %q, description %q, labels %v, server %v; want %q, %q, %v, %v",
			step, got.Name, desc, got.Labels, got.Server, name, description, labels, server)
	}
	return got
}

// testCLI01FloatingIPs: hcloud creates, updates, labels, protects a
// floating IP, sets its reverse DNS, assigns it to the fixture server,
// unassigns and deletes it.
func testCLI01FloatingIPs(t *testing.T, c *rowCtx) {
	e, fx := c.E, c.Fixture
	name := resName("hcloud", "floating-ips")
	renamed := name + "-renamed"
	var id int64
	t.Cleanup(func() { e.deleteFloatingIPUnprotected(t, id) })

	e.hcloudOK(t, "floating-ip", "create", "--type", "ipv4", "--home-location", serverLocation, "--name", name,
		"--description", "one", "--label", "row=cli-01-floating-ips")
	var listed []ipDetail
	e.hcloudJSON(t, &listed, "floating-ip", "list", "-l", "row=cli-01-floating-ips")
	if len(listed) != 1 || listed[0].Name != name {
		t.Fatalf("hcloud floating-ip list -l row=cli-01-floating-ips: %+v, want only %s", listed, name)
	}
	id = listed[0].ID
	got := e.wantFloatingIP(t, "create", id, name, "one", map[string]string{"row": "cli-01-floating-ips"}, nil)
	if got.Type != "ipv4" || got.HomeLocation == nil || got.HomeLocation.Name != serverLocation {
		t.Errorf("created floating IP %+v, want ipv4 at home in %s", got, serverLocation)
	}
	var described ipDetail
	e.hcloudJSON(t, &described, "floating-ip", "describe", name)
	if described.ID != id || described.IP != got.IP {
		t.Errorf("hcloud floating-ip describe %s: %+v, the API has %+v", name, described, got)
	}

	e.hcloudOK(t, "floating-ip", "update", "--name", renamed, "--description", "two", name)
	e.hcloudOK(t, "floating-ip", "add-label", renamed, "stage=two")
	e.hcloudOK(t, "floating-ip", "remove-label", renamed, "row")
	e.wantFloatingIP(t, "update and labels", id, renamed, "two", map[string]string{"stage": "two"}, nil)
	e.hcloudOK(t, "floating-ip", "enable-protection", renamed, "delete")
	if !e.floatingIPInfo(t, id).Protection.Delete {
		t.Error("delete protection off after enable-protection")
	}
	e.hcloudOK(t, "floating-ip", "disable-protection", renamed, "delete")
	if e.floatingIPInfo(t, id).Protection.Delete {
		t.Error("delete protection on after disable-protection")
	}

	ptr := "fip." + zoneName("hcloud-rdns")
	e.hcloudOK(t, "floating-ip", "set-rdns", "--ip", got.IP, "--hostname", ptr, renamed)
	if p := e.floatingIPInfo(t, id).ptr(got.IP); p != ptr {
		t.Errorf("dns_ptr of %s after set-rdns: %q, want %q", got.IP, p, ptr)
	}

	e.hcloudOK(t, "floating-ip", "assign", renamed, fx.Name)
	e.wantFloatingIP(t, "assign", id, renamed, "two", map[string]string{"stage": "two"}, &fx.ID)
	e.hcloudOK(t, "floating-ip", "unassign", renamed)
	e.wantFloatingIP(t, "unassign", id, renamed, "two", map[string]string{"stage": "two"}, nil)

	e.hcloudOK(t, "floating-ip", "delete", renamed)
	assertGoneIDs(t, e, "/floating_ips", "floating_ips", id)
}

// testCLI02FloatingIPs: OpenTofu manages hcloud_floating_ip and
// hcloud_floating_ip_assignment to the fixture server, updates them, reads
// hcloud_floating_ip and hcloud_floating_ips, and destroys them.
func testCLI02FloatingIPs(t *testing.T, c *rowCtx) {
	e, fx := sharedTofu(t), c.Fixture
	d := e.newTofuDir(t, "floating-ips")
	main := func(name, description, stage string) string {
		return fmt.Sprintf(`resource "hcloud_floating_ip" "f" {
  name          = %q
  description   = %q
  type          = "ipv4"
  home_location = %q
  labels = {
    row   = "cli-02-floating-ips"
    stage = %q
  }
}

resource "hcloud_floating_ip_assignment" "a" {
  floating_ip_id = hcloud_floating_ip.f.id
  server_id      = %d
}

data "hcloud_floating_ip" "by_id" {
  id         = hcloud_floating_ip.f.id
  depends_on = [hcloud_floating_ip.f, hcloud_floating_ip_assignment.a]
}

data "hcloud_floating_ips" "by_label" {
  with_selector = "row=cli-02-floating-ips"
  depends_on    = [hcloud_floating_ip.f]
}

output "id" {
  value = hcloud_floating_ip.f.id
}

output "by_id_description" {
  value = data.hcloud_floating_ip.by_id.description
}

output "by_id_server" {
  value = data.hcloud_floating_ip.by_id.server_id
}

output "label_ids" {
  value = jsonencode(data.hcloud_floating_ips.by_label.floating_ips[*].id)
}
`, name, description, serverLocation, stage, fx.ID)
	}
	d.run(t, "init", "-no-color")

	var id int64
	t.Cleanup(func() { e.deleteFloatingIPUnprotected(t, id) })
	name := resName("tofu", "floating-ips")
	d.writeMain(t, main(name, "one", "one"))
	d.run(t, "apply", "-auto-approve", "-no-color")
	idText := d.output(t, "id")
	id = parseID(t, idText)
	e.wantFloatingIP(t, "apply", id, name, "one", map[string]string{"row": "cli-02-floating-ips", "stage": "one"}, &fx.ID)
	if s := d.output(t, "by_id_server"); s != fmt.Sprint(fx.ID) {
		t.Errorf("data hcloud_floating_ip by ID: server %s, want %d", s, fx.ID)
	}
	if ids := d.output(t, "label_ids"); ids != "["+idText+"]" {
		t.Errorf("data hcloud_floating_ips with row=cli-02-floating-ips: IDs %s, want [%s]", ids, idText)
	}

	renamed := name + "-renamed"
	d.writeMain(t, main(renamed, "two", "two"))
	r := d.run(t, "apply", "-auto-approve", "-no-color")
	if !strings.Contains(r.Stdout, updatedInPlace) {
		t.Errorf("second apply is no in-place update (want %q):\n%s", updatedInPlace, r.Stdout)
	}
	e.wantFloatingIP(t, "update", id, renamed, "two", map[string]string{"row": "cli-02-floating-ips", "stage": "two"}, &fx.ID)
	if desc := d.output(t, "by_id_description"); desc != "two" {
		t.Errorf("data hcloud_floating_ip by ID after the update: description %q, want two", desc)
	}

	d.run(t, "destroy", "-auto-approve", "-no-color")
	assertGoneIDs(t, e, "/floating_ips", "floating_ips", id)
}

// testCLI03FloatingIPs: floating_ip creates, assigns, updates and deletes a
// floating IP; floating_ip_info finds it.
func testCLI03FloatingIPs(t *testing.T, c *rowCtx) {
	e, w := sharedAnsible(t)
	fx := c.Fixture
	name := resName("ansible", "floating-ips")
	var id int64
	t.Cleanup(func() { e.deleteFloatingIPUnprotected(t, id) })
	row := map[string]any{"name": name, "location": serverLocation, "server": fx.Name}

	e.ansibleOK(t, w, "cli03-floating-ips-create", localPlay(t, row, `    - name: floating IP
      hetzner.hcloud.floating_ip:
        name: "{{ row.name }}"
        type: ipv4
        home_location: "{{ row.location }}"
        description: one
        labels:
          row: cli-03-floating-ips
          stage: one
        state: present
`))
	var described ipDetail
	e.hcloudJSON(t, &described, "floating-ip", "describe", name)
	id = described.ID
	e.wantFloatingIP(t, "create", id, name, "one", map[string]string{"row": "cli-03-floating-ips", "stage": "one"}, nil)

	e.ansibleOK(t, w, "cli03-floating-ips-assign", localPlay(t, row, `    - name: floating IP assigned and updated
      hetzner.hcloud.floating_ip:
        name: "{{ row.name }}"
        type: ipv4
        server: "{{ row.server }}"
        description: two
        delete_protection: true
        labels:
          row: cli-03-floating-ips
          stage: two
        state: present
    - name: floating IP info
      hetzner.hcloud.floating_ip_info:
        name: "{{ row.name }}"
      register: info
    - name: check the answer
      ansible.builtin.assert:
        that:
          - info.hcloud_floating_ip_info | length == 1
          - info.hcloud_floating_ip_info[0].server == row.server
          - info.hcloud_floating_ip_info[0].description == "two"
          - info.hcloud_floating_ip_info[0].delete_protection
`))
	e.wantFloatingIP(t, "assign and update", id, name, "two", map[string]string{"row": "cli-03-floating-ips", "stage": "two"}, &fx.ID)
	if !e.floatingIPInfo(t, id).Protection.Delete {
		t.Error("delete protection off after delete_protection: true")
	}

	e.ansibleOK(t, w, "cli03-floating-ips-absent", localPlay(t, row, `    - name: floating IP unprotected
      hetzner.hcloud.floating_ip:
        name: "{{ row.name }}"
        type: ipv4
        server: "{{ row.server }}"
        delete_protection: false
        state: present
    - name: floating IP absent
      hetzner.hcloud.floating_ip:
        name: "{{ row.name }}"
        force: true
        state: absent
`))
	assertGoneIDs(t, e, "/floating_ips", "floating_ips", id)
}

// rdnsCase is one owner of a reverse DNS entry in the rdns rows: the
// fixture server, a floating IP or a primary IP.
type rdnsCase struct {
	name string // subtest name
	attr string // the owner argument of hcloud_rdns and of the rdns module
	ref  string // the owner's ID (tofu) or name (Ansible)
	ip   string
	ptr  func() string
}

// rdnsCases creates a floating IP and a primary IP through the API for the
// rdns row of client and returns the three cases with their owners.
func (e *env) rdnsCases(t *testing.T, client string, fx *server, byName bool) []rdnsCase {
	t.Helper()
	var fip struct {
		FloatingIP ipDetail `json:"floating_ip"`
	}
	e.post(t, "/floating_ips", map[string]any{
		"type": "ipv4", "home_location": serverLocation, "name": resName(client, "rdns-fip"),
	}, &fip)
	fipID := fip.FloatingIP.ID
	t.Cleanup(func() { e.deleteFloatingIP(t, fipID) })
	var pip struct {
		PrimaryIP ipDetail `json:"primary_ip"`
	}
	e.post(t, "/primary_ips", map[string]any{
		"type": "ipv4", "location": serverLocation, "assignee_type": "server", "name": resName(client, "rdns-pip"),
	}, &pip)
	pipID := pip.PrimaryIP.ID
	t.Cleanup(func() { e.deletePrimaryIP(t, pipID) })

	ref := func(id int64, name string) string {
		if byName {
			return name
		}
		return fmt.Sprint(id)
	}
	return []rdnsCase{
		{name: "server", attr: "server", ref: ref(fx.ID, fx.Name), ip: fx.IPv4, ptr: func() string {
			if p := e.serverInfo(t, fx.ID).PublicNet.IPv4; p != nil {
				return p.DNSPtr
			}
			return ""
		}},
		{name: "floating_ip", attr: "floating_ip", ref: ref(fipID, fip.FloatingIP.Name), ip: fip.FloatingIP.IP, ptr: func() string {
			return e.floatingIPInfo(t, fipID).ptr(fip.FloatingIP.IP)
		}},
		{name: "primary_ip", attr: "primary_ip", ref: ref(pipID, pip.PrimaryIP.Name), ip: pip.PrimaryIP.IP, ptr: func() string {
			return e.primaryIPInfo(t, pipID).ptr(pip.PrimaryIP.IP)
		}},
	}
}

// testCLI02RDNS: hcloud_rdns sets, changes and on destroy resets the PTR of
// the fixture server, a floating IP and a primary IP.
func testCLI02RDNS(t *testing.T, c *rowCtx) {
	e := sharedTofu(t)
	for _, tc := range e.rdnsCases(t, "tofu", c.Fixture, false) {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.ptr()
			d := e.newTofuDir(t, "rdns-"+strings.ReplaceAll(tc.name, "_", "-"))
			main := func(ptr string) string {
				return fmt.Sprintf(`resource "hcloud_rdns" "r" {
  %s_id      = %s
  ip_address = %q
  dns_ptr    = %q
}
`, tc.attr, tc.ref, tc.ip, ptr)
			}
			d.run(t, "init", "-no-color")
			for i, stage := range []string{"one", "two"} {
				ptr := strings.ReplaceAll(tc.name, "_", "-") + "-" + stage + "." + zoneName("tofu-rdns")
				d.writeMain(t, main(ptr))
				r := d.run(t, "apply", "-auto-approve", "-no-color")
				if i == 1 && !strings.Contains(r.Stdout, updatedInPlace) {
					t.Errorf("second apply is no in-place update (want %q):\n%s", updatedInPlace, r.Stdout)
				}
				if got := tc.ptr(); got != ptr {
					t.Errorf("PTR of %s after apply %s: %q, want %q", tc.ip, stage, got, ptr)
				}
			}
			d.run(t, "destroy", "-auto-approve", "-no-color")
			if got := tc.ptr(); got != before {
				t.Errorf("PTR of %s after destroy: %q, want the default %q", tc.ip, got, before)
			}
		})
	}
}

// testCLI03RDNS: the rdns module sets, changes and removes the PTR of the
// fixture server, a floating IP and a primary IP.
func testCLI03RDNS(t *testing.T, c *rowCtx) {
	e, w := sharedAnsible(t)
	for _, tc := range e.rdnsCases(t, "ansible", c.Fixture, true) {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.ptr()
			row := map[string]any{"ref": tc.ref, "ip": tc.ip}
			task := func(ptr string) string {
				state, line := "present", "        dns_ptr: "+ptr+"\n"
				if ptr == "" {
					state, line = "absent", ""
				}
				return `    - name: rdns
      hetzner.hcloud.rdns:
        ` + tc.attr + `: "{{ row.ref }}"
        ip_address: "{{ row.ip }}"
` + line + `        state: ` + state + `
`
			}
			for _, stage := range []string{"one", "two"} {
				ptr := strings.ReplaceAll(tc.name, "_", "-") + "-" + stage + "." + zoneName("ansible-rdns")
				e.ansibleOK(t, w, "cli03-rdns-"+tc.name+"-"+stage, localPlay(t, row, task(ptr)))
				if got := tc.ptr(); got != ptr {
					t.Errorf("PTR of %s after rdns %s: %q, want %q", tc.ip, stage, got, ptr)
				}
			}
			e.ansibleOK(t, w, "cli03-rdns-"+tc.name+"-absent", localPlay(t, row, task("")))
			if got := tc.ptr(); got != before {
				t.Errorf("PTR of %s after rdns absent: %q, want the default %q", tc.ip, got, before)
			}
		})
	}
}
