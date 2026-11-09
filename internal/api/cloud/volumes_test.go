package cloud_test

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/system"
)

// volumeAnswer is what the tests read of a volume.
type volumeAnswer struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Server   *int64 `json:"server"`
	Assignee *struct {
		ID   *int64 `json:"id"`
		Type string `json:"type"`
	} `json:"assignee"`
	Location struct {
		Name string `json:"name"`
	} `json:"location"`
	Size        int     `json:"size"`
	LinuxDevice string  `json:"linux_device"`
	Status      string  `json:"status"`
	Format      *string `json:"format"`
}

// volumeCreated is the answer of POST /volumes.
type volumeCreated struct {
	Volume      volumeAnswer    `json:"volume"`
	Action      schema.Action   `json:"action"`
	NextActions []schema.Action `json:"next_actions"`
}

// createVolume creates a volume and fails the test if it cannot.
func createVolume(t *testing.T, test *testLab, body map[string]any) volumeCreated {
	t.Helper()
	response := test.client.Do(http.MethodPost, "/v1/volumes", body)
	if response.Status != http.StatusCreated {
		t.Fatalf("POST /v1/volumes answered %d %s", response.Status, response.Body)
	}
	var created volumeCreated
	response.Decode(t, &created)
	return created
}

// getVolume returns a volume as the API shows it.
func getVolume(t *testing.T, test *testLab, id int64) volumeAnswer {
	t.Helper()
	var answer struct {
		Volume volumeAnswer `json:"volume"`
	}
	test.client.Get(fmt.Sprintf("/v1/volumes/%d", id)).Decode(t, &answer)
	return answer.Volume
}

// volumeAction runs a volume's action and returns the answer.
func volumeAction(test *testLab, id int64, command string, body any) *responseOf {
	return &responseOf{test.client.Do(http.MethodPost, fmt.Sprintf("/v1/volumes/%d/actions/%s", id, command), body)}
}

