//go:build live && windows

package itest

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestVolumesAttachMountGrowAndDetach runs the lab with a running server
// and two volumes, as on Hetzner (measured 2026-11-09): the first,
// created for the server with ext4 and automount, appears in the guest as
// /dev/disk/by-id/scsi-0HC_Volume_<id>, mounted at /mnt/HC_Volume_<id>
// with its line in /etc/fstab; the second, without a file system and
// attached without automount, only gets its name. The first grows while
// it is mounted, and resize2fs takes the new size once the guest has
// read the disk (Hyper-V tells the guest at its next access). Both
// detach; the fstab line stays.
func TestVolumesAttachMountGrowAndDetach(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	here := filepath.Dir(executable)
	release := filepath.Join(here, "hlab-appliance.vhdx")
	if _, err := os.Stat(release); err != nil {
		t.Skipf("no appliance to boot: %v (bin/dev appliance)", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	parts := newParts(t.TempDir())
	t.Cleanup(func() { _ = parts.Close() })
	keys := newLabKey(t)
	run := labRun{
		dataDir: t.TempDir(),
		release: release,
		labKey:  keys.public,
		parts:   parts,
		caches:  []*imageCache{{dir: filepath.Join(here, "image-cache"), name: "debian-13"}},
	}
	l := run.open(ctx, t)
	t.Cleanup(func() {
		run.close(t, l)
		run.removeVMs(t)
	})
	api := newCloudAPI(t, l, "itest")
	server := createServer(ctx, t, api, "itest-volumes", "debian-13")
	for _, cache := range run.caches {
		cache.keep(t, l)
	}

	started := time.Now()
	mounted := createVolume(ctx, t, api, map[string]any{"name": "itest-data", "size": 10, "format": "ext4", "automount": true,
		"assignee": map[string]any{"type": "server", "id": server.id}})
	t.Logf("the volume %d was created, formatted and attached in %s", mounted, time.Since(started).Round(100*time.Millisecond))
	link := fmt.Sprintf("/dev/disk/by-id/scsi-0HC_Volume_%d", mounted)
	dir := fmt.Sprintf("/mnt/HC_Volume_%d", mounted)
	waitInGuest(t, keys, server.ipv4, "findmnt -n -o FSTYPE "+dir, "ext4")
	t.Logf("mounted %s after %s", dir, time.Since(started).Round(100*time.Millisecond))
	if fstab := keys.run(t, server.ipv4, "", "grep HC_Volume /etc/fstab"); fstab != link+" "+dir+" ext4 discard,nofail,defaults 0 0" {
		t.Errorf("the fstab line is %q", fstab)
	}
	keys.run(t, server.ipv4, "", "echo kept > "+dir+"/file && sync")

	plain := createVolume(ctx, t, api, map[string]any{"name": "itest-plain", "size": 10, "location": "fsn1"})
	attach := volumeAction(t, api, plain, "attach", map[string]any{"server": server.id})
	waitForAction(ctx, t, api, attach)
	plainLink := fmt.Sprintf("/dev/disk/by-id/scsi-0HC_Volume_%d", plain)
	waitInGuest(t, keys, server.ipv4, "test -b "+plainLink+" && udevadm info -q property "+plainLink+" | grep -E '^ID_(VENDOR|MODEL|SERIAL_SHORT)=' | sort | tr '\\n' ' '",
		fmt.Sprintf("ID_MODEL=Volume ID_SERIAL_SHORT=%d ID_VENDOR=HC", plain))
	if mounts := keys.run(t, server.ipv4, "", "findmnt -n -o TARGET -S "+plainLink+" || true"); mounts != "" {
		t.Errorf("the volume without automount is mounted at %s", mounts)
	}

	resize := volumeAction(t, api, mounted, "resize", map[string]any{"size": 15})
	waitForAction(ctx, t, api, resize)
	t.Logf("the guest sees %s bytes right after the resize", keys.run(t, server.ipv4, "", "blockdev --getsize64 "+link))
	waitInGuest(t, keys, server.ipv4, "dd if="+link+" of=/dev/null bs=4096 count=1 iflag=direct 2>/dev/null; blockdev --getsize64 "+link,
		fmt.Sprint(15<<30))
	keys.run(t, server.ipv4, "", "resize2fs "+link)
	size, err := strconv.Atoi(keys.run(t, server.ipv4, "", "df -B1G --output=size "+dir+" | tail -n 1 | tr -d ' '"))
	if err != nil || size < 14 {
		t.Errorf("after resize2fs the file system has %d GB (%v)", size, err)
	}

	keys.run(t, server.ipv4, "", "umount "+dir)
	for _, id := range []int64{mounted, plain} {
		waitForAction(ctx, t, api, volumeAction(t, api, id, "detach", nil))
		waitInGuest(t, keys, server.ipv4, fmt.Sprintf("test -e /dev/disk/by-id/scsi-0HC_Volume_%d && echo there || echo gone", id), "gone")
	}
	if fstab := keys.run(t, server.ipv4, "", "grep -c HC_Volume /etc/fstab"); fstab != "1" {
		t.Errorf("after the detach /etc/fstab has %s lines of volumes, want the one that stays", fstab)
	}

	// the data is there at the next attach
	waitForAction(ctx, t, api, volumeAction(t, api, mounted, "attach", map[string]any{"server": server.id, "automount": true}))
	waitInGuest(t, keys, server.ipv4, "cat "+dir+"/file", "kept")
	keys.run(t, server.ipv4, "", "umount "+dir)
	waitForAction(ctx, t, api, volumeAction(t, api, mounted, "detach", nil))

	var deleted struct {
		Action struct{ ID int64 } `json:"action"`
	}
	api.do(t, http.MethodDelete, fmt.Sprintf("/servers/%d", server.id), nil, http.StatusOK, &deleted)
	waitForAction(ctx, t, api, deleted.Action.ID)
	for _, id := range []int64{mounted, plain} {
		api.do(t, http.MethodDelete, fmt.Sprintf("/volumes/%d", id), nil, http.StatusNoContent, nil)
	}
	run.close(t, l)
	for _, cache := range run.caches {
		cache.store(t)
	}
	checkHostIsClean(t)
}

// createVolume creates a volume and waits for its create and its attach.
func createVolume(ctx context.Context, t *testing.T, api cloudAPI, body map[string]any) int64 {
	t.Helper()
	var created struct {
		Volume      struct{ ID int64 } `json:"volume"`
		Action      struct{ ID int64 } `json:"action"`
		NextActions []struct{ ID int64 } `json:"next_actions"`
	}
	api.do(t, http.MethodPost, "/volumes", body, http.StatusCreated, &created)
	waitForAction(ctx, t, api, created.Action.ID)
	for _, next := range created.NextActions {
		waitForAction(ctx, t, api, next.ID)
	}
	return created.Volume.ID
}

// volumeAction runs a volume's action and returns its ID.
func volumeAction(t *testing.T, api cloudAPI, id int64, command string, body any) int64 {
	t.Helper()
	var answer struct {
		Action struct{ ID int64 } `json:"action"`
	}
	api.do(t, http.MethodPost, fmt.Sprintf("/volumes/%d/actions/%s", id, command), body, http.StatusCreated, &answer)
	return answer.Action.ID
}

// waitInGuest runs a command in the guest until its output is want, for a
// minute at most.
func waitInGuest(t *testing.T, keys labKey, address, command, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		got := keys.run(t, address, "", command)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q gives %q in the guest, want %q", command, got, want)
		}
		time.Sleep(time.Second)
	}
}
