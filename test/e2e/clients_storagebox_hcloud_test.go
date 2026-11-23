//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-05", Name: "storage_boxes", Run: testCLI05StorageBoxes})
	registerRow(clientRow{Req: "CLI-05", Name: "subaccounts", Run: testCLI05Subaccounts})
	registerRow(clientRow{Req: "CLI-05", Name: "snapshots", Run: testCLI05Snapshots})
	registerRow(clientRow{Req: "CLI-05", Name: "storage_box_types", Run: testCLI05StorageBoxTypes})
	registerRow(clientRow{Req: "CLI-05", Name: "all", Slots: 1, Run: testCLI05All})
}

// testCLI05StorageBoxes: hcloud with only hlab's endpoints manages a box
// through every box command, each step checked through the API.
func testCLI05StorageBoxes(t *testing.T, _ *rowCtx) {
	e := boxProjectEnv(t, "cli05-boxes")
	name, renamed := resName("cli05", "box"), resName("cli05", "box2")
	cleanupBoxesNamed(t, e, name, renamed)

	// create with type, location, password, SSH key, access settings and
	// labels
	e.hcloudOK(t, "storage-box", "create", "--name", name, "--type", "bx11", "--location", serverLocation,
		"--password", boxPassword(), "--ssh-key", e.sshKey, "--enable-ssh", "--reachable-externally",
		"--label", "row=cli-05", "--label", "tier=one")
	got := e.boxByName(t, name)
	id := got.ID
	wantAccess := boxAccess{ReachableExternally: true, SSHEnabled: true}
	if got.StorageBoxType.Name != "bx11" || got.Location.Name != serverLocation || got.AccessSettings != wantAccess ||
		got.Username == nil || *got.Username == "" {
		t.Errorf("created box %s, want type bx11, location %s, access %+v and a username", got.summary(), serverLocation, wantAccess)
	}
	assertLabels(t, "created box", got.Labels, map[string]string{"row": "cli-05", "tier": "one"})

	// list and describe by name and ID
	var listed []boxInfo
	e.hcloudJSON(t, &listed, "storage-box", "list")
	if len(listed) != 1 || listed[0].summary() != got.summary() {
		t.Errorf("hcloud storage-box list: %d boxes %v, the API has only %s", len(listed), listed, got.summary())
	}
	for _, ref := range []string{name, strconv.FormatInt(id, 10)} {
		var described boxInfo
		e.hcloudJSON(t, &described, "storage-box", "describe", ref)
		if described.summary() != got.summary() {
			t.Errorf("hcloud storage-box describe %s: %s, the API has %s", ref, described.summary(), got.summary())
		}
	}

	// update name and labels
	e.hcloudOK(t, "storage-box", "update", name, "--name", renamed)
	e.hcloudOK(t, "storage-box", "add-label", renamed, "stage=two")
	e.hcloudOK(t, "storage-box", "remove-label", renamed, "tier")
	got = e.boxByID(t, id)
	if got.Name != renamed {
		t.Errorf("after update: name %q, want %q", got.Name, renamed)
	}
	assertLabels(t, "after add-label and remove-label", got.Labels, map[string]string{"row": "cli-05", "stage": "two"})

	// change-type
	e.hcloudOK(t, "storage-box", "change-type", renamed, "bx21")
	if got = e.boxByID(t, id); got.StorageBoxType.Name != "bx21" {
		t.Errorf("after change-type: type %q, want bx21", got.StorageBoxType.Name)
	}

	// protection: delete is refused while it is on
	e.hcloudOK(t, "storage-box", "enable-protection", renamed, "delete")
	if got = e.boxByID(t, id); !got.Protection.Delete {
		t.Error("after enable-protection: protection.delete is false")
	}
	r, err := e.hcloudRun(context.Background(), hcloudTimeout, "storage-box", "delete", renamed)
	if err != nil || r.ExitCode == 0 || !strings.Contains(r.Stderr, "protected") {
		t.Errorf("hcloud storage-box delete of a protected box: %v, want a refusal naming the protection\n%s", err, r)
	}
	e.boxByID(t, id)
	e.hcloudOK(t, "storage-box", "disable-protection", renamed, "delete")
	if got = e.boxByID(t, id); got.Protection.Delete {
		t.Error("after disable-protection: protection.delete is true")
	}

	// reset-password
	e.hcloudOK(t, "storage-box", "reset-password", "--password", boxPassword(), renamed)
	e.assertBoxActionDone(t, id, "reset_password")

	// update-access-settings
	e.hcloudOK(t, "storage-box", "update-access-settings", renamed, "--enable-samba", "--enable-zfs", "--enable-ssh=false")
	got = e.boxByID(t, id)
	if a := got.AccessSettings; !a.SambaEnabled || !a.ZFSEnabled || a.SSHEnabled {
		t.Errorf("after update-access-settings: %+v, want samba and zfs on, ssh off", a)
	}

	// folders: a subaccount's home directory makes the folder cli05
	e.createSubaccount(t, &storageBox{ID: id}, "cli05/web", map[string]bool{"ssh_enabled": true})
	var apiFolders struct {
		Folders []string `json:"folders"`
	}
	e.hetznerGetOK(t, fmt.Sprintf("/storage_boxes/%d/folders", id), &apiFolders)
	if !slices.Contains(apiFolders.Folders, "cli05") {
		t.Errorf("GET /storage_boxes/%d/folders: %v, want cli05 among them", id, apiFolders.Folders)
	}
	r = e.hcloudOK(t, "storage-box", "folders", renamed, "-o", "json")
	if folders := decodeFolders(t, r.Stdout); !slices.Equal(sorted(folders), sorted(apiFolders.Folders)) {
		t.Errorf("hcloud storage-box folders: %v, the API has %v", folders, apiFolders.Folders)
	}

	// snapshot plan with Sunday: the API holds day_of_week 7 (E14); hcloud's
	// own JSON prints 0 (E20), so the plan is read through the API only.
	e.hcloudOK(t, "storage-box", "enable-snapshot-plan", renamed,
		"--day-of-week", "sunday", "--hour", "3", "--minute", "15", "--max-snapshots", "5")
	got = e.boxByID(t, id)
	if p := got.SnapshotPlan; p == nil || p.DayOfWeek == nil || *p.DayOfWeek != 7 || p.Hour == nil || *p.Hour != 3 ||
		p.Minute == nil || *p.Minute != 15 || p.MaxSnapshots != 5 || p.DayOfMonth != nil {
		t.Errorf("after enable-snapshot-plan sunday 03:15, 5 snapshots: plan %s", planString(p))
	}
	e.hcloudOK(t, "storage-box", "disable-snapshot-plan", renamed)
	if got = e.boxByID(t, id); got.SnapshotPlan != nil {
		t.Errorf("after disable-snapshot-plan: plan %s, want null", planString(got.SnapshotPlan))
	}

	// delete
	e.hcloudOK(t, "storage-box", "delete", renamed)
	e.assertHetznerGone(t, fmt.Sprintf("/storage_boxes/%d", id))
}

