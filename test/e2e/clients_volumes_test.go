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
	registerRow(clientRow{Req: "CLI-01", Name: "volumes", Fixture: true, Run: testCLI01Volumes})
	registerRow(clientRow{Req: "CLI-02", Name: "volumes", Fixture: true, Run: testCLI02Volumes})
	registerRow(clientRow{Req: "CLI-03", Name: "volumes", Fixture: true, Run: testCLI03Volumes})
}

// volumeDetail is what the client rows check of GET /volumes/{id}.
type volumeDetail struct {
	ID       int64             `json:"id"`
	Name     string            `json:"name"`
	Size     int               `json:"size"`
	Server   *int64            `json:"server"`
	Format   *string           `json:"format"`
	Labels   map[string]string `json:"labels"`
	Location struct {
		Name string `json:"name"`
	} `json:"location"`
	Protection struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
}

func (e *env) volumeInfo(t *testing.T, id int64) volumeDetail {
	t.Helper()
	var body struct {
		Volume volumeDetail `json:"volume"`
	}
	e.getOK(t, fmt.Sprintf("/volumes/%d", id), &body)
	return body.Volume
}

// wantVolume fails unless the volume has the name, size, labels and server
// (nil: detached), lies in the run's location and the server lists it.
func (e *env) wantVolume(t *testing.T, step string, id int64, name string, size int, labels map[string]string, server *server) volumeDetail {
	t.Helper()
	got := e.volumeInfo(t, id)
	attached := got.Server != nil && server != nil && *got.Server == server.ID
	if got.Name != name || got.Size != size || !maps.Equal(got.Labels, labels) || got.Location.Name != serverLocation ||
		(server == nil) != (got.Server == nil) || (server != nil && !attached) {
		want := "detached"
		if server != nil {
			want = fmt.Sprintf("on server %d", server.ID)
		}
		t.Errorf("%s: volume %q of %d GB in %s, labels %v, server %v; want %q of %d GB in %s, %v, %s",
			step, got.Name, got.Size, got.Location.Name, got.Labels, got.Server, name, size, serverLocation, labels, want)
	}
	if server != nil {
		var body struct {
			Server struct {
				Volumes []int64 `json:"volumes"`
			} `json:"server"`
		}
		e.getOK(t, fmt.Sprintf("/servers/%d", server.ID), &body)
		found := false
		for _, v := range body.Server.Volumes {
			found = found || v == id
		}
		if !found {
			t.Errorf("%s: server %d lists volumes %v, want %d among them", step, server.ID, body.Server.Volumes, id)
		}
	}
	return got
}

// deleteVolumeUnprotected turns off the delete protection of a volume a
// row made and deletes it; 0 is no volume, and a volume the row deleted
// already is left alone (deleteVolume waits for one that exists).
func (e *env) deleteVolumeUnprotected(t *testing.T, id int64) {
	t.Helper()
	if id == 0 {
		return
	}
	if status, _, err := e.apiDo(context.Background(), http.MethodGet, fmt.Sprintf("/volumes/%d", id), nil, nil); err == nil && status == http.StatusNotFound {
		return
	}
	path := fmt.Sprintf("/volumes/%d/actions/change_protection", id)
	_, _, _ = e.apiDo(context.Background(), http.MethodPost, path, map[string]any{"delete": false}, nil)
	e.deleteVolume(t, id)
}

