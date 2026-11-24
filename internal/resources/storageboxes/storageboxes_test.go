package storageboxes_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
)

func TestStorageBoxes(t *testing.T) {
	t.Run("SBOX-09/create", testCreate)
	t.Run("SBOX-09/create_invalid", testCreateInvalid)
	t.Run("SBOX-09/username", testUsername)
	t.Run("SBOX-09/update_delete", testUpdateDelete)
	t.Run("SBOX-09/password_secret", testPasswordSecret)
	t.Run("SBOX-10/protection", testProtection)
	t.Run("SBOX-10/change_type", testChangeType)
	t.Run("SBOX-10/reset_password", testResetPassword)
	t.Run("SBOX-10/access_settings", testAccessSettings)
	t.Run("SBOX-10/locked", testLocked)
	t.Run("SBOX-20/validate", testSnapshotPlanValidate)
	t.Run("SBOX-20/replace_disable", testSnapshotPlanReplaceDisable)
	t.Run("SBOX-02/create", testSubaccountCreate)
	t.Run("SBOX-02/create_invalid", testSubaccountCreateInvalid)
	t.Run("SBOX-02/limit_numbering", testSubaccountLimitNumbering)
	t.Run("SBOX-02/list_update_delete", testSubaccountListUpdateDelete)
	t.Run("SBOX-02/actions", testSubaccountActions)
	t.Run("SBOX-09/create_failed", testCreateFailed)
	t.Run("SBOX-09/delete_confirmed", testDeleteConfirmed)
	t.Run("SBOX-02/not_active", testNotActive)
	t.Run("SBOX-11/path", testFoldersPath)
	t.Run("SBOX-06/change_type", testChangeTypeUsage)
	t.Run("SBOX-06/free_space", testFreeSpace)
	t.Run("SBOX-09/stats", testStats)
	t.Run("SBOX-05/api", testSnapshotAPI)
	t.Run("SBOX-17/at_time", testSnapshotPlanAtTime)
	t.Run("SBOX-17/retention", testSnapshotPlanRetention)
	t.Run("SBOX-05/delete_gone", testSnapshotDeleteGone)
	t.Run("SBOX-17/prune_gone", testSnapshotPruneGone)
	t.Run("SBOX-18/api", testRollbackAPI)
	t.Run("CLI-06/delete_subaccount_waits", testDeleteSubaccountWaits)
	t.Run("CLI-06/delete_snapshot_waits", testDeleteSnapshotWaits)
	t.Run("CLI-06/others_locked", testDeleteOthersLocked)
	t.Run("CLI-06/gone_at_turn", testDeleteGoneAtTurn)
}

// listActions returns the actions at path.
func listActions(t *testing.T, e *boxEnv, path string) []actionView {
	t.Helper()
	resp := e.Do(http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
	}
	var b struct {
		Actions []actionView `json:"actions"`
	}
	apitest.Decode(t, resp, &b)
	return b.Actions
}

func containsAction(list []actionView, id int64) bool {
	return slices.ContainsFunc(list, func(a actionView) bool { return a.ID == id })
}

// deleteBox deletes box id and waits for its delete_storage_box action.
func deleteBox(t *testing.T, e *boxEnv, id int64) actionView {
	t.Helper()
	a := startAction(t, e, http.MethodDelete, boxPath(id), nil, http.StatusCreated)
	if a.Command != "delete_storage_box" {
		t.Errorf("DELETE %s: action %q, want delete_storage_box", boxPath(id), a.Command)
	}
	waitSuccess(t, e, a, "DELETE "+boxPath(id))
	return a
}

