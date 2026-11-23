//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	// snapshotStatsTimeout covers the stats refresh of the boxes, which runs
	// after each box action and every five minutes.
	snapshotStatsTimeout = 7 * time.Minute
	// sessionEndTimeout is the wait of the WSL2 session script until its
	// sftp session ends; the whole script stays within transferTimeout.
	sessionEndTimeout = 140 * time.Second
)

// boxSnapshot is a storage box snapshot as the API describes it.
type boxSnapshot struct {
	ID          int64             `json:"id"`
	StorageBox  int64             `json:"storage_box"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	IsAutomatic bool              `json:"is_automatic"`
	Stats       struct {
		Size           int64 `json:"size"`
		SizeFilesystem int64 `json:"size_filesystem"`
	} `json:"stats"`
}

// snapshotsPath is the API path of the snapshots of box.
func snapshotsPath(box *storageBox) string { return boxPath(box) + "/snapshots" }

// postSnapshot asks for a manual snapshot of box and returns the answer's
// status, error, snapshot ID and action ID.
func (e *env) postSnapshot(box *storageBox, description string, labels map[string]string) (int, apiError, int64, int64, error) {
	var created struct {
		Snapshot struct {
			ID int64 `json:"id"`
		} `json:"snapshot"`
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	body := map[string]any{"description": description}
	if labels != nil {
		body["labels"] = labels
	}
	status, aerr, err := e.hetznerDo(context.Background(), http.MethodPost, snapshotsPath(box), body, &created)
	return status, aerr, created.Snapshot.ID, created.Action.ID, err
}

// createSnapshot creates a manual snapshot of box, waits for its
// create_snapshot action and returns the snapshot as the API shows it.
func (e *env) createSnapshot(t *testing.T, box *storageBox, description string, labels map[string]string) boxSnapshot {
	t.Helper()
	status, aerr, id, action, err := e.postSnapshot(box, description, labels)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("POST %s: status %d %s %s %v", snapshotsPath(box), status, aerr.Code, aerr.Message, err)
	}
	if err := e.waitAction(action, boxActionTimeout); err != nil {
		t.Fatalf("create_snapshot %q of %s: %v", description, box.Username, err)
	}
	s, status, err := e.getSnapshot(box, id)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET %s/%d: status %d %v", snapshotsPath(box), id, status, err)
	}
	return s
}

// getSnapshot reads one snapshot of box.
func (e *env) getSnapshot(box *storageBox, id int64) (boxSnapshot, int, error) {
	var body struct {
		Snapshot boxSnapshot `json:"snapshot"`
	}
	status, _, err := e.hetznerDo(context.Background(), http.MethodGet, fmt.Sprintf("%s/%d", snapshotsPath(box), id), nil, &body)
	return body.Snapshot, status, err
}

// listSnapshots lists the snapshots of box.
func (e *env) listSnapshots(t *testing.T, box *storageBox) []boxSnapshot {
	t.Helper()
	var body struct {
		Snapshots []boxSnapshot `json:"snapshots"`
	}
	status, aerr, err := e.hetznerDo(context.Background(), http.MethodGet, snapshotsPath(box), nil, &body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET %s: status %d %s %v", snapshotsPath(box), status, aerr.Code, err)
	}
	return body.Snapshots
}

// snapshotNames are the names of snaps.
func snapshotNames(snaps []boxSnapshot) []string {
	names := make([]string, 0, len(snaps))
	for _, s := range snaps {
		names = append(names, s.Name)
	}
	return names
}

// sizeSnapshots reads stats.size_snapshots of box.
func (e *env) sizeSnapshots(box *storageBox) (int64, error) {
	var body struct {
		StorageBox struct {
			Stats struct {
				SizeSnapshots int64 `json:"size_snapshots"`
			} `json:"stats"`
		} `json:"storage_box"`
	}
	status, aerr, err := e.hetznerDo(context.Background(), http.MethodGet, boxPath(box), nil, &body)
	if err == nil && status != http.StatusOK {
		err = fmt.Errorf("GET %s: status %d %s", boxPath(box), status, aerr.Code)
	}
	return body.StorageBox.Stats.SizeSnapshots, err
}

// rollbackSnapshot rolls box back to the snapshot named name and waits for
// the rollback_snapshot action.
func (e *env) rollbackSnapshot(t *testing.T, box *storageBox, name string) {
	t.Helper()
	var body struct {
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	path := boxPath(box) + "/actions/rollback_snapshot"
	status, aerr, err := e.hetznerDo(context.Background(), http.MethodPost, path, map[string]any{"snapshot": name}, &body)
	if err != nil || status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST %s: status %d %s %s %v", path, status, aerr.Code, aerr.Message, err)
	}
	if err := e.waitAction(body.Action.ID, boxActionTimeout); err != nil {
		t.Fatalf("rollback_snapshot of %s to %s: %v", box.Username, name, err)
	}
}

// testSBOX05: on a box of an own type with snapshot_limit 2, snapshots are
// created, listed, updated and deleted through the API, a third manual
// snapshot is refused, and stats.size_snapshots of the box grows after data
// changed behind a snapshot.
func testSBOX05(t *testing.T) {
	const ownType = "e2e-snap2"
	e := sharedEnv(t)
	addOwnStorageBoxTypes(t, e, map[string]int64{ownType: 256 << 20}, 2)
	box := newTestBox(t, e, "sbox05", ownType, map[string]bool{"reachable_externally": true})
	l := passwordLogin(box, 22)
	dir := t.TempDir()

	data := randomFile(t, dir, "data.bin", 16<<20)
	if !eventually(t, "sftp put data.bin into "+box.Username, func() (result, error) {
		return hostSFTP(l, fmt.Sprintf("put %s data.bin\n", sftpPath(data)))
	}) {
		t.FailNow()
	}

	first := e.createSnapshot(t, box, "sbox05 first", map[string]string{"row": "sbox05"})
	second := e.createSnapshot(t, box, "sbox05 second", nil)
	for _, s := range []boxSnapshot{first, second} {
		if s.StorageBox != box.ID || s.Name == "" || s.IsAutomatic {
			t.Errorf("snapshot %d: storage_box %d, name %q, is_automatic %v; want box %d, a name, manual",
				s.ID, s.StorageBox, s.Name, s.IsAutomatic, box.ID)
		}
	}
	if first.Name == second.Name {
		t.Errorf("both snapshots are named %s", first.Name)
	}

	status, aerr, _, _, err := e.postSnapshot(box, "sbox05 third", nil)
	if err != nil {
		t.Fatalf("POST %s: %v", snapshotsPath(box), err)
	}
	if status != http.StatusForbidden || aerr.Code != "resource_limit_exceeded" {
		t.Errorf("third manual snapshot with snapshot_limit 2: status %d %s, want 403 resource_limit_exceeded", status, aerr.Code)
	}

	listed := e.listSnapshots(t, box)
	if names := snapshotNames(listed); len(names) != 2 || !slices.Contains(names, first.Name) || !slices.Contains(names, second.Name) {
		t.Errorf("snapshots listed %v, want %s and %s", names, first.Name, second.Name)
	}

	update := map[string]any{"description": "sbox05 updated", "labels": map[string]string{"state": "updated"}}
	var updated struct {
		Snapshot boxSnapshot `json:"snapshot"`
	}
	path := fmt.Sprintf("%s/%d", snapshotsPath(box), first.ID)
	if status, aerr, err := e.hetznerDo(context.Background(), http.MethodPut, path, update, &updated); err != nil || status != http.StatusOK {
		t.Fatalf("PUT %s: status %d %s %s %v", path, status, aerr.Code, aerr.Message, err)
	}
	got, status, err := e.getSnapshot(box, first.ID)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET %s: status %d %v", path, status, err)
	}
	for _, s := range []boxSnapshot{updated.Snapshot, got} {
		if s.Description != "sbox05 updated" || len(s.Labels) != 1 || s.Labels["state"] != "updated" {
			t.Errorf("after the update: description %q, labels %v; want %q and only state=updated", s.Description, s.Labels, "sbox05 updated")
		}
	}

	before, err := e.sizeSnapshots(box)
	if err != nil {
		t.Fatal(err)
	}
	// data.bin rewritten: its old blocks are held by the snapshots alone,
	// and by the first alone once the second is gone.
	changed := randomFile(t, dir, "changed.bin", 16<<20)
	if r, err := hostSFTP(l, fmt.Sprintf("put %s data.bin\n", sftpPath(changed))); err != nil {
		t.Fatalf("sftp overwrite data.bin: %v\n%s", err, r)
	}

	var deleted struct {
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	path = fmt.Sprintf("%s/%d", snapshotsPath(box), second.ID)
	status, aerr, err = e.hetznerDo(context.Background(), http.MethodDelete, path, nil, &deleted)
	if err != nil || status < 200 || status > 299 {
		t.Fatalf("DELETE %s: status %d %s %s %v", path, status, aerr.Code, aerr.Message, err)
	}
	if err := e.waitAction(deleted.Action.ID, boxActionTimeout); err != nil {
		t.Fatalf("delete_snapshot %s: %v", second.Name, err)
	}
	if _, status, err := e.getSnapshot(box, second.ID); err != nil || status != http.StatusNotFound {
		t.Errorf("GET %s after the delete: status %d %v, want 404", path, status, err)
	}
	if names := snapshotNames(e.listSnapshots(t, box)); len(names) != 1 || names[0] != first.Name {
		t.Errorf("snapshots listed after the delete %v, want only %s", names, first.Name)
	}

	var after int64
	err = waitFor(snapshotStatsTimeout, 5*time.Second, func() (bool, error) {
		after, err = e.sizeSnapshots(box)
		return err == nil && after >= before+8<<20, nil
	})
	if err != nil {
		t.Errorf("stats.size_snapshots %d after 16 MiB changed behind %s, want at least %d", after, first.Name, before+8<<20)
	}
}

// testSBOX19: with zfs_enabled the box account sees its snapshots as
// .zfs/snapshot/<name> over SFTP with the data of their time and cannot
// write there; with zfs_enabled off .zfs/snapshot is gone.
func testSBOX19(t *testing.T) {
	e := sharedEnv(t)
	box := newTestBox(t, e, "sbox19", "bx11", map[string]bool{"reachable_externally": true, "zfs_enabled": true})
	l := passwordLogin(box, 22)
	dir := t.TempDir()

	old := randomFile(t, dir, "old.bin", 1<<20)
	if !eventually(t, "sftp put data.bin into "+box.Username, func() (result, error) {
		return hostSFTP(l, fmt.Sprintf("put %s data.bin\n", sftpPath(old)))
	}) {
		t.FailNow()
	}
	snap := e.createSnapshot(t, box, "sbox19", nil)
	newer := randomFile(t, dir, "new.bin", 1<<20)
	if r, err := hostSFTP(l, fmt.Sprintf("put %s data.bin\n", sftpPath(newer))); err != nil {
		t.Fatalf("sftp overwrite data.bin: %v\n%s", err, r)
	}

	in := ".zfs/snapshot/" + snap.Name
	if !eventually(t, "sftp ls "+in, func() (result, error) { return hostSFTP(l, "ls "+in+"\n") }) {
		t.FailNow()
	}
	back := filepath.Join(dir, "back.bin")
	if r, err := hostSFTP(l, fmt.Sprintf("get %s/data.bin %s\n", in, sftpPath(back))); err != nil {
		t.Errorf("sftp get %s/data.bin: %v\n%s", in, err, r)
	} else {
		sameFile(t, old, back, in+"/data.bin")
	}

	for _, cmd := range []string{
		fmt.Sprintf("put %s %s/ro-new.bin\n", sftpPath(newer), in),
		fmt.Sprintf("put %s .zfs/snapshot/ro-new.bin\n", sftpPath(newer)),
		"rm " + in + "/data.bin\n",
		"mkdir " + in + "/ro-dir\n",
	} {
		if r, err := hostSFTP(l, cmd); err == nil {
			t.Errorf("%s succeeded in the read-only snapshot folder\n%s", strings.TrimSpace(cmd), r)
		}
	}

	e.updateAccess(t, boxPath(box), map[string]bool{"reachable_externally": true, "samba_enabled": false,
		"ssh_enabled": false, "webdav_enabled": false, "zfs_enabled": false})
	refusedWithin(t, ".zfs/snapshot of "+box.Username+" with zfs_enabled off", func() (result, error) {
		return hostSFTP(l, "ls .zfs/snapshot\n")
	})
}

// testSBOX18: after writing A, snapshot S1, writing B, snapshot S2,
// rollback to S1 shows A without B over SFTP and S2 is deleted; a
// subaccount's session opened before is ended and its next login sees the
// old state.
func testSBOX18(t *testing.T) {
	e := sharedEnv(t)
	_, w := sharedWSL(t)
	wdir := wslWorkDir(t, w)
	box := newTestBox(t, e, "sbox18", "bx11", map[string]bool{"reachable_externally": true})
	sub := e.createSubaccount(t, box, "sbox18-sub", map[string]bool{"reachable_externally": true})
	owner, subLogin := passwordLogin(box, 22), passwordLogin(sub, 22)
	dir := t.TempDir()

	a := randomFile(t, dir, "a.bin", 1<<20)
	if !eventually(t, "sftp put a.bin into "+box.Username, func() (result, error) {
		return hostSFTP(owner, fmt.Sprintf("put %s a.bin\n", sftpPath(a)))
	}) {
		t.FailNow()
	}
	if !eventually(t, "subaccount sftp put sub-a.bin", func() (result, error) {
		return hostSFTP(subLogin, fmt.Sprintf("put %s sub-a.bin\n", sftpPath(a)))
	}) {
		t.FailNow()
	}
	s1 := e.createSnapshot(t, box, "sbox18 S1", nil)
	b := randomFile(t, dir, "b.bin", 1<<20)
	if r, err := hostSFTP(owner, fmt.Sprintf("put %s b.bin\n", sftpPath(b))); err != nil {
		t.Fatalf("sftp put b.bin: %v\n%s", err, r)
	}
	if r, err := hostSFTP(subLogin, fmt.Sprintf("put %s sub-b.bin\n", sftpPath(b))); err != nil {
		t.Fatalf("subaccount sftp put sub-b.bin: %v\n%s", err, r)
	}
	s2 := e.createSnapshot(t, box, "sbox18 S2", nil)

	// The subaccount's session stays open on a fifo until the server ends
	// it; the script reports whether it ended within sessionEndTimeout.
	session := "rm -f ready cmds\nmkfifo cmds\n" +
		"sftp $SSHOPTS -P 22 -b - \"$LOGIN\" < cmds > session.out 2>&1 &\npid=$!\n" +
		"exec 3> cmds\nprintf 'ls -1\\n' >&3\n" +
		"for i in $(seq 30); do grep -q sub-a.bin session.out && break; kill -0 $pid 2>/dev/null || break; sleep 1; done\n" +
		"grep -q sub-a.bin session.out || { echo SESSION-NOT-OPEN; cat session.out; kill $pid 2>/dev/null || true; exit 1; }\n" +
		"touch ready\n" +
		fmt.Sprintf("if timeout %d tail --pid=$pid -f /dev/null; then echo SESSION-ENDED; else echo SESSION-ALIVE; kill $pid; exit 1; fi\n",
			int(sessionEndTimeout/time.Second))
	type outcome struct {
		r   result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := w.wslPassword(wdir, sub, session)
		done <- outcome{r, err}
	}()
	var early *outcome
	err := waitFor(time.Minute, 2*time.Second, func() (bool, error) {
		select {
		case o := <-done:
			early = &o
			return false, fmt.Errorf("the session script ended before the session was open")
		default:
		}
		_, err := w.run(context.Background(), 30*time.Second, nil, "test -f '"+wdir+"/ready'\n")
		return err == nil, nil
	})
	if err != nil {
		if early != nil {
			t.Fatalf("subaccount session from WSL2: %v: %v\n%s", err, early.err, early.r)
		}
		t.Fatalf("subaccount session from WSL2: %v", err)
	}

	e.rollbackSnapshot(t, box, s1.Name)

	o := <-done
	if o.err != nil || !strings.Contains(o.r.Stdout, "SESSION-ENDED") {
		t.Errorf("the subaccount's session opened before the rollback was not ended: %v\n%s", o.err, o.r)
	}

	var listing string
	if eventually(t, "box lists its data after the rollback", func() (result, error) {
		r, err := hostSFTP(owner, "ls -1\n")
		listing = sftpListing(r.Stdout)
		return r, err
	}) {
		names := strings.Fields(listing)
		if !slices.Contains(names, "a.bin") || slices.Contains(names, "b.bin") {
			t.Errorf("box after the rollback to %s lists %v, want a.bin without b.bin", s1.Name, names)
		}
		back := filepath.Join(dir, "a-back.bin")
		if r, err := hostSFTP(owner, fmt.Sprintf("get a.bin %s\n", sftpPath(back))); err != nil {
			t.Errorf("sftp get a.bin after the rollback: %v\n%s", err, r)
		} else {
			sameFile(t, a, back, "a.bin after the rollback")
		}
	}

	if eventually(t, "subaccount logs in again after the rollback", func() (result, error) {
		r, err := hostSFTP(subLogin, "ls -1\n")
		listing = sftpListing(r.Stdout)
		return r, err
	}) {
		names := strings.Fields(listing)
		if !slices.Contains(names, "sub-a.bin") || slices.Contains(names, "sub-b.bin") {
			t.Errorf("subaccount after the rollback to %s lists %v, want sub-a.bin without sub-b.bin", s1.Name, names)
		}
	}

	if names := snapshotNames(e.listSnapshots(t, box)); len(names) != 1 || names[0] != s1.Name {
		t.Errorf("snapshots after the rollback %v, want only %s", names, s1.Name)
	}
	if _, status, err := e.getSnapshot(box, s2.ID); err != nil || status != http.StatusNotFound {
		t.Errorf("GET snapshot %s after the rollback: status %d %v, want 404", s2.Name, status, err)
	}
}
