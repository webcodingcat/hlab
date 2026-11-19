//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	rescueImage  = "debian-13"
	srv11Marker  = "/root/hlab-srv11-marker"
	srv11Mount   = "/mnt/hlab-srv11"
	rootDiskProg = `lsblk -no PKNAME "$(findmnt -n -o SOURCE /)"`
)

// testSRV11: rescue boots a system that does not run from /dev/sda, takes
// the key and the returned password, and sees the server disk as /dev/sda;
// after disable_rescue and reboot the installed system runs again.
func testSRV11(t *testing.T) {
	e := sharedEnv(t)
	takeServerSlots(t, 1)
	srv := ownServerImage(t, e, "srv11", rescueImage)
	content := harness.runID + "-srv11"
	if r, err := e.ssh(srv, time.Minute, "", "echo "+content+" > "+srv11Marker+" && sync"); err != nil {
		t.Fatalf("write marker: %v\n%s", err, r)
	}
	before, err := e.bootID(srv)
	if err != nil {
		t.Fatal(err)
	}

	var body struct {
		RootPassword string `json:"root_password"`
		Action       struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	// The first enable prepares the rescue base disk.
	e.post(t, fmt.Sprintf("/servers/%d/actions/enable_rescue", srv.ID),
		map[string]any{"type": "linux64", "ssh_keys": []int64{e.sshKeyID(t)}}, &body)
	if body.RootPassword == "" {
		t.Fatal("enable_rescue returned no root_password")
	}
	if err := e.waitAction(body.Action.ID, 45*time.Minute); err != nil {
		t.Fatalf("enable_rescue: %v", err)
	}
	e.hcloudAction(t, 5*time.Minute, "server", "reset", srv.Name)
	rescueBoot := e.waitNewBoot(t, srv, before, 10*time.Minute)

	r, err := e.ssh(srv, time.Minute, "", rootDiskProg)
	if err != nil {
		t.Fatalf("root device in rescue: %v\n%s", err, r)
	}
	if strings.TrimSpace(r.Stdout) == "sda" {
		t.Errorf("/ of the rescue system is on /dev/sda, want another disk")
	}
	var last result
	if err := waitFor(5*time.Minute, 5*time.Second, func() (bool, error) {
		r, err := sshPassword(srv, body.RootPassword, 30*time.Second, "id -un")
		last = r
		return err == nil && strings.TrimSpace(r.Stdout) == "root", nil
	}); err != nil {
		t.Errorf("ssh root@%s into rescue with the returned password: %v\n%s", srv.IPv4, err, last)
	}
	findMarker := "mkdir -p " + srv11Mount + "; for p in $(lsblk -lnpo NAME,TYPE /dev/sda | awk '$2==\"part\"{print $1}'); do " +
		"if mount -o ro,noload \"$p\" " + srv11Mount + " 2>/dev/null || mount -o ro \"$p\" " + srv11Mount + " 2>/dev/null; then " +
		"cat " + srv11Mount + srv11Marker + " 2>/dev/null; umount " + srv11Mount + "; fi; done"
	r, err = e.ssh(srv, 2*time.Minute, "", findMarker)
	if err != nil || strings.TrimSpace(r.Stdout) != content {
		t.Errorf("marker on /dev/sda in rescue: %v\n%s\nwant %q", err, r, content)
	}

	e.runAPIAction(t, fmt.Sprintf("/servers/%d/actions/disable_rescue", srv.ID), nil, 5*time.Minute)
	e.hcloudAction(t, 5*time.Minute, "server", "reboot", srv.Name)
	e.waitNewBoot(t, srv, rescueBoot, 10*time.Minute)
	r, err = e.ssh(srv, time.Minute, "", "cat "+srv11Marker)
	if err != nil || strings.TrimSpace(r.Stdout) != content {
		t.Errorf("marker in / after disable_rescue and reboot: %v\n%s\nwant %q", err, r, content)
	}
	r, err = e.ssh(srv, time.Minute, "", rootDiskProg)
	if err != nil || strings.TrimSpace(r.Stdout) != "sda" {
		t.Errorf("/ after disable_rescue and reboot: %v\n%s\nwant on sda", err, r)
	}
}