// The life of a volume without a server, as Hetzner answers (measured
// 2026-11-09): creating, then available, formatted by the appliance;
// a resize, the protection, the delete without an action.
func TestVolumeLifeCycle(t *testing.T) {
	test := newTestLab(t)
	agent := quietMachine(t, test)
	created := createVolume(t, test, map[string]any{"name": "data", "size": 10, "location": "fsn1", "format": "ext4", "labels": map[string]string{"env": "dev"}})
	volume := created.Volume
	if volume.Status != "creating" || volume.Server != nil || volume.Assignee != nil || *volume.Format != "ext4" ||
		volume.LinuxDevice != fmt.Sprintf("/dev/disk/by-id/scsi-0HC_Volume_%d", volume.ID) || len(created.NextActions) != 0 {
		t.Errorf("the created volume: %+v, next actions %+v", volume, created.NextActions)
	}
	if created.Action.Command != "create_volume" || len(created.Action.Resources) != 1 || created.Action.Resources[0].Type != "volume" {
		t.Errorf("the create's action: %+v", created.Action)
	}
	waitActions(t, test, created.Action)
	if got := getVolume(t, test, volume.ID); got.Status != "available" {
		t.Errorf("after the create the volume is %s", got.Status)
	}
	if formats := agent.Formats(); len(formats) != 1 || formats[0].FileSystem != "ext4" ||
		formats[0].WWN != fmt.Sprintf("0x60022480484c41420000%012d", volume.ID) {
		t.Errorf("the appliance's formats: %+v", formats)
	}
	if calls := test.machine.Calls(); !containsPrefix(calls, "DetachDisk hlab-appliance-a 63") {
		t.Errorf("the volume stayed in the appliance: %v", calls)
	}

	var list struct {
		Volumes []volumeAnswer `json:"volumes"`
	}
	test.client.Get("/v1/volumes?label_selector=env%3Ddev&status=available").Decode(t, &list)
	if len(list.Volumes) != 1 || list.Volumes[0].ID != volume.ID {
		t.Errorf("the list: %+v", list.Volumes)
	}
	expectError(t, "an unknown status", &responseOf{test.client.Get("/v1/volumes?status=bogus")},
		http.StatusUnprocessableEntity, "invalid_input", "invalid status value")
	if response := test.client.Do(http.MethodPut, fmt.Sprintf("/v1/volumes/%d", volume.ID), map[string]any{"name": "renamed", "size": 50}); response.Status != http.StatusOK {
		t.Errorf("the PUT answered %d %s", response.Status, response.Body)
	}
	if got := getVolume(t, test, volume.ID); got.Name != "renamed" || got.Size != 10 {
		t.Errorf("after the PUT: %+v; Hetzner ignores the size there", got)
	}

	resize := actionOf(t, "resize", volumeAction(test, volume.ID, "resize", map[string]any{"size": 20}))
	if len(resize.Resources) != 1 {
		t.Errorf("the resize of a detached volume names %+v", resize.Resources)
	}
	waitActions(t, test, resize)
	if got := getVolume(t, test, volume.ID); got.Size != 20 {
		t.Errorf("after the resize the size is %d", got.Size)
	}

	waitActions(t, test, actionOf(t, "protect", volumeAction(test, volume.ID, "change_protection", map[string]any{"delete": true})))
	path := fmt.Sprintf("/v1/volumes/%d", volume.ID)
	expectError(t, "a protected delete", &responseOf{test.client.Do(http.MethodDelete, path, nil)}, http.StatusLocked, "protected", "volume deletion is protected")
	// an empty body turns the protection off (measured 2026-11-09)
	waitActions(t, test, actionOf(t, "unprotect", volumeAction(test, volume.ID, "change_protection", map[string]any{})))
	if response := test.client.Do(http.MethodDelete, path, nil); response.Status != http.StatusNoContent {
		t.Fatalf("the delete answered %d %s", response.Status, response.Body)
	}
	expectError(t, "a second delete", &responseOf{test.client.Do(http.MethodDelete, path, nil)}, http.StatusNotFound, "not_found", "volume does not exist")
	expectError(t, "a GET after the delete", &responseOf{test.client.Get(path)}, http.StatusNotFound, "not_found", "volume not found")
	if calls := test.machine.Calls(); !slices.ContainsFunc(calls, func(call string) bool {
		return strings.HasPrefix(call, "DeleteDisk ") && strings.HasSuffix(call, fmt.Sprintf("%d.vhdx", volume.ID))
	}) {
		t.Errorf("the disk stayed: %v", calls)
	}
}

// The errors of a create, as Hetzner answers them (measured 2026-11-09).
func TestVolumeCreateErrors(t *testing.T) {
	test := newTestLab(t)
	quietMachine(t, test)
	createVolume(t, test, map[string]any{"name": "taken", "size": 10, "location": "fsn1"})
	expectErrors(t, test, "/v1/volumes", map[string]struct {
		body any
		want networkError
	}{
		"no name":         {map[string]any{"size": 10, "location": "fsn1"}, invalidField("name", "is not allowed to be empty")},
		"a space":         {map[string]any{"name": "has space", "size": 10, "location": "fsn1"}, invalidField("name", "Invalid name.")},
		"a dash first":    {map[string]any{"name": "-a", "size": 10, "location": "fsn1"}, invalidField("name", "Invalid name.")},
		"65 characters":   {map[string]any{"name": strings.Repeat("a", 65), "size": 10, "location": "fsn1"}, invalidField("name", "Longer than maximum length 64.")},
		"too small":       {map[string]any{"name": "v", "size": 9, "location": "fsn1"}, invalidField("size", "Must be between 10 and 10240.")},
		"too large":       {map[string]any{"name": "v", "size": 10241, "location": "fsn1"}, invalidField("size", "Must be between 10 and 10240.")},
		"no place":        {map[string]any{"name": "v", "size": 10}, invalidField("location", "location is required if no server is given")},
		"a bad location":  {map[string]any{"name": "v", "size": 10, "location": "mars1"}, invalidField("location", "this location does not exist or is currently not available")},
		"a bad format":    {map[string]any{"name": "v", "size": 10, "location": "fsn1", "format": "ntfs"}, invalidField("format", "unknown value, must be empty or xfs or ext4")},
		"automount alone": {map[string]any{"name": "v", "size": 10, "location": "fsn1", "automount": true}, invalidField("format", "automount requires format to be provided")},
		"bad labels": {map[string]any{"name": "v", "size": 10, "location": "fsn1", "labels": map[string]string{"-bad": "x"}},
			invalidField("labels", "key contains invalid characters or is malformed")},
		"a size of 10.5": {map[string]any{"name": "v", "size": 10.5, "location": "fsn1"}, networkError{http.StatusUnprocessableEntity, "json_error", "can not read request body", nil}},
		"a missing server": {map[string]any{"name": "v", "size": 10, "assignee": map[string]any{"type": "server", "id": 1}},
			networkError{http.StatusInternalServerError, "server_error", "internal server error", nil}},
		"a name in use": {map[string]any{"name": "taken", "size": 10, "location": "fsn1"}, networkError{http.StatusConflict, "uniqueness_error", "name is already in use", nil}},
	})
}

