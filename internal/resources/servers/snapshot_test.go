package servers_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	diskfs "github.com/diskfs/go-diskfs"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	imagesres "github.com/webcodingcat/hlab/internal/resources/images"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/sshkeys"
	"github.com/webcodingcat/hlab/internal/store"
)

// testSnapshots holds the rows of snapshot_test.go; TestServers runs them.
func testSnapshots(t *testing.T) {
	t.Run("SRV-12", func(t *testing.T) {
		p := newRecPlatform()
		env, _, deps := newImageEnv(t, p, nil)
		src := createdServer(t, env, "snap-src", map[string]any{"start_after_create": true})
		srcDisk := p.lastCreate(t).path
		vm := vmName(t, p.Fake)

		release := block(t, p.Fake, "Snapshot")
		img, act := startCreateImage(t, env, src, map[string]any{"description": "first snapshot", "labels": map[string]string{"k": "v"}})
		if img.Status != "creating" {
			t.Errorf("create_image response image status %q, want creating", img.Status)
		}
		if a := env.GetAction(act); a.Status != "running" || a.Command != "create_image" {
			t.Errorf("create_image response action %q %q, want create_image running", a.Command, a.Status)
		}
		expectLocked(t, env, src, "create_image")
		release()
		if a := env.WaitAction(act); a.Status != "success" {
			t.Fatalf("create_image action: %q %+v, want success", a.Status, a.Error)
		}
		snap := p.lastSnapshot(t)
		if snap.vm != vm || snap.disk != srcDisk {
			t.Errorf("Snapshot of VM %q disk %q, want VM %q disk %q", snap.vm, snap.disk, vm, srcDisk)
		}
		got := getImage(t, env, img.ID)
		if got.Status != "available" || got.Type != "snapshot" {
			t.Errorf("image after create_image: status %q type %q, want available snapshot", got.Status, got.Type)
		}
		if got.DiskSize != 40 {
			t.Errorf("image disk_size %v, want 40 (cx23)", got.DiskSize)
		}
		if got.ImageSize == nil {
			t.Error("image image_size is null, want set")
		}
		if got.CreatedFrom == nil || got.CreatedFrom.ID != src || got.CreatedFrom.Name != "snap-src" {
			t.Errorf("image created_from %+v, want {%d snap-src}", got.CreatedFrom, src)
		}
		if got.BoundTo != nil {
			t.Errorf("snapshot bound_to %d, want null", *got.BoundTo)
		}
		path := imagePath(t, env, img.ID)
		if !sameFile(path, snap.target) || !fileExists(path) {
			t.Errorf("image file %s (snapshot target %s) missing", path, snap.target)
		}

		p.Fake.FailNext("Snapshot", &platform.Error{Kind: platform.Failed, Op: "Snapshot", Err: errors.New("injected failure")})
		failed, act := startCreateImage(t, env, src, nil)
		if a := env.WaitAction(act); a.Status != "error" {
			t.Errorf("create_image with a failing Snapshot: action %q, want error", a.Status)
		}
		if resp := env.Do(http.MethodGet, fmt.Sprintf("/images/%d", failed.ID), nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET /images/%d after the failed create_image: status %d, want 404", failed.ID, resp.StatusCode)
		}
		if target := p.lastSnapshot(t).target; fileExists(target) {
			t.Errorf("file %s of the failed create_image exists", target)
		}

		copyID := createdServer(t, env, "from-snap", map[string]any{"image": fmt.Sprint(img.ID)})
		if c := p.lastCreate(t); !sameFile(c.from, path) {
			t.Errorf("disk of the server created from the snapshot copied from %q, want %q", c.from, path)
		}
		ids := instanceIDs(t, deps.Settings.DataFolder)
		if len(ids) != 2 || ids[0] == ids[1] {
			t.Errorf("instance-ids of the source and the copy %v, want two distinct", ids)
		}

		runAction(t, env, copyID, "rebuild", map[string]any{"image": fmt.Sprint(img.ID)})
		if c := p.lastCreate(t); !sameFile(c.from, path) {
			t.Errorf("rebuild from the snapshot copied the disk from %q, want %q", c.from, path)
		}

		big := createdServer(t, env, "snap-big", map[string]any{"server_type": "cx33"})
		bigImg, act := startCreateImage(t, env, big, nil)
		if a := env.WaitAction(act); a.Status != "success" {
			t.Fatalf("create_image of the cx33 server: %q %+v, want success", a.Status, a.Error)
		}
		wantError(t, env.Do(http.MethodPost, "/servers", map[string]any{
			"name": "too-small", "server_type": "cx23", "image": fmt.Sprint(bigImg.ID), "location": "fsn1",
		}), http.StatusUnprocessableEntity, "invalid_input", "POST /servers cx23 from an 80 GB snapshot")
		wantError(t, postAction(env, copyID, "rebuild", map[string]any{"image": fmt.Sprint(bigImg.ID)}),
			http.StatusUnprocessableEntity, "invalid_input", "rebuild of a cx23 server from an 80 GB snapshot")

		deleteServer(t, env, src)
		if resp := env.Do(http.MethodGet, fmt.Sprintf("/images/%d", img.ID), nil); resp.StatusCode != http.StatusOK {
			t.Errorf("GET /images/%d after deleting its server: status %d, want 200", img.ID, resp.StatusCode)
		}
		if !fileExists(path) {
			t.Errorf("snapshot file %s gone after deleting its server", path)
		}
	})

	t.Run("SRV-18/create_image", func(t *testing.T) {
		p := newRecPlatform()
		p.diskFileBytes = 1 << 20
		env, _, _ := newImageEnv(t, p, nil)
		id := createdServer(t, env, "snap-limit", nil)
		nominal := p.lastCreate(t).size

		// The server's remaining growth (nominal minus file) fits; adding a
		// copy of the disk file does not.
		r := roomy
		r.FreeDiskBytes = int64(reserveDiskGB)*(nominal/40) + nominal - 1
		p.SetResources(r)
		images0 := len(listImages(t, env, "type=snapshot"))
		wantError(t, postAction(env, id, "create_image", map[string]any{"type": "snapshot"}),
			http.StatusPreconditionFailed, "resource_unavailable", "create_image with the disk file above the free disk")
		if n := len(listImages(t, env, "type=snapshot")); n != images0 {
			t.Errorf("GET /images?type=snapshot lists %d images after the refused create_image, want %d", n, images0)
		}
		if n := len(callsOf(p.Fake, "Snapshot")); n != 0 {
			t.Errorf("Snapshot calls after the refused create_image: %d, want none", n)
		}
	})
}

