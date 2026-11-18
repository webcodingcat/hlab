//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// volumeDevice is the guest path of a volume.
func volumeDevice(id int64) string {
	return fmt.Sprintf("/dev/disk/by-id/scsi-0HC_Volume_%d", id)
}

// testVOL02: an attached volume appears under its by-id path and keeps its
// data across detach and attach, also on a second server.
func testVOL02(t *testing.T) {
	e := sharedEnv(t)
	srv := ownServer(t, e, "vol02")
	vol := e.createVolume(t, map[string]any{"name": harness.runID + "-vol02", "size": 10, "location": serverLocation})
	dev := volumeDevice(vol)
	data := harness.runID + "-vol02"

	e.attachVolume(t, vol, srv, false)
	e.waitDevice(t, srv, dev, true)
	if r, err := e.ssh(srv, time.Minute, "", "printf '%s\\n' "+data+" | dd of="+dev+" conv=fsync status=none"); err != nil {
		t.Fatalf("write to %s: %v\n%s", dev, err, r)
	}
	readBack := func(s *server, when string) {
		t.Helper()
		r, err := e.ssh(s, time.Minute, "", "head -n 1 "+dev)
		if err != nil || strings.TrimSpace(r.Stdout) != data {
			t.Errorf("data on %s %s: %v\n%s\nwant %q", dev, when, err, r, data)
		}
	}

	e.detachVolume(t, vol)
	e.waitDevice(t, srv, dev, false)
	e.attachVolume(t, vol, srv, false)
	e.waitDevice(t, srv, dev, true)
	readBack(srv, "after detach and attach")

	e.detachVolume(t, vol)
	other := ownServer(t, e, "vol02-b")
	e.attachVolume(t, vol, other, false)
	e.waitDevice(t, other, dev, true)
	readBack(other, "on a second server")
}

// testVOL03: volumes with format and automount are mounted at
// /mnt/HC_Volume_<id> with their filesystem, at server create and on a
// later attach.
func testVOL03(t *testing.T) {
	e := sharedEnv(t)
	cases := []struct {
		name, format string
		atCreate     bool
	}{
		{name: "ext4_at_create", format: "ext4", atCreate: true},
		{name: "xfs_on_attach", format: "xfs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			suffix := "vol03-" + c.format
			vol := e.createVolume(t, map[string]any{
				"name": harness.runID + "-" + suffix, "size": 10, "location": serverLocation, "format": c.format,
			})
			var srv *server
			if c.atCreate {
				srv = ownServerImage(t, e, suffix, serverImage, "--volume", strconv.FormatInt(vol, 10), "--automount")
			} else {
				srv = ownServer(t, e, suffix)
				e.attachVolume(t, vol, srv, true)
			}
			mount := fmt.Sprintf("/mnt/HC_Volume_%d", vol)
			var last result
			if err := waitFor(5*time.Minute, 5*time.Second, func() (bool, error) {
				r, err := e.ssh(srv, 30*time.Second, "", "findmnt -n -o FSTYPE "+mount)
				last = r
				return err == nil && strings.TrimSpace(r.Stdout) != "", nil
			}); err != nil {
				t.Fatalf("%s not mounted: %v\n%s", mount, err, last)
			}
			if got := strings.TrimSpace(last.Stdout); got != c.format {
				t.Errorf("%s mounted as %q, want %s", mount, got, c.format)
			}
		})
	}
}

// testVOL04: resizing an attached volume grows the block device the
// running guest sees, without a rescan.
func testVOL04(t *testing.T) {
	e := sharedEnv(t)
	srv := ownServer(t, e, "vol04")
	vol := e.createVolume(t, map[string]any{"name": harness.runID + "-vol04", "size": 10, "location": serverLocation})
	dev := volumeDevice(vol)
	e.attachVolume(t, vol, srv, false)
	e.waitDevice(t, srv, dev, true)

	size := func() (int64, error) {
		r, err := e.ssh(srv, 30*time.Second, "", "blockdev --getsize64 "+dev)
		if err != nil || r.ExitCode != 0 {
			return 0, fmt.Errorf("blockdev: %v\n%s", err, r)
		}
		return strconv.ParseInt(strings.TrimSpace(r.Stdout), 10, 64)
	}
	before, err := size()
	if err != nil {
		t.Fatal(err)
	}
	e.runAPIAction(t, fmt.Sprintf("/volumes/%d/actions/resize", vol), map[string]any{"size": 11}, 5*time.Minute)
	want := before / 10 * 11
	var got int64
	if err := waitFor(2*time.Minute, 5*time.Second, func() (bool, error) {
		got, _ = size()
		return got == want, nil
	}); err != nil {
		t.Errorf("block device size %d after resize to 11, want %d (10 was %d): %v", got, want, before, err)
	}
}