// testCLI05Subaccounts: hcloud manages a subaccount of a box made through
// the API by every subaccount command, addressing it by name and ID (hcloud
// never resolves a username, E20).
func testCLI05Subaccounts(t *testing.T, _ *rowCtx) {
	e := boxProjectEnv(t, "cli05-subs")
	box := newTestBox(t, e, "cli05-subs", "bx11", map[string]bool{"ssh_enabled": true})
	boxRef := strconv.FormatInt(box.ID, 10)
	name, renamed := "cli05sub", "cli05sub2"

	// create with home directory, password, name and access settings
	e.hcloudOK(t, "storage-box", "subaccount", "create", boxRef, "--name", name, "--home-directory", "cli05/home",
		"--password", boxPassword(), "--description", "first", "--enable-ssh", "--readonly")
	got := e.subaccountByName(t, box.ID, name)
	id := got.ID
	if want := (boxAccess{SSHEnabled: true, Readonly: true}); got.HomeDirectory != "cli05/home" ||
		got.Description != "first" || got.AccessSettings != want || got.Username == "" {
		t.Errorf("created subaccount %s, want home cli05/home, description first, access %+v and a username", got.summary(), want)
	}

	// list and describe by name and ID
	var listed []subaccountInfo
	e.hcloudJSON(t, &listed, "storage-box", "subaccount", "list", boxRef)
	if len(listed) != 1 || listed[0].summary() != got.summary() {
		t.Errorf("hcloud storage-box subaccount list: %d subaccounts %v, the API has only %s", len(listed), listed, got.summary())
	}
	for _, ref := range []string{name, strconv.FormatInt(id, 10)} {
		var described subaccountInfo
		e.hcloudJSON(t, &described, "storage-box", "subaccount", "describe", boxRef, ref)
		if described.summary() != got.summary() {
			t.Errorf("hcloud storage-box subaccount describe %s: %s, the API has %s", ref, described.summary(), got.summary())
		}
	}

	// update name and description
	e.hcloudOK(t, "storage-box", "subaccount", "update", boxRef, name, "--name", renamed, "--description", "second")
	if got = e.subaccountByID(t, box.ID, id); got.Name != renamed || got.Description != "second" {
		t.Errorf("after update: name %q, description %q; want %q, second", got.Name, got.Description, renamed)
	}

	// change-home-directory
	e.hcloudOK(t, "storage-box", "subaccount", "change-home-directory", boxRef, renamed, "--home-directory", "cli05/other")
	if got = e.subaccountByID(t, box.ID, id); got.HomeDirectory != "cli05/other" {
		t.Errorf("after change-home-directory: home %q, want cli05/other", got.HomeDirectory)
	}

	// reset-password
	e.hcloudOK(t, "storage-box", "subaccount", "reset-password", boxRef, renamed, "--password", boxPassword())
	e.assertBoxActionDone(t, box.ID, "reset_subaccount_password")

	// update-access-settings
	e.hcloudOK(t, "storage-box", "subaccount", "update-access-settings", boxRef, renamed, "--readonly=false", "--enable-samba")
	if a := e.subaccountByID(t, box.ID, id).AccessSettings; a.Readonly || !a.SambaEnabled {
		t.Errorf("after update-access-settings: %+v, want readonly off and samba on", a)
	}

	// delete by ID
	e.hcloudOK(t, "storage-box", "subaccount", "delete", boxRef, strconv.FormatInt(id, 10))
	e.assertHetznerGone(t, fmt.Sprintf("/storage_boxes/%d/subaccounts/%d", box.ID, id))
}

