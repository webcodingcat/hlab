//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-01", Name: "primary_ips", Fixture: true, Run: testCLI01PrimaryIPs})
	registerRow(clientRow{Req: "CLI-02", Name: "primary_ips", Fixture: true, Run: testCLI02PrimaryIPs})
	registerRow(clientRow{Req: "CLI-03", Name: "primary_ips", Fixture: true, Run: testCLI03PrimaryIPs})
}

// ipDetail is what the client rows check of a primary or floating IP.
type ipDetail struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	IP          string            `json:"ip"`
	Description *string           `json:"description"`
	Labels      map[string]string `json:"labels"`
	AutoDelete  bool              `json:"auto_delete"`
	AssigneeID  *int64            `json:"assignee_id"`
	Server      *int64            `json:"server"`
	Location    *struct {
		Name string `json:"name"`
	} `json:"location"`
	HomeLocation *struct {
		Name string `json:"name"`
	} `json:"home_location"`
	DNSPtr []struct {
		IP     string `json:"ip"`
		DNSPtr string `json:"dns_ptr"`
	} `json:"dns_ptr"`
	Protection struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
}

// ptr is the reverse DNS entry of ip, "" if there is none.
func (d ipDetail) ptr(ip string) string {
	for _, p := range d.DNSPtr {
		if p.IP == ip {
			return p.DNSPtr
		}
	}
	return ""
}

func (e *env) primaryIPInfo(t *testing.T, id int64) ipDetail {
	t.Helper()
	var body struct {
		PrimaryIP ipDetail `json:"primary_ip"`
	}
	e.getOK(t, fmt.Sprintf("/primary_ips/%d", id), &body)
	return body.PrimaryIP
}

// ptrAddress is the address a reverse DNS entry of an IP is set for: the
// IPv4 itself, or the first address after the network of an IPv6 /64.
func ptrAddress(t *testing.T, ip string) string {
	t.Helper()
	if !strings.Contains(ip, "/") {
		return ip
	}
	p, err := netip.ParsePrefix(ip)
	if err != nil {
		t.Fatalf("IP %q: %v", ip, err)
	}
	return p.Masked().Addr().Next().String()
}