// A volume attached to a server and detached again, as Hetzner answers
// (measured 2026-11-09): the create's attach in next_actions, the disk in
// the VM with the ID that names it in the guest, the server's volumes.
func TestVolumeAttachAndDetach(t *testing.T) {
	test := newTestLab(t)
	quietMachine(t, test)
	server := offServer(t, test, "web")
	other := offServer(t, test, "other")
	created := createVolume(t, test, map[string]any{"name": "data", "size": 10, "format": "xfs", "automount": true,
		"assignee": map[string]any{"type": "server", "id": server.Server.ID}})
	if len(created.NextActions) != 1 || created.NextActions[0].Command != "attach_volume" || len(created.NextActions[0].Resources) != 2 ||
		created.NextActions[0].Resources[1].ID != server.Server.ID || created.Volume.Server != nil || created.Volume.Location.Name != "fsn1" {
		t.Errorf("the create with a server: %+v, next actions %+v", created.Volume, created.NextActions)
	}
	waitActions(t, test, created.Action, created.NextActions[0])
	id := created.Volume.ID
	volume := getVolume(t, test, id)
	if volume.Server == nil || *volume.Server != server.Server.ID || volume.Assignee == nil || *volume.Assignee.ID != server.Server.ID {
		t.Errorf("the attached volume: %+v", volume)
	}
	if got := getServer(t, test, server.Server.ID).Volumes; len(got) != 1 || got[0] != id {
		t.Errorf("the server's volumes: %v", got)
	}
	vm := lab.ServerVM(server.Server.ID)
	file := test.machine.Disks(vm)[1]
	if file == "" || test.machine.DiskID(file) != system.VolumeDiskID(id, true) {
		t.Errorf("the VM's disks %v, the volume's ID %x", test.machine.Disks(vm), test.machine.DiskID(file))
	}

	// an attach to the server it has ends at once (measured 2026-11-09)
	again := actionOf(t, "attach again", volumeAction(test, id, "attach", map[string]any{"server": server.Server.ID}))
	if again.Status != "success" {
		t.Errorf("the attach to its own server is %s", again.Status)
	}
	expectErrors(t, test, fmt.Sprintf("/v1/volumes/%d/actions/attach", id), map[string]struct {
		body any
		want networkError
	}{
		"nothing":          {map[string]any{}, invalidField("assignee", "is required")},
		"a load balancer":  {map[string]any{"assignee": map[string]any{"type": "load_balancer", "id": 1}}, invalidField("assignee.type", `unsupported resource type "load_balancer"`)},
		"a missing server": {map[string]any{"server": 1}, networkError{http.StatusNotFound, "not_found", "server not found", nil}},
	})
	// attached elsewhere: 201, and the action fails (measured 2026-11-09)
	elsewhere := actionOf(t, "attach elsewhere", volumeAction(test, id, "attach", map[string]any{"server": other.Server.ID}))
	if ended := waitActions(t, test, elsewhere); ended[0].Status != "error" || ended[0].Error.Code != "volume_already_attached" ||
		ended[0].Error.Message != "volume already attached" {
		t.Errorf("the attach of a volume attached elsewhere: %+v", ended[0])
	}
	expectError(t, "deleting an attached volume", &responseOf{test.client.Do(http.MethodDelete, fmt.Sprintf("/v1/volumes/%d", id), nil)},
		http.StatusUnprocessableEntity, "service_error", fmt.Sprintf("volume with ID '%d' is still attached to a server", id))

	detach := actionOf(t, "detach", volumeAction(test, id, "detach", nil))
	if len(detach.Resources) != 2 || detach.Resources[1].ID != server.Server.ID {
		t.Errorf("the detach names %+v", detach.Resources)
	}
	waitActions(t, test, detach)
	if disks := test.machine.Disks(vm); len(disks) != 1 {
		t.Errorf("after the detach the VM has the disks %v", disks)
	}
	expectError(t, "a second detach", volumeAction(test, id, "detach", nil), http.StatusUnprocessableEntity, "service_error", "volume not attached to a server")

	// an attach without automount gives the disk an ID without the flag
	waitActions(t, test, actionOf(t, "attach to other", volumeAction(test, id, "attach", map[string]any{"server": other.Server.ID})))
	if got := test.machine.DiskID(file); got != system.VolumeDiskID(id, false) {
		t.Errorf("the disk's ID after an attach without automount: %x", got)
	}
	// a server's delete keeps the volume, detached (measured 2026-11-09)
	waitActions(t, test, serverDelete(t, test, other.Server.ID))
	if got := getVolume(t, test, id); got.Server != nil {
		t.Errorf("after its server's delete the volume has the server %d", *got.Server)
	}
}