// testCLI05Snapshots: hcloud creates two snapshots of a box made through
// the API, lists, describes and updates them, rolls back to the first (the
// second is gone, SBOX-18) and deletes it.
func testCLI05Snapshots(t *testing.T, _ *rowCtx) {
	e := boxProjectEnv(t, "cli05-snaps")
	box := newTestBox(t, e, "cli05-snaps", "bx11", map[string]bool{"ssh_enabled": true})
	boxRef := strconv.FormatInt(box.ID, 10)

	e.hcloudOK(t, "storage-box", "snapshot", "create", "--description", "cli05-first", boxRef)
	e.hcloudOK(t, "storage-box", "snapshot", "create", "--description", "cli05-second", boxRef)
	first := e.snapshotByDescription(t, box.ID, "cli05-first")
	second := e.snapshotByDescription(t, box.ID, "cli05-second")

	// list and describe
	var listed []boxSnapshot
	e.hcloudJSON(t, &listed, "storage-box", "snapshot", "list", boxRef)
	if got, want := snapshotIDs(listed), snapshotIDs(e.snapshotsOf(t, box.ID)); !slices.Equal(got, want) {
		t.Errorf("hcloud storage-box snapshot list: %v, the API has %v", got, want)
	}
	for _, ref := range []string{first.Name, strconv.FormatInt(first.ID, 10)} {
		var described boxSnapshot
		e.hcloudJSON(t, &described, "storage-box", "snapshot", "describe", boxRef, ref)
		if described.ID != first.ID || described.Name != first.Name || described.Description != first.Description {
			t.Errorf("hcloud storage-box snapshot describe %s: %d %s %q, the API has %d %s %q",
				ref, described.ID, described.Name, described.Description, first.ID, first.Name, first.Description)
		}
	}

	// update description
	e.hcloudOK(t, "storage-box", "snapshot", "update", boxRef, first.Name, "--description", "cli05-updated")
	if s, status, err := e.getSnapshot(box, first.ID); err != nil || s.Description != "cli05-updated" {
		t.Errorf("after update: snapshot %d description %q (status %d %v), want cli05-updated", first.ID, s.Description, status, err)
	}

	// rollback to the first removes the newer second
	e.hcloudOK(t, "storage-box", "rollback-snapshot", "--snapshot", first.Name, boxRef)
	ids := snapshotIDs(e.snapshotsOf(t, box.ID))
	if !slices.Contains(ids, first.ID) || slices.Contains(ids, second.ID) {
		t.Errorf("after rollback-snapshot to %s: snapshots %v, want %d kept and %d gone", first.Name, ids, first.ID, second.ID)
	}
	e.assertHetznerGone(t, fmt.Sprintf("%s/%d", snapshotsPath(box), second.ID))

	// delete
	e.hcloudOK(t, "storage-box", "snapshot", "delete", boxRef, first.Name)
	e.assertHetznerGone(t, fmt.Sprintf("%s/%d", snapshotsPath(box), first.ID))
}

