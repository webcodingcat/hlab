//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// The storage box rows of the clients (CLI-05 hcloud, CLI-06 OpenTofu,
// CLI-07 Ansible) share the helpers of this file: an hcloud environment
// with hlab's Hetzner endpoint, readers of boxes, subaccounts and
// snapshots through hetznerDo, and the cleanup of boxes a client made.

// hetznerEnvVar is the variable hcloud, the OpenTofu provider and the
// Ansible collection read the Hetzner API endpoint from (E12, E13, E15).
const hetznerEnvVar = "HETZNER_ENDPOINT"

// boxProjectEnv is a project of its own (newProjectEnv); its clients get
// hlab's Cloud and Hetzner endpoints and the project's token only.
func boxProjectEnv(t *testing.T, suffix string) *env {
	t.Helper()
	return newProjectEnv(t, suffix)
}

// boxAccess is the access_settings object of a box or a subaccount;
// Readonly belongs to subaccounts, ZFSEnabled to boxes.
type boxAccess struct {
	ReachableExternally bool `json:"reachable_externally"`
	SambaEnabled        bool `json:"samba_enabled"`
	SSHEnabled          bool `json:"ssh_enabled"`
	WebDAVEnabled       bool `json:"webdav_enabled"`
	ZFSEnabled          bool `json:"zfs_enabled"`
	Readonly            bool `json:"readonly"`
}

// snapshotPlan is the snapshot_plan of a box; nil fields are "every".
type snapshotPlan struct {
	MaxSnapshots int  `json:"max_snapshots"`
	Minute       *int `json:"minute"`
	Hour         *int `json:"hour"`
	DayOfWeek    *int `json:"day_of_week"`
	DayOfMonth   *int `json:"day_of_month"`
}