// Attaches that Hetzner refuses (measured 2026-11-09): a server in
// another location, a 17th volume; the create's attach to a server in
// another location fails in its action.
func TestVolumeAttachLimits(t *testing.T) {
	test := newTestLab(t)
	quietMachine(t, test)
	server := offServer(t, test, "web")
	elsewhere := createVolume(t, test, map[string]any{"name": "elsewhere", "size": 10, "location": "nbg1"})
	waitActions(t, test, elsewhere.Action)
	expectError(t, "another location", volumeAction(test, elsewhere.Volume.ID, "attach", map[string]any{"server": server.Server.ID}),
		http.StatusUnprocessableEntity, "service_error", "server and volume must be in the same location")
	mismatch := createVolume(t, test, map[string]any{"name": "mismatch", "size": 10, "location": "nbg1", "server": server.Server.ID})
	ended := waitActions(t, test, mismatch.Action, mismatch.NextActions[0])
	for _, action := range ended {
		if action.Command == "attach_volume" && (action.Status != "error" || action.Error == nil || action.Error.Code != "unknown_error") {
			t.Errorf("the attach to a server in another location: %+v", action)
		}
	}
	for i := range 16 {
		created := createVolume(t, test, map[string]any{"name": fmt.Sprintf("v%d", i), "size": 10, "server": server.Server.ID})
		waitActions(t, test, created.Action, created.NextActions[0])
	}
	if disks := test.machine.Disks(lab.ServerVM(server.Server.ID)); len(disks) != 17 || disks[16] == "" {
		t.Errorf("the VM's disks: %v", disks)
	}
	expectError(t, "a 17th volume", &responseOf{test.client.Do(http.MethodPost, "/v1/volumes", map[string]any{"name": "v16", "size": 10, "server": server.Server.ID})},
		http.StatusForbidden, "resource_limit_exceeded", "number of volumes attached to a single server reached")
}

