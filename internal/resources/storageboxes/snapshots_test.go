package storageboxes_test

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/resources/storageboxes"
)

// snapshotNameLayout is the UTC time in a snapshot name.
const snapshotNameLayout = "2006-01-02T15-04-05"

// manualName is the form of a manual snapshot's name.
var manualName = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}$`)

func snapshotsPath(box int64) string { return boxPath(box) + "/snapshots" }

func snapshotPath(box, id int64) string { return fmt.Sprintf("%s/%d", snapshotsPath(box), id) }

type snapshotView struct {
	ID          int64             `json:"id"`
	StorageBox  int64             `json:"storage_box"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	Stats       struct {
		Size           int64 `json:"size"`
		SizeFilesystem int64 `json:"size_filesystem"`
	} `json:"stats"`
	IsAutomatic bool      `json:"is_automatic"`
	Created     time.Time `json:"created"`
}

type createdSnapshot struct {
	Snapshot struct {
		ID         int64 `json:"id"`
		StorageBox int64 `json:"storage_box"`
	} `json:"snapshot"`
	Action actionView `json:"action"`
}

// startSnapshot posts a snapshot create for box, expects 201 with action
// create_snapshot and returns the answer without waiting.
func startSnapshot(t *testing.T, e *boxEnv, box int64, body map[string]any) createdSnapshot {
	t.Helper()
	resp := e.Do(http.MethodPost, snapshotsPath(box), body)
	if resp.StatusCode != http.StatusCreated {
		wantError(t, resp, http.StatusCreated, "", fmt.Sprintf("POST %s %v", snapshotsPath(box), body))
		t.FailNow()
	}
	var c createdSnapshot
	apitest.Decode(t, resp, &c)
	if c.Action.Command != "create_snapshot" {
		t.Errorf("POST %s: action %+v, want create_snapshot", snapshotsPath(box), c.Action)
	}
	return c
}

// createSnapshot creates a manual snapshot of box, waits for its action to
// succeed and returns the snapshot as GET shows it.
func createSnapshot(t *testing.T, e *boxEnv, box int64, body map[string]any) snapshotView {
	t.Helper()
	c := startSnapshot(t, e, box, body)
	waitSuccess(t, e, c.Action, "create_snapshot of box "+fmt.Sprint(box))
	return mustSnapshot(t, e, box, c.Snapshot.ID)
}

// getSnapshot returns snapshot id of box and whether it exists.
func getSnapshot(t *testing.T, e *boxEnv, box, id int64) (snapshotView, bool) {
	t.Helper()
	resp := e.Do(http.MethodGet, snapshotPath(box, id), nil)
	if resp.StatusCode == http.StatusNotFound {
		wantError(t, resp, http.StatusNotFound, "not_found", "GET "+snapshotPath(box, id))
		return snapshotView{}, false
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", snapshotPath(box, id), resp.StatusCode)
	}
	var b struct {
		Snapshot snapshotView `json:"snapshot"`
	}
	apitest.Decode(t, resp, &b)
	return b.Snapshot, true
}

func mustSnapshot(t *testing.T, e *boxEnv, box, id int64) snapshotView {
	t.Helper()
	s, ok := getSnapshot(t, e, box, id)
	if !ok {
		t.Fatalf("snapshot %d of box %d: 404, want it", id, box)
	}
	return s
}

// listSnapshots returns GET /storage_boxes/{box}/snapshots with query.
func listSnapshots(t *testing.T, e *boxEnv, box int64, query string) []snapshotView {
	t.Helper()
	path := snapshotsPath(box) + query
	resp := e.Do(http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
	}
	var b struct {
		Snapshots []snapshotView `json:"snapshots"`
	}
	apitest.Decode(t, resp, &b)
	return b.Snapshots
}

// snapshotNames returns the sorted names of list.
func snapshotNames(list []snapshotView) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.Name)
	}
	slices.Sort(out)
	return out
}

// sorted returns a sorted copy of names.
func sorted(names ...string) []string {
	out := slices.Clone(names)
	if out == nil {
		out = []string{}
	}
	slices.Sort(out)
	return out
}

