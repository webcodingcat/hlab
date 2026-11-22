package storageboxes_test

import (
	"fmt"
	"net/http"
	"testing"
)

// boxActionPath is the path of the box action name of box id.
func boxActionPath(id int64, name string) string {
	return boxPath(id) + "/actions/" + name
}

func testProtection(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("protected"))

	a := runAction(t, e, boxActionPath(box.ID, "change_protection"), map[string]bool{"delete": true}, "change_protection")
	if !a.hasResource("storage_box", box.ID) {
		t.Errorf("change_protection resources %+v, want storage_box %d", a.Resources, box.ID)
	}
	if got := mustBox(t, e, box.ID); !got.Protection.Delete {
		t.Errorf("after change_protection delete=true: protection.delete false, want true")
	}
	wantError(t, e.Do(http.MethodDelete, boxPath(box.ID), nil), http.StatusLocked, "protected", "DELETE of a delete-protected box")
	if _, ok := getBox(t, e, box.ID); !ok {
		t.Fatalf("delete-protected box %d gone after refused DELETE", box.ID)
	}

	runAction(t, e, boxActionPath(box.ID, "change_protection"), map[string]bool{"delete": false}, "change_protection")
	if got := mustBox(t, e, box.ID); got.Protection.Delete {
		t.Errorf("after change_protection delete=false: protection.delete true, want false")
	}
	deleteBox(t, e, box.ID)
	if _, ok := getBox(t, e, box.ID); ok {
		t.Errorf("box %d still found after delete without protection", box.ID)
	}
}

func testChangeType(t *testing.T) {
	e := newBoxEnv(t)
	body := boxBody("typed")
	body["storage_box_type"] = "bx21"
	box := createBox(t, e, body)

	for _, name := range []string{"bx31", "bx11"} {
		runAction(t, e, boxActionPath(box.ID, "change_type"), map[string]string{"storage_box_type": name}, "change_type")
		got := mustBox(t, e, box.ID)
		if got.StorageBoxType.Name != name || got.StorageBoxType.ID != e.storageType(t, name).ID {
			t.Errorf("after change_type to %s: storage_box_type %+v", name, got.StorageBoxType)
		}
	}

	wantFieldError(t, e.Do(http.MethodPost, boxActionPath(box.ID, "change_type"), map[string]string{"storage_box_type": "bx99"}),
		"storage_box_type", "change_type to an unknown type")

	nbg := boxBody("in-nbg1")
	nbg["storage_box_type"] = "bx21"
	nbg["location"] = "nbg1"
	other := createBox(t, e, nbg)
	wantFieldError(t, e.Do(http.MethodPost, boxActionPath(other.ID, "change_type"), map[string]string{"storage_box_type": "bx11"}),
		"storage_box_type", "change_type to bx11 in nbg1 without a price")
	if got := mustBox(t, e, other.ID); got.StorageBoxType.Name != "bx21" {
		t.Errorf("after refused change_type: storage_box_type %q, want bx21", got.StorageBoxType.Name)
	}
}

func testResetPassword(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("reset"))
	const next = "Next-Passw0rd!"

	runAction(t, e, boxActionPath(box.ID, "reset_password"), map[string]string{"password": next}, "reset_password")
	_, before := storedBox(t, e, box.ID)
	wantHashes(t, before, next, "stored box after reset_password")

	wantFieldError(t, e.Do(http.MethodPost, boxActionPath(box.ID, "reset_password"), map[string]string{"password": "short"}),
		"password", "reset_password against the policy")
	if _, after := storedBox(t, e, box.ID); after != before {
		t.Errorf("refused reset_password changed the stored hashes")
	}
}

func testAccessSettings(t *testing.T) {
	e := newBoxEnv(t)
	body := boxBody("access")
	body["access_settings"] = map[string]bool{
		"reachable_externally": true,
		"samba_enabled":        false,
		"ssh_enabled":          false,
		"webdav_enabled":       true,
		"zfs_enabled":          false,
	}
	box := createBox(t, e, body)

	runAction(t, e, boxActionPath(box.ID, "update_access_settings"),
		map[string]bool{"ssh_enabled": true, "webdav_enabled": false, "zfs_enabled": true}, "update_access_settings")
	want := accessView{ReachableExternally: true, SSHEnabled: true, ZFSEnabled: true}
	if got := mustBox(t, e, box.ID).AccessSettings; got != want {
		t.Errorf("after update_access_settings: %+v, want %+v", got, want)
	}

	runAction(t, e, boxActionPath(box.ID, "update_access_settings"),
		map[string]bool{"samba_enabled": true, "reachable_externally": false}, "update_access_settings")
	want = accessView{SambaEnabled: true, SSHEnabled: true, ZFSEnabled: true}
	if got := mustBox(t, e, box.ID).AccessSettings; got != want {
		t.Errorf("after second update_access_settings: %+v, want %+v", got, want)
	}
}