// recPlatform is the fake platform that also keeps the arguments the fake
// does not record (disk source, LUN, DVD slot, child disk path) and writes
// the files of disks and snapshots, so file checks see them.
type recPlatform struct {
	*platformtest.Fake
	// diskFileBytes is the size of the file CreateDisk writes; 0 writes none.
	diskFileBytes int64

	mu        sync.Mutex
	creates   []diskCreate
	snapshots []snapshotCall
	attaches  []diskAttach
	dvds      []dvdSet
	children  []childDisk
}

type diskCreate struct {
	path, from string
	size       int64
}

type snapshotCall struct{ vm, disk, target string }

type diskAttach struct {
	vm, path string
	lun      int
}

type dvdSet struct {
	vm   string
	slot int
	iso  string
	seq  int // index in the fake's call log
}

type childDisk struct{ path, parent string }

func newRecPlatform() *recPlatform { return &recPlatform{Fake: platformtest.NewFake()} }

func (r *recPlatform) CreateDisk(ctx context.Context, path string, sizeBytes int64, from string, page83 [16]byte) error {
	if err := r.Fake.CreateDisk(ctx, path, sizeBytes, from, page83); err != nil {
		return err
	}
	r.mu.Lock()
	r.creates = append(r.creates, diskCreate{path: path, from: from, size: sizeBytes})
	r.mu.Unlock()
	if r.diskFileBytes > 0 {
		return writeSized(path, r.diskFileBytes)
	}
	return nil
}

