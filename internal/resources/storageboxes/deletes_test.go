package storageboxes_test

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// startDelete sends DELETE to path, expects 201 with an action command
// that is still running and returns it.
func startDelete(t *testing.T, e *boxEnv, path, command string) actionView {
	t.Helper()
	a := startAction(t, e, http.MethodDelete, path, nil, http.StatusCreated)
	if a.Command != command || a.Status != "running" {
		t.Errorf("DELETE %s: action %q status %q, want %s running", path, a.Command, a.Status, command)
	}
	return a
}

// holdBoxAction holds an update_access_settings action of box at its Apply.
func holdBoxAction(t *testing.T, e *boxEnv, box int64) (actionView, func()) {
	t.Helper()
	return holdAction(t, e, boxActionPath(box, "update_access_settings"), map[string]bool{"ssh_enabled": true}, "update_access_settings")
}

func testDeleteSubaccountWaits(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("sub-delete-waits"))
	sub := createSubaccount(t, e, box.ID, subaccountBody("gone"))

	e.gate.Close()
	held := startAction(t, e, http.MethodPost, boxActionPath(box.ID, "update_access_settings"),
		map[string]bool{"ssh_enabled": true}, http.StatusCreated)
	releaseHeld := e.gate.Next(t, "held update_access_settings")

	del := startDelete(t, e, subaccountPath(box.ID, sub.ID), "delete_subaccount")
	if got := e.For(t).GetAction(del.ID); got.Status != "running" {
		t.Errorf("delete_subaccount behind a held action: status %q, want running", got.Status)
	}
	mustSubaccount(t, e, box.ID, sub.ID)

	releaseHeld()
	releaseDelete := e.gate.Next(t, "Apply of delete_subaccount")
	if got := e.For(t).GetAction(held.ID); got.Status != "success" {
		t.Errorf("Apply of delete_subaccount started while the held action was %q, want it after success", got.Status)
	}
	releaseDelete()
	e.gate.Open()
	waitSuccess(t, e, del, "delete_subaccount behind a held action")
	if _, ok := getSubaccount(t, e, box.ID, sub.ID); ok {
		t.Errorf("subaccount %d still found after delete_subaccount", sub.ID)
	}
}

func testDeleteSnapshotWaits(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("snap-delete-waits"))
	user := boxUsername(t, box)
	snap := createSnapshot(t, e, box.ID, map[string]any{})

	var released atomic.Bool
	var early atomic.Int32
	e.agent.setAnswer(agentapi.StorageJobDeleteSnapshot, func(agentapi.StorageJob) (agentapi.StorageJobResult, error) {
		if !released.Load() {
			early.Add(1)
		}
		return agentapi.StorageJobResult{}, nil
	})

	_, finish := holdBoxAction(t, e, box.ID)
	del := startDelete(t, e, snapshotPath(box.ID, snap.ID), "delete_snapshot")
	if got := e.For(t).GetAction(del.ID); got.Status != "running" {
		t.Errorf("delete_snapshot behind a held action: status %q, want running", got.Status)
	}
	if jobs := e.agent.jobsOf(agentapi.StorageJobDeleteSnapshot); len(jobs) != 0 {
		t.Errorf("delete-snapshot jobs while a box action is held: %+v, want none", jobs)
	}
	mustSnapshot(t, e, box.ID, snap.ID)

	released.Store(true)
	finish()
	waitSuccess(t, e, del, "delete_snapshot behind a held action")
	if n := early.Load(); n != 0 {
		t.Errorf("%d delete-snapshot jobs reached the agent before the held action finished, want none", n)
	}
	if _, ok := getSnapshot(t, e, box.ID, snap.ID); ok {
		t.Errorf("snapshot %d still found after delete_snapshot", snap.ID)
	}
	if !hasJob(e.agent.jobsOf(agentapi.StorageJobDeleteSnapshot), user, snap.Name) {
		t.Errorf("no delete-snapshot job for box %s snapshot %s", user, snap.Name)
	}
}

