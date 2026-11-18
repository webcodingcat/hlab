package volumes_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/volumes"
	"github.com/webcodingcat/hlab/internal/store"
)

const (
	gib           = int64(1) << 30
	reserveDiskGB = 10
)

// plenty is a host with room for the largest volume.
var plenty = platform.Resources{MemoryMB: 64 << 10, FreeMemoryMB: 64 << 10, LogicalCPUs: 16, FreeDiskBytes: 1 << 50}

// recPlatform is the fake platform that also records the identifier of
// every created disk, writes an empty file for it, and records the LUN of
// every attach.
type recPlatform struct {
	*platformtest.Fake
	mu     sync.Mutex
	page83 map[string][16]byte // disk path -> identifier
	luns   map[string]int      // disk path -> LUN of its last attach
}

func (p *recPlatform) CreateDisk(ctx context.Context, path string, sizeBytes int64, from string, page83 [16]byte) error {
	if err := p.Fake.CreateDisk(ctx, path, sizeBytes, from, page83); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.page83[path] = page83
	return nil
}

func (p *recPlatform) AttachDisk(ctx context.Context, name, path string, lun int) error {
	if err := p.Fake.AttachDisk(ctx, name, path, lun); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.luns[path] = lun
	return nil
}

// jobRecorder is the ApplianceJob dependency; while gate is set, a job
// signals entered and waits for gate to close.
type jobRecorder struct {
	mu      sync.Mutex
	jobs    []recordedJob
	gate    chan struct{}
	entered chan struct{}
}

type recordedJob struct {
	DiskPath string
	Job      agentapi.Job
}

