//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	srv12Marker = "/root/hlab-srv12-marker"
	// srv12SnapshotUsers are the rows sharing the SRV-12 server and
	// snapshot: SRV-12 and SRV-08/own_snapshot.
	srv12SnapshotUsers = 2
)

// srv12Snapshot is a server with a marker file and a snapshot of it,
// created once for the rows that need both.
var srv12Snapshot struct {
	once    sync.Once
	e       *env
	name    string
	srv     *server
	image   int64
	content string // the marker's content
	slot    bool   // holds a server slot
	err     error

	mu       sync.Mutex
	done     int // rows that finished with it
	released bool
}

// sharedSnapshot returns the SRV-12 server, its snapshot and the marker
// content, created once; when every row using it has ended, the snapshot
// and the server are deleted (or at the end of TestE2E when a filter runs
// only some of them). own is the number of servers the row creates itself:
// the row that creates the fixture takes their slots together with the
// fixture's, so it never holds one slot while waiting for another.
func sharedSnapshot(t *testing.T, own int) (*env, *server, int64, string) {
	t.Helper()
	e := sharedEnv(t)
	f := &srv12Snapshot
	created := false
	f.once.Do(func() {
		created = true
		f.e = e
		f.name = harness.runID + "-srv12"
		got := serverSlots.acquire(1 + own)
		f.slot = true
		if got > 1 {
			t.Cleanup(func() { serverSlots.release(got - 1) })
		}
		f.srv, f.err = e.createServer(f.name)
		if f.err != nil {
			return
		}
		if f.err = e.waitReady(f.srv); f.err != nil {
			return
		}
		f.content = harness.runID + "-srv12"
		if r, err := e.ssh(f.srv, time.Minute, "", "echo "+f.content+" > "+srv12Marker+" && sync"); err != nil {
			f.err = fmt.Errorf("write marker: %v\n%s", err, r)
			return
		}
		var body struct {
			Image struct {
				ID int64 `json:"id"`
			} `json:"image"`
			Action struct {
				ID int64 `json:"id"`
			} `json:"action"`
		}
		path := fmt.Sprintf("/servers/%d/actions/create_image", f.srv.ID)
		status, aerr, err := e.apiDo(context.Background(), http.MethodPost, path,
			map[string]any{"type": "snapshot", "description": harness.runID + " srv12"}, &body)
		if err != nil || status != http.StatusCreated {
			f.err = fmt.Errorf("POST %s: %v (status %d %s: %s)", path, err, status, aerr.Code, aerr.Message)
			return
		}
		f.image = body.Image.ID
		if err := e.waitAction(body.Action.ID, 30*time.Minute); err != nil {
			f.err = fmt.Errorf("create_image: %v", err)
		}
	})
	if !created && own > 0 {
		// The fixture holds its own slot, which no other row waits on.
		takeServerSlots(t, own)
	}
	t.Cleanup(func() {
		f.mu.Lock()
		f.done++
		last := f.done == srv12SnapshotUsers
		f.mu.Unlock()
		if last {
			releaseSharedSnapshot(t)
		}
	})
	if f.err != nil {
		t.Fatalf("SRV-12 server and snapshot: %v", f.err)
	}
	return e, f.srv, f.image, f.content
}

// releaseSharedSnapshot deletes the snapshot and the server of
// sharedSnapshot once and gives back the server slot.
func releaseSharedSnapshot(t *testing.T) {
	t.Helper()
	f := &srv12Snapshot
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.released || f.e == nil {
		return
	}
	f.released = true
	f.e.deleteImage(t, f.image)
	deleteIfCreated(t, f.e, f.name)
	if f.slot {
		serverSlots.release(1)
	}
}

// testSRV12: a snapshot of a running server boots as a new server with the
// same disk content and its own hostname.
func testSRV12(t *testing.T) {
	e, _, image, content := sharedSnapshot(t, 1)

	copied := ownServerImage(t, e, "srv12-copy", fmt.Sprint(image))
	r, err := e.ssh(copied, time.Minute, "", "cat "+srv12Marker)
	if err != nil || strings.TrimSpace(r.Stdout) != content {
		t.Errorf("marker on the server from the snapshot: %v\n%s\nwant %q", err, r, content)
	}
	r, err = e.ssh(copied, time.Minute, "", "hostname")
	if err != nil || strings.TrimSpace(r.Stdout) != copied.Name {
		t.Errorf("hostname of the server from the snapshot: %v\n%s\nwant %q", err, r, copied.Name)
	}
}