func testDeleteOthersLocked(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("others-locked"))
	waiting := createSubaccount(t, e, box.ID, subaccountBody("waiting"))
	other := createSubaccount(t, e, box.ID, subaccountBody("other"))

	_, finish := holdBoxAction(t, e, box.ID)
	del := startDelete(t, e, subaccountPath(box.ID, waiting.ID), "delete_subaccount")

	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"reset_password", http.MethodPost, boxActionPath(box.ID, "reset_password"), map[string]string{"password": "Other-Passw0rd"}},
		{"change_home_directory", http.MethodPost, subActionPath(box.ID, other.ID, "change_home_directory"), map[string]string{"home_directory": "while/waiting"}},
		{"delete_storage_box", http.MethodDelete, boxPath(box.ID), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantError(t, e.For(t).Do(c.method, c.path, c.body), http.StatusLocked, "locked",
				c.method+" "+c.path+" while a subaccount delete waits")
		})
	}

	finish()
	waitSuccess(t, e, del, "waiting delete_subaccount")
	mustBox(t, e, box.ID)
}

func testDeleteGoneAtTurn(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, e *boxEnv, box boxView)
	}{
		{"second_subaccount_delete", func(t *testing.T, e *boxEnv, box boxView) {
			sub := createSubaccount(t, e, box.ID, subaccountBody("twice"))
			_, finish := holdBoxAction(t, e, box.ID)
			first := startDelete(t, e, subaccountPath(box.ID, sub.ID), "delete_subaccount")
			second := startDelete(t, e, subaccountPath(box.ID, sub.ID), "delete_subaccount")
			finish()
			waitSuccess(t, e, first, "first delete_subaccount")
			waitSuccess(t, e, second, "second delete_subaccount")
			if _, ok := getSubaccount(t, e, box.ID, sub.ID); ok {
				t.Errorf("subaccount %d still found after two deletes", sub.ID)
			}
		}},
		{"second_snapshot_delete", func(t *testing.T, e *boxEnv, box boxView) {
			snap := createSnapshot(t, e, box.ID, map[string]any{})
			_, finish := holdBoxAction(t, e, box.ID)
			first := startDelete(t, e, snapshotPath(box.ID, snap.ID), "delete_snapshot")
			second := startDelete(t, e, snapshotPath(box.ID, snap.ID), "delete_snapshot")
			finish()
			waitSuccess(t, e, first, "first delete_snapshot")
			waitSuccess(t, e, second, "second delete_snapshot")
			if _, ok := getSnapshot(t, e, box.ID, snap.ID); ok {
				t.Errorf("snapshot %d still found after two deletes", snap.ID)
			}
			if jobs := e.agent.jobsOf(agentapi.StorageJobDeleteSnapshot); len(jobs) != 1 {
				t.Errorf("delete-snapshot jobs %+v, want exactly one", jobs)
			}
		}},
		{"subaccount_delete_behind_box_delete", func(t *testing.T, e *boxEnv, box boxView) {
			sub := createSubaccount(t, e, box.ID, subaccountBody("orphan"))
			e.gate.Close()
			boxDel := startDelete(t, e, boxPath(box.ID), "delete_storage_box")
			release := e.gate.Next(t, "delete_storage_box")
			subDel := startDelete(t, e, subaccountPath(box.ID, sub.ID), "delete_subaccount")
			release()
			e.gate.Open()
			waitSuccess(t, e, boxDel, "delete_storage_box")
			waitSuccess(t, e, subDel, "delete_subaccount behind delete_storage_box")
			if _, ok := getBox(t, e, box.ID); ok {
				t.Errorf("box %d still found after delete_storage_box", box.ID)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newBoxEnv(t)
			c.run(t, e, createBox(t, e, boxBody("gone-at-turn")))
		})
	}
}
