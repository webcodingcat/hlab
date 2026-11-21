//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-02", Name: "servers", Slots: 1, Run: testCLI02Servers})
}

// tofuServer is the variable part of the main.tf of CLI-02/servers.
type tofuServer struct {
	name, serverType string
	labels           map[string]string
	backups          bool
	iso              string // "" leaves no ISO attached
	protection       bool
}

func (s tofuServer) main(pg, key string) string {
	iso := "null"
	if s.iso != "" {
		iso = strconv.Quote(s.iso)
	}
	return fmt.Sprintf(`resource "hcloud_placement_group" "pg" {
  name = %q
  type = "spread"
}

resource "hcloud_server" "s" {
  name               = %q
  server_type        = %q
  image              = %q
  location           = %q
  ssh_keys           = [%q]
  user_data          = file("${path.module}/user-data.yaml")
  placement_group_id = hcloud_placement_group.pg.id
  labels             = %s
  backups            = %t
  rescue             = "linux64"
  iso                = %s
  delete_protection  = %t
  rebuild_protection = %t
}

data "hcloud_server" "by_id" {
  id = hcloud_server.s.id
}

data "hcloud_servers" "by_label" {
  with_selector = "row=cli-02-servers"
  depends_on    = [hcloud_server.s]
}

output "id" {
  value = hcloud_server.s.id
}

output "pg" {
  value = hcloud_placement_group.pg.id
}

output "ds_name" {
  value = data.hcloud_server.by_id.name
}

output "ds_type" {
  value = data.hcloud_server.by_id.server_type
}

output "ds_ids" {
  value = jsonencode(data.hcloud_servers.by_label.servers[*].id)
}
`, pg, s.name, s.serverType, serverImage, serverLocation, key, hclMap(s.labels), s.backups, iso, s.protection, s.protection)
}

// testCLI02Servers: OpenTofu creates hcloud_server with labels, keys, user
// data, placement group, backups, rescue, ISO and protection, updates name,
// labels, type, backups, ISO and protection in place, reads hcloud_server
// and hcloud_servers, and destroys it.
func testCLI02Servers(t *testing.T, _ *rowCtx) {
	e := sharedTofu(t)
	ctx := context.Background()
	d := e.newTofuDir(t, "servers")
	d.timeout = tofuServerTimeout
	pg := resName("tofu", "servers-pg")
	first := tofuServer{
		name: resName("tofu", "servers"), serverType: serverType,
		labels: map[string]string{"row": "cli-02-servers"}, backups: true, iso: e.firstISO(t).Name, protection: true,
	}
	_, big := largerType(t, e)
	second := tofuServer{
		name: resName("tofu", "servers", "renamed"), serverType: big.Name,
		labels: map[string]string{"row": "cli-02-servers", "stage": "two"},
	}
	t.Cleanup(func() {
		for _, n := range []string{first.name, second.name} {
			_, _ = e.hcloudRun(ctx, 5*time.Minute, "server", "disable-protection", n, "delete", "rebuild")
			deleteIfCreated(t, e, n)
		}
		_, _ = e.hcloudRun(ctx, 5*time.Minute, "placement-group", "delete", pg)
	})

	data, err := os.ReadFile(e.userData)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.dir, "user-data.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	d.run(t, "init", "-no-color")

	d.writeMain(t, first.main(pg, e.sshKey))
	d.run(t, "apply", "-auto-approve", "-no-color")
	idText := d.output(t, "id")
	id := parseID(t, idText)
	pgID := parseID(t, d.output(t, "pg"))
	got := e.serverInfo(t, id)
	if got.Name != first.name || !maps.Equal(got.Labels, first.labels) || got.ServerType.Name != serverType {
		t.Errorf("created server %q, labels %v, type %s; want %q, %v, %s", got.Name, got.Labels, got.ServerType.Name, first.name, first.labels, serverType)
	}
	if got.PlacementGroup == nil || got.PlacementGroup.ID != pgID {
		t.Errorf("placement group %+v, want %d", got.PlacementGroup, pgID)
	}
	if got.BackupWindow == nil || !got.RescueEnabled || got.ISO == nil || got.ISO.Name != first.iso {
		t.Errorf("backup_window %v, rescue_enabled %t, iso %+v; want backups, rescue and ISO %s", got.BackupWindow, got.RescueEnabled, got.ISO, first.iso)
	}
	if !got.Protection.Delete || !got.Protection.Rebuild {
		t.Errorf("protection %+v, want delete and rebuild", got.Protection)
	}
	if n, typ := d.output(t, "ds_name"), d.output(t, "ds_type"); n != got.Name || typ != got.ServerType.Name {
		t.Errorf("data hcloud_server: name %q, type %q; the API has %q, %q", n, typ, got.Name, got.ServerType.Name)
	}
	if ids := d.output(t, "ds_ids"); ids != "["+idText+"]" {
		t.Errorf("data hcloud_servers with row=cli-02-servers: IDs %s, want [%s]", ids, idText)
	}

	d.writeMain(t, second.main(pg, e.sshKey))
	r := d.run(t, "apply", "-auto-approve", "-no-color")
	if !strings.Contains(r.Stdout, updatedInPlace) {
		t.Errorf("second apply is no in-place update (want %q):\n%s", updatedInPlace, r.Stdout)
	}
	if id2 := d.output(t, "id"); id2 != idText {
		t.Errorf("ID changed from %s to %s; want an in-place update", idText, id2)
	}
	got = e.serverInfo(t, id)
	if got.Name != second.name || !maps.Equal(got.Labels, second.labels) || got.ServerType.Name != big.Name {
		t.Errorf("updated server %q, labels %v, type %s; want %q, %v, %s", got.Name, got.Labels, got.ServerType.Name, second.name, second.labels, big.Name)
	}
	if got.BackupWindow != nil || got.ISO != nil || got.Protection.Delete || got.Protection.Rebuild {
		t.Errorf("after update: backup_window %v, iso %+v, protection %+v; want none of them", got.BackupWindow, got.ISO, got.Protection)
	}
	if n, typ := d.output(t, "ds_name"), d.output(t, "ds_type"); n != second.name || typ != big.Name {
		t.Errorf("data hcloud_server after update: name %q, type %q; want %q, %q", n, typ, second.name, big.Name)
	}

	d.run(t, "destroy", "-auto-approve", "-no-color")
	assertGoneIDs(t, e, "/servers", "servers", id)
	assertGoneIDs(t, e, "/placement_groups", "placement_groups", pgID)
}