// testCLI05StorageBoxTypes: hcloud lists and describes the storage box
// types as /storage_box_types serves them.
func testCLI05StorageBoxTypes(t *testing.T, c *rowCtx) {
	e := *c.E
	e.hcloudEnv = withHetznerEndpoint(c.E.hcloudEnv)

	// Other rows add and remove own types; the comparison is repeated until
	// both sides were read without such a change in between.
	var cliTypes, apiTypes map[int64]string
	err := waitFor(2*time.Minute, 5*time.Second, func() (bool, error) {
		var listed []named
		e.hcloudJSON(t, &listed, "storage-box-type", "list")
		cliTypes, apiTypes = namedOf(listed), e.boxTypes(t)
		return fmt.Sprint(cliTypes) == fmt.Sprint(apiTypes), nil
	})
	if err != nil || len(apiTypes) == 0 {
		t.Errorf("hcloud storage-box-type list: %v, the API has %v (%v)", cliTypes, apiTypes, err)
	}

	type boxType struct {
		ID                     int64  `json:"id"`
		Name                   string `json:"name"`
		Description            string `json:"description"`
		Size                   int64  `json:"size"`
		SnapshotLimit          int    `json:"snapshot_limit"`
		AutomaticSnapshotLimit int    `json:"automatic_snapshot_limit"`
		SubaccountsLimit       int    `json:"subaccounts_limit"`
	}
	var api struct {
		Type boxType `json:"storage_box_type"`
	}
	var list struct {
		Types []boxType `json:"storage_box_types"`
	}
	e.hetznerGetOK(t, "/storage_box_types?name=bx11", &list)
	if len(list.Types) != 1 {
		t.Fatalf("GET /storage_box_types?name=bx11: %d types, want 1", len(list.Types))
	}
	e.hetznerGetOK(t, fmt.Sprintf("/storage_box_types/%d", list.Types[0].ID), &api)
	for _, ref := range []string{"bx11", strconv.FormatInt(api.Type.ID, 10)} {
		var described boxType
		e.hcloudJSON(t, &described, "storage-box-type", "describe", ref)
		if described != api.Type {
			t.Errorf("hcloud storage-box-type describe %s: %+v, the API has %+v", ref, described, api.Type)
		}
	}
}

// debugHostRe matches the Host header of a request in hcloud's debug file.
var debugHostRe = regexp.MustCompile(`(?mi)^Host:\s*(\S+)`)

