//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rootPasswordRe matches a non-empty root_password in JSON output.
var rootPasswordRe = regexp.MustCompile(`"root_password":\s*"[^"]+"`)

func init() {
	registerRow(clientRow{Req: "CLI-01", Name: "servers", Slots: 1, Run: testCLI01Servers})
}

// testCLI01Servers: hcloud manages a server through every server command
// of the CLI except request-console, each step checked through the API.
func testCLI01Servers(t *testing.T, c *rowCtx) {
	e := c.E
	ctx := context.Background()
	name, renamed, pg := resName("hcloud", "servers"), resName("hcloud", "servers", "renamed"), resName("hcloud", "servers-pg")
	t.Cleanup(func() {
		for _, n := range []string{name, renamed} {
			_, _ = e.hcloudRun(ctx, 5*time.Minute, "server", "disable-protection", n, "delete", "rebuild")
			deleteIfCreated(t, e, n)
		}
		_, _ = e.hcloudRun(ctx, 5*time.Minute, "placement-group", "delete", pg)
	})

	e.hcloudOK(t, "placement-group", "create", "--name", pg, "--type", "spread")
	var group named
	e.hcloudJSON(t, &group, "placement-group", "describe", pg)
	labels := map[string]string{"row": "cli-01-servers"}

	// create with labels, SSH key, user data and placement group; the
	// first server of a fresh data folder downloads the image.
	if r, err := e.hcloudRun(ctx, 45*time.Minute, "server", "create",
		"--name", name, "--type", serverType, "--location", serverLocation, "--image", serverImage,
		"--ssh-key", e.sshKey, "--user-data-from-file", e.userData,
		"--placement-group", pg, "--label", "row=cli-01-servers"); err != nil || r.ExitCode != 0 {
		t.Fatalf("hcloud server create %s: %v\n%s", name, err, r)
	}
	var described serverDetail
	e.hcloudJSON(t, &described, "server", "describe", name)
	id := described.ID
	got := e.serverInfo(t, id)
	if got.Name != name || !maps.Equal(got.Labels, labels) || got.PlacementGroup == nil || got.PlacementGroup.ID != group.ID {
		t.Errorf("created server %+v, want name %s, labels %v, placement group %d", got, name, labels, group.ID)
	}
	if got.PublicNet.IPv4 == nil {
		t.Fatalf("server %d has no public IPv4", id)
	}
	ipv4 := got.PublicNet.IPv4.IP
	srv := &server{ID: id, Name: name, IPv4: ipv4}
	if err := e.waitReady(srv); err != nil {
		t.Fatal(err)
	}
	if r, err := e.ssh(srv, time.Minute, "", "cat "+userDataMarker); err != nil || strings.TrimSpace(r.Stdout) != userDataValue {
		t.Errorf("user data marker on %s: %v\n%s", name, err, r)
	}

	// list, describe, ip
	var listed []named
	e.hcloudJSON(t, &listed, "server", "list", "-l", "row=cli-01-servers")
	if l := namedOf(listed); len(l) != 1 || l[id] != name {
		t.Errorf("hcloud server list -l row=cli-01-servers: %v, want only %d %s", l, id, name)
	}
	if described.Name != name {
		t.Errorf("hcloud server describe %s: name %q", name, described.Name)
	}
	if r := e.hcloudOK(t, "server", "ip", name); strings.TrimSpace(r.Stdout) != ipv4 {
		t.Errorf("hcloud server ip %s: %q, the API has %s", name, r.Stdout, ipv4)
	}

	// update, labels
	e.hcloudOK(t, "server", "update", name, "--name", renamed)
	e.hcloudOK(t, "server", "add-label", renamed, "stage=two")
	e.hcloudOK(t, "server", "remove-label", renamed, "row")
	got = e.serverInfo(t, id)
	if want := map[string]string{"stage": "two"}; got.Name != renamed || !maps.Equal(got.Labels, want) {
		t.Errorf("after update and labels: name %q, labels %v; want %q, %v", got.Name, got.Labels, renamed, want)
	}
	name = renamed

	// power commands
	status := func(want string) {
		t.Helper()
		if err := e.waitStatus(id, want, 5*time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	e.hcloudOK(t, "server", "poweroff", name)
	status("off")
	e.hcloudOK(t, "server", "poweron", name)
	status("running")
	e.hcloudOK(t, "server", "reboot", name)
	e.assertActionDone(t, fmt.Sprintf("/servers/%d", id), "reboot_server")
	e.hcloudOK(t, "server", "reset", name)
	e.assertActionDone(t, fmt.Sprintf("/servers/%d", id), "reset_server")
	status("running")
	e.waitSSH(t, srv, 10*time.Minute)
	e.hcloudOK(t, "server", "shutdown", "--wait", "--wait-timeout", "5m", name)
	status("off")
	e.hcloudOK(t, "server", "poweron", name)
	status("running")

	// reset-password
	if r := e.hcloudOK(t, "server", "reset-password", "-o", "json", name); !rootPasswordRe.MatchString(r.Stdout) {
		t.Error("hcloud server reset-password -o json printed no root_password")
	}
	e.assertActionDone(t, fmt.Sprintf("/servers/%d", id), "reset_password")

	// change-type and placement group while powered off
	_, big := largerType(t, e)
	e.hcloudOK(t, "server", "poweroff", name)
	status("off")
	e.hcloudOK(t, "server", "change-type", name, big.Name)
	if got = e.serverInfo(t, id); got.ServerType.Name != big.Name {
		t.Errorf("server type after change-type %q, want %s", got.ServerType.Name, big.Name)
	}
	if got.Status != "off" {
		e.hcloudOK(t, "server", "poweroff", name)
		status("off")
	}
	e.hcloudOK(t, "server", "remove-from-placement-group", name)
	if got = e.serverInfo(t, id); got.PlacementGroup != nil {
		t.Errorf("placement group after remove-from-placement-group: %+v", got.PlacementGroup)
	}
	e.hcloudOK(t, "server", "add-to-placement-group", "--placement-group", pg, name)
	if got = e.serverInfo(t, id); got.PlacementGroup == nil || got.PlacementGroup.ID != group.ID {
		t.Errorf("placement group after add-to-placement-group: %+v, want %d", got.PlacementGroup, group.ID)
	}
	if got.Status != "running" {
		e.hcloudOK(t, "server", "poweron", name)
	}
	status("running")

	// rebuild
	e.hcloudOK(t, "server", "rebuild", "--image", serverImage, "--user-data-from-file", e.userData, name)
	e.assertActionDone(t, fmt.Sprintf("/servers/%d", id), "rebuild_server")
	if got = e.serverInfo(t, id); got.Image == nil || got.Image.Name != serverImage {
		t.Errorf("image after rebuild %+v, want %s", got.Image, serverImage)
	}

	// rescue
	e.hcloudOK(t, "server", "enable-rescue", "--ssh-key", e.sshKey, name)
	if got = e.serverInfo(t, id); !got.RescueEnabled {
		t.Error("rescue_enabled false after enable-rescue")
	}
	e.hcloudOK(t, "server", "disable-rescue", name)
	if got = e.serverInfo(t, id); got.RescueEnabled {
		t.Error("rescue_enabled true after disable-rescue")
	}

	// backups
	e.hcloudOK(t, "server", "enable-backup", name)
	if got = e.serverInfo(t, id); got.BackupWindow == nil {
		t.Error("backup_window null after enable-backup")
	}
	e.hcloudOK(t, "server", "disable-backup", name)
	if got = e.serverInfo(t, id); got.BackupWindow != nil {
		t.Errorf("backup_window %q after disable-backup", *got.BackupWindow)
	}

	// create-image: a snapshot, deleted again
	e.hcloudOK(t, "server", "create-image", "--type", "snapshot", "--description", name+"-snap", "--label", "row=cli-01-servers", name)
	var snaps struct {
		Images []struct {
			ID          int64  `json:"id"`
			Type        string `json:"type"`
			Description string `json:"description"`
			CreatedFrom *struct {
				ID int64 `json:"id"`
			} `json:"created_from"`
		} `json:"images"`
	}
	e.getOK(t, "/images?type=snapshot&label_selector=row%3Dcli-01-servers", &snaps)
	if len(snaps.Images) != 1 || snaps.Images[0].CreatedFrom == nil || snaps.Images[0].CreatedFrom.ID != id {
		t.Fatalf("snapshots with row=cli-01-servers: %+v, want one created from %d", snaps.Images, id)
	}
	snap := snaps.Images[0].ID
	t.Cleanup(func() { e.deleteImage(t, snap) })
	e.hcloudOK(t, "image", "delete", strconv.FormatInt(snap, 10))
	assertGoneIDs(t, e, "/images?type=snapshot", "images", snap)

	// ISO
	iso := e.firstISO(t)
	e.hcloudOK(t, "server", "attach-iso", name, iso.Name)
	if got = e.serverInfo(t, id); got.ISO == nil || got.ISO.ID != iso.ID {
		t.Errorf("iso after attach-iso %+v, want %d %s", got.ISO, iso.ID, iso.Name)
	}
	e.hcloudOK(t, "server", "detach-iso", name)
	if got = e.serverInfo(t, id); got.ISO != nil {
		t.Errorf("iso after detach-iso %+v, want none", got.ISO)
	}

	// set-rdns
	ptr := "srv." + zoneName("hcloud-rdns")
	e.hcloudOK(t, "server", "set-rdns", "--ip", ipv4, "--hostname", ptr, name)
	if got = e.serverInfo(t, id); got.PublicNet.IPv4 == nil || got.PublicNet.IPv4.DNSPtr != ptr {
		t.Errorf("dns_ptr of %s after set-rdns %+v, want %s", ipv4, got.PublicNet.IPv4, ptr)
	}

	// metrics
	now := time.Now().UTC()
	r := e.hcloudOK(t, "server", "metrics", "--type", "cpu", "--start", now.Add(-time.Hour).Format(time.RFC3339),
		"--end", now.Format(time.RFC3339), "-o", "json", name)
	var metrics map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.Stdout), &metrics); err != nil || len(metrics) == 0 {
		t.Errorf("hcloud server metrics -o json: %v\n%s", err, r.Stdout)
	}

	// protection
	e.hcloudOK(t, "server", "enable-protection", name, "delete", "rebuild")
	if got = e.serverInfo(t, id); !got.Protection.Delete || !got.Protection.Rebuild {
		t.Errorf("protection after enable-protection %+v, want delete and rebuild", got.Protection)
	}
	e.hcloudOK(t, "server", "disable-protection", name, "delete", "rebuild")
	if got = e.serverInfo(t, id); got.Protection.Delete || got.Protection.Rebuild {
		t.Errorf("protection after disable-protection %+v, want none", got.Protection)
	}

	// delete
	e.hcloudOK(t, "server", "delete", name)
	assertGoneIDs(t, e, "/servers", "servers", id)
	e.hcloudOK(t, "placement-group", "delete", pg)
	assertGoneIDs(t, e, "/placement_groups", "placement_groups", group.ID)
}
