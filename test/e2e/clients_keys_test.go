//go:build e2e

package e2e

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-01", Name: "ssh_keys", Run: testCLI01SSHKeys})
	registerRow(clientRow{Req: "CLI-01", Name: "placement_groups", Run: testCLI01PlacementGroups})
	registerRow(clientRow{Req: "CLI-03", Name: "ssh_keys", Run: testCLI03SSHKeys})
	registerRow(clientRow{Req: "CLI-03", Name: "placement_groups", Run: testCLI03PlacementGroups})
}

// newKeyPair makes an SSH key of its own for a row (SSH keys are unique by
// fingerprint, and the run's key is registered already) and returns the
// public key file and its content.
func newKeyPair(t *testing.T, comment string) (string, string) {
	t.Helper()
	key := filepath.Join(t.TempDir(), "id_ed25519")
	if r, err := command(context.Background(), time.Minute, nil, "", harness.sshKeygen, "-q", "-t", "ed25519", "-N", "", "-C", comment, "-f", key); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, r)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	return key + ".pub", strings.TrimSpace(string(pub))
}

// labelled is an API object with a name, labels and, for keys, a
// fingerprint, for placement groups a type.
type labelled struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Labels      map[string]string `json:"labels"`
	Fingerprint string            `json:"fingerprint"`
	Type        string            `json:"type"`
}

// getLabelled is GET <path>/<id> with the object under key.
func (e *env) getLabelled(t *testing.T, path, key string, id int64) labelled {
	t.Helper()
	var body map[string]labelled
	e.getOK(t, path+"/"+strconv.FormatInt(id, 10), &body)
	return body[key]
}

// testCLI01SSHKeys: hcloud creates, lists, describes, updates, labels and
// deletes an SSH key.
func testCLI01SSHKeys(t *testing.T, c *rowCtx) {
	e := c.E
	pubFile, _ := newKeyPair(t, "hlab-e2e-cli01")
	testCLI01Labelled(t, e, "ssh-key", "/ssh_keys", "ssh_keys", "ssh_key", resName("hcloud", "ssh-keys"),
		[]string{"--public-key-from-file", pubFile}, func(listed, got labelled) bool { return listed.Fingerprint == got.Fingerprint && got.Fingerprint != "" })
}

// testCLI01PlacementGroups: hcloud creates, lists, describes, updates,
// labels and deletes a placement group.
func testCLI01PlacementGroups(t *testing.T, c *rowCtx) {
	testCLI01Labelled(t, c.E, "placement-group", "/placement_groups", "placement_groups", "placement_group", resName("hcloud", "placement-groups"),
		[]string{"--type", "spread"}, func(_, got labelled) bool { return got.Type == "spread" })
}

// testCLI01Labelled runs create, list, describe, update, add-label,
// remove-label and delete of an hcloud command whose resource has only a
// name and labels to change.
func testCLI01Labelled(t *testing.T, e *env, cmd, path, listKey, key, name string, create []string, same func(described, got labelled) bool) {
	t.Helper()
	renamed := name + "-renamed"
	t.Cleanup(func() {
		for _, n := range []string{name, renamed} {
			_, _ = e.hcloudRun(context.Background(), 2*time.Minute, cmd, "delete", n)
		}
	})
	e.hcloudOK(t, append([]string{cmd, "create", "--name", name, "--label", "row=cli-01-" + key}, create...)...)

	var listed []labelled
	e.hcloudJSON(t, &listed, cmd, "list", "-l", "row=cli-01-"+key)
	if len(listed) != 1 || listed[0].Name != name {
		t.Fatalf("hcloud %s list -l row=cli-01-%s: %+v, want only %s", cmd, key, listed, name)
	}
	id := listed[0].ID
	got := e.getLabelled(t, path, key, id)
	if want := map[string]string{"row": "cli-01-" + key}; got.Name != name || !maps.Equal(got.Labels, want) {
		t.Errorf("created %s %q with labels %v, want %q with %v", key, got.Name, got.Labels, name, want)
	}
	var described labelled
	e.hcloudJSON(t, &described, cmd, "describe", name)
	if described.ID != id || !same(described, got) {
		t.Errorf("hcloud %s describe %s: %+v, the API has %+v", cmd, name, described, got)
	}

	e.hcloudOK(t, cmd, "update", "--name", renamed, name)
	e.hcloudOK(t, cmd, "add-label", renamed, "stage=two")
	e.hcloudOK(t, cmd, "remove-label", renamed, "row")
	got = e.getLabelled(t, path, key, id)
	if want := map[string]string{"stage": "two"}; got.Name != renamed || !maps.Equal(got.Labels, want) {
		t.Errorf("updated %s %q with labels %v, want %q with %v", key, got.Name, got.Labels, renamed, want)
	}

	e.hcloudOK(t, cmd, "delete", renamed)
	assertGoneIDs(t, e, path, listKey, id)
}