func (r *jobRecorder) run(ctx context.Context, diskPath string, job agentapi.Job) error {
	r.mu.Lock()
	r.jobs = append(r.jobs, recordedJob{diskPath, job})
	gate, entered := r.gate, r.entered
	r.mu.Unlock()
	if gate != nil {
		close(entered)
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (r *jobRecorder) recorded() []recordedJob {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.jobs)
}

type volEnv struct {
	*apitest.Env
	fake *recPlatform
	jobs *jobRecorder
}

// newVolEnv mounts the servers and volumes modules wired like the cloud
// does (hooks, disk rule with the volume disks) on the shipped catalog and
// a fake platform with plenty of disk.
func newVolEnv(t *testing.T) *volEnv {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	fake := &recPlatform{Fake: platformtest.NewFake(), page83: map[string][16]byte{}, luns: map[string]int{}}
	fake.SetResources(plenty)
	jobs := &jobRecorder{}
	d := apitest.ServersDeps(t, cat, fake)
	d.Settings.ReserveDiskGB = reserveDiskGB
	d.ExtraDisks = func(tx store.Tx) ([]servers.DiskUse, error) {
		disks, err := volumes.Disks(tx)
		if err != nil {
			return nil, err
		}
		var out []servers.DiskUse
		for _, disk := range disks {
			out = append(out, servers.DiskUse{Path: disk.Path, NominalBytes: disk.NominalBytes})
		}
		return out, nil
	}
	srv := servers.New(d)
	vol := volumes.New(volumes.Deps{
		Catalog:      cat,
		Store:        d.Store,
		Platform:     fake,
		DataFolder:   d.Settings.DataFolder,
		VMPrefix:     d.Settings.VMPrefix,
		CheckDisk:    srv.CheckDisk,
		ApplianceJob: jobs.run,
		PushAgent:    func(context.Context) error { return nil },
	})
	srv.SetVolumes(vol)
	return &volEnv{Env: apitest.New(t, srv, vol, resactions.New()), fake: fake, jobs: jobs}
}

// wantPage83 is 484C4142-0000-4000-8000-<id as 12 hex digits> in text
// byte order.
func wantPage83(t *testing.T, id int64) [16]byte {
	t.Helper()
	raw, err := hex.DecodeString(fmt.Sprintf("484C4142000040008000%012x", id))
	if err != nil {
		t.Fatal(err)
	}
	var b [16]byte
	copy(b[:], raw)
	return b
}

// diskOf is the path of the disk created with the identifier of volume id.
func (e *volEnv) diskOf(t *testing.T, id int64) string {
	t.Helper()
	want := wantPage83(t, id)
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	for path, p := range e.fake.page83 {
		if p == want {
			return path
		}
	}
	t.Fatalf("no disk created with identifier %X for volume %d", want, id)
	return ""
}

func (e *volEnv) lunOf(path string) int {
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	return e.fake.luns[path]
}

type volumeView struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Size        int64   `json:"size"`
	Status      string  `json:"status"`
	LinuxDevice string  `json:"linux_device"`
	Format      *string `json:"format"`
	Server      *int64  `json:"server"`
	Assignee    *struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"assignee"`
	Location struct {
		Name string `json:"name"`
	} `json:"location"`
}

// startVolume posts a volume create, expects 201 and returns the volume
// and its create action.
func startVolume(t *testing.T, e *volEnv, body map[string]any) (int64, int64) {
	t.Helper()
	resp := e.Do(http.MethodPost, "/volumes", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /volumes %v: status %d (%s), want 201", body, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Volume volumeView     `json:"volume"`
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	return b.Volume.ID, b.Action.ID
}

// createVolume creates a detached volume in fsn1 and waits for it.
func createVolume(t *testing.T, e *volEnv, name string, size int) int64 {
	t.Helper()
	id, act := startVolume(t, e, map[string]any{"name": name, "size": size, "location": "fsn1"})
	e.waitSuccess(t, act, "create volume "+name)
	return id
}

func (e *volEnv) waitSuccess(t *testing.T, act int64, what string) {
	t.Helper()
	if a := e.WaitAction(act); a.Status != "success" {
		t.Fatalf("%s: action %q %+v, want success", what, a.Status, a.Error)
	}
}

func getVolume(t *testing.T, e *volEnv, id int64) volumeView {
	t.Helper()
	resp := e.Do(http.MethodGet, fmt.Sprintf("/volumes/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /volumes/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		Volume volumeView `json:"volume"`
	}
	apitest.Decode(t, resp, &b)
	return b.Volume
}

func countVolumes(t *testing.T, e *volEnv) int {
	t.Helper()
	resp := e.Do(http.MethodGet, "/volumes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /volumes: status %d, want 200", resp.StatusCode)
	}
	var b struct {
		Volumes []volumeView `json:"volumes"`
	}
	apitest.Decode(t, resp, &b)
	return len(b.Volumes)
}

// createServer creates a cx23 server in location and waits for it.
func createServer(t *testing.T, e *volEnv, name, location string, extra map[string]any) int64 {
	t.Helper()
	req := map[string]any{"name": name, "server_type": "cx23", "image": "ubuntu-24.04", "location": location}
	for k, v := range extra {
		req[k] = v
	}
	resp := e.Do(http.MethodPost, "/servers", req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers %s: status %d (%s), want 201", name, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Server struct {
			ID int64 `json:"id"`
		} `json:"server"`
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	e.waitSuccess(t, b.Action.ID, "create server "+name)
	return b.Server.ID
}

func serverVolumes(t *testing.T, e *volEnv, id int64) []int64 {
	t.Helper()
	resp := e.Do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /servers/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		Server struct {
			Volumes []int64 `json:"volumes"`
		} `json:"server"`
	}
	apitest.Decode(t, resp, &b)
	slices.Sort(b.Server.Volumes)
	return b.Server.Volumes
}

func volumeAction(e *volEnv, id int64, name string, body any) *http.Response {
	return e.Do(http.MethodPost, fmt.Sprintf("/volumes/%d/actions/%s", id, name), body)
}

// startVolumeAction posts the action, expects 201 and returns its ID.
func startVolumeAction(t *testing.T, e *volEnv, id int64, name string, body any) int64 {
	t.Helper()
	resp := volumeAction(e, id, name, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /volumes/%d/actions/%s %v: status %d (%s), want 201", id, name, body, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	return b.Action.ID
}

func runVolumeAction(t *testing.T, e *volEnv, id int64, name string, body any) {
	t.Helper()
	e.waitSuccess(t, startVolumeAction(t, e, id, name, body), fmt.Sprintf("%s volume %d %v", name, id, body))
}

func attachTo(server int64) map[string]any {
	return map[string]any{"assignee": map[string]any{"id": server, "type": "server"}}
}

// wantError checks status and error.code of resp.
func wantError(t *testing.T, resp *http.Response, status int, code, what string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Errorf("%s: status %d, want %d", what, resp.StatusCode, status)
	}
	if got := apitest.ErrorCode(t, resp); got != code {
		t.Errorf("%s: error.code %q, want %s", what, got, code)
	}
}

// wantFieldError checks a 422 invalid_input naming field in
// error.details.fields.
func wantFieldError(t *testing.T, resp *http.Response, field, what string) {
	t.Helper()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("%s: status %d, want 422", what, resp.StatusCode)
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
	var names []string
	for _, f := range body.Error.Details.Fields {
		names = append(names, f.Name)
	}
	if body.Error.Code != "invalid_input" || !slices.Contains(names, field) {
		t.Errorf("%s: error %q on fields %v, want invalid_input on %s", what, body.Error.Code, names, field)
	}
}

// wantLocked checks that change_protection on the volume and on the
// server answers 423 locked.
func wantLocked(t *testing.T, e *volEnv, volume, server int64, during string) {
	t.Helper()
	wantError(t, volumeAction(e, volume, "change_protection", map[string]any{"delete": false}),
		http.StatusLocked, "locked", "volume change_protection during "+during)
	wantError(t, e.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/change_protection", server),
		map[string]any{"delete": false, "rebuild": false}),
		http.StatusLocked, "locked", "server change_protection during "+during)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sizeCalls(t *testing.T, fake *recPlatform, op string) []int64 {
	t.Helper()
	var out []int64
	for _, c := range fake.Calls() {
		if c.Op != op {
			continue
		}
		v, ok := c.Arg.(int64)
		if !ok {
			t.Fatalf("%s call argument %T, want the size in bytes", op, c.Arg)
		}
		out = append(out, v)
	}
	return out
}

func TestVolumes(t *testing.T) {
	t.Run("VOL-01", func(t *testing.T) {
		e := newVolEnv(t)
		fsn := createServer(t, e, "in-fsn1", "fsn1", nil)

		for _, c := range []struct {
			what  string
			body  map[string]any
			field string
		}{
			{"size 9", map[string]any{"size": 9, "location": "fsn1"}, "size"},
			{"size 10241", map[string]any{"size": 10241, "location": "fsn1"}, "size"},
			{"size 10.5", map[string]any{"size": 10.5, "location": "fsn1"}, "size"},
			{"unknown location", map[string]any{"size": 10, "location": "xyz9"}, "location"},
			{"automount without server", map[string]any{"size": 10, "location": "fsn1", "automount": true}, "automount"},
			{"format btrfs", map[string]any{"size": 10, "location": "fsn1", "format": "btrfs"}, "format"},
			{"location differs from the server's", map[string]any{"size": 10, "location": "nbg1", "server": fsn}, "location"},
		} {
			c.body["name"] = "bad"
			wantFieldError(t, e.Do(http.MethodPost, "/volumes", c.body), c.field, "POST /volumes "+c.what)
		}

		small := createVolume(t, e, "small", 10)
		v := getVolume(t, e, small)
		if v.Status != "available" || v.Size != 10 {
			t.Errorf("volume small = %+v, want available with size 10", v)
		}
		if want := fmt.Sprintf("/dev/disk/by-id/scsi-0HC_Volume_%d", small); v.LinuxDevice != want {
			t.Errorf("linux_device = %q, want %q", v.LinuxDevice, want)
		}
		smallPath := e.diskOf(t, small)
		if sizes := sizeCalls(t, e.fake, "CreateDisk"); !slices.Contains(sizes, 10*gib) {
			t.Errorf("CreateDisk sizes %v, want one of %d (10 GB nominal)", sizes, 10*gib)
		}
		if len(e.jobs.recorded()) != 0 {
			t.Errorf("appliance jobs for a volume without format: %v, want none", e.jobs.recorded())
		}

		createVolume(t, e, "big", 10240)
		if sizes := sizeCalls(t, e.fake, "CreateDisk"); !slices.Contains(sizes, 10240*gib) {
			t.Errorf("CreateDisk sizes %v, want one of %d (10240 GB nominal)", sizes, 10240*gib)
		}

		// With format, mkfs runs before the volume is available.
		gate := make(chan struct{})
		entered := make(chan struct{})
		e.jobs.mu.Lock()
		e.jobs.gate, e.jobs.entered = gate, entered
		e.jobs.mu.Unlock()
		t.Cleanup(func() {
			select {
			case <-gate:
			default:
				close(gate)
			}
		})
		formatted, act := startVolume(t, e, map[string]any{"name": "formatted", "size": 10, "location": "fsn1", "format": "xfs"})
		select {
		case <-entered:
		case <-time.After(apitest.ActionTimeout):
			t.Fatalf("no appliance job within %s for a volume with format", apitest.ActionTimeout)
		}
		if v := getVolume(t, e, formatted); v.Status == "available" {
			t.Errorf("volume formatted is available while mkfs runs")
		}
		close(gate)
		e.waitSuccess(t, act, "create volume formatted")
		if v := getVolume(t, e, formatted); v.Status != "available" || v.Format == nil || *v.Format != "xfs" {
			t.Errorf("volume formatted after mkfs = %+v, want available with format xfs", v)
		}
		jobs := e.jobs.recorded()
		if len(jobs) != 1 || jobs[0].Job.Kind != "mkfs" || jobs[0].Job.FS != "xfs" || jobs[0].DiskPath != e.diskOf(t, formatted) {
			t.Errorf("appliance jobs = %+v, want one mkfs xfs on the volume's disk", jobs)
		}

		// Resize grows only.
		runVolumeAction(t, e, small, "resize", map[string]any{"size": 20})
		if sizes := sizeCalls(t, e.fake, "ResizeDisk"); !slices.Equal(sizes, []int64{20 * gib}) {
			t.Errorf("ResizeDisk sizes %v, want [%d]", sizes, 20*gib)
		}
		if v := getVolume(t, e, small); v.Size != 20 {
			t.Errorf("size after resize = %d, want 20", v.Size)
		}
		wantFieldError(t, volumeAction(e, small, "resize", map[string]any{"size": 15}), "size", "resize to a smaller size")

		// Delete of a detached volume removes record and file.
		resp := e.Do(http.MethodDelete, fmt.Sprintf("/volumes/%d", small), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("DELETE /volumes/%d: status %d, want 204", small, resp.StatusCode)
		}
		if resp := e.Do(http.MethodGet, fmt.Sprintf("/volumes/%d", small), nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET /volumes/%d after delete: status %d, want 404", small, resp.StatusCode)
		}
		if exists(smallPath) {
			t.Errorf("disk %s still exists after delete", smallPath)
		}

		// Delete of an attached volume is refused and keeps both.
		runVolumeAction(t, e, formatted, "attach", attachTo(fsn))
		resp = e.Do(http.MethodDelete, fmt.Sprintf("/volumes/%d", formatted), nil)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("DELETE attached volume: status %d, want 422", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "invalid_input" {
			t.Errorf("DELETE attached volume: error.code %q, want invalid_input", code)
		}
		getVolume(t, e, formatted)
		if p := e.diskOf(t, formatted); !exists(p) {
			t.Errorf("disk %s removed by a refused delete", p)
		}
	})

	t.Run("SRV-18", func(t *testing.T) {
		e := newVolEnv(t)
		tight := plenty
		tight.FreeDiskBytes = reserveDiskGB*gib + 5*gib

		e.fake.SetResources(tight)
		wantError(t, e.Do(http.MethodPost, "/volumes", map[string]any{"name": "toobig", "size": 10, "location": "fsn1"}),
			http.StatusPreconditionFailed, "resource_unavailable", "volume create beyond free disk")
		if n := countVolumes(t, e); n != 0 {
			t.Errorf("volumes after refused create = %d, want 0", n)
		}

		e.fake.SetResources(plenty)
		id := createVolume(t, e, "grow", 10)
		e.fake.SetResources(tight)
		wantError(t, volumeAction(e, id, "resize", map[string]any{"size": 20}),
			http.StatusPreconditionFailed, "resource_unavailable", "volume resize beyond free disk")
		if v := getVolume(t, e, id); v.Size != 10 {
			t.Errorf("size after refused resize = %d, want 10", v.Size)
		}
		if sizes := sizeCalls(t, e.fake, "ResizeDisk"); len(sizes) != 0 {
			t.Errorf("ResizeDisk calls after refused resize: %v, want none", sizes)
		}
	})

	t.Run("VOL-05", func(t *testing.T) {
		e := newVolEnv(t)
		a := createServer(t, e, "a", "fsn1", nil)
		b := createServer(t, e, "b", "fsn1", nil)
		other := createServer(t, e, "other", "nbg1", nil)
		v1 := createVolume(t, e, "v1", 10)
		v2 := createVolume(t, e, "v2", 10)
		v3 := createVolume(t, e, "v3", 10)

		// Attach by assignee; the action locks volume and server.
		release := e.fake.Block("AttachDisk")
		t.Cleanup(sync.OnceFunc(release))
		act := startVolumeAction(t, e, v1, "attach", attachTo(a))
		wantLocked(t, e, v1, a, "attach")
		release()
		e.waitSuccess(t, act, "attach v1 to a")
		v := getVolume(t, e, v1)
		if v.Server == nil || *v.Server != a || v.Assignee == nil || v.Assignee.ID != a || v.Assignee.Type != "server" {
			t.Errorf("v1 after attach = %+v, want server %d and assignee {%d server}", v, a, a)
		}

		// Attach by the deprecated server field.
		runVolumeAction(t, e, v2, "attach", map[string]any{"server": a})
		if v := getVolume(t, e, v2); v.Server == nil || *v.Server != a || v.Assignee == nil || v.Assignee.ID != a {
			t.Errorf("v2 after attach by server = %+v, want server and assignee %d", v, a)
		}
		if got := serverVolumes(t, e, a); !slices.Equal(got, []int64{v1, v2}) {
			t.Errorf("server a volumes = %v, want [%d %d]", got, v1, v2)
		}
		if l1, l2 := e.lunOf(e.diskOf(t, v1)), e.lunOf(e.diskOf(t, v2)); l1 != 1 || l2 != 2 {
			t.Errorf("LUNs of v1, v2 = %d, %d, want 1, 2", l1, l2)
		}

		wantError(t, volumeAction(e, v1, "attach", attachTo(b)),
			http.StatusUnprocessableEntity, "volume_already_attached", "attach v1 to b while attached to a")
		wantError(t, volumeAction(e, v3, "attach", attachTo(other)),
			http.StatusUnprocessableEntity, "invalid_input", "attach to a server in another location")

		// Detach locks too and frees the LUN.
		release = e.fake.Block("DetachDisk")
		t.Cleanup(sync.OnceFunc(release))
		act = startVolumeAction(t, e, v1, "detach", nil)
		wantLocked(t, e, v1, a, "detach")
		release()
		e.waitSuccess(t, act, "detach v1")
		if v := getVolume(t, e, v1); v.Server != nil || v.Assignee != nil {
			t.Errorf("v1 after detach = %+v, want server and assignee null", v)
		}
		if got := serverVolumes(t, e, a); !slices.Equal(got, []int64{v2}) {
			t.Errorf("server a volumes after detach = %v, want [%d]", got, v2)
		}

		runVolumeAction(t, e, v3, "attach", attachTo(a))
		if l := e.lunOf(e.diskOf(t, v3)); l != 1 {
			t.Errorf("LUN of v3 after v1 left LUN 1 = %d, want 1", l)
		}
		runVolumeAction(t, e, v1, "attach", attachTo(b))
		if v := getVolume(t, e, v1); v.Server == nil || *v.Server != b {
			t.Errorf("v1 after attach to b = %+v, want server %d", v, b)
		}
	})

	t.Run("VOL-02", func(t *testing.T) {
		e := newVolEnv(t)

		// Deleting a server detaches its volumes and keeps them.
		s := createServer(t, e, "doomed", "fsn1", nil)
		kept := createVolume(t, e, "kept", 10)
		runVolumeAction(t, e, kept, "attach", attachTo(s))
		resp := e.Do(http.MethodDelete, fmt.Sprintf("/servers/%d", s), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("DELETE /servers/%d: status %d, want 200", s, resp.StatusCode)
		}
		var del struct {
			Action apitest.Action `json:"action"`
		}
		apitest.Decode(t, resp, &del)
		e.waitSuccess(t, del.Action.ID, "delete server")
		if v := getVolume(t, e, kept); v.Server != nil || v.Assignee != nil {
			t.Errorf("volume after server delete = %+v, want server and assignee null", v)
		}
		if p := e.diskOf(t, kept); !exists(p) {
			t.Errorf("volume disk %s removed with its server", p)
		}

		// A server created with volumes attaches them after its VM exists.
		w := createVolume(t, e, "with-server", 10)
		srv := createServer(t, e, "with-volume", "fsn1", map[string]any{"volumes": []int64{w}})
		deadline := time.Now().Add(apitest.ActionTimeout)
		for {
			v := getVolume(t, e, w)
			if v.Server != nil && *v.Server == srv {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("volume %d not attached to server %d within %s: %+v", w, srv, apitest.ActionTimeout, v)
			}
			time.Sleep(10 * time.Millisecond)
		}
		path := e.diskOf(t, w)
		createVM, attach := -1, -1
		for i, c := range e.fake.Calls() {
			switch {
			case c.Op == "CreateVM":
				createVM = i
			case c.Op == "AttachDisk" && fmt.Sprint(c.Arg) == path && attach < 0:
				attach = i
			}
		}
		if attach < 0 || attach < createVM {
			t.Errorf("AttachDisk of %s at call %d, last CreateVM at call %d: want the attach after the VM exists", path, attach, createVM)
		}
	})
}