func (r *recPlatform) Snapshot(ctx context.Context, vm, diskPath, target string) error {
	r.mu.Lock()
	r.snapshots = append(r.snapshots, snapshotCall{vm: vm, disk: diskPath, target: target})
	r.mu.Unlock()
	if err := r.Fake.Snapshot(ctx, vm, diskPath, target); err != nil {
		return err
	}
	return writeSized(target, 1<<20)
}

func (r *recPlatform) AttachDisk(ctx context.Context, vm, path string, lun int) error {
	r.mu.Lock()
	r.attaches = append(r.attaches, diskAttach{vm: vm, path: path, lun: lun})
	r.mu.Unlock()
	return r.Fake.AttachDisk(ctx, vm, path, lun)
}

func (r *recPlatform) SetDVD(ctx context.Context, vm string, slot int, iso string) error {
	r.mu.Lock()
	r.dvds = append(r.dvds, dvdSet{vm: vm, slot: slot, iso: iso, seq: len(r.Fake.Calls())})
	r.mu.Unlock()
	return r.Fake.SetDVD(ctx, vm, slot, iso)
}

func (r *recPlatform) CreateChildDisk(ctx context.Context, path, parent string) error {
	r.mu.Lock()
	r.children = append(r.children, childDisk{path: path, parent: parent})
	r.mu.Unlock()
	return r.Fake.CreateChildDisk(ctx, path, parent)
}

func (r *recPlatform) lastCreate(t *testing.T) diskCreate {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.creates) == 0 {
		t.Fatal("no CreateDisk call on the platform")
	}
	return r.creates[len(r.creates)-1]
}

func (r *recPlatform) lastSnapshot(t *testing.T) snapshotCall {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.snapshots) == 0 {
		t.Fatal("no Snapshot call on the platform")
	}
	return r.snapshots[len(r.snapshots)-1]
}

// dvdSets returns the SetDVD calls of slot so far.
func (r *recPlatform) dvdSets(slot int) []dvdSet {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []dvdSet
	for _, d := range r.dvds {
		if d.slot == slot {
			out = append(out, d)
		}
	}
	return out
}

func (r *recPlatform) lastDVD(t *testing.T, slot int) dvdSet {
	t.Helper()
	d := r.dvdSets(slot)
	if len(d) == 0 {
		t.Fatalf("no SetDVD call for slot %d", slot)
	}
	return d[len(d)-1]
}

func (r *recPlatform) attachesAt(lun int) []diskAttach {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []diskAttach
	for _, a := range r.attaches {
		if a.lun == lun {
			out = append(out, a)
		}
	}
	return out
}

func (r *recPlatform) childDisks() []childDisk {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]childDisk(nil), r.children...)
}

