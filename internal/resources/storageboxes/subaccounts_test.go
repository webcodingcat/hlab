package storageboxes_test

import (
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
)

// boxUsername returns the username of an active box.
func boxUsername(t *testing.T, b boxView) string {
	t.Helper()
	if b.Username == nil {
		t.Fatalf("box %d has no username", b.ID)
	}
	return *b.Username
}

// subActionPath is the path of the subaccount action name.
func subActionPath(box, sub int64, name string) string {
	return subaccountPath(box, sub) + "/actions/" + name
}

// deleteSubaccount deletes subaccount sub of box and waits for its
// delete_subaccount action.
func deleteSubaccount(t *testing.T, e *boxEnv, box, sub int64) actionView {
	t.Helper()
	a := startAction(t, e, http.MethodDelete, subaccountPath(box, sub), nil, http.StatusCreated)
	if a.Command != "delete_subaccount" {
		t.Errorf("DELETE %s: action %q, want delete_subaccount", subaccountPath(box, sub), a.Command)
	}
	waitSuccess(t, e, a, "DELETE "+subaccountPath(box, sub))
	return a
}

func testSubaccountCreate(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("with-subs"))
	user := boxUsername(t, box)

	body := subaccountBody("backup/data")
	body["access_settings"] = map[string]bool{"readonly": true, "ssh_enabled": true}
	c := startSubaccount(t, e, box.ID, body)
	if c.Subaccount.StorageBox != box.ID {
		t.Errorf("created subaccount storage_box %d, want %d", c.Subaccount.StorageBox, box.ID)
	}
	if !c.Action.hasResource("storage_box", box.ID) || !c.Action.hasResource("storage_box_subaccount", c.Subaccount.ID) {
		t.Errorf("create_subaccount resources %+v, want storage_box %d and storage_box_subaccount %d",
			c.Action.Resources, box.ID, c.Subaccount.ID)
	}
	waitSuccess(t, e, c.Action, "create_subaccount")

	sub := mustSubaccount(t, e, box.ID, c.Subaccount.ID)
	wantUser := user + "-sub1"
	if sub.Username != wantUser {
		t.Errorf("username %q, want %q", sub.Username, wantUser)
	}
	if want := wantUser + ".storagebox.hetzner.internal"; sub.Server != want {
		t.Errorf("server %q, want %q", sub.Server, want)
	}
	if sub.HomeDirectory != "backup/data" {
		t.Errorf("home_directory %q, want backup/data", sub.HomeDirectory)
	}
	if want := (accessView{Readonly: true, SSHEnabled: true}); sub.AccessSettings != want {
		t.Errorf("access_settings %+v, want %+v", sub.AccessSettings, want)
	}
	if sub.Name != wantUser {
		t.Errorf("name %q, want the username %q", sub.Name, wantUser)
	}
	if !containsAction(listActions(t, e, boxPath(box.ID)+"/actions"), c.Action.ID) {
		t.Errorf("GET %s/actions: create_subaccount action %d missing", boxPath(box.ID), c.Action.ID)
	}
}

func testSubaccountCreateInvalid(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("invalid-subs"))
	cases := []struct {
		name  string
		edit  func(b map[string]any)
		field string
	}{
		{"leading_slash", func(b map[string]any) { b["home_directory"] = "/data" }, "home_directory"},
		{"dotdot", func(b map[string]any) { b["home_directory"] = "../u100001" }, "home_directory"},
		{"character_outside_pattern", func(b map[string]any) { b["home_directory"] = "data$1" }, "home_directory"},
		{"longer_than_999", func(b map[string]any) { b["home_directory"] = strings.Repeat("a", 1000) }, "home_directory"},
		{"password_policy", func(b map[string]any) { b["password"] = "short" }, "password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &boxEnv{Env: e.For(t), cat: e.cat, gate: e.gate}
			body := subaccountBody("data")
			tc.edit(body)
			wantFieldError(t, postSubaccount(e, box.ID, body), tc.field, fmt.Sprintf("POST %s %v", subaccountsPath(box.ID), body))
		})
	}

	named := subaccountBody("one")
	named["name"] = "shared"
	createSubaccount(t, e, box.ID, named)
	again := subaccountBody("two")
	again["name"] = "shared"
	wantError(t, postSubaccount(e, box.ID, again), http.StatusConflict, "uniqueness_error", "subaccount name used in the same box")

	other := createBox(t, e, boxBody("other-box"))
	if sub := createSubaccount(t, e, other.ID, again); sub.Name != "shared" {
		t.Errorf("subaccount in another box: name %q, want shared", sub.Name)
	}
}

func testSubaccountLimitNumbering(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("full"))
	user := boxUsername(t, box)
	limit := e.storageType(t, "bx11").SubaccountsLimit
	if limit < 1 {
		t.Fatalf("recorded bx11 subaccounts_limit %d, want at least 1", limit)
	}

	var first subaccountView
	for i := 1; i <= limit; i++ {
		sub := createSubaccount(t, e, box.ID, subaccountBody(fmt.Sprintf("dir%d", i)))
		if i == 1 {
			first = sub
		}
	}
	wantError(t, postSubaccount(e, box.ID, subaccountBody("over")), http.StatusForbidden, "resource_limit_exceeded",
		fmt.Sprintf("subaccount %d at subaccounts_limit %d", limit+1, limit))

	if first.Username != user+"-sub1" {
		t.Fatalf("first subaccount username %q, want %s-sub1", first.Username, user)
	}
	deleteSubaccount(t, e, box.ID, first.ID)
	next := createSubaccount(t, e, box.ID, subaccountBody("after-delete"))
	if want := fmt.Sprintf("%s-sub%d", user, limit+1); next.Username != want {
		t.Errorf("subaccount after deleting sub1: username %q, want %q", next.Username, want)
	}
}