func testCreate(t *testing.T) {
	e := newBoxEnv(t)
	body := boxBody("backup-1")
	body["storage_box_type"] = "bx21"
	body["labels"] = map[string]string{"env": "test"}
	body["ssh_keys"] = []string{sshKey}
	access := accessView{ReachableExternally: true, SSHEnabled: true, ZFSEnabled: true}
	body["access_settings"] = map[string]bool{
		"reachable_externally": true,
		"samba_enabled":        false,
		"ssh_enabled":          true,
		"webdav_enabled":       false,
		"zfs_enabled":          true,
	}

	e.gate.Close()
	b := startBox(t, e, body)
	nb := b.Box
	if nb.Status != "initializing" || nb.Username != nil || nb.Server != nil || nb.System != nil || nb.SnapshotPlan != nil {
		t.Errorf("created box status %q username %v server %v system %v snapshot_plan %v, want initializing with nulls",
			nb.Status, nb.Username, nb.Server, nb.System, nb.SnapshotPlan)
	}
	if nb.Stats.Size != 0 || nb.Stats.SizeData != 0 || nb.Stats.SizeSnapshots != 0 {
		t.Errorf("created box stats %+v, want 0", nb.Stats)
	}
	if !b.Action.hasResource("storage_box", nb.ID) {
		t.Errorf("create action resources %+v, want storage_box %d", b.Action.Resources, nb.ID)
	}
	release := e.gate.Next(t, "create_storage_box")
	if got := mustBox(t, e, nb.ID); got.Status != "initializing" {
		t.Errorf("box during create_storage_box: status %q, want initializing", got.Status)
	}
	release()
	e.gate.Open()
	waitSuccess(t, e, b.Action, "create_storage_box")

	box := mustBox(t, e, nb.ID)
	str := func(p *string) string {
		if p == nil {
			return "<null>"
		}
		return *p
	}
	if box.Status != "active" {
		t.Errorf("status %q, want active", box.Status)
	}
	if got := str(box.Username); got != "u100000" {
		t.Errorf("username %q, want u100000", got)
	}
	if got := str(box.Server); got != "u100000.storagebox.hetzner.internal" {
		t.Errorf("server %q, want u100000.storagebox.hetzner.internal", got)
	}
	if got, want := str(box.System), fmt.Sprintf("FSN1-BX%d", box.ID); got != want {
		t.Errorf("system %q, want %q", got, want)
	}
	if box.StorageBoxType.Name != "bx21" || box.StorageBoxType.ID != e.storageType(t, "bx21").ID {
		t.Errorf("storage_box_type %+v, want bx21", box.StorageBoxType)
	}
	if box.Location.Name != "fsn1" {
		t.Errorf("location %q, want fsn1", box.Location.Name)
	}
	if !maps.Equal(box.Labels, map[string]string{"env": "test"}) {
		t.Errorf("labels %v, want env=test", box.Labels)
	}
	if box.AccessSettings != access {
		t.Errorf("access_settings %+v, want %+v", box.AccessSettings, access)
	}

	resp := e.Do(http.MethodGet, fmt.Sprintf("%s/actions/%d", boxesPath, b.Action.ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s/actions/%d: status %d, want 200", boxesPath, b.Action.ID, resp.StatusCode)
	}
	var one struct {
		Action actionView `json:"action"`
	}
	apitest.Decode(t, resp, &one)
	if one.Action.ID != b.Action.ID || one.Action.Command != "create_storage_box" || one.Action.Status != "success" {
		t.Errorf("GET %s/actions/%d: %+v, want create_storage_box success", boxesPath, b.Action.ID, one.Action)
	}
	if !containsAction(listActions(t, e, boxPath(box.ID)+"/actions"), b.Action.ID) {
		t.Errorf("GET %s/actions: action %d missing", boxPath(box.ID), b.Action.ID)
	}
	if !containsAction(listActions(t, e, fmt.Sprintf("/actions?id=%d", b.Action.ID)), b.Action.ID) {
		t.Errorf("GET /v1/actions?id=%d: action missing", b.Action.ID)
	}
}

func testCreateInvalid(t *testing.T) {
	e := newBoxEnv(t)
	cases := []struct {
		name  string
		edit  func(b map[string]any)
		field string
	}{
		{"unknown_type", func(b map[string]any) { b["storage_box_type"] = "bx99" }, "storage_box_type"},
		{"location_without_price", func(b map[string]any) { b["location"] = "nbg1" }, "location"},
		{"unknown_location", func(b map[string]any) { b["location"] = "xyz9" }, "location"},
		{"password_policy", func(b map[string]any) { b["password"] = "short" }, "password"},
		{"ssh_key_format", func(b map[string]any) { b["ssh_keys"] = []string{"not an openssh key"} }, "ssh_keys"},
		{"missing_name", func(b map[string]any) { delete(b, "name") }, "name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &boxEnv{Env: e.For(t), cat: e.cat, gate: e.gate}
			body := boxBody("invalid-" + tc.name)
			tc.edit(body)
			wantFieldError(t, postBox(e, body), tc.field, fmt.Sprintf("POST %s %v", boxesPath, body))
		})
	}
}