// writeSized writes a file of size bytes at path, creating its folder.
func writeSized(path string, size int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sameFile(a, b string) bool {
	return a != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// newImageEnv is newFakeEnv with the images module (catalog and project
// images) mounted as well.
func newImageEnv(t *testing.T, p platform.Platform, tune func(*servers.Deps)) (*apitest.Env, *servers.Module, servers.Deps) {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	d := apitest.ServersDeps(t, cat, p)
	d.Settings.ReserveRAMMB = reserveRAMMB
	d.Settings.ReserveDiskGB = reserveDiskGB
	if tune != nil {
		tune(&d)
	}
	mod := servers.New(d)
	imgs := imagesres.NewModule(imagesres.Deps{Catalog: cat, DataFolder: d.Settings.DataFolder})
	return apitest.New(t, sshkeys.New(), mod, resactions.New(), imgs), mod, d
}

// imageView is the part of an image object the tests check.
type imageView struct {
	ID          int64             `json:"id"`
	Type        string            `json:"type"`
	Status      string            `json:"status"`
	Description string            `json:"description"`
	DiskSize    float64           `json:"disk_size"`
	ImageSize   *float64          `json:"image_size"`
	BoundTo     *int64            `json:"bound_to"`
	Labels      map[string]string `json:"labels"`
	CreatedFrom *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"created_from"`
}

// startCreateImage posts create_image, expects 201 and returns the image
// and the action ID of the response.
func startCreateImage(t *testing.T, env *apitest.Env, id int64, body map[string]any) (imageView, int64) {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	resp := postAction(env, id, "create_image", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers/%d/actions/create_image %v: status %d (%s), want 201", id, body, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Image  imageView      `json:"image"`
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	return b.Image, b.Action.ID
}

// createImage runs create_image, waits for its success and returns the
// image ID.
func createImage(t *testing.T, env *apitest.Env, id int64, body map[string]any) int64 {
	t.Helper()
	img, act := startCreateImage(t, env, id, body)
	if a := env.WaitAction(act); a.Status != "success" {
		t.Fatalf("create_image %v: action %q %+v, want success", body, a.Status, a.Error)
	}
	return img.ID
}

func getImage(t *testing.T, env *apitest.Env, id int64) imageView {
	t.Helper()
	resp := env.Do(http.MethodGet, fmt.Sprintf("/images/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /images/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		Image imageView `json:"image"`
	}
	apitest.Decode(t, resp, &b)
	return b.Image
}

// listImages lists the images matching query (without "?").
func listImages(t *testing.T, env *apitest.Env, query string) []imageView {
	t.Helper()
	resp := env.Do(http.MethodGet, "/images?per_page=50&"+query, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /images?%s: status %d, want 200", query, resp.StatusCode)
	}
	var b struct {
		Images []imageView `json:"images"`
	}
	apitest.Decode(t, resp, &b)
	return b.Images
}

// imagePath is the disk file of a project image as its record holds it.
func imagePath(t *testing.T, env *apitest.Env, id int64) string {
	t.Helper()
	var rec imagesres.Record
	err := env.Store.Tx(func(tx store.Tx) error {
		var err error
		rec, err = imagesres.Load(tx, env.Project.ID, id)
		return err
	})
	if err != nil {
		t.Fatalf("load image record %d: %v", id, err)
	}
	return rec.Hlab.Path
}

// isoFiles reads the cloud-init files present in the ISO at path.
func isoFiles(t *testing.T, path string) map[string][]byte {
	t.Helper()
	d, err := diskfs.Open(path, diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = d.Close() }()
	fsys, err := d.GetFilesystem(0)
	if err != nil {
		t.Fatalf("read filesystem of %s: %v", path, err)
	}
	out := map[string][]byte{}
	for _, name := range []string{"meta-data", "user-data", "vendor-data", "network-config"} {
		f, err := fsys.OpenFile("/"+name, os.O_RDONLY)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("read %s in %s: %v", name, path, err)
		}
		out[name] = data
	}
	return out
}

// The seed writes meta-data as JSON, which is YAML too; both forms match.
var instanceIDRe = regexp.MustCompile(`(?m)^\s*"?instance-id"?:\s*"?([^",\s]+)"?,?\s*$`)

// instanceID is the instance-id of a meta-data file.
func instanceID(t *testing.T, metaData []byte) string {
	t.Helper()
	m := instanceIDRe.FindSubmatch(metaData)
	if m == nil {
		t.Fatalf("meta-data without instance-id:\n%s", metaData)
	}
	return string(m[1])
}

// instanceIDs are the instance-ids of every seed ISO in the data folder.
func instanceIDs(t *testing.T, dataFolder string) []string {
	t.Helper()
	var ids []string
	err := filepath.WalkDir(dataFolder, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".iso") {
			if md, ok := isoFiles(t, path)["meta-data"]; ok {
				ids = append(ids, instanceID(t, md))
			}
		}
		return err
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dataFolder, err)
	}
	return ids
}