func testSubaccountListUpdateDelete(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("list-subs"))
	createSubaccount(t, e, box.ID, subaccountBody("a"))
	second := createSubaccount(t, e, box.ID, subaccountBody("b"))

	path := subaccountsPath(box.ID) + "?username=" + url.QueryEscape(second.Username)
	resp := e.Do(http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
	}
	var list struct {
		Subaccounts []subaccountView `json:"subaccounts"`
	}
	apitest.Decode(t, resp, &list)
	if len(list.Subaccounts) != 1 || list.Subaccounts[0].ID != second.ID {
		t.Errorf("GET %s: %d subaccounts %+v, want only %d", path, len(list.Subaccounts), list.Subaccounts, second.ID)
	}

	labels := map[string]string{"role": "backup"}
	update := map[string]any{"name": "renamed", "description": "nightly backups", "labels": labels}
	resp = e.Do(http.MethodPut, subaccountPath(box.ID, second.ID), update)
	if resp.StatusCode != http.StatusOK {
		wantError(t, resp, http.StatusOK, "", "PUT "+subaccountPath(box.ID, second.ID))
		t.FailNow()
	}
	var put struct {
		Subaccount subaccountView `json:"subaccount"`
	}
	apitest.Decode(t, resp, &put)
	for what, s := range map[string]subaccountView{"PUT answer": put.Subaccount, "GET after PUT": mustSubaccount(t, e, box.ID, second.ID)} {
		if s.Name != "renamed" || s.Description != "nightly backups" || !maps.Equal(s.Labels, labels) {
			t.Errorf("%s: name %q description %q labels %v, want renamed, nightly backups, %v",
				what, s.Name, s.Description, s.Labels, labels)
		}
	}

	a := deleteSubaccount(t, e, box.ID, second.ID)
	if !a.hasResource("storage_box", box.ID) || !a.hasResource("storage_box_subaccount", second.ID) {
		t.Errorf("delete_subaccount resources %+v, want storage_box %d and storage_box_subaccount %d", a.Resources, box.ID, second.ID)
	}
	if _, ok := getSubaccount(t, e, box.ID, second.ID); ok {
		t.Errorf("subaccount %d still found after delete_subaccount", second.ID)
	}
}

func testSubaccountActions(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("sub-actions"))
	sub := createSubaccount(t, e, box.ID, subaccountBody("start"))

	home := subActionPath(box.ID, sub.ID, "change_home_directory")
	a := runAction(t, e, home, map[string]string{"home_directory": "moved/here"}, "change_home_directory")
	if !a.hasResource("storage_box", box.ID) || !a.hasResource("storage_box_subaccount", sub.ID) {
		t.Errorf("change_home_directory resources %+v, want storage_box %d and storage_box_subaccount %d", a.Resources, box.ID, sub.ID)
	}
	if got := mustSubaccount(t, e, box.ID, sub.ID).HomeDirectory; got != "moved/here" {
		t.Errorf("after change_home_directory: home_directory %q, want moved/here", got)
	}
	wantFieldError(t, e.Do(http.MethodPost, home, map[string]string{"home_directory": "/absolute"}),
		"home_directory", "change_home_directory with a leading slash")

	reset := subActionPath(box.ID, sub.ID, "reset_subaccount_password")
	const next = "Sub-Passw0rd.2"
	runAction(t, e, reset, map[string]string{"password": next}, "reset_subaccount_password")
	_, before := storedSubaccount(t, e, sub.ID)
	wantHashes(t, before, next, "stored subaccount after reset_subaccount_password")
	wantFieldError(t, e.Do(http.MethodPost, reset, map[string]string{"password": "short"}),
		"password", "reset_subaccount_password against the policy")
	if _, after := storedSubaccount(t, e, sub.ID); after != before {
		t.Errorf("refused reset_subaccount_password changed the stored hashes")
	}

	access := subActionPath(box.ID, sub.ID, "update_access_settings")
	runAction(t, e, access, map[string]bool{"readonly": true, "samba_enabled": true}, "update_subaccount_access_settings")
	runAction(t, e, access, map[string]bool{"webdav_enabled": true}, "update_subaccount_access_settings")
	want := accessView{Readonly: true, SambaEnabled: true, WebDAVEnabled: true}
	if got := mustSubaccount(t, e, box.ID, sub.ID).AccessSettings; got != want {
		t.Errorf("after update_access_settings: %+v, want %+v", got, want)
	}
	wantFieldError(t, e.Do(http.MethodPost, access, map[string]any{"readonly": false, "home_directory": "elsewhere"}),
		"home_directory", "update_access_settings with home_directory")
	if got := mustSubaccount(t, e, box.ID, sub.ID); got.HomeDirectory != "moved/here" || got.AccessSettings != want {
		t.Errorf("after refused update_access_settings: home_directory %q access %+v, want moved/here %+v",
			got.HomeDirectory, got.AccessSettings, want)
	}

	_, finish := holdAction(t, e, boxActionPath(box.ID, "update_access_settings"), map[string]bool{"ssh_enabled": true}, "update_access_settings")
	wantError(t, e.Do(http.MethodPost, home, map[string]string{"home_directory": "while/locked"}), http.StatusLocked, "locked",
		"change_home_directory while a box action runs")
	finish()
}