// A volume's actions wait for each other: a detach sent during a resize
// is 201 and runs after it, a second resize is 423 (measured 2026-11-09).
func TestVolumeActionsQueue(t *testing.T) {
	test := newTestLab(t)
	quietMachine(t, test)
	server := offServer(t, test, "web")
	created := createVolume(t, test, map[string]any{"name": "data", "size": 10, "server": server.Server.ID})
	waitActions(t, test, created.Action, created.NextActions[0])
	test.machine.Delay("ResizeDisk", 200*time.Millisecond)
	resize := actionOf(t, "resize", volumeAction(test, created.Volume.ID, "resize", map[string]any{"size": 20}))
	if len(resize.Resources) != 2 {
		t.Errorf("the resize of an attached volume names %+v", resize.Resources)
	}
	expectError(t, "a second resize", volumeAction(test, created.Volume.ID, "resize", map[string]any{"size": 30}),
		http.StatusLocked, "locked", "cannot perform operation because resource is locked")
	detach := actionOf(t, "detach during the resize", volumeAction(test, created.Volume.ID, "detach", nil))
	for _, action := range waitActions(t, test, resize, detach) {
		if action.Status != "success" {
			t.Errorf("%s ended %s", action.Command, action.Status)
		}
	}
	calls := test.machine.Calls()
	if resized, detached := indexOfPrefix(calls, "ResizeDisk"), indexOfPrefix(calls, "DetachDisk "+lab.ServerVM(server.Server.ID)); resized < 0 || detached < resized {
		t.Errorf("the detach did not wait for the resize: %v", calls)
	}
}

// An attach's steps take the server and the slot its plan chose: a detach
// queued behind it changes the record first, and both end well, the disk
// out of the VM again. A detach and an attach while the server's create
// runs are 201 and end well; the create's VM has what the records say
// when it is made, and what comes later follows it (measured 2026-11-09).
func TestVolumeAttachAndDetachInTheQueue(t *testing.T) {
	test := newTestLab(t)
	quietMachine(t, test)
	server := offServer(t, test, "web")
	created := createVolume(t, test, map[string]any{"name": "data", "size": 10, "location": "fsn1"})
	waitActions(t, test, created.Action)
	test.machine.Delay("AttachDisk", 200*time.Millisecond)
	attach := actionOf(t, "attach", volumeAction(test, created.Volume.ID, "attach", map[string]any{"server": server.Server.ID}))
	detach := actionOf(t, "detach behind the attach", volumeAction(test, created.Volume.ID, "detach", nil))
	for _, action := range waitActions(t, test, attach, detach) {
		if action.Status != "success" {
			t.Errorf("%s ended %s: %+v", action.Command, action.Status, action.Error)
		}
	}
	if disks := test.machine.Disks(lab.ServerVM(server.Server.ID)); len(disks) != 1 {
		t.Errorf("the VM's disks after the attach and the detach: %v", disks)
	}

	late := createVolume(t, test, map[string]any{"name": "late", "size": 10, "location": "fsn1"})
	waitActions(t, test, late.Action)
	// once the create has made the VM, the actions change it after the
	// create
	creating := createServer(t, test, map[string]any{"name": "db", "server_type": "cpx22", "image": "debian-13",
		"location": "fsn1", "start_after_create": false, "volumes": []int64{created.Volume.ID}})
	waitForCall(t, test, "CreateVM "+lab.ServerVM(creating.Server.ID))
	checkActionsDuringCreate(t, test, creating, created.Volume.ID, late.Volume.ID)
}

// Before a server's create has made the VM, an attach and a detach are
// only in the records, which the VM is made from (measured 2026-11-09:
// both 201, and in the end the server has the second volume only). The
// image's load holds the create before the VM.
func TestVolumeActionsBeforeTheServersVM(t *testing.T) {
	test := newTestLab(t)
	quietMachine(t, test)
	first := createVolume(t, test, map[string]any{"name": "first", "size": 10, "location": "fsn1"})
	late := createVolume(t, test, map[string]any{"name": "late", "size": 10, "location": "fsn1"})
	waitActions(t, test, first.Action, late.Action)
	test.machine.Delay("FlattenDisk", 300*time.Millisecond)
	creating := createServer(t, test, map[string]any{"name": "db", "server_type": "cpx22", "image": "debian-13",
		"location": "fsn1", "start_after_create": false, "volumes": []int64{first.Volume.ID}})
	checkActionsDuringCreate(t, test, creating, first.Volume.ID, late.Volume.ID)
	if containsPrefix(test.machine.Calls(), "AttachDisk "+lab.ServerVM(creating.Server.ID)) {
		t.Errorf("the attach before the VM changed the VM: %v", test.machine.Calls())
	}
}

