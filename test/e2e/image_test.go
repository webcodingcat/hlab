//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const srv12Marker = "/root/hlab-srv12-marker"

// testSRV12: a snapshot of a running server boots as a new server with the
// same disk content and its own hostname.
func testSRV12(t *testing.T) {
	e := sharedEnv(t)
	srv := ownServer(t, e, "srv12")
	content := harness.runID + "-srv12"
	if r, err := e.ssh(srv, time.Minute, "", "echo "+content+" > "+srv12Marker+" && sync"); err != nil {
		t.Fatalf("write marker: %v\n%s", err, r)
	}

	var body struct {
		Image struct {
			ID int64 `json:"id"`
		} `json:"image"`
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	e.post(t, fmt.Sprintf("/servers/%d/actions/create_image", srv.ID),
		map[string]any{"type": "snapshot", "description": harness.runID + " srv12"}, &body)
	t.Cleanup(func() { e.deleteImage(t, body.Image.ID) })
	if err := e.waitAction(body.Action.ID, 30*time.Minute); err != nil {
		t.Fatalf("create_image: %v", err)
	}

	copied := ownServerImage(t, e, "srv12-copy", fmt.Sprint(body.Image.ID))
	r, err := e.ssh(copied, time.Minute, "", "cat "+srv12Marker)
	if err != nil || strings.TrimSpace(r.Stdout) != content {
		t.Errorf("marker on the server from the snapshot: %v\n%s\nwant %q", err, r, content)
	}
	r, err = e.ssh(copied, time.Minute, "", "hostname")
	if err != nil || strings.TrimSpace(r.Stdout) != copied.Name {
		t.Errorf("hostname of the server from the snapshot: %v\n%s\nwant %q", err, r, copied.Name)
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
	takeServerSlot(t)
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
