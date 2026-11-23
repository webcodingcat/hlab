//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-06", Name: "storage_boxes", Run: testCLI06StorageBoxes})
	registerRow(clientRow{Req: "CLI-06", Name: "subaccounts", Run: testCLI06Subaccounts})
	registerRow(clientRow{Req: "CLI-06", Name: "snapshots", Run: testCLI06Snapshots})
	registerRow(clientRow{Req: "CLI-06", Name: "storage_box_types", Run: testCLI06StorageBoxTypes})
}

// flatBox is a storage box as the OpenTofu data sources and the Ansible
// modules describe it: type and location by name, the protection as a flag.
type flatBox struct {
	ID               int64             `json:"id"`
	Name             string            `json:"name"`
	Username         string            `json:"username"`
	Server           string            `json:"server"`
	StorageBoxType   string            `json:"storage_box_type"`
	Location         string            `json:"location"`
	Labels           map[string]string `json:"labels"`
	DeleteProtection bool              `json:"delete_protection"`
	AccessSettings   boxAccess         `json:"access_settings"`
}

// summary is boxInfo.summary of the same box.
func (f flatBox) summary() string {
	b := boxInfo{ID: f.ID, Name: f.Name, Username: &f.Username, Server: &f.Server, Labels: f.Labels, AccessSettings: f.AccessSettings}
	b.StorageBoxType.Name = f.StorageBoxType
	b.Location.Name = f.Location
	b.Protection.Delete = f.DeleteProtection
	return b.summary()
}

// snapSummary is what a client's view of a snapshot must share with the
// API's.
func snapSummary(s boxSnapshot) string {
	return fmt.Sprintf("id=%d name=%s description=%q labels=%v automatic=%t",
		s.ID, s.Name, s.Description, labelsOf(s.Labels), s.IsAutomatic)
}

// boxTypeInfo is a storage box type as the API, the OpenTofu data sources
// and the Ansible module describe it (the deprecation left out).
type boxTypeInfo struct {
	ID                     int64  `json:"id"`
	Name                   string `json:"name"`
	Description            string `json:"description"`
	Size                   int64  `json:"size"`
	SnapshotLimit          int    `json:"snapshot_limit"`
	AutomaticSnapshotLimit int    `json:"automatic_snapshot_limit"`
	SubaccountsLimit       int    `json:"subaccounts_limit"`
}

// apiBoxTypes lists the storage box types the API serves, sorted by ID.
func (e *env) apiBoxTypes(t *testing.T) []boxTypeInfo {
	t.Helper()
	var body struct {
		Types []boxTypeInfo `json:"storage_box_types"`
	}
	e.hetznerGetOK(t, "/storage_box_types?per_page=50", &body)
	return sortedTypes(body.Types)
}

// apiBoxType reads the type name through GET /storage_box_types/{id}.
func (e *env) apiBoxType(t *testing.T, name string) boxTypeInfo {
	t.Helper()
	var list struct {
		Types []boxTypeInfo `json:"storage_box_types"`
	}
	e.hetznerGetOK(t, "/storage_box_types?name="+name, &list)
	if len(list.Types) != 1 {
		t.Fatalf("GET /storage_box_types?name=%s: %d types, want 1", name, len(list.Types))
	}
	var one struct {
		Type boxTypeInfo `json:"storage_box_type"`
	}
	e.hetznerGetOK(t, fmt.Sprintf("/storage_box_types/%d", list.Types[0].ID), &one)
	return one.Type
}

// sortedTypes returns the types sorted by ID.
func sortedTypes(types []boxTypeInfo) []boxTypeInfo {
	types = slices.Clone(types)
	slices.SortFunc(types, func(a, b boxTypeInfo) int { return int(a.ID - b.ID) })
	return types
}

// decodeValues turns values a client returned (OpenTofu state, Ansible
// results) into out through JSON.
func decodeValues(t *testing.T, what string, v, out any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err == nil {
		err = json.Unmarshal(b, out)
	}
	if err != nil {
		t.Fatalf("%s: %v\n%s", what, err, b)
	}
}

// publicKey is the run's SSH public key in OpenSSH form.
func (e *env) publicKey(t *testing.T) string {
	t.Helper()
	key, err := os.ReadFile(e.pubKey)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(key))
}