// boxInfo is a storage box as the Hetzner API (and a client's JSON output
// of it) describes it.
type boxInfo struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Username       *string `json:"username"`
	Server         *string `json:"server"`
	Status         string  `json:"status"`
	StorageBoxType struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"storage_box_type"`
	Location struct {
		Name string `json:"name"`
	} `json:"location"`
	Labels     map[string]string `json:"labels"`
	Protection struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
	AccessSettings boxAccess     `json:"access_settings"`
	SnapshotPlan   *snapshotPlan `json:"snapshot_plan"`
}

// summary is what a client's view of the box must share with the API's;
// the snapshot plan is left out (hcloud prints Sunday as 0, E20).
func (b boxInfo) summary() string {
	deref := func(s *string) string {
		if s == nil {
			return "<null>"
		}
		return *s
	}
	return fmt.Sprintf("id=%d name=%s username=%s server=%s type=%s location=%s labels=%v delete=%t access=%+v",
		b.ID, b.Name, deref(b.Username), deref(b.Server), b.StorageBoxType.Name, b.Location.Name,
		labelsOf(b.Labels), b.Protection.Delete, b.AccessSettings)
}

// labelsOf makes a nil label map comparable to an empty one.
func labelsOf(l map[string]string) map[string]string {
	if l == nil {
		return map[string]string{}
	}
	return l
}

// subaccountInfo is a subaccount as the Hetzner API describes it.
type subaccountInfo struct {
	ID             int64             `json:"id"`
	Name           string            `json:"name"`
	Username       string            `json:"username"`
	Server         string            `json:"server"`
	HomeDirectory  string            `json:"home_directory"`
	Description    string            `json:"description"`
	Labels         map[string]string `json:"labels"`
	AccessSettings boxAccess         `json:"access_settings"`
}

// summary is what a client's view of the subaccount must share with the
// API's.
func (s subaccountInfo) summary() string {
	return fmt.Sprintf("id=%d name=%s username=%s server=%s home=%s description=%q labels=%v access=%+v",
		s.ID, s.Name, s.Username, s.Server, s.HomeDirectory, s.Description, labelsOf(s.Labels), s.AccessSettings)
}

// hetznerGetOK sends GET path to the Hetzner API and fails the test unless
// it answers 200; the body goes into out.
func (e *env) hetznerGetOK(t *testing.T, path string, out any) {
	t.Helper()
	status, aerr, err := e.hetznerDo(context.Background(), http.MethodGet, path, nil, out)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET %s%s: %v (status %d %s: %s)", hetznerEndpoint, path, err, status, aerr.Code, aerr.Message)
	}
}

// boxByID reads a box.
func (e *env) boxByID(t *testing.T, id int64) boxInfo {
	t.Helper()
	var body struct {
		StorageBox boxInfo `json:"storage_box"`
	}
	e.hetznerGetOK(t, fmt.Sprintf("/storage_boxes/%d", id), &body)
	return body.StorageBox
}

// boxByName reads the one box of the project named name.
func (e *env) boxByName(t *testing.T, name string) boxInfo {
	t.Helper()
	boxes := e.boxesNamed(t, name)
	if len(boxes) != 1 {
		t.Fatalf("GET /storage_boxes?name=%s: %d boxes, want 1", name, len(boxes))
	}
	return boxes[0]
}

// boxesNamed lists the boxes of the project named name.
func (e *env) boxesNamed(t *testing.T, name string) []boxInfo {
	t.Helper()
	var body struct {
		StorageBoxes []boxInfo `json:"storage_boxes"`
	}
	e.hetznerGetOK(t, "/storage_boxes?name="+url.QueryEscape(name), &body)
	return body.StorageBoxes
}

// subaccountsOf lists the subaccounts of the box.
func (e *env) subaccountsOf(t *testing.T, boxID int64) []subaccountInfo {
	t.Helper()
	var body struct {
		Subaccounts []subaccountInfo `json:"subaccounts"`
	}
	e.hetznerGetOK(t, fmt.Sprintf("/storage_boxes/%d/subaccounts?per_page=50", boxID), &body)
	return body.Subaccounts
}

// subaccountByID reads a subaccount of the box.
func (e *env) subaccountByID(t *testing.T, boxID, id int64) subaccountInfo {
	t.Helper()
	var body struct {
		Subaccount subaccountInfo `json:"subaccount"`
	}
	e.hetznerGetOK(t, fmt.Sprintf("/storage_boxes/%d/subaccounts/%d", boxID, id), &body)
	return body.Subaccount
}

// subaccountByName finds the subaccount of the box named name.
func (e *env) subaccountByName(t *testing.T, boxID int64, name string) subaccountInfo {
	t.Helper()
	for _, s := range e.subaccountsOf(t, boxID) {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("GET /storage_boxes/%d/subaccounts lists no subaccount named %q", boxID, name)
	return subaccountInfo{}
}

// snapshotsOf lists the snapshots of the box (listSnapshots by ID).
func (e *env) snapshotsOf(t *testing.T, boxID int64) []boxSnapshot {
	t.Helper()
	return e.listSnapshots(t, &storageBox{ID: boxID})
}

// snapshotByDescription finds the snapshot of the box with the
// description.
func (e *env) snapshotByDescription(t *testing.T, boxID int64, description string) boxSnapshot {
	t.Helper()
	for _, s := range e.snapshotsOf(t, boxID) {
		if s.Description == description {
			return s
		}
	}
	t.Fatalf("GET /storage_boxes/%d/snapshots lists no snapshot described %q", boxID, description)
	return boxSnapshot{}
}

// boxTypes lists the storage box types the Hetzner API serves, ID to name.
func (e *env) boxTypes(t *testing.T) map[int64]string {
	t.Helper()
	var body struct {
		Types []named `json:"storage_box_types"`
	}
	e.hetznerGetOK(t, "/storage_box_types?per_page=50", &body)
	return namedOf(body.Types)
}

// assertHetznerGone fails unless GET path of the Hetzner API answers 404
// within boxActionTimeout.
func (e *env) assertHetznerGone(t *testing.T, path string) {
	t.Helper()
	var last int
	err := waitFor(boxActionTimeout, 2*time.Second, func() (bool, error) {
		status, _, err := e.hetznerDo(context.Background(), http.MethodGet, path, nil, nil)
		last = status
		return err == nil && status == http.StatusNotFound, nil
	})
	if err != nil {
		t.Errorf("GET %s still answers %d: %v", path, last, err)
	}
}

// assertBoxActionDone fails unless GET /storage_boxes/{id}/actions lists a
// successful action with the command (subaccount actions included).
func (e *env) assertBoxActionDone(t *testing.T, boxID int64, command string) {
	t.Helper()
	var body struct {
		Actions []struct {
			Command string `json:"command"`
			Status  string `json:"status"`
		} `json:"actions"`
	}
	path := fmt.Sprintf("/storage_boxes/%d/actions?per_page=50&sort=id:desc", boxID)
	e.hetznerGetOK(t, path, &body)
	for _, a := range body.Actions {
		if a.Command == command && a.Status == "success" {
			return
		}
	}
	t.Errorf("GET %s shows no successful %s action: %+v", path, command, body.Actions)
}

// assertLabels fails unless got equals want, nil counting as empty.
func assertLabels(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	if !maps.Equal(labelsOf(got), labelsOf(want)) {
		t.Errorf("%s: labels %v, want %v", what, got, want)
	}
}

// cleanupBoxesNamed deletes, when the test ends, every box of the project
// named one of names (a client made them), delete protection turned off
// first.
func cleanupBoxesNamed(t *testing.T, e *env, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, name := range names {
			var body struct {
				StorageBoxes []boxInfo `json:"storage_boxes"`
			}
			status, _, err := e.hetznerDo(context.Background(), http.MethodGet, "/storage_boxes?name="+url.QueryEscape(name), nil, &body)
			if err != nil || status != http.StatusOK {
				t.Errorf("cleanup: GET /storage_boxes?name=%s: status %d %v", name, status, err)
				continue
			}
			for _, b := range body.StorageBoxes {
				if b.Protection.Delete {
					unprotectBox(t, e, b.ID)
				}
				if err := e.deleteStorageBox(b.ID); err != nil {
					t.Errorf("cleanup: %v", err)
				}
			}
		}
	})
}

// unprotectBox turns the delete protection of the box off and waits for
// the action.
func unprotectBox(t *testing.T, e *env, id int64) {
	t.Helper()
	var body struct {
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	path := fmt.Sprintf("/storage_boxes/%d/actions/change_protection", id)
	status, aerr, err := e.hetznerLocked(http.MethodPost, path, map[string]bool{"delete": false}, &body)
	if err != nil || status != http.StatusCreated && status != http.StatusOK {
		t.Errorf("cleanup: POST %s: status %d %s %v", path, status, aerr.Code, err)
		return
	}
	if err := e.waitAction(body.Action.ID, boxActionTimeout); err != nil {
		t.Errorf("cleanup: change_protection of box %d: %v", id, err)
	}
}