// checkActionsDuringCreate detaches one volume from a server whose create
// runs and attaches another; both end well, and the VM has the second.
func checkActionsDuringCreate(t *testing.T, test *testLab, creating createAnswer, detached, attached int64) {
	t.Helper()
	detach := actionOf(t, "detach during the create", volumeAction(test, detached, "detach", nil))
	attach := actionOf(t, "attach during the create", volumeAction(test, attached, "attach", map[string]any{"server": creating.Server.ID}))
	for _, action := range waitActions(t, test, creating.Action, detach, attach) {
		if action.Status != "success" {
			t.Errorf("%s ended %s: %+v", action.Command, action.Status, action.Error)
		}
	}
	disks := test.machine.Disks(lab.ServerVM(creating.Server.ID))
	found := slices.ContainsFunc(slices.Collect(maps.Values(disks)), func(path string) bool {
		return strings.HasSuffix(path, fmt.Sprintf("volumes/%d.vhdx", attached))
	})
	if len(disks) != 2 || !found {
		t.Errorf("the VM's disks %v, want the system disk and volume %d", disks, attached)
	}
}

// A failed resize, attach or detach takes back only what it set itself:
// an action queued behind keeps what it set (DEVELOPING.md, Actions).
func TestFailedVolumeActionsUndoWhatTheySet(t *testing.T) {
	test := newTestLab(t)
	quietMachine(t, test)
	server := offServer(t, test, "web")
	created := createVolume(t, test, map[string]any{"name": "data", "size": 10, "server": server.Server.ID})
	waitActions(t, test, created.Action, created.NextActions[0])
	id := created.Volume.ID
	broken := errors.New("broken")

	test.machine.FailNext("ResizeDisk", broken)
	expectFailed(t, test, actionOf(t, "resize", volumeAction(test, id, "resize", map[string]any{"size": 20})))
	if got := getVolume(t, test, id); got.Size != 10 {
		t.Errorf("after a failed resize the size is %d", got.Size)
	}

	// a failed resize with a detach behind: the size goes back, the detach
	// stays
	test.machine.FailNext("ResizeDisk", broken)
	test.machine.Delay("ResizeDisk", 100*time.Millisecond)
	resize := actionOf(t, "resize", volumeAction(test, id, "resize", map[string]any{"size": 20}))
	detach := actionOf(t, "detach behind", volumeAction(test, id, "detach", nil))
	waitActions(t, test, resize, detach)
	test.machine.Delay("ResizeDisk", 0)
	if got := getVolume(t, test, id); got.Size != 10 || got.Server != nil {
		t.Errorf("after a failed resize with a detach behind: size %d, server %v", got.Size, got.Server)
	}

	// a failed attach with a resize behind: the attach goes back, the
	// size stays
	test.machine.FailNext("AttachDisk", broken)
	test.machine.Delay("AttachDisk", 100*time.Millisecond)
	attach := actionOf(t, "attach", volumeAction(test, id, "attach", map[string]any{"server": server.Server.ID}))
	resize = actionOf(t, "resize behind", volumeAction(test, id, "resize", map[string]any{"size": 30}))
	ended := waitActions(t, test, attach, resize)
	test.machine.Delay("AttachDisk", 0)
	if got := getVolume(t, test, id); got.Server != nil || got.Size != 30 || ended[0].Status != "error" {
		t.Errorf("after a failed attach with a resize behind: server %v, size %d, %+v", got.Server, got.Size, ended)
	}

	// a failed detach gives the volume back to its server
	waitActions(t, test, actionOf(t, "attach", volumeAction(test, id, "attach", map[string]any{"server": server.Server.ID})))
	test.machine.FailNext("DetachDisk", broken)
	expectFailed(t, test, actionOf(t, "detach", volumeAction(test, id, "detach", nil)))
	if got := getVolume(t, test, id); got.Server == nil || *got.Server != server.Server.ID {
		t.Errorf("after a failed detach the volume has the server %v", got.Server)
	}
}