// testSRV08OwnSnapshot: rebuilding a server from a snapshot of itself with
// new user_data runs that user_data, as for a new instance, and keeps the
// snapshot's content.
func testSRV08OwnSnapshot(t *testing.T) {
	e, srv, image, content := sharedSnapshot(t, 0)
	const marker = "/root/hlab-srv08-own-snapshot"
	value := harness.runID + "-srv08-own-snapshot"
	userData := "#cloud-config\nruncmd:\n  - [sh, -c, \"echo " + value + " > " + marker + "\"]\n"

	e.runAPIAction(t, fmt.Sprintf("/servers/%d/actions/rebuild", srv.ID),
		map[string]any{"image": image, "user_data": userData}, 30*time.Minute)
	if err := e.waitReady(srv); err != nil {
		t.Fatalf("SSH after rebuild from the own snapshot: %v", err)
	}

	r, err := e.ssh(srv, time.Minute, "", "cat "+marker)
	if err != nil || strings.TrimSpace(r.Stdout) != value {
		t.Errorf("new user_data did not run after rebuild from the own snapshot: %v\n%s\nwant %q", err, r, value)
	}
	r, err = e.ssh(srv, time.Minute, "", "cat "+srv12Marker)
	if err != nil || strings.TrimSpace(r.Stdout) != content {
		t.Errorf("snapshot marker after rebuild: %v\n%s\nwant %q", err, r, content)
	}
}

// createServerImage creates a server like createServer from image, with
// extra hcloud arguments added.
func (e *env) createServerImage(name, image string, extra ...string) (*server, error) {
	args := append([]string{"server", "create",
		"--name", name, "--type", serverType, "--location", serverLocation, "--image", image,
		"--ssh-key", e.sshKey, "--user-data-from-file", e.userData}, extra...)
	// The first server of an image downloads and converts it.
	r, err := e.hcloudRun(context.Background(), 45*time.Minute, args...)
	if err != nil {
		return nil, fmt.Errorf("hcloud server create %s from %s: %v\n%s", name, image, err, r)
	}
	return e.describeServer(name)
}

// ownServerImage is ownServer from image, with extra hcloud arguments.
func ownServerImage(t *testing.T, e *env, suffix, image string, extra ...string) *server {
	t.Helper()
	name := harness.runID + "-" + suffix
	t.Cleanup(func() { deleteIfCreated(t, e, name) })
	srv, err := e.createServerImage(name, image, extra...)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.waitReady(srv); err != nil {
		t.Fatal(err)
	}
	return srv
}

// post sends a POST that must answer 201 and decodes its body into out.
func (e *env) post(t *testing.T, path string, body, out any) {
	t.Helper()
	status, aerr, err := e.apiDo(context.Background(), http.MethodPost, path, body, out)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	if status != http.StatusCreated {
		t.Fatalf("POST %s: status %d %s: %s", path, status, aerr.Code, aerr.Message)
	}
}

// runAPIAction posts an action and waits for its success.
func (e *env) runAPIAction(t *testing.T, path string, body any, timeout time.Duration) {
	t.Helper()
	var b struct {
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	e.post(t, path, body, &b)
	if err := e.waitAction(b.Action.ID, timeout); err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
}

// deleteImage deletes a project image at the end of a row.
func (e *env) deleteImage(t *testing.T, id int64) {
	t.Helper()
	if id == 0 {
		return
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodDelete, fmt.Sprintf("/images/%d", id), nil, nil)
	if err != nil || (status != http.StatusNoContent && status != http.StatusOK && status != http.StatusNotFound) {
		t.Errorf("DELETE /images/%d: %v (status %d %s)", id, err, status, aerr.Code)
	}
}

// sshKeyID is the API ID of the run's SSH key.
func (e *env) sshKeyID(t *testing.T) int64 {
	t.Helper()
	var body struct {
		SSHKeys []struct {
			ID int64 `json:"id"`
		} `json:"ssh_keys"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, "/ssh_keys?name="+url.QueryEscape(e.sshKey), nil, &body)
	if err != nil || status != http.StatusOK || len(body.SSHKeys) != 1 {
		t.Fatalf("GET /ssh_keys?name=%s: %v (status %d %s, %d keys)", e.sshKey, err, status, aerr.Code, len(body.SSHKeys))
	}
	return body.SSHKeys[0].ID
}

// waitNewBoot waits until SSH with the run's key answers from a boot other
// than before and returns the new boot_id.
func (e *env) waitNewBoot(t *testing.T, srv *server, before string, timeout time.Duration) string {
	t.Helper()
	var id string
	if err := waitFor(timeout, 5*time.Second, func() (bool, error) {
		got, err := e.bootID(srv)
		id = got
		return err == nil && got != "" && got != before, nil
	}); err != nil {
		t.Fatalf("SSH to %s did not come back from a new boot: %v", srv.Name, err)
	}
	return id
}
