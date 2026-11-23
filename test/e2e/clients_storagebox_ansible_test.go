//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-07", Name: "storage_boxes", Run: testCLI07StorageBoxes})
	registerRow(clientRow{Req: "CLI-07", Name: "subaccounts", Run: testCLI07Subaccounts})
	registerRow(clientRow{Req: "CLI-07", Name: "snapshots", Run: testCLI07Snapshots})
	registerRow(clientRow{Req: "CLI-07", Name: "storage_box_types", Run: testCLI07StorageBoxTypes})
}

// ansibleDump runs tasks and then writes the values of exprs (Jinja
// expressions of registered results, by key) as one JSON object into a
// file in the distro, which it decodes into out.
func (e *env) ansibleDump(t *testing.T, w *wslEnv, name string, row map[string]any, tasks string, exprs map[string]string, out any) {
	t.Helper()
	file := w.dir + "/" + name + ".json"
	row = maps.Clone(row)
	if row == nil {
		row = map[string]any{}
	}
	row["out"] = file
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(exprs)) {
		parts = append(parts, fmt.Sprintf("'%s': %s", k, exprs[k]))
	}
	e.ansibleOK(t, w, name, localPlay(t, row, tasks+`    - name: write the answers
      ansible.builtin.copy:
        dest: "{{ row.out }}"
        mode: "0600"
        content: "{{ {`+strings.Join(parts, ", ")+`} | to_json }}"
`))
	r, err := w.run(context.Background(), time.Minute, nil, "cat -- '"+file+"'")
	if err != nil {
		t.Fatalf("reading %s in distro %s: %v\n%s", file, w.distro, err, r)
	}
	if err := json.Unmarshal([]byte(r.Stdout), out); err != nil {
		t.Fatalf("answers of playbook %s: %v\n%s", name, err, r.Stdout)
	}
}

