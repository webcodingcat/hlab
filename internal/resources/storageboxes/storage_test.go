package storageboxes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/agentclient"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/ipam"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/resources/storageboxes"
	"github.com/webcodingcat/hlab/internal/scheduler"
	"github.com/webcodingcat/hlab/internal/store"
)

// errAgent is the error the fake storage VM answers an Apply with.
var errAgent = errors.New("storage agent unreachable")

// storedIP returns the _hlab.ip of stored box id.
func storedIP(t *testing.T, e *boxEnv, id int64) netip.Addr {
	t.Helper()
	raw, _ := storedBox(t, e, id)
	var doc struct {
		HLab struct {
			IP string `json:"ip"`
		} `json:"_hlab"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("stored box %d: %v", id, err)
	}
	a, err := netip.ParseAddr(doc.HLab.IP)
	if err != nil {
		t.Fatalf("stored box %d: _hlab.ip %q: %v", id, doc.HLab.IP, err)
	}
	return a
}

// ipInUse reports whether ipam has a marked in use.
func ipInUse(t *testing.T, e *boxEnv, a netip.Addr) bool {
	t.Helper()
	var used []netip.Addr
	err := e.Store.Tx(func(tx store.Tx) error {
		var err error
		// Outside a range holding only 0.0.0.0 lists every address in use.
		used, _, err = ipam.Outside(tx, netip.MustParsePrefix("0.0.0.0/32"))
		return err
	})
	if err != nil {
		t.Fatalf("addresses in use: %v", err)
	}
	for _, u := range used {
		if u == a {
			return true
		}
	}
	return false
}

// wantActionError waits for action a and fails unless it ends in error
// carrying errAgent.
func wantActionError(t *testing.T, e *boxEnv, a actionView, what string) {
	t.Helper()
	got := e.For(t).WaitAction(a.ID)
	if got.Status != "error" || got.Error == nil {
		t.Fatalf("%s: action %d ended %q %+v, want error", what, a.ID, got.Status, got.Error)
	}
	if !strings.Contains(got.Error.Message, errAgent.Error()) {
		t.Errorf("%s: action error %q, want it to carry %q", what, got.Error.Message, errAgent)
	}
}

func testCreateFailed(t *testing.T) {
	e := newBoxEnv(t)
	e.gate.Close()
	b := startBox(t, e, boxBody("failing"))
	release := e.gate.Next(t, "create_storage_box")
	ip := storedIP(t, e, b.Box.ID)
	if !ipInUse(t, e, ip) {
		t.Fatalf("IP %s of the box being created is not in use", ip)
	}
	e.failApply(errAgent)
	release()

	wantActionError(t, e, b.Action, "failed create_storage_box")
	if _, ok := getBox(t, e, b.Box.ID); ok {
		t.Errorf("box %d exists after its create failed, want it deleted", b.Box.ID)
	}
	if ipInUse(t, e, ip) {
		t.Errorf("IP %s still in use after the create failed, want it released", ip)
	}
}

func testDeleteConfirmed(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("kept"))
	sub := createSubaccount(t, e, box.ID, subaccountBody("sub"))
	ip := storedIP(t, e, box.ID)

	e.gate.Close()
	a := startAction(t, e, http.MethodDelete, boxPath(box.ID), nil, http.StatusCreated)
	release := e.gate.Next(t, "delete_storage_box")
	if got := e.For(t).GetAction(a.ID); got.Status != "running" {
		t.Errorf("delete before Apply confirmed: action %q, want running", got.Status)
	}
	mustBox(t, e, box.ID)
	e.failApply(errAgent)
	release()
	wantActionError(t, e, a, "delete_storage_box with a failing Apply")

	mustBox(t, e, box.ID)
	mustSubaccount(t, e, box.ID, sub.ID)
	if got := storedIP(t, e, box.ID); got != ip || !ipInUse(t, e, ip) {
		t.Errorf("after the failed delete: IP %s (in use %v), want %s in use", got, ipInUse(t, e, ip), ip)
	}

	e.failApply(nil)
	e.gate.Open()
	deleteBox(t, e, box.ID)
	if _, ok := getBox(t, e, box.ID); ok {
		t.Errorf("box %d exists after its delete succeeded", box.ID)
	}
	if ipInUse(t, e, ip) {
		t.Errorf("IP %s still in use after the delete succeeded", ip)
	}
}

func testNotActive(t *testing.T) {
	e := newBoxEnv(t)
	e.gate.Close()
	b := startBox(t, e, boxBody("initializing"))
	release := e.gate.Next(t, "create_storage_box")
	if got := mustBox(t, e, b.Box.ID); got.Status == "active" {
		t.Fatalf("box status %q during its create, want not active", got.Status)
	}

	cases := []struct {
		name, method, path string
		body               any
	}{
		{"create_subaccount", http.MethodPost, subaccountsPath(b.Box.ID), subaccountBody("sub")},
		{"change_type", http.MethodPost, boxActionPath(b.Box.ID, "change_type"), map[string]string{"storage_box_type": "bx21"}},
		{"folders", http.MethodGet, boxPath(b.Box.ID) + "/folders", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantError(t, e.For(t).Do(tc.method, tc.path, tc.body), http.StatusLocked, "locked",
				tc.name+" on a box that is not active")
		})
	}

	release()
	e.gate.Open()
	waitSuccess(t, e, b.Action, "create_storage_box")
}

func testFoldersPath(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("folders"))
	e.agent.setFolders(func(j agentapi.StorageJob) (agentapi.StorageJobResult, error) {
		return agentapi.StorageJobResult{}, &agentclient.JobError{Status: http.StatusNotFound, Code: "not_found"}
	})

	cases := []struct {
		name, path string
		status     int // 422 means invalid_input on field path
		asksAgent  bool
	}{
		{"absolute", "/etc", http.StatusUnprocessableEntity, false},
		{"dotdot", "data/../../other", http.StatusUnprocessableEntity, false},
		{"missing", "nothere", http.StatusNotFound, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(e.agent.jobsOf(agentapi.StorageJobFolders))
			path := boxPath(box.ID) + "/folders?" + url.Values{"path": {tc.path}}.Encode()
			resp := e.For(t).Do(http.MethodGet, path, nil)
			if tc.status == http.StatusUnprocessableEntity {
				wantFieldError(t, resp, "path", "folders of "+tc.path)
			} else {
				wantError(t, resp, tc.status, "not_found", "folders of "+tc.path)
			}
			if asked := len(e.agent.jobsOf(agentapi.StorageJobFolders)) > before; asked != tc.asksAgent {
				t.Errorf("folders of %q: agent asked %v, want %v", tc.path, asked, tc.asksAgent)
			}
		})
	}
}

func testChangeTypeUsage(t *testing.T) {
	e := newBoxEnv(t)
	body := boxBody("usage")
	body["storage_box_type"] = "bx21"
	box := createBox(t, e, body)
	small := e.storageType(t, "bx11")
	change := map[string]string{"storage_box_type": "bx11"}

	e.agent.setUsage(agentapi.Usage{Data: small.Size - 1, Snapshots: 2})
	wantFieldError(t, e.Do(http.MethodPost, boxActionPath(box.ID, "change_type"), change),
		"storage_box_type", "change_type to bx11 with more data and snapshots than it holds")
	if got := mustBox(t, e, box.ID); got.StorageBoxType.Name != "bx21" {
		t.Errorf("after refused change_type: storage_box_type %q, want bx21", got.StorageBoxType.Name)
	}

	e.agent.setUsage(agentapi.Usage{Data: small.Size / 4, Snapshots: small.Size / 4})
	runAction(t, e, boxActionPath(box.ID, "change_type"), change, "change_type")
	if got := mustBox(t, e, box.ID); got.StorageBoxType.Name != "bx11" {
		t.Errorf("after change_type that fits: storage_box_type %q, want bx11", got.StorageBoxType.Name)
	}
}

func testFreeSpace(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("small"))
	e.free.Store(storageboxes.FreeSpaceReserve - 1)

	wantError(t, postBox(e, boxBody("no-room")), http.StatusPreconditionFailed, "resource_unavailable",
		"create with less than the free space reserve")
	wantError(t, e.Do(http.MethodPost, boxActionPath(box.ID, "change_type"), map[string]string{"storage_box_type": "bx21"}),
		http.StatusPreconditionFailed, "resource_unavailable", "change_type to a larger type with less than the reserve")
}

func testStats(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("stats"))
	sch, ok := any(e.mod).(resource.Scheduled)
	if !ok {
		t.Fatal("storage box module has no scheduled jobs")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		scheduler.Run(ctx, e.clock, sch.Jobs())
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(apitest.ActionTimeout):
			t.Errorf("scheduler still runs %s after the test", apitest.ActionTimeout)
		}
	})

	want := agentapi.Usage{Data: 3 << 20, Snapshots: 1 << 20}
	e.agent.setUsage(want)
	if got := mustBox(t, e, box.ID).Stats; got.SizeData == want.Data {
		t.Fatalf("stats %+v before the tick, want the usage from before it", got)
	}
	e.clock.tick(t, storageboxes.StatsEvery)

	deadline := time.Now().Add(apitest.ActionTimeout)
	for {
		got := mustBox(t, e, box.ID).Stats
		if got.SizeData == want.Data && got.SizeSnapshots == want.Snapshots && got.Size == want.Data+want.Snapshots {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stats %+v %s after the tick, want size_data %d, size_snapshots %d, size %d",
				got, apitest.ActionTimeout, want.Data, want.Snapshots, want.Data+want.Snapshots)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