func testUsername(t *testing.T) {
	e := newBoxEnv(t)
	first := createBox(t, e, boxBody("first"))
	if first.Username == nil || *first.Username != "u100000" {
		t.Fatalf("first box username %v, want u100000", first.Username)
	}
	deleteBox(t, e, first.ID)
	second := createBox(t, e, boxBody("second"))
	if second.Username == nil || *second.Username != "u100001" {
		t.Errorf("box after deleting u100000: username %v, want u100001", second.Username)
	}
}

func testUpdateDelete(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("old-name"))
	sub := createSubaccount(t, e, box.ID, subaccountBody("data"))

	labels := map[string]string{"team": "ops"}
	resp := e.Do(http.MethodPut, boxPath(box.ID), map[string]any{"name": "new-name", "labels": labels})
	if resp.StatusCode != http.StatusOK {
		wantError(t, resp, http.StatusOK, "", "PUT "+boxPath(box.ID))
		t.FailNow()
	}
	var put struct {
		Box boxView `json:"storage_box"`
	}
	apitest.Decode(t, resp, &put)
	if put.Box.Name != "new-name" || !maps.Equal(put.Box.Labels, labels) {
		t.Errorf("PUT answer name %q labels %v, want new-name %v", put.Box.Name, put.Box.Labels, labels)
	}
	if got := mustBox(t, e, box.ID); got.Name != "new-name" || !maps.Equal(got.Labels, labels) {
		t.Errorf("GET after PUT: name %q labels %v, want new-name %v", got.Name, got.Labels, labels)
	}

	a := deleteBox(t, e, box.ID)
	if !a.hasResource("storage_box", box.ID) {
		t.Errorf("delete action resources %+v, want storage_box %d", a.Resources, box.ID)
	}
	if _, ok := getBox(t, e, box.ID); ok {
		t.Errorf("box %d still found after delete_storage_box", box.ID)
	}
	if _, ok := getSubaccount(t, e, box.ID, sub.ID); ok {
		t.Errorf("subaccount %d still found after deleting its box", sub.ID)
	}
}

func testPasswordSecret(t *testing.T) {
	logs := captureLog(t)
	e := newBoxEnv(t)
	const first = "Kq7-Plain-Secret1"
	const second = "Zr8.Other-Secret2"
	var answers []string
	call := func(method, path string, body any, status int) string {
		t.Helper()
		got, text := doRaw(t, e, method, path, body)
		if got != status {
			t.Fatalf("%s %s: status %d (%s), want %d", method, path, got, text, status)
		}
		answers = append(answers, method+" "+path+": "+text)
		return text
	}

	body := boxBody("secret")
	body["password"] = first
	var created createdBox
	if err := json.Unmarshal([]byte(call(http.MethodPost, boxesPath, body, http.StatusCreated)), &created); err != nil {
		t.Fatalf("decode create answer: %v", err)
	}
	waitSuccess(t, e, created.Action, "create_storage_box")
	id := created.Box.ID
	call(http.MethodGet, boxPath(id), nil, http.StatusOK)
	call(http.MethodGet, boxesPath, nil, http.StatusOK)
	call(http.MethodGet, fmt.Sprintf("/actions/%d", created.Action.ID), nil, http.StatusOK)
	doc, h := storedBox(t, e, id)
	wantHashes(t, h, first, "stored box after create")
	if strings.Contains(doc, first) {
		t.Error("stored box after create holds the plain password")
	}

	var reset struct {
		Action actionView `json:"action"`
	}
	path := boxPath(id) + "/actions/reset_password"
	if err := json.Unmarshal([]byte(call(http.MethodPost, path, map[string]string{"password": second}, http.StatusCreated)), &reset); err != nil {
		t.Fatalf("decode reset_password answer: %v", err)
	}
	waitSuccess(t, e, reset.Action, "reset_password")
	call(http.MethodGet, boxPath(id), nil, http.StatusOK)
	call(http.MethodGet, fmt.Sprintf("%s/actions/%d", boxesPath, reset.Action.ID), nil, http.StatusOK)
	doc, h = storedBox(t, e, id)
	wantHashes(t, h, second, "stored box after reset_password")

	for _, pw := range []string{first, second} {
		for _, a := range answers {
			if strings.Contains(a, pw) {
				t.Errorf("response holds a plain password: %s", a)
			}
		}
		if strings.Contains(doc, pw) {
			t.Error("stored box holds a plain password")
		}
		if strings.Contains(logs.String(), pw) {
			t.Error("log holds a plain password")
		}
	}
}
