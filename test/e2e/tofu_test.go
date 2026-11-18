//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testCLI02 holds the OpenTofu rows of CLI-02, one subtest per resource type.
func testCLI02(t *testing.T) {
	t.Run("ssh_keys", parallel(testCLI02SSHKeys))
	t.Run("placement_groups", parallel(testCLI02PlacementGroups))
}

// updatedInPlace is the summary line of `tofu apply` for an in-place update.
const updatedInPlace = "0 added, 1 changed, 0 destroyed"

// testCLI02SSHKeys: hcloud_ssh_key with labels is created, a second apply
// with changed labels updates it in place, and destroy removes it.
func testCLI02SSHKeys(t *testing.T) {
	e := sharedTofu(t)
	ctx := context.Background()
	d := e.newTofuDir(t, "ssh_keys")

	// A key of its own: the run's key is registered already and SSH keys
	// are unique by fingerprint.
	key := filepath.Join(d.dir, "id_ed25519")
	if r, err := command(ctx, time.Minute, nil, "", harness.sshKeygen, "-q", "-t", "ed25519", "-N", "", "-C", "hlab-e2e-tofu", "-f", key); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, r)
	}
	name := harness.runID + "-tofu-key"
	main := func(labels map[string]string) string {
		return fmt.Sprintf(`resource "hcloud_ssh_key" "k" {
  name       = %q
  public_key = file("${path.module}/id_ed25519.pub")
  labels     = %s
}

output "id" {
  value = hcloud_ssh_key.k.id
}
`, name, hclMap(labels))
	}

	d.run(t, "init", "-no-color")

	first := map[string]string{"env": "e2e", "row": "cli-02"}
	d.writeMain(t, main(first))
	d.run(t, "apply", "-auto-approve", "-no-color")
	id := d.output(t, "id")
	var got struct {
		SSHKey struct {
			ID     int64             `json:"id"`
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"ssh_key"`
	}
	if status, err := e.apiGet(ctx, "/ssh_keys/"+id, &got); err != nil || status != http.StatusOK {
		t.Fatalf("GET /ssh_keys/%s after create: status %d, %v", id, status, err)
	}
	if got.SSHKey.Name != name || !maps.Equal(got.SSHKey.Labels, first) {
		t.Errorf("created key %q with labels %v, want %q with %v", got.SSHKey.Name, got.SSHKey.Labels, name, first)
	}

	second := map[string]string{"env": "e2e", "stage": "two"}
	d.writeMain(t, main(second))
	r := d.run(t, "apply", "-auto-approve", "-no-color")
	if !strings.Contains(r.Stdout, updatedInPlace) {
		t.Errorf("second apply is no in-place update (want %q):\n%s", updatedInPlace, r.Stdout)
	}
	if id2 := d.output(t, "id"); id2 != id {
		t.Errorf("ID changed from %s to %s; want an in-place update", id, id2)
	}
	if status, err := e.apiGet(ctx, "/ssh_keys/"+id, &got); err != nil || status != http.StatusOK {
		t.Fatalf("GET /ssh_keys/%s after update: status %d, %v", id, status, err)
	}
	if !maps.Equal(got.SSHKey.Labels, second) {
		t.Errorf("labels after update %v, want %v", got.SSHKey.Labels, second)
	}

	d.run(t, "destroy", "-auto-approve", "-no-color")
	assertGone(t, e, "/ssh_keys", "ssh_keys", id)
}

// testCLI02PlacementGroups: hcloud_placement_group of type spread is created,
// a second apply with a new name and labels updates it in place, and
// destroy removes it.
func testCLI02PlacementGroups(t *testing.T) {
	e := sharedTofu(t)
	ctx := context.Background()
	d := e.newTofuDir(t, "placement_groups")

	main := func(name string, labels map[string]string) string {
		return fmt.Sprintf(`resource "hcloud_placement_group" "g" {
  name   = %q
  type   = "spread"
  labels = %s
}

output "id" {
  value = hcloud_placement_group.g.id
}
`, name, hclMap(labels))
	}
	type group struct {
		PlacementGroup struct {
			ID     int64             `json:"id"`
			Name   string            `json:"name"`
			Type   string            `json:"type"`
			Labels map[string]string `json:"labels"`
		} `json:"placement_group"`
	}

	d.run(t, "init", "-no-color")

	name1, labels1 := harness.runID+"-tofu-pg", map[string]string{"env": "e2e"}
	d.writeMain(t, main(name1, labels1))
	d.run(t, "apply", "-auto-approve", "-no-color")
	id := d.output(t, "id")
	var got group
	if status, err := e.apiGet(ctx, "/placement_groups/"+id, &got); err != nil || status != http.StatusOK {
		t.Fatalf("GET /placement_groups/%s after create: status %d, %v", id, status, err)
	}
	if pg := got.PlacementGroup; pg.Name != name1 || pg.Type != "spread" || !maps.Equal(pg.Labels, labels1) {
		t.Errorf("created group %+v, want name %q, type spread, labels %v", pg, name1, labels1)
	}

	name2, labels2 := harness.runID+"-tofu-pg-renamed", map[string]string{"env": "e2e", "stage": "two"}
	d.writeMain(t, main(name2, labels2))
	r := d.run(t, "apply", "-auto-approve", "-no-color")
	if !strings.Contains(r.Stdout, updatedInPlace) {
		t.Errorf("second apply is no in-place update (want %q):\n%s", updatedInPlace, r.Stdout)
	}
	if id2 := d.output(t, "id"); id2 != id {
		t.Errorf("ID changed from %s to %s; want an in-place update", id, id2)
	}
	got = group{}
	if status, err := e.apiGet(ctx, "/placement_groups/"+id, &got); err != nil || status != http.StatusOK {
		t.Fatalf("GET /placement_groups/%s after update: status %d, %v", id, status, err)
	}
	if pg := got.PlacementGroup; pg.Name != name2 || !maps.Equal(pg.Labels, labels2) {
		t.Errorf("updated group %+v, want name %q, labels %v", pg, name2, labels2)
	}

	d.run(t, "destroy", "-auto-approve", "-no-color")
	assertGone(t, e, "/placement_groups", "placement_groups", id)
}

// assertGone fails unless the list endpoint no longer shows the ID.
func assertGone(t *testing.T, e *env, path, key, id string) {
	t.Helper()
	want, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		t.Fatalf("tofu output id %q: %v", id, err)
	}
	ids, err := e.apiIDs(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(ids, want) {
		t.Errorf("GET %s still lists %d after tofu destroy", path, want)
	}
}

// hclMap writes a map of strings as an HCL object, keys sorted.
func hclMap(m map[string]string) string {
	var b strings.Builder
	b.WriteString("{\n")
	for _, k := range slices.Sorted(maps.Keys(m)) {
		fmt.Fprintf(&b, "    %q = %q\n", k, m[k])
	}
	b.WriteString("  }")
	return b.String()
}