// assertInPlace fails unless an apply changed exactly one resource in
// place.
func assertInPlace(t *testing.T, what string, r result) {
	t.Helper()
	if want := "0 added, 1 changed, 0 destroyed"; !strings.Contains(r.Stdout, want) {
		t.Errorf("%s is no in-place update (want %q):\n%s", what, want, r.Stdout)
	}
}

// tfBoxConf is one state of the hcloud_storage_box of CLI-06.
type tfBoxConf struct {
	Name, Type, Password string
	Access               boxAccess
	Plan                 string // HCL object or "null"
	Protect              bool
	Labels               string // HCL object
}

// testCLI06StorageBoxes: OpenTofu with HCLOUD_ENDPOINT and HETZNER_ENDPOINT
// only creates a box with every setting, changes each of them in place,
// reads it through the data sources and destroys it while it is protected.
func testCLI06StorageBoxes(t *testing.T, _ *rowCtx) {
	sharedTofu(t)
	e := boxProjectEnv(t, "cli06-boxes")
	name, renamed := resName("cli06", "box"), resName("cli06", "box2")
	cleanupBoxesNamed(t, e, name, renamed)
	key := e.publicKey(t)
	d := e.newTofuDir(t, "storage-boxes")
	main := func(c tfBoxConf) string {
		return fmt.Sprintf(`resource "hcloud_storage_box" "b" {
  name              = %q
  storage_box_type  = %q
  location          = %q
  password          = %q
  ssh_keys          = [%q]
  delete_protection = %t
  labels            = %s

  access_settings = {
    reachable_externally = %t
    samba_enabled        = %t
    ssh_enabled          = %t
    webdav_enabled       = false
    zfs_enabled          = %t
  }

  snapshot_plan = %s
}

data "hcloud_storage_box" "by_id" {
  id         = hcloud_storage_box.b.id
  depends_on = [hcloud_storage_box.b]
}

data "hcloud_storage_box" "by_name" {
  name       = hcloud_storage_box.b.name
  depends_on = [hcloud_storage_box.b]
}

data "hcloud_storage_boxes" "by_label" {
  with_selector = "row=cli-06-boxes"
  depends_on    = [hcloud_storage_box.b]
}

output "id" {
  value = hcloud_storage_box.b.id
}
`, c.Name, c.Type, serverLocation, c.Password, key, c.Protect, c.Labels,
			c.Access.ReachableExternally, c.Access.SambaEnabled, c.Access.SSHEnabled, c.Access.ZFSEnabled, c.Plan)
	}
	// checkData compares the three data sources with the API's box.
	checkData := func(step string, api boxInfo) {
		t.Helper()
		for _, addr := range []string{"data.hcloud_storage_box.by_id", "data.hcloud_storage_box.by_name"} {
			var got flatBox
			decodeValues(t, addr, d.dataValues(t, addr), &got)
			if got.summary() != api.summary() {
				t.Errorf("%s: %s: %s, the API has %s", step, addr, got.summary(), api.summary())
			}
		}
		var listed []flatBox
		decodeValues(t, "data.hcloud_storage_boxes.by_label", d.dataValues(t, "data.hcloud_storage_boxes.by_label")["storage_boxes"], &listed)
		if len(listed) != 1 || listed[0].summary() != api.summary() {
			t.Errorf("%s: data hcloud_storage_boxes with row=cli-06-boxes: %d boxes %v, the API has only %s", step, len(listed), listed, api.summary())
		}
	}
	d.run(t, "init", "-no-color")

	// create; the provider counts Sunday as 0, the API as 7 (E14)
	conf := tfBoxConf{
		Name: name, Type: "bx11", Password: boxPassword(),
		Access: boxAccess{ReachableExternally: true, SSHEnabled: true},
		Plan: `{
    max_snapshots = 5
    hour          = 3
    minute        = 15
    day_of_week   = 0
  }`,
		Protect: true,
		Labels:  `{ row = "cli-06-boxes", tier = "one" }`,
	}
	d.writeMain(t, main(conf))
	d.run(t, "apply", "-auto-approve", "-no-color")
	id := parseID(t, d.output(t, "id"))
	got := e.boxByID(t, id)
	if got.Name != name || got.StorageBoxType.Name != "bx11" || got.Location.Name != serverLocation ||
		got.AccessSettings != conf.Access || !got.Protection.Delete || got.Username == nil || *got.Username == "" {
		t.Errorf("created box %s, want %s, bx11, %s, access %+v, protected and a username", got.summary(), name, serverLocation, conf.Access)
	}
	assertLabels(t, "created box", got.Labels, map[string]string{"row": "cli-06-boxes", "tier": "one"})
	if p := got.SnapshotPlan; p == nil || p.DayOfWeek == nil || *p.DayOfWeek != 7 || p.Hour == nil || *p.Hour != 3 ||
		p.Minute == nil || *p.Minute != 15 || p.MaxSnapshots != 5 || p.DayOfMonth != nil {
		t.Errorf("created box: plan %s, want Sunday (7) 03:15, 5 snapshots", planString(p))
	}
	checkData("apply", got)

	// in place: name, labels, type, password, access settings, plan and
	// protection
	conf = tfBoxConf{
		Name: renamed, Type: "bx21", Password: boxPassword(),
		Access: boxAccess{SambaEnabled: true, ZFSEnabled: true},
		Plan: `{
    max_snapshots = 3
    hour          = 4
    minute        = 30
    day_of_month  = 15
  }`,
		Protect: false,
		Labels:  `{ row = "cli-06-boxes", stage = "two" }`,
	}
	d.writeMain(t, main(conf))
	assertInPlace(t, "the update of the box", d.run(t, "apply", "-auto-approve", "-no-color"))
	got = e.boxByID(t, id)
	if got.Name != renamed || got.StorageBoxType.Name != "bx21" || got.AccessSettings != conf.Access || got.Protection.Delete {
		t.Errorf("updated box %s, want %s, bx21, access %+v, unprotected", got.summary(), renamed, conf.Access)
	}
	assertLabels(t, "updated box", got.Labels, map[string]string{"row": "cli-06-boxes", "stage": "two"})
	if p := got.SnapshotPlan; p == nil || p.DayOfWeek != nil || p.DayOfMonth == nil || *p.DayOfMonth != 15 ||
		p.Hour == nil || *p.Hour != 4 || p.Minute == nil || *p.Minute != 30 || p.MaxSnapshots != 3 {
		t.Errorf("updated box: plan %s, want day 15 of the month 04:30, 3 snapshots", planString(p))
	}
	e.assertBoxActionDone(t, id, "reset_password")
	checkData("update", got)

	// protection back on, so the destroy meets a protected box
	conf.Protect = true
	d.writeMain(t, main(conf))
	assertInPlace(t, "turning the protection on", d.run(t, "apply", "-auto-approve", "-no-color"))
	if got = e.boxByID(t, id); !got.Protection.Delete {
		t.Error("after delete_protection = true: protection.delete is false")
	}

	d.run(t, "destroy", "-auto-approve", "-no-color")
	e.assertHetznerGone(t, fmt.Sprintf("/storage_boxes/%d", id))
}