// testCLI01Volumes: hcloud creates a volume in a location and one on the
// fixture server with automount and format, attaches, detaches, resizes,
// updates, labels, protects and deletes them.
func testCLI01Volumes(t *testing.T, c *rowCtx) {
	e, fx := c.E, c.Fixture
	loc, onServer := resName("hcloud", "volumes-loc"), resName("hcloud", "volumes-srv")
	ids := map[string]int64{}
	t.Cleanup(func() {
		for _, id := range ids {
			e.deleteVolumeUnprotected(t, id)
		}
	})
	labels := map[string]string{"row": "cli-01-volumes"}

	e.hcloudOK(t, "volume", "create", "--name", loc, "--size", "10", "--location", serverLocation, "--label", "row=cli-01-volumes")
	e.hcloudOK(t, "volume", "create", "--name", onServer, "--size", "10", "--server", fx.Name, "--automount", "--format", "ext4",
		"--label", "row=cli-01-volumes")
	var listed []volumeDetail
	e.hcloudJSON(t, &listed, "volume", "list", "-l", "row=cli-01-volumes")
	for _, v := range listed {
		ids[v.Name] = v.ID
	}
	if len(listed) != 2 || ids[loc] == 0 || ids[onServer] == 0 {
		t.Fatalf("hcloud volume list -l row=cli-01-volumes: %+v, want %s and %s", listed, loc, onServer)
	}
	e.wantVolume(t, "create in a location", ids[loc], loc, 10, labels, nil)
	if got := e.wantVolume(t, "create on the server", ids[onServer], onServer, 10, labels, fx); got.Format == nil || *got.Format != "ext4" {
		t.Errorf("volume created with --format ext4 has format %v", got.Format)
	}
	var described volumeDetail
	e.hcloudJSON(t, &described, "volume", "describe", loc)
	if described.ID != ids[loc] || described.Size != 10 {
		t.Errorf("hcloud volume describe %s: %+v, want ID %d of 10 GB", loc, described, ids[loc])
	}

	e.hcloudOK(t, "volume", "attach", "--server", fx.Name, loc)
	e.wantVolume(t, "attach", ids[loc], loc, 10, labels, fx)
	e.hcloudOK(t, "volume", "detach", loc)
	e.wantVolume(t, "detach", ids[loc], loc, 10, labels, nil)
	e.hcloudOK(t, "volume", "detach", onServer)
	e.wantVolume(t, "detach the automounted volume", ids[onServer], onServer, 10, labels, nil)

	e.hcloudOK(t, "volume", "resize", "--size", "20", loc)
	renamed := loc + "-renamed"
	e.hcloudOK(t, "volume", "update", "--name", renamed, loc)
	e.hcloudOK(t, "volume", "add-label", renamed, "stage=two")
	e.hcloudOK(t, "volume", "remove-label", renamed, "row")
	e.wantVolume(t, "resize, update and labels", ids[loc], renamed, 20, map[string]string{"stage": "two"}, nil)
	e.hcloudOK(t, "volume", "enable-protection", renamed, "delete")
	if !e.volumeInfo(t, ids[loc]).Protection.Delete {
		t.Error("delete protection off after enable-protection")
	}
	e.hcloudOK(t, "volume", "disable-protection", renamed, "delete")
	if e.volumeInfo(t, ids[loc]).Protection.Delete {
		t.Error("delete protection on after disable-protection")
	}

	e.hcloudOK(t, "volume", "delete", renamed, onServer)
	assertGoneIDs(t, e, "/volumes", "volumes", ids[loc], ids[onServer])
}

// testCLI02Volumes: OpenTofu manages hcloud_volume and
// hcloud_volume_attachment to the fixture server, resizes in place, reads
// hcloud_volume and hcloud_volumes, and destroys them.
func testCLI02Volumes(t *testing.T, c *rowCtx) {
	e, fx := sharedTofu(t), c.Fixture
	d := e.newTofuDir(t, "volumes")
	main := func(name string, size int, stage string) string {
		return fmt.Sprintf(`resource "hcloud_volume" "v" {
  name     = %q
  size     = %d
  location = %q
  format   = "ext4"
  labels = {
    row   = "cli-02-volumes"
    stage = %q
  }
}

resource "hcloud_volume_attachment" "a" {
  volume_id = hcloud_volume.v.id
  server_id = %d
  automount = false
}

data "hcloud_volume" "by_id" {
  id         = hcloud_volume.v.id
  depends_on = [hcloud_volume.v, hcloud_volume_attachment.a]
}

data "hcloud_volumes" "by_label" {
  with_selector = "row=cli-02-volumes"
  depends_on    = [hcloud_volume.v]
}

output "id" {
  value = hcloud_volume.v.id
}

output "by_id_size" {
  value = data.hcloud_volume.by_id.size
}

output "label_ids" {
  value = jsonencode(data.hcloud_volumes.by_label.volumes[*].id)
}
`, name, size, serverLocation, stage, fx.ID)
	}
	d.run(t, "init", "-no-color")

	var id int64
	t.Cleanup(func() { e.deleteVolumeUnprotected(t, id) })
	name := resName("tofu", "volumes")
	d.writeMain(t, main(name, 10, "one"))
	d.run(t, "apply", "-auto-approve", "-no-color")
	idText := d.output(t, "id")
	id = parseID(t, idText)
	e.wantVolume(t, "apply", id, name, 10, map[string]string{"row": "cli-02-volumes", "stage": "one"}, fx)
	if s := fmt.Sprint(d.dataValues(t, "data.hcloud_volume.by_id")["server_id"]); s != fmt.Sprint(fx.ID) {
		t.Errorf("data hcloud_volume by ID: server %s, want %d", s, fx.ID)
	}
	if ids := d.output(t, "label_ids"); ids != "["+idText+"]" {
		t.Errorf("data hcloud_volumes with row=cli-02-volumes: IDs %s, want [%s]", ids, idText)
	}

	renamed := name + "-renamed"
	d.writeMain(t, main(renamed, 20, "two"))
	r := d.run(t, "apply", "-auto-approve", "-no-color")
	if !strings.Contains(r.Stdout, updatedInPlace) {
		t.Errorf("second apply is no in-place update (want %q):\n%s", updatedInPlace, r.Stdout)
	}
	e.wantVolume(t, "resize and update", id, renamed, 20, map[string]string{"row": "cli-02-volumes", "stage": "two"}, fx)
	if s := d.output(t, "by_id_size"); s != "20" {
		t.Errorf("data hcloud_volume by ID after the resize: size %s, want 20", s)
	}

	d.run(t, "destroy", "-auto-approve", "-no-color")
	assertGoneIDs(t, e, "/volumes", "volumes", id)
}