// createVolume creates a volume, waits until it is available and deletes
// it when the row ends (detached first if a server still holds it).
func (e *env) createVolume(t *testing.T, req map[string]any) int64 {
	t.Helper()
	var body struct {
		Volume struct {
			ID int64 `json:"id"`
		} `json:"volume"`
		Action *struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	e.post(t, "/volumes", req, &body)
	id := body.Volume.ID
	t.Cleanup(func() { e.deleteVolume(t, id) })
	if body.Action != nil {
		// Formatting runs in the appliance.
		if err := e.waitAction(body.Action.ID, 15*time.Minute); err != nil {
			t.Fatalf("create volume %v: %v", req, err)
		}
	}
	if err := waitFor(15*time.Minute, 2*time.Second, func() (bool, error) {
		v, err := e.volume(id)
		return err == nil && v.Status == "available", nil
	}); err != nil {
		t.Fatalf("volume %d not available: %v", id, err)
	}
	return id
}

type volumeInfo struct {
	Status string `json:"status"`
	Server *int64 `json:"server"`
}

func (e *env) volume(id int64) (volumeInfo, error) {
	var body struct {
		Volume volumeInfo `json:"volume"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, fmt.Sprintf("/volumes/%d", id), nil, &body)
	if err != nil {
		return volumeInfo{}, err
	}
	if status != http.StatusOK {
		return volumeInfo{}, fmt.Errorf("GET /volumes/%d: status %d %s", id, status, aerr.Code)
	}
	return body.Volume, nil
}

// deleteVolume detaches the volume if needed and deletes it, trying again
// while an action of it runs.
func (e *env) deleteVolume(t *testing.T, id int64) {
	t.Helper()
	path := fmt.Sprintf("/volumes/%d", id)
	err := waitFor(15*time.Minute, 5*time.Second, func() (bool, error) {
		v, err := e.volume(id)
		if err != nil {
			return false, nil
		}
		if v.Server != nil {
			_, _, _ = e.apiDo(context.Background(), http.MethodPost, path+"/actions/detach", nil, nil)
			return false, nil
		}
		status, _, err := e.apiDo(context.Background(), http.MethodDelete, path, nil, nil)
		if err != nil {
			return false, nil
		}
		return status == http.StatusNoContent || status == http.StatusOK || status == http.StatusNotFound, nil
	})
	if err != nil {
		t.Errorf("DELETE %s: %v", path, err)
	}
}

func (e *env) attachVolume(t *testing.T, vol int64, srv *server, automount bool) {
	t.Helper()
	e.runAPIAction(t, fmt.Sprintf("/volumes/%d/actions/attach", vol), map[string]any{
		"assignee": map[string]any{"id": srv.ID, "type": "server"}, "automount": automount,
	}, 15*time.Minute)
}

func (e *env) detachVolume(t *testing.T, vol int64) {
	t.Helper()
	e.runAPIAction(t, fmt.Sprintf("/volumes/%d/actions/detach", vol), nil, 15*time.Minute)
}

// waitDevice waits until the path exists (or is gone) in the guest.
func (e *env) waitDevice(t *testing.T, srv *server, path string, present bool) {
	t.Helper()
	test := "test -e " + path
	if !present {
		test = "! " + test
	}
	var last result
	if err := waitFor(2*time.Minute, 2*time.Second, func() (bool, error) {
		r, err := e.ssh(srv, 30*time.Second, "", test)
		last = r
		return err == nil && r.ExitCode == 0, nil
	}); err != nil {
		t.Fatalf("%s on %s present=%v: %v\n%s", path, srv.Name, present, err, last)
	}
}