// testCLI06Subaccounts: OpenTofu creates a subaccount of a box made through
// the API, changes it in place, reads it through the data sources and
// destroys it.
func testCLI06Subaccounts(t *testing.T, _ *rowCtx) {
	sharedTofu(t)
	e := boxProjectEnv(t, "cli06-subs")
	box := newTestBox(t, e, "cli06-subs", "bx11", map[string]bool{"ssh_enabled": true})
	d := e.newTofuDir(t, "storage-box-subaccounts")
	main := func(description, home, password string, a boxAccess) string {
		return fmt.Sprintf(`resource "hcloud_storage_box_subaccount" "s" {
  storage_box_id = %[1]d
  name           = "cli06sub"
  description    = %[2]q
  home_directory = %[3]q
  password       = %[4]q
  labels         = { row = "cli-06-subaccounts" }

  access_settings = {
    reachable_externally = %[5]t
    readonly             = %[6]t
    samba_enabled        = %[7]t
    ssh_enabled          = %[8]t
    webdav_enabled       = false
  }
}

data "hcloud_storage_box_subaccount" "by_id" {
  storage_box_id = %[1]d
  id             = hcloud_storage_box_subaccount.s.id
  depends_on     = [hcloud_storage_box_subaccount.s]
}

data "hcloud_storage_box_subaccount" "by_name" {
  storage_box_id = %[1]d
  name           = hcloud_storage_box_subaccount.s.name
  depends_on     = [hcloud_storage_box_subaccount.s]
}

data "hcloud_storage_box_subaccounts" "all" {
  storage_box_id = %[1]d
  depends_on     = [hcloud_storage_box_subaccount.s]
}

output "id" {
  value = hcloud_storage_box_subaccount.s.id
}
`, box.ID, description, home, password, a.ReachableExternally, a.Readonly, a.SambaEnabled, a.SSHEnabled)
	}
	checkData := func(step string, api subaccountInfo) {
		t.Helper()
		for _, addr := range []string{"data.hcloud_storage_box_subaccount.by_id", "data.hcloud_storage_box_subaccount.by_name"} {
			var got subaccountInfo
			decodeValues(t, addr, d.dataValues(t, addr), &got)
			if got.summary() != api.summary() {
				t.Errorf("%s: %s: %s, the API has %s", step, addr, got.summary(), api.summary())
			}
		}
		var listed []subaccountInfo
		decodeValues(t, "data.hcloud_storage_box_subaccounts.all", d.dataValues(t, "data.hcloud_storage_box_subaccounts.all")["subaccounts"], &listed)
		if len(listed) != 1 || listed[0].summary() != api.summary() {
			t.Errorf("%s: data hcloud_storage_box_subaccounts: %d subaccounts %v, the API has only %s", step, len(listed), listed, api.summary())
		}
	}
	d.run(t, "init", "-no-color")

	access := boxAccess{SSHEnabled: true, Readonly: true}
	d.writeMain(t, main("first", "cli06/home", boxPassword(), access))
	d.run(t, "apply", "-auto-approve", "-no-color")
	id := parseID(t, d.output(t, "id"))
	got := e.subaccountByID(t, box.ID, id)
	if got.Name != "cli06sub" || got.Description != "first" || got.HomeDirectory != "cli06/home" ||
		got.AccessSettings != access || got.Username == "" {
		t.Errorf("created subaccount %s, want cli06sub, first, home cli06/home, access %+v and a username", got.summary(), access)
	}
	assertLabels(t, "created subaccount", got.Labels, map[string]string{"row": "cli-06-subaccounts"})
	checkData("apply", got)

	// in place: home directory, password, access settings, description
	access = boxAccess{SSHEnabled: true, SambaEnabled: true}
	d.writeMain(t, main("second", "cli06/other", boxPassword(), access))
	assertInPlace(t, "the update of the subaccount", d.run(t, "apply", "-auto-approve", "-no-color"))
	got = e.subaccountByID(t, box.ID, id)
	if got.Description != "second" || got.HomeDirectory != "cli06/other" || got.AccessSettings != access {
		t.Errorf("updated subaccount %s, want second, home cli06/other, access %+v", got.summary(), access)
	}
	e.assertBoxActionDone(t, box.ID, "reset_subaccount_password")
	checkData("update", got)

	d.run(t, "destroy", "-auto-approve", "-no-color")
	e.assertHetznerGone(t, fmt.Sprintf("/storage_boxes/%d/subaccounts/%d", box.ID, id))
}

