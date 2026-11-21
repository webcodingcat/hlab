//go:build e2e

package e2e

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-01", Name: "images", Fixture: true, Run: testCLI01Images})
	registerRow(clientRow{Req: "CLI-02", Name: "images", Fixture: true, Run: testCLI02Images})
	registerRow(clientRow{Req: "CLI-03", Name: "images", Fixture: true, Run: testCLI03Images})
}

// imageDetail is what the image rows check of GET /images/{id}.
type imageDetail struct {
	ID          int64             `json:"id"`
	Type        string            `json:"type"`
	Name        *string           `json:"name"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	CreatedFrom *struct {
		ID int64 `json:"id"`
	} `json:"created_from"`
	Protection struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
}

func (e *env) imageInfo(t *testing.T, id int64) imageDetail {
	t.Helper()
	var body struct {
		Image imageDetail `json:"image"`
	}
	e.getOK(t, fmt.Sprintf("/images/%d", id), &body)
	return body.Image
}

// systemImageID is the ID of the run's system image.
func (e *env) systemImageID(t *testing.T) int64 {
	t.Helper()
	var body struct {
		Images []imageDetail `json:"images"`
	}
	e.getOK(t, "/images?type=system&architecture=x86&name="+serverImage, &body)
	if len(body.Images) != 1 {
		t.Fatalf("GET /images?name=%s: %d images, want 1", serverImage, len(body.Images))
	}
	return body.Images[0].ID
}

// testCLI01Images: hcloud creates a snapshot of the fixture server, lists,
// describes, updates, protects and unprotects, and deletes it.
func testCLI01Images(t *testing.T, c *rowCtx) {
	e, fx := c.E, c.Fixture
	e.hcloudOK(t, "server", "create-image", "--type", "snapshot", "--description", resName("hcloud", "images"),
		"--label", "row=cli-01-images", fx.Name)
	var listed []imageDetail
	e.hcloudJSON(t, &listed, "image", "list", "-t", "snapshot", "-l", "row=cli-01-images")
	if len(listed) != 1 {
		t.Fatalf("hcloud image list -t snapshot -l row=cli-01-images: %d images, want 1", len(listed))
	}
	id := listed[0].ID
	t.Cleanup(func() { e.deleteImage(t, id) })
	ref := strconv.FormatInt(id, 10)
	got := e.imageInfo(t, id)
	if got.Type != "snapshot" || got.CreatedFrom == nil || got.CreatedFrom.ID != fx.ID || got.Description != resName("hcloud", "images") {
		t.Errorf("snapshot %+v, want type snapshot, created from %d, description %s", got, fx.ID, resName("hcloud", "images"))
	}

	var described imageDetail
	e.hcloudJSON(t, &described, "image", "describe", ref)
	if described.ID != id || described.Description != got.Description {
		t.Errorf("hcloud image describe %s: %+v, the API has %+v", ref, described, got)
	}

	e.hcloudOK(t, "image", "update", "--description", resName("hcloud", "images", "updated"), ref)
	e.hcloudOK(t, "image", "add-label", ref, "stage=two")
	e.hcloudOK(t, "image", "remove-label", ref, "row")
	got = e.imageInfo(t, id)
	if want := map[string]string{"stage": "two"}; got.Description != resName("hcloud", "images", "updated") || !maps.Equal(got.Labels, want) {
		t.Errorf("after update: description %q, labels %v; want %q, %v", got.Description, got.Labels, resName("hcloud", "images", "updated"), want)
	}

	e.hcloudOK(t, "image", "enable-protection", ref, "delete")
	if !e.imageInfo(t, id).Protection.Delete {
		t.Error("delete protection off after enable-protection")
	}
	e.hcloudOK(t, "image", "disable-protection", ref, "delete")
	if e.imageInfo(t, id).Protection.Delete {
		t.Error("delete protection on after disable-protection")
	}

	e.hcloudOK(t, "image", "delete", ref)
	assertGoneIDs(t, e, "/images?type=snapshot", "images", id)
}

// testCLI02Images: OpenTofu creates hcloud_snapshot of the fixture server,
// updates it in place, reads hcloud_image (system image by name, snapshot
// by ID) and hcloud_images (by label), and destroys it.
func testCLI02Images(t *testing.T, c *rowCtx) {
	e, fx := sharedTofu(t), c.Fixture
	d := e.newTofuDir(t, "images")
	main := func(description, stage string) string {
		return fmt.Sprintf(`resource "hcloud_snapshot" "s" {
  server_id   = %d
  description = %q
  labels = {
    row   = "cli-02-images"
    stage = %q
  }
}

data "hcloud_image" "system" {
  name              = %q
  with_architecture = "x86"
}

data "hcloud_image" "snap" {
  id = hcloud_snapshot.s.id
}

data "hcloud_images" "by_label" {
  with_selector = "row=cli-02-images"
  depends_on    = [hcloud_snapshot.s]
}

output "id" {
  value = hcloud_snapshot.s.id
}

output "system_id" {
  value = data.hcloud_image.system.id
}

output "snap_description" {
  value = data.hcloud_image.snap.description
}

output "label_ids" {
  value = jsonencode(data.hcloud_images.by_label.images[*].id)
}
`, fx.ID, description, stage, serverImage)
	}
	d.run(t, "init", "-no-color")

	desc1 := resName("tofu", "images")
	d.writeMain(t, main(desc1, "one"))
	d.run(t, "apply", "-auto-approve", "-no-color")
	idText := d.output(t, "id")
	id := parseID(t, idText)
	t.Cleanup(func() { e.deleteImage(t, id) })
	got := e.imageInfo(t, id)
	if want := map[string]string{"row": "cli-02-images", "stage": "one"}; got.Type != "snapshot" || got.CreatedFrom == nil ||
		got.CreatedFrom.ID != fx.ID || got.Description != desc1 || !maps.Equal(got.Labels, want) {
		t.Errorf("snapshot %+v, want a snapshot of %d with description %s and labels %v", got, fx.ID, desc1, want)
	}
	if sys := d.output(t, "system_id"); sys != strconv.FormatInt(e.systemImageID(t), 10) {
		t.Errorf("data hcloud_image %s: ID %s, the API has %d", serverImage, sys, e.systemImageID(t))
	}
	if sd := d.output(t, "snap_description"); sd != desc1 {
		t.Errorf("data hcloud_image by ID %d: description %q, want %q", id, sd, desc1)
	}
	if ids := d.output(t, "label_ids"); ids != "["+idText+"]" {
		t.Errorf("data hcloud_images with row=cli-02-images: IDs %s, want [%s]", ids, idText)
	}

	desc2 := resName("tofu", "images", "updated")
	d.writeMain(t, main(desc2, "two"))
	r := d.run(t, "apply", "-auto-approve", "-no-color")
	if !strings.Contains(r.Stdout, updatedInPlace) {
		t.Errorf("second apply is no in-place update (want %q):\n%s", updatedInPlace, r.Stdout)
	}
	got = e.imageInfo(t, id)
	if want := map[string]string{"row": "cli-02-images", "stage": "two"}; got.Description != desc2 || !maps.Equal(got.Labels, want) {
		t.Errorf("updated snapshot: description %q, labels %v; want %q, %v", got.Description, got.Labels, desc2, want)
	}

	d.run(t, "destroy", "-auto-approve", "-no-color")
	assertGoneIDs(t, e, "/images?type=snapshot", "images", id)
}

// testCLI03Images: image_info finds a system image by name and a snapshot
// of the fixture server, made through the API, by label.
func testCLI03Images(t *testing.T, c *rowCtx) {
	e, w := sharedAnsible(t)
	fx := c.Fixture
	var made struct {
		Image struct {
			ID int64 `json:"id"`
		} `json:"image"`
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	e.post(t, fmt.Sprintf("/servers/%d/actions/create_image", fx.ID), map[string]any{
		"type": "snapshot", "description": resName("ansible", "images"), "labels": map[string]string{"row": "cli-03-images"},
	}, &made)
	snap := made.Image.ID
	t.Cleanup(func() { e.deleteImage(t, snap) })
	if err := e.waitAction(made.Action.ID, 20*time.Minute); err != nil {
		t.Fatal(err)
	}

	e.ansibleOK(t, w, "cli03-images", localPlay(t, map[string]any{
		"system": serverImage, "system_id": e.systemImageID(t), "snap_id": snap,
	}, `    - name: system image by name
      hetzner.hcloud.image_info:
        name: "{{ row.system }}"
        architecture: x86
      register: system
    - name: snapshot by label
      hetzner.hcloud.image_info:
        type: snapshot
        label_selector: row=cli-03-images
      register: snap
    - name: check the answers
      ansible.builtin.assert:
        that:
          - system.hcloud_image_info | length == 1
          - system.hcloud_image_info[0].id | int == row.system_id
          - snap.hcloud_image_info | length == 1
          - snap.hcloud_image_info[0].id | int == row.snap_id
          - snap.hcloud_image_info[0].type == "snapshot"
`))

	e.deleteImage(t, snap)
	assertGoneIDs(t, e, "/images?type=snapshot", "images", snap)
}