// waitForCall waits until the fake machine has had a call that starts
// with prefix.
func waitForCall(t *testing.T, test *testLab, prefix string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !containsPrefix(test.machine.Calls(), prefix) {
		if time.Now().After(deadline) {
			t.Fatalf("no call %s: %v", prefix, test.machine.Calls())
		}
		time.Sleep(time.Millisecond)
	}
}

// A server's create attaches its volumes before the VM is made and puts
// it into its placement group (measured 2026-11-09; the volumes' order
// among next_actions is assumed).
func TestServerCreateWithVolumesAndPlacementGroup(t *testing.T) {
	test := newTestLab(t)
	quietMachine(t, test)
	volume := createVolume(t, test, map[string]any{"name": "data", "size": 10, "location": "fsn1", "format": "ext4"})
	waitActions(t, test, volume.Action)
	group := createPlacementGroup(t, test, map[string]any{"name": "spread", "type": "spread"})
	created := createServer(t, test, map[string]any{"name": "web", "server_type": "cpx22", "image": "debian-13", "location": "fsn1",
		"volumes": []int64{volume.Volume.ID}, "automount": true, "placement_group": group.ID})
	if got := commands(created.NextActions); !slices.Equal(got, []string{"attach_volume volume,server", "start_server server"}) {
		t.Errorf("next_actions: %v", got)
	}
	if created.Server.PlacementGroup == nil || created.Server.PlacementGroup.ID != group.ID ||
		len(created.Server.Volumes) != 1 || created.Server.Volumes[0] != volume.Volume.ID {
		t.Errorf("the created server: placement group %+v, volumes %v", created.Server.PlacementGroup, created.Server.Volumes)
	}
	waitActions(t, test, append([]schema.Action{created.Action}, created.NextActions...)...)
	disks := test.machine.Disks(lab.ServerVM(created.Server.ID))
	if disks[1] == "" || test.machine.DiskID(disks[1]) != system.VolumeDiskID(volume.Volume.ID, true) {
		t.Errorf("the VM's disks %v", disks)
	}
	expectError(t, "a missing group", &responseOf{test.client.Do(http.MethodPost, "/v1/servers", map[string]any{
		"name": "db", "server_type": "cpx22", "image": "debian-13", "placement_group": 1})},
		http.StatusNotFound, "not_found", "placement group not found")
	elsewhere := createVolume(t, test, map[string]any{"name": "elsewhere", "size": 10, "location": "nbg1"})
	waitActions(t, test, elsewhere.Action)
	for name, c := range map[string]struct {
		volume  int64
		message string
	}{
		"a missing volume":             {1, "volume not found"},
		"an attached volume":           {volume.Volume.ID, "volume already attached"},
		"a volume in another location": {elsewhere.Volume.ID, "volume in invalid location"},
	} {
		expectError(t, name, &responseOf{test.client.Do(http.MethodPost, "/v1/servers", map[string]any{
			"name": "db", "server_type": "cpx22", "image": "debian-13", "location": "fsn1", "volumes": []int64{c.volume}})},
			http.StatusUnprocessableEntity, "invalid_input", c.message)
	}
}

// containsPrefix tells whether one of the calls starts with prefix.
func containsPrefix(calls []string, prefix string) bool {
	return indexOfPrefix(calls, prefix) >= 0
}

// indexOfPrefix returns the index of the last call that starts with
// prefix, -1 for none.
func indexOfPrefix(calls []string, prefix string) int {
	for i := len(calls) - 1; i >= 0; i-- {
		if strings.HasPrefix(calls[i], prefix) {
			return i
		}
	}
	return -1
}