// serverIPv6ID is the ID of the server's primary IPv6, 0 if it has none.
func (e *env) serverIPv6ID(id int64) (int64, error) {
	var body struct {
		Server struct {
			Status    string `json:"status"`
			PublicNet struct {
				IPv6 *struct {
					ID int64 `json:"id"`
				} `json:"ipv6"`
			} `json:"public_net"`
		} `json:"server"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, &body)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("GET /servers/%d: status %d %s", id, status, aerr.Code)
	}
	if body.Server.PublicNet.IPv6 == nil {
		return 0, nil
	}
	return body.Server.PublicNet.IPv6.ID, nil
}

// freeFixtureIPv6 powers the fixture server off and unassigns its own
// primary IPv6, so the row can assign another one. It returns a function
// that gives the fixture its own IPv6 back and powers it on; the row calls
// it, and the cleanup calls it again if the row ended before.
func (e *env) freeFixtureIPv6(t *testing.T, fx *server) func() {
	t.Helper()
	own, err := e.serverIPv6ID(fx.ID)
	if err != nil || own == 0 {
		t.Fatalf("fixture server %d has no primary IPv6 (%v)", fx.ID, err)
	}
	restored := false
	restore := func() {
		t.Helper()
		if restored {
			return
		}
		restored = true
		cur, err := e.serverIPv6ID(fx.ID)
		if err != nil {
			t.Errorf("restore the fixture: %v", err)
			return
		}
		if cur != own {
			// A client may have powered the server on again (the
			// provider does after assigning), and assign needs it off.
			if s, _ := e.serverStatus(fx.ID); s != "off" {
				if err := e.postAction(fmt.Sprintf("/servers/%d/actions/poweroff", fx.ID), nil, 5*time.Minute); err != nil {
					t.Errorf("restore the fixture: power off: %v", err)
				}
				if err := e.waitStatus(fx.ID, "off", 2*time.Minute); err != nil {
					t.Errorf("restore the fixture: %v", err)
				}
			}
			if cur != 0 {
				if err := e.postAction(fmt.Sprintf("/primary_ips/%d/actions/unassign", cur), nil, 5*time.Minute); err != nil {
					t.Errorf("restore the fixture: unassign primary IP %d: %v", cur, err)
				}
			}
			if err := e.postAction(fmt.Sprintf("/primary_ips/%d/actions/assign", own),
				map[string]any{"assignee_id": fx.ID, "assignee_type": "server"}, 5*time.Minute); err != nil {
				t.Errorf("restore the fixture: assign its IPv6 %d: %v", own, err)
			}
		}
		if s, _ := e.serverStatus(fx.ID); s != "running" {
			if err := e.postAction(fmt.Sprintf("/servers/%d/actions/poweron", fx.ID), nil, 5*time.Minute); err != nil {
				t.Errorf("restore the fixture: power on: %v", err)
			}
		}
		if err := e.waitStatus(fx.ID, "running", 5*time.Minute); err != nil {
			t.Error(err)
			return
		}
		e.waitSSH(t, fx, 10*time.Minute)
	}
	t.Cleanup(restore)
	e.powerOff(t, fx)
	e.runAPIAction(t, fmt.Sprintf("/primary_ips/%d/actions/unassign", own), nil, 5*time.Minute)
	return restore
}

// wantAssigned fails unless the primary IP is the fixture's IPv6 (on) or
// is unassigned (off).
func (e *env) wantAssigned(t *testing.T, step string, id int64, fx *server, on bool) {
	t.Helper()
	got := e.primaryIPInfo(t, id)
	cur, err := e.serverIPv6ID(fx.ID)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case on && (got.AssigneeID == nil || *got.AssigneeID != fx.ID || cur != id):
		t.Errorf("%s: primary IP %d assignee %v, server IPv6 %d; want both %d and %d", step, id, got.AssigneeID, cur, fx.ID, id)
	case !on && (got.AssigneeID != nil || cur == id):
		t.Errorf("%s: primary IP %d still assigned (assignee %v, server IPv6 %d)", step, id, got.AssigneeID, cur)
	}
}

// deletePrimaryIP deletes a primary IP at the end of a row; one already
// gone is no error.
func (e *env) deletePrimaryIP(t *testing.T, id int64) {
	t.Helper()
	if id == 0 {
		return
	}
	path := fmt.Sprintf("/primary_ips/%d", id)
	_, _, _ = e.apiDo(context.Background(), http.MethodPost, path+"/actions/change_protection", map[string]any{"delete": false}, nil)
	err := waitFor(5*time.Minute, 3*time.Second, func() (bool, error) {
		status, _, err := e.apiDo(context.Background(), http.MethodDelete, path, nil, nil)
		return err == nil && (status == http.StatusNoContent || status == http.StatusOK || status == http.StatusNotFound), nil
	})
	if err != nil {
		t.Errorf("DELETE %s: %v", path, err)
	}
}

// testCLI01PrimaryIPs: hcloud creates, updates, labels, protects a primary
// IP, sets its reverse DNS, assigns it to the powered-off fixture server,
// unassigns and deletes it.
func testCLI01PrimaryIPs(t *testing.T, c *rowCtx) {
	e, fx := c.E, c.Fixture
	name := resName("hcloud", "primary-ips")
	renamed := name + "-renamed"
	var id int64
	t.Cleanup(func() { e.deletePrimaryIP(t, id) })

	e.hcloudOK(t, "primary-ip", "create", "--type", "ipv6", "--name", name, "--location", serverLocation, "--label", "row=cli-01-primary-ips")
	var listed []ipDetail
	e.hcloudJSON(t, &listed, "primary-ip", "list", "-l", "row=cli-01-primary-ips")
	if len(listed) != 1 || listed[0].Name != name {
		t.Fatalf("hcloud primary-ip list -l row=cli-01-primary-ips: %+v, want only %s", listed, name)
	}
	id = listed[0].ID
	got := e.primaryIPInfo(t, id)
	if got.Type != "ipv6" || got.Location == nil || got.Location.Name != serverLocation || got.AssigneeID != nil {
		t.Errorf("created primary IP %+v, want an unassigned ipv6 in %s", got, serverLocation)
	}
	var described ipDetail
	e.hcloudJSON(t, &described, "primary-ip", "describe", name)
	if described.ID != id || described.IP != got.IP {
		t.Errorf("hcloud primary-ip describe %s: %+v, the API has %+v", name, described, got)
	}

	e.hcloudOK(t, "primary-ip", "update", "--name", renamed, "--auto-delete=true", name)
	e.hcloudOK(t, "primary-ip", "add-label", renamed, "stage=two")
	e.hcloudOK(t, "primary-ip", "remove-label", renamed, "row")
	got = e.primaryIPInfo(t, id)
	if want := map[string]string{"stage": "two"}; got.Name != renamed || !got.AutoDelete || !maps.Equal(got.Labels, want) {
		t.Errorf("updated primary IP %q, auto_delete %t, labels %v; want %q, true, %v", got.Name, got.AutoDelete, got.Labels, renamed, want)
	}
	e.hcloudOK(t, "primary-ip", "update", "--auto-delete=false", renamed)
	if e.primaryIPInfo(t, id).AutoDelete {
		t.Error("auto_delete still on after update --auto-delete=false")
	}
	e.hcloudOK(t, "primary-ip", "enable-protection", renamed, "delete")
	if !e.primaryIPInfo(t, id).Protection.Delete {
		t.Error("delete protection off after enable-protection")
	}
	e.hcloudOK(t, "primary-ip", "disable-protection", renamed, "delete")
	if e.primaryIPInfo(t, id).Protection.Delete {
		t.Error("delete protection on after disable-protection")
	}

	addr := ptrAddress(t, got.IP)
	ptr := "pip." + zoneName("hcloud-rdns")
	e.hcloudOK(t, "primary-ip", "set-rdns", "--ip", addr, "--hostname", ptr, renamed)
	if p := e.primaryIPInfo(t, id).ptr(addr); p != ptr {
		t.Errorf("dns_ptr of %s after set-rdns: %q, want %q", addr, p, ptr)
	}

	restore := e.freeFixtureIPv6(t, fx)
	e.hcloudOK(t, "primary-ip", "assign", "--server", fx.Name, renamed)
	e.wantAssigned(t, "primary-ip assign", id, fx, true)
	e.hcloudOK(t, "primary-ip", "unassign", renamed)
	e.wantAssigned(t, "primary-ip unassign", id, fx, false)
	restore()

	e.hcloudOK(t, "primary-ip", "delete", renamed)
	assertGoneIDs(t, e, "/primary_ips", "primary_ips", id)
}

// testCLI02PrimaryIPs: OpenTofu manages hcloud_primary_ip with an assignee,
// updates it in place, reads hcloud_primary_ip and hcloud_primary_ips, and
// destroys it.
func testCLI02PrimaryIPs(t *testing.T, c *rowCtx) {
	e, fx := sharedTofu(t), c.Fixture
	d := e.newTofuDir(t, "primary-ips")
	main := func(name, stage string) string {
		return fmt.Sprintf(`resource "hcloud_primary_ip" "p" {
  name          = %q
  type          = "ipv6"
  assignee_type = "server"
  assignee_id   = %d
  auto_delete   = false
  labels = {
    row   = "cli-02-primary-ips"
    stage = %q
  }
}

data "hcloud_primary_ip" "by_id" {
  id         = hcloud_primary_ip.p.id
  depends_on = [hcloud_primary_ip.p]
}

data "hcloud_primary_ips" "by_label" {
  with_selector = "row=cli-02-primary-ips"
  depends_on    = [hcloud_primary_ip.p]
}

output "id" {
  value = hcloud_primary_ip.p.id
}

output "by_id_assignee" {
  value = data.hcloud_primary_ip.by_id.assignee_id
}

output "label_ids" {
  value = jsonencode(data.hcloud_primary_ips.by_label.primary_ips[*].id)
}
`, name, fx.ID, stage)
	}
	d.run(t, "init", "-no-color")

	var id int64
	t.Cleanup(func() { e.deletePrimaryIP(t, id) })
	restore := e.freeFixtureIPv6(t, fx)

	name := resName("tofu", "primary-ips")
	d.writeMain(t, main(name, "one"))
	d.run(t, "apply", "-auto-approve", "-no-color")
	idText := d.output(t, "id")
	id = parseID(t, idText)
	got := e.primaryIPInfo(t, id)
	if want := map[string]string{"row": "cli-02-primary-ips", "stage": "one"}; got.Name != name || got.Type != "ipv6" || got.AutoDelete || !maps.Equal(got.Labels, want) {
		t.Errorf("primary IP %+v, want ipv6 %s without auto_delete with %v", got, name, want)
	}
	e.wantAssigned(t, "hcloud_primary_ip", id, fx, true)
	if n, _ := d.dataValues(t, "data.hcloud_primary_ip.by_id")["name"].(string); n != name {
		t.Errorf("data hcloud_primary_ip by ID: name %q, want %q", n, name)
	}
	if a := d.output(t, "by_id_assignee"); a != fmt.Sprint(fx.ID) {
		t.Errorf("data hcloud_primary_ip by ID: assignee %s, want %d", a, fx.ID)
	}
	if ids := d.output(t, "label_ids"); ids != "["+idText+"]" {
		t.Errorf("data hcloud_primary_ips with row=cli-02-primary-ips: IDs %s, want [%s]", ids, idText)
	}

	renamed := name + "-renamed"
	d.writeMain(t, main(renamed, "two"))
	r := d.run(t, "apply", "-auto-approve", "-no-color")
	if !strings.Contains(r.Stdout, updatedInPlace) {
		t.Errorf("second apply is no in-place update (want %q):\n%s", updatedInPlace, r.Stdout)
	}
	got = e.primaryIPInfo(t, id)
	if want := map[string]string{"row": "cli-02-primary-ips", "stage": "two"}; got.Name != renamed || !maps.Equal(got.Labels, want) {
		t.Errorf("updated primary IP %q with labels %v, want %q with %v", got.Name, got.Labels, renamed, want)
	}

	d.run(t, "destroy", "-auto-approve", "-no-color")
	assertGoneIDs(t, e, "/primary_ips", "primary_ips", id)
	restore()
}

// testCLI03PrimaryIPs: primary_ip creates, assigns, updates and deletes a
// primary IP; primary_ip_info finds it.
func testCLI03PrimaryIPs(t *testing.T, c *rowCtx) {
	e, w := sharedAnsible(t)
	fx := c.Fixture
	name := resName("ansible", "primary-ips")
	var id int64
	t.Cleanup(func() { e.deletePrimaryIP(t, id) })
	row := map[string]any{"name": name, "location": serverLocation, "server": fx.Name, "server_id": fx.ID}

	e.ansibleOK(t, w, "cli03-primary-ips-create", localPlay(t, row, `    - name: primary IP
      hetzner.hcloud.primary_ip:
        name: "{{ row.name }}"
        type: ipv6
        location: "{{ row.location }}"
        auto_delete: false
        labels:
          row: cli-03-primary-ips
          stage: one
        state: present
`))
	var described ipDetail
	e.hcloudJSON(t, &described, "primary-ip", "describe", name)
	id = described.ID
	got := e.primaryIPInfo(t, id)
	if want := map[string]string{"row": "cli-03-primary-ips", "stage": "one"}; got.Name != name || got.Type != "ipv6" ||
		got.Location == nil || got.Location.Name != serverLocation || got.AssigneeID != nil || !maps.Equal(got.Labels, want) {
		t.Errorf("created primary IP %+v, want an unassigned ipv6 %s in %s with %v", got, name, serverLocation, want)
	}

	e.ansibleOK(t, w, "cli03-primary-ips-update", localPlay(t, row, `    - name: primary IP updated
      hetzner.hcloud.primary_ip:
        name: "{{ row.name }}"
        type: ipv6
        location: "{{ row.location }}"
        auto_delete: true
        delete_protection: true
        labels:
          row: cli-03-primary-ips
          stage: two
        state: present
`))
	got = e.primaryIPInfo(t, id)
	if want := map[string]string{"row": "cli-03-primary-ips", "stage": "two"}; !got.AutoDelete || !got.Protection.Delete || !maps.Equal(got.Labels, want) {
		t.Errorf("updated primary IP: auto_delete %t, delete protection %t, labels %v; want true, true, %v", got.AutoDelete, got.Protection.Delete, got.Labels, want)
	}

	restore := e.freeFixtureIPv6(t, fx)
	e.ansibleOK(t, w, "cli03-primary-ips-assign", localPlay(t, row, `    - name: primary IP assigned
      hetzner.hcloud.primary_ip:
        name: "{{ row.name }}"
        type: ipv6
        server: "{{ row.server }}"
        state: present
    - name: primary IP info
      hetzner.hcloud.primary_ip_info:
        name: "{{ row.name }}"
      register: info
    - name: check the answer
      ansible.builtin.assert:
        that:
          - info.hcloud_primary_ip_info | length == 1
          - info.hcloud_primary_ip_info[0].assignee_id | int == row.server_id
          - info.hcloud_primary_ip_info[0].labels.stage == "two"
`))
	e.wantAssigned(t, "primary_ip with server", id, fx, true)

	e.ansibleOK(t, w, "cli03-primary-ips-unassign", localPlay(t, row, `    - name: primary IP unassigned and unprotected
      hetzner.hcloud.primary_ip:
        name: "{{ row.name }}"
        type: ipv6
        server: null
        delete_protection: false
        state: present
`))
	e.wantAssigned(t, "primary_ip with server null", id, fx, false)
	if e.primaryIPInfo(t, id).Protection.Delete {
		t.Error("delete protection on after delete_protection: false")
	}
	restore()

	e.ansibleOK(t, w, "cli03-primary-ips-absent", localPlay(t, row, `    - name: primary IP absent
      hetzner.hcloud.primary_ip:
        name: "{{ row.name }}"
        state: absent
`))
	assertGoneIDs(t, e, "/primary_ips", "primary_ips", id)
}