// testCLI07StorageBoxes: Ansible from WSL2 with HCLOUD_ENDPOINT and
// HETZNER_ENDPOINT only creates a box with every setting, changes it by
// id, resets its password, reads it through storage_box_info and removes
// it.
func testCLI07StorageBoxes(t *testing.T, _ *rowCtx) {
	_, w := sharedAnsible(t)
	e := boxProjectEnv(t, "cli07-boxes")
	name, renamed := resName("cli07", "box"), resName("cli07", "box2")
	cleanupBoxesNamed(t, e, name, renamed)
	row := map[string]any{
		"name": name, "renamed": renamed, "location": serverLocation, "password": boxPassword(), "key": e.publicKey(t),
	}

	e.ansibleOK(t, w, "cli07-boxes-create", localPlay(t, row, `    - name: storage box
      hetzner.hcloud.storage_box:
        name: "{{ row.name }}"
        storage_box_type: bx11
        location: "{{ row.location }}"
        password: "{{ row.password }}"
        ssh_keys:
          - "{{ row.key }}"
        access_settings:
          reachable_externally: true
          samba_enabled: false
          ssh_enabled: true
          webdav_enabled: false
          zfs_enabled: false
        snapshot_plan:
          max_snapshots: 5
          hour: 3
          minute: 15
          day_of_week: 7
        delete_protection: true
        labels:
          row: cli-07-boxes
          tier: one
        state: present
`))
	got := e.boxByName(t, name)
	id := got.ID
	wantAccess := boxAccess{ReachableExternally: true, SSHEnabled: true}
	if got.StorageBoxType.Name != "bx11" || got.Location.Name != serverLocation || got.AccessSettings != wantAccess ||
		!got.Protection.Delete || got.Username == nil || *got.Username == "" {
		t.Errorf("created box %s, want bx11, %s, access %+v, protected and a username", got.summary(), serverLocation, wantAccess)
	}
	assertLabels(t, "created box", got.Labels, map[string]string{"row": "cli-07-boxes", "tier": "one"})
	if p := got.SnapshotPlan; p == nil || p.DayOfWeek == nil || *p.DayOfWeek != 7 || p.Hour == nil || *p.Hour != 3 ||
		p.Minute == nil || *p.Minute != 15 || p.MaxSnapshots != 5 || p.DayOfMonth != nil {
		t.Errorf("created box: plan %s, want Sunday (7) 03:15, 5 snapshots", planString(p))
	}

	// second run by id: type, access settings, plan, protection and name
	row["id"] = id
	e.ansibleOK(t, w, "cli07-boxes-update", localPlay(t, row, `    - name: storage box by id
      hetzner.hcloud.storage_box:
        id: "{{ row.id }}"
        name: "{{ row.renamed }}"
        storage_box_type: bx21
        access_settings:
          reachable_externally: false
          samba_enabled: true
          ssh_enabled: false
          webdav_enabled: false
          zfs_enabled: true
        snapshot_plan:
          max_snapshots: 3
          hour: 4
          minute: 30
          day_of_week: null
          day_of_month: 15
        delete_protection: false
        state: present
`))
	got = e.boxByID(t, id)
	wantAccess = boxAccess{SambaEnabled: true, ZFSEnabled: true}
	if got.Name != renamed || got.StorageBoxType.Name != "bx21" || got.AccessSettings != wantAccess || got.Protection.Delete {
		t.Errorf("updated box %s, want %s, bx21, access %+v, unprotected", got.summary(), renamed, wantAccess)
	}
	if p := got.SnapshotPlan; p == nil || p.DayOfWeek != nil || p.DayOfMonth == nil || *p.DayOfMonth != 15 ||
		p.Hour == nil || *p.Hour != 4 || p.Minute == nil || *p.Minute != 30 || p.MaxSnapshots != 3 {
		t.Errorf("updated box: plan %s, want day 15 of the month 04:30, 3 snapshots", planString(p))
	}

	// reset_password and storage_box_info by ID, by name and all
	row["password2"] = boxPassword()
	var info map[string][]flatBox
	e.ansibleDump(t, w, "cli07-boxes-info", row, `    - name: reset the password
      hetzner.hcloud.storage_box:
        id: "{{ row.id }}"
        password: "{{ row.password2 }}"
        state: reset_password
    - name: info by id
      hetzner.hcloud.storage_box_info:
        id: "{{ row.id }}"
      register: by_id
    - name: info by name
      hetzner.hcloud.storage_box_info:
        name: "{{ row.renamed }}"
      register: by_name
    - name: info of all
      hetzner.hcloud.storage_box_info:
      register: all
`, map[string]string{
		"by_id": "by_id.hcloud_storage_box_info", "by_name": "by_name.hcloud_storage_box_info", "all": "all.hcloud_storage_box_info",
	}, &info)
	e.assertBoxActionDone(t, id, "reset_password")
	api := e.boxByID(t, id)
	for _, k := range []string{"by_id", "by_name", "all"} {
		if len(info[k]) != 1 || info[k][0].summary() != api.summary() {
			t.Errorf("storage_box_info %s: %d boxes %v, the API has only %s", k, len(info[k]), info[k], api.summary())
		}
	}

	e.ansibleOK(t, w, "cli07-boxes-absent", localPlay(t, row, `    - name: storage box absent
      hetzner.hcloud.storage_box:
        name: "{{ row.renamed }}"
        state: absent
`))
	e.assertHetznerGone(t, fmt.Sprintf("/storage_boxes/%d", id))
}