// waitSnapshots waits up to apitest.ActionTimeout until the snapshots of
// box listed with query have exactly the names want.
func waitSnapshots(t *testing.T, e *boxEnv, box int64, query string, want []string) []snapshotView {
	t.Helper()
	want = sorted(want...)
	deadline := time.Now().Add(apitest.ActionTimeout)
	for {
		list := listSnapshots(t, e, box, query)
		got := snapshotNames(list)
		if slices.Equal(got, want) {
			return list
		}
		if time.Now().After(deadline) {
			t.Fatalf("snapshots of box %d%s: %v after %s, want %v", box, query, got, apitest.ActionTimeout, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitBoxIdle waits up to apitest.ActionTimeout until no action of box runs.
func waitBoxIdle(t *testing.T, e *boxEnv, box int64) {
	t.Helper()
	deadline := time.Now().Add(apitest.ActionTimeout)
	for slices.ContainsFunc(listActions(t, e, boxPath(box)+"/actions"), func(a actionView) bool { return a.Status == "running" }) {
		if time.Now().After(deadline) {
			t.Fatalf("box %d still has running actions after %s", box, apitest.ActionTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// hasJob reports whether jobs holds one for box user and snapshot name.
func hasJob(jobs []agentapi.StorageJob, user, name string) bool {
	return slices.ContainsFunc(jobs, func(j agentapi.StorageJob) bool { return j.Box == user && j.Snapshot == name })
}

// automaticName is the name of the automatic snapshot taken at at.
func automaticName(at time.Time) string { return "snapshot-daily--" + at.UTC().Format(snapshotNameLayout) }

func testSnapshotAPI(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("snapshots"))
	user := *box.Username

	labels := map[string]string{"a": "1", "b": "2"}
	c := startSnapshot(t, e, box.ID, map[string]any{"description": "first one", "labels": labels})
	if !c.Action.hasResource("storage_box", box.ID) || !c.Action.hasResource("storage_box_snapshot", c.Snapshot.ID) {
		t.Errorf("create_snapshot resources %+v, want storage_box %d and storage_box_snapshot %d",
			c.Action.Resources, box.ID, c.Snapshot.ID)
	}
	waitSuccess(t, e, c.Action, "create_snapshot")
	first := mustSnapshot(t, e, box.ID, c.Snapshot.ID)
	if first.StorageBox != box.ID || first.IsAutomatic || first.Description != "first one" || !maps.Equal(first.Labels, labels) {
		t.Errorf("created snapshot %+v, want storage_box %d, manual, description and labels as sent", first, box.ID)
	}
	if !manualName.MatchString(first.Name) {
		t.Errorf("manual snapshot name %q, want the form %s", first.Name, snapshotNameLayout)
	} else if at, err := time.Parse(snapshotNameLayout, first.Name); err != nil || at.Sub(first.Created.UTC()).Abs() > time.Minute {
		t.Errorf("manual snapshot name %q, want the UTC time of its creation %s", first.Name, first.Created.UTC())
	}
	if !hasJob(e.agent.jobsOf(agentapi.StorageJobSnapshot), user, first.Name) {
		t.Errorf("no snapshot job for box %s snapshot %s; jobs %+v", user, first.Name, e.agent.jobsOf(agentapi.StorageJobSnapshot))
	}

	e.clock.set(e.clock.Now().Add(time.Second))
	second := createSnapshot(t, e, box.ID, map[string]any{})

	if got, want := snapshotNames(listSnapshots(t, e, box.ID, "")), sorted(first.Name, second.Name); !slices.Equal(got, want) {
		t.Errorf("list: %v, want %v", got, want)
	}
	if got, want := snapshotNames(listSnapshots(t, e, box.ID, "?is_automatic=false")), sorted(first.Name, second.Name); !slices.Equal(got, want) {
		t.Errorf("list is_automatic=false: %v, want %v", got, want)
	}
	if got := listSnapshots(t, e, box.ID, "?is_automatic=true"); len(got) != 0 {
		t.Errorf("list is_automatic=true: %v, want none", snapshotNames(got))
	}
	var ids []int64
	for _, s := range listSnapshots(t, e, box.ID, "?sort=id:desc") {
		ids = append(ids, s.ID)
	}
	if want := []int64{second.ID, first.ID}; !slices.Equal(ids, want) {
		t.Errorf("list sort=id:desc: ids %v, want %v", ids, want)
	}

	newLabels := map[string]string{"c": "3"}
	resp := e.Do(http.MethodPut, snapshotPath(box.ID, first.ID), map[string]any{"description": "changed", "labels": newLabels})
	if resp.StatusCode != http.StatusOK {
		wantError(t, resp, http.StatusOK, "", "PUT "+snapshotPath(box.ID, first.ID))
		t.FailNow()
	}
	var put struct {
		Snapshot snapshotView `json:"snapshot"`
	}
	apitest.Decode(t, resp, &put)
	if put.Snapshot.Description != "changed" || !maps.Equal(put.Snapshot.Labels, newLabels) {
		t.Errorf("PUT answer description %q labels %v, want changed %v", put.Snapshot.Description, put.Snapshot.Labels, newLabels)
	}
	if got := mustSnapshot(t, e, box.ID, first.ID); got.Description != "changed" || !maps.Equal(got.Labels, newLabels) {
		t.Errorf("GET after PUT: description %q labels %v, want changed %v (labels replaced)", got.Description, got.Labels, newLabels)
	}

	a := startAction(t, e, http.MethodDelete, snapshotPath(box.ID, second.ID), nil, http.StatusCreated)
	if a.Command != "delete_snapshot" {
		t.Errorf("DELETE %s: action %q, want delete_snapshot", snapshotPath(box.ID, second.ID), a.Command)
	}
	waitSuccess(t, e, a, "delete_snapshot")
	if _, ok := getSnapshot(t, e, box.ID, second.ID); ok {
		t.Errorf("snapshot %d still found after delete_snapshot", second.ID)
	}
	if !hasJob(e.agent.jobsOf(agentapi.StorageJobDeleteSnapshot), user, second.Name) {
		t.Errorf("no delete-snapshot job for box %s snapshot %s", user, second.Name)
	}

	limit := e.storageType(t, "bx11").SnapshotLimit
	if limit == nil {
		t.Fatal("recorded bx11 has no snapshot_limit")
	}
	for n := len(listSnapshots(t, e, box.ID, "")); n < *limit; n++ {
		createSnapshot(t, e, box.ID, map[string]any{})
	}
	b := wantError(t, e.Do(http.MethodPost, snapshotsPath(box.ID), map[string]any{}), http.StatusForbidden,
		"resource_limit_exceeded", fmt.Sprintf("create beyond snapshot_limit %d", *limit))
	if !slices.ContainsFunc(b.Error.Details.Limits, func(l struct {
		Name string `json:"name"`
	}) bool {
		return l.Name == "snapshot_limit"
	}) {
		t.Errorf("limit answer details.limits %+v, want snapshot_limit", b.Error.Details.Limits)
	}
	if got := len(listSnapshots(t, e, box.ID, "")); got != *limit {
		t.Errorf("after the refused create: %d snapshots, want %d", got, *limit)
	}

	failing := createBox(t, e, boxBody("snapshot-fails"))
	e.agent.setAnswer(agentapi.StorageJobSnapshot, func(agentapi.StorageJob) (agentapi.StorageJobResult, error) {
		return agentapi.StorageJobResult{}, errAgent
	})
	fc := startSnapshot(t, e, failing.ID, map[string]any{})
	if got := e.For(t).WaitAction(fc.Action.ID); got.Status != "error" {
		t.Fatalf("create_snapshot with a failing snapshot job: action %q, want error", got.Status)
	}
	if _, ok := getSnapshot(t, e, failing.ID, fc.Snapshot.ID); ok {
		t.Errorf("snapshot %d exists after its snapshot job failed, want it deleted", fc.Snapshot.ID)
	}
	if got := listSnapshots(t, e, failing.ID, ""); len(got) != 0 {
		t.Errorf("snapshots after the failed create: %v, want none", snapshotNames(got))
	}
}

// enablePlan enables a snapshot plan on box.
func enablePlan(t *testing.T, e *boxEnv, box int64, plan map[string]any) {
	t.Helper()
	runAction(t, e, boxActionPath(box, "enable_snapshot_plan"), plan, "enable_snapshot_plan")
}

// startPlans starts the scheduled jobs and lets the plan job run once, one
// interval after the start.
func startPlans(t *testing.T, e *boxEnv) {
	t.Helper()
	startJobs(t, e)
	runPlansAt(t, e, e.clock.Now().Add(storageboxes.PlanEvery))
}

func testSnapshotPlanAtTime(t *testing.T) {
	at := func(month time.Month, day, hour, minute int) time.Time {
		return time.Date(2026, month, day, hour, minute, 0, 0, time.UTC)
	}
	type step struct {
		at    time.Time
		takes bool
	}
	// 2026-01-01 is a Thursday; plans run at 03:00 UTC.
	cases := []struct {
		name  string
		days  map[string]any
		steps []step
	}{
		{"every_day", nil, []step{
			{at(1, 1, 2, 59), false}, {at(1, 1, 3, 0), true}, {at(1, 1, 12, 0), false}, {at(1, 2, 3, 0), true},
		}},
		{"day_of_week", map[string]any{"day_of_week": 5}, []step{
			{at(1, 1, 3, 0), false}, {at(1, 2, 3, 0), true}, {at(1, 3, 3, 0), false},
		}},
		{"day_of_month", map[string]any{"day_of_month": 3}, []step{
			{at(1, 1, 3, 0), false}, {at(1, 2, 3, 0), false}, {at(1, 3, 3, 0), true}, {at(1, 4, 3, 0), false},
		}},
		{"both", map[string]any{"day_of_week": 1, "day_of_month": 12}, []step{
			{at(1, 5, 3, 0), false}, {at(1, 12, 3, 0), true}, {at(2, 12, 3, 0), false},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newBoxEnv(t)
			box := createBox(t, e, boxBody("plan-"+tc.name))
			plan := map[string]any{"max_snapshots": 10, "minute": 0, "hour": 3}
			maps.Copy(plan, tc.days)
			enablePlan(t, e, box.ID, plan)
			startPlans(t, e)

			want := []string{}
			for _, s := range tc.steps {
				runPlansAt(t, e, s.at)
				if s.takes {
					want = append(want, automaticName(s.at))
				}
				waitSnapshots(t, e, box.ID, "?is_automatic=true", want)
			}
			waitBoxIdle(t, e, box.ID)
			list := waitSnapshots(t, e, box.ID, "", want)
			for _, s := range list {
				if !s.IsAutomatic {
					t.Errorf("snapshot %s of the plan: is_automatic false, want true", s.Name)
				}
			}
		})
	}
}

func testSnapshotPlanRetention(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("retention"))
	user := *box.Username
	manual := createSnapshot(t, e, box.ID, map[string]any{})
	enablePlan(t, e, box.ID, map[string]any{"max_snapshots": 2, "minute": 0, "hour": 3})
	startPlans(t, e)

	var taken []string
	for day := 1; day <= 4; day++ {
		when := time.Date(2026, 1, day, 3, 0, 0, 0, time.UTC)
		runPlansAt(t, e, when)
		taken = append(taken, automaticName(when))
		want := taken[max(0, len(taken)-2):]
		waitSnapshots(t, e, box.ID, "?is_automatic=true", want)
		if _, ok := getSnapshot(t, e, box.ID, manual.ID); !ok {
			t.Fatalf("manual snapshot %s gone after the plan run of day %d", manual.Name, day)
		}
	}
	waitBoxIdle(t, e, box.ID)
	waitSnapshots(t, e, box.ID, "", append([]string{manual.Name}, taken[2:]...))
	deleted := e.agent.jobsOf(agentapi.StorageJobDeleteSnapshot)
	for _, name := range taken[:2] {
		if !hasJob(deleted, user, name) {
			t.Errorf("no delete-snapshot job for the pruned snapshot %s", name)
		}
	}
	if hasJob(deleted, user, manual.Name) {
		t.Errorf("delete-snapshot job for the manual snapshot %s", manual.Name)
	}
}

// rollbackJob waits up to apitest.ActionTimeout for the n-th rollback job
// (counting from 1) and returns it.
func rollbackJob(t *testing.T, e *boxEnv, n int) agentapi.StorageJob {
	t.Helper()
	deadline := time.Now().Add(apitest.ActionTimeout)
	for {
		if jobs := e.agent.jobsOf(agentapi.StorageJobRollback); len(jobs) >= n {
			return jobs[n-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no rollback job %d within %s", n, apitest.ActionTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func wantRollbackJob(t *testing.T, j agentapi.StorageJob, user, target string, newer []string) {
	t.Helper()
	if j.Box != user || j.Snapshot != target || !slices.Equal(sorted(j.Newer...), sorted(newer...)) {
		t.Errorf("rollback job box %q snapshot %q newer %v, want %q %q %v", j.Box, j.Snapshot, j.Newer, user, target, sorted(newer...))
	}
}

func testRollbackAPI(t *testing.T) {
	e := newBoxEnv(t)
	box := createBox(t, e, boxBody("rollback"))
	user := *box.Username
	path := boxActionPath(box.ID, "rollback_snapshot")
	enablePlan(t, e, box.ID, map[string]any{"max_snapshots": 10, "minute": 5, "hour": 0})
	startPlans(t, e)

	next := func() { e.clock.set(e.clock.Now().Add(time.Second)) }
	s0 := createSnapshot(t, e, box.ID, map[string]any{})
	next()
	target := createSnapshot(t, e, box.ID, map[string]any{})
	planAt := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)
	runPlansAt(t, e, planAt)
	auto := automaticName(planAt)
	waitSnapshots(t, e, box.ID, "?is_automatic=true", []string{auto})
	next()
	s2 := createSnapshot(t, e, box.ID, map[string]any{})

	release := make(chan struct{})
	e.agent.setAnswer(agentapi.StorageJobRollback, func(agentapi.StorageJob) (agentapi.StorageJobResult, error) {
		select {
		case <-release:
			return agentapi.StorageJobResult{}, nil
		case <-time.After(apitest.ActionTimeout):
			return agentapi.StorageJobResult{}, errors.New("rollback job held too long")
		}
	})
	a := startAction(t, e, http.MethodPost, path, map[string]string{"snapshot": target.Name}, http.StatusCreated)
	if a.Command != "rollback_snapshot" {
		t.Errorf("POST %s: action %q, want rollback_snapshot", path, a.Command)
	}
	wantRollbackJob(t, rollbackJob(t, e, 1), user, target.Name, []string{auto, s2.Name})
	if got, want := snapshotNames(listSnapshots(t, e, box.ID, "")), sorted(s0.Name, target.Name, auto, s2.Name); !slices.Equal(got, want) {
		t.Errorf("snapshots while the rollback job runs: %v, want all of %v", got, want)
	}
	close(release)
	waitSuccess(t, e, a, "rollback_snapshot by name")
	e.agent.setAnswer(agentapi.StorageJobRollback, nil)
	if got, want := snapshotNames(listSnapshots(t, e, box.ID, "")), sorted(s0.Name, target.Name); !slices.Equal(got, want) {
		t.Errorf("snapshots after rollback to %s: %v, want %v", target.Name, got, want)
	}

	next()
	s3 := createSnapshot(t, e, box.ID, map[string]any{})
	runAction(t, e, path, map[string]string{"snapshot": strconv.FormatInt(s0.ID, 10)}, "rollback_snapshot")
	wantRollbackJob(t, rollbackJob(t, e, 2), user, s0.Name, []string{target.Name, s3.Name})
	if got, want := snapshotNames(listSnapshots(t, e, box.ID, "")), sorted(s0.Name); !slices.Equal(got, want) {
		t.Errorf("snapshots after rollback to %s by ID: %v, want %v", s0.Name, got, want)
	}

	for _, unknown := range []string{"no-such-snapshot", "999999"} {
		wantError(t, e.Do(http.MethodPost, path, map[string]string{"snapshot": unknown}), http.StatusNotFound, "not_found",
			"rollback_snapshot to unknown "+unknown)
	}
	if n := len(e.agent.jobsOf(agentapi.StorageJobRollback)); n != 2 {
		t.Errorf("%d rollback jobs, want 2 (none for unknown snapshots)", n)
	}
}