func testLocked(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("locked"))
	pw := map[string]string{"password": "Other-Passw0rd"}

	_, finish := holdAction(t, e, boxActionPath(box.ID, "change_protection"), map[string]bool{"delete": true}, "change_protection")
	wantError(t, e.Do(http.MethodPost, boxActionPath(box.ID, "reset_password"), pw), http.StatusLocked, "locked",
		"reset_password while change_protection runs")
	finish()

	runAction(t, e, boxActionPath(box.ID, "reset_password"), pw, "reset_password")
}

func testSnapshotPlanValidate(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("plan"))
	limit := e.storageType(t, "bx11").AutomaticSnapshotLimit
	if limit == nil {
		t.Fatal("recorded bx11 has no automatic_snapshot_limit")
	}
	path := boxActionPath(box.ID, "enable_snapshot_plan")

	cases := []struct {
		name  string
		edit  func(b map[string]any)
		field string
	}{
		{"max_snapshots_0", func(b map[string]any) { b["max_snapshots"] = 0 }, "max_snapshots"},
		{"max_snapshots_above_limit", func(b map[string]any) { b["max_snapshots"] = *limit + 1 }, "max_snapshots"},
		{"minute_60", func(b map[string]any) { b["minute"] = 60 }, "minute"},
		{"hour_24", func(b map[string]any) { b["hour"] = 24 }, "hour"},
		{"missing_minute", func(b map[string]any) { delete(b, "minute") }, "minute"},
		{"missing_hour", func(b map[string]any) { delete(b, "hour") }, "hour"},
		{"day_of_week_0", func(b map[string]any) { b["day_of_week"] = 0 }, "day_of_week"},
		{"day_of_week_8", func(b map[string]any) { b["day_of_week"] = 8 }, "day_of_week"},
		{"day_of_month_0", func(b map[string]any) { b["day_of_month"] = 0 }, "day_of_month"},
		{"day_of_month_32", func(b map[string]any) { b["day_of_month"] = 32 }, "day_of_month"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &boxEnv{Env: e.For(t), cat: e.cat, gate: e.gate}
			body := map[string]any{"max_snapshots": 1, "minute": 30, "hour": 3}
			tc.edit(body)
			wantFieldError(t, e.Do(http.MethodPost, path, body), tc.field, fmt.Sprintf("enable_snapshot_plan %v", body))
			if got := mustBox(t, e, box.ID); got.SnapshotPlan != nil {
				t.Errorf("after refused enable_snapshot_plan: snapshot_plan %+v, want null", *got.SnapshotPlan)
			}
		})
	}

	runAction(t, e, path, map[string]any{"max_snapshots": *limit, "minute": 59, "hour": 23, "day_of_week": nil, "day_of_month": nil},
		"enable_snapshot_plan")
	got := mustBox(t, e, box.ID).SnapshotPlan
	if got == nil {
		t.Fatal("after enable_snapshot_plan: snapshot_plan null")
	}
	if got.MaxSnapshots != *limit || got.Minute != 59 || got.Hour != 23 || got.DayOfWeek != nil || got.DayOfMonth != nil {
		t.Errorf("snapshot_plan %+v (day_of_week %v, day_of_month %v), want max %d 23:59 with null days",
			*got, got.DayOfWeek, got.DayOfMonth, *limit)
	}
}

func testSnapshotPlanReplaceDisable(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("plan"))
	path := boxActionPath(box.ID, "enable_snapshot_plan")

	runAction(t, e, path, map[string]any{"max_snapshots": 1, "minute": 0, "hour": 1}, "enable_snapshot_plan")
	runAction(t, e, path, map[string]any{"max_snapshots": 2, "minute": 15, "hour": 4, "day_of_week": 3, "day_of_month": 12},
		"enable_snapshot_plan")
	got := mustBox(t, e, box.ID).SnapshotPlan
	if got == nil {
		t.Fatal("after second enable_snapshot_plan: snapshot_plan null")
	}
	if got.MaxSnapshots != 2 || got.Minute != 15 || got.Hour != 4 ||
		got.DayOfWeek == nil || *got.DayOfWeek != 3 || got.DayOfMonth == nil || *got.DayOfMonth != 12 {
		t.Errorf("snapshot_plan %+v, want the second plan (2, 04:15, day_of_week 3, day_of_month 12)", *got)
	}

	runAction(t, e, boxActionPath(box.ID, "disable_snapshot_plan"), nil, "disable_snapshot_plan")
	if got := mustBox(t, e, box.ID).SnapshotPlan; got != nil {
		t.Errorf("after disable_snapshot_plan: snapshot_plan %+v, want null", *got)
	}
}