// testCLI07Subaccounts: storage_box_subaccount creates a subaccount of a box
// made through the API, changes it, resets its password and removes it;
// storage_box_subaccount_info shows it as the API does.
func testCLI07Subaccounts(t *testing.T, _ *rowCtx) {
	_, w := sharedAnsible(t)
	e := boxProjectEnv(t, "cli07-subs")
	box := newTestBox(t, e, "cli07-subs", "bx11", map[string]bool{"ssh_enabled": true})
	row := map[string]any{"box": box.ID, "password": boxPassword(), "password2": boxPassword()}

	e.ansibleOK(t, w, "cli07-subaccounts-create", localPlay(t, row, `    - name: subaccount
      hetzner.hcloud.storage_box_subaccount:
        storage_box: "{{ row.box }}"
        name: cli07sub
        home_directory: cli07/home
        password: "{{ row.password }}"
        description: first
        access_settings:
          reachable_externally: false
          readonly: true
          samba_enabled: false
          ssh_enabled: true
          webdav_enabled: false
        labels:
          row: cli-07-subaccounts
        state: present
`))
	got := e.subaccountByName(t, box.ID, "cli07sub")
	id := got.ID
	access := boxAccess{SSHEnabled: true, Readonly: true}
	if got.HomeDirectory != "cli07/home" || got.Description != "first" || got.AccessSettings != access || got.Username == "" {
		t.Errorf("created subaccount %s, want home cli07/home, first, access %+v and a username", got.summary(), access)
	}
	assertLabels(t, "created subaccount", got.Labels, map[string]string{"row": "cli-07-subaccounts"})

	// second run: home directory, access settings and description
	e.ansibleOK(t, w, "cli07-subaccounts-update", localPlay(t, row, `    - name: subaccount changed
      hetzner.hcloud.storage_box_subaccount:
        storage_box: "{{ row.box }}"
        name: cli07sub
        home_directory: cli07/other
        description: second
        access_settings:
          reachable_externally: false
          readonly: false
          samba_enabled: true
          ssh_enabled: true
          webdav_enabled: false
        state: present
`))
	got = e.subaccountByID(t, box.ID, id)
	access = boxAccess{SSHEnabled: true, SambaEnabled: true}
	if got.HomeDirectory != "cli07/other" || got.Description != "second" || got.AccessSettings != access {
		t.Errorf("updated subaccount %s, want home cli07/other, second, access %+v", got.summary(), access)
	}

	// reset_password and storage_box_subaccount_info by ID, by name and all
	row["id"] = id
	var info map[string][]subaccountInfo
	e.ansibleDump(t, w, "cli07-subaccounts-info", row, `    - name: reset the password
      hetzner.hcloud.storage_box_subaccount:
        storage_box: "{{ row.box }}"
        name: cli07sub
        password: "{{ row.password2 }}"
        state: reset_password
    - name: info by id
      hetzner.hcloud.storage_box_subaccount_info:
        storage_box: "{{ row.box }}"
        id: "{{ row.id }}"
      register: by_id
    - name: info by name
      hetzner.hcloud.storage_box_subaccount_info:
        storage_box: "{{ row.box }}"
        name: cli07sub
      register: by_name
    - name: info of all
      hetzner.hcloud.storage_box_subaccount_info:
        storage_box: "{{ row.box }}"
      register: all
`, map[string]string{
		"by_id":   "by_id.hcloud_storage_box_subaccount_info",
		"by_name": "by_name.hcloud_storage_box_subaccount_info",
		"all":     "all.hcloud_storage_box_subaccount_info",
	}, &info)
	e.assertBoxActionDone(t, box.ID, "reset_subaccount_password")
	api := e.subaccountByID(t, box.ID, id)
	for _, k := range []string{"by_id", "by_name", "all"} {
		if len(info[k]) != 1 || info[k][0].summary() != api.summary() {
			t.Errorf("storage_box_subaccount_info %s: %d subaccounts %v, the API has only %s", k, len(info[k]), info[k], api.summary())
		}
	}

	e.ansibleOK(t, w, "cli07-subaccounts-absent", localPlay(t, row, `    - name: subaccount absent
      hetzner.hcloud.storage_box_subaccount:
        storage_box: "{{ row.box }}"
        name: cli07sub
        state: absent
`))
	e.assertHetznerGone(t, fmt.Sprintf("/storage_boxes/%d/subaccounts/%d", box.ID, id))
}