// testCLI06Snapshots: OpenTofu creates a snapshot of a box made through the
// API, changes its description in place, reads it through the data sources
// and destroys it.
func testCLI06Snapshots(t *testing.T, _ *rowCtx) {
	sharedTofu(t)
	e := boxProjectEnv(t, "cli06-snaps")
	box := newTestBox(t, e, "cli06-snaps", "bx11", map[string]bool{"ssh_enabled": true})
	d := e.newTofuDir(t, "storage-box-snapshots")
	main := func(description string) string {
		return fmt.Sprintf(`resource "hcloud_storage_box_snapshot" "s" {
  storage_box_id = %[1]d
  description    = %[2]q
  labels         = { row = "cli-06-snapshots" }
}

data "hcloud_storage_box_snapshot" "by_id" {
  storage_box_id = %[1]d
  id             = hcloud_storage_box_snapshot.s.id
  depends_on     = [hcloud_storage_box_snapshot.s]
}

data "hcloud_storage_box_snapshot" "by_name" {
  storage_box_id = %[1]d
  name           = hcloud_storage_box_snapshot.s.name
  depends_on     = [hcloud_storage_box_snapshot.s]
}

data "hcloud_storage_box_snapshots" "all" {
  storage_box_id = %[1]d
  depends_on     = [hcloud_storage_box_snapshot.s]
}

output "id" {
  value = hcloud_storage_box_snapshot.s.id
}
`, box.ID, description)
	}
	checkData := func(step string, api boxSnapshot) {
		t.Helper()
		for _, addr := range []string{"data.hcloud_storage_box_snapshot.by_id", "data.hcloud_storage_box_snapshot.by_name"} {
			var got boxSnapshot
			decodeValues(t, addr, d.dataValues(t, addr), &got)
			if snapSummary(got) != snapSummary(api) {
				t.Errorf("%s: %s: %s, the API has %s", step, addr, snapSummary(got), snapSummary(api))
			}
		}
		var listed []boxSnapshot
		decodeValues(t, "data.hcloud_storage_box_snapshots.all", d.dataValues(t, "data.hcloud_storage_box_snapshots.all")["snapshots"], &listed)
		if len(listed) != 1 || snapSummary(listed[0]) != snapSummary(api) {
			t.Errorf("%s: data hcloud_storage_box_snapshots: %d snapshots %v, the API has only %s", step, len(listed), listed, snapSummary(api))
		}
	}
	d.run(t, "init", "-no-color")

	d.writeMain(t, main("cli06-first"))
	d.run(t, "apply", "-auto-approve", "-no-color")
	id := parseID(t, d.output(t, "id"))
	got, status, err := e.getSnapshot(box, id)
	if err != nil || got.Description != "cli06-first" || got.Name == "" {
		t.Errorf("created snapshot %s (status %d %v), want description cli06-first and a name", snapSummary(got), status, err)
	}
	assertLabels(t, "created snapshot", got.Labels, map[string]string{"row": "cli-06-snapshots"})
	checkData("apply", got)

	d.writeMain(t, main("cli06-updated"))
	assertInPlace(t, "the description change", d.run(t, "apply", "-auto-approve", "-no-color"))
	got, status, err = e.getSnapshot(box, id)
	if err != nil || got.Description != "cli06-updated" {
		t.Errorf("updated snapshot %s (status %d %v), want description cli06-updated", snapSummary(got), status, err)
	}
	checkData("update", got)

	d.run(t, "destroy", "-auto-approve", "-no-color")
	e.assertHetznerGone(t, fmt.Sprintf("%s/%d", snapshotsPath(box), id))
}

