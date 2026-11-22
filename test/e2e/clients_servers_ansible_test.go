//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"maps"
	"os"
	"strings"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-03", Name: "servers", Slots: 1, Run: testCLI03Servers})
}

// testCLI03Servers: the server module creates, stops, starts, restarts,
// rebuilds, puts into rescue and back, changes type and deletes a server;
// server_info finds it without filter, by name, ID and label selector.
// hetzner.hcloud 7.2.0 has no option to attach an ISO.
func testCLI03Servers(t *testing.T, _ *rowCtx) {
	e, w := sharedAnsible(t)
	name, pg := resName("ansible", "servers"), resName("ansible", "servers-pg")
	t.Cleanup(func() {
		_, _ = e.hcloudRun(context.Background(), 5*time.Minute, "server", "disable-protection", name, "delete", "rebuild")
		deleteIfCreated(t, e, name)
		_, _ = e.hcloudRun(context.Background(), 5*time.Minute, "placement-group", "delete", pg)
	})
	userData, err := os.ReadFile(e.userData)
	if err != nil {
		t.Fatal(err)
	}
	_, big := largerType(t, e)
	row := map[string]any{
		"name": name, "pg": pg, "type": serverType, "big": big.Name, "image": serverImage,
		"location": serverLocation, "key": e.sshKey, "user_data": string(userData),
	}
	step := func(label, tasks string) {
		t.Helper()
		e.ansibleOK(t, w, "cli03-servers-"+label, localPlay(t, row, tasks))
	}

	step("create", `    - name: placement group
      hetzner.hcloud.placement_group:
        name: "{{ row.pg }}"
        type: spread
        state: present
    - name: server
      hetzner.hcloud.server:
        name: "{{ row.name }}"
        server_type: "{{ row.type }}"
        image: "{{ row.image }}"
        location: "{{ row.location }}"
        ssh_keys: ["{{ row.key }}"]
        user_data: "{{ row.user_data }}"
        placement_group: "{{ row.pg }}"
        labels:
          row: cli-03-servers
        backups: true
        delete_protection: true
        rebuild_protection: true
        state: present
`)
	var described serverDetail
	e.hcloudJSON(t, &described, "server", "describe", name)
	id := described.ID
	var group named
	e.hcloudJSON(t, &group, "placement-group", "describe", pg)
	got := e.serverInfo(t, id)
	if want := map[string]string{"row": "cli-03-servers"}; got.Name != name || !maps.Equal(got.Labels, want) {
		t.Errorf("created server %q with labels %v, want %q with %v", got.Name, got.Labels, name, want)
	}
	if got.PlacementGroup == nil || got.PlacementGroup.ID != group.ID || got.BackupWindow == nil {
		t.Errorf("placement group %+v, backup_window %v; want %d and backups", got.PlacementGroup, got.BackupWindow, group.ID)
	}
	if !got.Protection.Delete || !got.Protection.Rebuild {
		t.Errorf("protection %+v, want delete and rebuild", got.Protection)
	}
	if got.PublicNet.IPv4 == nil {
		t.Fatalf("server %d has no public IPv4", id)
	}
	srv := &server{ID: id, Name: name, IPv4: got.PublicNet.IPv4.IP}
	if err := e.waitReady(srv); err != nil {
		t.Fatal(err)
	}
	if r, err := e.ssh(srv, time.Minute, "", "cat "+userDataMarker); err != nil || strings.TrimSpace(r.Stdout) != userDataValue {
		t.Errorf("user data marker on %s: %v\n%s", name, err, r)
	}
	row["id"], row["ipv4"] = id, srv.IPv4

	state := func(s string) string {
		return `    - name: server ` + s + `
      hetzner.hcloud.server:
        name: "{{ row.name }}"
        state: ` + s + "\n"
	}
	status := func(want string) {
		t.Helper()
		if err := e.waitStatus(id, want, 5*time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	res := fmt.Sprintf("/servers/%d", id)
	step("stopped", state("stopped"))
	status("off")
	step("started", state("started"))
	status("running")
	// hetzner.hcloud 7.2.0 restarts with poweroff and poweron, not reboot.
	done := func(command string) int {
		t.Helper()
		var body struct {
			Actions []struct {
				Command string `json:"command"`
				Status  string `json:"status"`
			} `json:"actions"`
		}
		e.getOK(t, res+"/actions?per_page=50", &body)
		n := 0
		for _, a := range body.Actions {
			if a.Command == command && a.Status == "success" {
				n++
			}
		}
		return n
	}
	stops, starts := done("stop_server"), done("start_server")
	step("restarted", state("restarted"))
	if s, st := done("stop_server"), done("start_server"); s != stops+1 || st != starts+1 {
		t.Errorf("state restarted: %d stop_server and %d start_server actions more, want one each", s-stops, st-starts)
	}
	status("running")

	step("unprotect", `    - name: protection off
      hetzner.hcloud.server:
        name: "{{ row.name }}"
        delete_protection: false
        rebuild_protection: false
        state: present
`)
	if got = e.serverInfo(t, id); got.Protection.Delete || got.Protection.Rebuild {
		t.Errorf("protection %+v after turning it off, want none", got.Protection)
	}
	step("rebuild", `    - name: rebuild
      hetzner.hcloud.server:
        name: "{{ row.name }}"
        image: "{{ row.image }}"
        state: rebuild
`)
	e.assertActionDone(t, res, "rebuild_server")

	step("rescue-on", `    - name: rescue on
      hetzner.hcloud.server:
        name: "{{ row.name }}"
        rescue_mode: linux64
        state: present
`)
	if got = e.serverInfo(t, id); !got.RescueEnabled {
		t.Error("rescue_enabled false after rescue_mode linux64")
	}
	step("rescue-off", state("present"))
	if got = e.serverInfo(t, id); got.RescueEnabled {
		t.Error("rescue_enabled true after present without rescue_mode")
	}

	step("change-type", `    - name: change type
      hetzner.hcloud.server:
        name: "{{ row.name }}"
        server_type: "{{ row.big }}"
        force: true
        state: present
`)
	if got = e.serverInfo(t, id); got.ServerType.Name != big.Name {
		t.Errorf("server type %q after the change, want %s", got.ServerType.Name, big.Name)
	}

	step("info", `    - name: all servers
      hetzner.hcloud.server_info:
      register: all_servers
    - name: by name
      hetzner.hcloud.server_info:
        name: "{{ row.name }}"
      register: by_name
    - name: by ID
      hetzner.hcloud.server_info:
        id: "{{ row.id }}"
      register: by_id
    - name: by label selector
      hetzner.hcloud.server_info:
        label_selector: row=cli-03-servers
      register: by_label
    - name: check the answers
      ansible.builtin.assert:
        that:
          - row.name in (all_servers.hcloud_server_info | map(attribute='name') | list)
          - by_name.hcloud_server_info | length == 1
          - by_name.hcloud_server_info[0].id | int == row.id
          - by_name.hcloud_server_info[0].ipv4_address == row.ipv4
          - by_name.hcloud_server_info[0].server_type == row.big
          - by_id.hcloud_server_info | length == 1
          - by_id.hcloud_server_info[0].name == row.name
          - by_label.hcloud_server_info | map(attribute='name') | list == [row.name]
`)

	step("absent", `    - name: server absent
      hetzner.hcloud.server:
        name: "{{ row.name }}"
        state: absent
    - name: placement group absent
      hetzner.hcloud.placement_group:
        name: "{{ row.pg }}"
        state: absent
`)
	assertGoneIDs(t, e, "/servers", "servers", id)
	assertGoneIDs(t, e, "/placement_groups", "placement_groups", group.ID)
}
