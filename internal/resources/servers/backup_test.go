package servers_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/scheduler"
)

// testBackups holds the rows of backup_test.go; TestServers runs them.
func testBackups(t *testing.T) {
	t.Run("SRV-14", func(t *testing.T) {
		p := newRecPlatform()
		clk := newTestClock(time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC))
		env, mod, _ := newImageEnv(t, p, func(d *servers.Deps) { d.Clock = clk })
		id := createdServer(t, env, "backups", nil)

		wantFieldError(t, postAction(env, id, "create_image", map[string]any{"type": "backup"}),
			http.StatusUnprocessableEntity, "invalid_input", "type", "create_image type=backup without backups")

		snapshot := createImage(t, env, id, map[string]any{"type": "snapshot"})
		snapshotPath := imagePath(t, env, snapshot)

		runAction(t, env, id, "enable_backup", nil)
		first := createImage(t, env, id, map[string]any{"type": "backup"})
		img := getImage(t, env, first)
		if img.Type != "backup" || img.BoundTo == nil || *img.BoundTo != id {
			t.Errorf("manual backup: type %q bound_to %v, want backup bound to %d", img.Type, img.BoundTo, id)
		}
		firstPath := imagePath(t, env, first)

		// One daily backup, then manual ones until eight were made: the
		// slots are shared, so the oldest (the first manual one) goes.
		clk.Add(24 * time.Hour)
		runJob(t, mod, "backups")
		waitBackups(t, env, id, 2)
		daily := backupIDs(t, env, id)
		for range 5 {
			createImage(t, env, id, map[string]any{"type": "backup"})
		}
		if n := len(backupIDs(t, env, id)); n != 7 {
			t.Fatalf("backups after one daily and six manual: %d, want 7", n)
		}
		createImage(t, env, id, map[string]any{"type": "backup"})
		left := backupIDs(t, env, id)
		paths := map[int64]string{}
		for _, b := range left {
			paths[b] = imagePath(t, env, b)
		}
		if len(left) != 7 {
			t.Errorf("backups after the eighth: %d, want 7", len(left))
		}
		if slices.Contains(left, first) {
			t.Errorf("oldest backup %d still listed after the eighth backup", first)
		}
		if fileExists(firstPath) {
			t.Errorf("file %s of the oldest backup still exists", firstPath)
		}
		for _, d := range daily {
			if d != first && !slices.Contains(left, d) {
				t.Errorf("daily backup %d gone, want only the oldest gone", d)
			}
		}

		runAction(t, env, id, "disable_backup", nil)
		if left := backupIDs(t, env, id); len(left) != 0 {
			t.Errorf("backups after disable_backup: %v, want none", left)
		}
		for b, path := range paths {
			if fileExists(path) {
				t.Errorf("file %s of backup %d exists after disable_backup", path, b)
			}
		}

		runAction(t, env, id, "enable_backup", nil)
		last := createImage(t, env, id, map[string]any{"type": "backup"})
		lastPath := imagePath(t, env, last)
		deleteServer(t, env, id)
		if resp := env.Do(http.MethodGet, fmt.Sprintf("/images/%d", last), nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET /images/%d (backup) after deleting the server: status %d, want 404", last, resp.StatusCode)
		}
		if fileExists(lastPath) {
			t.Errorf("backup file %s exists after deleting the server", lastPath)
		}
		if resp := env.Do(http.MethodGet, fmt.Sprintf("/images/%d", snapshot), nil); resp.StatusCode != http.StatusOK {
			t.Errorf("GET /images/%d (snapshot) after deleting the server: status %d, want 200", snapshot, resp.StatusCode)
		}
		if !fileExists(snapshotPath) {
			t.Errorf("snapshot file %s gone after deleting the server", snapshotPath)
		}
	})

	t.Run("SRV-13", func(t *testing.T) {
		p := newRecPlatform()
		clk := newTestClock(time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC))
		env, mod, _ := newImageEnv(t, p, func(d *servers.Deps) { d.Clock = clk })
		on := createdServer(t, env, "daily-on", nil)
		off := createdServer(t, env, "daily-off", nil)
		runAction(t, env, on, "enable_backup", nil)

		runJob(t, mod, "backups")
		waitBackups(t, env, on, 1)
		firstDay := backupIDs(t, env, on)
		oldestPath := imagePath(t, env, firstDay[0])
		if n := len(backupIDs(t, env, off)); n != 0 {
			t.Errorf("backups of the server without backups: %d, want none", n)
		}

		clk.Add(time.Hour)
		runJob(t, mod, "backups")
		settle(t, env, on)
		if n := len(backupIDs(t, env, on)); n != 1 {
			t.Errorf("backups after a second run on the same day: %d, want 1", n)
		}

		for day := 2; day <= 7; day++ {
			clk.Add(24 * time.Hour)
			runJob(t, mod, "backups")
			waitBackups(t, env, on, day)
		}
		clk.Add(24 * time.Hour)
		runJob(t, mod, "backups")
		waitCreateImages(t, env, on, 8)
		settle(t, env, on)
		left := backupIDs(t, env, on)
		if len(left) != 7 || slices.Contains(left, firstDay[0]) {
			t.Errorf("backups on the eighth day %v, want 7 without the oldest %d", left, firstDay[0])
		}
		if fileExists(oldestPath) {
			t.Errorf("file %s of the oldest backup exists on the eighth day", oldestPath)
		}
		if n := len(backupIDs(t, env, off)); n != 0 {
			t.Errorf("backups of the server without backups after eight days: %d, want none", n)
		}

		locked := createdServer(t, env, "daily-locked", nil)
		runAction(t, env, locked, "enable_backup", nil)
		release := block(t, p.Fake, "Power")
		act, _ := startAction(t, env, locked, "poweroff", nil)
		expectLocked(t, env, locked, "poweroff")
		clk.Add(24 * time.Hour)
		runJob(t, mod, "backups")
		if n := len(backupIDs(t, env, locked)); n != 0 {
			t.Errorf("backups of a server locked by poweroff during the run: %d, want none", n)
		}
		release()
		if a := env.WaitAction(act); a.Status != "success" {
			t.Fatalf("poweroff action: %q %+v, want success", a.Status, a.Error)
		}
		settle(t, env, on)
		clk.Add(time.Hour)
		runJob(t, mod, "backups")
		waitBackups(t, env, locked, 1)
	})
}