// testCLI03SSHKeys: ssh_key creates, updates and deletes a key;
// ssh_key_info finds it.
func testCLI03SSHKeys(t *testing.T, _ *rowCtx) {
	e, w := sharedAnsible(t)
	_, pub := newKeyPair(t, "hlab-e2e-cli03")
	name := resName("ansible", "ssh-keys")
	t.Cleanup(func() { _, _ = e.hcloudRun(context.Background(), 2*time.Minute, "ssh-key", "delete", name) })
	row := map[string]any{"name": name, "pub": pub}
	key := func(stage string) string {
		return `    - name: ssh key
      hetzner.hcloud.ssh_key:
        name: "{{ row.name }}"
        public_key: "{{ row.pub }}"
        labels:
          row: cli-03-ssh-keys
          stage: ` + stage + `
        state: present
`
	}

	e.ansibleOK(t, w, "cli03-ssh-keys-create", localPlay(t, row, key("one")))
	var described labelled
	e.hcloudJSON(t, &described, "ssh-key", "describe", name)
	id := described.ID
	got := e.getLabelled(t, "/ssh_keys", "ssh_key", id)
	if want := map[string]string{"row": "cli-03-ssh-keys", "stage": "one"}; got.Name != name || !maps.Equal(got.Labels, want) {
		t.Errorf("created key %q with labels %v, want %q with %v", got.Name, got.Labels, name, want)
	}

	row["id"], row["fingerprint"] = id, got.Fingerprint
	e.ansibleOK(t, w, "cli03-ssh-keys-update", localPlay(t, row, key("two")+`    - name: by name
      hetzner.hcloud.ssh_key_info:
        name: "{{ row.name }}"
      register: by_name
    - name: by label selector
      hetzner.hcloud.ssh_key_info:
        label_selector: row=cli-03-ssh-keys
      register: by_label
    - name: check the answers
      ansible.builtin.assert:
        that:
          - by_name.hcloud_ssh_key_info | length == 1
          - by_name.hcloud_ssh_key_info[0].id | int == row.id
          - by_name.hcloud_ssh_key_info[0].fingerprint == row.fingerprint
          - by_name.hcloud_ssh_key_info[0].labels.stage == "two"
          - by_label.hcloud_ssh_key_info | map(attribute='name') | list == [row.name]
`))
	got = e.getLabelled(t, "/ssh_keys", "ssh_key", id)
	if want := map[string]string{"row": "cli-03-ssh-keys", "stage": "two"}; !maps.Equal(got.Labels, want) {
		t.Errorf("updated key labels %v, want %v", got.Labels, want)
	}

	e.ansibleOK(t, w, "cli03-ssh-keys-absent", localPlay(t, row, `    - name: ssh key absent
      hetzner.hcloud.ssh_key:
        name: "{{ row.name }}"
        state: absent
`))
	assertGoneIDs(t, e, "/ssh_keys", "ssh_keys", id)
}

// testCLI03PlacementGroups: placement_group creates, updates and deletes a
// placement group.
func testCLI03PlacementGroups(t *testing.T, _ *rowCtx) {
	e, w := sharedAnsible(t)
	name := resName("ansible", "placement-groups")
	t.Cleanup(func() { _, _ = e.hcloudRun(context.Background(), 2*time.Minute, "placement-group", "delete", name) })
	row := map[string]any{"name": name}
	group := func(stage string) string {
		return `    - name: placement group
      hetzner.hcloud.placement_group:
        name: "{{ row.name }}"
        type: spread
        labels:
          row: cli-03-placement-groups
          stage: ` + stage + `
        state: present
`
	}

	e.ansibleOK(t, w, "cli03-placement-groups-create", localPlay(t, row, group("one")))
	var described labelled
	e.hcloudJSON(t, &described, "placement-group", "describe", name)
	id := described.ID
	got := e.getLabelled(t, "/placement_groups", "placement_group", id)
	if want := map[string]string{"row": "cli-03-placement-groups", "stage": "one"}; got.Name != name || got.Type != "spread" || !maps.Equal(got.Labels, want) {
		t.Errorf("created group %+v, want %q of type spread with %v", got, name, want)
	}

	e.ansibleOK(t, w, "cli03-placement-groups-update", localPlay(t, row, group("two")))
	got = e.getLabelled(t, "/placement_groups", "placement_group", id)
	if want := map[string]string{"row": "cli-03-placement-groups", "stage": "two"}; !maps.Equal(got.Labels, want) {
		t.Errorf("updated group labels %v, want %v", got.Labels, want)
	}

	e.ansibleOK(t, w, "cli03-placement-groups-absent", localPlay(t, row, `    - name: placement group absent
      hetzner.hcloud.placement_group:
        name: "{{ row.name }}"
        state: absent
`))
	assertGoneIDs(t, e, "/placement_groups", "placement_groups", id)
}