// testCLI05All: hcloud all list in a project with a box and a server lists
// both, and every request goes to hlab, none to Hetzner's real APIs. The
// debug file holds the token: it lives in the row's temporary folder, is
// never logged and is deleted.
func testCLI05All(t *testing.T, _ *rowCtx) {
	e := boxProjectEnv(t, "cli05-all")
	box := newTestBox(t, e, "cli05-all", "bx11", map[string]bool{"ssh_enabled": true})
	srvName := resName("cli05", "all")
	t.Cleanup(func() { deleteIfCreated(t, e, srvName) })
	srv, err := e.createServer(srvName)
	if err != nil {
		t.Fatal(err)
	}

	debug := filepath.Join(t.TempDir(), "hcloud-debug.log")
	t.Cleanup(func() { _ = os.Remove(debug) })
	r := e.hcloudOK(t, "all", "list", "-o", "json", "--debug", "--debug-file", debug)
	var all struct {
		Servers      []named `json:"servers"`
		StorageBoxes []named `json:"storage_boxes"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &all); err != nil {
		t.Fatalf("hcloud all list -o json: %v", err)
	}
	if got := namedOf(all.Servers); len(got) != 1 || got[srv.ID] != srvName {
		t.Errorf("hcloud all list servers: %v, want only %d %s", got, srv.ID, srvName)
	}
	if got := namedOf(all.StorageBoxes); len(got) != 1 || got[box.ID] == "" {
		t.Errorf("hcloud all list storage_boxes: %v, want only box %d", got, box.ID)
	}

	b, err := os.ReadFile(debug)
	if err != nil {
		t.Fatalf("hcloud debug file: %v", err)
	}
	if err := os.Remove(debug); err != nil {
		t.Errorf("deleting the hcloud debug file: %v", err)
	} else if _, err := os.Stat(debug); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the hcloud debug file is still there: %v", err)
	}
	text := string(b)
	hosts := map[string]int{}
	for _, m := range debugHostRe.FindAllStringSubmatch(text, -1) {
		hosts[strings.ToLower(m[1])]++
	}
	if len(hosts) != 1 || hosts["api.hetzner.internal"] == 0 {
		t.Errorf("hcloud all list sent requests to the hosts %v, want only api.hetzner.internal", hosts)
	}
	for _, real := range []string{"api.hetzner.com", "api.hetzner.cloud"} {
		if strings.Contains(text, real) {
			t.Errorf("hcloud all list debug output names %s", real)
		}
	}
	if !strings.Contains(text, "/hetzner/v1/storage_boxes") {
		t.Error("hcloud all list sent no request to /hetzner/v1/storage_boxes")
	}
}

// decodeFolders reads the JSON output of hcloud storage-box folders, an
// object with the list "folders" or the list itself.
func decodeFolders(t *testing.T, out string) []string {
	t.Helper()
	var obj struct {
		Folders []string `json:"folders"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err == nil {
		return obj.Folders
	}
	var list []string
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("hcloud storage-box folders -o json: %v\n%s", err, out)
	}
	return list
}

// sorted returns a sorted copy of s.
func sorted(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}

// snapshotIDs are the sorted IDs of snaps.
func snapshotIDs(snaps []boxSnapshot) []int64 {
	ids := make([]int64, 0, len(snaps))
	for _, s := range snaps {
		ids = append(ids, s.ID)
	}
	slices.Sort(ids)
	return ids
}

// planString prints a snapshot plan with its null fields.
func planString(p *snapshotPlan) string {
	if p == nil {
		return "null"
	}
	f := func(v *int) string {
		if v == nil {
			return "null"
		}
		return strconv.Itoa(*v)
	}
	return fmt.Sprintf("{max_snapshots %d, minute %s, hour %s, day_of_week %s, day_of_month %s}",
		p.MaxSnapshots, f(p.Minute), f(p.Hour), f(p.DayOfWeek), f(p.DayOfMonth))
}