// testClock is a scheduler.Clock the test moves with Add; After fires once
// the clock passed the deadline.
type testClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []clockWaiter
}

type clockWaiter struct {
	at time.Time
	ch chan time.Time
}

var _ scheduler.Clock = (*testClock)(nil)

func newTestClock(now time.Time) *testClock { return &testClock{now: now} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, clockWaiter{at: c.now.Add(d), ch: ch})
	return ch
}

// Add moves the clock forward and fires the waiters it passed.
func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
			continue
		}
		kept = append(kept, w)
	}
	c.waiters = kept
}

// runJob runs the module's job of that name once.
func runJob(t *testing.T, mod *servers.Module, name string) {
	t.Helper()
	for _, j := range mod.Jobs() {
		if j.Name == name {
			ctx, cancel := context.WithTimeout(context.Background(), apitest.ActionTimeout)
			defer cancel()
			j.Run(ctx)
			return
		}
	}
	t.Fatalf("servers module has no job %q", name)
}

// backupIDs are the IDs of the server's backups, ascending.
func backupIDs(t *testing.T, env *apitest.Env, serverID int64) []int64 {
	t.Helper()
	var ids []int64
	for _, img := range listImages(t, env, fmt.Sprintf("type=backup&bound_to=%d", serverID)) {
		ids = append(ids, img.ID)
	}
	slices.Sort(ids)
	return ids
}

// createImageActions counts the server's create_image actions.
func createImageActions(t *testing.T, env *apitest.Env, serverID int64) int {
	t.Helper()
	n := 0
	for _, a := range listActions(t, env, fmt.Sprintf("/servers/%d/actions", serverID)) {
		if a["command"] == "create_image" {
			n++
		}
	}
	return n
}

// waitCreateImages waits until the server has n create_image actions.
func waitCreateImages(t *testing.T, env *apitest.Env, serverID int64, n int) {
	t.Helper()
	deadline := time.Now().Add(apitest.ActionTimeout)
	for createImageActions(t, env, serverID) < n {
		if time.Now().After(deadline) {
			t.Fatalf("server %d has %d create_image actions after %s, want %d", serverID, createImageActions(t, env, serverID), apitest.ActionTimeout, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// settle waits until no action of the server runs.
func settle(t *testing.T, env *apitest.Env, serverID int64) {
	t.Helper()
	for _, a := range listActions(t, env, fmt.Sprintf("/servers/%d/actions", serverID)) {
		if a["status"] == "running" {
			if id, ok := a["id"].(float64); ok {
				env.WaitAction(int64(id))
			}
		}
	}
}

// waitBackups waits until the server has n backups and no running action.
func waitBackups(t *testing.T, env *apitest.Env, serverID int64, n int) {
	t.Helper()
	deadline := time.Now().Add(apitest.ActionTimeout)
	for {
		settle(t, env, serverID)
		got := len(backupIDs(t, env, serverID))
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server %d has %d backups after %s, want %d", serverID, got, apitest.ActionTimeout, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// wantFieldError is wantError that also expects field in
// error.details.fields.
func wantFieldError(t *testing.T, resp *http.Response, status int, code, field, what string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Errorf("%s: status %d, want %d", what, resp.StatusCode, status)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Fields []struct {
					Name string `json:"name"`
				} `json:"fields"`
			} `json:"details"`
		} `json:"error"`
	}
	apitest.Decode(t, resp, &body)
	if body.Error.Code != code {
		t.Errorf("%s: error.code %q, want %s", what, body.Error.Code, code)
	}
	for _, f := range body.Error.Details.Fields {
		if f.Name == field {
			return
		}
	}
	t.Errorf("%s: error.details.fields %+v, want %q", what, body.Error.Details.Fields, field)
}