// testCLI03Volumes: volume and volume_attachment create, attach, resize,
// detach and delete; volume_info finds the volume.
func testCLI03Volumes(t *testing.T, c *rowCtx) {
	e, w := sharedAnsible(t)
	fx := c.Fixture
	name := resName("ansible", "volumes")
	var id int64
	t.Cleanup(func() { e.deleteVolumeUnprotected(t, id) })
	row := map[string]any{"name": name, "location": serverLocation, "server": fx.Name}
	// format applies to a new volume only.
	volume := func(size int, stage, format string) string {
		return fmt.Sprintf(`    - name: volume
      hetzner.hcloud.volume:
        name: "{{ row.name }}"
        size: %d
        location: "{{ row.location }}"
%s        labels:
          row: cli-03-volumes
          stage: %s
        state: present
`, size, format, stage)
	}
	attachment := func(state string) string {
		return `    - name: volume attachment
      hetzner.hcloud.volume_attachment:
        volume: "{{ row.name }}"
        server: "{{ row.server }}"
        automount: false
        state: ` + state + `
`
	}

	e.ansibleOK(t, w, "cli03-volumes-create", localPlay(t, row, volume(10, "one", "        format: ext4\n")))
	var described volumeDetail
	e.hcloudJSON(t, &described, "volume", "describe", name)
	id = described.ID
	e.wantVolume(t, "volume", id, name, 10, map[string]string{"row": "cli-03-volumes", "stage": "one"}, nil)

	e.ansibleOK(t, w, "cli03-volumes-attach", localPlay(t, row, attachment("present")))
	e.wantVolume(t, "volume_attachment", id, name, 10, map[string]string{"row": "cli-03-volumes", "stage": "one"}, fx)

	e.ansibleOK(t, w, "cli03-volumes-resize", localPlay(t, row, volume(20, "two", "")+`    - name: volume info
      hetzner.hcloud.volume_info:
        name: "{{ row.name }}"
      register: info
    - name: check the answer
      ansible.builtin.assert:
        that:
          - info.hcloud_volume_info | length == 1
          - info.hcloud_volume_info[0].size | int == 20
          - info.hcloud_volume_info[0].server == row.server
          - info.hcloud_volume_info[0].labels.stage == "two"
`))
	e.wantVolume(t, "resize", id, name, 20, map[string]string{"row": "cli-03-volumes", "stage": "two"}, fx)

	e.ansibleOK(t, w, "cli03-volumes-detach", localPlay(t, row, attachment("absent")))
	e.wantVolume(t, "volume_attachment absent", id, name, 20, map[string]string{"row": "cli-03-volumes", "stage": "two"}, nil)

	e.ansibleOK(t, w, "cli03-volumes-absent", localPlay(t, row, `    - name: volume absent
      hetzner.hcloud.volume:
        name: "{{ row.name }}"
        state: absent
`))
	assertGoneIDs(t, e, "/volumes", "volumes", id)
}