// testCLI06StorageBoxTypes: the data sources hcloud_storage_box_type by
// name and ID and hcloud_storage_box_types match /storage_box_types.
func testCLI06StorageBoxTypes(t *testing.T, _ *rowCtx) {
	e := *sharedTofu(t)
	bx11 := e.apiBoxType(t, "bx11")
	d := e.newTofuDir(t, "storage-box-types")
	d.writeMain(t, fmt.Sprintf(`data "hcloud_storage_box_type" "by_name" {
  name = "bx11"
}

data "hcloud_storage_box_type" "by_id" {
  id = %d
}

data "hcloud_storage_box_types" "all" {}
`, bx11.ID))
	d.run(t, "init", "-no-color")

	// Other rows add and remove own types; the comparison is repeated until
	// both sides were read without such a change in between.
	var tofuTypes, apiTypes []boxTypeInfo
	err := waitFor(3*time.Minute, 5*time.Second, func() (bool, error) {
		d.run(t, "apply", "-auto-approve", "-no-color")
		var listed []boxTypeInfo
		decodeValues(t, "data.hcloud_storage_box_types.all", d.dataValues(t, "data.hcloud_storage_box_types.all")["storage_box_types"], &listed)
		tofuTypes, apiTypes = sortedTypes(listed), e.apiBoxTypes(t)
		return slices.Equal(tofuTypes, apiTypes), nil
	})
	if err != nil || len(apiTypes) == 0 {
		t.Errorf("data hcloud_storage_box_types: %+v, the API has %+v (%v)", tofuTypes, apiTypes, err)
	}
	for _, addr := range []string{"data.hcloud_storage_box_type.by_name", "data.hcloud_storage_box_type.by_id"} {
		var got boxTypeInfo
		decodeValues(t, addr, d.dataValues(t, addr), &got)
		if got != bx11 {
			t.Errorf("%s: %+v, the API has %+v", addr, got, bx11)
		}
	}
}