// testCLI07Snapshots: storage_box_snapshot creates two snapshots of a box
// made through the API and updates a description, storage_box rolls back
// to the first (the second is gone), storage_box_snapshot_info shows them as
// the API does, and absent removes the first.
func testCLI07Snapshots(t *testing.T, _ *rowCtx) {
	_, w := sharedAnsible(t)
	e := boxProjectEnv(t, "cli07-snaps")
	box := newTestBox(t, e, "cli07-snaps", "bx11", map[string]bool{"ssh_enabled": true})
	row := map[string]any{"box": box.ID}

	// present without id or name creates a new snapshot each time
	e.ansibleOK(t, w, "cli07-snapshots-create", localPlay(t, row, `    - name: first snapshot
      hetzner.hcloud.storage_box_snapshot:
        storage_box: "{{ row.box }}"
        description: cli07-first
        labels:
          row: cli-07-snapshots
        state: present
    - name: second snapshot
      hetzner.hcloud.storage_box_snapshot:
        storage_box: "{{ row.box }}"
        description: cli07-second
        state: present
`))
	first := e.snapshotByDescription(t, box.ID, "cli07-first")
	second := e.snapshotByDescription(t, box.ID, "cli07-second")
	assertLabels(t, "first snapshot", first.Labels, map[string]string{"row": "cli-07-snapshots"})

	// description, rollback to the first, storage_box_snapshot_info
	row["first"], row["first_name"] = first.ID, first.Name
	var info map[string][]boxSnapshot
	e.ansibleDump(t, w, "cli07-snapshots-rollback", row, `    - name: description of the first
      hetzner.hcloud.storage_box_snapshot:
        storage_box: "{{ row.box }}"
        id: "{{ row.first }}"
        description: cli07-updated
        state: present
    - name: rollback to the first
      hetzner.hcloud.storage_box:
        id: "{{ row.box }}"
        snapshot: "{{ row.first }}"
        state: rollback_snapshot
    - name: info by id
      hetzner.hcloud.storage_box_snapshot_info:
        storage_box: "{{ row.box }}"
        id: "{{ row.first }}"
      register: by_id
    - name: info by name
      hetzner.hcloud.storage_box_snapshot_info:
        storage_box: "{{ row.box }}"
        name: "{{ row.first_name }}"
      register: by_name
    - name: info of all
      hetzner.hcloud.storage_box_snapshot_info:
        storage_box: "{{ row.box }}"
      register: all
`, map[string]string{
		"by_id":   "by_id.hcloud_storage_box_snapshot_info",
		"by_name": "by_name.hcloud_storage_box_snapshot_info",
		"all":     "all.hcloud_storage_box_snapshot_info",
	}, &info)
	api, status, err := e.getSnapshot(box, first.ID)
	if err != nil || api.Description != "cli07-updated" {
		t.Errorf("first snapshot after the update: %s (status %d %v), want description cli07-updated", snapSummary(api), status, err)
	}
	ids := snapshotIDs(e.snapshotsOf(t, box.ID))
	if !slices.Contains(ids, first.ID) || slices.Contains(ids, second.ID) {
		t.Errorf("after rollback_snapshot to %s: snapshots %v, want %d kept and %d gone", first.Name, ids, first.ID, second.ID)
	}
	e.assertHetznerGone(t, fmt.Sprintf("%s/%d", snapshotsPath(box), second.ID))
	for _, k := range []string{"by_id", "by_name", "all"} {
		if len(info[k]) != 1 || snapSummary(info[k][0]) != snapSummary(api) {
			t.Errorf("storage_box_snapshot_info %s: %d snapshots %v, the API has only %s", k, len(info[k]), info[k], snapSummary(api))
		}
	}

	e.ansibleOK(t, w, "cli07-snapshots-absent", localPlay(t, row, `    - name: first snapshot absent
      hetzner.hcloud.storage_box_snapshot:
        storage_box: "{{ row.box }}"
        id: "{{ row.first }}"
        state: absent
`))
	e.assertHetznerGone(t, fmt.Sprintf("%s/%d", snapshotsPath(box), first.ID))
}

// testCLI07StorageBoxTypes: storage_box_type_info by name and without a
// filter matches /storage_box_types.
func testCLI07StorageBoxTypes(t *testing.T, _ *rowCtx) {
	shared, w := sharedAnsible(t)
	e := *shared
	e.hcloudEnv = withHetznerEndpoint(shared.hcloudEnv)
	bx11 := e.apiBoxType(t, "bx11")

	// Other rows add and remove own types; the comparison is repeated until
	// both sides were read without such a change in between.
	var info map[string][]boxTypeInfo
	var apiTypes []boxTypeInfo
	err := waitFor(3*time.Minute, 5*time.Second, func() (bool, error) {
		info = nil
		e.ansibleDump(t, w, "cli07-storage-box-types", nil, `    - name: info by name
      hetzner.hcloud.storage_box_type_info:
        name: bx11
      register: by_name
    - name: info of all
      hetzner.hcloud.storage_box_type_info:
      register: all
`, map[string]string{
			"by_name": "by_name.hcloud_storage_box_type_info", "all": "all.hcloud_storage_box_type_info",
		}, &info)
		apiTypes = e.apiBoxTypes(t)
		return slices.Equal(sortedTypes(info["all"]), apiTypes), nil
	})
	if err != nil || len(apiTypes) == 0 {
		t.Errorf("storage_box_type_info without a filter: %+v, the API has %+v (%v)", info["all"], apiTypes, err)
	}
	if len(info["by_name"]) != 1 || info["by_name"][0] != bx11 {
		t.Errorf("storage_box_type_info name bx11: %+v, the API has %+v", info["by_name"], bx11)
	}
}
